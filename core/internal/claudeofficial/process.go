package claudeofficial

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/claudecli"
)

// isolationEnv keeps a serving CLI to the conversation it is given: no
// project or user memory, no compaction or summaries of its own, no tool
// search or advisor, and no silent retry in another mode.
var isolationEnv = map[string]string{
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS":            "1",
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY":           "1",
	"DISABLE_AUTO_COMPACT":                      "1",
	"CLAUDE_CODE_ENABLE_AWAY_SUMMARY":           "0",
	"ENABLE_TOOL_SEARCH":                        "false",
	"CLAUDE_CODE_DISABLE_ADVISOR_TOOL":          "1",
	"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1",
	"CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK":      "1",
	"MAX_MCP_OUTPUT_TOKENS":                     "200000",
}

const (
	initializeTimeout = time.Minute
	// toolArrivalTimeout bounds the wait for a tool call the model made to
	// reach AstrLink after the caller already answered it.
	toolArrivalTimeout = time.Minute
	stderrTail         = 4 << 10
)

var (
	// ErrSignedOut means the account's CLI holds no sign-in.
	ErrSignedOut = errors.New("Claude Code is signed out")
	errExited    = errors.New("Claude Code exited")
)

// spawnSpec is how one process starts.
type spawnSpec struct {
	Binary    claudecli.Binary
	ConfigDir string
	WorkDir   string
	Home      string
	Environ   []string
	BaseURL   string
	// Tunnel carries the CLI's own traffic; the process owns and closes it.
	Tunnel  *claudecli.Tunnel
	Request request
	Session string
	Resume  bool
}

type toolCall struct {
	requestID string
	rpcID     json.RawMessage
}

type controlReply struct {
	ok       bool
	response json.RawMessage
	err      string
}

type process struct {
	secret string
	tunnel *claudecli.Tunnel
	tools  map[string]bool
	// models are the models the CLI offers, from its initialize reply.
	models  []string
	command *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	exited  chan struct{}
	onExit  func(*process)

	mu       sync.Mutex
	call     Call
	turn     *turn
	calls    map[string]toolCall
	arrived  chan struct{}
	waiters  map[string]chan controlReply
	nextID   int
	stderr   bytes.Buffer
	exitErr  error
	stopping bool
	// dead is set once the process has exited; no turn attaches after it.
	dead bool
}

func newSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// newSessionID is a random UUID for the CLI's own session.
func newSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}

func processArgs(spec spawnSpec) []string {
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json"}
	if spec.Request.Thinking {
		args = append(args, "--thinking=adaptive", "--thinking-display=summarized")
	} else {
		args = append(args, "--thinking=disabled")
	}
	if spec.Request.Effort != "" {
		args = append(args, "--effort", spec.Request.Effort)
	}
	if spec.Request.Model != "" {
		args = append(args, "--model", spec.Request.Model)
	}
	if spec.Resume {
		args = append(args, "--resume", spec.Session)
	}
	args = append(args,
		"--allowedTools", toolPrefix+"*",
		"--tools=",
		"--setting-sources=",
		"--strict-mcp-config",
		"--permission-mode", "dontAsk",
		"--include-partial-messages",
	)
	if !spec.Resume {
		args = append(args, "--session-id", spec.Session)
	}
	return append(args, "--settings", `{"disableAllHooks":true}`)
}

// startProcess runs the CLI and initializes its SDK session.
func startProcess(ctx context.Context, spec spawnSpec, onExit func(*process)) (*process, error) {
	started := false
	defer func() {
		if !started && spec.Tunnel != nil {
			_ = spec.Tunnel.Close()
		}
	}()
	secret, err := newSecret()
	if err != nil {
		return nil, err
	}
	extra := map[string]string{
		"ANTHROPIC_BASE_URL":       spec.BaseURL,
		"ANTHROPIC_CUSTOM_HEADERS": ProcessHeader + ": " + secret,
	}
	if spec.Tunnel != nil {
		for name, value := range spec.Tunnel.Env() {
			extra[name] = value
		}
	}
	for name, value := range isolationEnv {
		extra[name] = value
	}
	command := exec.Command(spec.Binary.Path, processArgs(spec)...)
	command.Env = claudecli.AddPathDir(claudecli.ServeEnv(spec.Environ, spec.Home, spec.ConfigDir, extra), dirOf(spec.Binary.Path))
	command.Dir = spec.WorkDir
	command.WaitDelay = 5 * time.Second
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	current := &process{
		secret: secret, tunnel: spec.Tunnel, tools: map[string]bool{}, command: command, stdin: stdin,
		exited: make(chan struct{}), onExit: onExit, calls: map[string]toolCall{}, arrived: make(chan struct{}),
		waiters: map[string]chan controlReply{},
	}
	command.Stderr = &lockedTail{process: current}
	for _, item := range spec.Request.Tools {
		current.tools[item.MCPName] = true
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	// From here read closes the tunnel when the process exits.
	started = true
	go current.read(stdout)
	reply, err := current.control(ctx, initializeRequest(spec.Request), initializeTimeout)
	if err != nil {
		current.stop()
		return nil, err
	}
	var initialized struct {
		Account struct {
			TokenSource string `json:"tokenSource"`
		} `json:"account"`
		Models []struct {
			ResolvedModel string `json:"resolvedModel"`
		} `json:"models"`
	}
	if json.Unmarshal(reply, &initialized) == nil && initialized.Account.TokenSource == "none" {
		current.stop()
		return nil, ErrSignedOut
	}
	for _, model := range initialized.Models {
		// "[1m]" picks the long context window in the CLI's own model menu;
		// the model is the same.
		id := strings.TrimSuffix(strings.TrimSpace(model.ResolvedModel), "[1m]")
		if id != "" && !slices.Contains(current.models, id) {
			current.models = append(current.models, id)
		}
	}
	return current, nil
}

func dirOf(path string) string {
	index := strings.LastIndexAny(path, `/\`)
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func initializeRequest(parsed request) map[string]any {
	system := parsed.System
	if system == nil {
		system = []string{}
	}
	initialize := map[string]any{"subtype": "initialize", "systemPrompt": system}
	if len(parsed.Tools) > 0 {
		tools := make([]map[string]any, 0, len(parsed.Tools))
		for _, item := range parsed.Tools {
			tools = append(tools, map[string]any{
				"name": item.MCPName, "description": item.Description, "inputSchema": item.Schema,
				"annotations": map[string]any{"readOnlyHint": false},
			})
		}
		initialize["sdkMcpServers"] = []string{mcpServerName}
		initialize["sdkMcpServerManifests"] = map[string]any{mcpServerName: map[string]any{
			"initializeResult": mcpInitializeResult(),
			"toolsListResult":  map[string]any{"tools": tools},
		}}
	}
	return initialize
}

func mcpInitializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": mcpServerName, "version": "1.0.0"},
	}
}

func (current *process) write(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	current.writeMu.Lock()
	defer current.writeMu.Unlock()
	_, err = current.stdin.Write(append(raw, '\n'))
	return err
}

// control sends a control request and waits for its reply.
func (current *process) control(ctx context.Context, request map[string]any, timeout time.Duration) (json.RawMessage, error) {
	current.mu.Lock()
	current.nextID++
	id := fmt.Sprintf("req_%d", current.nextID)
	reply := make(chan controlReply, 1)
	current.waiters[id] = reply
	current.mu.Unlock()
	defer func() {
		current.mu.Lock()
		delete(current.waiters, id)
		current.mu.Unlock()
	}()
	if err := current.write(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return nil, current.failure(err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case answer := <-reply:
		if !answer.ok {
			return nil, fmt.Errorf("claude refused %v: %s", request["subtype"], answer.err)
		}
		return answer.response, nil
	case <-current.exited:
		return nil, current.failure(errExited)
	case <-timer.C:
		return nil, fmt.Errorf("claude did not answer %v", request["subtype"])
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// failure explains why the process is gone, with the last thing it said.
func (current *process) failure(err error) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	line := lastLine(current.stderr.String())
	if line == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, line)
}

func lastLine(text string) string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			if len(line) > 200 {
				line = line[:200]
			}
			return line
		}
	}
	return ""
}

// sendUser starts a turn with the caller's newest content.
func (current *process) sendUser(content []block) error {
	uuid, err := newSessionID()
	if err != nil {
		return err
	}
	return current.write(map[string]any{
		"type": "user", "uuid": uuid, "parent_tool_use_id": nil, "client_composed": true,
		"message": map[string]any{"role": "user", "content": content},
	})
}

// answerTools returns the caller's tool results to the calls the model made,
// which resumes the turn. extra is the rest of the caller's message; it rides
// on the last result.
func (current *process) answerTools(ctx context.Context, results []block, extra []block) error {
	deadline := time.NewTimer(toolArrivalTimeout)
	defer deadline.Stop()
	for index, result := range results {
		id, _ := result["tool_use_id"].(string)
		call, err := current.awaitCall(ctx, id, deadline.C)
		if err != nil {
			return err
		}
		content := mcpContent(result["content"])
		if index == len(results)-1 {
			for _, item := range extra {
				content = append(content, mcpContent([]any{map[string]any(item)})...)
			}
		}
		if err := current.write(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype": "success", "request_id": call.requestID,
				"response": map[string]any{"mcp_response": map[string]any{
					"jsonrpc": "2.0", "id": call.rpcID,
					"result": map[string]any{"content": content, "isError": result["is_error"] == true},
				}},
			},
		}); err != nil {
			return current.failure(err)
		}
	}
	return nil
}

func (current *process) awaitCall(ctx context.Context, id string, deadline <-chan time.Time) (toolCall, error) {
	for {
		current.mu.Lock()
		call, ok := current.calls[id]
		if ok {
			delete(current.calls, id)
		}
		arrived := current.arrived
		current.mu.Unlock()
		if ok {
			return call, nil
		}
		select {
		case <-arrived:
		case <-current.exited:
			return toolCall{}, current.failure(errExited)
		case <-deadline:
			return toolCall{}, fmt.Errorf("Claude Code never asked for the result of tool call %s", id)
		case <-ctx.Done():
			return toolCall{}, ctx.Err()
		}
	}
}

// mcpContent turns Claude tool result content into MCP content.
func mcpContent(value any) []map[string]any {
	switch typed := value.(type) {
	case nil:
		return []map[string]any{}
	case string:
		return []map[string]any{{"type": "text", "text": typed}}
	case []any:
		content := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			entry, _ := item.(map[string]any)
			switch entry["type"] {
			case "text":
				text, _ := entry["text"].(string)
				content = append(content, map[string]any{"type": "text", "text": text})
			case "image":
				source, _ := entry["source"].(map[string]any)
				if source["type"] == "base64" {
					content = append(content, map[string]any{"type": "image", "data": source["data"], "mimeType": source["media_type"]})
					continue
				}
				content = append(content, map[string]any{"type": "text", "text": compactJSON(stripBlock(entry))})
			default:
				content = append(content, map[string]any{"type": "text", "text": compactJSON(stripBlock(entry))})
			}
		}
		return content
	}
	return []map[string]any{{"type": "text", "text": compactJSON(value)}}
}

// attach makes a turn the receiver of the process's output.
func (current *process) attach(active *turn, call Call) bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.dead {
		return false
	}
	current.turn, current.call = active, call
	return true
}

// stopGracefully closes the CLI's input so it exits on its own, and kills it
// if it does not.
func (current *process) stopGracefully() {
	_ = current.stdin.Close()
	timer := time.NewTimer(gracefulStop)
	defer timer.Stop()
	select {
	case <-current.exited:
	case <-timer.C:
		current.stop()
	}
}

// stop ends the process. It does not wait for the exit.
func (current *process) stop() {
	current.mu.Lock()
	current.stopping = true
	current.mu.Unlock()
	_ = current.stdin.Close()
	if current.command.Process != nil {
		_ = current.command.Process.Kill()
	}
}

func (current *process) read(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			current.handle(line)
		}
		if err != nil {
			break
		}
	}
	waitErr := current.command.Wait()
	if current.tunnel != nil {
		_ = current.tunnel.Close()
	}
	current.mu.Lock()
	current.exitErr = waitErr
	current.dead = true
	active := current.turn
	current.turn = nil
	current.mu.Unlock()
	close(current.exited)
	if active != nil {
		active.fail(httpFailure{status: 502, kind: "api_error", message: current.failure(errExited).Error()})
	}
	if current.onExit != nil {
		current.onExit(current)
	}
}

type envelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
	Event     json.RawMessage `json:"event"`
	Message   json.RawMessage `json:"message"`
	Error     string          `json:"error"`
	// result
	Subtype        string          `json:"subtype"`
	IsError        bool            `json:"is_error"`
	APIErrorStatus *int            `json:"api_error_status"`
	Result         string          `json:"result"`
	RateLimitInfo  json.RawMessage `json:"rate_limit_info"`
	ParentToolUse  *string         `json:"parent_tool_use_id"`
}

func (current *process) handle(line []byte) {
	var item envelope
	if json.Unmarshal(line, &item) != nil {
		return
	}
	switch item.Type {
	case "control_request":
		current.handleControlRequest(item)
		return
	case "control_response":
		current.handleControlResponse(item.Response)
		return
	}
	current.mu.Lock()
	active := current.turn
	current.mu.Unlock()
	if active == nil || (item.ParentToolUse != nil && *item.ParentToolUse != "") {
		return
	}
	switch item.Type {
	case "stream_event":
		active.handleEvent(item.Event)
	case "assistant":
		if item.Error != "" {
			active.noteError(item.Error)
		}
	case "rate_limit_event":
		active.noteRateLimit(item.RateLimitInfo)
	case "result":
		active.handleResult(item)
	}
}

func (current *process) handleControlResponse(raw json.RawMessage) {
	var response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	current.mu.Lock()
	waiter := current.waiters[response.RequestID]
	current.mu.Unlock()
	if waiter != nil {
		waiter <- controlReply{ok: response.Subtype == "success", response: response.Response, err: response.Error}
	}
}

func (current *process) handleControlRequest(item envelope) {
	var request struct {
		Subtype    string          `json:"subtype"`
		ServerName string          `json:"server_name"`
		Message    json.RawMessage `json:"message"`
		ToolName   string          `json:"tool_name"`
		Input      json.RawMessage `json:"input"`
	}
	if json.Unmarshal(item.Request, &request) != nil {
		return
	}
	switch request.Subtype {
	case "mcp_message":
		current.handleMCP(item.RequestID, request.ServerName, request.Message)
	case "can_use_tool":
		// Only the caller's own tools are allowed; nothing else exists.
		response := map[string]any{"behavior": "deny", "message": "only the client's tools are available"}
		if strings.HasPrefix(request.ToolName, toolPrefix) && current.tools[strings.TrimPrefix(request.ToolName, toolPrefix)] {
			response = map[string]any{"behavior": "allow", "updatedInput": request.Input}
		}
		current.reply(item.RequestID, response)
	default:
		_ = current.write(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "error", "request_id": item.RequestID, "error": "unsupported request",
		}})
	}
}

func (current *process) reply(requestID string, response any) {
	_ = current.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": requestID, "response": response,
	}})
}

func (current *process) mcpReply(requestID string, rpcID json.RawMessage, result any) {
	if len(rpcID) == 0 {
		rpcID = json.RawMessage("0")
	}
	current.reply(requestID, map[string]any{"mcp_response": map[string]any{"jsonrpc": "2.0", "id": rpcID, "result": result}})
}

func (current *process) handleMCP(requestID, server string, raw json.RawMessage) {
	var message struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Params struct {
			Name string `json:"name"`
			Meta struct {
				ToolUseID string `json:"claudecode/toolUseId"`
			} `json:"_meta"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return
	}
	if server != mcpServerName {
		current.mcpReply(requestID, message.ID, map[string]any{})
		return
	}
	switch message.Method {
	case "initialize":
		current.mcpReply(requestID, message.ID, mcpInitializeResult())
	case "tools/call":
		if !current.tools[message.Params.Name] || message.Params.Meta.ToolUseID == "" {
			current.mcpReply(requestID, message.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "tool is not available"}}, "isError": true,
			})
			return
		}
		current.mu.Lock()
		current.calls[message.Params.Meta.ToolUseID] = toolCall{requestID: requestID, rpcID: message.ID}
		close(current.arrived)
		current.arrived = make(chan struct{})
		current.mu.Unlock()
	default:
		// tools/list is answered from the manifest the CLI already holds;
		// notifications and anything else get an empty result.
		current.mcpReply(requestID, message.ID, map[string]any{})
	}
}

// lockedTail keeps the end of the CLI's stderr for error messages.
type lockedTail struct {
	process *process
}

func (tail *lockedTail) Write(data []byte) (int, error) {
	tail.process.mu.Lock()
	defer tail.process.mu.Unlock()
	tail.process.stderr.Write(data)
	if extra := tail.process.stderr.Len() - stderrTail; extra > 0 {
		tail.process.stderr.Next(extra)
	}
	return len(data), nil
}

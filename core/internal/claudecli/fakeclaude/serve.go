package fakeclaude

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FakeToken is the Authorization the serving fake sends when Settings.Token
// is empty.
const FakeToken = "sk-ant-oat01-fake-cli-token"

// ServeArgsFile is where the serving fake records its arguments, one per
// line.
func ServeArgsFile(configDir string) string {
	return filepath.Join(configDir, "fake-serve-args.txt")
}

// serving reports whether the arguments start an SDK session.
func serving(args []string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--input-format" && args[index+1] == "stream-json" {
			return true
		}
	}
	return false
}

func flagValue(args []string, name string) string {
	for index, arg := range args {
		if arg == name && index+1 < len(args) {
			return args[index+1]
		}
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value
		}
	}
	return ""
}

type server struct {
	settings  Settings
	configDir string
	args      []string
	session   string
	model     string
	system    []string
	tools     []map[string]any
	messages  []map[string]any
	reader    *bufio.Reader
	out       io.Writer
	nextID    int
}

// serve speaks the SDK's stream-json protocol. Each user message runs the
// model through ANTHROPIC_BASE_URL until it stops asking for tools; tool
// calls go to the SDK's MCP server and wait for its answers.
func serve(settings Settings, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	recordEnv(configDir, "serve")
	if configDir != "" {
		_ = os.WriteFile(ServeArgsFile(configDir), []byte(strings.Join(args, "\n")+"\n"), 0o600)
	}
	reachOut(settings, configDir, RefreshHost)
	reachOut(settings, configDir, OtherHost)
	current := &server{
		settings: settings, configDir: configDir, args: args, model: flagValue(args, "--model"),
		reader: bufio.NewReaderSize(stdin, 1<<20), out: stdout,
	}
	if resume := flagValue(args, "--resume"); resume != "" {
		raw, err := os.ReadFile(current.transcript(resume))
		if err != nil {
			fmt.Fprintf(stderr, "No conversation found with session ID: %s\n", resume)
			return 1
		}
		if err := json.Unmarshal(raw, &current.messages); err != nil {
			fmt.Fprintln(stderr, "transcript is unreadable")
			return 1
		}
		current.session = resume
	} else {
		current.session = flagValue(args, "--session-id")
	}
	for {
		line, err := current.reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var message map[string]any
			if json.Unmarshal(line, &message) == nil {
				current.handle(message)
			}
		}
		if err != nil {
			return 0
		}
	}
}

func (current *server) write(value any) {
	raw, _ := json.Marshal(value)
	_, _ = current.out.Write(append(raw, '\n'))
}

func (current *server) transcript(session string) string {
	cwd, _ := os.Getwd()
	slug := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(cwd, "-")
	return filepath.Join(current.configDir, "projects", slug, session+".jsonl")
}

func (current *server) save() {
	path := current.transcript(current.session)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, _ := json.Marshal(current.messages)
	_ = os.WriteFile(path, raw, 0o600)
}

func (current *server) handle(message map[string]any) {
	switch message["type"] {
	case "control_request":
		request, _ := message["request"].(map[string]any)
		id, _ := message["request_id"].(string)
		if request["subtype"] == "initialize" {
			current.initialize(id, request)
			return
		}
		current.write(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": id, "response": map[string]any{},
		}})
	case "user":
		inner, _ := message["message"].(map[string]any)
		current.messages = append(current.messages, map[string]any{"role": "user", "content": inner["content"]})
		current.turn()
		current.save()
	}
}

func (current *server) initialize(id string, request map[string]any) {
	if prompts, ok := request["systemPrompt"].([]any); ok {
		for _, prompt := range prompts {
			text, _ := prompt.(string)
			current.system = append(current.system, text)
		}
	}
	if manifests, ok := request["sdkMcpServerManifests"].(map[string]any); ok {
		for server, manifest := range manifests {
			listed, _ := manifest.(map[string]any)["toolsListResult"].(map[string]any)
			tools, _ := listed["tools"].([]any)
			for _, item := range tools {
				entry, _ := item.(map[string]any)
				name, _ := entry["name"].(string)
				current.tools = append(current.tools, map[string]any{
					"name": "mcp__" + server + "__" + name, "description": entry["description"], "input_schema": entry["inputSchema"],
				})
			}
		}
	}
	source := "claude.ai"
	if _, err := os.Stat(AccountFile(current.configDir)); err != nil {
		source = "none"
	}
	models := make([]map[string]any, 0, len(offeredModels))
	for _, model := range offeredModels {
		entry := map[string]any{"value": model[0], "displayName": model[0]}
		if model[1] != "" {
			entry["resolvedModel"] = model[1]
		}
		models = append(models, entry)
	}
	current.write(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": id,
		"response": map[string]any{"account": map[string]any{"tokenSource": source}, "models": models},
	}})
}

// offeredModels is the model menu the fake's initialize reply lists, as
// value and resolved model; CLI 2.1.300 lists its default entry and the long
// context variant besides the plain ones.
var offeredModels = [][2]string{
	{"default", "claude-sonnet-5-5"},
	{"sonnet", "claude-sonnet-5-5"},
	{"sonnet[1m]", "claude-sonnet-5-5[1m]"},
	{"opus", "claude-opus-5-5"},
	{"haiku", "claude-haiku-5-5"},
	{"custom", ""},
}

// OfferedModels are the models the fake offers.
var OfferedModels = []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"}

func newUUID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// turn runs the model until it ends or fails.
func (current *server) turn() {
	for {
		content, stopReason, calls, failure := current.call()
		if failure != nil {
			current.write(map[string]any{"type": "assistant", "error": failure.kind, "session_id": current.session,
				"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": failure.message}}}})
			current.write(map[string]any{
				"type": "result", "subtype": "success", "is_error": true, "api_error_status": failure.status,
				"result": failure.message, "session_id": current.session,
			})
			return
		}
		current.messages = append(current.messages, map[string]any{"role": "assistant", "content": content})
		if stopReason != "tool_use" || len(calls) == 0 {
			text := ""
			for _, item := range content {
				if item["type"] == "text" {
					text, _ = item["text"].(string)
				}
			}
			current.write(map[string]any{
				"type": "result", "subtype": "success", "is_error": false, "result": text, "session_id": current.session,
				"stop_reason": stopReason,
			})
			return
		}
		var results []any
		for _, call := range calls {
			result, ok := current.awaitTool(call)
			if !ok {
				return
			}
			results = append(results, result)
		}
		current.messages = append(current.messages, map[string]any{"role": "user", "content": results})
	}
}

type toolRequest struct {
	requestID string
	useID     string
}

type apiFailure struct {
	status  int
	kind    string
	message string
}

// call makes one model request and relays its stream.
func (current *server) call() ([]map[string]any, string, []toolRequest, *apiFailure) {
	system := []any{
		map[string]any{"type": "text", "text": "x-anthropic-billing-header: cc_version=" + current.settings.Version + "; cc_entrypoint=sdk-cli;"},
		map[string]any{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK."},
	}
	for _, text := range current.system {
		system = append(system, map[string]any{"type": "text", "text": text})
	}
	userID, _ := json.Marshal(map[string]string{"device_id": "fake-device", "account_uuid": "", "session_id": current.session})
	body := map[string]any{
		"model": current.model, "system": system, "messages": current.messages, "max_tokens": 32000, "stream": true,
		"metadata": map[string]any{"user_id": string(userID)},
	}
	if len(current.tools) > 0 {
		body["tools"] = current.tools
	}
	raw, _ := json.Marshal(body)
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(os.Getenv("ANTHROPIC_BASE_URL"), "/")+"/v1/messages?beta=true", bytes.NewReader(raw))
	if err != nil {
		return nil, "", nil, &apiFailure{status: 0, kind: "unknown", message: err.Error()}
	}
	token := current.settings.Token
	if token == "" {
		token = FakeToken
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Anthropic-Version", "2023-06-01")
	// The fingerprint the real CLI sends with every model call.
	request.Header.Set("User-Agent", "claude-cli/"+current.settings.Version+" (external, sdk-cli)")
	request.Header.Set("X-App", "cli")
	request.Header.Set("X-Claude-Code-Session-Id", current.session)
	request.Header.Set("Anthropic-Beta", "claude-code-20250219,oauth-2025-04-20")
	request.Header.Set("X-Stainless-Lang", "js")
	request.Header.Set("X-Stainless-Package-Version", "0.95.1")
	request.Header.Set("X-Stainless-Runtime", "node")
	for _, line := range strings.Split(os.Getenv("ANTHROPIC_CUSTOM_HEADERS"), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok {
			request.Header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}
	response, err := (&http.Client{Timeout: time.Minute}).Do(request)
	if err != nil {
		return nil, "", nil, &apiFailure{kind: "unknown", message: "API Error: Connection error."}
	}
	defer response.Body.Close()
	if response.Header.Get("Anthropic-Ratelimit-Unified-Status") == "rejected" {
		reset, _ := strconv.ParseInt(response.Header.Get("Anthropic-Ratelimit-Unified-Reset"), 10, 64)
		current.write(map[string]any{"type": "rate_limit_event", "session_id": current.session,
			"rate_limit_info": map[string]any{"status": "rejected", "resetsAt": reset, "rateLimitType": "five_hour"}})
	}
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		kind := "unknown"
		switch response.StatusCode {
		case http.StatusTooManyRequests:
			kind = "rate_limit"
		case http.StatusUnauthorized:
			kind = "authentication_failed"
		case http.StatusBadRequest:
			kind = "invalid_request"
		}
		if response.StatusCode >= 500 {
			kind = "server_error"
		}
		return nil, "", nil, &apiFailure{status: response.StatusCode, kind: kind,
			message: fmt.Sprintf("API Error: %d %s", response.StatusCode, strings.TrimSpace(string(detail)))}
	}
	return current.relay(response.Body)
}

// relay forwards the model stream and asks for each tool as its block ends,
// as the real CLI does.
func (current *server) relay(body io.Reader) ([]map[string]any, string, []toolRequest, *apiFailure) {
	var content []map[string]any
	inputs := map[int]*strings.Builder{}
	stopReason := ""
	var calls []toolRequest
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		var event map[string]any
		decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(data)))
		decoder.UseNumber()
		if decoder.Decode(&event) != nil {
			continue
		}
		current.write(map[string]any{"type": "stream_event", "event": event, "session_id": current.session,
			"parent_tool_use_id": nil, "uuid": newUUID()})
		index := 0
		if number, ok := event["index"].(json.Number); ok {
			value, _ := number.Int64()
			index = int(value)
		}
		switch event["type"] {
		case "content_block_start":
			started, _ := event["content_block"].(map[string]any)
			for len(content) <= index {
				content = append(content, nil)
			}
			content[index] = started
			if started["type"] == "tool_use" {
				inputs[index] = &strings.Builder{}
			}
		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			switch delta["type"] {
			case "text_delta":
				text, _ := delta["text"].(string)
				previous, _ := content[index]["text"].(string)
				content[index]["text"] = previous + text
			case "input_json_delta":
				text, _ := delta["partial_json"].(string)
				inputs[index].WriteString(text)
			}
		case "content_block_stop":
			if builder := inputs[index]; builder != nil {
				var input any = map[string]any{}
				if builder.Len() > 0 {
					_ = json.Unmarshal([]byte(builder.String()), &input)
				}
				content[index]["input"] = input
				calls = append(calls, current.askTool(content[index]))
			}
		case "message_delta":
			delta, _ := event["delta"].(map[string]any)
			stopReason, _ = delta["stop_reason"].(string)
		}
	}
	kept := slices.DeleteFunc(content, func(item map[string]any) bool { return item == nil })
	return kept, stopReason, calls, nil
}

func (current *server) askTool(use map[string]any) toolRequest {
	name, _ := use["name"].(string)
	id, _ := use["id"].(string)
	server, tool := "", name
	if parts := strings.SplitN(name, "__", 3); len(parts) == 3 && parts[0] == "mcp" {
		server, tool = parts[1], parts[2]
	}
	current.nextID++
	requestID := fmt.Sprintf("fake_%d", current.nextID)
	current.write(map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{
		"subtype": "mcp_message", "server_name": server,
		"message": map[string]any{"jsonrpc": "2.0", "id": current.nextID, "method": "tools/call", "params": map[string]any{
			"name": tool, "arguments": use["input"], "_meta": map[string]any{"claudecode/toolUseId": id},
		}},
	}})
	return toolRequest{requestID: requestID, useID: id}
}

// awaitTool reads input until the SDK answers a tool call.
func (current *server) awaitTool(call toolRequest) (map[string]any, bool) {
	for {
		line, err := current.reader.ReadBytes('\n')
		if err != nil {
			return nil, false
		}
		var message struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					MCPResponse struct {
						Result struct {
							Content []map[string]any `json:"content"`
							IsError bool             `json:"isError"`
						} `json:"result"`
					} `json:"mcp_response"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &message) != nil || message.Type != "control_response" ||
			message.Response.RequestID != call.requestID {
			continue
		}
		result := message.Response.Response.MCPResponse.Result
		content := make([]any, 0, len(result.Content))
		for _, item := range result.Content {
			if item["type"] == "image" {
				content = append(content, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": item["mimeType"], "data": item["data"],
				}})
				continue
			}
			content = append(content, map[string]any{"type": "text", "text": item["text"]})
		}
		return map[string]any{"type": "tool_result", "tool_use_id": call.useID, "content": content, "is_error": result.IsError}, true
	}
}

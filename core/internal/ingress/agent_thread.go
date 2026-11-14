package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// Coding agents run subagents: a thread the conversation starts for a task,
// which makes model calls of its own. AstrLink records each thread as its own
// conversation and links it to the one that started it, using the thread
// identity the client already sends. These values are read for local grouping
// only; none of them changes what is forwarded.
const (
	// Claude Code names a subagent's calls with its agent id, inside the
	// session id the main thread uses, and a nested one with its parent's.
	claudeCodeAgentHeader       = "X-Claude-Code-Agent-Id"
	claudeCodeParentAgentHeader = "X-Claude-Code-Parent-Agent-Id"
	// Codex sends the thread's own id and, on a spawned thread, its parent's.
	// Since 0.131 the headers are hyphenated; 0.129 and 0.130 used
	// underscores, and up to 0.128 session_id named each thread.
	codexParentThreadHeader = "X-Codex-Parent-Thread-Id"
	// OpenCode names its session and, in a child session, the parent's.
	openCodeSessionHeader = "X-Opencode-Session-Id"
)

var (
	codexThreadHeaders     = []string{"Thread-Id", "Thread_id"}
	codexRootHeaders       = []string{"Session-Id", "Session_id", "Conversation_id"}
	openCodeParentHeaders  = []string{"X-Opencode-Parent-Session-Id", "X-Parent-Session-Id"}
	claudeCodeTitleMarkers = []string{"You are naming a coding session"}
)

const (
	// A DeepSeek Harness subagent's prompt is followed, in the same message,
	// by this note naming its parent's session id. Over Responses every
	// agent sends its own session id as prompt_cache_key; over Chat it sends
	// no thread ids at all.
	deepSeekHarnessParentMarker = `Your parent agent id is "`
	// Title requests quote the conversation's opening message.
	deepSeekHarnessTitlePrefix = "Generate the session title from this JSON array of human messages:"
	codexTitlePrefix           = "Generate a concise, single-line task title"
	codexTitleQuote            = "\n\nUser prompt:\n"
	openCodeTitlePrefix        = "Generate a title for this conversation:"
)

// agentThread is what a request says about the agent thread that sent it.
type agentThread struct {
	// cursor names the thread when the client tells its threads apart: a
	// Claude Code subagent, a Codex thread, an OpenCode session. Empty when
	// convo's own explicit cursor already names it.
	cursor string
	// parent is the explicit cursor of the thread that started this one.
	parent string
	// helper marks a title request the client sends under its
	// conversation's own identity; it must not open or move a turn.
	helper bool
	// quotedOpener is the opening message a title request quotes when the
	// client sends it outside the conversation, as a Codex title thread or
	// DeepSeek Harness does. Raw client text: never persist or log it.
	quotedOpener string
	// spawnOpener is the newest DeepSeek Harness user message that carries
	// the parent note, which names its subagent thread when the request
	// carries no thread id. Raw client text: never persist or log it.
	spawnOpener string
	// paseoTask is the create_agent task a request's newest message carries,
	// which names its parent only when the request opens a new conversation. Raw client text: never persist or log it.
	paseoTask string
}

// readAgentThread reads the thread identity of a request whose body fields
// and convo summary are known. Values that are not usable cursors are
// ignored.
func readAgentThread(
	header http.Header,
	protocol contract.ProtocolID,
	client contract.ClientType,
	fields map[string]json.RawMessage,
	summary convo.RequestSummary,
) agentThread {
	var thread agentThread
	switch protocol {
	case contract.ProtocolAnthropicMessages:
		readClaudeCodeThread(&thread, header, client, fields, summary)
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		readCodexThread(&thread, header, fields, summary)
	}
	if session := threadCursor(header.Get(openCodeSessionHeader)); session != "" {
		thread.cursor = session
		for _, name := range openCodeParentHeaders {
			if parent := threadCursor(header.Get(name)); parent != "" && parent != session {
				thread.parent = parent
				break
			}
		}
		thread.helper = client == contract.ClientOpenCode && !summary.HasTools &&
			strings.HasPrefix(strings.TrimSpace(firstUserRawText(protocol, fields)), openCodeTitlePrefix)
	}
	if client == contract.ClientDeepSeekHarness &&
		(protocol == contract.ProtocolOpenAIChat || protocol == contract.ProtocolOpenAIResponses) {
		readDeepSeekHarnessThread(&thread, protocol, fields, summary)
	}
	if thread.parent == "" && thread.spawnOpener == "" {
		history := fields["messages"]
		if protocol == contract.ProtocolOpenAIResponses {
			history = fields["input"]
		}
		// Most requests carry no envelope; skip reading their history.
		switch protocol {
		case contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses, contract.ProtocolAnthropicMessages:
			if bytes.Contains(history, []byte("paseo-system")) {
				thread.paseoTask = paseoCreateTask(protocol, userRawParts(protocol, fields))
			}
		}
	}
	return thread
}

func readClaudeCodeThread(thread *agentThread, header http.Header, client contract.ClientType, fields map[string]json.RawMessage, summary convo.RequestSummary) {
	// The main thread names the session in metadata.user_id, which convo
	// reads as its first explicit cursor; the header carries the same id.
	session := ""
	if len(summary.ExplicitCursors) > 0 {
		session = summary.ExplicitCursors[0]
	}
	if session == "" {
		session = threadCursor(header.Get(accountauth.ClaudeCodeSessionHeader))
	}
	if agent := strings.TrimSpace(header.Get(claudeCodeAgentHeader)); agent != "" && session != "" {
		thread.cursor = threadCursor(session + "#agent:" + agent)
		thread.parent = session
		if parent := strings.TrimSpace(header.Get(claudeCodeParentAgentHeader)); parent != "" && parent != agent {
			thread.parent = threadCursor(session + "#agent:" + parent)
		}
	} else if parent := claudeCodeParentSession(fields["metadata"]); parent != "" && parent != session {
		// An agent team's teammate runs a session of its own that names
		// the lead's.
		thread.parent = parent
	}
	if client == contract.ClientClaudeCode && !summary.HasTools {
		system := systemRawText(contract.ProtocolAnthropicMessages, fields)
		for _, marker := range claudeCodeTitleMarkers {
			if strings.Contains(system, marker) {
				thread.helper = true
			}
		}
		thread.helper = thread.helper || strings.HasPrefix(strings.TrimSpace(firstUserRawText(contract.ProtocolAnthropicMessages, fields)), "<session>")
	}
}

// claudeCodeParentSession reads parent_session_id from the JSON form of
// Claude Code's metadata.user_id.
func claudeCodeParentSession(raw json.RawMessage) string {
	userID := ojson.GetBytes(raw, "user_id")
	if userID.Type != ojson.String || !strings.HasPrefix(strings.TrimSpace(userID.Str), "{") {
		return ""
	}
	return threadCursor(ojson.Get(userID.Str, "parent_session_id").String())
}

func readCodexThread(thread *agentThread, header http.Header, fields map[string]json.RawMessage, summary convo.RequestSummary) {
	metadata := ojson.ParseBytes(fields["client_metadata"])
	turn := ojson.Parse(header.Get(codexTurnMetadataHeader))
	if !turn.IsObject() {
		turn = ojson.Parse(metadata.Get("x-codex-turn-metadata").String())
	}
	cursor := firstHeaderCursor(header, codexThreadHeaders)
	if cursor == "" {
		cursor = threadCursor(metadata.Get("thread_id").String())
	}
	if cursor == "" {
		cursor = firstHeaderCursor(header, codexRootHeaders)
	}
	if cursor == "" {
		cursor = threadCursor(metadata.Get("session_id").String())
	}
	thread.cursor = cursor
	for _, parent := range []string{
		header.Get(codexParentThreadHeader),
		metadata.Get("x-codex-parent-thread-id").String(),
		turn.Get("parent_thread_id").String(),
	} {
		if parent = threadCursor(parent); parent != "" && parent != cursor {
			thread.parent = parent
			break
		}
	}
	// A TUI title is a hidden thread of its own, marked only by its source,
	// that quotes the prompt it names.
	source := turn.Get("thread_source")
	title := strings.Contains(source.Raw, "thread_title")
	text := summary.LastUserText
	if title || strings.HasPrefix(text, codexTitlePrefix) {
		if _, quoted, ok := strings.Cut(text, codexTitleQuote); ok {
			thread.quotedOpener = quoted
		}
	}
}

func readDeepSeekHarnessThread(thread *agentThread, protocol contract.ProtocolID, fields map[string]json.RawMessage, summary convo.RequestSummary) {
	texts := userRawTexts(protocol, fields)
	for index := len(texts) - 1; index >= 0; index-- {
		_, note, ok := strings.Cut(texts[index], deepSeekHarnessParentMarker)
		if !ok {
			continue
		}
		// A request that names its own thread came from a client whose
		// parent named its own the same way, so the note's id finds it.
		if thread.cursor != "" || len(summary.ExplicitCursors) > 0 {
			if parent, _, ok := strings.Cut(note, `"`); ok {
				thread.parent = threadCursor(parent)
			}
		} else {
			thread.spawnOpener = texts[index]
		}
		break
	}
	if summary.HasTools || len(texts) == 0 {
		return
	}
	quoted, ok := strings.CutPrefix(strings.TrimSpace(texts[0]), deepSeekHarnessTitlePrefix)
	if !ok {
		return
	}
	var messages []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(quoted)), &messages) == nil && len(messages) > 0 {
		thread.quotedOpener = messages[0].Text
	}
}

// spawnPrompt is the task a DeepSeek Harness subagent was given: its opening
// message up to the parent note.
func spawnPrompt(opener string) string {
	prompt, _, _ := strings.Cut(opener, deepSeekHarnessParentMarker)
	return strings.TrimSpace(prompt)
}

func firstHeaderCursor(header http.Header, names []string) string {
	for _, name := range names {
		if value := threadCursor(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// threadCursor keeps value when it is a usable explicit cursor.
func threadCursor(value string) string {
	cursor := convo.Cursor{Kind: convo.KindExplicit, Direction: convo.DirectionIn, Value: strings.TrimSpace(value)}
	if cursor.Validate() != nil {
		return ""
	}
	return cursor.Value
}

// userRawTexts returns the text of each user message in history order,
// without convo's harness unwrapping, for the few markers clients put there.
func userRawTexts(protocol contract.ProtocolID, fields map[string]json.RawMessage) []string {
	var texts []string
	for _, parts := range userRawParts(protocol, fields) {
		texts = append(texts, strings.Join(parts, ""))
	}
	return texts
}

func firstUserRawText(protocol contract.ProtocolID, fields map[string]json.RawMessage) string {
	if texts := userRawTexts(protocol, fields); len(texts) > 0 {
		return texts[0]
	}
	return ""
}

// systemRawText joins the system prompt's text.
func systemRawText(protocol contract.ProtocolID, fields map[string]json.RawMessage) string {
	if protocol != contract.ProtocolAnthropicMessages {
		return ""
	}
	return partsText(ojson.ParseBytes(fields["system"]))
}

// partsText reads content that is a string or a list of text parts.
func partsText(content ojson.Result) string {
	if content.Type == ojson.String {
		return content.Str
	}
	var text strings.Builder
	content.ForEach(func(_, part ojson.Result) bool {
		if value := part.Get("text"); value.Type == ojson.String {
			text.WriteString(value.Str)
		}
		return true
	})
	return text.String()
}

// Link fingerprints are keyed digests that let one conversation find another
// without storing either's text: the prompt a parent gave a subagent, and the
// opening message a title request quotes. They use their own key, so they
// never equal a session fingerprint.
const (
	linkFingerprintInfo = "astrlink/subagent-link/v1"
	// openerKeyRunes is how much of an opening message names it; title
	// requests may quote it cut short.
	openerKeyRunes = 160
	// spawnLinkWindow and titleLinkWindow bound how long after a parent's
	// call or a conversation's start the link may be found.
	spawnLinkWindow = 30 * time.Minute
	titleLinkWindow = 6 * time.Hour
)

// spawnFingerprint names the prompt a parent gave a subagent.
func spawnFingerprint(fp *convo.Fingerprinter, prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if fp == nil || prompt == "" {
		return ""
	}
	return fp.Fingerprint(convo.DigestText("subagent prompt\n"+prompt, false))
}

// spawnThreadCursor names a DeepSeek Harness subagent thread by its opening
// message, which carries its prompt and its parent's id.
func spawnThreadCursor(fp *convo.Fingerprinter, opener string) string {
	if fp == nil || strings.TrimSpace(opener) == "" {
		return ""
	}
	return "agent:" + fp.Fingerprint(convo.DigestText("subagent thread\n"+opener, false))
}

// openerFingerprint names a conversation by the start of its opening message.
func openerFingerprint(fp *convo.Fingerprinter, text string) string {
	key := strings.Join(strings.Fields(text), " ")
	if fp == nil || key == "" {
		return ""
	}
	if utf8.RuneCountInString(key) > openerKeyRunes {
		key = string([]rune(key)[:openerKeyRunes])
	}
	return fp.Fingerprint(convo.DigestText("conversation opener\n"+key, false))
}

// DeepSeek Harness starts a subagent with one of these tools; its prompt
// argument becomes the subagent's opening message.
var deepSeekHarnessSpawnTools = map[string]bool{"subagent": true, "subagent_fork": true}

// Paseo starts an agent with its create_agent tool, served by its MCP server
// "paseo" (or "paseo_<variant>"). Clients name it mcp__paseo__create_agent
// (Claude Code), paseo.create_agent (Codex), or create_agent under the
// Responses namespace mcp__paseo__. Its trimmed initialPrompt opens the new
// agent as a message that is exactly one escaped agent-message envelope:
// <paseo-system version="1" kind="agent-message" source-agent-id="…"
// message-id="…" …>\n…\n</paseo-system>.
func isPaseoCreateAgentTool(namespace, name string) bool {
	isPaseo := func(server string) bool { return server == "paseo" || strings.HasPrefix(server, "paseo_") }
	if namespace != "" {
		server, ok := mcpServer(namespace)
		return ok && isPaseo(server) && name == "create_agent"
	}
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		server, leaf, _ := strings.Cut(rest, "__")
		return isPaseo(server) && leaf == "create_agent"
	}
	server, leaf, _ := strings.Cut(name, ".")
	return isPaseo(server) && leaf == "create_agent"
}

var (
	paseoEnvelope  = regexp.MustCompile(`^<paseo-system((?:\s+[a-z-]+="[^"<>]*")+)>\n([^<>]*)\n</paseo-system>$`)
	paseoAttribute = regexp.MustCompile(`\s+([a-z-]+)="([^"<>]*)"`)
	// paseoBadEscape finds an ampersand that does not start an entity Paseo
	// writes, which its own parser rejects.
	paseoBadEscape    = regexp.MustCompile(`&(?:amp;|lt;|gt;|quot;|apos;)?`)
	paseoXMLUnescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&")
)

func paseoEscaped(value string) bool {
	for _, match := range paseoBadEscape.FindAllString(value, -1) {
		if match == "&" {
			return false
		}
	}
	return true
}

// paseoCreateTask returns the task a Paseo create_agent gave this agent: the
// body of a text part of the newest user message that is a whole
// agent-message envelope, when no earlier user message holds a Paseo
// envelope. Earlier messages may hold client context such as AGENTS.md. The
// only other part the newest may hold is a whole Claude Code system reminder
// (see convo/text.go) on Messages; any other text there may be a quoted
// example, so nothing is inferred. Raw client text: never persist or log it.
func paseoCreateTask(protocol contract.ProtocolID, messages [][]string) string {
	if len(messages) == 0 {
		return ""
	}
	for _, parts := range messages[:len(messages)-1] {
		for _, part := range parts {
			if strings.HasPrefix(strings.TrimSpace(part), "<paseo-system ") {
				return ""
			}
		}
	}
	task := ""
	for _, part := range messages[len(messages)-1] {
		body, ok := paseoAgentMessage(part)
		switch {
		case ok && task == "":
			task = body
		case !ok && protocol == contract.ProtocolAnthropicMessages && isSystemReminder(part):
		default:
			return ""
		}
	}
	return task
}

// isSystemReminder reports whether text is exactly one <system-reminder>
// block.
func isSystemReminder(text string) bool {
	text = strings.TrimSpace(text)
	const open, close = "<system-reminder>", "</system-reminder>"
	return strings.HasPrefix(text, open) && strings.HasSuffix(text, close) &&
		strings.Count(text, open) == 1 && strings.Count(text, close) == 1
}

// paseoAgentMessage reads text that is exactly one agent-message envelope as
// Paseo writes it, and returns its unescaped body.
func paseoAgentMessage(text string) (string, bool) {
	match := paseoEnvelope.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil || !paseoEscaped(match[2]) {
		return "", false
	}
	attributes := map[string]string{}
	for index, attribute := range paseoAttribute.FindAllStringSubmatch(match[1], -1) {
		name, value := attribute[1], attribute[2]
		if _, seen := attributes[name]; seen || !paseoEscaped(value) || (index == 0) != (name == "version") {
			return "", false
		}
		switch name {
		case "version", "kind", "source-agent-id", "message-id", "source-server-id", "source-agent-title":
		default:
			return "", false
		}
		attributes[name] = value
	}
	if attributes["version"] != "1" || attributes["kind"] != "agent-message" ||
		attributes["source-agent-id"] == "" || attributes["message-id"] == "" {
		return "", false
	}
	body := strings.TrimSpace(paseoXMLUnescaper.Replace(match[2]))
	return body, body != ""
}

// userRawParts returns the text parts of each user message in history order.
func userRawParts(protocol contract.ProtocolID, fields map[string]json.RawMessage) [][]string {
	var messages [][]string
	add := func(content ojson.Result) {
		if content.Type == ojson.String {
			messages = append(messages, []string{content.Str})
			return
		}
		var parts []string
		content.ForEach(func(_, part ojson.Result) bool {
			if value := part.Get("text"); value.Type == ojson.String {
				parts = append(parts, value.Str)
			}
			return true
		})
		messages = append(messages, parts)
	}
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages:
		ojson.ParseBytes(fields["messages"]).ForEach(func(_, message ojson.Result) bool {
			if message.Get("role").String() == "user" {
				add(message.Get("content"))
			}
			return true
		})
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		input := ojson.ParseBytes(fields["input"])
		if input.Type == ojson.String {
			add(input)
			break
		}
		input.ForEach(func(_, item ojson.Result) bool {
			itemType := item.Get("type").String()
			if item.Get("role").String() == "user" && (itemType == "" || itemType == "message") {
				add(item.Get("content"))
			}
			return true
		})
	}
	return messages
}

// maxSpawnArgumentBytes bounds the tool arguments one response may buffer.
const maxSpawnArgumentBytes = 1 << 20

// spawnCalls collects the arguments of the subagent tool calls a response
// makes (OpenAI Chat, OpenAI Responses, Anthropic Messages), in memory and
// only for the response being read, so the record can store a keyed
// fingerprint of each prompt.
type spawnCalls struct {
	protocol contract.ProtocolID
	// deepSeek also accepts DeepSeek Harness subagent tools.
	deepSeek bool
	calls    map[int64]*spawnCall
	bytes    int
}

type spawnCall struct {
	namespace string
	name      string
	arguments strings.Builder
}

// observe reads one stream event, or a whole body when streaming is false.
func (spawns *spawnCalls) observe(payload []byte, streaming bool) {
	if spawns == nil {
		return
	}
	if !streaming {
		// The whole body is read again on every usage read.
		spawns.calls, spawns.bytes = nil, 0
	}
	document := ojson.ParseBytes(payload)
	switch spawns.protocol {
	case contract.ProtocolOpenAIChat:
		key := "message"
		if streaming {
			key = "delta"
		}
		document.Get("choices").ForEach(func(_, choice ojson.Result) bool {
			choice.Get(key + ".tool_calls").ForEach(func(position, call ojson.Result) bool {
				index := position.Int()
				if value := call.Get("index"); value.Exists() {
					index = value.Int()
				}
				spawns.add(choice.Get("index").Int()<<16|index&0xffff, call.Get("function.name").String(), call.Get("function.arguments").String())
				return true
			})
			return true
		})
	case contract.ProtocolOpenAIResponses:
		// A stream sends each finished item in output_item.done and repeats
		// them in its final event, which the Codex backend leaves empty.
		if streaming && document.Get("type").String() == "response.output_item.done" {
			spawns.setItem(document.Get("output_index").Int(), document.Get("item"))
			return
		}
		output := document.Get("output")
		if streaming {
			output = document.Get("response.output")
		}
		output.ForEach(func(position, item ojson.Result) bool {
			spawns.setItem(position.Int(), item)
			return true
		})
	case contract.ProtocolAnthropicMessages:
		if !streaming {
			document.Get("content").ForEach(func(position, block ojson.Result) bool {
				if block.Get("type").String() == "tool_use" {
					spawns.add(position.Int(), block.Get("name").String(), block.Get("input").Raw)
				}
				return true
			})
		} else if block := document.Get("content_block"); block.Get("type").String() == "tool_use" {
			// The start event names the tool; input_json_delta events carry it.
			spawns.add(document.Get("index").Int(), block.Get("name").String(), "")
		} else if delta := document.Get("delta"); delta.Get("type").String() == "input_json_delta" {
			spawns.add(document.Get("index").Int(), "", delta.Get("partial_json").String())
		}
	}
}

// setItem replaces the call at key with a whole Responses output item.
func (spawns *spawnCalls) setItem(key int64, item ojson.Result) {
	if item.Get("type").String() != "function_call" {
		return
	}
	if call := spawns.calls[key]; call != nil {
		spawns.bytes -= call.arguments.Len()
		delete(spawns.calls, key)
	}
	spawns.add(key, item.Get("name").String(), item.Get("arguments").String())
	spawns.calls[key].namespace = item.Get("namespace").String()
}

func (spawns *spawnCalls) add(key int64, name, arguments string) {
	if spawns.calls == nil {
		spawns.calls = make(map[int64]*spawnCall)
	}
	call := spawns.calls[key]
	if call == nil {
		call = &spawnCall{}
		spawns.calls[key] = call
	}
	if name != "" {
		call.name = name
	}
	if spawns.bytes+len(arguments) > maxSpawnArgumentBytes {
		return
	}
	spawns.bytes += len(arguments)
	call.arguments.WriteString(arguments)
}

// prompts returns the prompt of each subagent tool call.
func (spawns *spawnCalls) prompts() []string {
	if spawns == nil {
		return nil
	}
	var prompts []string
	for _, call := range spawns.calls {
		field := ""
		switch {
		case spawns.deepSeek && deepSeekHarnessSpawnTools[call.name]:
			field = "prompt"
		case isPaseoCreateAgentTool(call.namespace, call.name):
			field = "initialPrompt"
		default:
			continue
		}
		if prompt := ojson.Get(call.arguments.String(), field); prompt.Type == ojson.String && strings.TrimSpace(prompt.Str) != "" {
			prompts = append(prompts, prompt.Str)
		}
	}
	return prompts
}

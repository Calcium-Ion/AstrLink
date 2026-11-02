package ingress

import (
	"crypto/sha256"
	"hash"
	"html"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Context observation reads what a call carried to the model from its
// decoded body, for the request records: how its input splits by source, the
// rules, memory, skills and MCP tools it carried, whether it asks for or
// continues from a compaction summary, the memory files its previous
// response wrote and read, the skills it loaded, and a tool call repeated
// while it kept failing. It only reads; nothing it finds changes what is
// forwarded.
//
// Text stays here. Items keep a digest of their content, and memory titles,
// index lines and skill descriptions leave as raw client text that the
// record session cleans like the input preview before a record keeps it.
// Only ContextTexts, which reads a body the audit view already shows,
// returns the texts themselves.

// contextObservation is what one request body showed. Its digests are plain
// SHA-256 of content: the record session keys them before they are stored.
type contextObservation struct {
	parts contract.ContextParts
	// stateful is set when the body only held what is new since a response
	// the provider keeps, so the parts cover part of the input.
	stateful  bool
	items     []observedItem
	system    []byte
	tools     []byte
	summary   bool
	compacted []byte
	project   string
	writes    []observedWrite
	reads     []observedRead
	skills    []string
	repeats   *contract.FailedRepeats
	// written are the writes before an index line takes in the file it
	// links, kept for ContextTexts.
	written []observedWrite
}

type observedItem struct {
	kind  contract.ContextItemKind
	name  string
	path  string
	scope contract.ContextItemScope
	// count is -1 when the item lists no entries.
	count int
	// names are a skills catalog's skills or the MCP servers.
	names []string
	// descriptions are what a skills catalog says of each of its skills,
	// in the order of names; tools are each MCP server's tools.
	descriptions []string
	tools        map[string][]string
	// entries are the lines of a memory index.
	entries []observedIndexEntry
	tokens  int
	digest  []byte
	// text is the content, kept for ContextTexts.
	text string
}

// observedIndexEntry is one line of a memory index, its text raw.
type observedIndexEntry struct {
	file  string
	title string
	hook  string
}

type observedWrite struct {
	kind contract.ContextItemKind
	name string
	path string
	// title, hook and content are raw client text: what the call wrote, the
	// whole file a write created or what an edit or patch added.
	title   string
	hook    string
	entry   string
	content string
}

// observedRead is a memory file a tool call read.
type observedRead struct {
	name string
	path string
	// content is what the tool returned, kept for ContextTexts.
	content string
}

// Images cost about this many tokens whatever their encoded size.
const contextImageTokens = 1600

// estimateTokens approximates the tokens of text: about four ASCII
// characters per token and one per other character, which is close for CJK.
// Raw JSON counts its escapes, a small excess.
func estimateTokens(text string) int {
	ascii, other := 0, 0
	for index := 0; index < len(text); index++ {
		switch b := text[index]; {
		case b < 0x80:
			ascii++
		case b >= 0xc0:
			other++
		}
	}
	return (ascii+3)/4 + other
}

// encryptedTokens guesses the tokens behind encrypted or encoded content,
// which is longer than the text it stands for.
func encryptedTokens(raw string) int { return len(raw) / 8 }

func resultTokens(value gjson.Result) int {
	if value.Type == gjson.String {
		return estimateTokens(value.Str)
	}
	return estimateTokens(value.Raw)
}

type textChannel int

const (
	channelUser textChannel = iota
	channelSystem
)

// observedCall is one tool call of the history and what its result said.
type observedCall struct {
	id       string
	name     string
	key      [32]byte
	args     gjson.Result
	exchange int
	resolved bool
	failed   bool
	// output is what the call returned, kept for ContextTexts.
	output gjson.Result
}

// contextWalk is the state of one pass over a body.
type contextWalk struct {
	observation  contextObservation
	itemIndex    map[string]int
	systemDigest hash.Hash
	systemSeen   bool
	toolsDigest  hash.Hash
	toolsSeen    bool
	mcp          *observedItem
	mcpDigest    hash.Hash
	calls        []observedCall
	callIndex    map[string]int
	exchange     int
	// newest is the exchange whose tool calls the body answers last; 0 when
	// the newest response made none.
	newest   int
	lastUser strings.Builder
	// texts keeps the content of items, writes and the compaction summary.
	texts   bool
	summary string
}

// observeRequestContext reads a decoded JSON request body. It returns nil for
// protocols without replayed history and for bodies that are not an object.
func observeRequestContext(protocol contract.ProtocolID, raw []byte) *contextObservation {
	w := walkRequestContext(protocol, raw, false)
	if w == nil {
		return nil
	}
	return w.finish()
}

func walkRequestContext(protocol contract.ProtocolID, raw []byte, texts bool) *contextWalk {
	var walk func(*contextWalk, gjson.Result)
	switch protocol {
	case contract.ProtocolAnthropicMessages:
		walk = walkAnthropicContext
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		walk = walkResponsesContext
	case contract.ProtocolOpenAIChat:
		walk = walkChatContext
	default:
		return nil
	}
	root := gjson.ParseBytes(raw)
	if !root.IsObject() {
		return nil
	}
	w := &contextWalk{
		itemIndex:    map[string]int{},
		callIndex:    map[string]int{},
		systemDigest: sha256.New(),
		toolsDigest:  sha256.New(),
		mcpDigest:    sha256.New(),
		texts:        texts,
	}
	walk(w, root)
	if protocol == contract.ProtocolOpenAIResponsesCompact {
		w.observation.summary = true
	}
	return w
}

func walkAnthropicContext(w *contextWalk, root gjson.Result) {
	system := root.Get("system")
	w.observeContent(system, &w.observation.parts.System, channelSystem, true)
	w.observeTools(root.Get("tools"))
	lastAssistantCalls := false
	root.Get("messages").ForEach(func(_, message gjson.Result) bool {
		content := message.Get("content")
		switch message.Get("role").Str {
		case "assistant":
			w.exchange++
			lastAssistantCalls = w.observeAnthropicAssistant(content)
		case "system":
			w.observeContent(content, &w.observation.parts.System, channelSystem, false)
		default:
			w.lastUser.Reset()
			w.observeAnthropicUser(content)
		}
		return true
	})
	if lastAssistantCalls {
		w.newest = w.exchange
	}
}

func (w *contextWalk) observeAnthropicAssistant(content gjson.Result) bool {
	parts := &w.observation.parts
	if content.Type == gjson.String {
		parts.History += estimateTokens(content.Str)
		return false
	}
	calls := false
	content.ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").Str {
		case "text":
			parts.History += estimateTokens(block.Get("text").Str)
		case "thinking":
			parts.History += estimateTokens(block.Get("thinking").Str)
		case "redacted_thinking":
			parts.Other += encryptedTokens(block.Get("data").Raw)
		case "tool_use":
			input := block.Get("input")
			parts.History += estimateTokens(input.Raw)
			w.addCall(block.Get("id").Str, block.Get("name").Str, "", input)
			calls = true
		default:
			parts.History += estimateTokens(block.Raw)
		}
		return true
	})
	return calls
}

func (w *contextWalk) observeAnthropicUser(content gjson.Result) {
	parts := &w.observation.parts
	if content.Type == gjson.String {
		w.observeText(content.Str, &parts.History, channelUser, false)
		return
	}
	content.ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").Str {
		case "text":
			w.observeText(block.Get("text").Str, &parts.History, channelUser, false)
		case "tool_result":
			result := block.Get("content")
			parts.ToolResults += resultTokens(result)
			w.resolveCall(block.Get("tool_use_id").Str, result, block.Get("is_error").Bool())
		case "image":
			parts.Other += contextImageTokens
		case "document":
			parts.Other += encryptedTokens(block.Raw)
		default:
			parts.History += estimateTokens(block.Raw)
		}
		return true
	})
}

func walkResponsesContext(w *contextWalk, root gjson.Result) {
	parts := &w.observation.parts
	w.observation.stateful = root.Get("previous_response_id").String() != ""
	if instructions := root.Get("instructions"); instructions.Type == gjson.String {
		w.observeText(instructions.Str, &parts.System, channelSystem, true)
	}
	w.observeTools(root.Get("tools"))
	input := root.Get("input")
	if input.Type == gjson.String {
		w.lastUser.Reset()
		w.observeText(input.Str, &parts.History, channelUser, false)
		return
	}
	opening, inResponse, lastResponseCall := true, false, false
	responseItem := func(call bool) {
		opening = false
		if !inResponse {
			w.exchange++
			inResponse = true
		}
		lastResponseCall = call
	}
	input.ForEach(func(_, item gjson.Result) bool {
		kind := item.Get("type").Str
		if kind == "" && item.Get("role").Exists() {
			kind = "message"
		}
		switch kind {
		case "message":
			switch item.Get("role").Str {
			case "assistant":
				responseItem(false)
				w.observeResponsesContent(item.Get("content"), &parts.History, channelUser)
			case "developer", "system":
				inResponse = false
				w.observeContentHashed(item.Get("content"), &parts.System, channelSystem, opening)
			default:
				inResponse, opening = false, false
				w.lastUser.Reset()
				w.observeResponsesContent(item.Get("content"), &parts.History, channelUser)
			}
		case "function_call", "custom_tool_call", "local_shell_call":
			responseItem(true)
			args := item.Get("arguments")
			name := item.Get("name").Str
			switch kind {
			case "custom_tool_call":
				args = item.Get("input")
			case "local_shell_call":
				args, name = item.Get("action"), "local_shell"
			}
			parts.History += resultTokens(args)
			w.addCall(item.Get("call_id").Str, name, item.Get("namespace").Str, args)
		case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
			inResponse, opening = false, false
			output := item.Get("output")
			parts.ToolResults += resultTokens(output)
			w.resolveCall(item.Get("call_id").Str, output, false)
		case "reasoning":
			responseItem(false)
			item.Get("summary").ForEach(func(_, summary gjson.Result) bool {
				parts.History += estimateTokens(summary.Get("text").Str)
				return true
			})
			parts.Other += encryptedTokens(item.Get("encrypted_content").Raw)
		case "compaction", "compaction_summary":
			inResponse, opening = false, false
			encrypted := item.Get("encrypted_content").Str
			parts.History += encryptedTokens(encrypted)
			if encrypted != "" {
				digest := sha256.Sum256([]byte(encrypted))
				w.observation.compacted = digest[:]
			}
		case "additional_tools":
			w.observeTools(item.Get("tools"))
		case "item_reference":
			parts.Other += estimateTokens(item.Raw)
		default:
			if strings.HasSuffix(kind, "_output") {
				inResponse, opening = false, false
				parts.ToolResults += estimateTokens(item.Raw)
			} else {
				responseItem(false)
				parts.History += estimateTokens(item.Raw)
			}
		}
		return true
	})
	if lastResponseCall {
		w.newest = w.exchange
	}
}

// observeResponsesContent reads a Responses message's content: a string, or
// input_text, output_text and media parts.
func (w *contextWalk) observeResponsesContent(content gjson.Result, part *int, channel textChannel) {
	if content.Type == gjson.String {
		w.observeText(content.Str, part, channel, false)
		return
	}
	content.ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").Str {
		case "input_text", "output_text", "text":
			w.observeText(block.Get("text").Str, part, channel, false)
		case "input_image":
			w.observation.parts.Other += contextImageTokens
		default:
			w.observation.parts.Other += encryptedTokens(block.Raw)
		}
		return true
	})
}

func walkChatContext(w *contextWalk, root gjson.Result) {
	parts := &w.observation.parts
	w.observeTools(root.Get("tools"))
	opening, lastAssistantCalls := true, false
	root.Get("messages").ForEach(func(_, message gjson.Result) bool {
		content := message.Get("content")
		switch message.Get("role").Str {
		case "system", "developer":
			w.observeContentHashed(content, &parts.System, channelSystem, opening)
		case "assistant":
			opening = false
			w.exchange++
			w.observeContent(content, &parts.History, channelUser, false)
			parts.History += estimateTokens(message.Get("reasoning_content").Str)
			lastAssistantCalls = false
			message.Get("tool_calls").ForEach(func(_, call gjson.Result) bool {
				args := call.Get("function.arguments")
				parts.History += resultTokens(args)
				w.addCall(call.Get("id").Str, call.Get("function.name").Str, "", args)
				lastAssistantCalls = true
				return true
			})
		case "tool", "function":
			opening = false
			parts.ToolResults += resultTokens(content)
			w.resolveCall(message.Get("tool_call_id").Str, content, false)
		default:
			opening = false
			w.lastUser.Reset()
			w.observeContent(content, &parts.History, channelUser, false)
		}
		return true
	})
	if lastAssistantCalls {
		w.newest = w.exchange
	}
}

// observeContent reads a string or an array of text and media blocks.
func (w *contextWalk) observeContent(content gjson.Result, part *int, channel textChannel, hashed bool) {
	if content.Type == gjson.String {
		w.observeText(content.Str, part, channel, hashed)
		return
	}
	content.ForEach(func(_, block gjson.Result) bool {
		switch {
		case block.Type == gjson.String:
			w.observeText(block.Str, part, channel, hashed)
		case block.Get("text").Type == gjson.String:
			w.observeText(block.Get("text").Str, part, channel, hashed)
		case strings.Contains(block.Get("type").Str, "image"):
			w.observation.parts.Other += contextImageTokens
		default:
			w.observation.parts.Other += encryptedTokens(block.Raw)
		}
		return true
	})
}

func (w *contextWalk) observeContentHashed(content gjson.Result, part *int, channel textChannel, hashed bool) {
	if content.Type == gjson.String {
		w.observeText(content.Str, part, channel, hashed)
		return
	}
	w.observeContent(content, part, channel, hashed)
}

// observeTools adds tool definitions to the tools part and their digest,
// and gathers the MCP tools among them: names under mcp__<server>__, or a
// Responses namespace named mcp__<server>.
func (w *contextWalk) observeTools(tools gjson.Result) {
	if !tools.IsArray() {
		return
	}
	w.toolsSeen = true
	_, _ = w.toolsDigest.Write([]byte(tools.Raw))
	tools.ForEach(func(_, tool gjson.Result) bool {
		tokens := estimateTokens(tool.Raw)
		w.observation.parts.Tools += tokens
		name := tool.Get("name").Str
		if name == "" {
			name = tool.Get("function.name").Str
		}
		server, ok := mcpServer(name)
		if !ok {
			return true
		}
		count := 1
		if tool.Get("type").Str == "namespace" {
			count = int(tool.Get("tools.#").Int())
		}
		if tool.Get("defer_loading").Bool() {
			// A deferred tool costs nothing until the model loads it.
			tokens = 0
		}
		if w.mcp == nil {
			w.mcp = &observedItem{kind: contract.ContextItemMCP, count: 0, tools: map[string][]string{}}
		}
		w.mcp.count += count
		w.mcp.tokens += tokens
		if !slices.Contains(w.mcp.names, server) {
			w.mcp.names = append(w.mcp.names, server)
		}
		if tool.Get("type").Str == "namespace" {
			tool.Get("tools").ForEach(func(_, nested gjson.Result) bool {
				w.mcp.addTool(server, nested.Get("name").Str)
				return true
			})
		} else {
			_, bare, _ := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
			w.mcp.addTool(server, bare)
		}
		_, _ = w.mcpDigest.Write([]byte(tool.Raw))
		return true
	})
}

// addTool notes one of an MCP server's tools, at most
// contract.MaxCatalogTools of each.
func (item *observedItem) addTool(server, name string) {
	if name == "" || len(item.tools[server]) >= contract.MaxCatalogTools || slices.Contains(item.tools[server], name) {
		return
	}
	item.tools[server] = append(item.tools[server], name)
}

// mcpServer reads the server out of an MCP tool or namespace name.
func mcpServer(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok || rest == "" {
		return "", false
	}
	server, _, _ := strings.Cut(rest, "__")
	server = strings.TrimSuffix(server, "_")
	return server, server != ""
}

// addCall remembers a tool call of the history in order.
func (w *contextWalk) addCall(id, name, namespace string, args gjson.Result) {
	key := sha256.New()
	_, _ = key.Write([]byte(namespace + "\x00" + name + "\x00" + args.Raw))
	call := observedCall{id: id, name: name, args: args, exchange: w.exchange}
	copy(call.key[:], key.Sum(nil))
	if id != "" {
		w.callIndex[id] = len(w.calls)
	}
	w.calls = append(w.calls, call)
}

// resolveCall notes a tool result: failed when the client flagged it or the
// output reports a non-zero exit code.
func (w *contextWalk) resolveCall(id string, output gjson.Result, flagged bool) {
	index, ok := w.callIndex[id]
	if !ok {
		return
	}
	call := &w.calls[index]
	call.resolved = true
	call.failed = flagged || outputFailed(output)
	if w.texts {
		call.output = output
	}
}

// toolExitCode finds the exit status coding agents append to shell output:
// Claude Code, Codex (text and JSON metadata), DeepSeek Harness and Pi.
var toolExitCode = regexp.MustCompile(`(?m)(?:^Exit code:? |Process exited with code |\[exit code: |Command exited with code |"exit_code":\s*)(\d+)`)

// exitWindow is how much of each end of an output may carry its exit code.
const exitWindow = 2048

// outputText is the text a tool result holds: a string, or the text blocks
// of an array.
func outputText(output gjson.Result) string {
	if output.Type == gjson.String {
		return output.Str
	}
	var builder strings.Builder
	output.ForEach(func(_, block gjson.Result) bool {
		if value := block.Get("text"); value.Type == gjson.String {
			builder.WriteString(value.Str)
			builder.WriteByte('\n')
		}
		return true
	})
	return builder.String()
}

func outputFailed(output gjson.Result) bool {
	text := outputText(output)
	check := func(window string) bool {
		for _, match := range toolExitCode.FindAllStringSubmatch(window, -1) {
			if match[1] != "0" {
				return true
			}
		}
		return false
	}
	if len(text) <= 2*exitWindow {
		return check(text)
	}
	return check(text[:exitWindow]) || check(text[len(text)-exitWindow:])
}

// observeText adds one text to part, less the rules, memory and skills it
// carries, which count as memory. It also notes the working directory and
// the compaction marks the text holds. hashed texts open the system prompt.
func (w *contextWalk) observeText(text string, part *int, channel textChannel, hashed bool) {
	if channel == channelUser {
		w.lastUser.WriteString(text)
		w.lastUser.WriteByte('\n')
		w.noteContinuation(text)
	}
	if w.observation.project == "" {
		w.noteProject(text)
	}
	spans := contextSpans(text, channel)
	if hashed && !strings.HasPrefix(text, "x-anthropic-billing-header:") {
		// The billing line Claude Code puts first changes with the client
		// version, not with what the model is told. The items keep their
		// own digests, so a changed file is told apart from the prompt.
		w.systemSeen = true
		at := 0
		for _, span := range spans {
			_, _ = w.systemDigest.Write([]byte(text[at:span.start]))
			at = span.end
		}
		_, _ = w.systemDigest.Write([]byte(text[at:]))
		_, _ = w.systemDigest.Write([]byte{0})
	}
	total := estimateTokens(text)
	memory := 0
	for _, span := range spans {
		memory += w.addItem(span)
	}
	w.observation.parts.Memory += memory
	*part += max(0, total-memory)
}

// Prefixes of the message that carries a compaction summary into the
// conversation after it.
var compactionContinuations = []string{
	"This session is being continued from a previous conversation",                        // Claude Code
	"Another language model started to solve this problem and produced a summary",         // Codex
	"This is an automatically generated checkpoint condensing",                            // DeepSeek Harness
	"The conversation history before this point was compacted into the following summary", // Pi
}

// What a call that asks for a compaction summary says last.
var compactionRequests = []string{
	"Your task is to create a detailed summary of the conversation so far", // Claude Code
	"CONTEXT CHECKPOINT COMPACTION",                                        // Codex
	"You are now acting as a compaction engine",                            // DeepSeek Harness
	"The messages above are a conversation to summarize",                   // Pi
	"The messages above are NEW conversation messages to incorporate",      // Pi, updating one
}

// noteContinuation keeps the first compaction summary the history carries;
// a later one would be text the user pasted.
func (w *contextWalk) noteContinuation(text string) {
	if w.observation.compacted != nil {
		return
	}
	if at := strings.LastIndex(text, "</system-reminder>"); at >= 0 {
		text = text[at+len("</system-reminder>"):]
	}
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, prefix := range compactionContinuations {
		if strings.HasPrefix(trimmed, prefix) {
			digest := sha256.Sum256([]byte(trimmed))
			w.observation.compacted = digest[:]
			if w.texts {
				w.summary = trimmed
			}
			return
		}
	}
}

// Where coding agents name the directory they work in: Claude Code (its
// environment updates add the one before), its older environment block,
// Codex and Pi, Codex's instructions header, and DeepSeek Harness's sandbox
// policy.
var contextProjectPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)Primary working directory: ([^\n]+?)(?: \(was [^\n]*\))?$`),
	regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]*)?Working directory: ([^\n]+)`),
	regexp.MustCompile(`<cwd>\s*([^<\n]+?)\s*</cwd>`),
	regexp.MustCompile(`(?m)^# AGENTS\.md instructions for ([^\n]+)$`),
	regexp.MustCompile(`session workspace: "([^"\n]+)"`),
}

func (w *contextWalk) noteProject(text string) {
	if !strings.Contains(text, "orking directory") && !strings.Contains(text, "<cwd>") &&
		!strings.Contains(text, "AGENTS.md instructions for") && !strings.Contains(text, "session workspace: ") {
		return
	}
	for _, pattern := range contextProjectPatterns {
		if match := pattern.FindStringSubmatch(text); match != nil {
			if project := strings.TrimRight(strings.TrimSpace(match[1]), `/\`); isAbsoluteContextPath(project) {
				w.observation.project = project
				return
			}
		}
	}
}

// itemSpan is an item found in a text, where it sits there and its content.
type itemSpan struct {
	start, end int
	item       observedItem
	content    string
}

// contextSpans finds the rules, memory and skills a text carries. Clients
// put them in system prompts and in reminders inside user messages; user
// text without a reminder only counts Codex's own instruction messages, so
// a pasted transcript does not.
func contextSpans(text string, channel textChannel) []itemSpan {
	reminder := channel == channelSystem || strings.Contains(text, "<system-reminder>")
	var spans []itemSpan
	if reminder && strings.Contains(text, "Contents of ") {
		spans = append(spans, claudeMDSpans(text)...)
	}
	if strings.Contains(text, "AGENTS.md instructions for ") || strings.Contains(text, "<user_instructions>") {
		spans = append(spans, codexInstructionSpans(text)...)
	}
	if reminder {
		if strings.Contains(text, "<skills_instructions>") {
			spans = append(spans, skillCatalogSpans(text, "<skills_instructions>", "</skills_instructions>")...)
		}
		if strings.Contains(text, claudeCodeSkillsIntro) {
			spans = append(spans, skillCatalogSpans(text, claudeCodeSkillsIntro, "\n\n")...)
		}
		if strings.Contains(text, "<available_skills>") {
			spans = append(spans, skillCatalogSpans(text, "<available_skills>", "</available_skills>")...)
		}
		if strings.Contains(text, "nstructions from: ") {
			spans = append(spans, harnessInstructionSpans(text)...)
		}
		if strings.Contains(text, "<project_instructions ") {
			spans = append(spans, piInstructionSpans(text)...)
		}
	}
	if len(spans) < 2 {
		return spans
	}
	// A file that quotes another client's markers must not count twice.
	slices.SortFunc(spans, func(left, right itemSpan) int { return left.start - right.start })
	kept := spans[:1]
	for _, span := range spans[1:] {
		if span.start >= kept[len(kept)-1].end {
			kept = append(kept, span)
		}
	}
	return kept
}

func newItemSpan(start, end int, kind contract.ContextItemKind, name, filePath string, scope contract.ContextItemScope, content string) itemSpan {
	content = strings.TrimSpace(content)
	digest := sha256.Sum256([]byte(content))
	count := -1
	var entries []observedIndexEntry
	if kind == contract.ContextItemMemory {
		count = listEntries(content)
		entries = memoryIndexEntries(content)
	}
	return itemSpan{start: start, end: end, content: content, item: observedItem{
		kind: kind, name: name, path: filePath, scope: scope, count: count,
		entries: entries, tokens: estimateTokens(content), digest: digest[:],
	}}
}

// memoryIndexEntries reads the lines of a memory index that link a memory
// file, "- [Title](file.md) — hook", each file once.
func memoryIndexEntries(content string) []observedIndexEntry {
	var entries []observedIndexEntry
	seen := map[string]bool{}
	for line := range strings.Lines(content) {
		trimmed := strings.TrimSpace(line)
		marked := memoryListMark.ReplaceAllString(trimmed, "")
		if marked == trimmed {
			// Only list lines are entries; a link in prose is not.
			continue
		}
		title, target, hook, ok := memoryLink(strings.TrimSpace(marked))
		if !ok {
			continue
		}
		file := contextBase(target)
		if !strings.EqualFold(path.Ext(file), ".md") || seen[strings.ToLower(file)] {
			continue
		}
		seen[strings.ToLower(file)] = true
		entries = append(entries, observedIndexEntry{file: file, title: title, hook: hook})
		if len(entries) == contract.MaxMemoryIndexEntries {
			break
		}
	}
	return entries
}

// listEntries counts the list lines of a memory index.
func listEntries(content string) int {
	count := 0
	for line := range strings.Lines(content) {
		if trimmed := strings.TrimLeft(line, " \t"); strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			count++
		}
	}
	return count
}

// claudeMDHeader opens each file in Claude Code's context reminder:
// "Contents of <path> (<what it is>):".
var claudeMDHeader = regexp.MustCompile(`(?m)^Contents of ([^\n]+?) \(([^\n]*)\):[ \t]*$`)

// The context reminder goes on to other sections after its files.
var claudeMDEnds = []string{"</system-reminder>", "\n# currentDate", "\n# gitStatus", "\n      IMPORTANT: this context may or may not be relevant"}

func claudeMDSpans(text string) []itemSpan {
	headers := claudeMDHeader.FindAllStringSubmatchIndex(text, -1)
	spans := make([]itemSpan, 0, len(headers))
	for index, header := range headers {
		start, contentStart := header[0], header[1]
		end := len(text)
		if index+1 < len(headers) {
			end = headers[index+1][0]
		}
		for _, marker := range claudeMDEnds {
			if at := strings.Index(text[contentStart:end], marker); at >= 0 {
				end = contentStart + at
			}
		}
		filePath, description := text[header[2]:header[3]], strings.ToLower(text[header[4]:header[5]])
		name := contextBase(filePath)
		kind, scope := contract.ContextItemRules, contract.ContextItemScope("")
		switch {
		case strings.EqualFold(name, "MEMORY.md") || strings.Contains(description, "memory"):
			kind = contract.ContextItemMemory
		case strings.Contains(description, "global"):
			scope = contract.ContextScopeGlobal
		case strings.Contains(description, "not checked in") || strings.Contains(description, "private project"):
			scope = contract.ContextScopeLocal
		case strings.Contains(description, "project"):
			scope = contract.ContextScopeProject
		}
		spans = append(spans, newItemSpan(start, end, kind, name, filePath, scope, text[contentStart:end]))
	}
	return spans
}

// Codex joins the user's global AGENTS.md and the project's with this line.
const codexProjectDocSeparator = "\n\n--- project-doc ---\n\n"

var codexInstructionsHeader = regexp.MustCompile(`(?m)^# AGENTS\.md instructions for ([^\n]+)$`)

func codexInstructionSpans(text string) []itemSpan {
	directory := ""
	open, close := "<user_instructions>", "</user_instructions>"
	if match := codexInstructionsHeader.FindStringSubmatch(text); match != nil {
		directory = strings.TrimRight(strings.TrimSpace(match[1]), `/\`)
		open, close = "<INSTRUCTIONS>", "</INSTRUCTIONS>"
	}
	start := strings.Index(text, open)
	if start < 0 {
		return nil
	}
	contentStart := start + len(open)
	end := len(text)
	if at := strings.Index(text[contentStart:], close); at >= 0 {
		end = contentStart + at
	}
	content := text[contentStart:end]
	global, project, joined := strings.Cut(content, codexProjectDocSeparator)
	if !joined {
		// One file, and the request does not say whose.
		return []itemSpan{newItemSpan(start, end, contract.ContextItemRules, "AGENTS.md", "", "", content)}
	}
	split := contentStart + len(global)
	projectPath := ""
	if directory != "" {
		projectPath = directory + "/AGENTS.md"
	}
	return []itemSpan{
		newItemSpan(start, split, contract.ContextItemRules, "AGENTS.md", "~/.codex/AGENTS.md", contract.ContextScopeGlobal, global),
		newItemSpan(split, end, contract.ContextItemRules, "AGENTS.md", projectPath, contract.ContextScopeProject, project),
	}
}

const claudeCodeSkillsIntro = "The following skills are available for use with the Skill tool:\n\n"

// skillNameLine is a catalog entry, "- name: description", the name in
// backticks for DeepSeek Harness. Plugin skills have colons in their names.
var skillNameLine = regexp.MustCompile("(?m)^- `?([^\\s`]+?)`?: ([^\n]*)")

var (
	skillNameTag        = regexp.MustCompile(`<name>\s*([^<\n]+?)\s*</name>`)
	skillBlockTag       = regexp.MustCompile(`(?s)<skill>(.*?)</skill>`)
	skillDescriptionTag = regexp.MustCompile(`(?s)<description>\s*(.*?)\s*</description>`)
	// skillFileNote is where Codex says a skill's file is, after its
	// description.
	skillFileNote = regexp.MustCompile(`\s*\(file: [^()\n]*\)\s*$`)
)

// skillEntries reads the skills of a catalog and what it says of each: the
// tagged entries of Pi, else the list lines of the others.
func skillEntries(content string) (names, descriptions []string) {
	for _, block := range skillBlockTag.FindAllStringSubmatch(content, -1) {
		name := skillNameTag.FindStringSubmatch(block[1])
		if name == nil {
			continue
		}
		description := ""
		if match := skillDescriptionTag.FindStringSubmatch(block[1]); match != nil {
			description = html.UnescapeString(match[1])
		}
		names, descriptions = append(names, name[1]), append(descriptions, description)
	}
	if len(names) == 0 {
		for _, match := range skillNameTag.FindAllStringSubmatch(content, -1) {
			names, descriptions = append(names, match[1]), append(descriptions, "")
		}
	}
	if len(names) == 0 {
		for _, match := range skillNameLine.FindAllStringSubmatch(content, -1) {
			names = append(names, match[1])
			descriptions = append(descriptions, skillFileNote.ReplaceAllString(match[2], ""))
		}
	}
	return names, descriptions
}

// skillCatalogSpans reads the catalog between open and close, the names
// from its entries.
func skillCatalogSpans(text, open, close string) []itemSpan {
	var spans []itemSpan
	offset := 0
	for {
		at := strings.Index(text[offset:], open)
		if at < 0 {
			return spans
		}
		start := offset + at
		contentStart := start + len(open)
		end := len(text)
		if close != "" {
			if found := strings.Index(text[contentStart:], close); found >= 0 {
				end = contentStart + found
			}
		}
		if found := strings.Index(text[contentStart:end], "</system-reminder>"); found >= 0 {
			end = contentStart + found
		}
		content := text[contentStart:end]
		names, descriptions := skillEntries(content)
		span := newItemSpan(start, end, contract.ContextItemSkills, "", "", "", content)
		span.item.count, span.item.names, span.item.descriptions = len(names), names, descriptions
		spans = append(spans, span)
		offset = max(end, contentStart)
	}
}

// harnessInstructionHeader opens each file DeepSeek Harness sends in its
// workspace instructions reminder.
var harnessInstructionHeader = regexp.MustCompile(`(?m)^(?:Additional instructions|Instructions) from: ([^\n]+)$`)

func harnessInstructionSpans(text string) []itemSpan {
	headers := harnessInstructionHeader.FindAllStringSubmatchIndex(text, -1)
	spans := make([]itemSpan, 0, len(headers))
	for index, header := range headers {
		start, contentStart := header[0], header[1]
		end := len(text)
		if index+1 < len(headers) {
			end = headers[index+1][0]
		}
		if at := strings.Index(text[contentStart:end], "</system-reminder>"); at >= 0 {
			end = contentStart + at
		}
		filePath := strings.ReplaceAll(text[header[2]:header[3]], `<\/`, `</`)
		scope := contract.ContextScopeProject
		if strings.HasPrefix(filePath, "~") || strings.HasPrefix(filePath, "$") {
			scope = contract.ContextScopeGlobal
		}
		spans = append(spans, newItemSpan(start, end, contract.ContextItemRules, contextBase(filePath), filePath, scope, text[contentStart:end]))
	}
	return spans
}

var piInstructionHeader = regexp.MustCompile(`<project_instructions path="([^"\n]+)">`)

func piInstructionSpans(text string) []itemSpan {
	headers := piInstructionHeader.FindAllStringSubmatchIndex(text, -1)
	spans := make([]itemSpan, 0, len(headers))
	for _, header := range headers {
		start, contentStart := header[0], header[1]
		end := len(text)
		if at := strings.Index(text[contentStart:], "</project_instructions>"); at >= 0 {
			end = contentStart + at
		}
		filePath := text[header[2]:header[3]]
		scope := contract.ContextScopeProject
		if globalAgentPath(filePath) {
			scope = contract.ContextScopeGlobal
		}
		spans = append(spans, newItemSpan(start, end, contract.ContextItemRules, contextBase(filePath), filePath, scope, text[contentStart:end]))
	}
	return spans
}

// globalAgentPath tells a file in an agent's own home directory.
func globalAgentPath(filePath string) bool {
	normalized := strings.ReplaceAll(filePath, `\`, "/")
	for _, home := range []string{"/.pi/", "/.agents/", "/.claude/", "/.codex/", "/.dsh/"} {
		if strings.Contains(normalized, home) {
			return true
		}
	}
	return false
}

// addItem merges an item into the body's list: a file sent twice counts its
// tokens twice and keeps its newest content. It returns the item's tokens.
func (w *contextWalk) addItem(span itemSpan) int {
	item := span.item
	if w.texts {
		item.text = span.content
	}
	key := string(item.kind) + "\x00" + item.path + "\x00" + item.name
	if index, ok := w.itemIndex[key]; ok {
		existing := &w.observation.items[index]
		existing.tokens += item.tokens
		existing.digest, existing.count, existing.names, existing.text = item.digest, item.count, item.names, item.text
		existing.descriptions, existing.entries = item.descriptions, item.entries
		return item.tokens
	}
	w.itemIndex[key] = len(w.observation.items)
	w.observation.items = append(w.observation.items, item)
	return item.tokens
}

// finish settles what needs the whole body: item paths against the project,
// the MCP item, the summary request, and the tool calls.
func (w *contextWalk) finish() *contextObservation {
	observation := &w.observation
	if w.systemSeen {
		observation.system = w.systemDigest.Sum(nil)
	}
	if w.toolsSeen {
		observation.tools = w.toolsDigest.Sum(nil)
	}
	for index := range observation.items {
		item := &observation.items[index]
		item.path = resolveContextPath(observation.project, item.path)
	}
	observation.project = projectRoot(observation.project, observation.items)
	limit := contract.MaxContextItems
	if w.mcp != nil {
		limit--
	}
	if len(observation.items) > limit {
		// What the dropped items carried stays in the memory part.
		observation.items = observation.items[:limit]
	}
	if w.mcp != nil {
		w.mcp.digest = w.mcpDigest.Sum(nil)
		slices.Sort(w.mcp.names)
		observation.items = append(observation.items, *w.mcp)
	}
	last := w.lastUser.String()
	for _, marker := range compactionRequests {
		if strings.Contains(last, marker) {
			observation.summary = true
			break
		}
	}
	observation.repeats = failedRepeats(w.calls)
	if w.newest > 0 {
		for _, call := range w.calls {
			if call.exchange == w.newest && call.resolved && !call.failed {
				observation.writes = append(observation.writes, memoryWrites(call, observation.project)...)
				observation.noteRead(call)
			}
		}
		if w.texts {
			observation.written = slices.Clone(observation.writes)
		}
		observation.writes = mergeMemoryWrites(observation.writes)
	}
	return observation
}

// noteRead notes the memory file a successful tool call read, or the skill
// it loaded: Claude Code's Skill tool, or a read of a skill's SKILL.md.
// Each file and skill counts once, at most contract.MaxMemoryReads files
// and contract.MaxSkillsUsed skills.
func (observation *contextObservation) noteRead(call observedCall) {
	args := callArgs(call.args)
	if strings.EqualFold(call.name, "Skill") {
		observation.noteSkill(firstString(args, "skill"))
		return
	}
	target := readTarget(call.name, args)
	if target == "" {
		return
	}
	resolved := resolveContextPath(observation.project, target)
	name := contextBase(resolved)
	if strings.EqualFold(name, "SKILL.md") {
		observation.noteSkill(contextBase(contextDir(resolved)))
		return
	}
	kind, index, ok := memoryFileKind(resolved)
	if !ok || index || kind != contract.ContextItemMemory || len(observation.reads) >= contract.MaxMemoryReads ||
		slices.ContainsFunc(observation.reads, func(read observedRead) bool { return read.path == resolved }) {
		return
	}
	read := observedRead{name: name, path: resolved}
	if call.output.Exists() {
		read.content = readResultText(call.output)
	}
	observation.reads = append(observation.reads, read)
}

func (observation *contextObservation) noteSkill(name string) {
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if name == "" || len(observation.skills) >= contract.MaxSkillsUsed || slices.Contains(observation.skills, name) {
		return
	}
	observation.skills = append(observation.skills, name)
}

// callArgs is a tool call's arguments as an object, parsed when the
// protocol sends them as a JSON string.
func callArgs(args gjson.Result) gjson.Result {
	if args.Type == gjson.String && strings.HasPrefix(strings.TrimSpace(args.Str), "{") {
		return gjson.Parse(args.Str)
	}
	return args
}

// readTarget is the one file a tool call reads whole or in part: the read
// tools of Claude Code, DeepSeek Harness, Pi and others, the text editor
// tool's view, and a shell command that only prints one file.
func readTarget(name string, args gjson.Result) string {
	switch strings.ToLower(name) {
	case "read", "read_file", "readfile":
		return firstString(args, "file_path", "path", "filePath", "absolute_path")
	case "str_replace_editor", "str_replace_based_edit_tool":
		if args.Get("command").Str == "view" {
			return firstString(args, "path", "file_path")
		}
		return ""
	}
	if shellTool(name) {
		return shellReadTarget(shellCommand(args))
	}
	return ""
}

// shellCommand is the script a shell tool call runs: a command string, or
// the script of an argument list that runs one through a shell.
func shellCommand(args gjson.Result) string {
	if args.Type == gjson.String {
		return args.Str
	}
	command := args.Get("command")
	if !command.Exists() {
		command = args.Get("cmd")
	}
	if command.Type == gjson.String {
		return command.Str
	}
	parts := command.Array()
	if len(parts) == 3 && (parts[1].Str == "-c" || parts[1].Str == "-lc") {
		return parts[2].Str
	}
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		words = append(words, part.Str)
	}
	return strings.Join(words, " ")
}

// sedPrintRange is a sed script that prints lines: "1,200p".
var sedPrintRange = regexp.MustCompile(`^\d+(?:,\d+)?p$`)

// shellReadTarget is the file a shell command only prints: cat with one
// file, sed -n printing a range of one, or head of one. Anything that pipes,
// chains, redirects or expands is left alone.
func shellReadTarget(command string) string {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|;&<>`$(){}\n\\") {
		return ""
	}
	words, ok := shellWords(command)
	if !ok || len(words) < 2 {
		return ""
	}
	var files []string
	args := words[1:]
	switch words[0] {
	case "cat":
		for _, word := range args {
			if !strings.HasPrefix(word, "-") {
				files = append(files, word)
			}
		}
	case "head":
		for index := 0; index < len(args); index++ {
			switch word := args[index]; {
			case word == "-n" || word == "-c":
				index++
			case strings.HasPrefix(word, "-"):
			default:
				files = append(files, word)
			}
		}
	case "sed":
		if len(args) != 3 || args[0] != "-n" || !sedPrintRange.MatchString(args[1]) {
			return ""
		}
		files = args[2:]
	}
	if len(files) != 1 {
		return ""
	}
	return files[0]
}

// shellWords splits a command into words, honoring single and double
// quotes. ok is false when a quote is left open.
func shellWords(command string) (words []string, ok bool) {
	var word strings.Builder
	inWord := false
	var quote rune
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case unicode.IsSpace(r):
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, true
}

// readLineNumber is the line number read tools put in front of each line:
// spaces, digits, then a tab or an arrow.
var readLineNumber = regexp.MustCompile(`^ *\d+(?:\t|→)`)

// readResultText is a file as a read tool returned it, without the line
// numbers in front of its lines, the reminders a client appends, or the
// header Codex puts above a command's output.
func readResultText(output gjson.Result) string {
	text := outputText(output)
	if at := strings.Index(text, "<system-reminder>"); at >= 0 {
		text = text[:at]
	}
	if at := strings.Index(text, "\nOutput:\n"); at >= 0 && at < 512 && strings.HasPrefix(text, "Chunk ID:") {
		text = text[at+len("\nOutput:\n"):]
	}
	lines := strings.SplitAfter(text, "\n")
	numbered := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !readLineNumber.MatchString(line) {
			return strings.TrimRight(text, "\n") + "\n"
		}
		numbered++
	}
	if numbered == 0 {
		return text
	}
	var builder strings.Builder
	for _, line := range lines {
		if location := readLineNumber.FindStringIndex(line); location != nil {
			line = line[location[1]:]
		}
		builder.WriteString(line)
	}
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

// mergeMemoryWrites folds a new memory file into the index line that links
// it, as Claude Code writes the file and then adds its line to MEMORY.md,
// and keeps at most contract.MaxMemoryWrites.
func mergeMemoryWrites(writes []observedWrite) []observedWrite {
	linked := map[string]bool{}
	for _, write := range writes {
		if write.entry != "" {
			linked[strings.ToLower(write.entry)] = true
		}
	}
	kept := writes[:0]
	for _, write := range writes {
		if write.entry == "" && write.kind == contract.ContextItemMemory && linked[strings.ToLower(write.name)] {
			continue
		}
		if len(kept) < contract.MaxMemoryWrites {
			kept = append(kept, write)
		}
	}
	return kept
}

// failedRepeats counts the newest tool calls that repeated the same call
// and failed each time.
func failedRepeats(calls []observedCall) *contract.FailedRepeats {
	count := 0
	var last *observedCall
	for index := len(calls) - 1; index >= 0; index-- {
		call := &calls[index]
		if !call.resolved {
			continue
		}
		if !call.failed || (last != nil && call.key != last.key) {
			break
		}
		last = call
		count++
	}
	if last == nil || count < contract.MinFailedRepeats {
		return nil
	}
	name := contract.OutputToolName(last.name)
	if name == "" {
		return nil
	}
	return &contract.FailedRepeats{Tool: name, Count: count, Command: shellTool(last.name)}
}

func shellTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "shell_command", "exec_command", "local_shell", "powershell", "pwsh",
		"run_terminal_cmd", "execute_command", "terminal":
		return true
	default:
		return false
	}
}

// memoryWrites reads the memory and rules files a successful tool call
// wrote: Write, Edit and MultiEdit (Claude Code, DeepSeek Harness, Pi),
// apply_patch (Codex), and the text editor tool.
func memoryWrites(call observedCall, project string) []observedWrite {
	args := callArgs(call.args)
	var changes []fileChange
	switch strings.ToLower(call.name) {
	case "apply_patch":
		patch := args.Str
		if args.IsObject() {
			patch = firstString(args, "input", "patch")
		}
		changes = patchChanges(patch)
	case "write", "write_file", "create_file":
		changes = []fileChange{{path: firstString(args, "file_path", "path", "filePath"), added: firstString(args, "content", "file_text"), created: true}}
	case "edit", "edit_file", "multiedit", "multi_edit", "str_replace_editor", "str_replace_based_edit_tool":
		changes = []fileChange{editChange(args)}
	}
	var writes []observedWrite
	for _, change := range changes {
		resolved := resolveContextPath(project, change.path)
		kind, index, ok := memoryFileKind(resolved)
		if !ok || (!index && !change.created) {
			// Editing a memory file updates a memory; only a new one counts.
			continue
		}
		title, entry, hook := memoryTitle(change.added)
		if index && title == "" {
			continue
		}
		writes = append(writes, observedWrite{
			kind: kind, name: contextBase(resolved), path: resolved, title: title, hook: hook, entry: entry, content: change.added,
		})
	}
	return writes
}

// fileChange is what a tool call added to one file.
type fileChange struct {
	path    string
	added   string
	created bool
}

func firstString(object gjson.Result, keys ...string) string {
	for _, key := range keys {
		if value := object.Get(key); value.Type == gjson.String {
			return value.Str
		}
	}
	return ""
}

// editChange reads the lines an edit added: those of its new text that its
// old text lacked, over every replacement it makes.
func editChange(args gjson.Result) fileChange {
	change := fileChange{path: firstString(args, "file_path", "path", "filePath")}
	if args.Get("command").Str == "create" {
		change.added, change.created = firstString(args, "file_text"), true
		return change
	}
	var added strings.Builder
	addPair := func(edit gjson.Result) {
		old := firstString(edit, "old_string", "oldText", "old_str")
		replacement := firstString(edit, "new_string", "newText", "new_str")
		existing := map[string]bool{}
		for line := range strings.Lines(old) {
			existing[strings.TrimSpace(line)] = true
		}
		for line := range strings.Lines(replacement) {
			if trimmed := strings.TrimSpace(line); trimmed != "" && !existing[trimmed] {
				added.WriteString(trimmed)
				added.WriteByte('\n')
			}
		}
	}
	addPair(args)
	args.Get("edits").ForEach(func(_, edit gjson.Result) bool {
		addPair(edit)
		return true
	})
	change.added = added.String()
	return change
}

// patchChanges reads the files an apply_patch envelope adds to or updates,
// with the lines it adds to each.
func patchChanges(patch string) []fileChange {
	var changes []fileChange
	var current *fileChange
	var added strings.Builder
	flush := func() {
		if current != nil {
			current.added = added.String()
			changes = append(changes, *current)
		}
		current = nil
		added.Reset()
	}
	for line := range strings.Lines(patch) {
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			flush()
			current = &fileChange{path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: ")), created: true}
		case strings.HasPrefix(line, "*** Update File: "):
			flush()
			current = &fileChange{path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
		case strings.HasPrefix(line, "*** "):
			flush()
		case current != nil && strings.HasPrefix(line, "+"):
			added.WriteString(line[1:])
			added.WriteByte('\n')
		}
	}
	flush()
	return changes
}

// memoryFileKind tells a memory index (MEMORY.md), a rules file the agent
// reads each session, and a file of an agent's memory directory: a markdown
// file under a memory directory inside a hidden one, such as
// ~/.claude/projects/<project>/memory/.
func memoryFileKind(filePath string) (kind contract.ContextItemKind, index, ok bool) {
	name := contextBase(filePath)
	switch strings.ToLower(name) {
	case "memory.md":
		return contract.ContextItemMemory, true, true
	case "claude.md", "agents.md", "claude.local.md", "agents.override.md":
		return contract.ContextItemRules, true, true
	}
	if !strings.EqualFold(path.Ext(name), ".md") {
		return "", false, false
	}
	segments := strings.FieldsFunc(filePath, func(r rune) bool { return r == '/' || r == '\\' })
	hidden, memory := false, false
	for _, segment := range segments[:max(0, len(segments)-1)] {
		hidden = hidden || (strings.HasPrefix(segment, ".") && len(segment) > 1 && segment != "..")
		memory = memory || strings.EqualFold(segment, "memory") || strings.EqualFold(segment, "memories")
	}
	if hidden && memory {
		return contract.ContextItemMemory, false, true
	}
	return "", false, false
}

var (
	memoryLinkLine  = regexp.MustCompile(`^\[([^\]\n]+)\]\(([^)\s]+)\)`)
	memoryListMark  = regexp.MustCompile(`^(?:[-*+>#]+|\d+[.)])\s*`)
	frontMatterLine = regexp.MustCompile(`(?m)^(name|title|description):\s*["']?([^"'\n]+?)["']?\s*$`)
)

// memoryTitle picks the entry a write added: the first line with words,
// list marks and link syntax removed. A memory index line links its file,
// returned as entry, and says what it holds after the link, returned as
// hook. A new memory file with front matter is named by its first heading,
// else its name, title or description.
func memoryTitle(added string) (title, entry, hook string) {
	text := strings.TrimSpace(added)
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if front, body, closed := strings.Cut(rest, "\n---"); closed {
			if heading := firstMarkdownHeading(body); heading != "" {
				return heading, "", ""
			}
			values := map[string]string{}
			for _, match := range frontMatterLine.FindAllStringSubmatch(front, -1) {
				values[match[1]] = match[2]
			}
			for _, key := range []string{"name", "title", "description"} {
				if values[key] != "" {
					return values[key], "", ""
				}
			}
			text = body
		}
	}
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(memoryListMark.ReplaceAllString(strings.TrimSpace(line), ""))
		if title, target, hook, ok := memoryLink(line); ok {
			if target = contextBase(target); strings.EqualFold(path.Ext(target), ".md") {
				entry = target
			}
			return title, entry, hook
		}
		if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			return line, "", ""
		}
	}
	return "", "", ""
}

// memoryLink reads a memory index line with its list mark removed: the
// title, the file it links, and what it says after the link, less the dash
// or colon that leads in.
func memoryLink(line string) (title, target, hook string, ok bool) {
	match := memoryLinkLine.FindStringSubmatch(line)
	if match == nil {
		return "", "", "", false
	}
	hook = strings.TrimLeft(strings.TrimSpace(line[len(match[0]):]), "—–-:|")
	return match[1], match[2], strings.TrimSpace(hook), true
}

func firstMarkdownHeading(text string) string {
	for line := range strings.Lines(text) {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "#"); ok {
			if heading = strings.TrimSpace(strings.TrimLeft(heading, "#")); heading != "" {
				return heading
			}
		}
	}
	return ""
}

// contextBase is the last element of a Unix or Windows path.
func contextBase(filePath string) string {
	filePath = strings.TrimRight(filePath, `/\`)
	if at := strings.LastIndexAny(filePath, `/\`); at >= 0 {
		return filePath[at+1:]
	}
	return filePath
}

var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func isAbsoluteContextPath(filePath string) bool {
	return strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "~") ||
		strings.HasPrefix(filePath, `\\`) || windowsAbsolutePath.MatchString(filePath)
}

// projectRoot moves the working directory up to the outermost directory
// whose project rules the call carried. Agents name the directory their
// shell is in, which wanders into subdirectories, while rules files sit at
// the project's root. A rules file in a home directory, or above one, names
// no project. Without a working directory the rules alone name the root.
func projectRoot(directory string, items []observedItem) string {
	root := directory
	for _, item := range items {
		if item.kind != contract.ContextItemRules || !isAbsoluteContextPath(item.path) ||
			(item.scope != contract.ContextScopeProject && item.scope != contract.ContextScopeLocal) {
			continue
		}
		parent := contextDir(item.path)
		if parent == "" || homeOrAbove(parent) {
			continue
		}
		switch {
		case root == "":
			root = parent
		case directory == "" && withinContextDir(root, parent):
			root = parent
		case directory != "" && withinContextDir(directory, parent) && withinContextDir(root, parent):
			root = parent
		}
	}
	return root
}

// contextDir is the directory of a Unix or Windows path.
func contextDir(filePath string) string {
	filePath = strings.TrimRight(filePath, `/\`)
	if at := strings.LastIndexAny(filePath, `/\`); at > 0 {
		return filePath[:at]
	}
	return ""
}

// withinContextDir tells whether filePath is directory or inside it.
func withinContextDir(filePath, directory string) bool {
	normalize := func(value string) string {
		return strings.TrimRight(strings.ReplaceAll(value, `\`, "/"), "/")
	}
	filePath, directory = normalize(filePath), normalize(directory)
	return filePath == directory || strings.HasPrefix(filePath, directory+"/")
}

// aboveHomes matches the directories that hold the users' homes.
var aboveHomes = regexp.MustCompile(`(?i)^(?:|~|/users|/home|[a-z]:|[a-z]:/users|[a-z]:/documents and settings)$`)

// homeOrAbove tells a home directory, or a directory that holds homes.
func homeOrAbove(directory string) bool {
	if location := homeDirectory.FindStringIndex(directory); location != nil {
		return strings.TrimRight(directory[location[1]:], `/\`) == ""
	}
	return aboveHomes.MatchString(strings.TrimRight(strings.ReplaceAll(directory, `\`, "/"), "/"))
}

// resolveContextPath joins a path the request gave relative to the project
// onto it.
func resolveContextPath(project, filePath string) string {
	if filePath == "" || project == "" || isAbsoluteContextPath(filePath) || strings.HasPrefix(filePath, "$") {
		return filePath
	}
	return strings.TrimRight(project, `/\`) + "/" + strings.TrimPrefix(filePath, "./")
}

// homeDirectory matches a user's home directory on macOS, Linux and
// Windows. The client may run on another host, so its home is read from
// the path rather than from this process.
var homeDirectory = regexp.MustCompile(`^(?:/Users/[^/]+|/home/[^/]+|/root|[A-Za-z]:[\\/](?:Users|Documents and Settings)[\\/][^\\/]+)(?:[\\/]|$)`)

// homePath writes filePath with the home directory as ~, so stored paths do
// not carry the user's account name. Claude Code names a project's directory
// after its path with every other character turned into -, so the home in
// that form becomes ~ too.
func homePath(filePath string) string {
	location := homeDirectory.FindStringIndex(filePath)
	if location == nil {
		return filePath
	}
	home, rest := filePath[:location[1]], filePath[location[1]:]
	if rest == "" {
		return "~"
	}
	encoded := strings.Map(func(r rune) rune {
		if r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return '-'
	}, strings.TrimRight(home, `/\`))
	rest = strings.ReplaceAll(rest, encoded+"-", "~-")
	return "~" + home[len(home)-1:] + rest
}

// contextFingerprintInfo separates the key of the request context digests
// from the session and link keys, so equal content never yields an equal
// value across them.
const contextFingerprintInfo = "astrlink/context-fingerprint/v1"

// observeContext keys what the request body carried for its records.
// Without a key, when audit storage is unavailable, the records keep the
// context without digests.
func (session *recordSession) observeContext(observation *contextObservation, fingerprinter *convo.Fingerprinter) {
	if session == nil || observation == nil {
		return
	}
	digest := func(sum []byte) string {
		if fingerprinter == nil || len(sum) == 0 {
			return ""
		}
		return fingerprinter.Fingerprint(sum)
	}
	requestContext := &contract.RequestContext{
		SystemHash:    digest(observation.system),
		ToolsHash:     digest(observation.tools),
		Summary:       observation.summary,
		Compacted:     digest(observation.compacted),
		FailedRepeats: observation.repeats,
	}
	if !observation.stateful {
		parts := observation.parts
		requestContext.Parts = &parts
	}
	for _, item := range observation.items {
		converted := contract.ContextItem{
			Kind:   item.kind,
			Name:   contextText(item.name, contract.MaxContextNameRunes),
			Path:   contextText(homePath(item.path), contract.MaxContextPathRunes),
			Scope:  item.scope,
			Tokens: item.tokens,
			Hash:   digest(item.digest),
		}
		if item.count >= 0 {
			count := item.count
			converted.Count = &count
		}
		names := contextNames(item.names, contract.MaxContextNames)
		if item.kind == contract.ContextItemMCP {
			converted.Servers = contextNames(names, contract.MaxContextServers)
			for index, server := range converted.Servers {
				converted.Servers[index] = contract.ClampRunes(server, contract.MaxContextServerRunes)
			}
		} else {
			converted.Names = names
		}
		requestContext.Items = append(requestContext.Items, converted)
		switch item := item; item.kind {
		case contract.ContextItemMemory:
			if session.contextIndex == nil && len(item.entries) > 0 {
				session.contextIndex = item.entries
			}
		case contract.ContextItemSkills:
			session.contextSkills = &item
		case contract.ContextItemMCP:
			session.contextMCP = &item
		}
	}
	for _, write := range observation.writes {
		name := contextText(write.name, contract.MaxContextNameRunes)
		if name == "" {
			continue
		}
		requestContext.MemoryWrites = append(requestContext.MemoryWrites, contract.MemoryWrite{
			Kind:  write.kind,
			Name:  name,
			Path:  contextText(homePath(write.path), contract.MaxContextPathRunes),
			Entry: contextText(write.entry, contract.MaxContextNameRunes),
		})
		session.contextTitles = append(session.contextTitles, [2]string{write.title, write.hook})
	}
	for _, read := range observation.reads {
		if name := contextText(read.name, contract.MaxContextNameRunes); name != "" {
			requestContext.MemoryReads = append(requestContext.MemoryReads, contract.MemoryRead{
				Name: name, Path: contextText(homePath(read.path), contract.MaxContextPathRunes),
			})
		}
	}
	requestContext.SkillsUsed = contextNames(observation.skills, contract.MaxSkillsUsed)
	session.context = requestContext
	session.project = contextText(homePath(observation.project), contract.MaxContextPathRunes)
}

// recordContext is the context a record carries: the estimates of the body,
// which the store scales to the input the call reports, its memory titles
// cleaned like the input preview, and whether the session placed it as a
// helper call. Once the call ended it also carries the memory index lines
// and the catalog, cleaned the same way, for the store to keep once; while
// it runs a privacy decision may still be pending.
func (session *recordSession) recordContext() *contract.RequestContext {
	if session.context == nil {
		return nil
	}
	requestContext := *session.context
	requestContext.Helper = session.sideCall
	if len(requestContext.MemoryWrites) > 0 {
		writes := slices.Clone(requestContext.MemoryWrites)
		for index := range writes {
			raw := session.contextTitles[index]
			writes[index].Title = session.visibleContextTitle(raw[0])
			writes[index].Hook = session.visibleContextTitle(raw[1])
		}
		requestContext.MemoryWrites = writes
	}
	if session.status != contract.RequestStatusPending {
		requestContext.MemoryIndex = session.memoryIndex()
		requestContext.Catalog = session.contextCatalog()
	}
	return &requestContext
}

// memoryIndex is the memory index lines the call carried, cleaned.
func (session *recordSession) memoryIndex() []contract.MemoryIndexEntry {
	var index []contract.MemoryIndexEntry
	for _, entry := range session.contextIndex {
		if file := contextText(entry.file, contract.MaxContextNameRunes); file != "" {
			index = append(index, contract.MemoryIndexEntry{
				File: file, Title: session.visibleContextTitle(entry.title), Hook: session.visibleContextTitle(entry.hook),
			})
		}
	}
	return index
}

// contextCatalog is what the call's skills catalog says of each skill,
// cleaned, and the tools of each MCP server; nil when it carried neither.
func (session *recordSession) contextCatalog() *contract.ContextCatalog {
	catalog := &contract.ContextCatalog{}
	if skills := session.contextSkills; skills != nil {
		seen := map[string]bool{}
		for index, raw := range skills.names {
			name := contextText(raw, contract.MaxContextNameRunes)
			if name == "" || seen[name] || len(catalog.Skills) == contract.MaxCatalogSkills {
				continue
			}
			seen[name] = true
			skill := contract.CatalogSkill{Name: name}
			if index < len(skills.descriptions) {
				skill.Description = session.visibleContextTitle(skills.descriptions[index])
			}
			catalog.Skills = append(catalog.Skills, skill)
		}
	}
	if mcp := session.contextMCP; mcp != nil {
		for _, raw := range mcp.names {
			server := contract.ClampRunes(contextText(raw, contract.MaxContextNameRunes), contract.MaxContextServerRunes)
			if server == "" || len(catalog.MCP) == contract.MaxContextServers {
				continue
			}
			var tools []string
			for _, tool := range mcp.tools[raw] {
				if name := contract.OutputToolName(tool); name != "" && !slices.Contains(tools, name) {
					tools = append(tools, name)
				}
			}
			catalog.MCP = append(catalog.MCP, contract.CatalogServer{Server: server, Tools: tools})
		}
	}
	if len(catalog.Skills) == 0 && len(catalog.MCP) == 0 {
		return nil
	}
	return catalog
}

// visibleContextTitle cleans a memory title like the input preview: secrets
// and URLs cut, masked once a redact or block decision withheld the body,
// and absent while a gated body has no decision.
func (session *recordSession) visibleContextTitle(raw string) string {
	switch session.privacyDecision {
	case contract.PrivacyDecisionRedact, contract.PrivacyDecisionBlock:
		raw = maskProtected(raw, session.protectedValues)
	default:
		if session.auditExposure(storage.AuditDirectionRequest) != storage.AuditExposureShareable {
			return ""
		}
	}
	return sanitizePreview(raw)
}

// contextText bounds client text a record keeps: valid UTF-8 without
// control characters, at most maxRunes runes.
func contextText(value string, maxRunes int) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, ""))
	return contract.ClampRunes(strings.TrimSpace(value), maxRunes)
}

// contextNames bounds a list of names, leaving out the empty ones.
func contextNames(names []string, limit int) []string {
	var kept []string
	for _, name := range names {
		if name = contextText(name, contract.MaxContextNameRunes); name != "" && len(kept) < limit {
			kept = append(kept, name)
		}
	}
	return kept
}

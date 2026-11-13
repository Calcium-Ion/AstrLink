package ingress

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonedit"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
)

// reasoningRanks orders reasoning levels from least to most reasoning.
var reasoningRanks = map[string]int{
	"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6,
}

func reasoningRank(level string) int {
	if rank, ok := reasoningRanks[strings.ToLower(strings.TrimSpace(level))]; ok {
		return rank
	}
	return -1
}

// levelBudgets are the thinking budgets of the settings' levels, after
// OmniRoute's EFFORT_BUDGETS. Medium is 8192 rather than 10240, which
// RelayKit reads back as high, and high is 32768 rather than 131072, which
// would make it max.
var levelBudgets = map[contract.ReasoningLevel]int{
	contract.ReasoningLevelOff:    0,
	contract.ReasoningLevelLow:    1024,
	contract.ReasoningLevelMedium: 8192,
	contract.ReasoningLevelHigh:   32768,
	contract.ReasoningLevelMax:    131072,
}

// budgetLevel names the level a thinking budget stands for, in RelayKit's
// vocabulary.
func budgetLevel(budget int) string {
	switch {
	case budget <= 0:
		return "none"
	case budget <= 1024:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 32768:
		return "high"
	case budget < 131072:
		return "xhigh"
	default:
		return "max"
	}
}

// adaptiveSignals are the request traits OmniRoute's adaptive budget grows
// with, read from the upstream body.
type adaptiveSignals struct {
	// messages counts Chat and Messages messages, Responses input items (a
	// string input is one) or Gemini contents.
	messages int
	// tools counts the tools, or the function declarations of Gemini's.
	tools int
	// lastUserChars is the length of the last user message without inline
	// base64 images: the text of a string, the JSON of anything else.
	lastUserChars int
}

// hits counts the signals a request shows at OmniRoute's thresholds: more
// than 10 messages, more than 3 tools and a last user message over 2000
// characters. OmniRoute weighs them 0.5, 0.5 and 0.3 to scale one budget.
// Here each one moves the level a step up the adaptive range.
func (signals adaptiveSignals) hits() int {
	hits := 0
	for _, hit := range []bool{signals.messages > 10, signals.tools > 3, signals.lastUserChars > 2000} {
		if hit {
			hits++
		}
	}
	return hits
}

func requestSignals(protocol contract.ProtocolID, body []byte) adaptiveSignals {
	var signals adaptiveSignals
	var messages ojson.Result
	content := "content"
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages:
		messages = ojson.GetBytes(body, "messages")
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		messages = ojson.GetBytes(body, "input")
		if messages.Type == ojson.String {
			signals.messages, signals.lastUserChars = 1, textLength(messages.Str)
		}
	case contract.ProtocolGoogleGenerateContent:
		messages, content = ojson.GetBytes(body, "contents"), "parts"
	}
	if messages.IsArray() {
		items := messages.Array()
		signals.messages = len(items)
		for index := len(items) - 1; index >= 0; index-- {
			role := items[index].Get("role")
			// Gemini reads a content without a role as the user's.
			user := role.Str == "user" || (protocol == contract.ProtocolGoogleGenerateContent && !role.Exists())
			if !items[index].IsObject() || !user {
				continue
			}
			if value := items[index].Get(content); value.Type == ojson.String {
				signals.lastUserChars = textLength(value.Str)
			} else if value.Exists() {
				signals.lastUserChars = jsonLength(value, 0)
			}
			break
		}
	}
	tools := ojson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return signals
	}
	if protocol != contract.ProtocolGoogleGenerateContent {
		signals.tools = len(tools.Array())
		return signals
	}
	for _, tool := range tools.Array() {
		declarations := tool.Get("functionDeclarations")
		if !declarations.Exists() {
			declarations = tool.Get("function_declarations")
		}
		if declarations.IsArray() {
			signals.tools += len(declarations.Array())
		}
	}
	return signals
}

// inlineImage is an inline base64 image, which OmniRoute leaves out of a
// message's length.
var inlineImage = regexp.MustCompile(`(?i)data:image/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=]+`)

// textLength is the length of text in UTF-16 code units, as JavaScript
// counts it, without inline base64 images.
func textLength(text string) int {
	length := 0
	for _, r := range inlineImage.ReplaceAllString(text, "") {
		length++
		if r >= 0x10000 {
			length++
		}
	}
	return length
}

// maxSignalDepth bounds how deep jsonLength walks; deeper values count their
// raw length.
const maxSignalDepth = 64

// jsonLength is the length of value as compact JSON in UTF-16 code units, as
// JavaScript's JSON.stringify writes it, with inline base64 images left out
// of its strings.
func jsonLength(value ojson.Result, depth int) int {
	switch {
	case value.Type == ojson.String:
		return quotedLength(inlineImage.ReplaceAllString(value.Str, ""))
	case depth >= maxSignalDepth:
		return len(value.Raw)
	case value.IsArray():
		length := 2
		for index, item := range value.Array() {
			if index > 0 {
				length++
			}
			length += jsonLength(item, depth+1)
		}
		return length
	case value.IsObject():
		length, first := 2, true
		value.ForEach(func(key, item ojson.Result) bool {
			if !first {
				length++
			}
			first = false
			length += quotedLength(key.Str) + 1 + jsonLength(item, depth+1)
			return true
		})
		return length
	default:
		return len(value.Raw)
	}
}

// quotedLength is the length of text as a JSON string, quotes and escapes
// included.
func quotedLength(text string) int {
	length := 2
	for _, r := range text {
		switch {
		case r == '"' || r == '\\' || r == '\b' || r == '\t' || r == '\n' || r == '\f' || r == '\r':
			length += 2
		case r < 0x20:
			length += 6
		case r >= 0x10000:
			length += 2
		default:
			length++
		}
	}
	return length
}

// reasoningConfig is what the Routing settings ask of reasoning.
type reasoningConfig struct {
	mode contract.ReasoningMode
	// level is the fixed mode's level.
	level contract.ReasoningLevel
	// preset names the adaptive range, low through high.
	preset    contract.AdaptivePreset
	low, high contract.ReasoningLevel
}

func reasoningConfigFrom(settings contract.RoutingSettings) reasoningConfig {
	low, high := settings.AdaptiveRange()
	return reasoningConfig{
		mode: settings.ReasoningMode, level: settings.ReasoningLevel,
		preset: settings.AdaptivePreset, low: low, high: high,
	}
}

// reasoningTarget is what the reasoning settings send for one request.
type reasoningTarget struct {
	config reasoningConfig
	// setting is the level the settings pick for the request.
	setting contract.ReasoningLevel
	// level is setting in RelayKit's vocabulary, none for off, and budget
	// the thinking budget it stands for.
	level  string
	budget int
	// signals and hits are what the adaptive mode read from the request.
	signals adaptiveSignals
	hits    int
}

// reasoningTargetFor resolves the reasoning settings for one request body
// in the upstream protocol. The fixed mode sends its level. The adaptive
// mode sends the lower end of its range for a request without signals, one
// level higher for each signal up to the upper end, and the upper end for
// all three, so a range of one level always sends that level.
func reasoningTargetFor(config reasoningConfig, protocol contract.ProtocolID, body []byte) (reasoningTarget, bool) {
	target := reasoningTarget{config: config}
	switch config.mode {
	case contract.ReasoningModeFixed:
		target.setting = config.level
	case contract.ReasoningModeAdaptive:
		low, high := config.low.Rank(), config.high.Rank()
		if low < 0 || low > high {
			return reasoningTarget{}, false
		}
		target.signals = requestSignals(protocol, body)
		target.hits = target.signals.hits()
		rank := low + target.hits
		if target.hits >= 3 || rank > high {
			rank = high
		}
		target.setting = contract.ReasoningLevels[rank]
	default:
		return reasoningTarget{}, false
	}
	budget, ok := levelBudgets[target.setting]
	if !ok {
		return reasoningTarget{}, false
	}
	target.level, target.budget = string(target.setting), budget
	if target.setting == contract.ReasoningLevelOff {
		target.level = "none"
	}
	return target, true
}

// describe is the request diagnostic for what target did to a body in the
// upstream protocol.
func (target reasoningTarget) describe(edit reasoningEdit, protocol contract.ProtocolID) string {
	var text strings.Builder
	config := target.config
	adaptive := config.mode == contract.ReasoningModeAdaptive
	if adaptive {
		signals := "signals"
		if target.hits == 1 {
			signals = "signal"
		}
		fmt.Fprintf(&text, "adaptive %s %s..%s: messages %d, tools %d, last %d chars, %d %s, ",
			config.preset, config.low, config.high,
			target.signals.messages, target.signals.tools, target.signals.lastUserChars, target.hits, signals)
	} else {
		text.WriteString("fixed: ")
	}
	switch {
	case edit.kept != "":
		fmt.Fprintf(&text, "wanted %s, kept client thinking: %s", target.level, edit.kept)
	case edit.sent != "":
		fmt.Fprintf(&text, "sent %s", edit.sent)
		switch {
		case target.level == "max" && edit.sent != "max":
			text.WriteString(" (model top)")
		case target.level == "none" && edit.sent != "none":
			text.WriteString(" (model lowest)")
		case adaptive && target.hits > 0 && target.setting == config.high:
			text.WriteString(" (upper bound)")
		}
	default:
		text.WriteString("nothing sent")
	}
	if !adaptive {
		asked := edit.asked
		if asked == "" {
			asked = "nothing"
		}
		fmt.Fprintf(&text, ", client asked %s", asked)
	}
	if edit.outside {
		if len(edit.changed) == 0 {
			text.WriteString("; model not in capability table: no field changed")
		} else {
			fmt.Fprintf(&text, "; model not in capability table: changed %s only", strings.Join(edit.changed, ", "))
		}
	}
	if adaptive && config.low != config.high && protocol == contract.ProtocolAnthropicMessages && edit.sent != "" {
		// Claude's message cache keys on the thinking settings.
		text.WriteString("; on the turn the level changes, Claude caches the whole conversation again")
	}
	return text.String()
}

// codexModel is what Codex's model catalog lists for a model.
type codexModel struct {
	levels []string
}

// reasoningEdit is what the reasoning settings did to one request body.
type reasoningEdit struct {
	body []byte
	// sent is the level the body carries for the settings; empty when the
	// client's thinking was kept or there was nothing to set.
	sent string
	// changed lists the fields the edit changed, empty when the body already
	// carried sent.
	changed []string
	// asked is the level the client asked for; empty when it named none.
	asked string
	// kept says why the client's thinking was kept as it was.
	kept string
	// outside marks a model outside RelayKit's capability tables: only the
	// thinking fields the client sent are changed.
	outside bool
	err     error
}

func (edit *reasoningEdit) set(path string, value any) {
	if edit.err != nil {
		return
	}
	updated, err := jsonedit.Set(edit.body, path, value)
	if err != nil {
		edit.err = err
		return
	}
	if !bytes.Equal(updated, edit.body) {
		edit.body = updated
		edit.note(path)
	}
}

func (edit *reasoningEdit) remove(path string) {
	if edit.err != nil || !ojson.GetBytes(edit.body, path).Exists() {
		return
	}
	updated, err := jsonedit.Delete(edit.body, path)
	if err != nil {
		edit.err = err
		return
	}
	edit.body = updated
	edit.note(path)
}

func (edit *reasoningEdit) note(path string) {
	if !slices.Contains(edit.changed, path) {
		edit.changed = append(edit.changed, path)
	}
}

// overrideReasoning applies target to a request body in the upstream
// protocol. Models in RelayKit's capability tables get the target written
// even when the client sent no thinking. Other models keep their own fields,
// which are rewritten but never added or turned off. Only the reasoning
// fields and the parameters they conflict with are spliced.
func overrideReasoning(
	protocol contract.ProtocolID,
	body []byte,
	model string,
	target reasoningTarget,
	codex codexModel,
) reasoningEdit {
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return chatReasoning(body, model, target, openAIReasoningFor(model, codex))
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return responsesReasoning(body, model, target, openAIReasoningFor(model, codex))
	case contract.ProtocolAnthropicMessages:
		if !relaykitbridge.ClaudeModelKnown(model) {
			return unknownClaudeReasoning(body, model, target)
		}
		return claudeReasoning(body, model, target)
	case contract.ProtocolGoogleGenerateContent:
		return geminiReasoning(body, model, target)
	default:
		return reasoningEdit{body: body}
	}
}

// openAIReasoning is what is known about an OpenAI-protocol model's
// reasoning efforts.
type openAIReasoning struct {
	known bool
	// levels are the efforts Codex's catalog lists; nil when it lists none.
	levels []string
	// lowest is the effort sent to turn reasoning off, and highest the one
	// sent for max when no catalog lists levels.
	lowest, highest string
}

func openAIReasoningFor(model string, codex codexModel) openAIReasoning {
	if len(codex.levels) > 0 {
		lowest := codex.levels[0]
		for _, level := range codex.levels[1:] {
			if reasoningRank(level) < reasoningRank(lowest) {
				lowest = level
			}
		}
		return openAIReasoning{known: true, levels: codex.levels, lowest: lowest}
	}
	if lowest, ok := relaykitbridge.OpenAIReasoningModel(model); ok {
		return openAIReasoning{known: true, lowest: lowest, highest: relaykitbridge.OpenAIHighestEffort(model)}
	}
	return openAIReasoning{highest: "high"}
}

// effort is the effort sent for level. A catalog model gets its nearest
// listed level, the highest one for max and the lowest one for none.
// Without a catalog, max becomes the model's highest effort unless the
// client already asked for more.
func (info openAIReasoning) effort(level, asked string) string {
	if level == "none" {
		return info.lowest
	}
	if len(info.levels) == 0 {
		if level != "max" {
			return level
		}
		if reasoningRank(asked) > reasoningRank(info.highest) {
			return asked
		}
		return info.highest
	}
	if level == "max" {
		highest := info.levels[0]
		for _, candidate := range info.levels[1:] {
			if reasoningRank(candidate) > reasoningRank(highest) {
				highest = candidate
			}
		}
		return highest
	}
	if slices.Contains(info.levels, level) {
		return level
	}
	nearest, distance := "", 0
	for _, candidate := range info.levels {
		if reasoningRank(candidate) <= 0 {
			continue
		}
		gap := reasoningRank(candidate) - reasoningRank(level)
		if gap < 0 {
			gap = -gap
		}
		// Ties go to the higher level.
		if nearest == "" || gap < distance || (gap == distance && reasoningRank(candidate) > reasoningRank(nearest)) {
			nearest, distance = candidate, gap
		}
	}
	return nearest
}

// cappedEffort is the effort for a model whose levels are unknown: xhigh
// and max become high unless the client already asked for more.
func cappedEffort(level, asked string) string {
	if reasoningRank(level) <= reasoningRank("high") {
		return level
	}
	if reasoningRank(asked) > reasoningRank("high") {
		return asked
	}
	return "high"
}

func namedLevel(value ojson.Result) string {
	if value.Type != ojson.String {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value.Str))
}

func chatReasoning(body []byte, model string, target reasoningTarget, info openAIReasoning) reasoningEdit {
	edit := reasoningEdit{body: body, outside: !info.known}
	effort := namedLevel(ojson.GetBytes(body, "reasoning_effort"))
	nested := ojson.GetBytes(body, "reasoning")
	nestedEffort := namedLevel(nested.Get("effort"))
	nestedBudget := nested.Get("max_tokens")
	nestedEnabled := nested.Get("enabled")
	off := (effort != "" && reasoningRank(effort) == 0) || nestedEnabled.Type == ojson.False ||
		(nestedEffort != "" && reasoningRank(nestedEffort) == 0) ||
		(nestedBudget.Type == ojson.Number && nestedBudget.Int() == 0)
	switch {
	case off:
		edit.asked = "none"
	case effort != "":
		edit.asked = effort
	case nestedEffort != "":
		edit.asked = nestedEffort
	case nestedBudget.Type == ojson.Number && nestedBudget.Int() > 0:
		edit.asked = budgetLevel(int(nestedBudget.Int()))
	}
	asks := !off && (edit.asked != "" || (nested.IsObject() && nestedEnabled.Type == ojson.True))
	if !info.known && !asks {
		return edit
	}
	want := info.effort(target.level, edit.asked)
	if want == "" {
		return edit
	}
	if effort != "" || !nested.IsObject() {
		edit.set("reasoning_effort", want)
	}
	if nested.IsObject() {
		edit.set("reasoning.effort", want)
		// The level replaces a token budget; providers take one or the other.
		edit.remove("reasoning.max_tokens")
		if want != "none" && nestedEnabled.Type == ojson.False {
			edit.remove("reasoning.enabled")
		}
	}
	if info.known && len(edit.changed) > 0 {
		edit.dropOpenAISampling(model, want, "temperature", "top_p", "logprobs", "top_logprobs")
	}
	edit.sent = want
	return edit
}

func responsesReasoning(body []byte, model string, target reasoningTarget, info openAIReasoning) reasoningEdit {
	edit := reasoningEdit{body: body, outside: !info.known}
	reasoning := ojson.GetBytes(body, "reasoning")
	edit.asked = namedLevel(reasoning.Get("effort"))
	if !info.known && (!reasoning.IsObject() || reasoningRank(edit.asked) == 0) {
		return edit
	}
	want := info.effort(target.level, edit.asked)
	if want == "" {
		return edit
	}
	if reasoning.IsObject() {
		edit.set("reasoning.effort", want)
	} else {
		edit.set("reasoning", map[string]string{"effort": want})
	}
	if info.known && len(edit.changed) > 0 {
		edit.dropOpenAISampling(model, want, "temperature", "top_p", "top_logprobs")
	}
	edit.sent = want
	return edit
}

// dropOpenAISampling removes the sampling parameters RelayKit lists as
// unsupported at effort; several OpenAI models reject them while reasoning.
func (edit *reasoningEdit) dropOpenAISampling(model, effort string, fields ...string) {
	temperature, topP, logProbs := relaykitbridge.OpenAISampling(model, effort)
	allowed := map[string]bool{"temperature": temperature, "top_p": topP, "logprobs": logProbs, "top_logprobs": logProbs}
	for _, field := range fields {
		if !allowed[field] {
			edit.remove(field)
		}
	}
}

// claudeAsked is the level a Messages request asks for: none when thinking
// is off, the effort it names, or the level of its manual budget.
func claudeAsked(thinking ojson.Result, effort string) string {
	switch thinking.Get("type").Str {
	case "disabled", "between_tools":
		return "none"
	case "enabled":
		if budget := thinking.Get("budget_tokens"); effort == "" && budget.Type == ojson.Number {
			return budgetLevel(int(budget.Int()))
		}
	}
	return effort
}

// claudeReasoning renders target with RelayKit's Claude capabilities:
// adaptive thinking with an effort where the model has it, a manual budget
// otherwise, and the model's lowest setting for off. Turning thinking on is
// skipped when the Messages API would then reject the request.
func claudeReasoning(body []byte, model string, target reasoningTarget) reasoningEdit {
	edit := reasoningEdit{body: body}
	thinking := ojson.GetBytes(body, "thinking")
	absent := !thinking.Exists() || thinking.Type == ojson.Null
	if !absent && !thinking.IsObject() {
		return edit
	}
	thinkingType := thinking.Get("type").Str
	clientEffort := namedLevel(ojson.GetBytes(body, "output_config.effort"))
	edit.asked = claudeAsked(thinking, clientEffort)
	on := thinkingType == "enabled" || thinkingType == "adaptive" ||
		(absent && relaykitbridge.ClaudeDefaultLevel(model) != "none")
	if target.level == "none" && !on {
		edit.sent = "none"
		return edit
	}
	if target.level != "none" && !on {
		if reason := claudeThinkingGuard(body); reason != "" {
			edit.kept = reason
			return edit
		}
	}
	maxTokens := ojson.GetBytes(body, "max_tokens")
	limit, limitMissing := int(maxTokens.Int()), false
	if maxTokens.Type != ojson.Number || limit <= 0 {
		limit, limitMissing = claudeDefaultMaxTokens, true
	}
	rendered, err := relaykitbridge.RenderClaudeThinking(model, target.level, target.budget, clientEffort, &limit, claudeDisplay(thinking))
	if err != nil {
		edit.err = err
		return edit
	}
	edit.applyClaudeThinking(thinking, rendered, limitMissing, limit, true)
	switch {
	case rendered.Type == "enabled" && rendered.BudgetTokens != nil:
		edit.sent = budgetLevel(*rendered.BudgetTokens)
	case rendered.Type == "disabled" || rendered.Type == "between_tools":
		edit.sent = "none"
	case rendered.Effort != "":
		edit.sent = rendered.Effort
	default:
		edit.sent = rendered.Level
	}
	return edit
}

// unknownClaudeReasoning handles a model RelayKit does not know. Its own
// thinking fields are rewritten in the shape the client chose; nothing is
// added and off leaves the request alone.
func unknownClaudeReasoning(body []byte, model string, target reasoningTarget) reasoningEdit {
	edit := reasoningEdit{body: body, outside: true}
	thinking := ojson.GetBytes(body, "thinking")
	thinkingType := thinking.Get("type").Str
	clientEffort := namedLevel(ojson.GetBytes(body, "output_config.effort"))
	edit.asked = claudeAsked(thinking, clientEffort)
	if target.level == "none" {
		return edit
	}
	setEffort := func() reasoningEdit {
		want := cappedEffort(target.level, clientEffort)
		edit.setClaudeEffort(want)
		edit.sent = want
		return edit
	}
	switch {
	case thinking.IsObject() && thinkingType == "adaptive":
		// Adaptive thinking takes its strength from effort alone.
		return setEffort()
	case thinking.IsObject() && thinkingType == "enabled":
	case (!thinking.Exists() || thinking.Type == ojson.Null) && clientEffort != "" && reasoningRank(clientEffort) != 0:
		// Effort alone still sets how much the model spends.
		return setEffort()
	default:
		return edit
	}
	maxTokens := ojson.GetBytes(body, "max_tokens")
	limit, limitMissing := int(maxTokens.Int()), false
	if maxTokens.Type != ojson.Number || limit <= 0 {
		limit, limitMissing = claudeDefaultMaxTokens, true
	}
	rendered, err := relaykitbridge.RenderClaudeThinking(model, target.level, target.budget, "", &limit, claudeDisplay(thinking))
	if err != nil || rendered.Type != "enabled" || rendered.BudgetTokens == nil {
		edit.err = err
		return edit
	}
	edit.applyClaudeThinking(thinking, rendered, limitMissing, limit, false)
	edit.sent = budgetLevel(*rendered.BudgetTokens)
	return edit
}

// setClaudeEffort sets output_config.effort, creating output_config when the
// request has none. An output_config of another type is left alone.
func (edit *reasoningEdit) setClaudeEffort(effort string) {
	config := ojson.GetBytes(edit.body, "output_config")
	switch {
	case config.IsObject():
		edit.set("output_config.effort", effort)
	case !config.Exists() || config.Type == ojson.Null:
		edit.set("output_config", map[string]string{"effort": effort})
	}
}

func claudeDisplay(thinking ojson.Result) *bool {
	switch thinking.Get("display").Str {
	case "summarized":
		value := true
		return &value
	case "omitted":
		value := false
		return &value
	}
	return nil
}

// applyClaudeThinking splices a rendered level into a Messages body. known
// marks a model in RelayKit's table: it also gets RelayKit's sampling rules
// for the new thinking, and an effort it takes none of is removed. A model
// outside the table keeps its own effort and sampling.
func (edit *reasoningEdit) applyClaudeThinking(
	thinking ojson.Result,
	rendered relaykitbridge.ClaudeThinking,
	limitMissing bool,
	limit int,
	known bool,
) {
	var budget int
	if rendered.Type == "enabled" && rendered.BudgetTokens != nil {
		budget = *rendered.BudgetTokens
	}
	switch {
	case rendered.Type == "":
	case !thinking.Exists() || thinking.Type == ojson.Null:
		object := struct {
			Type         string `json:"type"`
			BudgetTokens *int   `json:"budget_tokens,omitempty"`
		}{Type: rendered.Type}
		if rendered.Type == "enabled" {
			object.BudgetTokens = &budget
		}
		edit.set("thinking", object)
	default:
		if thinking.Get("type").Str != rendered.Type {
			edit.set("thinking.type", rendered.Type)
		}
		if rendered.Type == "enabled" {
			if current := thinking.Get("budget_tokens"); current.Type != ojson.Number || int(current.Int()) != budget {
				edit.set("thinking.budget_tokens", budget)
			}
		} else {
			edit.remove("thinking.budget_tokens")
		}
		if rendered.Type == "disabled" || rendered.Type == "between_tools" {
			// These types take no other thinking field.
			edit.remove("thinking.display")
		}
	}
	switch config := ojson.GetBytes(edit.body, "output_config"); {
	case rendered.Effort != "":
		if config.Get("effort").Str != rendered.Effort {
			edit.setClaudeEffort(rendered.Effort)
		}
	case known && config.IsObject() && config.Get("effort").Exists():
		// The model takes no effort with this thinking.
		if len(config.Map()) == 1 {
			edit.remove("output_config")
		} else {
			edit.remove("output_config.effort")
		}
	}
	if rendered.MaxTokens != nil {
		edit.set("max_tokens", *rendered.MaxTokens)
	} else if rendered.Type == "enabled" && limitMissing {
		edit.set("max_tokens", limit)
	}
	if !known || len(edit.changed) == 0 {
		return
	}
	if rendered.ClearSampling {
		edit.remove("temperature")
		edit.remove("top_k")
		edit.remove("top_p")
	} else if rendered.ConstrainSampling {
		edit.remove("temperature")
		edit.remove("top_k")
		if topP := ojson.GetBytes(edit.body, "top_p"); topP.Exists() && (topP.Type != ojson.Number || topP.Float() < 0.95 || topP.Float() > 1) {
			edit.remove("top_p")
		}
	}
}

// claudeThinkingGuard names why the Messages API would reject a request once
// thinking is turned on: a forced tool choice, an assistant prefill, or a
// tool call in the last assistant turn that no thinking block precedes. It
// is empty when thinking can be turned on.
func claudeThinkingGuard(body []byte) string {
	switch ojson.GetBytes(body, "tool_choice.type").Str {
	case "any", "tool":
		return "forced tool choice"
	}
	messages := ojson.GetBytes(body, "messages").Array()
	if len(messages) == 0 {
		return ""
	}
	if messages[len(messages)-1].Get("role").Str == "assistant" {
		return "assistant prefill"
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Get("role").Str != "assistant" {
			continue
		}
		thought := false
		for _, block := range messages[index].Get("content").Array() {
			switch block.Get("type").Str {
			case "thinking", "redacted_thinking":
				thought = true
			case "tool_use", "server_tool_use", "mcp_tool_use":
				if !thought {
					return "tool loop without thinking block"
				}
				return ""
			}
		}
		return ""
	}
	return ""
}

// geminiReasoning renders target with RelayKit's Gemini capabilities: the
// budget, clamped to the model's range, on models that take one, and a
// thinking level on the rest. A model RelayKit does not know keeps its
// budget, and only a thinking level it sent is rewritten.
func geminiReasoning(body []byte, model string, target reasoningTarget) reasoningEdit {
	edit := reasoningEdit{body: body}
	generation := "generationConfig"
	if !ojson.GetBytes(body, generation).Exists() && ojson.GetBytes(body, "generation_config").Exists() {
		generation = "generation_config"
	}
	configPath := generation + ".thinkingConfig"
	if !ojson.GetBytes(body, configPath).Exists() && ojson.GetBytes(body, generation+".thinking_config").Exists() {
		configPath = generation + ".thinking_config"
	}
	config := ojson.GetBytes(body, configPath)
	if (config.Exists() && config.Type != ojson.Null && !config.IsObject()) ||
		(ojson.GetBytes(body, generation).Exists() && !ojson.GetBytes(body, generation).IsObject()) {
		return edit
	}
	levelKey, budgetKey := "thinkingLevel", "thinkingBudget"
	if strings.HasSuffix(configPath, "thinking_config") {
		levelKey, budgetKey = "thinking_level", "thinking_budget"
	}
	if !config.Get(levelKey).Exists() && config.Get(otherCase(levelKey)).Exists() {
		levelKey = otherCase(levelKey)
	}
	if !config.Get(budgetKey).Exists() && config.Get(otherCase(budgetKey)).Exists() {
		budgetKey = otherCase(budgetKey)
	}
	clientLevel := namedLevel(config.Get(levelKey))
	clientBudget := config.Get(budgetKey)
	switch {
	case clientBudget.Type == ojson.Number && clientBudget.Int() < 0:
		edit.asked = "dynamic"
	case clientBudget.Type == ojson.Number:
		edit.asked = budgetLevel(int(clientBudget.Int()))
	default:
		edit.asked = clientLevel
	}
	rendered, known, err := relaykitbridge.RenderGeminiThinking(model, target.level, target.budget)
	if err != nil {
		edit.err = err
		return edit
	}
	if !known {
		edit.outside = true
		if target.level == "none" || clientLevel == "" || reasoningRank(clientLevel) == 0 {
			return edit
		}
		// Gemini levels stop at high.
		want := cappedEffort(target.level, "")
		edit.set(configPath+"."+levelKey, want)
		edit.sent = want
		return edit
	}
	if rendered.Budget != nil {
		edit.set(configPath+"."+budgetKey, *rendered.Budget)
		edit.remove(configPath + "." + levelKey)
		edit.sent = budgetLevel(*rendered.Budget)
	} else {
		edit.set(configPath+"."+levelKey, rendered.Level)
		edit.remove(configPath + "." + budgetKey)
		edit.sent = rendered.Level
	}
	return edit
}

// otherCase switches a Gemini field between its camelCase and snake_case
// spellings.
func otherCase(key string) string {
	switch key {
	case "thinkingLevel":
		return "thinking_level"
	case "thinking_level":
		return "thinkingLevel"
	case "thinkingBudget":
		return "thinking_budget"
	default:
		return "thinkingBudget"
	}
}

package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/injection"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// RequestDecision is what the entry hooks decided about a request: let in,
// or refused with an error in the client's protocol.
type RequestDecision struct {
	Deny    bool
	Status  int
	Code    string
	Message string
}

type entryResult struct {
	Action  string   `json:"action"`
	Tags    []string `json:"tags"`
	Status  int      `json:"status"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
}

var errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// maxDenyMessageRunes bounds the message a refused request's client reads.
const maxDenyMessageRunes = 500

// OnRequest runs the entry hooks: each may let the request in, tag it, or,
// granted request.gate, refuse it. A hook that fails lets the request in,
// unless its plugin fails closed with the user's consent.
func (ext *Extensions) OnRequest(ctx context.Context, body []byte) RequestDecision {
	const hook = plugin.HookOnRequest
	for _, target := range ext.serving(hook) {
		closed := func() RequestDecision {
			return RequestDecision{Deny: true, Status: http.StatusServiceUnavailable, Code: "plugin_unavailable", Message: target.Meta.Name + " could not decide on the request"}
		}
		arg := map[string]any{"request": ext.requestContext()}
		if target.Can(plugin.CapabilityRequest) {
			view, ok := ext.view(ctx, target, ext.input.Protocol, body)
			if !ok {
				// A plugin that reads the request does not run on one it
				// may not see.
				ext.note(target, hook, plugin.CallInfo{}, skipped("privacy_blocked"))
				if target.FailsClosed() {
					return closed()
				}
				continue
			}
			arg["body"] = json.RawMessage(view.Body)
		}
		result, info, skip, err := ext.call(ctx, target, hook, arg)
		var decided entryResult
		switch {
		case skip != "":
			ext.note(target, hook, info, skipped(skip))
		case err != nil:
			ext.note(target, hook, info, failure(err))
		case decode(result, &decided) != nil || (decided.Action != "" && decided.Action != "allow" && decided.Action != "deny"):
			ext.note(target, hook, info, record{status: contract.PluginRecordFailed, skipped: plugin.ReasonInvalidResult, summary: `onRequest returns { action: "allow" | "deny" }`})
		default:
			tags := ext.addTags(decided.Tags)
			line := record{status: contract.PluginRecordOK, tags: tags}
			if len(tags) > 0 {
				line.effects = []string{contract.PluginEffectTag}
			}
			if decided.Action != "deny" {
				ext.note(target, hook, info, line)
				continue
			}
			if !target.Can(plugin.CapabilityRequestGate) {
				line.skipped = "no_request_gate"
				ext.note(target, hook, info, line)
				continue
			}
			decision := RequestDecision{Deny: true, Status: decided.Status, Code: decided.Code, Message: clip(strings.TrimSpace(decided.Message), maxDenyMessageRunes)}
			if decision.Status < 400 || decision.Status > 599 {
				decision.Status = http.StatusForbidden
			}
			if !errorCodePattern.MatchString(decision.Code) {
				decision.Code = "plugin_denied"
			}
			if decision.Message == "" {
				decision.Message = target.Meta.Name + " refused the request"
			}
			line.effects = append(line.effects, contract.PluginEffectDeny)
			line.summary = decision.Message
			ext.note(target, hook, info, line)
			return decision
		}
		if target.FailsClosed() {
			return closed()
		}
	}
	return RequestDecision{}
}

// addTags keeps the valid tags of a plugin, within the bound of a request,
// and returns those it kept.
func (ext *Extensions) addTags(tags []string) []string {
	ext.mutex.Lock()
	defer ext.mutex.Unlock()
	var added []string
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > contract.MaxPluginTagRunes || clip(tag, contract.MaxPluginTagRunes) != tag || slices.Contains(ext.tags, tag) {
			continue
		}
		if len(ext.tags) == contract.MaxPluginTags {
			break
		}
		ext.tags = append(ext.tags, tag)
		added = append(added, tag)
	}
	return added
}

// RouteCandidate is one provider a request may go to, as route sees it:
// ID names it in the decision.
type RouteCandidate struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
}

// RouteDecision is what the route hooks decided: the candidates in their
// new order, the ones left out, and the model to resolve instead, "" to
// keep it.
type RouteDecision struct {
	Order []string
	Model string
}

type routeResult struct {
	Order   []string `json:"order"`
	Exclude []string `json:"exclude"`
	Model   string   `json:"model"`
	Reason  string   `json:"reason"`
}

// Route runs the route hooks of plugins granted routing over candidates.
// Each sees the order the one before left; one that would exclude every
// candidate excludes none.
func (ext *Extensions) Route(ctx context.Context, candidates []RouteCandidate, body []byte) RouteDecision {
	const hook = plugin.HookRoute
	current := slices.Clone(candidates)
	decision := RouteDecision{}
	for _, target := range ext.serving(hook, plugin.CapabilityRouting) {
		arg := map[string]any{"request": ext.requestContext(), "candidates": current}
		if target.Can(plugin.CapabilityRequest) {
			view, ok := ext.view(ctx, target, ext.input.Protocol, body)
			if !ok {
				ext.note(target, hook, plugin.CallInfo{}, skipped("privacy_blocked"))
				continue
			}
			arg["body"] = json.RawMessage(view.Body)
		}
		result, info, skip, err := ext.call(ctx, target, hook, arg)
		if skip != "" {
			ext.note(target, hook, info, skipped(skip))
			continue
		}
		if err != nil {
			ext.note(target, hook, info, failure(err))
			continue
		}
		var routed routeResult
		if err := decode(result, &routed); err != nil {
			ext.note(target, hook, info, record{status: contract.PluginRecordFailed, skipped: plugin.ReasonInvalidResult, summary: "route returns { order?, exclude?, model? }"})
			continue
		}
		line := record{status: contract.PluginRecordOK, summary: routed.Reason}
		if len(routed.Exclude) > 0 {
			kept := slices.DeleteFunc(slices.Clone(current), func(candidate RouteCandidate) bool { return slices.Contains(routed.Exclude, candidate.ID) })
			switch {
			case len(kept) == 0:
				line.skipped = "would_exclude_all"
			case len(kept) < len(current):
				current = kept
				line.effects = append(line.effects, contract.PluginEffectExclude)
			}
		}
		if len(routed.Order) > 0 {
			if moved := moveFirst(current, routed.Order); !slices.Equal(moved, current) {
				current = moved
				line.effects = append(line.effects, contract.PluginEffectOrder)
			}
		}
		if model := strings.TrimSpace(routed.Model); model != "" && model != ext.model && decision.Model == "" {
			decision.Model = model
			line.effects = append(line.effects, contract.PluginEffectModel)
		}
		ext.note(target, hook, info, line)
	}
	for _, candidate := range current {
		decision.Order = append(decision.Order, candidate.ID)
	}
	return decision
}

// moveFirst puts the candidates order names first, in its order, and keeps
// the rest after them as they were.
func moveFirst(candidates []RouteCandidate, order []string) []RouteCandidate {
	var first, rest []RouteCandidate
	for _, id := range order {
		for _, candidate := range candidates {
			if candidate.ID == id && !slices.ContainsFunc(first, func(kept RouteCandidate) bool { return kept.ID == id }) {
				first = append(first, candidate)
			}
		}
	}
	for _, candidate := range candidates {
		if !slices.ContainsFunc(first, func(kept RouteCandidate) bool { return kept.ID == candidate.ID }) {
			rest = append(rest, candidate)
		}
	}
	return append(first, rest...)
}

// Routed notes the model the request resolves after route.
func (ext *Extensions) Routed(model string) {
	if ext != nil && model != "" {
		ext.model = model
	}
}

type forwardResult struct {
	System *struct {
		Append string `json:"append"`
	} `json:"system"`
	Turn *struct {
		Prepend string `json:"prepend"`
	} `json:"turn"`
	Params map[string]json.RawMessage `json:"params"`
	Tools  *struct {
		Add    []ToolSpec `json:"add"`
		Remove []string   `json:"remove"`
	} `json:"tools"`
	Body json.RawMessage `json:"body"`
}

// maxForwardBlock bounds a plugin's system or turn text in one request.
const maxForwardBlock = 16 << 10

// forwardKey keeps a plugin's beforeForward blocks apart from its prepare
// hooks' in the ledger.
func forwardKey(key string) string { return "forward:" + key }

// BeforeForward runs the beforeForward hooks over the client's body, once,
// before every attempt, and returns the body every attempt then sends. A
// plugin granted request.write appends to the system prompt, puts a block
// at the start of the turn, sets parameters and adds or removes tools; one
// granted request.write.raw replaces the body. The turn block is settled by
// the turn's first call and put back, byte for byte, in its later calls and
// turns, so the prompt cache holds.
func (ext *Extensions) BeforeForward(ctx context.Context, body []byte) []byte {
	const hook = plugin.HookBeforeForward
	current := body
	var blocks []injection.Plugin
	params := map[string]json.RawMessage{}
	var addTools []ToolSpec
	var removeTools []string
	budget := ext.host.Settings().InjectionBudgetBytes
	opening, hasOpening := ext.host.ledger.Opening(ext.input.Protocol, body)
	for _, target := range ext.serving(hook, plugin.CapabilityRequestWrite, plugin.CapabilityInject, plugin.CapabilityRequestWriteRaw) {
		arg := map[string]any{"request": ext.requestContext()}
		view := BodyView{Body: current}
		seen := false
		if target.Can(plugin.CapabilityRequest) || target.Can(plugin.CapabilityRequestWriteRaw) {
			if view, seen = ext.view(ctx, target, ext.input.Protocol, current); !seen {
				ext.note(target, hook, plugin.CallInfo{}, skipped("privacy_blocked"))
				continue
			}
			arg["body"] = json.RawMessage(view.Body)
		}
		result, info, skip, err := ext.call(ctx, target, hook, arg)
		if skip != "" {
			ext.note(target, hook, info, skipped(skip))
			continue
		}
		if err != nil {
			ext.note(target, hook, info, failure(err))
			continue
		}
		var edits forwardResult
		if err := decode(result, &edits); err != nil {
			ext.note(target, hook, info, record{status: contract.PluginRecordFailed, skipped: plugin.ReasonInvalidResult, summary: "beforeForward returns { system?, turn?, params?, tools?, body? }"})
			continue
		}
		line := record{status: contract.PluginRecordOK}
		if len(edits.Body) > 0 && string(edits.Body) != "null" {
			switch {
			case !target.Can(plugin.CapabilityRequestWriteRaw) || !seen:
				line.skipped = "no_request_write_raw"
			default:
				replaced := []byte(edits.Body)
				if view.Restore != nil {
					replaced = view.Restore(replaced)
				}
				if json.Valid(replaced) && strings.HasPrefix(strings.TrimSpace(string(replaced)), "{") {
					current = replaced
					line.effects = append(line.effects, contract.PluginEffectBody)
				} else {
					line.skipped = "invalid_body"
				}
			}
		}
		structured := target.Can(plugin.CapabilityRequestWrite) || target.Can(plugin.CapabilityInject)
		if !structured && (edits.System != nil || edits.Turn != nil || len(edits.Params) > 0 || edits.Tools != nil) {
			line.skipped = "no_request_write"
		}
		if structured {
			block := injection.Plugin{Key: forwardKey(target.Key), Settled: true}
			if edits.System != nil && edits.System.Append != "" {
				block.Resident = clipBytes(edits.System.Append, maxForwardBlock)
				line.effects = append(line.effects, contract.PluginEffectSystem)
			}
			if ext.input.Session != "" && hasOpening {
				block.Settled = ext.host.forwardTurns.settle(ext.input.Session, target.Key, opening)
			}
			if edits.Turn != nil && edits.Turn.Prepend != "" && block.Settled {
				block.Turn = clipBytes(edits.Turn.Prepend, maxForwardBlock)
				line.effects = append(line.effects, contract.PluginEffectTurn)
			}
			if size := len(block.Resident) + len(block.Turn); size > 0 {
				if size > budget {
					block.Resident, block.Turn = "", ""
					line.skipped = "over_budget"
					line.effects = slices.DeleteFunc(line.effects, func(effect string) bool {
						return effect == contract.PluginEffectSystem || effect == contract.PluginEffectTurn
					})
				} else {
					budget -= size
				}
			}
			if block.Resident != "" || block.Turn != "" || !block.Settled {
				blocks = append(blocks, block)
			}
			if len(edits.Params) > 0 {
				for name, value := range edits.Params {
					params[name] = value
				}
				line.effects = append(line.effects, contract.PluginEffectParams)
			}
			if edits.Tools != nil && (len(edits.Tools.Add) > 0 || len(edits.Tools.Remove) > 0) {
				addTools = append(addTools, edits.Tools.Add...)
				removeTools = append(removeTools, edits.Tools.Remove...)
				line.effects = append(line.effects, contract.PluginEffectTools)
			}
		}
		ext.note(target, hook, info, line)
	}
	if len(params) > 0 {
		if edited, err := applyParams(ext.input.Protocol, current, params); err == nil {
			current = edited
		} else {
			ext.host.logf("apply the parameters plugins set: %v", err)
		}
	}
	if len(addTools) > 0 || len(removeTools) > 0 {
		if edited, err := applyTools(ext.input.Protocol, current, addTools, removeTools); err == nil {
			current = edited
		} else {
			ext.host.logf("apply the tools plugins set: %v", err)
		}
	}
	if len(blocks) > 0 {
		result, err := ext.host.ledger.Apply(injection.Request{Protocol: ext.input.Protocol, Session: ext.input.Session, Body: current, Plugins: blocks})
		if err == nil {
			current = result.Body
		} else {
			ext.host.logf("put the plugins' blocks into the request: %v", err)
		}
	}
	return current
}

// clipBytes cuts text to at most max bytes, on a rune boundary.
func clipBytes(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// UpstreamAttempt is the attempt beforeUpstream changes: the provider it
// goes to, in the provider's protocol and model.
type UpstreamAttempt struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Protocol string `json:"protocol"`
	Model    string `json:"model"`
	Index    int    `json:"index"`
}

type upstreamResult struct {
	Body    json.RawMessage   `json:"body"`
	Headers map[string]string `json:"headers"`
}

// BeforeUpstream runs the beforeUpstream hooks of plugins granted
// request.write.raw over the body one attempt sends, in the provider's
// protocol, and returns it with the headers they add.
func (ext *Extensions) BeforeUpstream(ctx context.Context, attempt UpstreamAttempt, body []byte) ([]byte, map[string]string) {
	const hook = plugin.HookBeforeUpstream
	current := body
	headers := map[string]string{}
	protocol := contract.ProtocolID(attempt.Protocol)
	for _, target := range ext.serving(hook, plugin.CapabilityRequestWriteRaw) {
		view, seen := ext.view(ctx, target, protocol, current)
		if !seen {
			ext.note(target, hook, plugin.CallInfo{}, skipped("privacy_blocked"))
			continue
		}
		arg := map[string]any{"request": ext.requestContext(), "attempt": attempt, "body": json.RawMessage(view.Body)}
		result, info, skip, err := ext.call(ctx, target, hook, arg)
		if skip != "" {
			ext.note(target, hook, info, skipped(skip))
			continue
		}
		if err != nil {
			ext.note(target, hook, info, failure(err))
			continue
		}
		var edits upstreamResult
		if err := decode(result, &edits); err != nil {
			ext.note(target, hook, info, record{status: contract.PluginRecordFailed, skipped: plugin.ReasonInvalidResult, summary: "beforeUpstream returns { body?, headers? }"})
			continue
		}
		line := record{status: contract.PluginRecordOK}
		if len(edits.Body) > 0 && string(edits.Body) != "null" {
			replaced := []byte(edits.Body)
			if view.Restore != nil {
				replaced = view.Restore(replaced)
			}
			if json.Valid(replaced) && strings.HasPrefix(strings.TrimSpace(string(replaced)), "{") {
				current = replaced
				line.effects = append(line.effects, contract.PluginEffectBody)
			} else {
				line.skipped = "invalid_body"
			}
		}
		for name, value := range edits.Headers {
			if !allowedHeader(name, value) {
				line.skipped = "header_denied"
				continue
			}
			headers[http.CanonicalHeaderKey(name)] = value
			if !slices.Contains(line.effects, contract.PluginEffectHeaders) {
				line.effects = append(line.effects, contract.PluginEffectHeaders)
			}
		}
		ext.note(target, hook, info, line)
	}
	return current, headers
}

var headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")

// allowedHeader reports whether a plugin may add a header to an attempt:
// never a credential, nor one the transport or the gateway owns.
func allowedHeader(name, value string) bool {
	if !headerToken.MatchString(name) || strings.ContainsAny(value, "\r\n\x00") || len(value) > 4096 {
		return false
	}
	lower := strings.ToLower(name)
	switch lower {
	case "authorization", "cookie", "host", "content-length", "connection", "keep-alive", "transfer-encoding",
		"te", "trailer", "upgrade", "expect", "x-api-key", "api-key", "x-goog-api-key", "proxy-authorization":
		return false
	}
	return !strings.HasPrefix(lower, "proxy-") && !strings.HasPrefix(lower, "x-astrlink-") && !strings.HasPrefix(lower, "sec-")
}

// forwardTurns remembers, by session and plugin, the opening message whose
// turn block beforeForward last settled: the turn's first call settles it,
// the later ones put it back.
type forwardTurns struct {
	mutex sync.Mutex
	turns map[string]string
	order []string
}

const maxForwardTurns = 4096

func newForwardTurns() *forwardTurns { return &forwardTurns{turns: map[string]string{}} }

// settle reports whether this call settles the plugin's turn block for the
// session's turn that opens with opening.
func (turns *forwardTurns) settle(session, plugin, opening string) bool {
	key := session + "\x00" + plugin
	turns.mutex.Lock()
	defer turns.mutex.Unlock()
	if last, found := turns.turns[key]; found && last == opening {
		return false
	}
	if _, found := turns.turns[key]; !found {
		turns.order = append(turns.order, key)
		if len(turns.order) > maxForwardTurns {
			delete(turns.turns, turns.order[0])
			turns.order = turns.order[1:]
		}
	}
	turns.turns[key] = opening
	return true
}

// ToolSpec is a tool beforeForward adds, in the protocol's own shape once
// added.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

var toolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (spec ToolSpec) check() error {
	if !toolName.MatchString(spec.Name) {
		return fmt.Errorf("tool name %q must be 1 to 64 letters, digits, _ or -", spec.Name)
	}
	if len(spec.Parameters) > 0 && !strings.HasPrefix(strings.TrimSpace(string(spec.Parameters)), "{") {
		return fmt.Errorf("the parameters of tool %s must be a JSON Schema object", spec.Name)
	}
	return nil
}

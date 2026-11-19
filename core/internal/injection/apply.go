package injection

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonedit"
	"github.com/QuantumNous/astrlink/core/internal/systemprompt"
	"github.com/tidwall/gjson"
)

// separator parts blocks from each other and from the text of the message
// they open.
const separator = "\n\n"

// adapters reads the conversation of each protocol. Lookups only read it.
var adapters = convo.DefaultRegistry()

// layout is how a protocol's user message takes a block.
type layout struct {
	protocol convo.Protocol
	// content names the member of a message that holds its text.
	content string
	// part is the content part that carries a block.
	part func(text string) any
	// leads reports a part that must stay ahead of the user's words: a tool
	// result, which the API wants first in its message.
	leads func(part gjson.Result) bool
}

var layouts = map[contract.ProtocolID]layout{
	contract.ProtocolAnthropicMessages: {
		protocol: convo.AnthropicMessages,
		content:  "content",
		part:     typedText("text"),
		leads:    func(part gjson.Result) bool { return part.Get("type").Str == "tool_result" },
	},
	contract.ProtocolOpenAIChat: {
		protocol: convo.OpenAIChat,
		content:  "content",
		part:     typedText("text"),
	},
	contract.ProtocolOpenAIResponses: {
		protocol: convo.OpenAIResponses,
		content:  "content",
		part:     typedText("input_text"),
	},
	contract.ProtocolGoogleGenerateContent: {
		protocol: convo.GeminiGenerateContent,
		content:  "parts",
		part:     func(text string) any { return map[string]string{"text": text} },
		leads: func(part gjson.Result) bool {
			return part.Get("functionResponse").Exists() || part.Get("function_response").Exists()
		},
	},
}

// textPart is a typed text part, its type first as clients write it.
type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func typedText(kind string) func(string) any {
	return func(text string) any { return textPart{Type: kind, Text: text} }
}

// Supports reports whether Apply can put turn blocks into requests of
// protocol.
func Supports(protocol contract.ProtocolID) bool {
	_, ok := layouts[protocol]
	return ok
}

// Plugin is one plugin's part in a request.
type Plugin struct {
	// Key names the plugin. The ledger keeps each plugin's blocks apart.
	Key string
	// Resident goes at the end of the system prompt; "" for none.
	Resident string
	// Settled marks the call that settles the plugin's block for the turn,
	// usually the turn's first: Turn is the block, "" for none. Every other
	// call of the turn puts back what was settled.
	Settled bool
	Turn    string
}

// Request is a request body, in the client's protocol, to put plugin blocks
// into.
type Request struct {
	Protocol contract.ProtocolID
	// Session keys the ledger. Without one, earlier turns' blocks are not
	// put back.
	Session string
	Body    []byte
	Plugins []Plugin
}

// Outcome is what Apply did for one plugin.
type Outcome struct {
	Key string
	// ResidentBytes is the size of the resident block put in. NoSystem
	// marks a resident block the body had no system prompt for, as beside
	// Gemini cached content.
	ResidentBytes int
	NoSystem      bool
	// TurnBytes is the size of the block put at the start of the turn's
	// opening message, settled by this call or an earlier one of the turn.
	// NoOpening marks a block settled now with no message to carry it.
	TurnBytes int
	NoOpening bool
	// Replayed counts earlier turns' blocks put back.
	Replayed int
}

// Result is the body with the plugins' blocks in it.
type Result struct {
	Body     []byte
	Outcomes []Outcome
}

// opening is a user message that opens a turn: one with words the user typed
// or an attachment, not a tool result.
type opening struct {
	// content is the message's content, a string or an array of parts.
	content gjson.Result
	// key names the message in the ledger: a fingerprint of its text, and
	// for a text seen before in the request, which time it is.
	key string
}

type ledgerOp struct {
	session, key, block string
}

// Apply puts the plugins' blocks into a request: each turn block at the
// start of its turn's opening user message, so it never goes ahead of a tool
// result, and each resident block at the end of the system prompt. Earlier
// turns' blocks go back where they went, byte for byte, so the prompt cache
// holds across turns; a stateful request (previous_response_id) needs none
// put back, since the upstream keeps them. The client's bytes outside the
// added text stay as they were. On an error the request should go out as it
// came, and the ledger is left unchanged.
func (ledger *Ledger) Apply(request Request) (Result, error) {
	result := Result{Body: request.Body, Outcomes: make([]Outcome, len(request.Plugins))}
	for index, plugin := range request.Plugins {
		result.Outcomes[index].Key = plugin.Key
	}
	body := request.Body
	var ops []ledgerOp
	spec, ok := layouts[request.Protocol]
	if !ok {
		for index, plugin := range request.Plugins {
			result.Outcomes[index].NoOpening = plugin.Settled && plugin.Turn != ""
		}
	} else {
		openings, stateful := ledger.openings(spec, body)
		blocks := make([][]string, len(openings))
		current := len(openings) - 1
		if current >= 0 && !carries(body, openings[current].content) {
			current = -1
		}
		for index, plugin := range request.Plugins {
			outcome := &result.Outcomes[index]
			session := ""
			if request.Session != "" {
				session = request.Session + "/" + plugin.Key
			}
			for at, message := range openings {
				var block string
				switch {
				case at == current && plugin.Settled:
					block = plugin.Turn
					if session != "" {
						ops = append(ops, ledgerOp{session, message.key, block})
					}
				case session == "" || (at != current && stateful) || !carries(body, message.content):
					continue
				default:
					block, _ = ledger.Lookup(session, message.key)
				}
				if block == "" {
					continue
				}
				blocks[at] = append(blocks[at], block)
				if at == current {
					outcome.TurnBytes = len(block)
				} else {
					outcome.Replayed++
				}
			}
			if plugin.Settled && plugin.Turn != "" && current < 0 {
				outcome.NoOpening = true
			}
		}
		var err error
		if body, err = spec.prefix(body, openings, blocks); err != nil {
			return Result{}, err
		}
	}
	for index, plugin := range request.Plugins {
		updated, changed, err := systemprompt.Append(request.Protocol, body, plugin.Resident)
		if err != nil {
			return Result{}, err
		}
		body = updated
		if changed {
			result.Outcomes[index].ResidentBytes = len(plugin.Resident)
		} else if plugin.Resident != "" {
			result.Outcomes[index].NoSystem = true
		}
	}
	for _, op := range ops {
		if op.block == "" {
			ledger.Forget(op.session, op.key)
		} else {
			ledger.Remember(op.session, op.key, op.block)
		}
	}
	result.Body = body
	return result, nil
}

// Opening names the newest opening user message of body, the one a turn
// block goes to, by the key the ledger keeps its block under: two turns that
// open with the same words still differ. ok is false when the body carries
// none.
func (ledger *Ledger) Opening(protocol contract.ProtocolID, body []byte) (string, bool) {
	spec, ok := layouts[protocol]
	if !ok {
		return "", false
	}
	openings, _ := ledger.openings(spec, body)
	if len(openings) == 0 {
		return "", false
	}
	return openings[len(openings)-1].key, true
}

// openings lists the opening user messages of body in order, and whether
// the request continues a conversation the upstream keeps.
func (ledger *Ledger) openings(spec layout, body []byte) ([]opening, bool) {
	adapter, ok := adapters.Lookup(spec.protocol)
	if !ok {
		return nil, false
	}
	field := adapter.HistoryField()
	fields := map[string]json.RawMessage{}
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		// The history is the bulk of a body; statefulness never needs it.
		if key.Str != field {
			fields[key.Str] = json.RawMessage(value.Raw)
		}
		return true
	})
	stateful := adapter.IsStateful(fields)
	history := gjson.GetBytes(body, field)
	var openings []opening
	seen := map[string]int{}
	add := func(content gjson.Result, text string) {
		key := ledger.Fingerprint(text)
		if count := seen[key]; count > 0 {
			seen[key]++
			key += "#" + strconv.Itoa(count)
		} else {
			seen[key] = 1
		}
		openings = append(openings, opening{content: content, key: key})
	}
	switch {
	case history.Type == gjson.String:
		// Responses takes input as a bare string: one user message.
		item := json.RawMessage(`{"role":"user","content":` + history.Raw + `}`)
		if adapter.IsUserTurn(item) {
			add(history, adapter.UserText(item))
		}
	case history.IsArray():
		for _, element := range history.Array() {
			item := json.RawMessage(element.Raw)
			if adapter.IsUserTurn(item) {
				add(element.Get(spec.content), adapter.UserText(item))
			}
		}
	}
	return openings, stateful
}

// carries reports whether content can take a block: a string or an array of
// parts, found where the body has it.
func carries(body []byte, content gjson.Result) bool {
	return (content.Type == gjson.String || content.IsArray()) && located(body, content)
}

// prefix puts each opening's blocks at the start of its content, in one pass
// over the body.
func (spec layout) prefix(body []byte, openings []opening, blocks [][]string) ([]byte, error) {
	type splice struct {
		start, end int
		text       []byte
	}
	var splices []splice
	for at, message := range openings {
		if len(blocks[at]) == 0 {
			continue
		}
		block := strings.Join(blocks[at], separator)
		content := message.content
		if content.Type == gjson.String {
			encoded, err := jsonedit.Encode(block + separator)
			if err != nil {
				return nil, err
			}
			// The client's characters keep their bytes, escapes included.
			text := append(encoded[:len(encoded)-1:len(encoded)-1], content.Raw[1:]...)
			splices = append(splices, splice{content.Index, content.Index + len(content.Raw), text})
			continue
		}
		part, err := jsonedit.Encode(spec.part(block))
		if err != nil {
			return nil, err
		}
		parts := content.Array()
		lead := 0
		for spec.leads != nil && lead < len(parts) && spec.leads(parts[lead]) {
			lead++
		}
		switch {
		case len(parts) == 0:
			at := content.Index + 1
			splices = append(splices, splice{at, at, part})
		case lead < len(parts):
			if !located(body, parts[lead]) {
				return nil, jsonedit.ErrUnsafe
			}
			at := parts[lead].Index
			splices = append(splices, splice{at, at, append(part, ',')})
		default:
			last := parts[len(parts)-1]
			if !located(body, last) {
				return nil, jsonedit.ErrUnsafe
			}
			at := last.Index + len(last.Raw)
			splices = append(splices, splice{at, at, append([]byte{','}, part...)})
		}
	}
	if len(splices) == 0 {
		return body, nil
	}
	slices.SortFunc(splices, func(a, b splice) int { return a.start - b.start })
	size := len(body)
	for _, edit := range splices {
		size += len(edit.text) - (edit.end - edit.start)
	}
	updated := make([]byte, 0, size)
	from := 0
	for _, edit := range splices {
		updated = append(updated, body[from:edit.start]...)
		updated = append(updated, edit.text...)
		from = edit.end
	}
	return append(updated, body[from:]...), nil
}

// located reports whether value's offset points at its raw bytes in body.
func located(body []byte, value gjson.Result) bool {
	return value.Index > 0 && value.Index+len(value.Raw) <= len(body) &&
		string(body[value.Index:value.Index+len(value.Raw)]) == value.Raw
}

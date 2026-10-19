package ingress

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

// estimatedTokensPerMediaPart is the flat context cost of one image, audio or
// video part. Providers bill an image at roughly 85 to 1,600 tokens depending
// on its size and detail, and the body does not carry the dimensions without
// decoding the payload, so one middle value stands in for every part.
const estimatedTokensPerMediaPart = 1000

// minEncodedPayloadBytes is the length from which a string made only of base64
// characters is read as an inlined payload rather than as text.
const minEncodedPayloadBytes = 256

// requestShape is the structural reading of an inference body that the model
// routing facts need: counts and media kinds, never text. known is false when
// the body was never read for it, which leaves every fact derived from it
// unknown rather than zero, so no rule testing one of them matches.
type requestShape struct {
	known             bool
	inputBytes        int
	toolDeclarations  int
	toolCalls         int
	toolMessages      int
	assistantMessages int
	mediaParts        int
	image             bool
	audio             bool
	video             bool
	estimatedTokens   int
}

// namesModelRoutingGroup reports whether model selects a model routing group,
// the only requests whose routing reads the body's shape.
func namesModelRoutingGroup(model string) bool {
	return strings.HasPrefix(model, contract.ModelRoutingModelPrefix) && model != contract.AstrLinkAutoModelID
}

// inspectRequestShape reads the protocol's history, tool and media layout from
// a body the caller has already decoded as a JSON object. Item and part types
// it does not know are skipped, so a body that grows new shapes still yields
// every count it can read.
//
// Only the protocols whose bodies AstrLink inspects before routing are read;
// a Gemini path cannot name a routing group, and a legacy completions prompt
// has no history, tools or media to count. Tool results are counted one per
// result rather than one per message: Chat and Responses give each result its
// own message or item while Anthropic batches parallel results into one user
// turn, and the counts should mean the same thing for the same conversation
// on every protocol. Files and documents are not media: those parts carry
// PDFs and text, and images travel as image parts on every protocol.
func inspectRequestShape(protocol contract.ProtocolID, raw []byte) requestShape {
	shape := requestShape{known: true, inputBytes: len(raw)}
	switch protocol {
	case contract.ProtocolOpenAIChat:
		shape.inspectOpenAIChat(raw)
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		shape.inspectOpenAIResponses(raw)
	case contract.ProtocolAnthropicMessages:
		shape.inspectAnthropicMessages(raw)
	}
	shape.estimatedTokens = estimateTextTokens(raw) + shape.mediaParts*estimatedTokensPerMediaPart
	return shape
}

// mediaKind folds the media flags into the media_kind domain.
func (shape requestShape) mediaKind() string {
	kind, kinds := "none", 0
	for _, present := range []struct {
		name string
		seen bool
	}{{"image", shape.image}, {"audio", shape.audio}, {"video", shape.video}} {
		if present.seen {
			kind = present.name
			kinds++
		}
	}
	if kinds > 1 {
		return "mixed"
	}
	return kind
}

func (shape *requestShape) inspectOpenAIChat(raw []byte) {
	// functions is the deprecated spelling of tools; a body may carry either.
	shape.toolDeclarations = arrayLength(gjson.GetBytes(raw, "tools")) + arrayLength(gjson.GetBytes(raw, "functions"))
	for _, message := range arrayItems(gjson.GetBytes(raw, "messages")) {
		switch message.Get("role").Str {
		case "assistant":
			shape.assistantMessages++
			shape.toolCalls += arrayLength(message.Get("tool_calls"))
			if message.Get("function_call").IsObject() {
				shape.toolCalls++
			}
		case "tool", "function":
			shape.toolMessages++
		}
		shape.inspectContentParts(message.Get("content"))
	}
}

func (shape *requestShape) inspectOpenAIResponses(raw []byte) {
	shape.toolDeclarations = arrayLength(gjson.GetBytes(raw, "tools"))
	// Responses spreads one model turn over several items (reasoning, text,
	// tool calls), where Chat and Anthropic keep it in one assistant message.
	// A run of model items counts as one assistant message, so the same
	// conversation counts the same in every format.
	modelTurn := false
	noteModelItem := func() {
		if !modelTurn {
			shape.assistantMessages++
			modelTurn = true
		}
	}
	// A bare string input is one user message of text, which counts nothing.
	for _, item := range arrayItems(gjson.GetBytes(raw, "input")) {
		switch kind := item.Get("type").Str; {
		case kind == "" || kind == "message":
			if item.Get("role").Str == "assistant" {
				noteModelItem()
			} else {
				modelTurn = false
			}
			shape.inspectContentParts(item.Get("content"))
		case kind == "reasoning":
			noteModelItem()
		// Each client-executed tool family pairs a <name>_call item with a
		// <name>_call_output item: function, custom tool, local shell and
		// computer use. Hosted tools (web search, file search, MCP) carry
		// their result inside the call item.
		case strings.HasSuffix(kind, "_call_output"):
			modelTurn = false
			shape.toolMessages++
			shape.inspectContentParts(item.Get("output"))
		case strings.HasSuffix(kind, "_call"):
			noteModelItem()
			shape.toolCalls++
		}
	}
}

func (shape *requestShape) inspectAnthropicMessages(raw []byte) {
	shape.toolDeclarations = arrayLength(gjson.GetBytes(raw, "tools"))
	for _, message := range arrayItems(gjson.GetBytes(raw, "messages")) {
		if message.Get("role").Str == "assistant" {
			shape.assistantMessages++
		}
		shape.inspectContentParts(message.Get("content"))
	}
}

// inspectContentParts reads one Chat, Responses or Anthropic content value: a
// string (text only), an array of parts, or the single part object a
// computer-use output carries. Anthropic tool results nest their own content,
// which may hold images, so they are read recursively.
func (shape *requestShape) inspectContentParts(content gjson.Result) {
	parts := arrayItems(content)
	if content.IsObject() {
		parts = []gjson.Result{content}
	}
	for _, part := range parts {
		switch part.Get("type").Str {
		case "image_url", "input_image", "image", "computer_screenshot":
			shape.noteMedia(&shape.image)
		case "input_audio", "audio":
			shape.noteMedia(&shape.audio)
		case "video_url", "input_video", "video":
			shape.noteMedia(&shape.video)
		case "tool_use", "server_tool_use", "mcp_tool_use":
			shape.toolCalls++
		case "tool_result":
			shape.toolMessages++
			shape.inspectContentParts(part.Get("content"))
		}
	}
}

func (shape *requestShape) noteMedia(kind *bool) {
	*kind = true
	shape.mediaParts++
}

// estimateTextTokens approximates the context tokens of every string in the
// body, keys included: system prompts, messages, tool arguments and results,
// and the tool declarations a model reads as part of its prompt. A Han, kana
// or Hangul character counts as one token and other text as one token per
// four characters, the usual rule of thumb across providers' tokenizers.
// Inlined payloads are skipped; media parts add their flat cost separately.
//
// The body is one the caller decoded already, so every quote outside a string
// opens one, and a single pass over the bytes reads every string without
// decoding the document again. A Responses request that continues server-side
// history carries only its newest items, so its estimate covers only what the
// request itself sends.
func estimateTextTokens(raw []byte) int {
	tokens := 0
	for index := 0; index < len(raw); index++ {
		if raw[index] == '"' {
			end, count := stringTokens(raw, index+1)
			tokens += count
			index = end
		}
	}
	return tokens
}

// stringTokens reads the JSON string that starts at start, just past its
// opening quote, and returns the index of its closing quote with the string's
// estimated tokens.
func stringTokens(raw []byte, start int) (int, int) {
	wide, other := 0, 0
	base64Only := true
	index := start
	for index < len(raw) {
		char := raw[index]
		switch {
		case char == '"':
			if isEncodedPayload(raw[start:index], base64Only) {
				return index, 0
			}
			return index, wide + (other+3)/4
		case char == '\\' && index+1 < len(raw):
			escaped := raw[index+1]
			index += 2
			if escaped == 'u' && index+4 <= len(raw) {
				if code, ok := hexRune(raw[index : index+4]); ok && isWideRune(code) {
					wide++
				} else {
					other++
				}
				index += 4
				base64Only = false
				continue
			}
			// Some encoders write a slash, which base64 uses, as "\/".
			if escaped != '/' {
				base64Only = false
			}
			other++
		case char < utf8.RuneSelf:
			if !isBase64Byte(char) {
				base64Only = false
			}
			other++
			index++
		default:
			code, size := utf8.DecodeRune(raw[index:])
			if isWideRune(code) {
				wide++
			} else {
				other++
			}
			base64Only = false
			index += size
		}
	}
	return index, wide + (other+3)/4
}

func isWideRune(code rune) bool {
	return unicode.In(code, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func isBase64Byte(char byte) bool {
	return 'A' <= char && char <= 'Z' || 'a' <= char && char <= 'z' || '0' <= char && char <= '9' ||
		char == '+' || char == '/' || char == '=' || char == '-' || char == '_'
}

func hexRune(digits []byte) (rune, bool) {
	var code rune
	for _, digit := range digits {
		switch {
		case '0' <= digit && digit <= '9':
			code = code<<4 | rune(digit-'0')
		case 'a' <= digit && digit <= 'f':
			code = code<<4 | rune(digit-'a'+10)
		case 'A' <= digit && digit <= 'F':
			code = code<<4 | rune(digit-'A'+10)
		default:
			return 0, false
		}
	}
	return code, true
}

// isEncodedPayload reports a base64 data URL or a long run of base64
// characters: image, audio and file bytes inlined into the body, or opaque
// signatures, none of which a model reads as text.
func isEncodedPayload(text []byte, base64Only bool) bool {
	if bytes.HasPrefix(text, []byte("data:")) {
		return bytes.Contains(text[:min(len(text), 256)], []byte(";base64,"))
	}
	return base64Only && len(text) >= minEncodedPayloadBytes
}

// arrayItems is the elements of an array value; anything else has none.
func arrayItems(value gjson.Result) []gjson.Result {
	if !value.IsArray() {
		return nil
	}
	return value.Array()
}

func arrayLength(value gjson.Result) int {
	return len(arrayItems(value))
}

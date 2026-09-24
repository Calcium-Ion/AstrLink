package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

func rewriteRequestModel(
	request *http.Request,
	classified Request,
	upstreamModel string,
	replayable bool,
) (*http.Request, error) {
	switch classified.Protocol {
	case contract.ProtocolGoogleGenerateContent:
		return rewriteGeminiPathModel(request, upstreamModel)
	case contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAICompletions,
		contract.ProtocolAnthropicMessages:
		return rewriteJSONBodyModel(request, upstreamModel, replayable)
	default:
		return nil, fmt.Errorf("alias rewrite is not supported for protocol %q", classified.Protocol)
	}
}

func rewriteGeminiPathModel(request *http.Request, upstreamModel string) (*http.Request, error) {
	path := request.URL.Path
	colon := strings.LastIndex(path, ":")
	if colon < 0 {
		return nil, fmt.Errorf("gemini path has no action separator")
	}
	prefix := path[:colon]
	action := path[colon+1:]
	const modelsPrefix = "/v1beta/models/"
	if !strings.HasPrefix(prefix, modelsPrefix) {
		return nil, fmt.Errorf("gemini path is missing models prefix")
	}
	setGeminiModelPath(request.URL, upstreamModel, ":"+action)
	return request, nil
}

// setGeminiModelPath stores the decoded model in Path and its escaped form in
// RawPath. Assigning an escaped string to Path would escape it a second time
// when the forwarder builds the target URL ("a/b" -> "a%252Fb").
func setGeminiModelPath(target *url.URL, model, suffix string) {
	const modelsPrefix = "/v1beta/models/"
	target.Path = modelsPrefix + model + suffix
	target.RawPath = modelsPrefix + url.PathEscape(model) + suffix
	if target.RawPath == target.Path {
		target.RawPath = ""
	}
}

func rewriteJSONBodyModel(request *http.Request, upstreamModel string, replayable bool) (*http.Request, error) {
	if !replayable {
		return nil, fmt.Errorf("alias rewrite requires a fully buffered request body")
	}
	contents, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	_ = request.Body.Close()

	valueStart, valueEnd, err := topLevelModelValueSpan(contents)
	if err != nil {
		return nil, err
	}
	newValue, err := json.Marshal(upstreamModel)
	if err != nil {
		return nil, err
	}
	rewritten := make([]byte, 0, len(contents)-(valueEnd-valueStart)+len(newValue))
	rewritten = append(rewritten, contents[:valueStart]...)
	rewritten = append(rewritten, newValue...)
	rewritten = append(rewritten, contents[valueEnd:]...)

	request.Body = io.NopCloser(bytes.NewReader(rewritten))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewritten)), nil
	}
	request.ContentLength = int64(len(rewritten))
	return request, nil
}

func topLevelModelValueSpan(contents []byte) (int, int, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()

	open, err := decoder.Token()
	if err != nil {
		return 0, 0, err
	}
	delim, ok := open.(json.Delim)
	if !ok || delim != '{' {
		return 0, 0, fmt.Errorf("request body is not a JSON object")
	}

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return 0, 0, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return 0, 0, fmt.Errorf("request object member key is not a string")
		}
		if key != "model" {
			if err := skipJSONValue(decoder); err != nil {
				return 0, 0, err
			}
			continue
		}
		start := decoder.InputOffset()
		valueToken, err := decoder.Token()
		if err != nil {
			return 0, 0, err
		}
		if _, ok := valueToken.(string); !ok {
			return 0, 0, fmt.Errorf("top-level model member must be a string")
		}
		end := decoder.InputOffset()
		relative := bytes.IndexByte(contents[start:end], '"')
		if relative < 0 {
			return 0, 0, fmt.Errorf("top-level model value has no opening quote")
		}
		return int(start) + relative, int(end), nil
	}
	return 0, 0, fmt.Errorf("request has no top-level model member to rewrite")
}

func skipJSONValue(decoder *json.Decoder) error {
	depth := 0
	read := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		read = true
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth == 0 && read {
			return nil
		}
	}
}

// modelMemberSpans finds every object-member string value whose key is in
// memberNames and whose decoded value equals expectedValue, at any depth.
func modelMemberSpans(
	contents []byte,
	memberNames map[string]bool,
	expectedValue string,
) ([][2]int, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()

	type frame struct {
		isObject  bool
		expectKey bool
	}
	var stack []frame
	var spans [][2]int
	var pendingKey string
	haveKey := false

	for {
		watching := len(stack) > 0 &&
			stack[len(stack)-1].isObject &&
			!stack[len(stack)-1].expectKey &&
			haveKey &&
			memberNames[pendingKey]
		var start int64
		if watching {
			start = decoder.InputOffset()
		}

		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				stack = append(stack, frame{isObject: true, expectKey: true})
			case '[':
				stack = append(stack, frame{isObject: false, expectKey: false})
			case '}', ']':
				if len(stack) == 0 {
					return nil, fmt.Errorf("json delimiter imbalance")
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].isObject {
					stack[len(stack)-1].expectKey = true
				}
			}
			haveKey = false
			continue
		}

		if len(stack) == 0 {
			continue
		}
		top := &stack[len(stack)-1]
		if top.isObject && top.expectKey {
			key, ok := tok.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			pendingKey = key
			haveKey = true
			top.expectKey = false
			continue
		}

		if watching {
			if value, ok := tok.(string); ok && value == expectedValue {
				end := decoder.InputOffset()
				relative := bytes.IndexByte(contents[start:end], '"')
				if relative < 0 {
					return nil, fmt.Errorf("model value has no opening quote")
				}
				spans = append(spans, [2]int{int(start) + relative, int(end)})
			}
		}
		haveKey = false
		if top.isObject {
			top.expectKey = true
		}
	}
	return spans, nil
}

func spliceSpans(contents []byte, spans [][2]int, replacement []byte) []byte {
	if len(spans) == 0 {
		return contents
	}
	size := len(contents)
	for _, span := range spans {
		size += len(replacement) - (span[1] - span[0])
	}
	out := make([]byte, 0, size)
	cursor := 0
	for _, span := range spans {
		out = append(out, contents[cursor:span[0]]...)
		out = append(out, replacement...)
		cursor = span[1]
	}
	out = append(out, contents[cursor:]...)
	return out
}

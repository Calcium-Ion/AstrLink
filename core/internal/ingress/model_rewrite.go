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

// rewriteRequestModel replaces only the model the upstream reads: the Gemini
// path segment, or the top-level JSON "model" value spliced in place. Every
// other request byte is forwarded unchanged; no DTO round trip is involved.
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
		return nil, fmt.Errorf("model rewrite is not supported for protocol %q", classified.Protocol)
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
		return nil, fmt.Errorf("model rewrite requires a fully buffered request body")
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

	valueStart, valueEnd := -1, -1
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
		// Keep scanning: like encoding/json, which classified the request and
		// matched the redirect, most upstreams read the last duplicate member.
		valueStart, valueEnd = int(start)+relative, int(end)
	}
	if valueStart < 0 {
		return 0, 0, fmt.Errorf("request has no top-level model member to rewrite")
	}
	return valueStart, valueEnd, nil
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

package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

var errProtocolPathNotFound = errors.New("protocol path not found")
var errInvalidMetadata = errors.New("invalid JSON request metadata")
var errMetadataTooLarge = errors.New("request metadata exceeds the M1 inspection limit")
var errUnsupportedContentEncoding = errors.New("encoded JSON request metadata cannot be inspected safely")

const maxMetadataBytes = 8 << 20
const maxModelRunes = 256

type methodNotAllowedError struct {
	allow string
}

func (err methodNotAllowedError) Error() string {
	return "method is not allowed for protocol path"
}

// Request describes the protocol facts needed before routing. It deliberately
// contains no converted DTO: Alpha forwards the original HTTP request body.
type Request struct {
	Protocol  contract.ProtocolID
	Model     string
	Streaming bool
}

type protocolRoute struct {
	method          string
	protocol        contract.ProtocolID
	streaming       bool
	inspectMetadata bool
}

var exactProtocolRoutes = map[string]protocolRoute{
	"/v1/responses": {
		method: http.MethodPost, protocol: contract.ProtocolOpenAIResponses, inspectMetadata: true,
	},
	"/v1/responses/compact": {
		method: http.MethodPost, protocol: contract.ProtocolOpenAIResponsesCompact, inspectMetadata: true,
	},
	"/v1/messages": {
		method: http.MethodPost, protocol: contract.ProtocolAnthropicMessages, inspectMetadata: true,
	},
	"/v1/chat/completions": {
		method: http.MethodPost, protocol: contract.ProtocolOpenAIChat, inspectMetadata: true,
	},
	"/v1/completions": {
		method: http.MethodPost, protocol: contract.ProtocolOpenAICompletions, inspectMetadata: true,
	},
	"/v1/models": {
		method: http.MethodGet, protocol: contract.ProtocolOpenAIModels,
	},
	"/v1beta/models": {
		method: http.MethodGet, protocol: contract.ProtocolGoogleModels,
	},
}

func classify(request *http.Request) (Request, error) {
	route, model, ok := matchProtocolRoute(request.URL.Path)
	if !ok {
		return Request{}, errProtocolPathNotFound
	}
	if request.Method != route.method {
		return Request{}, methodNotAllowedError{allow: route.method}
	}

	result := Request{
		Protocol:  route.protocol,
		Model:     model,
		Streaming: route.streaming,
	}
	if !route.inspectMetadata {
		return validateClassifiedRequest(result)
	}

	metadata, err := inspectJSONMetadata(request)
	if err != nil {
		return Request{}, err
	}
	if result.Protocol != contract.ProtocolOpenAIResponsesCompact {
		result.Streaming = metadata.Stream
	}
	if result.Model == "" {
		result.Model = metadata.Model
	}
	return validateClassifiedRequest(result)
}

func validateClassifiedRequest(request Request) (Request, error) {
	if utf8.RuneCountInString(request.Model) > maxModelRunes {
		return Request{}, errInvalidMetadata
	}
	return request, nil
}

func matchProtocolRoute(path string) (protocolRoute, string, bool) {
	if route, ok := exactProtocolRoutes[path]; ok {
		return route, "", true
	}

	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return protocolRoute{}, "", false
	}
	modelAndAction := strings.TrimPrefix(path, prefix)
	model, action, found := strings.Cut(modelAndAction, ":")
	if !found || model == "" || strings.Contains(model, "/") {
		return protocolRoute{}, "", false
	}
	switch action {
	case "generateContent":
		return protocolRoute{
			method: http.MethodPost, protocol: contract.ProtocolGoogleGenerateContent,
		}, model, true
	case "streamGenerateContent":
		return protocolRoute{
			method: http.MethodPost, protocol: contract.ProtocolGoogleGenerateContent, streaming: true,
		}, model, true
	default:
		return protocolRoute{}, "", false
	}
}

type requestMetadata struct {
	Model  string
	Stream bool
}

// inspectJSONMetadata observes routing fields without changing bytes that are
// forwarded. Malformed JSON is rejected locally because planning cannot safely
// infer its streaming/model requirements; encoded JSON is likewise rejected
// unless it explicitly uses the no-op identity encoding.
func inspectJSONMetadata(request *http.Request) (requestMetadata, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return requestMetadata{}, nil
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return requestMetadata{}, errUnsupportedContentEncoding
	}

	original := request.Body
	var consumed bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(io.LimitReader(original, maxMetadataBytes+1), &consumed))
	var fields map[string]json.RawMessage
	decodeErr := decoder.Decode(&fields)
	if decodeErr == nil {
		var extra json.RawMessage
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			decodeErr = errInvalidMetadata
		}
	}
	request.Body = &replayReadCloser{
		reader: io.MultiReader(bytes.NewReader(consumed.Bytes()), original),
		closer: original,
	}
	if consumed.Len() > maxMetadataBytes {
		return requestMetadata{}, errMetadataTooLarge
	}
	if errors.Is(decodeErr, io.EOF) && consumed.Len() == 0 {
		return requestMetadata{}, nil
	}
	if decodeErr != nil {
		return requestMetadata{}, errInvalidMetadata
	}
	if fields == nil {
		return requestMetadata{}, errInvalidMetadata
	}
	var metadata requestMetadata
	if rawModel, ok := fields["model"]; ok {
		trimmed := bytes.TrimSpace(rawModel)
		if len(trimmed) == 0 || trimmed[0] != '"' || json.Unmarshal(trimmed, &metadata.Model) != nil {
			return requestMetadata{}, errInvalidMetadata
		}
	}
	if rawStream, ok := fields["stream"]; ok {
		switch string(bytes.TrimSpace(rawStream)) {
		case "true":
			metadata.Stream = true
		case "false":
		default:
			return requestMetadata{}, errInvalidMetadata
		}
	}
	return metadata, nil
}

type replayReadCloser struct {
	mu        sync.Mutex
	reader    io.Reader
	closer    io.Closer
	closeOnce sync.Once
	closeErr  error
}

func (body *replayReadCloser) Read(buffer []byte) (int, error) {
	body.mu.Lock()
	reader := body.reader
	body.mu.Unlock()
	if reader == nil {
		return 0, http.ErrBodyReadAfterClose
	}
	return reader.Read(buffer)
}

func (body *replayReadCloser) Close() error {
	body.closeOnce.Do(func() {
		body.mu.Lock()
		body.reader = nil
		closer := body.closer
		body.mu.Unlock()
		body.closeErr = closer.Close()
	})
	return body.closeErr
}

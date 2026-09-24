package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotext"
)

var errProtocolPathNotFound = errors.New("protocol path not found")
var errInvalidMetadata = errors.New("invalid JSON request metadata")
var errMetadataTooLarge = errors.New("request body exceeds the configured size limit")
var errUnsupportedContentEncoding = errors.New("encoded JSON request metadata cannot be inspected safely")

// Response inspection buffers keep a separate bound from configurable request sizes.
const maxResponseInspectionBytes = 8 << 20
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
	ThinkingEnabled    *bool
	Protocol           contract.ProtocolID
	Model              string
	ReasoningEffort    *string
	Streaming          bool
	PreviousResponseID string
	ConversationID     string
	InputPreview       string
	// Conversation is the convo view of the replayed history used to link
	// this request to an earlier session. It is derived, never persisted or
	// logged; LastUserText/FirstUserText inside it are raw client text.
	Conversation convo.RequestSummary
	// lastUserText is classifier input only. Never persist, log, or cache it.
	lastUserText string
	// RedirectedModel is the routing model chosen by an enabled model
	// redirect rule. Model keeps the client's original id so responses and
	// records still report what the client asked for.
	RedirectedModel string
}

// routingModel is the model id used to select services and sent upstream when
// a candidate has no explicit upstream model.
func (request Request) routingModel() string {
	if request.RedirectedModel != "" {
		return request.RedirectedModel
	}
	return request.Model
}

type protocolRoute struct {
	method          string
	protocol        contract.ProtocolID
	streaming       bool
	inspectMetadata bool
}

// TODO(instance-proxy): Cover Claude Code direct telemetry through client launch
// configuration or a dedicated proxy ingress. Those requests currently bypass
// this gateway and cannot inherit the selected service proxy here.
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

// classifyFromPath identifies the protocol from the URL alone. It is used to
// record local boundary failures (auth, media type) without reading the body.
func classifyFromPath(request *http.Request) (Request, bool) {
	if request == nil || request.URL == nil {
		return Request{}, false
	}
	route, model, ok := matchProtocolRoute(request.URL.Path)
	if !ok {
		return Request{}, false
	}
	return Request{
		Protocol:  route.protocol,
		Model:     model,
		Streaming: route.streaming,
	}, true
}

func classify(request *http.Request, maxBodyBytes int64) (Request, error) {
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
		raw, err := inspectConversationBestEffort(&result, request, maxBodyBytes)
		if err != nil {
			return Request{}, err
		}
		attachAutoClassifyText(&result, request, raw, maxBodyBytes)
		return validateClassifiedRequest(result)
	}

	metadata, err := inspectJSONMetadata(request, route.protocol, maxBodyBytes)
	if err != nil {
		return Request{}, err
	}
	if result.Protocol != contract.ProtocolOpenAIResponsesCompact {
		result.Streaming = metadata.Stream
	}
	if result.Model == "" {
		result.Model = metadata.Model
	}
	result.ReasoningEffort = metadata.ReasoningEffort
	result.ThinkingEnabled = metadata.ThinkingEnabled
	result.PreviousResponseID = metadata.PreviousResponseID
	result.ConversationID = metadata.ConversationID
	result.InputPreview = metadata.InputPreview
	result.Conversation = metadata.Conversation
	attachAutoClassifyText(&result, request, metadata.raw, maxBodyBytes)
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
	if decoded, err := url.PathUnescape(modelAndAction); err == nil {
		modelAndAction = decoded
	}
	model, action, found := strings.Cut(modelAndAction, ":")
	if !found || model == "" {
		return protocolRoute{}, "", false
	}
	if strings.Contains(model, "/") && model != contract.AstrLinkAutoModelID {
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
	ThinkingEnabled    *bool
	Model              string
	ReasoningEffort    *string
	Stream             bool
	PreviousResponseID string
	ConversationID     string
	InputPreview       string
	Conversation       convo.RequestSummary
	raw                []byte
}

// inspectConversationBestEffort summarises the replayed history of protocols
// whose bodies AstrLink otherwise treats as opaque (Gemini: model and
// streaming come from the path). Encoded or malformed bodies are forwarded
// untouched and simply do not link; the configured size limit still applies.
// The buffered bytes are returned so the auto classifier can reuse them.
func inspectConversationBestEffort(result *Request, request *http.Request, maxBodyBytes int64) ([]byte, error) {
	convoProto, ok := convoProtocol(result.Protocol)
	if !ok {
		return nil, nil
	}
	raw, err := readAndReplayRequestBody(request, maxBodyBytes)
	if errors.Is(err, errMetadataTooLarge) {
		return nil, err
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if err != nil || len(raw) == 0 || (encoding != "" && encoding != "identity") {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return raw, nil
	}
	result.ReasoningEffort = extractReasoningEffort(result.Protocol, fields)
	summary, err := conversationPolicy.InspectFields(convoProto, fields)
	if err != nil {
		return raw, nil
	}
	result.Conversation = summary
	result.ConversationID = conversationCursor(summary, "")
	result.InputPreview = sanitizePreview(summary.LastUserText)
	return raw, nil
}

func attachAutoClassifyText(result *Request, request *http.Request, raw []byte, maxBodyBytes int64) {
	if result.Model != contract.AstrLinkAutoModelID {
		return
	}
	if len(raw) == 0 {
		buffered, err := bufferRequestBody(request, maxBodyBytes)
		if err != nil {
			return
		}
		raw = buffered
	}
	result.lastUserText = autotext.ExtractLastUserText(result.Protocol, raw)
}

func bufferRequestBody(request *http.Request, maxBodyBytes int64) ([]byte, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return nil, nil
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return nil, errUnsupportedContentEncoding
	}
	return readAndReplayRequestBody(request, maxBodyBytes)
}

func readRequestBody(reader io.Reader, maxBodyBytes int64) ([]byte, error) {
	if maxBodyBytes > 0 {
		reader = io.LimitReader(reader, maxBodyBytes+1)
	}
	body, err := io.ReadAll(reader)
	if maxBodyBytes > 0 && int64(len(body)) > maxBodyBytes {
		return body, errMetadataTooLarge
	}
	return body, err
}

func readAndReplayRequestBody(request *http.Request, maxBodyBytes int64) ([]byte, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return nil, nil
	}
	original := request.Body
	body, err := readRequestBody(original, maxBodyBytes)
	request.Body = &replayReadCloser{
		reader: io.MultiReader(bytes.NewReader(body), original),
		closer: original,
	}
	return body, err
}

// inspectJSONMetadata observes routing fields without changing bytes that are
// forwarded. Malformed JSON is rejected locally because planning cannot safely
// infer its streaming/model requirements; encoded JSON is likewise rejected
// unless it explicitly uses the no-op identity encoding.
func inspectJSONMetadata(request *http.Request, protocol contract.ProtocolID, maxBodyBytes int64) (requestMetadata, error) {
	raw, err := bufferRequestBody(request, maxBodyBytes)
	if err != nil {
		return requestMetadata{}, err
	}
	if len(raw) == 0 {
		return requestMetadata{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	decodeErr := decoder.Decode(&fields)
	if decodeErr == nil {
		var extra json.RawMessage
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			decodeErr = errInvalidMetadata
		}
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
	metadata.ReasoningEffort = extractReasoningEffort(protocol, fields)
	if raw, ok := fields["enable_thinking"]; ok {
		var enabled bool
		if json.Unmarshal(raw, &enabled) == nil {
			metadata.ThinkingEnabled = &enabled
		}
	}
	metadata.PreviousResponseID = extractProtocolCursor(fields, "previous_response_id")
	if convoProto, ok := convoProtocol(protocol); ok {
		// The summary walks the history once; conversation cursors and the
		// preview both come from it so the two never disagree.
		summary, err := conversationPolicy.InspectFields(convoProto, fields)
		if err == nil {
			metadata.Conversation = summary
			metadata.ConversationID = conversationCursor(summary, metadata.PreviousResponseID)
			metadata.InputPreview = sanitizePreview(summary.LastUserText)
		}
	}
	metadata.raw = raw
	return metadata, nil
}

// conversationCursor picks the first explicit cursor that is not the official
// previous_response_id chain. It feeds the legacy previous_response_id record
// column, which stores whichever inbound cursor named the conversation.
func conversationCursor(summary convo.RequestSummary, previousResponseID string) string {
	for _, cursor := range summary.ExplicitCursors {
		if cursor == previousResponseID {
			continue
		}
		return clampCursor(cursor)
	}
	return ""
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

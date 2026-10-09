package ingress

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// imagesRequest is the OpenAI Images body Codex sends for its image tool.
// Edits carry their reference images inline as data URLs.
type imagesRequest struct {
	Prompt     string           `json:"prompt"`
	N          *int             `json:"n"`
	Size       string           `json:"size"`
	Quality    string           `json:"quality"`
	Background string           `json:"background"`
	Images     []imageReference `json:"images"`
}

type imageReference struct {
	ImageURL string `json:"image_url"`
	FileID   string `json:"file_id"`
}

type imagesResponse struct {
	Created      int64                `json:"created"`
	Data         []imagesResponseData `json:"data"`
	OutputFormat string               `json:"output_format"`
	Usage        builtintools.Object  `json:"usage,omitempty"`
}

type imagesResponseData struct {
	B64JSON       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

const maxImagesEditReferences = 5

// A decoded RGBA canvas this large already takes about 160 MB.
const maxConvertedImagePixels = 40_000_000

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// serveImages answers /v1/images/generations and /v1/images/edits. The image
// generation configured under built-in tools comes first, and the client's
// model is replaced by the configured one. With it turned off, the request
// goes to the provider that served this Codex turn, when that provider
// generates images itself.
//
// Failures use 4xx statuses because Codex retries any 5xx up to four times,
// and each retry can bill another image.
func (handler *Handler) serveImages(writer http.ResponseWriter, request *http.Request, classified Request, settings contract.RoutingSettings) {
	session := recordSessionFromContext(request.Context())
	fail := func(status int, code, message string) {
		writeInferenceError(writer, status, code, message, false, nil)
		session.noteFailed(errorSummaryFromInference(code, message, false))
	}
	var config contract.BuiltinTool
	if settings.BuiltinTools != nil {
		config = settings.BuiltinTools.ImageGeneration
	}
	if !config.Enabled || config.Validate("image_generation") != nil {
		ref := codexTurnRef{turnID: codexTurnID(request.Header.Get(codexImageTurnHeader))}
		candidate, binding, ok := handler.codexToolProvider(request.Context(), ref, "image_generation")
		if !ok {
			session.captureUnreadRequestBody(request)
			fail(http.StatusUnprocessableEntity, "image_generation_disabled", "image generation is turned off, and this conversation's provider has no Images API; it can be turned on under Routing → Models & tools → Built-in tools")
			return
		}
		service := candidate.CanonicalService()
		model := codexToolModel(settings, service, classified.Model)
		if service.Kind == contract.ServiceKindCodexSubscription {
			handler.forwardCodexImages(writer, request, classified, settings, candidate, binding, model)
			return
		}
		// Codex names OpenAI's image model, which MiniMax does not serve.
		// MiniMax draws with image-01 unless a redirect or the provider's
		// own mapping chose another model.
		if model == classified.Model && minimaxImageKind(service.Kind) {
			model = minimaxDefaultImageModel
		}
		// The provider's Images API is called as a configured provider
		// Images backend would be, so its answer reaches Codex the same way.
		config = contract.BuiltinTool{Enabled: true, Backend: "service_images", ServiceID: service.ID, Model: model}
		if config.Validate("image_generation") != nil {
			session.captureUnreadRequestBody(request)
			fail(http.StatusBadRequest, "invalid_request", "image model is required")
			return
		}
	}
	finishPrivacy, _, err := handler.applyPrivacy(writer, request, classified, config.ServiceID)
	defer finishPrivacy()
	if err != nil {
		handler.writePrivacyError(writer, request, err)
		return
	}
	limit := handler.maxRequestBodyBytes
	if limit == 0 {
		limit = 128 << 20
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		fail(http.StatusBadRequest, "invalid_request", "image request could not be read")
		return
	}
	edit := request.URL.Path == "/v1/images/edits"
	invocation, err := imagesInvocation(body, edit)
	if err != nil {
		fail(http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	session.noteAttemptedService(config.ServiceID)
	if config.Backend != "upstream" {
		// An upstream Responses tool picks its own image model.
		session.noteModelRedirect(request.Context(), classified.Model, config.Model)
	}
	executor := handler.builtinExecutor(request, classified)
	// The whole request already passed the privacy policy above.
	executor.Inspect = nil
	started := time.Now()
	result, err := executor.Execute(request.Context(), config, invocation)
	session.noteBuiltinTool("image_generation", config, started, result, err)
	if request.Context().Err() != nil {
		return
	}
	if err != nil {
		fail(http.StatusFailedDependency, "image_generation_failed", err.Error())
		return
	}
	response := imagesResponse{Created: time.Now().Unix(), OutputFormat: "png", Usage: result.Usage}
	for index, raw := range result.Images {
		encoded, err := pngImage(raw)
		if err != nil {
			fail(http.StatusFailedDependency, "image_generation_failed", err.Error())
			return
		}
		data := imagesResponseData{B64JSON: encoded}
		if index < len(result.Items) {
			data.RevisedPrompt = builtintools.String(result.Items[index]["revised_prompt"])
		}
		response.Data = append(response.Data, data)
	}
	if len(response.Data) == 0 {
		fail(http.StatusFailedDependency, "image_generation_failed", "image backend returned no image")
		return
	}
	header := writer.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response)
	session.noteSucceeded()
}

// forwardCodexImages sends an image request to the Codex subscription that
// served the turn. Its answer is already what Codex expects, except that a 5xx
// becomes 424 so Codex does not retry it.
func (handler *Handler) forwardCodexImages(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	settings contract.RoutingSettings,
	candidate endpoint.Resolved,
	binding codexTurnBinding,
	model string,
) {
	session := recordSessionFromContext(request.Context())
	service := candidate.CanonicalService()
	fail := func(status int, code, message string) {
		writeInferenceError(writer, status, code, message, false, nil)
		session.noteFailed(errorSummaryFromInference(code, message, false))
	}
	finishPrivacy, _, err := handler.applyPrivacy(writer, request, classified, service.ID)
	defer finishPrivacy()
	if err != nil {
		handler.writePrivacyError(writer, request, err)
		return
	}
	limit := handler.maxRequestBodyBytes
	if limit == 0 {
		limit = 128 << 20
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		fail(http.StatusBadRequest, "invalid_request", "image request could not be read")
		return
	}
	if model != "" && model != classified.Model {
		if body, err = setJSONField(body, "model", model); err != nil {
			fail(http.StatusBadRequest, "invalid_request", "image request is not valid JSON")
			return
		}
		session.noteModelRedirect(request.Context(), classified.Model, model)
	}
	session.noteAttemptedService(service.ID)
	started := time.Now()
	status := 0
	err = handler.forwardCodexTool(writer, request, candidate, binding, settings, body, func(response *http.Response) error {
		status = response.StatusCode
		if status >= 500 {
			_ = response.Body.Close()
			return nil
		}
		return transport.WriteResponse(writer, response)
	})
	if err == nil && (status < 200 || status >= 300) {
		err = fmt.Errorf("the provider returned HTTP %d", status)
	}
	session.noteBuiltinTool("image_generation", contract.BuiltinTool{Backend: "provider", ServiceID: service.ID}, started, builtintools.Result{}, err)
	switch {
	case request.Context().Err() != nil:
	case status >= 500 || (status == 0 && err != nil):
		// Codex retries any 5xx, and each retry can bill another image.
		fail(http.StatusFailedDependency, "image_generation_failed", err.Error())
	case err != nil:
		// A 4xx answer, such as a reached image limit, went to Codex as is.
		session.noteFailed(errorSummaryFromInference("image_generation_failed", err.Error(), false))
	default:
		session.noteSucceeded()
	}
}

// setJSONField replaces one top-level field of a JSON object body. Other
// fields keep their bytes.
func setJSONField(body []byte, key string, value any) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("request body is not a JSON object")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields[key] = encoded
	return json.Marshal(fields)
}

// imagesInvocation maps an Images API body onto the built-in image tool.
// Size and quality "auto" are the upstream defaults, and an opaque background
// is what models without a background option produce, so only other values
// are passed on.
func imagesInvocation(body []byte, edit bool) (builtintools.Invocation, error) {
	var input imagesRequest
	if err := json.Unmarshal(body, &input); err != nil {
		return builtintools.Invocation{}, errors.New("image request is not valid JSON")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return builtintools.Invocation{}, errors.New("prompt is required")
	}
	if input.N != nil && *input.N != 1 {
		return builtintools.Invocation{}, errors.New("only one image per request is supported")
	}
	options := builtintools.Object{}
	if input.Size != "" && input.Size != "auto" {
		options["size"] = input.Size
	}
	if input.Quality != "" && input.Quality != "auto" {
		options["quality"] = input.Quality
	}
	if input.Background == "transparent" {
		options["background"] = input.Background
	}
	invocation := builtintools.Invocation{
		Kind:      "image_generation",
		Arguments: builtintools.Object{"prompt": input.Prompt},
		Options:   options,
	}
	if !edit {
		if len(input.Images) != 0 {
			return builtintools.Invocation{}, errors.New("reference images belong in /v1/images/edits")
		}
		return invocation, nil
	}
	if len(input.Images) == 0 {
		return builtintools.Invocation{}, errors.New("image editing requires a reference image")
	}
	if len(input.Images) > maxImagesEditReferences {
		return builtintools.Invocation{}, fmt.Errorf("at most %d reference images are supported", maxImagesEditReferences)
	}
	for _, reference := range input.Images {
		if !strings.HasPrefix(reference.ImageURL, "data:image/") {
			return builtintools.Invocation{}, errors.New("reference images must be inline data URLs; uploaded file IDs are not available here")
		}
		invocation.Images = append(invocation.Images, reference.ImageURL)
	}
	return invocation, nil
}

// pngImage returns the base64 PNG bytes of a generated image. Codex saves
// every result as a .png file and sends it back to the model as image/png,
// so other formats are converted here.
func pngImage(dataURL string) (string, error) {
	_, encoded, ok := strings.Cut(dataURL, ";base64,")
	if !ok {
		return "", errors.New("image backend returned an image that is not inline data")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return "", errors.New("image backend returned an image that could not be decoded")
	}
	if bytes.HasPrefix(data, pngSignature) {
		return encoded, nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("image backend returned an unsupported image format")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width*config.Height > maxConvertedImagePixels {
		return "", errors.New("generated image is too large to convert to PNG")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("image backend returned an image that could not be decoded")
	}
	var converted bytes.Buffer
	if err := png.Encode(&converted, decoded); err != nil {
		return "", errors.New("generated image could not be converted to PNG")
	}
	if converted.Len() > builtintools.MaxResultBytes {
		return "", errors.New("generated image is too large after conversion to PNG")
	}
	return base64.StdEncoding.EncodeToString(converted.Bytes()), nil
}

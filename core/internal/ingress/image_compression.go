package ingress

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/MoeForge/moego/base64"
	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/imagecompress"
)

// maxImageNesting bounds the walk; request images sit a few levels deep, also
// inside tool results.
const maxImageNesting = 32

// imageCompression is the Routing setting that re-encodes request images.
type imageCompression struct {
	enabled bool
	mode    contract.ImageCompressionMode
	options imagecompress.Options
}

func imageCompressionFrom(settings contract.RoutingSettings) imageCompression {
	value := settings.ImageCompression
	return imageCompression{
		enabled: value.Enabled,
		mode:    value.Mode,
		options: imagecompress.Options{Lossy: value.Mode == contract.ImageCompressionLossy, Quality: value.Quality},
	}
}

// imageCompressionResult is what image compression did to one attempt.
type imageCompressionResult struct {
	mode    contract.ImageCompressionMode
	quality int
	// images counts the inline images found, compressed those replaced.
	images, compressed int
	// before and after are the decoded bytes of every inline image.
	before, after int
	// startedAt and endedAt bound the step, reading the body included.
	startedAt, endedAt time.Time
}

// summary is the image_compression event summary, empty when the body held
// no inline image. The desktop parses it, so its shape is fixed:
// "lossless · 1/2 images · 75301 → 37406 bytes", or "lossy q80 · …".
func (result imageCompressionResult) summary() string {
	if result.images == 0 {
		return ""
	}
	mode := string(result.mode)
	if result.mode == contract.ImageCompressionLossy {
		mode += fmt.Sprintf(" q%d", result.quality)
	}
	return fmt.Sprintf("%s · %d/%d images · %d → %d bytes", mode, result.compressed, result.images, result.before, result.after)
}

// applyImageCompression re-encodes the inline images of one attempt's body
// as WebP. The body is in the upstream protocol, so converted requests are
// compressed in the upstream's own fields, and every attempt starts again
// from the client's body; the compressor's cache keeps retries cheap and
// their bytes the same. An image that cannot be compressed is forwarded as
// it was; only a failed read is an error.
func (handler *Handler) applyImageCompression(
	request *http.Request,
	protocol contract.ProtocolID,
	compression imageCompression,
) (imageCompressionResult, error) {
	if !compression.enabled || !rewritesProtocol(protocol) || request.Body == nil || request.Body == http.NoBody {
		return imageCompressionResult{}, nil
	}
	if encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		return imageCompressionResult{}, nil
	}
	started := time.Now().UTC()
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return imageCompressionResult{}, err
	}
	body, result := compressRequestImages(request.Context(), &handler.images, protocol, raw, compression)
	replaceRecoveryRequestBody(request, body)
	result.startedAt, result.endedAt = started, time.Now().UTC()
	return result, nil
}

// requestImage is one inline image in a request body: its base64 string,
// either a data URL or bare base64 with its media type in a sibling field.
type requestImage struct {
	data      ojson.Result
	mediaType ojson.Result
	dataURL   bool
}

// imageMarkers are strings every inline image of a protocol carries, so a
// body without one skips the walk.
func imageMarkers(protocol contract.ProtocolID) []string {
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return []string{`"image_url"`}
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return []string{`"input_image"`, `"computer_screenshot"`}
	case contract.ProtocolAnthropicMessages:
		return []string{`"base64"`}
	case contract.ProtocolGoogleGenerateContent:
		return []string{`"inlineData"`, `"inline_data"`}
	default:
		return nil
	}
}

func compressRequestImages(
	ctx context.Context,
	compressor *imagecompress.Compressor,
	protocol contract.ProtocolID,
	body []byte,
	compression imageCompression,
) ([]byte, imageCompressionResult) {
	result := imageCompressionResult{mode: compression.mode, quality: compression.options.Quality}
	if !slices.ContainsFunc(imageMarkers(protocol), func(marker string) bool { return bytes.Contains(body, []byte(marker)) }) ||
		!ojson.ValidBytes(body) {
		return body, result
	}
	var images []requestImage
	collectRequestImages(protocol, ojson.ParseBytes(body), 0, &images)
	type splice struct {
		start, end int
		text       string
	}
	var splices []splice
	for _, image := range images {
		encoded := image.data.Str
		if image.dataURL {
			encoded, _ = imageDataURLPayload(encoded)
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil || len(data) == 0 {
			continue
		}
		result.images++
		result.before += len(data)
		if ctx.Err() != nil {
			result.after += len(data)
			continue
		}
		webp, ok := compressor.WebP(ctx, data, compression.options)
		if !ok || !spanMatches(body, image.data) || (!image.dataURL && !spanMatches(body, image.mediaType)) {
			result.after += len(data)
			continue
		}
		result.compressed++
		result.after += len(webp)
		replacement := base64.StdEncoding.EncodeToString(webp)
		if image.dataURL {
			replacement = "data:image/webp;base64," + replacement
		} else {
			splices = append(splices, splice{image.mediaType.Index, image.mediaType.Index + len(image.mediaType.Raw), `"image/webp"`})
		}
		splices = append(splices, splice{image.data.Index, image.data.Index + len(image.data.Raw), `"` + replacement + `"`})
	}
	if len(splices) == 0 {
		return body, result
	}
	slices.SortFunc(splices, func(a, b splice) int { return a.start - b.start })
	var rebuilt bytes.Buffer
	rebuilt.Grow(len(body))
	last := 0
	for _, edit := range splices {
		rebuilt.Write(body[last:edit.start])
		rebuilt.WriteString(edit.text)
		last = edit.end
	}
	rebuilt.Write(body[last:])
	return rebuilt.Bytes(), result
}

// spanMatches checks that a value's offset points at its raw text, so a
// splice never lands anywhere else.
func spanMatches(body []byte, value ojson.Result) bool {
	end := value.Index + len(value.Raw)
	return value.Index > 0 && value.Type == ojson.String && end <= len(body) && string(body[value.Index:end]) == value.Raw
}

func collectRequestImages(protocol contract.ProtocolID, value ojson.Result, depth int, images *[]requestImage) {
	if depth > maxImageNesting || (!value.IsObject() && !value.IsArray()) {
		return
	}
	if value.IsObject() {
		if image, ok := requestImageIn(protocol, value); ok {
			*images = append(*images, image)
			return
		}
	}
	value.ForEach(func(_, child ojson.Result) bool {
		collectRequestImages(protocol, child, depth+1, images)
		return true
	})
}

// requestImageIn reports the inline image an object of the protocol holds:
// a Chat image_url part, a Responses input_image or computer screenshot, an
// Anthropic base64 image block or a Gemini inline-data part. Image URLs and
// uploaded file IDs are not inline images.
func requestImageIn(protocol contract.ProtocolID, object ojson.Result) (requestImage, bool) {
	kind := object.Get("type").Str
	switch protocol {
	case contract.ProtocolOpenAIChat:
		if kind != "image_url" {
			return requestImage{}, false
		}
		url := object.Get("image_url")
		if url.IsObject() {
			url = url.Get("url")
		}
		return dataURLImage(url)
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		if kind != "input_image" && kind != "computer_screenshot" {
			return requestImage{}, false
		}
		return dataURLImage(object.Get("image_url"))
	case contract.ProtocolAnthropicMessages:
		source := object.Get("source")
		if kind != "image" || source.Get("type").Str != "base64" {
			return requestImage{}, false
		}
		return base64Image(source.Get("data"), source.Get("media_type"))
	case contract.ProtocolGoogleGenerateContent:
		for _, field := range []struct{ part, mediaType string }{{"inlineData", "mimeType"}, {"inline_data", "mime_type"}} {
			if inline := object.Get(field.part); inline.IsObject() {
				return base64Image(inline.Get("data"), inline.Get(field.mediaType))
			}
		}
	}
	return requestImage{}, false
}

func dataURLImage(url ojson.Result) (requestImage, bool) {
	if url.Type != ojson.String {
		return requestImage{}, false
	}
	if _, ok := imageDataURLPayload(url.Str); !ok {
		return requestImage{}, false
	}
	return requestImage{data: url, dataURL: true}, true
}

func base64Image(data, mediaType ojson.Result) (requestImage, bool) {
	if data.Type != ojson.String || mediaType.Type != ojson.String || !strings.HasPrefix(strings.ToLower(mediaType.Str), "image/") {
		return requestImage{}, false
	}
	return requestImage{data: data, mediaType: mediaType}, true
}

// imageDataURLPayload returns the base64 payload of a data:image/…;base64,
// URL.
func imageDataURLPayload(url string) (string, bool) {
	header, payload, ok := strings.Cut(url, ",")
	if !ok || len(header) < len("data:image/") || !strings.EqualFold(header[:len("data:image/")], "data:image/") ||
		!strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", false
	}
	return payload, true
}

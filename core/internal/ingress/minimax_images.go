package ingress

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
)

// minimaxImageKind reports provider kinds whose images come from MiniMax's
// image_generation API rather than the OpenAI Images endpoints.
func minimaxImageKind(kind contract.ServiceKind) bool {
	return kind == contract.ServiceKindMiniMax || kind == contract.ServiceKindMiniMaxCoding
}

// minimaxDefaultImageModel draws Codex's own image requests on a MiniMax
// provider that served the turn, unless a redirect names another model.
const minimaxDefaultImageModel = "image-01"

// minimaxAspectRatios are the aspect_ratio values MiniMax accepts. Only
// image-01 takes 21:9.
var minimaxAspectRatios = []struct {
	name          string
	width, height float64
}{
	{"1:1", 1, 1}, {"16:9", 16, 9}, {"4:3", 4, 3}, {"3:2", 3, 2},
	{"2:3", 2, 3}, {"3:4", 3, 4}, {"9:16", 9, 16}, {"21:9", 21, 9},
}

// minimaxImageRequest converts the OpenAI Images request the built-in image
// tool builds into a MiniMax image_generation body. MiniMax's image-to-image
// keeps the person shown in a reference photo, so the images of an edit
// become subject references.
func minimaxImageRequest(path, contentType string, body io.Reader) ([]byte, error) {
	fields := map[string]string{}
	references := []builtintools.Object{}
	switch path {
	case "/images/generations":
		var input builtintools.Object
		if err := json.NewDecoder(body).Decode(&input); err != nil {
			return nil, errors.New("image request is not valid JSON")
		}
		for key, value := range input {
			fields[key] = builtintools.String(value)
		}
	case "/images/edits":
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return nil, errors.New("image request form is invalid")
		}
		form := multipart.NewReader(body, params["boundary"])
		for {
			part, err := form.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, errors.New("image request form is invalid")
			}
			data, err := io.ReadAll(io.LimitReader(part, builtintools.MaxImageBytes+1))
			if err != nil || len(data) > builtintools.MaxImageBytes {
				return nil, errors.New("image request form is invalid")
			}
			switch part.FormName() {
			case "image[]":
				references = append(references, builtintools.Object{
					"type":       "character",
					"image_file": "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data),
				})
			case "mask":
				return nil, errors.New("MiniMax image models do not support masks")
			default:
				fields[part.FormName()] = string(data)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported image request path %s", path)
	}
	if fields["background"] == "transparent" {
		return nil, errors.New("MiniMax image models do not support transparent backgrounds")
	}
	// Base64 keeps the image in this one response. MiniMax's image URLs need
	// a second download and expire after 24 hours.
	request := builtintools.Object{"model": fields["model"], "prompt": fields["prompt"], "n": 1, "response_format": "base64"}
	if err := minimaxImageSize(request, fields["model"], fields["size"]); err != nil {
		return nil, err
	}
	if len(references) > 0 {
		request["subject_reference"] = references
	}
	return json.Marshal(request)
}

// minimaxImageSize maps an OpenAI Images size onto MiniMax. image-01 takes
// the exact size when both sides are 512 to 2048 pixels and multiples of 8.
// Any other size gets the closest aspect ratio the model offers.
func minimaxImageSize(request builtintools.Object, model, size string) error {
	if size == "" || size == "auto" {
		return nil
	}
	w, h, _ := strings.Cut(size, "x")
	width, widthErr := strconv.Atoi(w)
	height, heightErr := strconv.Atoi(h)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return fmt.Errorf("image size %q is not WIDTHxHEIGHT", size)
	}
	exact := func(side int) bool { return side >= 512 && side <= 2048 && side%8 == 0 }
	if model == "image-01" && exact(width) && exact(height) {
		request["width"], request["height"] = width, height
		return nil
	}
	closest, distance := "", math.Inf(1)
	for _, ratio := range minimaxAspectRatios {
		if ratio.name == "21:9" && model != "image-01" {
			continue
		}
		// Comparing logarithms weighs portrait and landscape alike.
		if d := math.Abs(math.Log(float64(width)/float64(height)) - math.Log(ratio.width/ratio.height)); d < distance {
			closest, distance = ratio.name, d
		}
	}
	request["aspect_ratio"] = closest
	return nil
}

// minimaxImageResponse reads a MiniMax image_generation answer into the
// OpenAI Images shape the built-in image tool reads. MiniMax reports failures
// in base_resp with HTTP 200.
func minimaxImageResponse(data []byte) (builtintools.Object, error) {
	var response struct {
		Data struct {
			ImageBase64 []string `json:"image_base64"`
			ImageURLs   []string `json:"image_urls"`
		} `json:"data"`
		BaseResp *struct {
			StatusCode int `json:"status_code"`
		} `json:"base_resp"`
	}
	if json.Unmarshal(data, &response) != nil || response.BaseResp == nil {
		return nil, errors.New("invalid image provider response")
	}
	if code := response.BaseResp.StatusCode; code != 0 {
		return nil, minimaxImageError(code)
	}
	images := []any{}
	for _, encoded := range response.Data.ImageBase64 {
		images = append(images, builtintools.Object{"b64_json": encoded})
	}
	for _, address := range response.Data.ImageURLs {
		images = append(images, builtintools.Object{"url": address})
	}
	if len(images) == 0 {
		// A successful request returns fewer images only when MiniMax's
		// content check withholds them.
		return nil, errors.New("MiniMax's content check withheld the generated image")
	}
	return builtintools.Object{"data": images}, nil
}

// minimaxImageError names a MiniMax status code. MiniMax's own message stays
// out of the error, which reaches request records, since it may echo the
// prompt.
func minimaxImageError(code int) error {
	var reason string
	switch code {
	case 1002, 2045:
		reason = "rate limited"
	case 1004, 2049:
		reason = "the API key was rejected"
	case 1008:
		reason = "insufficient balance"
	case 2056:
		reason = "the plan's usage limit for the current period is reached"
	case 1026:
		reason = "the prompt was flagged as sensitive"
	case 1027:
		reason = "the generated image was flagged as sensitive"
	case 2013:
		reason = "invalid request parameters"
	default:
		return fmt.Errorf("MiniMax image generation failed with status %d", code)
	}
	return fmt.Errorf("MiniMax image generation failed with status %d: %s", code, reason)
}

package providerapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var vertexModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)
var vertexGeminiPath = regexp.MustCompile(`^/v1(?:beta)?/models/([^/]+):(generateContent|streamGenerateContent)$`)

// VertexRequest adjusts only the provider envelope after protocol conversion.
func VertexRequest(request *http.Request, connection *contract.HTTPConnection) error {
	if connection == nil || connection.Vertex == nil {
		return fmt.Errorf("invalid Vertex connection")
	}
	vertex := connection.Vertex
	if err := vertex.Validate(); err != nil {
		return err
	}
	if connection.BaseURL != vertex.BaseURL() || connection.Auth.Scheme != contract.AuthSchemeGoogleServiceAccount {
		return fmt.Errorf("invalid Vertex connection")
	}
	if request.Method != http.MethodPost {
		return fmt.Errorf("unsupported Vertex operation")
	}
	root := "/v1/projects/" + vertex.ProjectID + "/locations/" + vertex.Location
	query := request.URL.Query()
	query.Del("key")
	query.Del("access_token")
	request.Header.Set("X-Goog-User-Project", vertex.ProjectID)
	request.Header.Del("X-Goog-Api-Key")
	switch {
	case vertexGeminiPath.MatchString(request.URL.Path):
		match := vertexGeminiPath.FindStringSubmatch(request.URL.Path)
		if !vertexModelID.MatchString(match[1]) || !strings.HasPrefix(match[1], "gemini-") {
			return fmt.Errorf("Vertex Gemini requires an official Gemini model ID")
		}
		request.URL.Path = root + "/publishers/google/models/" + match[1] + ":" + match[2]
		if match[2] == "streamGenerateContent" {
			query.Set("alt", "sse")
		}
	case request.URL.Path == "/v1/messages", request.URL.Path == "/v1/chat/completions":
		if request.Body == nil {
			return fmt.Errorf("missing Vertex request body")
		}
		body, err := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return err
		}
		if !gjson.ValidBytes(body) {
			return fmt.Errorf("invalid Vertex request JSON")
		}
		modelValue := gjson.GetBytes(body, "model")
		if modelValue.Type != gjson.String {
			return fmt.Errorf("Vertex model must be a string")
		}
		model := modelValue.String()
		if request.URL.Path == "/v1/messages" {
			if !vertexModelID.MatchString(model) || !strings.HasPrefix(model, "claude-") {
				return fmt.Errorf("Vertex Claude requires an official Claude model ID")
			}
			action := "rawPredict"
			if gjson.GetBytes(body, "stream").Bool() {
				action = "streamRawPredict"
			}
			request.URL.Path = root + "/publishers/anthropic/models/" + model + ":" + action
			body, err = sjson.DeleteBytes(body, "model")
			if err == nil {
				body, err = sjson.SetBytes(body, "anthropic_version", "vertex-2023-10-16")
			}
			if err != nil {
				return err
			}
			request.Header.Del("Anthropic-Version")
		} else {
			publisher, id, ok := strings.Cut(model, "/")
			if !ok || !vertexModelID.MatchString(publisher) || !vertexModelID.MatchString(id) {
				return fmt.Errorf("Vertex Chat requires a publisher/model ID")
			}
			request.URL.Path = root + "/endpoints/openapi/chat/completions"
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		request.ContentLength = int64(len(body))
		request.Header.Del("Content-Length")
	default:
		return fmt.Errorf("unsupported Vertex operation")
	}
	request.URL.RawPath = ""
	request.URL.RawQuery = query.Encode()
	return nil
}

package providerapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
)

var vertexModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)
var vertexGeminiPath = regexp.MustCompile(`^/v1(?:beta)?/models/([^/]+):(generateContent|streamGenerateContent)$`)

// VertexProjectHeader supplies the key's project when http.vertex is absent.
// VertexRequest consumes it before forwarding.
const VertexProjectHeader = "X-AstrLink-Vertex-Project"

func vertexBaseURL(base *url.URL) *url.URL {
	segments := strings.Split(base.EscapedPath(), "/")
	for index, segment := range segments {
		if segment != "v1" && segment != "v1beta1" && segment != "v1beta" {
			continue
		}
		updated := *base
		updated.RawPath = strings.Join(segments[:index], "/")
		updated.Path, _ = url.PathUnescape(updated.RawPath)
		if updated.RawPath == updated.Path {
			updated.RawPath = ""
		}
		return &updated
	}
	return base
}

// VertexRequest adjusts only the provider envelope after protocol conversion.
func VertexRequest(request *http.Request, connection *contract.HTTPConnection) error {
	if connection == nil {
		return fmt.Errorf("invalid Vertex connection")
	}
	checked := *connection
	checked.CredentialRef = ""
	if err := checked.Validate(""); err != nil {
		return err
	}
	express := connection.Auth.Scheme == contract.AuthSchemeGoogleAPIKey
	if !express && connection.Auth.Scheme != contract.AuthSchemeGoogleServiceAccount {
		return fmt.Errorf("invalid Vertex connection")
	}
	base, _ := url.Parse(connection.BaseURL) // Validate already checked the URL.
	base = vertexBaseURL(base)
	prefix := strings.TrimRight(base.Path, "/")
	path := strings.TrimPrefix(request.URL.Path, prefix)
	root := prefix + "/v1"
	project := request.Header.Get(VertexProjectHeader)
	request.Header.Del(VertexProjectHeader)
	if !express {
		vertex := connection.Vertex
		if vertex == nil {
			vertex = &contract.VertexConnection{ProjectID: project, Location: contract.VertexLocation(connection.BaseURL)}
		}
		if err := vertex.Validate(); err != nil {
			return err
		}
		root += "/projects/" + vertex.ProjectID + "/locations/" + vertex.Location
		request.Header.Set("X-Goog-User-Project", vertex.ProjectID)
		request.Header.Del("X-Goog-Api-Key")
	} else {
		request.Header.Del("Authorization")
		request.Header.Del("X-Goog-User-Project")
	}
	if request.Method != http.MethodPost {
		return fmt.Errorf("unsupported Vertex operation")
	}
	query := request.URL.Query()
	query.Del("key")
	query.Del("access_token")
	switch {
	case vertexGeminiPath.MatchString(path):
		match := vertexGeminiPath.FindStringSubmatch(path)
		if !vertexModelID.MatchString(match[1]) || !strings.HasPrefix(match[1], "gemini-") {
			return fmt.Errorf("Vertex Gemini requires an official Gemini model ID")
		}
		request.URL.Path = root + "/publishers/google/models/" + match[1] + ":" + match[2]
		if match[2] == "streamGenerateContent" {
			query.Set("alt", "sse")
		}
	case !express && (path == "/v1/messages" || path == "/v1/chat/completions"):
		if request.Body == nil {
			return fmt.Errorf("missing Vertex request body")
		}
		body, err := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return err
		}
		if !ojson.ValidBytes(body) {
			return fmt.Errorf("invalid Vertex request JSON")
		}
		modelValue := ojson.GetBytes(body, "model")
		if modelValue.Type != ojson.String {
			return fmt.Errorf("Vertex model must be a string")
		}
		model := modelValue.String()
		if path == "/v1/messages" {
			if !vertexModelID.MatchString(model) || !strings.HasPrefix(model, "claude-") {
				return fmt.Errorf("Vertex Claude requires an official Claude model ID")
			}
			action := "rawPredict"
			if ojson.GetBytes(body, "stream").Bool() {
				action = "streamRawPredict"
			}
			request.URL.Path = root + "/publishers/anthropic/models/" + model + ":" + action
			body, err = ojson.DeleteBytes(body, "model")
			if err == nil {
				body, err = ojson.SetBytes(body, "anthropic_version", "vertex-2023-10-16")
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
	request.URL.RawPath = strings.TrimRight(base.EscapedPath(), "/") + (&url.URL{Path: strings.TrimPrefix(request.URL.Path, prefix)}).EscapedPath()
	if request.URL.RawPath == request.URL.Path {
		request.URL.RawPath = ""
	}
	request.URL.RawQuery = query.Encode()
	return nil
}

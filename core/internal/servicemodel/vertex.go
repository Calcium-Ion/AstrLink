package servicemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

func (prober *Prober) probeVertex(ctx context.Context, connection contract.HTTPConnection, token string) (Discovery, error) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-Goog-User-Project", connection.Vertex.ProjectID)
	discovery, err := prober.probeHTTPPages(ctx, connection.BaseURL, headers, contract.ProtocolGoogleModels, false,
		"/v1beta1/publishers/*/models", true)
	if err == nil && len(discovery.Models) == 0 {
		return Discovery{}, fmt.Errorf("%w: Vertex catalog contains no identifiable chat model IDs; add official model IDs manually", ErrUpstream)
	}
	return discovery, err
}

var vertexPublisherModel = regexp.MustCompile(`^publishers/([a-z0-9-]+)/models/([A-Za-z0-9][A-Za-z0-9._@-]*)$`)

func decodeVertexPage(body []byte) ([]listedModel, string, bool, error) {
	var page struct {
		Models []struct {
			Name     string `json:"name"`
			Template string `json:"publisherModelTemplate"`
		} `json:"publisherModels"`
		NextPageToken string `json:"nextPageToken"`
	}
	if json.Unmarshal(body, &page) != nil || page.Models == nil {
		return nil, "", false, fmt.Errorf("%w: invalid Vertex catalog response", ErrUpstream)
	}
	models := make([]listedModel, 0, len(page.Models))
	for _, model := range page.Models {
		name := vertexPublisherModel.FindStringSubmatch(model.Name)
		if name == nil {
			continue
		}
		resource := strings.TrimPrefix(model.Template, "projects/{project}/locations/{location}/")
		resolved := vertexPublisherModel.FindStringSubmatch(resource)
		if resolved == nil || resolved[1] != name[1] {
			continue
		}
		publisher, id := resolved[1], strings.TrimSuffix(resolved[2], "@default")
		// A catalog version is not necessarily an inference version. Only use
		// the published resource template, never append versionId speculatively.
		if id != name[2] && strings.Split(id, "@")[0] != strings.Split(name[2], "@")[0] {
			continue
		}
		format := contract.ProtocolOpenAIChat
		switch {
		case publisher == "google" && strings.HasPrefix(id, "gemini-"):
			if strings.Contains(id, "embedding") || strings.Contains(id, "live") || strings.Contains(id, "-tts") || strings.Contains(id, "-transcribe") {
				continue
			}
			format = contract.ProtocolGoogleGenerateContent
		case publisher == "anthropic" && strings.HasPrefix(id, "claude-"):
			format = contract.ProtocolAnthropicMessages
		case publisher != "google" && publisher != "anthropic" && strings.HasSuffix(id, "-maas"):
			id = publisher + "/" + id
		default:
			// Catalog cards also describe deployable models and non-chat APIs.
			// Unknown serving formats remain available through manual model entry.
			continue
		}
		models = append(models, listedModel{id: id, format: format})
	}
	return models, page.NextPageToken, page.NextPageToken != "", nil
}

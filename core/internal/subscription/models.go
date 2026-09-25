package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func chatgptBackendBase(apiBase string) (string, bool) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if !strings.Contains(base, "/backend-api") {
		return base, false
	}
	if strings.HasSuffix(base, "/codex") {
		base = strings.TrimSuffix(base, "/codex")
	}
	return base, true
}

// CodexUsageURL builds the official openai/codex usage lookup.
// ChatGPT backend-api bases use GET {chatgpt_backend}/wham/usage; other
// Codex API bases use GET {base}/api/codex/usage.
func CodexUsageURL(apiBase string) string {
	base, chatgpt := chatgptBackendBase(apiBase)
	if chatgpt {
		return base + "/wham/usage"
	}
	return base + "/api/codex/usage"
}

// CodexConsumeResetURL builds the official openai/codex reset-credit consume
// path from backend-client (`POST …/rate-limit-reset-credits/consume`).
func CodexConsumeResetURL(apiBase string) string {
	base, chatgpt := chatgptBackendBase(apiBase)
	if chatgpt {
		return base + "/wham/rate-limit-reset-credits/consume"
	}
	return base + "/api/codex/rate-limit-reset-credits/consume"
}

// CodexModelsURL builds GET {apiBase}/models?client_version=… as required by
// the official openai/codex ModelsClient.
func CodexModelsURL(apiBase, clientVersion string) string {
	return strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/models?" +
		codexModelsQuery(nil, clientVersion).Encode()
}

// ApplyCodexModelsQuery sets client_version when the incoming request did not
// already supply one.
func ApplyCodexModelsQuery(query url.Values, clientVersion string) {
	_ = codexModelsQuery(query, clientVersion)
}

func codexModelsQuery(query url.Values, clientVersion string) url.Values {
	if query == nil {
		query = make(url.Values)
	}
	if query.Get("client_version") == "" {
		if strings.TrimSpace(clientVersion) == "" {
			clientVersion = accountauth.DefaultCodexModelsClientVersion
		}
		query.Set("client_version", clientVersion)
	}
	return query
}

type officialCodexModel struct {
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
}

// DecodeCodexModels reads the official Codex catalog envelope
// `{models:[{slug,visibility}]}` and the OpenAI-compatible `{data:[{id}]}`
// shape used by older tests and gateways.
func DecodeCodexModels(body []byte) (ModelList, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ModelList{}, fmt.Errorf("decode codex models: %w", err)
	}
	if envelope == nil {
		return ModelList{}, fmt.Errorf("decode codex models: empty object")
	}
	var list ModelList
	var err error
	if raw, ok := envelope["models"]; ok {
		list, err = decodeOfficialCodexModels(raw)
	} else if raw, ok := envelope["data"]; ok {
		list, err = decodeOpenAICompatibleModels(raw)
	} else {
		return ModelList{}, fmt.Errorf("decode codex models: missing models or data")
	}
	if err != nil {
		return ModelList{}, err
	}
	// Codex's auto-review model may be omitted or hidden in the upstream catalog.
	for _, model := range list.Data {
		if model.ID == "codex-auto-review" {
			return list, nil
		}
	}
	list.Data = append(list.Data, ModelRecord{ID: "codex-auto-review", Object: "model"})
	return list, nil
}

func decodeOfficialCodexModels(raw json.RawMessage) (ModelList, error) {
	if !jsonArray(raw) {
		return ModelList{}, fmt.Errorf("decode codex models: models is not an array")
	}
	var models []officialCodexModel
	if err := json.Unmarshal(raw, &models); err != nil {
		return ModelList{}, fmt.Errorf("decode codex models: %w", err)
	}
	list := ModelList{Object: "list", Data: make([]ModelRecord, 0, len(models))}
	for _, model := range models {
		slug := strings.TrimSpace(model.Slug)
		if slug == "" || hiddenCodexVisibility(model.Visibility) {
			continue
		}
		list.Data = append(list.Data, ModelRecord{ID: slug, Object: "model"})
	}
	return list, nil
}

func decodeOpenAICompatibleModels(raw json.RawMessage) (ModelList, error) {
	if !jsonArray(raw) {
		return ModelList{}, fmt.Errorf("decode codex models: data is not an array")
	}
	var records []ModelRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return ModelList{}, fmt.Errorf("decode codex models: %w", err)
	}
	list := ModelList{Object: "list", Data: make([]ModelRecord, 0, len(records))}
	for _, record := range records {
		id := strings.TrimSpace(record.ID)
		if id == "" {
			continue
		}
		if record.Object == "" {
			record.Object = "model"
		}
		record.ID = id
		list.Data = append(list.Data, record)
	}
	return list, nil
}

func hiddenCodexVisibility(visibility string) bool {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "hide", "none":
		return true
	default:
		return false
	}
}

func jsonArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// EncodeOpenAIModelDiscovery rewrites a decoded Codex catalog into the
// OpenAI `{data:[{id}]}` bytes ingress already knows how to aggregate.
func EncodeOpenAIModelDiscovery(list ModelList) ([]byte, error) {
	type entry struct {
		ID string `json:"id"`
	}
	data := make([]entry, 0, len(list.Data))
	for _, model := range list.Data {
		data = append(data, entry{ID: model.ID})
	}
	return json.Marshal(struct {
		Data []entry `json:"data"`
	}{Data: data})
}

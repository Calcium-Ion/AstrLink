package ingress

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

// Codex's models endpoint shares the OpenAI path but uses a capability catalog,
// not data[].id. Match the original request before upstream identity overlays.
func wantsCodexModelCatalog(request *http.Request, protocol contract.ProtocolID) bool {
	return protocol == contract.ProtocolOpenAIModels &&
		(strings.TrimSpace(request.URL.Query().Get("client_version")) != "" ||
			detectClientType(request.Header) == contract.ClientCodex)
}

func codexCatalogDiscoveryEntries(list subscription.ModelList) ([]discoveryEntry, error) {
	entries := make([]discoveryEntry, 0, len(list.Data))
	for _, model := range list.Data {
		raw, err := json.Marshal(struct {
			ID string `json:"id"`
		}{ID: model.ID})
		if err != nil {
			return nil, err
		}
		entries = append(entries, discoveryEntry{id: model.ID, raw: raw, codexCatalog: model.CodexCatalog})
	}
	return entries, nil
}

func encodeCodexModelCatalog(entries []discoveryEntry) ([]byte, error) {
	models := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		model, err := encodeCodexCatalogEntry(entry)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return json.Marshal(struct {
		Models []json.RawMessage `json:"models"`
	}{Models: models})
}

func encodeCodexCatalogEntry(entry discoveryEntry) ([]byte, error) {
	return contract.CodexCatalogEntry(entry.id, entry.codexCatalog, nil, 0)
}

// A redirect takes precedence even when the upstream also lists its source.
func withCodexRedirects(entries []discoveryEntry, rules []contract.ModelRedirect) []discoveryEntry {
	if len(rules) == 0 {
		return entries
	}
	indices := make(map[string]int, len(entries))
	for i, entry := range entries {
		indices[entry.id] = i
	}
	for _, rule := range rules {
		target, exists := indices[rule.To]
		if !rule.Enabled || !exists || rule.From == "" || rule.From == contract.AstrLinkAutoModelID {
			continue
		}
		raw := entries[target].codexCatalog
		if source, ok := indices[rule.From]; ok {
			entries[source].codexCatalog = raw
		} else {
			entries = append(entries, discoveryEntry{id: rule.From, codexCatalog: raw})
		}
	}
	return entries
}

// Only the original declarations are returned to the desktop. Missing fields
// can then be filled from official metadata without overriding an empty list.
func encodeUpstreamCodexCatalog(entries []discoveryEntry) ([]byte, error) {
	models := map[string]json.RawMessage{}
	for _, entry := range entries {
		if len(entry.codexCatalog) > 0 {
			models[entry.id] = entry.codexCatalog
		}
	}
	return json.Marshal(models)
}

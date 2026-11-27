package controlapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/modelmeta"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const ModelMetadataPath = "/control/v1/model-metadata"

// getModelMetadata answers what LLM Metadata says about the official model
// a gateway model reaches.
func (h *Handler) getModelMetadata(w http.ResponseWriter, r *http.Request) {
	if h.pricingStore == nil {
		writeError(w, 503, "model_metadata_unavailable", "model metadata is unavailable")
		return
	}
	q := r.URL.Query()
	model := q.Get("model")
	if len(q) != 1 || len(q["model"]) != 1 || model == "" || len(model) > 256 {
		writeError(w, 400, "invalid_query", "model is required")
		return
	}
	values, err := h.officialModels(r.Context(), []string{model}, nil)
	value, ok := values[model]
	if err != nil {
		h.writeStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, 404, "model_metadata_not_found", "LLM Metadata lists no official model for this model")
		return
	}
	writeJSON(w, 200, value)
}

// resolveModelMetadata looks up a batch with one snapshot of the price
// catalog, restricted to providers the selected token can reach.
func (h *Handler) resolveModelMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, 405, "method_not_allowed", "only POST is allowed")
		return
	}
	var input struct {
		TokenID contract.AccessTokenID `json:"token_id"`
		Models  []string               `json:"models"`
	}
	if !requireMediaType(w, r) || !decodeControlJSON(w, r, &input) {
		return
	}
	if input.TokenID == "" || len(input.Models) == 0 || len(input.Models) > 500 {
		writeError(w, 400, "invalid_input", "a token and 1 to 500 models are required")
		return
	}
	for _, model := range input.Models {
		if model == "" || utf8.RuneCountInString(model) > 256 || strings.TrimSpace(model) != model || strings.IndexFunc(model, unicode.IsControl) >= 0 {
			writeError(w, 400, "invalid_input", "invalid model name")
			return
		}
	}
	if h.pricingStore == nil || h.accessTokens == nil {
		writeError(w, 503, "model_metadata_unavailable", "model metadata is unavailable")
		return
	}
	tokens, err := h.accessTokens.List(r.Context())
	if err != nil {
		h.writeStoreError(w, err)
		return
	}
	for _, token := range tokens {
		if token.ID != input.TokenID {
			continue
		}
		values, err := h.officialModels(r.Context(), input.Models, &token.Services)
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, values)
		return
	}
	h.writeStoreError(w, storage.ErrNotFound)
}

func (h *Handler) officialModels(ctx context.Context, models []string, scope *contract.AccessTokenServices) (map[string]modelmeta.Model, error) {
	catalog, err := h.pricingStore.PricingCatalog(ctx)
	if err != nil {
		return nil, err
	}
	var redirects []contract.ModelRedirect
	if h.routingSettings != nil {
		settings, err := h.routingSettings.GetRoutingSettings(ctx)
		if err != nil {
			return nil, err
		}
		redirects = settings.ModelRedirects
	}
	var services []contract.Service
	options := storage.ServiceListOptions{Limit: 200}
	for {
		page, err := h.serviceStore.ListServices(ctx, options)
		if err != nil {
			return nil, err
		}
		for _, record := range page.Items {
			service := record.Service
			if service.Enabled && (scope == nil || scope.Allows(service.ID) && (service.Subscription == nil || service.Subscription.Status == contract.SubscriptionStatusConnected)) {
				services = append(services, service)
			}
		}
		if page.NextCursor == "" || page.NextCursor == options.Cursor {
			break
		}
		options.Cursor = page.NextCursor
	}
	if scope != nil {
		// Prefer an existing Responses entry, then a provider for which the
		// setup dialog can enable a conversion. Ties follow routing order.
		rank := map[contract.ServiceID]int{}
		if store, ok := h.serviceStore.(storage.ServiceOrderStore); ok {
			order, err := store.GetServiceOrder(ctx)
			if err != nil {
				return nil, err
			}
			for i, id := range order.Order.ServiceIDs {
				rank[id] = i + 1
			}
		}
		priority := func(s contract.Service) int {
			value := rank[s.ID]
			if value == 0 {
				value = len(rank) + 1
			}
			for _, row := range s.Capabilities {
				if row.Protocol == contract.ProtocolOpenAIResponses {
					return value
				}
			}
			return value + len(rank) + 1
		}
		slices.SortStableFunc(services, func(a, b contract.Service) int { return cmp.Compare(priority(a), priority(b)) })
	}
	configs := map[contract.ServiceID]pricing.Config{}
	found := map[string]modelmeta.Model{}
	for _, id := range models {
		model := id
		if redirect, ok := contract.ResolveModelRedirect(redirects, id); ok && redirect.To != "" {
			model = redirect.To
		}
		for _, service := range services {
			upstream, served := service.UpstreamModelFor(model)
			if !served {
				continue
			}
			config, ok := configs[service.ID]
			if !ok {
				config, err = h.pricingStore.PricingConfig(ctx, service.ID)
				if err != nil {
					return nil, err
				}
				configs[service.ID] = config
			}
			if entry, ok := config.ResolveModel(upstream, catalog.Models); ok {
				found[id] = entry
				break
			}
			// For the scoped Codex picker, another service's pricing binding
			// cannot describe what the first route serves.
			if scope != nil {
				break
			}
		}
		if scope == nil {
			if _, ok := found[id]; !ok {
				if entry, ok := (pricing.Config{}).ResolveModel(model, catalog.Models); ok {
					found[id] = entry
				}
			}
		}
	}
	return found, nil
}

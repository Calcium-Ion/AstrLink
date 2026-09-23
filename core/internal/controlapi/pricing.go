package controlapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
)

const PricingPath = "/control/v1/pricing"

type PricingStore interface {
	pricing.CatalogStore
	SavePricingConfig(context.Context, contract.ServiceID, pricing.Config) error
	ServiceBilling(context.Context, contract.ServiceID) (pricing.ServiceReport, error)
	BackfillPricing(context.Context, contract.ServiceID) (int, error)
	BillingSummary(context.Context, contract.ServiceID, string, time.Time, time.Time, pricing.BillingSummaryOptions) (pricing.Summary, error)
}

func (h *Handler) pricingResource(w http.ResponseWriter, r *http.Request) {
	if h.pricingStore == nil || h.pricingManager == nil {
		writeError(w, 503, "pricing_unavailable", "pricing is unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, PricingPath+"/")
	allow := func(method string) bool {
		if r.Method == method {
			return true
		}
		w.Header().Set("Allow", method)
		writeError(w, 405, "method_not_allowed", "method not allowed")
		return false
	}
	if path == "status" {
		if !allow("GET") {
			return
		}
		value, err := h.pricingManager.Status(r.Context())
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, value)
		return
	}
	if path == "catalog" {
		if !allow("GET") {
			return
		}
		value, err := h.pricingStore.PricingCatalog(r.Context())
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, value)
		return
	}
	if path == "sync" {
		if !allow("POST") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		value, err := h.pricingManager.Sync(ctx)
		if err != nil {
			writeError(w, 502, "pricing_sync_failed", err.Error())
			return
		}
		writeJSON(w, 200, value)
		return
	}
	if path == "summary" {
		if !allow("GET") {
			return
		}
		q := r.URL.Query()
		from, e1 := time.Parse(time.RFC3339Nano, q.Get("from"))
		to, e2 := time.Parse(time.RFC3339Nano, q.Get("to"))
		if len(q) != 2 || len(q["from"]) != 1 || len(q["to"]) != 1 || e1 != nil || e2 != nil {
			writeError(w, 400, "invalid_query", "from and to are required")
			return
		}
		value, err := h.pricingStore.BillingSummary(r.Context(), "", "", from, to, pricing.BillingSummaryOptions{IncludeTokenBreakdown: true})
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, value)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "services" {
		writeError(w, 404, "not_found", "pricing path not found")
		return
	}
	id := contract.ServiceID(parts[1])
	if id.Validate() != nil {
		writeError(w, 400, "invalid_service_id", "invalid service id")
		return
	}
	if len(parts) == 3 {
		if parts[2] != "backfill" {
			writeError(w, 404, "not_found", "pricing path not found")
			return
		}
		if !allow("POST") {
			return
		}
		n, err := h.pricingStore.BackfillPricing(r.Context(), id)
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]int{"processed": n})
		return
	}
	switch r.Method {
	case "GET":
		value, err := h.pricingStore.ServiceBilling(r.Context(), id)
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, value)
	case "PUT":
		var c pricing.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&c)
		if err != nil || decoder.Decode(new(any)) != io.EOF || c.Validate() != nil {
			writeError(w, 400, "invalid_pricing_config", "invalid official provider, bindings, budget or billing cycle")
			return
		}
		if err = h.pricingStore.SavePricingConfig(r.Context(), id, c); err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, c)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "only GET and PUT are allowed")
	}
}

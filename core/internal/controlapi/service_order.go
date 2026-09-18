package controlapi

import (
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"net/http"
)

const ServiceOrderPath = "/control/v1/service-order"

func (handler *Handler) serviceOrderResource(w http.ResponseWriter, r *http.Request) {
	store, ok := handler.serviceStore.(storage.ServiceOrderStore)
	if !ok {
		writeError(w, 503, "service_order_unavailable", "service order is unavailable")
		return
	}
	var record storage.ServiceOrderRecord
	var err error
	switch r.Method {
	case http.MethodGet:
		record, err = store.GetServiceOrder(r.Context())
	case http.MethodPut:
		if !requireMediaType(w, r, "application/json") {
			return
		}
		if r.Header.Get("If-Match") == "" {
			writeError(w, 428, "precondition_required", "If-Match is required")
			return
		}
		var order contract.ServiceOrder
		if !decodeControlJSON(w, r, &order) {
			return
		}
		record, err = store.UpdateServiceOrder(r.Context(), order, r.Header.Get("If-Match"))
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "only GET and PUT are allowed")
		return
	}
	if err != nil {
		handler.writeStoreError(w, err)
		return
	}
	w.Header().Set("ETag", record.ETag)
	writeJSON(w, 200, record.Order)
}

func (handler *Handler) retiredRouting(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "routing_feature_retired", "Legacy routes, call paths and classification are retired; configure service order and routing settings instead")
}

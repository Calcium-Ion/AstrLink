package controlapi

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"net/http"
	"time"
)

type serviceStatisticsStore interface {
	ServiceStatistics(context.Context, contract.ServiceID, time.Time, time.Time) (storage.ServiceStatistics, error)
}

func (h *Handler) serviceStatistics(w http.ResponseWriter, r *http.Request, id contract.ServiceID) {
	if r.Method != "GET" {
		writeMethodNotAllowed(w, "GET")
		return
	}
	store, ok := h.serviceStore.(serviceStatisticsStore)
	if !ok {
		writeError(w, 503, "statistics_unavailable", "统计不可用")
		return
	}
	q := r.URL.Query()
	from, e1 := time.Parse(time.RFC3339, q.Get("from"))
	to, e2 := time.Parse(time.RFC3339, q.Get("to"))
	if len(q) != 2 || len(q["from"]) != 1 || len(q["to"]) != 1 || e1 != nil || e2 != nil {
		writeError(w, 400, "invalid_range", "时间范围无效")
		return
	}
	value, err := store.ServiceStatistics(r.Context(), id, from, to)
	if err != nil {
		writeError(w, 400, "statistics_error", err.Error())
		return
	}
	writeJSON(w, 200, value)
}

package controlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
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
		if errors.Is(err, storage.ErrInvalidArgument) {
			writeError(w, http.StatusBadRequest, "invalid_range", "请选择不超过 31 天的时间范围")
		} else {
			h.writeStoreError(w, err)
		}
		return
	}
	writeJSON(w, 200, value)
}

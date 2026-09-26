package controlapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/intelligence"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const IntelligencePath = "/control/v1/intelligence"

func (h *Handler) intelligenceResource(w http.ResponseWriter, r *http.Request) {
	if h.intelligence == nil {
		writeError(w, 503, "intelligence_unavailable", "智力检测不可用")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, IntelligencePath+"/"), "/")
	decode := func(v any) error {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20))
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			return err
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("请求格式无效")
		}
		return nil
	}
	reply := func(v any, err error) {
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, 404, "not_found", "检测或渠道不存在")
			} else {
				writeError(w, 400, "intelligence_error", err.Error())
			}
			return
		}
		if v == nil {
			v = map[string]bool{"ok": true}
		}
		writeJSON(w, 200, v)
	}
	allow := func(methods string) {
		w.Header().Set("Allow", methods)
		writeError(w, 405, "method_not_allowed", "method not allowed")
	}
	if len(parts) == 1 && parts[0] == "settings" {
		switch r.Method {
		case "GET":
			v, e := h.intelligence.Settings(r.Context())
			reply(v, e)
		case "PUT":
			var v intelligence.Settings
			if e := decode(&v); e != nil {
				reply(nil, e)
				return
			}
			reply(nil, h.intelligence.SaveSettings(r.Context(), v))
		default:
			allow("GET, PUT")
		}
		return
	}
	if len(parts) >= 2 && len(parts) <= 3 && parts[0] == "channels" {
		id := contract.ServiceID(parts[1])
		if e := id.Validate(); e != nil {
			reply(nil, e)
			return
		}
		if len(parts) == 2 {
			switch r.Method {
			case "GET":
				v, e := h.intelligence.Channel(r.Context(), id)
				reply(v, e)
			case "PUT":
				var v intelligence.ChannelConfig
				if e := decode(&v); e != nil {
					reply(nil, e)
					return
				}
				reply(nil, h.intelligence.SaveChannel(r.Context(), id, v))
			default:
				allow("GET, PUT")
			}
			return
		}
		if parts[2] == "runs" {
			switch r.Method {
			case "GET":
				v, e := h.intelligence.History(r.Context(), id)
				reply(v, e)
			case "POST":
				var v intelligence.StartRequest
				if e := decode(&v); e != nil {
					reply(nil, e)
					return
				}
				run, e := h.intelligence.Start(r.Context(), id, v)
				reply(run, e)
			default:
				allow("GET, POST")
			}
			return
		}
	}
	if len(parts) >= 2 && len(parts) <= 3 && parts[0] == "runs" {
		if !intelligence.ValidRunID(parts[1]) {
			writeError(w, 400, "invalid_id", "无效的检测 ID")
			return
		}
		if len(parts) == 2 {
			if r.Method != "GET" {
				allow("GET")
				return
			}
			v, e := h.intelligence.Get(r.Context(), parts[1])
			reply(v, e)
			return
		}
		if parts[2] == "cancel" || parts[2] == "render" {
			if r.Method != "POST" {
				allow("POST")
				return
			}
			if parts[2] == "cancel" {
				reply(nil, h.intelligence.Cancel(parts[1]))
				return
			}
			var v intelligence.RenderInput
			if e := decode(&v); e != nil {
				reply(nil, e)
				return
			}
			reply(nil, h.intelligence.Render(parts[1], v))
			return
		}
	}
	writeError(w, 404, "not_found", "intelligence path not found")
}

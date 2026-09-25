package controlapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestRoutingGraphControlRequiresCompleteVersionedInput(t *testing.T) {
	_, handler := newRouteHandler(t)
	get := controlRequest(t, handler, http.MethodGet, RoutingGraphPath, "", "", "")
	if get.Code != 200 {
		t.Fatalf("read graph: %d %s", get.Code, get.Body.String())
	}
	var initial contract.RoutingGraphDocument
	if err := json.Unmarshal(get.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	valid := `{"graph":{"nodes":[{"id":"entry_public","kind":"entry","model":"public","enabled":true},{"id":"end_here","kind":"stop","enabled":true}],"edges":[{"id":"edge_first","source":"entry_public","port":"next","target":"end_here"}]},"layout":{},"apply":true}`
	for _, bad := range []string{`{}`, `{"graph":{"nodes":[],"edges":[]},"apply":true}`, strings.Replace(valid, `"kind":"entry"`, `"kind":"entry","unknown_execution_flag":true`, 1)} {
		response := controlRequest(t, handler, http.MethodPut, RoutingGraphPath, "application/json", bad, initial.ETag)
		if response.Code < 400 {
			t.Fatalf("invalid input applied: %s", response.Body.String())
		}
	}
	missingTag := controlRequest(t, handler, http.MethodPut, RoutingGraphPath, "application/json", valid, "")
	if missingTag.Code != 428 {
		t.Fatalf("missing If-Match: %d", missingTag.Code)
	}
	applied := controlRequest(t, handler, http.MethodPut, RoutingGraphPath, "application/json", valid, initial.ETag)
	if applied.Code != 200 {
		t.Fatalf("apply: %d %s", applied.Code, applied.Body.String())
	}
	stale := controlRequest(t, handler, http.MethodPut, RoutingGraphPath, "application/json", valid, initial.ETag)
	if stale.Code != 412 {
		t.Fatalf("stale revision applied: %d", stale.Code)
	}
	history := controlRequest(t, handler, http.MethodGet, RoutingGraphPath+"?revision=1", "", "", "")
	if history.Code != 200 || !strings.Contains(history.Body.String(), `"model":"public"`) {
		t.Fatalf("history: %d %s", history.Code, history.Body.String())
	}
	preview := `{"graph":{"nodes":[{"id":"entry_public","kind":"entry","model":"public","enabled":true},{"id":"end_here","kind":"stop","enabled":true}],"edges":[{"id":"edge_first","source":"entry_public","port":"next","target":"end_here"}]},"entry_id":"entry_public","facts":{"protocol":"openai.chat","streaming":false},"outcomes":{}}`
	result := controlRequest(t, handler, http.MethodPost, RoutingGraphPath+"/preview", "application/json", preview, "")
	if result.Code != 200 || !strings.Contains(result.Body.String(), `"stop_reason":"explicit_stop"`) || !strings.Contains(result.Body.String(), `"attempts":0`) {
		t.Fatalf("preview: %d %s", result.Code, result.Body.String())
	}
}

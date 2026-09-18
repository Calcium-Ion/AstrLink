package controlapi

import (
	"context"
	"fmt"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"net/http"
	"testing"
)

func TestServiceOrderControlAndPagination(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	for i := 0; i < 205; i++ {
		id := contract.ServiceID(fmt.Sprintf("service_%03d", i))
		service := contract.Service{ID: id, Name: string(id), Kind: contract.ServiceKindOpenAI, Enabled: i%2 == 0, Models: []string{"test-model"}, HTTP: &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}}}
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	response := controlRequest(t, handler, http.MethodGet, ServiceOrderPath, "", "", "")
	var order contract.ServiceOrder
	decode(t, response, &order)
	if response.Code != 200 || len(order.ServiceIDs) != 205 || response.Header().Get("ETag") == "" {
		t.Fatalf("order: %d %d", response.Code, len(order.ServiceIDs))
	}
	tag := response.Header().Get("ETag")
	for _, tc := range []struct {
		body, tag string
		code      int
	}{{`{"service_ids":[]}`, "", 428}, {`{"service_ids":[]}`, tag, 422}, {`{"service_ids":null}`, tag, 422}, {`{"service_ids":[],"unknown":true}`, tag, 400}, {`{"service_ids":[]}`, `"stale"`, 412}} {
		result := controlRequest(t, handler, http.MethodPut, ServiceOrderPath, "application/json", tc.body, tc.tag)
		if result.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.body, result.Code, result.Body.String())
		}
	}
	// Legacy ID pagination remains stable independently of priority order.
	page, err := store.ListServices(ctx, storage.ServiceListOptions{Limit: 200})
	if err != nil || len(page.Items) != 200 || page.NextCursor == "" {
		t.Fatalf("first page: %d %v", len(page.Items), err)
	}
	last, err := store.ListServices(ctx, storage.ServiceListOptions{Limit: 200, Cursor: page.NextCursor})
	if err != nil || len(last.Items) != 5 || last.NextCursor != "" {
		t.Fatalf("last page: %d %v", len(last.Items), err)
	}
}

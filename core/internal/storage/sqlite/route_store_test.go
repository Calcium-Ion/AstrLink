package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestListRoutesReturnsStrictStableSnapshot(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	routes := []contract.Route{
		sqliteTestRoute("route_z", true),
		sqliteTestRoute("route_a", false),
	}
	for _, route := range routes {
		document, err := json.Marshal(route)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(
			ctx,
			`INSERT INTO routes (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			route.ID,
			document,
			"now",
			"now",
		); err != nil {
			t.Fatal(err)
		}
	}

	records, err := store.ListRoutes(ctx)
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("route count = %d, want 2", len(records))
	}
	got := []contract.Route{records[0].Route, records[1].Route}
	want := []contract.Route{routes[1], routes[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %#v, want %#v", got, want)
	}
	for _, record := range records {
		if !strings.HasPrefix(record.ETag, `"sha256:`) {
			t.Errorf("route %q ETag = %q", record.Route.ID, record.ETag)
		}
	}
}

func TestListRoutesFailsClosedOnInvalidPersistedDocument(t *testing.T) {
	validRoute := sqliteTestRoute("route_document", true)
	validDocument, err := json.Marshal(validRoute)
	if err != nil {
		t.Fatal(err)
	}
	relayRoute := validRoute
	relayRoute.ID = "route_relay"
	relayRoute.Targets[0].PlanType = contract.PlanTypeRelayKit
	relayRoute.Targets[0].UpstreamProtocol = contract.ProtocolOpenAIChat
	relayDocument, err := json.Marshal(relayRoute)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		rowID    string
		document string
	}{
		{
			name:     "unknown field",
			rowID:    "route_document",
			document: strings.TrimSuffix(string(validDocument), "}") + `,"unexpected":true}`,
		},
		{
			name:     "row ID mismatch",
			rowID:    "route_other",
			document: string(validDocument),
		},
		{
			name:     "multiple JSON values",
			rowID:    "route_document",
			document: string(validDocument) + `{}`,
		},
		{
			name:     "RelayKit unavailable in Alpha",
			rowID:    "route_relay",
			document: string(relayDocument),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
			defer store.Close()
			if _, err := store.db.Exec(
				`INSERT INTO routes (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`,
				test.rowID,
				test.document,
				"now",
				"now",
			); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ListRoutes(context.Background()); !errors.Is(err, storagecontract.ErrInvalidRecord) {
				t.Fatalf("ListRoutes error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func sqliteTestRoute(id contract.RouteID, enabled bool) contract.Route {
	return contract.Route{
		ID:       id,
		Name:     string(id),
		Enabled:  enabled,
		Priority: 5,
		Match: contract.RouteMatch{
			Protocol: contract.ProtocolOpenAIResponses,
		},
		Targets: []contract.RouteTarget{{
			EndpointID:       "endpoint_01",
			PlanType:         contract.PlanTypeNative,
			UpstreamProtocol: contract.ProtocolOpenAIResponses,
			Priority:         10,
		}},
	}
}

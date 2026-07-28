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

func TestRouteCRUDValidatesReferencesETagsAndEvaluationOrder(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	for _, id := range []contract.ServiceID{"endpoint_01", "endpoint_02"} {
		if _, err := store.CreateEndpoint(
			ctx,
			testEndpoint(id),
			storagecontract.CredentialMutation{},
		); err != nil {
			t.Fatal(err)
		}
	}

	late := sqliteTestRoute("route_late", true)
	late.Priority = 20
	first := sqliteTestRoute("route_first", true)
	first.Priority = 10
	first.Match.Model = "public-model"
	first.Targets[0].UpstreamModel = "upstream-model"
	first.Targets[0].ServiceID = "endpoint_02"
	disabled := sqliteTestRoute("route_disabled", false)
	disabled.Priority = 0
	for _, route := range []contract.Route{late, first, disabled} {
		if _, err := store.CreateRoute(ctx, route); err != nil {
			t.Fatalf("CreateRoute(%s): %v", route.ID, err)
		}
	}

	enabled := true
	page, err := store.ListRoutePage(
		ctx,
		storagecontract.RouteListOptions{Limit: 1, Enabled: &enabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Route.ID != first.ID || page.NextCursor == "" {
		t.Fatalf("first page=%#v", page)
	}
	page, err = store.ListRoutePage(
		ctx,
		storagecontract.RouteListOptions{
			Limit:   1,
			Cursor:  page.NextCursor,
			Enabled: &enabled,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Route.ID != late.ID || page.NextCursor != "" {
		t.Fatalf("second page=%#v", page)
	}

	record, err := store.GetRoute(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated := record.Route
	updated.Name = "updated"
	if _, err := store.UpdateRoute(ctx, updated, `"stale"`); !errors.Is(err, storagecontract.ErrPrecondition) {
		t.Fatalf("stale UpdateRoute error=%v", err)
	}
	updatedRecord, err := store.UpdateRoute(ctx, updated, record.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if updatedRecord.Route.Name != "updated" || updatedRecord.ETag == record.ETag {
		t.Fatalf("updated record=%#v", updatedRecord)
	}
	if err := store.DeleteRoute(ctx, first.ID, record.ETag); !errors.Is(err, storagecontract.ErrPrecondition) {
		t.Fatalf("stale DeleteRoute error=%v", err)
	}
	if err := store.DeleteRoute(ctx, first.ID, updatedRecord.ETag); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRoute(ctx, first.ID); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("deleted GetRoute error=%v", err)
	}
}

func TestRouteCreateRejectsMissingOrIncompatibleEndpoint(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateEndpoint(
		ctx,
		testEndpoint("endpoint_01"),
		storagecontract.CredentialMutation{},
	); err != nil {
		t.Fatal(err)
	}

	missing := sqliteTestRoute("route_missing", true)
	missing.Targets[0].ServiceID = "endpoint_missing"
	if _, err := store.CreateRoute(ctx, missing); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("missing endpoint CreateRoute error=%v", err)
	}
	incompatible := sqliteTestRoute("route_incompatible", true)
	incompatible.Targets[0].PlanType = contract.PlanTypeDelegated
	if _, err := store.CreateRoute(ctx, incompatible); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("incompatible endpoint CreateRoute error=%v", err)
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
			ServiceID:        "endpoint_01",
			PlanType:         contract.PlanTypeNative,
			UpstreamProtocol: contract.ProtocolOpenAIResponses,
			Priority:         10,
		}},
	}
}

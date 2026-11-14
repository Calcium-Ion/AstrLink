package metadata

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func newRegistry(t *testing.T) (*Registry, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewRegistry(store), store
}

func TestResolvePrefersHeadersThenSessionsThenTokens(t *testing.T) {
	registry, store := newRegistry(t)
	ctx := context.Background()
	if err := store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Owner: contract.MetadataOwnerUser}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterPlugin(ctx, "powercontext", []contract.MetadataAttribute{{Key: "project", Label: "项目", Pattern: "^[a-z0-9][a-z0-9._-]{0,63}$", Marker: "memory-project"}}); err != nil {
		t.Fatal(err)
	}
	token := contract.AccessTokenID("token_laptop")
	if err := store.ReplaceTokenMetadata(ctx, token, map[string]string{"env": "dev", "project": "astrlink"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	session := map[string]storage.MetadataValueRecord{"project": {Key: "project", Value: "paseo", SetBy: contract.MetadataSetByModelMarker, Turn: 2}}

	header := http.Header{}
	header.Set("X-AstrLink-Meta-Env", "prod")
	header.Set("x-astrlink-meta-project", "Not Valid!")
	metadata := registry.Resolve(header, token, session)
	if got := metadata.Values["env"]; got != (contract.MetadataValue{Value: "prod", Source: contract.MetadataSourceHeader}) {
		t.Fatalf("env = %+v", got)
	}
	if got := metadata.Values["project"]; got != (contract.MetadataValue{Value: "paseo", Source: contract.MetadataSourceSession, SetBy: contract.MetadataSetByModelMarker, Turn: 2}) {
		t.Fatalf("project = %+v", got)
	}
	if len(metadata.Events) != 1 || metadata.Events[0] != (contract.MetadataEvent{Key: "project", Event: contract.MetadataEventIgnored, Value: "Not Valid!", SetBy: contract.MetadataSourceHeader, Reason: contract.MetadataIgnoredInvalidValue}) {
		t.Fatalf("events = %+v", metadata.Events)
	}
	if err := metadata.Validate(); err != nil {
		t.Fatalf("the resolved metadata does not validate: %v", err)
	}

	metadata = registry.Resolve(http.Header{}, token, nil)
	if metadata.Values["project"].Source != contract.MetadataSourceToken || metadata.Values["project"].Value != "astrlink" || metadata.Values["env"].Value != "dev" {
		t.Fatalf("token defaults = %+v", metadata.Values)
	}
	none := http.Header{}
	none.Set("X-AstrLink-Meta-Project", "none")
	if metadata = registry.Resolve(none, "", nil); metadata.Values["project"].Value != contract.MetadataNone || len(metadata.Values) != 1 {
		t.Fatalf("none = %+v", metadata.Values)
	}
	if metadata = registry.Resolve(http.Header{}, "", nil); !metadata.Empty() {
		t.Fatalf("a request without sources = %+v", metadata)
	}
}

func TestValidIsTheOneCheckForEveryValue(t *testing.T) {
	registry, store := newRegistry(t)
	if err := store.CreateMetadataAttribute(context.Background(), contract.MetadataAttribute{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Owner: contract.MetadataOwnerUser}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]bool{"dev": true, "none": true, "staging": false, "": false, "dev\n": false, "missing-key": false} {
		key := "env"
		if value == "missing-key" {
			key = "missing"
		}
		if got := registry.Valid(key, value); got != want {
			t.Fatalf("Valid(%s, %q) = %v", key, value, got)
		}
	}
	if header := contract.MetadataHeader("build_id"); header != "X-Astrlink-Meta-Build-Id" {
		t.Fatalf("MetadataHeader = %q", header)
	}
}

package clientconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

type catalogGateway struct {
	Gateway
	origin   string
	mu       sync.Mutex
	requests [][]string
}

func (g *catalogGateway) Session() (string, string, bool) { return g.origin, "test session", true }
func (g *catalogGateway) RevealAccessToken(_ context.Context, id string) (string, error) {
	return "test-" + id, nil
}
func (g *catalogGateway) ModelMetadataForModels(_ context.Context, id string, models []string) (map[string]json.RawMessage, error) {
	g.mu.Lock()
	g.requests = append(g.requests, slices.Clone(models))
	g.mu.Unlock()
	level := "high"
	if id == "two" {
		level = "medium"
	}
	return map[string]json.RawMessage{"official": json.RawMessage(`{"reasoning":true,"reasoning_levels":["` + level + `"]}`), "missing-field": json.RawMessage(`{"reasoning":true,"reasoning_levels":["low"]}`)}, nil
}

func TestCodexCatalogScopesNativeAndBulkOfficialChoices(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/models" || r.Header.Get(contract.CodexUpstreamCatalogHeader) != "1" || r.URL.Query().Get("client_version") == "" {
			t.Error("invalid local discovery request")
		}
		if r.Header.Get("Authorization") == "Bearer test-two" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-one" {
			t.Error("wrong test credential")
		}
		_, _ = w.Write([]byte(`{"native":{"slug":"original","default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"high"}]},"empty":{"slug":"empty","supported_reasoning_levels":[]},"missing-field":{"slug":"missing-field"}}`))
	}))
	defer srv.Close()
	g := &catalogGateway{origin: srv.URL}
	cache := CodexCatalogCache{}
	ids := []string{"native", "empty", "official", "missing-field", "unknown"}
	lookup := func(token string) Catalog {
		t.Helper()
		details, err := cache.Load(t.Context(), g, token, srv.URL, ids)
		if err != nil {
			t.Fatal(err)
		}
		return details
	}
	first := lookup("one")
	if !slices.Equal(first["native"].CodexReasoning().Levels, []string{"high"}) || first["empty"].CodexReasoning().Levels == nil || len(first["empty"].CodexReasoning().Levels) != 0 || first["unknown"].CodexReasoning().Default != nil || !slices.Equal(first["missing-field"].CodexReasoning().Levels, []string{"low"}) {
		t.Fatalf("wrong choices: %+v", first)
	}
	if r := first["empty"].CodexReasoning().Reasoning; r == nil || *r {
		t.Fatalf("an empty native declaration must read as not adjustable: %+v", first["empty"].CodexReasoning())
	}
	if r := first["native"].CodexReasoning().Reasoning; r == nil || !*r {
		t.Fatalf("a native declaration with levels must read as adjustable: %+v", first["native"].CodexReasoning())
	}
	if len(g.requests) != 1 || !slices.Equal(g.requests[0], ids[2:]) {
		t.Fatalf("wanted one bulk lookup for missing declarations, got %v", g.requests)
	}
	_ = lookup("one")
	if requests.Load() != 1 || len(g.requests) != 1 {
		t.Fatal("re-fetched same choices during the write")
	}
	other := lookup("two")
	if requests.Load() != 2 || len(other["native"].CodexReasoning().Levels) != 0 || !slices.Equal(other["official"].CodexReasoning().Levels, []string{"medium"}) {
		t.Fatal("capabilities crossed token scopes")
	}
}

type twoFileSpec struct{ Spec }
type plainConfigFile struct{}

func (plainConfigFile) Format() format.Format { return format.JSONFormat{} }
func (twoFileSpec) Files() []File             { return []File{plainConfigFile{}, plainConfigFile{}} }

func TestRestoreRollsBackWhenCompanionChangedDuringCommit(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "config.toml"), filepath.Join(dir, "catalog.json")}
	if err := os.WriteFile(paths[0], []byte("written config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[1], []byte("new user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	err := commitFiles(New(twoFileSpec{}), paths, []*string{Some("written config"), Some("written catalog")}, []*string{nil, nil}, false)
	if err == nil {
		t.Fatal("stale restore succeeded")
	}
	for i, want := range []string{"written config", "new user edit"} {
		got, err := os.ReadFile(paths[i])
		if err != nil || string(got) != want {
			t.Fatalf("file %d = %q (%v)", i, got, err)
		}
	}
}

func TestCodexCatalogConcurrentLoadsShareOneDiscoveryRead(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-release
		_, _ = w.Write([]byte(`{"native":{"slug":"native","supported_reasoning_levels":[{"effort":"high"}]}}`))
	}))
	defer srv.Close()
	g := &catalogGateway{origin: srv.URL}
	cache := CodexCatalogCache{}
	const callers = 8
	errs := make(chan error, callers)
	for range callers {
		go func() {
			details, err := cache.Load(t.Context(), g, "one", srv.URL, []string{"native"})
			if err == nil && !slices.Equal(details["native"].CodexReasoning().Levels, []string{"high"}) {
				err = errors.New("wrong levels")
			}
			errs <- err
		}()
	}
	// A caller blocked behind the read can still give up.
	cancelled, cancel := context.WithCancel(t.Context())
	cancelledDone := make(chan error, 1)
	go func() {
		_, err := cache.Load(cancelled, g, "one", srv.URL, []string{"native"})
		cancelledDone <- err
	}()
	cancel()
	select {
	case err := <-cancelledDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled caller: %v", err)
		}
	case err := <-errs:
		t.Fatalf("a caller finished before the discovery read was released: %v", err)
	}
	close(release)
	for range callers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("discovery read %d times, want once", n)
	}
}

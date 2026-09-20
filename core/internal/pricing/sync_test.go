package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type memoryCatalog struct{ c Catalog }

func (s *memoryCatalog) PricingCatalog(context.Context) (Catalog, error)       { return s.c, nil }
func (s *memoryCatalog) SavePricingCatalog(_ context.Context, c Catalog) error { s.c = c; return nil }
func TestSyncOnlyOfficialCatalogsAtomicAndConditional(t *testing.T) {
	broken := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/manifest.json":
			if r.Header.Get("If-None-Match") == "v1" && !broken {
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", "v1")
			json.NewEncoder(w).Encode(map[string]any{"version": 1, "generatedAt": time.Now().UTC().Truncate(24 * time.Hour), "warnings": []string{}})
		case "/all.json":
			ioJSON := `{"moonshotai":{"models":{"kimi-k2":{"name":"Kimi"}}},"moonshotai-coding-plan":{"models":{}},"zai-coding-plan":{"models":{}}}`
			w.Write([]byte(ioJSON))
		case "/newapi/providers/moonshotai/ratio_config-v1-base.json":
			if broken {
				w.WriteHeader(500)
				return
			}
			w.Write([]byte(`{"success":true,"data":{"billing_mode":{"kimi-k2":"tiered_expr"},"billing_expr":{"kimi-k2":"tier(\"standard\",p * 1 + c * 4)"}}}`))
		default:
			t.Errorf("unexpected source path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	store := &memoryCatalog{}
	m := NewManager(store, server.Client())
	m.base = server.URL
	status, err := m.Sync(context.Background())
	if err != nil || status.Models != 1 {
		t.Fatalf("%+v %v", status, err)
	}
	version := store.c.Version
	before := calls
	if _, err = m.Sync(context.Background()); err != nil || calls != before+1 {
		t.Fatalf("conditional sync %v %d", err, calls)
	}
	broken = true
	if _, err = m.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected fetch failure %v", err)
	}
	if store.c.Version != version {
		t.Fatal("failed sync replaced catalog")
	}
}

func TestLiveOfficialCatalog(t *testing.T) {
	if os.Getenv("ASTRLINK_TEST_LIVE_PRICES") != "1" {
		t.Skip("opt-in public catalog smoke test")
	}
	store := &memoryCatalog{}
	manager := NewManager(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := manager.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, price := range store.c.Prices {
		if strings.Contains(price.Provider, "plan") {
			t.Fatal(price.Provider)
		}
		found[price.Provider] = true
	}
	for _, provider := range []string{"openai", "anthropic", "moonshotai", "zai", "minimax"} {
		if !found[provider] {
			t.Fatalf("missing %s", provider)
		}
	}
	t.Logf("official models=%d source warnings=%v", status.Models, status.Warnings)
}

func TestConcurrentSyncIsNotReportedAsSuccess(t *testing.T) {
	m := NewManager(&memoryCatalog{}, nil)
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if _, err := m.Sync(context.Background()); err == nil {
		t.Fatal("concurrent sync reported success")
	}
}

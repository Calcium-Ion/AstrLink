package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestGitHubRepositoryReadsOnlyRepositoryAddresses(t *testing.T) {
	for address, want := range map[string]string{
		"https://github.com/oceanbase/powercontext":      "oceanbase/powercontext",
		"https://www.github.com/oceanbase/powercontext/": "oceanbase/powercontext",
		"https://github.com/oceanbase/powercontext.git":  "oceanbase/powercontext",
		"https://github.com/oceanbase":                   "",
		"https://github.com/oceanbase/powercontext/tree": "",
		"http://github.com/oceanbase/powercontext":       "",
		"https://gitlab.com/oceanbase/powercontext":      "",
		"https://github.com/ocean%20base/powercontext":   "",
		"https://github.com/oceanbase/..":                "",
	} {
		if got := githubRepository(address); got != want {
			t.Errorf("githubRepository(%q) = %q, want %q", address, got, want)
		}
	}
}

const creditedPlugin = `export const meta = {
  apiVersion: 1, key: "credited", name: "Credited", version: "1.0.0", capabilities: [],
  credits: [
    { name: "Memo", url: "https://github.com/example/memo" },
    { name: "Gone", url: "https://github.com/example/gone" },
    { name: "Elsewhere", url: "https://example.com/elsewhere" },
  ],
};
`

func TestPluginCreditsShowGitHubStars(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	count := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		copied := map[string]int{}
		for path, times := range asked {
			copied[path] = times
		}
		return copied
	}
	github := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		asked[request.URL.Path]++
		mu.Unlock()
		if request.Header.Get("User-Agent") == "" {
			http.Error(writer, "no user agent", http.StatusForbidden)
			return
		}
		if request.URL.Path != "/repos/example/memo" {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte(`{"full_name":"example/memo","stargazers_count":1367}`))
	}))
	defer github.Close()

	_, handler, manager, directory := newPluginHandler(t)
	writePlugin(t, directory, "credited", "plugin.js", creditedPlugin)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	handler.repoStars = newRepoStars(github.URL)
	handler.repoStars.now = func() time.Time { return clock }
	read := func() map[string]int {
		t.Helper()
		response := controlRequest(t, handler, http.MethodGet, PluginsPath+"/credited/credits", "", "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET credits = %d %s", response.Code, response.Body.String())
		}
		return decodeBody[contract.PluginCreditStars](t, response).Stars
	}

	// Only the repository GitHub answered for has stars.
	if stars := read(); len(stars) != 1 || stars["https://github.com/example/memo"] != 1367 {
		t.Fatalf("stars = %v", stars)
	}
	// The answers stand: neither repository is asked for again soon after.
	clock = clock.Add(5 * time.Minute)
	read()
	if asked := count(); asked["/repos/example/memo"] != 1 || asked["/repos/example/gone"] != 1 || len(asked) != 2 {
		t.Fatalf("asked = %v, want each repository once", asked)
	}
	// A repository GitHub did not answer for is asked again after a while,
	// and one it answered for after hours.
	clock = clock.Add(starsRetry)
	read()
	if asked := count(); asked["/repos/example/memo"] != 1 || asked["/repos/example/gone"] != 2 {
		t.Fatalf("asked = %v, want only the unanswered one again", asked)
	}
	clock = clock.Add(starsFresh)
	read()
	if asked := count(); asked["/repos/example/memo"] != 2 {
		t.Fatalf("asked = %v, want the stars asked for again", asked)
	}
}

func TestPluginStarsStayWhileGitHubIsUnreachable(t *testing.T) {
	var failing atomic.Bool
	github := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if failing.Load() {
			http.Error(writer, "rate limited", http.StatusForbidden)
			return
		}
		_, _ = writer.Write([]byte(`{"stargazers_count":12}`))
	}))
	defer github.Close()
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	cache := newRepoStars(github.URL)
	cache.now = func() time.Time { return clock }
	if stars, ok := cache.stars(context.Background(), "example/memo"); !ok || stars != 12 {
		t.Fatalf("stars = %d, %v", stars, ok)
	}
	failing.Store(true)
	clock = clock.Add(starsFresh)
	if stars, ok := cache.stars(context.Background(), "example/memo"); !ok || stars != 12 {
		t.Fatalf("stars after a failed refresh = %d, %v; want the last count", stars, ok)
	}
}

func TestPluginListShowsTheIcon(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	png := "\x89PNG\r\n\x1a\nimage"
	if err := os.WriteFile(filepath.Join(directory, "demo", "icon.png"), []byte(png), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := controlRequest(t, handler, http.MethodGet, PluginsPath, "", "", "")
	list := decodeBody[struct {
		Plugins []contract.PluginSummary `json:"plugins"`
	}](t, response)
	for _, summary := range list.Plugins {
		hasIcon := strings.HasPrefix(summary.Icon, "data:image/png;base64,")
		if hasIcon != (summary.Key == "demo") {
			t.Fatalf("%s icon = %.40q", summary.Key, summary.Icon)
		}
	}
}

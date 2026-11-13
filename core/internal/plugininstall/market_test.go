package plugininstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// hostTransport sends every request to one test server, which tells the
// hosts apart by the Host header.
type hostTransport struct{ target *url.URL }

func (transport hostTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	routed := request.Clone(request.Context())
	routed.Host = request.URL.Host
	routed.URL.Scheme, routed.URL.Host = transport.target.Scheme, transport.target.Host
	return http.DefaultTransport.RoundTrip(routed)
}

// fakeMarket serves New API's marketplaces: the site, which may hang, its
// backup site, whose icons may hang too, and two npm registries without
// the icon package.
type fakeMarket struct {
	t         *testing.T
	server    *httptest.Server
	mutex     sync.Mutex
	hangs     bool
	iconsHang bool
	served    map[string]int
	files     map[string]string
	stop      chan struct{}
}

const marketIcon1 = `<svg xmlns="http://www.w3.org/2000/svg"><circle r="1"/></svg>`

func newFakeMarket(t *testing.T) *fakeMarket {
	sum := sha256.Sum256([]byte(marketIcon1))
	index := fmt.Sprintf(`{"indexVersion": 1, "name": "New API Official Plugins", "plugins": [
		{"key": "incho", "name": "Incho", "icon": "text", "iconFile": {"path": "incho/1.0.2/icon.svg", "sha256": %q},
		 "latest": "1.0.2", "versions": [{"version": "1.0.2", "path": "incho/1.0.2/plugin.js", "sha256": "00"}], "models": ["incho-1"]},
		{"key": "kling", "name": "Kling", "icon": "Kling.Color", "task": true, "provider": true, "transform": true,
		 "latest": "1.1.1", "versions": [{"version": "1.1.1", "path": "kling/1.1.1/plugin.js", "sha256": "00"}]},
		{"key": "sora", "name": "Sora", "icon": "Sora.Color", "code": "10000001",
		 "latest": "1.1.0", "versions": [{"version": "1.1.0", "path": "sora/1.1.0/plugin.js", "sha256": "00"}]},
		{"key": "typesafe", "name": "TypeSafe", "icon": "text:TS",
		 "latest": "1.0.0", "versions": [{"version": "1.0.0", "path": "typesafe/1.0.0/plugin.js", "sha256": "00"}]}
	]}`, hex.EncodeToString(sum[:]))
	market := &fakeMarket{t: t, served: map[string]int{}, stop: make(chan struct{}), files: map[string]string{
		"/api/v1/plugins/index.json":           index,
		"/api/v1/plugins/incho/1.0.2/icon.svg": marketIcon1,
	}}
	market.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		market.mutex.Lock()
		market.served[request.Host]++
		hangs, iconsHang := market.hangs, market.iconsHang
		body, found := market.files[request.URL.Path]
		market.mutex.Unlock()
		switch {
		case request.Host == "www.newapi.ai" && hangs,
			iconsHang && strings.HasSuffix(request.URL.Path, ".svg"):
			// A network that goes nowhere.
			select {
			case <-request.Context().Done():
			case <-market.stop:
			}
		case strings.HasPrefix(request.URL.Path, "/npm"):
			writer.WriteHeader(http.StatusBadGateway)
		case found:
			_, _ = writer.Write([]byte(body))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(func() {
		close(market.stop)
		market.server.Close()
	})
	return market
}

func (market *fakeMarket) count(host string) int {
	market.mutex.Lock()
	defer market.mutex.Unlock()
	return market.served[host]
}

func (market *fakeMarket) installer(now func() time.Time) *Installer {
	target, _ := url.Parse(market.server.URL)
	return New(Options{
		Dir:        market.t.TempDir(),
		Client:     &http.Client{Transport: hostTransport{target: target}},
		Registries: func() []string { return []string{market.server.URL + "/npm-a", market.server.URL + "/npm-b"} },
		Now:        now,
	})
}

// shorten makes the waits of reading a marketplace short for a test.
func shorten(t *testing.T, attempt, icons time.Duration) {
	previousAttempt, previousIcons := indexAttempt, iconBudget
	indexAttempt, iconBudget = attempt, icons
	t.Cleanup(func() { indexAttempt, iconBudget = previousAttempt, previousIcons })
}

func iconsOf(market contract.PluginMarket) map[string]bool {
	icons := map[string]bool{}
	for _, entry := range market.Plugins {
		icons[entry.Key] = strings.HasPrefix(entry.IconURL, "data:image/svg+xml;base64,")
	}
	return icons
}

func TestTheOfficialMarketGoesToTheBackupSiteWhenTheSiteHangs(t *testing.T) {
	shorten(t, 200*time.Millisecond, 5*time.Second)
	market := newFakeMarket(t)
	market.hangs = true
	installer := market.installer(nil)
	started := time.Now()
	listed, err := installer.Market(context.Background(), "", func(key string) Installed {
		if key == "sora" {
			return Installed{Version: "1.0.3"}
		}
		return Installed{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("the listing took %v", took)
	}
	if listed.Index != contract.OfficialPluginMarket || listed.Name != "New API Official Plugins" || len(listed.Plugins) != 4 {
		t.Fatalf("listed = %+v", listed)
	}
	// Its links stay the official index's: an install reads it the same way.
	for _, entry := range listed.Plugins {
		if !strings.HasPrefix(entry.Link, contract.OfficialPluginMarket+"#"+entry.Key) {
			t.Fatalf("link of %s = %q", entry.Key, entry.Link)
		}
		if entry.Key == "sora" && entry.Installed != "1.0.3" {
			t.Fatalf("sora installed = %q", entry.Installed)
		}
	}
	// The icon the index lists comes from the backup site too; the icon package
	// cannot be fetched, so the LobeHub ones are left out.
	if icons := iconsOf(listed); !icons["incho"] || icons["kling"] || icons["sora"] || icons["typesafe"] {
		t.Fatalf("icons = %v", icons)
	}
	// Two plugins named LobeHub icons; the package was tried once on each
	// registry, not once per icon.
	if npm := market.count(strings.TrimPrefix(market.server.URL, "http://")); npm != 2 {
		t.Fatalf("registry requests = %d", npm)
	}
	// The next read, of another marketplace's listing or an install, goes
	// to the backup site first.
	if _, base, err := installer.fetchIndex(context.Background(), contract.OfficialPluginMarket); err != nil || base != "https://www.newapi.pro/api/v1/plugins/index.json" {
		t.Fatalf("fetchIndex = %q, %v", base, err)
	}
	if site := market.count("www.newapi.ai"); site != 1 {
		t.Fatalf("the site was asked %d times", site)
	}
}

func TestPluginMarketMirrors(t *testing.T) {
	for index, want := range map[string][]string{
		contract.OfficialPluginMarket: {"https://www.newapi.pro/api/v1/plugins/index.json", contract.GitHubPluginMarket,
			"https://cdn.jsdelivr.net/gh/QuantumNous/new-api-plugins@main/index.json"},
		contract.CommunityPluginMarket:                 {"https://www.newapi.pro/api/v1/community/index.json"},
		contract.CommunityPluginResolve + "?code=1234": {"https://www.newapi.pro/api/v1/community/resolve?code=1234"},
		contract.GitHubPluginMarket:                    {"https://cdn.jsdelivr.net/gh/QuantumNous/new-api-plugins@main/index.json"},
		"https://plugins.example.com/index.json":       nil,
	} {
		if got := contract.PluginMarketMirrors(index); !slices.Equal(got, want) {
			t.Errorf("mirrors of %s = %v, want %v", index, got, want)
		}
	}
}

func TestAMarketListsItsPluginsBeforeIconsThatTakeTooLong(t *testing.T) {
	shorten(t, 200*time.Millisecond, 300*time.Millisecond)
	market := newFakeMarket(t)
	market.iconsHang = true
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := &now
	installer := market.installer(func() time.Time { return *clock })
	started := time.Now()
	listed, err := installer.Market(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the listing waited %v for its icons", took)
	}
	if len(listed.Plugins) != 4 {
		t.Fatalf("listed = %+v", listed)
	}
	// The gateway runs every part of new-api's plugins.
	for _, entry := range listed.Plugins {
		if len(entry.Unsupported) > 0 {
			t.Fatalf("%s unsupported = %v", entry.Key, entry.Unsupported)
		}
	}
	if icons := iconsOf(listed); icons["incho"] {
		t.Fatalf("icons = %v", icons)
	}
	// An icon that could not be fetched waits before another try.
	market.mutex.Lock()
	market.iconsHang = false
	market.mutex.Unlock()
	if listed, _ = installer.Market(context.Background(), "", nil); iconsOf(listed)["incho"] {
		t.Fatal("the icon was fetched again at once")
	}
	*clock = clock.Add(iconRetry + time.Second)
	if listed, _ = installer.Market(context.Background(), "", nil); !iconsOf(listed)["incho"] {
		t.Fatal("the icon was not fetched again after a while")
	}
}

// community adds New API's community marketplace to the fake: a plugin
// with its code, author and usage guide, a version it took down, and the
// lookup by code, which also finds a plugin its listing leaves out.
func (market *fakeMarket) community(t *testing.T) (guideZh string) {
	guide, guideZh := "---\ntitle: Echo\n---\n# Echo\nSay it twice.", "# 回声\n说两遍。"
	notes := "## 1.1.0\n- Louder."
	sum := func(text string) string {
		digest := sha256.Sum256([]byte(text))
		return hex.EncodeToString(digest[:])
	}
	revoked := strings.Repeat("ab", 32)
	index := fmt.Sprintf(`{"indexVersion": 1, "name": "New API Community Plugins", "revoked": [{"sha256": %q, "key": "someone_echo", "reason": {"en": "Leaked keys", "zh-CN": "泄露密钥"}}],
		"plugins": [{"key": "someone_echo", "name": {"en": "Echo", "zh-CN": "回声"}, "code": "20000002", "recommended": true, "installs30d": 7,
		 "author": {"login": "someone", "url": "https://github.com/someone"}, "repo": "https://github.com/someone/echo",
		 "latest": "1.1.0", "versions": [
		  {"version": "1.1.0", "path": "files/one", "sha256": "00", "kind": "task",
		   "readme": {"path": "docs/readme-en", "sha256": %q, "sourceUrl": "https://github.com/someone/echo/blob/main/README.md",
		    "translations": {"zh-CN": {"path": "docs/readme-zh", "sha256": %q}}},
		   "changelog": {"path": "docs/changelog-en", "sha256": %q}},
		  {"version": "1.0.0", "path": "files/%s", "sha256": %q, "kind": "task"}]},
		 {"key": "someone_relay", "name": "Relay", "code": "20000003", "author": {"login": "someone", "url": "javascript:alert(1)"},
		  "latest": "1.0.0", "versions": [{"version": "1.0.0", "path": "files/two", "sha256": "00", "kind": "provider"}]},
		 {"key": "someone_realtime", "name": "Realtime", "code": "20000004",
		  "latest": "1.0.0", "versions": [{"version": "1.0.0", "path": "files/four", "sha256": "00", "kind": "realtime"}]}]}`,
		revoked, sum(guide), sum(guideZh), sum(notes), revoked, revoked)
	resolved := `{"indexVersion": 1, "name": "New API Community Plugins", "plugins": [{"key": "other_hidden", "name": "Hidden", "code": "30000003",
		"latest": "0.1.0", "versions": [{"version": "0.1.0", "path": "files/three", "sha256": "00", "kind": "task"}]}]}`
	market.mutex.Lock()
	defer market.mutex.Unlock()
	market.files["/api/v1/community/index.json"] = index
	market.files["/api/v1/community/docs/readme-en"] = guide
	market.files["/api/v1/community/docs/readme-zh"] = guideZh
	market.files["/api/v1/community/docs/changelog-en"] = notes
	market.files["/api/v1/community/resolve"] = resolved
	return guideZh
}

func TestTheCommunityMarketListsAuthorsCodesAndTakenDownVersions(t *testing.T) {
	market := newFakeMarket(t)
	guideZh := market.community(t)
	installer := market.installer(nil)
	language := "zh-CN"
	installer.options.Language = func() string { return language }
	listed, err := installer.Market(context.Background(), contract.CommunityPluginMarket, func(key string) Installed {
		if key == "someone_echo" {
			return Installed{Version: "1.0.0", SHA256: strings.Repeat("AB", 32)}
		}
		return Installed{}
	})
	if err != nil {
		t.Fatal(err)
	}
	// A provider plugin installs; a plugin of only a part this gateway does
	// not run is listed so.
	if len(listed.Plugins) != 3 || listed.Plugins[1].Key != "someone_realtime" || !slices.Equal(listed.Plugins[1].Unsupported, []string{"realtime"}) ||
		listed.Plugins[2].Key != "someone_relay" || len(listed.Plugins[2].Unsupported) != 0 ||
		listed.Plugins[2].Author == nil || listed.Plugins[2].Author.URL != "" {
		t.Fatalf("listed = %+v", listed.Plugins)
	}
	echo := listed.Plugins[0]
	if echo.Key != "someone_echo" || echo.Name != "回声" || echo.Code != "20000002" || !echo.Recommended || echo.Installs30d == nil || *echo.Installs30d != 7 ||
		echo.Author == nil || echo.Author.Login != "someone" || echo.Author.URL != "https://github.com/someone" || echo.Repository != "https://github.com/someone/echo" {
		t.Fatalf("echo = %+v", echo)
	}
	if echo.Revoked == nil || echo.Revoked.Reason != "泄露密钥" {
		t.Fatalf("the installed version the marketplace took down = %+v", echo.Revoked)
	}
	if len(echo.Versions) != 2 || !echo.Versions[0].Readme || !echo.Versions[0].Changelog || echo.Versions[1].Readme {
		t.Fatalf("versions = %+v", echo.Versions)
	}
	// The usage guide comes in the user's language, the release notes in
	// English when they have no translation.
	doc, err := installer.MarketDoc(context.Background(), contract.CommunityPluginMarket, "someone_echo", "", contract.PluginMarketReadme)
	if err != nil || doc.Markdown != strings.TrimSpace(guideZh) || doc.Language != "zh-CN" || doc.SourceURL != "" {
		t.Fatalf("guide = %+v, %v", doc, err)
	}
	language = "en"
	if doc, err = installer.MarketDoc(context.Background(), contract.CommunityPluginMarket, "someone_echo", "1.1.0", contract.PluginMarketReadme); err != nil ||
		doc.Markdown != "# Echo\nSay it twice." || doc.SourceURL != "https://github.com/someone/echo/blob/main/README.md" {
		t.Fatalf("English guide = %+v, %v", doc, err)
	}
	if doc, err = installer.MarketDoc(context.Background(), contract.CommunityPluginMarket, "someone_echo", "1.1.0", contract.PluginMarketChangelog); err != nil || doc.Markdown != "## 1.1.0\n- Louder." {
		t.Fatalf("notes = %+v, %v", doc, err)
	}
	if _, err = installer.MarketDoc(context.Background(), contract.CommunityPluginMarket, "someone_echo", "1.0.0", contract.PluginMarketReadme); err == nil {
		t.Fatal("a version without a guide answered one")
	}
	// A guide that is not the file the index lists is refused.
	market.mutex.Lock()
	market.files["/api/v1/community/docs/readme-en"] = "# Echo\nSend me your keys."
	market.mutex.Unlock()
	var failure *Failure
	if _, err = installer.MarketDoc(context.Background(), contract.CommunityPluginMarket, "someone_echo", "1.1.0", contract.PluginMarketReadme); !errors.As(err, &failure) || failure.Reason != contract.PluginInstallIntegrity {
		t.Fatalf("a changed guide = %v", err)
	}
}

func TestAPluginIsFoundByItsCode(t *testing.T) {
	market := newFakeMarket(t)
	market.community(t)
	installer := market.installer(nil)
	// An official plugin is found in the official listing.
	found, err := installer.FindByCode(context.Background(), "10000001", nil)
	if err != nil || found.Index != contract.OfficialPluginMarket || len(found.Plugins) != 1 || found.Plugins[0].Key != "sora" {
		t.Fatalf("official = %+v, %v", found, err)
	}
	// Any other through the community lookup, whose links install from it.
	found, err = installer.FindByCode(context.Background(), " 30000003 ", nil)
	if err != nil || len(found.Plugins) != 1 || found.Plugins[0].Key != "other_hidden" ||
		found.Plugins[0].Link != MarketLink(contract.CommunityPluginResolve+"?code=30000003", "other_hidden", "0.1.0") {
		t.Fatalf("community = %+v, %v", found, err)
	}
	if ref, ok := parseMarketLink(found.Plugins[0].Link); !ok || ref.key != "other_hidden" || ref.index != contract.CommunityPluginResolve+"?code=30000003" {
		t.Fatalf("the link reads as %+v, %v", ref, ok)
	}
	if _, err = installer.FindByCode(context.Background(), "1234", nil); err == nil {
		t.Fatal("a code that is not 8 digits was looked up")
	}
}

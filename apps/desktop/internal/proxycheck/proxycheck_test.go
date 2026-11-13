package proxycheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// matcherCase is proxy settings and where requests to dst go, as
// hyper-util 0.1.20 decided (testdata/matcher.json).
type matcherCase struct {
	All   string   `json:"all"`
	HTTP  string   `json:"http"`
	HTTPS string   `json:"https"`
	No    string   `json:"no"`
	Dst   []string `json:"dst"`
	Want  []*struct {
		URI       string   `json:"uri"`
		Authority string   `json:"authority"`
		Basic     string   `json:"basic"`
		Raw       []string `json:"raw"`
	} `json:"want"`
}

func TestMatcherDecidesAsHyperUtil(t *testing.T) {
	data, err := os.ReadFile("testdata/matcher.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []matcherCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		m := settings{all: tc.All, http: tc.HTTP, https: tc.HTTPS, no: tc.No}.build()
		for i, dst := range tc.Dst {
			got, want := m.intercepts(dst), tc.Want[i]
			name := tc.HTTP + "|" + tc.All + "|" + tc.No + " → " + dst
			if want == nil {
				if got != nil {
					t.Errorf("%s: proxied through %+v", name, got)
				}
				continue
			}
			if got == nil {
				t.Errorf("%s: direct, want %s", name, want.URI)
				continue
			}
			if uri := got.scheme + "://" + got.authority + "/"; uri != want.URI {
				t.Errorf("%s: uri %s, want %s", name, uri, want.URI)
			}
			if got.proxyAuthority() != want.Authority || got.basicAuth() != want.Basic {
				t.Errorf("%s: %q %q, want %q %q", name, got.proxyAuthority(), got.basicAuth(), want.Authority, want.Basic)
			}
			isRaw := got.hasUser && got.basicAuth() == ""
			if isRaw != (want.Raw != nil) || isRaw && (got.user != want.Raw[0] || got.password != want.Raw[1]) {
				t.Errorf("%s: raw %q %q, want %q", name, got.user, got.password, want.Raw)
			}
		}
	}
}

func TestWindowsSettingsKeepBypassEntriesAsDomains(t *testing.T) {
	s := settings{}
	windowsSettings(&s, "127.0.0.1:7890", true, "localhost; 127.*;*.corp.example;<local>", true)
	if s.http != "127.0.0.1:7890" || s.https != "127.0.0.1:7890" || s.no != "localhost,127.*,corp.example,<local>" {
		t.Fatalf("%+v", s)
	}
	m := s.build()
	if m.intercepts("http://127.0.0.1:18317") == nil {
		t.Error("127.* bypassed 127.0.0.1")
	}
	if m.intercepts("http://localhost:18317") != nil || m.intercepts("http://a.corp.example") != nil {
		t.Error("a bypassed domain went through the proxy")
	}
	// The environment wins over each setting it names.
	s = settings{http: "env:1", no: "x"}
	windowsSettings(&s, "sys:2", true, "y", true)
	if s.http != "env:1" || s.https != "sys:2" || s.no != "x" {
		t.Fatalf("%+v", s)
	}
	s = settings{}
	windowsSettings(&s, "", false, "", false)
	if s != (settings{}) {
		t.Fatalf("%+v", s)
	}
}

func TestMacSettingsNeedTheEnableFlagAndAHost(t *testing.T) {
	for _, tc := range []struct {
		enabled             int32
		hasEnabled, hasHost bool
		port                int32
		hasPort             bool
		want                string
	}{
		{1, true, true, 7890, true, "proxy:7890"},
		{1, true, true, 0, false, "proxy"},
		{0, true, true, 7890, true, ""},
		{1, false, true, 7890, true, ""},
		{1, true, false, 7890, true, ""},
	} {
		if got := macSetting(tc.enabled, tc.hasEnabled, "proxy", tc.hasHost, tc.port, tc.hasPort); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
}

func TestCGIRequestsUseNoProxy(t *testing.T) {
	if m := (settings{cgi: true, http: "p:1"}).build(); m.intercepts("http://a") != nil {
		t.Fatal("proxied under CGI")
	}
}

func TestLocalhostAlsoChecksTheNumericAddress(t *testing.T) {
	if got, ok := numericOrigin("http://localhost:18317"); !ok || got != "http://127.0.0.1:18317" {
		t.Fatalf("%q", got)
	}
	if _, ok := numericOrigin("http://127.0.0.1:18317"); ok {
		t.Fatal("numeric for a numeric origin")
	}
}

func TestBypassedAddressesConnectDirectlyWithoutProbing(t *testing.T) {
	m := settings{http: "http://127.0.0.1:7892", no: "localhost"}.build()
	got, err := route(context.Background(), m, "http://localhost:18317", func(context.Context, string, *intercept) bool {
		t.Fatal("probed")
		return false
	})
	if err != nil || got != (Route{Route: "direct"}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestOnlyTheGatewayMarkerCountsAsReached(t *testing.T) {
	marked := http.Header{}
	marked.Set("x-astrlink-reachable", "1")
	if !reached(http.StatusNoContent, marked) || reached(http.StatusNoContent, http.Header{}) || reached(http.StatusBadGateway, marked) {
		t.Fatal("reached")
	}
}

func TestProbeGoesThroughTheProxyWithoutCredentialsInTheRoute(t *testing.T) {
	var auth, agent, target string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, agent, target = r.Header.Get("Proxy-Authorization"), r.Header.Get("User-Agent"), r.URL.String()
		if r.URL.Host == "127.0.0.1:18317" {
			w.Header().Set("X-Astrlink-Reachable", "1")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	authority := strings.TrimPrefix(proxy.URL, "http://")
	m := settings{http: "http://user:secret@" + authority}.build()
	got, err := route(context.Background(), m, "http://127.0.0.1:18317", probe)
	if err != nil || got != (Route{Route: "proxied", Proxy: authority}) {
		t.Fatalf("%+v %v", got, err)
	}
	if auth != "Basic dXNlcjpzZWNyZXQ=" || agent != "" || target != "http://127.0.0.1:18317/astrlink/reachability" {
		t.Fatalf("%q %q %q", auth, agent, target)
	}
	got, err = route(context.Background(), m, "http://localhost:18317", probe)
	if err != nil || got != (Route{Route: "blocked", Proxy: authority}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCheckRejectsAnythingButTheLocalGatewayRoot(t *testing.T) {
	if _, err := Check(context.Background(), "http://127.0.0.1:1/v1"); err == nil {
		t.Fatal("accepted a path")
	}
}

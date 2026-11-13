package proxycheck

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

// A read-only check of whether a client that honours the system proxy
// reaches the local gateway, as Codex's Rust HTTP stack reads the proxy.
// It never changes proxy settings, the registry, or environment variables;
// the UI only explains what the user can change.

const (
	// reachabilityPath mirrors `ingress.ReachabilityPath`: token-less and
	// never recorded.
	reachabilityPath = "/astrlink/reachability"
	// reachabilityHeader mirrors `ingress.ReachabilityHeader`, which only
	// the gateway sets.
	reachabilityHeader = "X-Astrlink-Reachable"
	probeTimeout       = 5 * time.Second
)

// Route is how a system-proxy-aware client reaches one gateway address:
// "direct" when the system proxy does not apply, "proxied" when requests
// pass through Proxy and still reach the gateway, "blocked" when they pass
// through it and never do.
type Route struct {
	Route string `json:"route"`
	Proxy string `json:"proxy,omitempty"`
}

// ProxyCheck is how a client reaches the gateway.
type ProxyCheck struct {
	// Client is the route to the address AstrLink writes into client
	// configs.
	Client Route `json:"client"`
	// Numeric is the route to `http://127.0.0.1:<port>` while the client
	// address is localhost, for configs written by hand or by other tools
	// before the switch.
	Numeric *Route `json:"numeric"`
}

// Check checks the routes to the gateway at inferenceURL.
func Check(ctx context.Context, inferenceURL string) (ProxyCheck, error) {
	origin, err := clientconfig.LocalOrigin(inferenceURL)
	if err != nil {
		return ProxyCheck{}, err
	}
	m := systemMatcher()
	client, err := route(ctx, m, origin, probe)
	if err != nil {
		return ProxyCheck{}, err
	}
	check := ProxyCheck{Client: client}
	if numeric, ok := numericOrigin(origin); ok {
		r, err := route(ctx, m, numeric, probe)
		if err != nil {
			return ProxyCheck{}, err
		}
		check.Numeric = &r
	}
	return check, nil
}

func route(ctx context.Context, m *matcher, origin string, probe func(context.Context, string, *intercept) bool) (Route, error) {
	if _, ok := parseURI(origin); !ok {
		return Route{}, errors.New("invalid inference URL")
	}
	through := m.intercepts(origin)
	if through == nil {
		return Route{Route: "direct"}, nil
	}
	if probe(ctx, origin, through) {
		return Route{Route: "proxied", Proxy: through.proxyAuthority()}, nil
	}
	return Route{Route: "blocked", Proxy: through.proxyAuthority()}, nil
}

func numericOrigin(origin string) (string, bool) {
	port, ok := strings.CutPrefix(origin, "http://localhost:")
	if !ok {
		return "", false
	}
	return "http://127.0.0.1:" + port, true
}

// probe sends the probe the way Codex would, through the proxy. No token
// or identifying header is attached. The Tauri shell's HTTP client spoke
// HTTP to every proxy, a SOCKS one included, and so does the probe.
func probe(ctx context.Context, origin string, through *intercept) bool {
	proxy := through.proxyURL()
	if proxy.Scheme != "https" {
		proxy.Scheme = "http"
	}
	client := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{
			Proxy:             func(*http.Request) (*url.URL, error) { return proxy, nil },
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+reachabilityPath, nil)
	if err != nil {
		return false
	}
	// reqwest sends no User-Agent.
	request.Header.Set("User-Agent", "")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return reached(response.StatusCode, response.Header)
}

// reached reports the gateway's answer. A proxy that cannot reach the
// gateway answers on its own (often an empty 502), so only the gateway's
// marker counts.
func reached(status int, header http.Header) bool {
	values := header.Values(reachabilityHeader)
	return status == http.StatusNoContent && len(values) > 0 && values[0] == "1"
}

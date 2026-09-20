package networkproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const macFixture = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.internal.example
    1 : 10.0.0.0/8
  }
  ExcludeSimpleHostnames : 1
  HTTPEnable : 1
  HTTPPort : 6152
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 6152
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 6153
  SOCKSProxy : 127.0.0.1
  ProxyAutoConfigEnable : 0
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      HTTPSProxy : wrong.example
      HTTPSPort : 9000
    }
  }
}`

func TestSystemProxySelectionAndBypass(t *testing.T) {
	config, err := parseMacSettings(macFixture)
	if err != nil {
		t.Fatal(err)
	}
	// System selection must not accidentally inherit terminal proxy overrides.
	t.Setenv("HTTPS_PROXY", "http://wrong.example:1234")
	proxy := fromSettings(config)
	for _, test := range []struct{ target, want string }{
		{"https://chatgpt.com/backend-api/wham/usage", "http://127.0.0.1:6152"},
		{"http://upstream.example/v1", "http://127.0.0.1:6152"},
		{"http://localhost:8317", ""},
		{"https://LOCALHOST.:8317", ""},
		{"https://api.localhost:8317", ""},
		{"http://127.0.0.2:8317", ""},
		{"http://[::1]:8317", ""},
		{"http://[::ffff:127.0.0.1]:8317", ""},
		{"http://printer", ""},
		{"https://10.1.2.3", ""},
		{"https://api.internal.example", ""},
	} {
		t.Run(test.target, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, test.target, nil)
			actual, err := proxy(req)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if actual != nil {
				got = actual.String()
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestDirectSkipsSystemDiscoveryAndEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	proxy, err := selectProxy("direct", func() (settings, error) {
		t.Fatal("disabled proxy must not query system settings")
		return settings{}, nil
	})
	if err != nil || proxy != nil {
		t.Fatalf("direct proxy = %v, err = %v", proxy, err)
	}
	if _, err := New("invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestDiscoveryAndAutomaticProxyFailuresKeepLoopbackReachable(t *testing.T) {
	failed, _ := selectProxy("system", func() (settings, error) {
		return settings{}, errors.New("query failed")
	})
	for _, proxy := range []ProxyFunc{failed, fromSettings(settings{automatic: true})} {
		req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
		if _, err := proxy(req); err == nil {
			t.Fatal("failed discovery/PAC silently fell back to direct")
		}
		req, _ = http.NewRequest(http.MethodGet, "http://localhost:8317", nil)
		if got, err := proxy(req); got != nil || err != nil {
			t.Fatalf("loopback blocked: %v %v", got, err)
		}
	}
}

func TestMacDisabledInvalidAndSOCKSOnlySettings(t *testing.T) {
	for _, output := range []string{"", "bad", strings.Replace(macFixture, "HTTPSPort : 6152", "HTTPSPort : 0", 1)} {
		if _, err := parseMacSettings(output); err == nil {
			t.Fatalf("invalid configuration accepted: %q", output)
		}
	}
	config, err := parseMacSettings(strings.ReplaceAll(strings.ReplaceAll(macFixture, "HTTPEnable : 1", "HTTPEnable : 0"), "HTTPSEnable : 1", "HTTPSEnable : 0"))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
	got, err := fromSettings(config)(req)
	if err != nil || got.String() != "socks5://127.0.0.1:6153" {
		t.Fatalf("SOCKS-only proxy = %v, %v", got, err)
	}
	config, err = parseMacSettings("<dictionary> {\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err = fromSettings(config)(req); got != nil || err != nil {
		t.Fatalf("empty system settings must connect directly: %v %v", got, err)
	}
}

func TestWindowsProtocolsAndExceptions(t *testing.T) {
	config, err := parseWindowsSettings(true, "http=127.0.0.1:6152;https=127.0.0.1:6154;socks=127.0.0.1:6153", "<local>;*.internal.example;10.0.0.0/8")
	if err != nil || config.http != "http://127.0.0.1:6152" || config.https != "http://127.0.0.1:6154" || config.socks != "socks5://127.0.0.1:6153" || !config.excludeSimple || config.bypass != "*.internal.example,10.0.0.0/8" {
		t.Fatalf("Windows configuration: %+v, %v", config, err)
	}
	config, err = parseWindowsSettings(true, "127.0.0.1:6152", "")
	if err != nil || config.http != config.https || config.http == "" {
		t.Fatalf("shared proxy: %+v %v", config, err)
	}
	if _, err := parseWindowsSettings(true, "", ""); err == nil {
		t.Fatal("empty enabled proxy accepted")
	}
	config, err = parseWindowsSettings(false, "invalid old setting", "")
	if err != nil || config.http != "" || config.https != "" {
		t.Fatalf("disabled system proxy: %+v %v", config, err)
	}
	config, err = parseWindowsSettings(true, "https=proxy.example;", "")
	if err != nil || config.https != "http://proxy.example:80" {
		t.Fatalf("default CONNECT port: %+v %v", config, err)
	}
	for _, server := range []string{"https=ftp://proxy.example:21", "https=http://user:secret@proxy.example:80", "https=http://proxy.example:80/path"} {
		if _, err := parseWindowsSettings(true, server, ""); err == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
}

func TestLinuxEnvironmentIncludesSOCKSFallback(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:6153")
	t.Setenv("NO_PROXY", "*.internal.example")
	config := environmentSettings()
	req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
	got, err := fromSettings(config)(req)
	if err != nil || got.String() != "socks5://127.0.0.1:6153" {
		t.Fatalf("environment fallback = %v %v", got, err)
	}
}

// Exercise a real CONNECT tunnel with TLS verification enabled, then the same
// destination in direct mode. No external network or account tokens are used.
func TestHTTPSUsesProxyAndDirectModeDisablesIt(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" || r.URL.Path != "/backend-api/wham/usage" {
			t.Errorf("unexpected upstream request: %s %s", r.Host, r.URL.Path)
		}
		io.WriteString(w, `{"allowed":true}`)
	}))
	defer upstream.Close()
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "example.com:443" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		connects.Add(1)
		target, err := net.DialTimeout("tcp", upstream.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer target.Close()
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { io.Copy(target, buffered); target.Close() }()
		io.Copy(conn, target)
	}))
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	for _, mode := range []string{"system", "direct"} {
		selector, err := selectProxy(mode, func() (settings, error) { return settings{https: proxy.URL}, nil })
		if err != nil {
			t.Fatal(err)
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = selector
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		if mode == "direct" {
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
			}
		}
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		response, err := client.Get("https://example.com/backend-api/wham/usage")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || string(body) != `{"allowed":true}` {
			t.Fatalf("%s response: %s %v", mode, body, err)
		}
	}
	if connects.Load() != 1 {
		t.Fatalf("expected only system mode to proxy, CONNECT count = %d", connects.Load())
	}
}

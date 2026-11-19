package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type credentials map[string]struct {
	secret string
	origin string
}

func (stored credentials) PluginCredential(_ context.Context, owner, name string) ([]byte, string, error) {
	credential, found := stored[owner+"/"+name]
	if !found {
		return nil, "", storage.ErrNotFound
	}
	return []byte(credential.secret), credential.origin, nil
}

type received struct {
	mutex    sync.Mutex
	requests []*http.Request
	bodies   []string
}

func (seen *received) add(request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	seen.requests = append(seen.requests, request)
	seen.bodies = append(seen.bodies, string(body))
}

func (seen *received) count() int {
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	return len(seen.requests)
}

func testPlugin(origins ...plugin.NetworkOrigin) *plugin.Plugin {
	return &plugin.Plugin{
		Key: "demo",
		Meta: plugin.Meta{Credentials: []plugin.CredentialSlot{
			{Name: "token", Label: "Token", Header: "Authorization", Scheme: "Bearer"},
			{Name: "key", Label: "Key", Header: "X-Api-Key"},
		}},
		Settings: plugin.Settings{Origins: origins},
	}
}

func origin(t *testing.T, raw string) string {
	t.Helper()
	normalized, err := plugin.NormalizeOrigin(raw)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}

func TestFetcherReachesAnyAddressAndSendsCredentialsOnlyToTheirServer(t *testing.T) {
	seen := &received{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen.add(request)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Add("X-Trace", "first")
		writer.Header().Add("X-Trace", "second")
		_, _ = writer.Write([]byte(`{"package_version":"1.3.0"}`))
	}))
	defer server.Close()
	allowedOrigin := origin(t, server.URL)
	target := testPlugin(plugin.NetworkOrigin{Origin: allowedOrigin})
	fetcher := &Fetcher{Credentials: credentials{"demo/token": {secret: "pc-secret", origin: allowedOrigin}}}

	response := fetcher.Fetch(context.Background(), target, plugin.HTTPRequest{
		Method: "POST", URL: server.URL + "/v1/sources/content", JSON: json.RawMessage(`{"content":"hi"}`),
		Headers: map[string]string{"Accept": "application/json"}, Credential: "token",
	})
	if response.Error != "" || response.Status != 200 || string(response.Body) != `{"package_version":"1.3.0"}` || response.Header["x-trace"] != "first" || strings.Join(response.Values["x-trace"], ",") != "first,second" {
		t.Fatalf("Fetch = %+v", response)
	}
	request := seen.requests[0]
	if request.Header.Get("Authorization") != "Bearer pc-secret" || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
		t.Fatalf("request headers = %v", request.Header)
	}
	if _, sent := request.Header["User-Agent"]; sent {
		t.Fatalf("User-Agent = %q, want none", request.Header.Get("User-Agent"))
	}
	for name := range request.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-astrlink") {
			t.Fatalf("sent %s", name)
		}
	}
	if seen.bodies[0] != `{"content":"hi"}` || request.URL.Path != "/v1/sources/content" {
		t.Fatalf("sent %s %s", request.URL.Path, seen.bodies[0])
	}

	// Plugins are trusted code: without a credential a request reaches
	// any address, with the plugin's own headers and bytes.
	elsewhere := testPlugin()
	response = fetcher.Fetch(context.Background(), elsewhere, plugin.HTTPRequest{
		Method: "PUT", URL: server.URL + "/upload", BodyBytes: []byte{0, 1, 2},
		Headers: map[string]string{"User-Agent": "sdk/1.0", "Authorization": "Bearer own"},
	})
	if response.Error != "" || response.Status != 200 {
		t.Fatalf("Fetch without a credential = %+v", response)
	}
	request, body := seen.requests[1], seen.bodies[1]
	if body != "\x00\x01\x02" || request.Header.Get("Content-Type") != "application/octet-stream" || request.Header.Get("User-Agent") != "sdk/1.0" || request.Header.Get("Authorization") != "Bearer own" {
		t.Fatalf("sent %q with %v", body, request.Header)
	}

	for _, test := range []struct {
		name    string
		target  *plugin.Plugin
		request plugin.HTTPRequest
	}{
		{"a credential to an address that is not one of its servers", testPlugin(plugin.NetworkOrigin{Origin: allowedOrigin + "1"}), plugin.HTTPRequest{Method: "GET", URL: server.URL, Credential: "token"}},
		{"a credential bound elsewhere", target, plugin.HTTPRequest{Method: "GET", URL: server.URL, Credential: "key"}},
		{"a credential not stored", target, plugin.HTTPRequest{Method: "GET", URL: server.URL, Credential: "missing"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := seen.count()
			fetcher := &Fetcher{Credentials: credentials{
				"demo/token": {secret: "pc-secret", origin: allowedOrigin},
				"demo/key":   {secret: "k", origin: "https://elsewhere.test:443"},
			}}
			if response := fetcher.Fetch(context.Background(), test.target, test.request); response.Error != plugin.FetchDenied || response.Status != 0 {
				t.Fatalf("Fetch = %+v, want denied", response)
			}
			if seen.count() != before {
				t.Fatal("a denied request reached the server")
			}
		})
	}
}

func TestFetcherSendsCredentialsOverPlaintextBeyondThisMachineOnlyWhenAsked(t *testing.T) {
	remote := origin(t, "http://192.0.2.1:9090")
	fetcher := &Fetcher{Credentials: credentials{"demo/token": {secret: "s", origin: remote}}}
	response := fetcher.Fetch(context.Background(), testPlugin(plugin.NetworkOrigin{Origin: remote}), plugin.HTTPRequest{Method: "GET", URL: "http://192.0.2.1:9090/v1/server-info", Credential: "token"})
	if response.Error != plugin.FetchDenied {
		t.Fatalf("a credential over plain http without the user's consent = %+v", response)
	}
	for _, local := range []string{"http://127.0.0.1:9090", "http://localhost:9090", "http://[::1]:9090", "https://192.0.2.1:9090"} {
		if plugin.Plaintext(origin(t, local)) {
			t.Fatalf("%s counts as plaintext beyond this machine", local)
		}
	}
	if !plugin.Plaintext(remote) {
		t.Fatalf("%s does not count as plaintext", remote)
	}
	// 192.0.2.1 is TEST-NET-1, routed nowhere: allowed, the request leaves
	// and times out instead of being denied.
	for _, test := range []struct {
		name    string
		target  *plugin.Plugin
		request plugin.HTTPRequest
	}{
		{"with the user's consent", testPlugin(plugin.NetworkOrigin{Origin: remote, AllowPlaintext: true}), plugin.HTTPRequest{Method: "GET", URL: "http://192.0.2.1:9090/v1/server-info", Credential: "token"}},
		{"without a credential", testPlugin(), plugin.HTTPRequest{Method: "GET", URL: "http://192.0.2.1:9090/v1/server-info"}},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		response = fetcher.Fetch(ctx, test.target, test.request)
		cancel()
		if response.Error == plugin.FetchDenied {
			t.Fatalf("plain http %s = %+v", test.name, response)
		}
	}
}

func TestFetcherReturnsRedirectsAndBoundsResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/moved":
			http.Redirect(writer, request, "http://127.0.0.1:1/stolen", http.StatusFound)
		case "/large":
			_, _ = writer.Write([]byte(strings.Repeat("x", plugin.MaxFetchResponseBytes+1)))
		case "/slow":
			select {
			case <-request.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}
	}))
	defer server.Close()
	target := testPlugin(plugin.NetworkOrigin{Origin: origin(t, server.URL)})
	fetcher := &Fetcher{}
	if response := fetcher.Fetch(context.Background(), target, plugin.HTTPRequest{Method: "GET", URL: server.URL + "/moved"}); response.Status != http.StatusFound || response.Error != "" || response.Header["location"] != "http://127.0.0.1:1/stolen" {
		t.Fatalf("redirect = %+v", response)
	}
	if response := fetcher.Fetch(context.Background(), target, plugin.HTTPRequest{Method: "GET", URL: server.URL + "/large"}); response.Error != plugin.FetchTooLarge || response.Body != nil {
		t.Fatalf("large response = status %d, error %q, %d bytes", response.Status, response.Error, len(response.Body))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if response := fetcher.Fetch(ctx, target, plugin.HTTPRequest{Method: "GET", URL: server.URL + "/slow"}); response.Error != plugin.FetchTimeout || response.Status != 0 {
		t.Fatalf("slow response = %+v", response)
	}
	server.Close()
	if response := fetcher.Fetch(context.Background(), target, plugin.HTTPRequest{Method: "GET", URL: server.URL + "/gone"}); response.Error != plugin.FetchNetwork {
		t.Fatalf("closed server = %+v", response)
	}
}

type valueStore struct {
	storage.PluginStore
	values   map[string]json.RawMessage
	settings []storage.PluginSettingsRecord
}

func (store *valueStore) PutPluginValue(_ context.Context, owner, key string, value json.RawMessage) error {
	if len(value) > storage.MaxPluginValueBytes {
		return storage.ErrLimitReached
	}
	store.values[owner+"/"+key] = value
	return nil
}

func (store *valueStore) GetPluginValue(_ context.Context, owner, key string) (json.RawMessage, bool, error) {
	value, found := store.values[owner+"/"+key]
	return value, found, nil
}

func (store *valueStore) ListPluginSettings(context.Context) ([]storage.PluginSettingsRecord, error) {
	return store.settings, nil
}

func TestStoreAndSettingsReadTheGatewaySettings(t *testing.T) {
	backing := &valueStore{values: map[string]json.RawMessage{}, settings: []storage.PluginSettingsRecord{{
		Key: "demo", Enabled: true, Capabilities: []string{plugin.CapabilityStorage}, Trusted: true,
		Origins: []storage.PluginOrigin{{Origin: "http://192.0.2.1:9090", AllowPlaintext: true}},
		Config:  json.RawMessage(`{"serverUrl":"http://192.0.2.1:9090"}`),
	}}}
	store := Store{Values: backing}
	if err := store.Set(context.Background(), "demo", "scope:astrlink", json.RawMessage(`"scp_1"`)); err != nil {
		t.Fatal(err)
	}
	if value, found, err := store.Get(context.Background(), "demo", "scope:astrlink"); err != nil || !found || string(value) != `"scp_1"` {
		t.Fatalf("Get = %s, %v, %v", value, found, err)
	}
	if err := store.Set(context.Background(), "demo", "big", make(json.RawMessage, storage.MaxPluginValueBytes+1)); !errors.Is(err, storage.ErrLimitReached) {
		t.Fatalf("oversized Set = %v", err)
	}
	all, err := Settings{Store: backing}.PluginSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	demo := all["demo"]
	if !demo.Enabled || !demo.Trusted || len(demo.Origins) != 1 || demo.Origins[0] != (plugin.NetworkOrigin{Origin: "http://192.0.2.1:9090", AllowPlaintext: true}) || string(demo.Config) != `{"serverUrl":"http://192.0.2.1:9090"}` {
		t.Fatalf("PluginSettings = %+v", demo)
	}
}

func TestFetcherStreamsOnTheSameTerms(t *testing.T) {
	seen := &received{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen.add(request)
		switch request.URL.Path {
		case "/moved":
			http.Redirect(writer, request, "/elsewhere", http.StatusFound)
		case "/large":
			writer.Header().Set("Content-Type", "application/octet-stream")
			flusher := writer.(http.Flusher)
			chunk := make([]byte, 64<<10)
			for written := 0; written <= plugin.MaxFetchResponseBytes; written += len(chunk) {
				_, _ = writer.Write(chunk)
				flusher.Flush()
			}
		default:
			_, _ = writer.Write([]byte("streamed"))
		}
	}))
	defer server.Close()
	allowedOrigin := origin(t, server.URL)
	target := testPlugin(plugin.NetworkOrigin{Origin: allowedOrigin})
	fetcher := &Fetcher{Credentials: credentials{"demo/token": {secret: "pc-secret", origin: allowedOrigin}}}
	stream := func(path, credential string) (*http.Response, error) {
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		return fetcher.Stream(context.Background(), target, request, credential)
	}

	response, err := stream("/", "token")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "streamed" {
		t.Fatalf("body = %q, %v", body, err)
	}
	if request := seen.requests[0]; request.Header.Get("Authorization") != "Bearer pc-secret" || request.Header.Get("User-Agent") != "" {
		t.Fatalf("request headers = %v", request.Header)
	}

	// A redirect comes back as it is.
	response, err = stream("/moved", "")
	if err != nil || response.StatusCode != http.StatusFound || seen.count() != 2 {
		t.Fatalf("redirect = %v, %v after %d requests", response, err, seen.count())
	}
	response.Body.Close()

	// A credential bound elsewhere never leaves.
	var failure *plugin.FetchError
	if _, err := stream("/", "key"); !errors.As(err, &failure) || failure.Reason != plugin.FetchDenied || seen.count() != 2 {
		t.Fatalf("Stream with a credential bound elsewhere = %v after %d requests", err, seen.count())
	}

	// The body ends past what a plugin may read.
	response, err = stream("/large", "")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if !errors.As(err, &failure) || failure.Reason != plugin.FetchTooLarge {
		t.Fatalf("reading a large body = %v", err)
	}
}

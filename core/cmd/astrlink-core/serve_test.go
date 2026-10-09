package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/console"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/coreapp"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const servePassword = "server edition password"

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestLoadServeConfigReadsOnlyItsVariables(t *testing.T) {
	read := map[string]bool{}
	recording := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			read[name] = true
			value, ok := values[name]
			return value, ok
		}
	}
	config := loadServeConfig(recording(map[string]string{"ASTRLINK_PASSWORD": "no longer used"}))
	if config.dataDirectory != "/data" || config.listen != "0.0.0.0:8317" ||
		config.outboundProxy != "environment" || config.localKeyFile != "" || config.resetPassword != "" {
		t.Fatalf("defaults = %+v", config)
	}
	config = loadServeConfig(recording(map[string]string{
		envDataDir: "/srv/astrlink", envListen: "[::]:9317", envOutboundProxy: "direct",
		localkey.EnvKeyFile: "/run/secrets/astrlink.key", console.ResetVariable: "2026-10-09",
	}))
	if config.dataDirectory != "/srv/astrlink" || config.listen != "[::]:9317" || config.outboundProxy != "direct" ||
		config.localKeyFile != "/run/secrets/astrlink.key" || config.resetPassword != "2026-10-09" {
		t.Fatalf("configured = %+v", config)
	}
	// ASTRLINK_PASSWORD is gone: the console signs in with the raw password.
	want := map[string]bool{envDataDir: true, envListen: true, envOutboundProxy: true, localkey.EnvKeyFile: true, console.ResetVariable: true}
	if len(read) != len(want) {
		t.Fatalf("read %v, want exactly %v", read, want)
	}
	for name := range want {
		if !read[name] {
			t.Fatalf("read %v, want exactly %v", read, want)
		}
	}
}

func TestServeCommandRejectsArguments(t *testing.T) {
	var stderr bytes.Buffer
	if code := runServeCommand([]string{"--data-dir", "/tmp"}, &stderr); code != 2 || !strings.Contains(stderr.String(), envDataDir) {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	stderr.Reset()
	if code := runServeCommand([]string{"--help"}, &stderr); code != 0 || !strings.Contains(stderr.String(), console.ResetVariable) ||
		strings.Contains(stderr.String(), "ASTRLINK_PASSWORD") {
		t.Fatalf("help code = %d, stderr = %s", code, stderr.String())
	}
}

func TestHealthcheckURLProbesLoopbackForAllInterfaces(t *testing.T) {
	for address, want := range map[string]string{
		"0.0.0.0:8318":   "http://127.0.0.1:8318/console/v1/status",
		":8318":          "http://127.0.0.1:8318/console/v1/status",
		"[::]:8318":      "http://[::1]:8318/console/v1/status",
		"10.0.0.2:8318":  "http://10.0.0.2:8318/console/v1/status",
		"astrlink:8318":  "http://astrlink:8318/console/v1/status",
		"127.0.0.1:9000": "http://127.0.0.1:9000/console/v1/status",
	} {
		got, err := healthcheckURL(address)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", address, got, err, want)
		}
	}
}

func TestHealthcheckExitCodes(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != console.StatusPath {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, `{"status":"setup_expired"}`)
	}))
	defer healthy.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedAddress := closed.Listener.Addr().String()
	closed.Close()

	for name, test := range map[string]struct {
		args    []string
		address string
		code    int
	}{
		// A console whose setup window closed is still up; the page explains.
		"healthy":       {address: healthy.Listener.Addr().String(), code: 0},
		"server error":  {address: failing.Listener.Addr().String(), code: 1},
		"not listening": {address: closedAddress, code: 1},
		"bad address":   {address: "no-port", code: 1},
		"arguments":     {args: []string{"--verbose"}, address: healthy.Listener.Addr().String(), code: 1},
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runHealthcheck(test.args, mapLookup(map[string]string{envListen: test.address}), &stderr)
			if code != test.code {
				t.Fatalf("code = %d, want %d; stderr = %s", code, test.code, stderr.String())
			}
		})
	}
}

type lockedLog struct {
	mu    sync.Mutex
	lines []string
}

func (log *lockedLog) Printf(format string, args ...any) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.lines = append(log.lines, fmt.Sprintf(format, args...))
}

type runningServe struct {
	address string
	done    <-chan error
	cancel  context.CancelFunc
}

// refuseNetwork fails every request without dialing.
type refuseNetwork struct{}

func (refuseNetwork) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("tests make no network calls: %s", request.URL.Host)
}

// offlineNetwork keeps the Core a test starts off the network, with or
// without ASTRLINK_CI_NO_REMOTE_MODELS: privacy model metadata comes from a
// loopback stub and price syncs fail at once.
func offlineNetwork(t *testing.T) *testNetwork {
	t.Helper()
	stub := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(stub.Close)
	return &testNetwork{
		privacyModelURL:    stub.URL,
		privacyModelClient: stub.Client(),
		pricingClient:      &http.Client{Transport: refuseNetwork{}},
	}
}

func startServe(t *testing.T, config serveConfig) runningServe {
	t.Helper()
	if config.testNetwork == nil {
		config.testNetwork = offlineNetwork(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan net.Addr, 1)
	done := make(chan error, 1)
	stopped := make(chan struct{})
	logs := &lockedLog{}
	go func() {
		defer close(stopped)
		done <- serve(ctx, cancel, config, logs.Printf, func(address net.Addr) { listening <- address })
	}()
	select {
	case address := <-listening:
		t.Cleanup(func() {
			cancel()
			<-stopped
		})
		return runningServe{address: address.String(), done: done, cancel: cancel}
	case err := <-done:
		cancel()
		t.Fatalf("serve: %v", err)
		return runningServe{}
	}
}

// shortDataDir keeps the control socket path under the macOS limit.
func shortDataDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "alsv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "data")
}

// clientRequest is one HTTP request to the shared listener.
type clientRequest struct {
	method, path, body string
	host               string
	header             map[string]string
	cookie             *http.Cookie
}

func (request clientRequest) send(t *testing.T, client *http.Client, address string) *http.Response {
	t.Helper()
	outgoing, err := http.NewRequest(request.method, "http://"+address+request.path, strings.NewReader(request.body))
	if err != nil {
		t.Fatal(err)
	}
	if request.host != "" {
		outgoing.Host = request.host
	}
	if request.body != "" {
		outgoing.Header.Set("Content-Type", "application/json")
	}
	for name, value := range request.header {
		outgoing.Header.Set(name, value)
	}
	if request.cookie != nil {
		outgoing.AddCookie(request.cookie)
	}
	response, err := client.Do(outgoing)
	if err != nil {
		t.Fatalf("%s %s: %v", request.method, request.path, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// pageHeader is what the console's own page sends with every write.
var pageHeader = map[string]string{console.RequestHeader: console.RequestHeaderValue}

func decodeBody(t *testing.T, response *http.Response, value any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(value); err != nil {
		t.Fatalf("decode %s: %v", response.Request.URL.Path, err)
	}
}

func TestServeRunsTheServerEditionOnOnePort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the server edition test drives the control socket")
	}
	directory := shortDataDir(t)
	config := serveConfig{dataDirectory: directory, listen: "127.0.0.1:0", outboundProxy: "environment"}
	running := startServe(t, config)

	// It persists under the data directory with a local key file.
	for _, name := range []string{"astrlink.db", localkey.FileName, "control.sock"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name != "control.sock" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", name, info.Mode().Perm())
		}
	}

	direct := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer direct.CloseIdleConnections()
	send := func(request clientRequest) *http.Response {
		t.Helper()
		return request.send(t, direct, running.address)
	}
	if status := consoleStatus(t, direct, running.address); status.Status != console.StatusSetupRequired || status.SetupSecondsLeft <= 0 {
		t.Fatalf("status = %+v", status)
	}
	if response := send(clientRequest{method: http.MethodGet, path: controlapi.HealthPath}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous control = %d", response.StatusCode)
	}
	passwordBody := `{"password":"` + servePassword + `"}`
	if response := send(clientRequest{method: http.MethodPost, path: console.SetupPath, body: passwordBody}); response.StatusCode != http.StatusForbidden {
		t.Fatalf("setup without the console header = %d", response.StatusCode)
	}
	// Behind nginx's default proxy_pass, Host is the upstream address and
	// differs from the page's Origin.
	setup := send(clientRequest{method: http.MethodPost, path: console.SetupPath, body: passwordBody, host: "127.0.0.1:8317",
		header: map[string]string{console.RequestHeader: console.RequestHeaderValue, "Origin": "https://astrlink.example.com"}})
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", setup.StatusCode)
	}
	session := sessionCookieOf(t, setup)
	if status := consoleStatus(t, direct, running.address); status.Status != console.StatusLoginRequired {
		t.Fatalf("status after setup = %+v", status)
	}
	// The setup password is the raw password, and it signs in.
	if login := send(clientRequest{method: http.MethodPost, path: console.LoginPath, body: passwordBody, header: pageHeader}); login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", login.StatusCode)
	}

	created := send(clientRequest{method: http.MethodPost, path: controlapi.AccessTokensPath, body: `{"name":"laptop"}`, header: pageHeader, cookie: session})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create access token = %d", created.StatusCode)
	}
	var secret struct {
		AccessToken string `json:"access_token"`
	}
	decodeBody(t, created, &secret)
	// Neither credential works on the other side.
	if response := send(clientRequest{method: http.MethodGet, path: controlapi.AccessTokensPath,
		header: map[string]string{"Authorization": "Bearer " + secret.AccessToken}}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("access token on the console = %d", response.StatusCode)
	}
	if response := send(clientRequest{method: http.MethodGet, path: "/v1/models", cookie: session}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session cookie on inference = %d", response.StatusCode)
	}

	chat := `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`
	infer := func(host string, header map[string]string) int {
		t.Helper()
		response := send(clientRequest{method: http.MethodPost, path: "/v1/chat/completions", body: chat, host: host, header: header})
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	if code := infer("my-nas.lan", nil); code != http.StatusUnauthorized {
		t.Fatalf("tokenless inference = %d", code)
	}
	if code := infer("my-nas.lan", map[string]string{"Authorization": "Bearer astr_wrong_token_0123456789abcdefghijklmnop"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong-token inference = %d", code)
	}
	if code := infer("my-nas.lan", map[string]string{"Authorization": "Bearer " + secret.AccessToken, "Origin": "http://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("browser-origin inference = %d", code)
	}
	// Any Host reaches the gateway; with no provider configured it fails
	// after authentication, which is recorded.
	if code := infer("astrlink:8317", map[string]string{"Authorization": "Bearer " + secret.AccessToken}); code == http.StatusUnauthorized ||
		code == http.StatusMisdirectedRequest || code == http.StatusForbidden || code == http.StatusNotFound {
		t.Fatalf("authenticated inference = %d", code)
	}
	var records struct {
		Items []contract.RequestRecord `json:"items"`
	}
	decodeBody(t, send(clientRequest{method: http.MethodGet, path: controlapi.RequestsPath, cookie: session}), &records)
	if len(records.Items) != 1 || records.Items[0].LocalAccessTokenID == nil {
		t.Fatalf("records = %+v; only the authenticated request may be recorded", records.Items)
	}

	// No preflight is approved, on console or inference paths.
	for _, path := range []string{controlapi.AccessTokensPath, console.LoginPath, "/v1/chat/completions", "/"} {
		response := send(clientRequest{method: http.MethodOptions, path: path, header: map[string]string{
			"Origin":                         "https://attacker.example",
			"Access-Control-Request-Method":  http.MethodPost,
			"Access-Control-Request-Headers": "content-type, authorization, x-astrlink-console",
		}})
		if response.StatusCode < 400 {
			t.Fatalf("preflight %s = %d", path, response.StatusCode)
		}
		for name := range response.Header {
			if strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
				t.Fatalf("preflight %s answered %s", path, name)
			}
		}
	}

	if code := runHealthcheck(nil, mapLookup(map[string]string{envListen: running.address}), io.Discard); code != 0 {
		t.Fatalf("healthcheck = %d", code)
	}

	// A second instance on the same data directory stops before touching it.
	second := config
	second.listen = "127.0.0.1:0"
	err := serve(context.Background(), func() {}, second, func(string, ...any) {}, func(net.Addr) {
		t.Error("the second instance started listening")
	})
	if !errors.Is(err, coreapp.ErrDataDirectoryInUse) {
		t.Fatalf("second instance = %v, want ErrDataDirectoryInUse", err)
	}
	socket := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", filepath.Join(directory, "control.sock"))
	}}}
	defer socket.CloseIdleConnections()
	response, err := socket.Get("http://local-control" + controlapi.HealthPath)
	if err != nil {
		t.Fatalf("first instance socket after the second failed: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("socket health = %d", response.StatusCode)
	}
	if response := send(clientRequest{method: http.MethodGet, path: console.StatusPath}); response.StatusCode != http.StatusOK {
		t.Fatalf("first instance console = %d", response.StatusCode)
	}

	direct.CloseIdleConnections()
	running.cancel()
	if err := <-running.done; err != nil {
		t.Fatalf("serve after cancel: %v", err)
	}
	if code := runHealthcheck(nil, mapLookup(map[string]string{envListen: running.address}), io.Discard); code != 1 {
		t.Fatalf("healthcheck after stop = %d", code)
	}
}

type serveStatus struct {
	Status           string `json:"status"`
	SignedIn         bool   `json:"signed_in"`
	SetupSecondsLeft int    `json:"setup_seconds_left"`
	PasswordReset    bool   `json:"password_reset"`
}

func consoleStatus(t *testing.T, client *http.Client, address string) serveStatus {
	t.Helper()
	var status serveStatus
	decodeBody(t, clientRequest{method: http.MethodGet, path: console.StatusPath}.send(t, client, address), &status)
	return status
}

func sessionCookieOf(t *testing.T, response *http.Response) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == console.SessionCookie {
			return cookie
		}
	}
	t.Fatalf("no session cookie (status %d)", response.StatusCode)
	return nil
}

func TestServeResetSwitchClearsThePasswordOncePerValue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the server edition test drives the control socket")
	}
	directory := shortDataDir(t)
	direct := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	passwordBody := `{"password":"` + servePassword + `"}`
	run := func(resetValue string) (runningServe, serveStatus) {
		t.Helper()
		running := startServe(t, serveConfig{dataDirectory: directory, listen: "127.0.0.1:0", outboundProxy: "environment", resetPassword: resetValue})
		status := consoleStatus(t, direct, running.address)
		// The container stays healthy in every setup state.
		if code := runHealthcheck(nil, mapLookup(map[string]string{envListen: running.address}), io.Discard); code != 0 {
			t.Fatalf("healthcheck with %q = %d", resetValue, code)
		}
		return running, status
	}
	stop := func(running runningServe) {
		t.Helper()
		running.cancel()
		if err := <-running.done; err != nil {
			t.Fatalf("serve: %v", err)
		}
	}

	// First start: set the password and create a client access token.
	running, status := run("")
	if status.Status != console.StatusSetupRequired || status.PasswordReset {
		t.Fatalf("first start = %+v", status)
	}
	setup := clientRequest{method: http.MethodPost, path: console.SetupPath, body: passwordBody, header: pageHeader}.send(t, direct, running.address)
	session := sessionCookieOf(t, setup)
	created := clientRequest{method: http.MethodPost, path: controlapi.AccessTokensPath, body: `{"name":"laptop"}`, header: pageHeader, cookie: session}.
		send(t, direct, running.address)
	var secret struct {
		AccessToken string `json:"access_token"`
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create access token = %d", created.StatusCode)
	}
	decodeBody(t, created, &secret)
	stop(running)

	tokenWorks := func(running runningServe) bool {
		t.Helper()
		response := clientRequest{method: http.MethodGet, path: "/v1/models", header: map[string]string{"Authorization": "Bearer " + secret.AccessToken}}.
			send(t, direct, running.address)
		return response.StatusCode != http.StatusUnauthorized
	}
	// A new switch value clears the password once; the access token, which
	// the local key protects, keeps working.
	running, status = run("forgot-1")
	if status.Status != console.StatusSetupRequired || !status.PasswordReset {
		t.Fatalf("start with a new reset value = %+v", status)
	}
	if !tokenWorks(running) {
		t.Fatal("the reset broke a client access token")
	}
	setup = clientRequest{method: http.MethodPost, path: console.SetupPath, body: passwordBody, header: pageHeader}.send(t, direct, running.address)
	if setup.StatusCode != http.StatusOK {
		t.Fatalf("setup after reset = %d", setup.StatusCode)
	}
	stop(running)

	// Leaving the same value in place does nothing.
	running, status = run("forgot-1")
	if status.Status != console.StatusLoginRequired || status.PasswordReset {
		t.Fatalf("restart with the same reset value = %+v", status)
	}
	stop(running)

	// A different value resets again.
	running, status = run("forgot-2")
	if status.Status != console.StatusSetupRequired || !status.PasswordReset || !tokenWorks(running) {
		t.Fatalf("start with another reset value = %+v", status)
	}
	stop(running)
}

func TestConsumePasswordResetDiscardsRawPartsAndKeepsCredentials(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, err := sqlite.Open(ctx, filepath.Join(directory, "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	tokens, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Create(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	vault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{
		KDF: rawseal.KDFParams{Algorithm: "argon2id", Version: 19, Time: 1, MemoryKiB: 64, Threads: 1},
	})
	setPassword := func() {
		t.Helper()
		if _, err := vault.ChangePassword(ctx, controlapi.RawPasswordSet, []byte(servePassword), controlapi.RawProof{}); err != nil {
			t.Fatal(err)
		}
	}
	setPassword()
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: offlineRequestID, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.AuditRecordSummary{RequestBodyCaptured: true},
	}); err != nil {
		t.Fatal(err)
	}
	insertOfflinePart(t, store, storage.AuditDirectionRequest, storage.AuditExposureRaw, `{"content":"`+offlineSecret+`"}`)
	insertOfflinePart(t, store, storage.AuditDirectionUpstreamRequest, storage.AuditExposureShareable, `{"content":"<EMAIL_1>"}`)
	rawParts := func() int {
		t.Helper()
		parts, err := store.GetAuditBlobsByRequest(ctx, offlineRequestID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, part := range parts {
			if part.Exposure == storage.AuditExposureRaw {
				count++
			}
		}
		return count
	}
	if rawParts() != 1 {
		t.Fatal("the raw part was not captured")
	}
	passwordSet := func() bool {
		t.Helper()
		status, err := vault.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return status.PasswordSet
	}
	consume := func(value string) bool {
		t.Helper()
		reset, err := consumePasswordReset(ctx, directory, value, vault, func(string, ...any) {})
		if err != nil {
			t.Fatal(err)
		}
		return reset
	}

	if consume("") || !passwordSet() {
		t.Fatal("an empty switch reset the password")
	}
	if !consume("forgot-1") || passwordSet() || rawParts() != 0 {
		t.Fatalf("new value: password set = %t, raw parts = %d", passwordSet(), rawParts())
	}
	if parts, err := store.GetAuditBlobsByRequest(ctx, offlineRequestID); err != nil || len(parts) != 1 {
		t.Fatalf("shareable parts after reset = %d, %v", len(parts), err)
	}
	if _, err := tokens.Authenticate(ctx, token.Value); err != nil {
		t.Fatalf("access token after reset: %v", err)
	}
	info, err := os.Stat(filepath.Join(directory, passwordResetMarker))
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("reset marker = %v, %v", info, err)
	}
	if marker, _ := os.ReadFile(filepath.Join(directory, passwordResetMarker)); strings.Contains(string(marker), "forgot-1") {
		t.Fatal("the marker stores the switch value itself")
	}

	setPassword()
	if consume("forgot-1") || !passwordSet() {
		t.Fatal("the same value reset the password again")
	}
	if !consume("forgot-2") || passwordSet() {
		t.Fatal("a different value did not reset the password")
	}
}

func TestServeRaisesTheRawBackoffCap(t *testing.T) {
	options := serveCoreOptions(serveConfig{dataDirectory: "/data"}, "token", console.NewSessions(), func() {}, func(string, ...any) {})
	if options.rawBackoffCap != 15*time.Minute || !options.noLoopbackCallback || options.consoleSessions == nil {
		t.Fatalf("serve options = %+v", options)
	}
}

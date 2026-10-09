package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const (
	testPassword = "correct horse battery"
	testHost     = "nas.local:8317"
)

// testKDF keeps Argon2id cheap; the vault's rules are otherwise unchanged.
var testKDF = rawseal.KDFParams{Algorithm: "argon2id", Version: 19, Time: 1, MemoryKiB: 64, Threads: 1}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) advance(by time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(by)
	clock.mu.Unlock()
}

type fixture struct {
	console *Handler
	vault   *controlapi.Vault
	clock   *testClock
}

// newFixture wires the console to a real control API and raw vault the way
// serve does, on one frozen clock. A non-empty password is set already.
func newFixture(t *testing.T, password string) fixture {
	t.Helper()
	return newFixtureWith(t, password, false)
}

func newFixtureWith(t *testing.T, password string, passwordReset bool) fixture {
	t.Helper()
	ctx := context.Background()
	clock := &testClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	tokens, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatalf("access token manager: %v", err)
	}
	vault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{KDF: testKDF, Now: clock.Now})
	sessions := NewSessions()
	control, err := controlapi.NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), controlapi.Dependencies{
		ServiceStore:       store,
		AccessTokenManager: tokens,
		RequestRecords:     store,
		AuditSettings:      store,
		AuditKeys:          store,
		AuditBlobs:         store,
		RawVault:           vault,
		ConsoleSessions:    sessions,
		// Serve generates this in memory and never hands it out.
		ControlToken: "unpublished-control-token-0123456789",
	})
	if err != nil {
		t.Fatalf("control handler: %v", err)
	}
	if password != "" {
		if _, err := vault.ChangePassword(ctx, controlapi.RawPasswordSet, []byte(password), controlapi.RawProof{}); err != nil {
			t.Fatalf("set raw password: %v", err)
		}
	}
	console, err := New(Config{RawPassword: vault, Control: control, Sessions: sessions, PasswordReset: passwordReset, Now: clock.Now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fixture{console: console, vault: vault, clock: clock}
}

func (f fixture) rawStatus(t *testing.T) controlapi.RawVaultStatus {
	t.Helper()
	status, err := f.vault.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

type call struct {
	method, path, body string
	// page marks a request from the console's own page, which sends
	// RequestHeader.
	page   bool
	cookie *http.Cookie
	header map[string]string
}

func (c call) do(handler http.Handler) *httptest.ResponseRecorder {
	var request *http.Request
	if c.body != "" {
		request = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request = httptest.NewRequest(c.method, c.path, nil)
	}
	request.Host = testHost
	if c.page {
		request.Header.Set(RequestHeader, RequestHeaderValue)
	}
	if c.cookie != nil {
		request.AddCookie(c.cookie)
	}
	for name, value := range c.header {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func passwordBody(password string) string {
	body, _ := json.Marshal(map[string]string{"password": password})
	return string(body)
}

func login(handler http.Handler, password string) *httptest.ResponseRecorder {
	return call{method: http.MethodPost, path: LoginPath, body: passwordBody(password), page: true}.do(handler)
}

func setup(handler http.Handler, password string) *httptest.ResponseRecorder {
	return call{method: http.MethodPost, path: SetupPath, body: passwordBody(password), page: true}.do(handler)
}

func sessionFrom(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookie {
			return cookie
		}
	}
	t.Fatalf("no session cookie in %d %v %s", response.Code, response.Header(), response.Body.String())
	return nil
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope %q: %v", response.Body.String(), err)
	}
	return envelope.Error.Code
}

func statusOf(t *testing.T, handler http.Handler, cookie *http.Cookie) statusResponse {
	t.Helper()
	response := call{method: http.MethodGet, path: StatusPath, cookie: cookie}.do(handler)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var status statusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestConsoleFirstRunSetupSucceedsOnceAndSignsIn(t *testing.T) {
	f := newFixture(t, "")
	handler := f.console
	status := statusOf(t, handler, nil)
	if status.Status != StatusSetupRequired || status.SetupSecondsLeft != 600 || status.SignedIn || status.PasswordReset ||
		status.ResetVariable != "ASTRLINK_RESET_PASSWORD" || status.PasswordMinLength != rawseal.MinPasswordRunes ||
		status.PasswordMaxLength != rawseal.MaxPasswordRunes {
		t.Fatalf("status = %+v", status)
	}
	if response := login(handler, testPassword); response.Code != http.StatusConflict || errorCode(t, response) != StatusSetupRequired {
		t.Fatalf("login before setup = %d %s", response.Code, response.Body.String())
	}
	if response := (call{method: http.MethodPost, path: SetupPath, body: passwordBody(testPassword)}).do(handler); response.Code != http.StatusForbidden ||
		errorCode(t, response) != "console_header_required" {
		t.Fatalf("setup without the console header = %d %s", response.Code, response.Body.String())
	}
	if response := setup(handler, "short"); response.Code != http.StatusBadRequest || errorCode(t, response) != "invalid_password" {
		t.Fatalf("short setup = %d %s", response.Code, response.Body.String())
	}

	response := setup(handler, testPassword)
	if response.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", response.Code, response.Body.String())
	}
	cookie := sessionFrom(t, response)
	if status := statusOf(t, handler, cookie); status.Status != StatusLoginRequired || !status.SignedIn || status.SetupSecondsLeft != 0 {
		t.Fatalf("status after setup = %+v", status)
	}
	if raw := f.rawStatus(t); !raw.PasswordSet || raw.Unlocked {
		t.Fatalf("raw vault after setup = %+v; setup sets the raw password and unlocks nothing", raw)
	}

	// Only the first setup wins, and its password is the one that signs in.
	if response := setup(handler, "another password"); response.Code != http.StatusConflict || errorCode(t, response) != "already_configured" {
		t.Fatalf("second setup = %d %s", response.Code, response.Body.String())
	}
	if response := login(handler, "another password"); response.Code != http.StatusUnauthorized {
		t.Fatalf("login with the refused password = %d", response.Code)
	}
	if response := login(handler, testPassword); response.Code != http.StatusOK {
		t.Fatalf("login with the setup password = %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleSetupClosesAfterItsWindow(t *testing.T) {
	f := newFixture(t, "")
	f.clock.advance(SetupWindow - time.Second)
	if status := statusOf(t, f.console, nil); status.Status != StatusSetupRequired || status.SetupSecondsLeft != 1 {
		t.Fatalf("status in the last second = %+v", status)
	}
	f.clock.advance(time.Second)
	if status := statusOf(t, f.console, nil); status.Status != StatusSetupExpired || status.SetupSecondsLeft != 0 {
		t.Fatalf("status after the window = %+v", status)
	}
	if response := setup(f.console, testPassword); response.Code != http.StatusForbidden || errorCode(t, response) != StatusSetupExpired {
		t.Fatalf("setup after the window = %d %s", response.Code, response.Body.String())
	}
	if f.rawStatus(t).PasswordSet {
		t.Fatal("a setup after the window set the password")
	}
}

func TestConsoleReportsAPasswordReset(t *testing.T) {
	status := statusOf(t, newFixtureWith(t, "", true).console, nil)
	if status.Status != StatusSetupRequired || !status.PasswordReset {
		t.Fatalf("status = %+v", status)
	}
}

func TestConsoleLoginUsesTheRawPasswordAndLeavesRawContentLocked(t *testing.T) {
	f := newFixture(t, testPassword)
	handler := f.console
	if status := statusOf(t, handler, nil); status.Status != StatusLoginRequired || status.SignedIn {
		t.Fatalf("status = %+v", status)
	}
	cookie := sessionFrom(t, login(handler, testPassword))
	if raw := f.rawStatus(t); raw.Unlocked {
		t.Fatal("signing in unlocked raw content")
	}
	var sealing controlapi.RawSealingStatus
	response := call{method: http.MethodGet, path: controlapi.RawSealingPath, cookie: cookie}.do(handler)
	if err := json.Unmarshal(response.Body.Bytes(), &sealing); err != nil || response.Code != http.StatusOK || sealing.Unlocked || !sealing.PasswordSet {
		t.Fatalf("raw sealing = %d %s", response.Code, response.Body.String())
	}
	// Viewing raw content still asks for the password through unlock.
	unlock := call{method: http.MethodPost, path: controlapi.RawUnlockPath, cookie: cookie, page: true}
	if response := unlock.do(handler); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unlock without the password = %d %s", response.Code, response.Body.String())
	}
	unlock.body = `{"proof":{"password":"` + testPassword + `"}}`
	if response := unlock.do(handler); response.Code != http.StatusOK || !f.rawStatus(t).Unlocked {
		t.Fatalf("unlock with the password = %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleWrongPasswordsHitTheVaultBackoff(t *testing.T) {
	f := newFixture(t, testPassword)
	// The vault answers two wrong passwords at once; the third starts a
	// one-second wait.
	for attempt := range 3 {
		if response := login(f.console, "wrong password"); response.Code != http.StatusUnauthorized || errorCode(t, response) != "invalid_password" {
			t.Fatalf("attempt %d = %d %s", attempt+1, response.Code, response.Body.String())
		}
	}
	response := login(f.console, testPassword)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("backoff = %d Retry-After=%q", response.Code, response.Header().Get("Retry-After"))
	}
	if raw := f.rawStatus(t); raw.RetryAfter != time.Second {
		t.Fatalf("vault retry after = %s; the console must use the vault's backoff", raw.RetryAfter)
	}
	f.clock.advance(time.Second)
	if response := login(f.console, testPassword); response.Code != http.StatusOK {
		t.Fatalf("login after the backoff = %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleSignInReachesOperatorControlAndSignOutEndsIt(t *testing.T) {
	handler := newFixture(t, testPassword).console
	createToken := call{method: http.MethodPost, path: controlapi.AccessTokensPath, body: `{"name":"laptop"}`, page: true}
	if response := createToken.do(handler); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous create = %d %s", response.Code, response.Body.String())
	}
	response := login(handler, testPassword)
	cookie := sessionFrom(t, response)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Secure || cookie.MaxAge <= 0 {
		t.Fatalf("cookie = %+v", cookie)
	}
	// Creating an access token is an operator write.
	createToken.cookie = cookie
	if response := createToken.do(handler); response.Code != http.StatusCreated {
		t.Fatalf("operator create = %d %s", response.Code, response.Body.String())
	}
	logout := call{method: http.MethodPost, path: LogoutPath, page: true, cookie: cookie}.do(handler)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", logout.Code, logout.Body.String())
	}
	if cleared := sessionFrom(t, logout); cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v", cleared)
	}
	if status := statusOf(t, handler, cookie); status.SignedIn {
		t.Fatalf("status after logout = %+v", status)
	}
	if response := (call{method: http.MethodGet, path: controlapi.AccessTokensPath, cookie: cookie}).do(handler); response.Code != http.StatusUnauthorized {
		t.Fatalf("list after logout = %d", response.Code)
	}
}

func rawPasswordAction(cookie *http.Cookie, body string) call {
	return call{method: http.MethodPost, path: controlapi.RawPasswordPath, body: body, cookie: cookie, page: true}
}

func TestConsolePasswordChangeEndsEveryOtherSession(t *testing.T) {
	f := newFixture(t, testPassword)
	handler := f.console
	changer := sessionFrom(t, login(handler, testPassword))
	other := sessionFrom(t, login(handler, testPassword))
	const newPassword = "a brand new password"

	wrongProof := rawPasswordAction(changer, `{"action":"change","password":"`+newPassword+`","proof":{"password":"wrong password"}}`)
	if response := wrongProof.do(handler); response.Code < 400 {
		t.Fatalf("change with a wrong current password = %d", response.Code)
	}
	if status := statusOf(t, handler, other); !status.SignedIn {
		t.Fatal("a refused change ended another session")
	}
	f.clock.advance(time.Minute) // past any backoff the wrong proof started

	change := rawPasswordAction(changer, `{"action":"change","password":"`+newPassword+`","proof":{"password":"`+testPassword+`"}}`)
	if response := change.do(handler); response.Code != http.StatusOK {
		t.Fatalf("change = %d %s", response.Code, response.Body.String())
	}
	if status := statusOf(t, handler, other); status.SignedIn {
		t.Fatal("the other session survived a password change")
	}
	if status := statusOf(t, handler, changer); !status.SignedIn {
		t.Fatal("the change ended the changer's own session")
	}
	if response := login(handler, testPassword); response.Code != http.StatusUnauthorized {
		t.Fatalf("old password after change = %d", response.Code)
	}
	if response := login(handler, newPassword); response.Code != http.StatusOK {
		t.Fatalf("new password after change = %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleRefusesRawPasswordSetAndResetThroughControl(t *testing.T) {
	f := newFixture(t, testPassword)
	cookie := sessionFrom(t, login(f.console, testPassword))
	for _, body := range []string{
		`{"action":"reset","password":"taken over password"}`,
		`{"action":"set","password":"taken over password"}`,
	} {
		response := rawPasswordAction(cookie, body).do(f.console)
		if response.Code != http.StatusForbidden || errorCode(t, response) != "raw_password_action_unavailable" {
			t.Fatalf("%s = %d %s", body, response.Code, response.Body.String())
		}
	}
	if response := login(f.console, testPassword); response.Code != http.StatusOK {
		t.Fatalf("the original password stopped working: %d", response.Code)
	}
}

func TestConsoleWritesRequireTheConsoleHeader(t *testing.T) {
	handler := newFixture(t, testPassword).console
	cookie := sessionFrom(t, login(handler, testPassword))
	for _, value := range []string{"", "0", "true"} {
		for _, request := range []call{
			{method: http.MethodPost, path: controlapi.AccessTokensPath, body: `{"name":"x"}`},
			{method: http.MethodPost, path: LoginPath, body: `{"password":"` + testPassword + `"}`},
			{method: http.MethodPost, path: LogoutPath},
		} {
			request.cookie = cookie
			request.header = map[string]string{"Origin": "http://" + testHost}
			if value != "" {
				request.header[RequestHeader] = value
			}
			response := request.do(handler)
			if response.Code != http.StatusForbidden || errorCode(t, response) != "console_header_required" {
				t.Fatalf("%s=%q %s = %d %s", RequestHeader, value, request.path, response.Code, response.Body.String())
			}
		}
	}
	// The session survived every refused logout, and reads need no header.
	if status := statusOf(t, handler, cookie); !status.SignedIn {
		t.Fatalf("status = %+v", status)
	}
	if response := (call{method: http.MethodGet, path: controlapi.AccessTokensPath, cookie: cookie}).do(handler); response.Code != http.StatusOK {
		t.Fatalf("read = %d %s", response.Code, response.Body.String())
	}
	// nginx's default proxy_pass sends its upstream address as Host, so Host
	// and Origin differ; the header alone decides.
	request := call{method: http.MethodPost, path: controlapi.AccessTokensPath, body: `{"name":"proxied"}`, cookie: cookie, page: true,
		header: map[string]string{"Origin": "https://astrlink.example.com"}}
	if response := request.do(handler); response.Code != http.StatusCreated {
		t.Fatalf("proxied write = %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleRefusesCORSPreflights(t *testing.T) {
	handler := newFixture(t, testPassword).console
	cookie := sessionFrom(t, login(handler, testPassword))
	for _, path := range []string{controlapi.AccessTokensPath, LoginPath, LogoutPath, StatusPath, "/"} {
		response := call{method: http.MethodOptions, path: path, cookie: cookie, header: map[string]string{
			"Origin":                         "https://attacker.example",
			"Access-Control-Request-Method":  http.MethodPost,
			"Access-Control-Request-Headers": "content-type, x-astrlink-console",
		}}.do(handler)
		if response.Code < 400 {
			t.Fatalf("preflight %s = %d", path, response.Code)
		}
		for name := range response.Header() {
			if strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
				t.Fatalf("preflight %s answered %s", path, name)
			}
		}
	}
}

func TestConsoleIgnoresAccessTokensAsSessions(t *testing.T) {
	handler := newFixture(t, testPassword).console
	cookie := sessionFrom(t, login(handler, testPassword))
	created := call{method: http.MethodPost, path: controlapi.AccessTokensPath, body: `{"name":"client"}`, cookie: cookie, page: true}.do(handler)
	var secret struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &secret); err != nil || secret.AccessToken == "" {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	for _, header := range []map[string]string{
		{"Authorization": "Bearer " + secret.AccessToken},
		{"X-Api-Key": secret.AccessToken},
		{"Cookie": SessionCookie + "=" + secret.AccessToken},
	} {
		response := call{method: http.MethodGet, path: controlapi.AccessTokensPath, header: header}.do(handler)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("access token as %v = %d", header, response.Code)
		}
	}
}

func TestConsoleOwnsItsPathsAndLeavesTheRestToInference(t *testing.T) {
	handler := newFixture(t, testPassword).console
	for path, owned := range map[string]bool{
		"/":                          true,
		"/index.html":                true,
		StatusPath:                   true,
		"/console/v1/unknown":        true,
		controlapi.HealthPath:        true,
		controlapi.AccessTokensPath:  true,
		"/v1/models":                 false,
		"/v1/chat/completions":       false,
		"/v1beta/models":             false,
		"/astrlink/reachability":     false,
		"/console":                   false,
		"/control/v2/health":         false,
		"/settings/routing":          false,
		"/missing.js":                false,
		"/v1/messages?beta=true#top": false,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if got := handler.Owns(request); got != owned {
			t.Errorf("Owns(%s) = %t, want %t", path, got, owned)
		}
	}
}

func TestConsoleBoundsRequestBodies(t *testing.T) {
	read := make(chan error, 1)
	handler, err := New(Config{RawPassword: newFixture(t, testPassword).vault, Control: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, err := io.Copy(io.Discard, request.Body)
		read <- err
		writer.WriteHeader(http.StatusNoContent)
	})})
	if err != nil {
		t.Fatal(err)
	}
	cookie := sessionFrom(t, login(handler, testPassword))
	call{method: http.MethodPost, path: "/control/v1/upload", body: strings.Repeat("x", maxBodyBytes+1), cookie: cookie, page: true}.do(handler)
	var tooLarge *http.MaxBytesError
	if err := <-read; !errors.As(err, &tooLarge) {
		t.Fatalf("reading an oversized body = %v, want *http.MaxBytesError", err)
	}
}

func TestConsoleSessionsExpireWhenIdleAndAfterTheirLifetime(t *testing.T) {
	fixture := newFixture(t, testPassword)
	handler, clock := fixture.console, fixture.clock
	idle := sessionFrom(t, login(handler, testPassword))
	clock.advance(sessionIdleTimeout)
	if status := statusOf(t, handler, idle); status.SignedIn {
		t.Fatalf("idle session = %+v", status)
	}

	active := sessionFrom(t, login(handler, testPassword))
	for elapsed := time.Duration(0); elapsed < sessionLifetime-sessionIdleTimeout/2; elapsed += sessionIdleTimeout / 2 {
		clock.advance(sessionIdleTimeout / 2)
		if status := statusOf(t, handler, active); !status.SignedIn {
			t.Fatalf("active session ended after %s", elapsed)
		}
	}
	clock.advance(sessionIdleTimeout / 2)
	if status := statusOf(t, handler, active); status.SignedIn {
		t.Fatalf("session outlived its lifetime: %+v", status)
	}
}

func TestConsoleSessionCookieIsSecureBehindHTTPS(t *testing.T) {
	handler := newFixture(t, testPassword).console
	body := `{"password":"` + testPassword + `"}`
	proxied := call{method: http.MethodPost, path: LoginPath, body: body, page: true,
		header: map[string]string{"X-Forwarded-Proto": "https"}}.do(handler)
	if cookie := sessionFrom(t, proxied); !cookie.Secure {
		t.Fatalf("cookie behind HTTPS proxy = %+v", cookie)
	}
}

func TestConsoleSessionLimitEndsTheLeastRecentlyUsed(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	store := NewSessions()
	first, err := store.create(clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for range maxSessions - 1 {
		clock.advance(time.Second)
		if _, err := store.create(clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	clock.advance(time.Second)
	if !store.touch(first, clock.Now()) {
		t.Fatal("first session ended early")
	}
	clock.advance(time.Second)
	if _, err := store.create(clock.Now()); err != nil {
		t.Fatal(err)
	}
	if len(store.sessions) != maxSessions || !store.touch(first, clock.Now()) {
		t.Fatalf("sessions = %d; the recently used first session must survive", len(store.sessions))
	}
}

func TestConsoleSetsSecurityHeadersAndNoStore(t *testing.T) {
	handler := newFixture(t, testPassword).console
	for _, request := range []call{
		{method: http.MethodGet, path: StatusPath},
		{method: http.MethodGet, path: controlapi.HealthPath},
		{method: http.MethodGet, path: "/console/v1/unknown"},
		{method: http.MethodGet, path: "/"},
	} {
		response := request.do(handler)
		header := response.Header()
		if !strings.Contains(header.Get("Content-Security-Policy"), "default-src 'self'") ||
			!strings.Contains(header.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
			header.Get("Referrer-Policy") != "no-referrer" || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s headers = %v", request.path, header)
		}
		if strings.HasPrefix(request.path, "/console/") || strings.HasPrefix(request.path, "/control/") {
			if header.Get("Cache-Control") != "no-store" {
				t.Fatalf("%s Cache-Control = %q", request.path, header.Get("Cache-Control"))
			}
		}
	}
}

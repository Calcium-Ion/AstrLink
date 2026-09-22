package accountauth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func TestBeginAuthorizationUsesOfficialPublicClientByDefault(t *testing.T) {
	store := accountauth.NewMemoryCredentialStore()
	preferred, fallback := availablePortPair(t)
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		PreferredPort:  preferred,
		FallbackPort:   fallback,
		AuthorizeURL:   "https://auth.openai.com/oauth/authorize?originator=astrlink&Originator=astrlink",
		ExtraAuthQuery: url.Values{"originator": {"astrlink"}},
	}, store, nil)
	session, err := manager.Begin(
		context.Background(),
		"service_test",
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	t.Cleanup(func() { _, _ = manager.Cancel(context.Background(), "service_test") })
	authorizationURL, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if session.Flow != contract.AuthorizationFlowBrowser ||
		authorizationURL.Query().Get("client_id") != accountauth.DefaultCodexOAuthClientID {
		t.Fatalf("session = %#v url = %s", session, session.AuthorizationURL)
	}
	if values := authorizationURL.Query()["originator"]; len(values) != 1 || values[0] != accountauth.DefaultCodexOriginator ||
		authorizationURL.Query().Get("Originator") != "" {
		t.Fatal("authorization URL did not enforce the Codex originator")
	}
}

func TestOAuthDefaultsUseRegisteredCallbackPorts(t *testing.T) {
	t.Parallel()
	config := (accountauth.OAuthConfig{}).Normalize()
	if config.PreferredPort != 1455 || config.FallbackPort != 1457 {
		t.Fatalf(
			"callback ports = %d, %d; want 1455, 1457",
			config.PreferredPort,
			config.FallbackPort,
		)
	}
}

func TestAuthorizationCallbackExchangesCodeAndPersistsTokens(t *testing.T) {
	t.Parallel()
	var exchanged atomic.Bool
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/oauth/authorize":
			http.Error(writer, "not used", http.StatusNotFound)
		case request.URL.Path == "/oauth/token":
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("code") != "auth-code-1" || values.Get("code_verifier") == "" {
				http.Error(writer, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			exchanged.Store(true)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token":  "access-secret-token-value",
				"refresh_token": "refresh-secret-token-value",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"id_token":      tinyIDToken("acct_12345678"),
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(issuer.Close)

	store := accountauth.NewMemoryCredentialStore()
	var savedAccount contract.SubscriptionAccountID
	preferred, fallback := availablePortPair(t)
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		ClientID:      "astrlink_test_client",
		Issuer:        issuer.URL,
		HTTPClient:    issuer.Client(),
		PreferredPort: preferred,
		FallbackPort:  fallback,
		Now:           func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
	}, store, func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
		savedAccount = "subscription_test01"
		return store.Put(ctx, savedAccount, tokens)
	})

	session, err := manager.Begin(
		context.Background(),
		"service_test01",
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("session.Validate() = %v", err)
	}
	authURL, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" || authURL.Query().Get("code_challenge") == "" {
		t.Fatalf("authorization url missing state/pkce: %s", session.AuthorizationURL)
	}

	// redirect_uri uses localhost; callback server listens on 127.0.0.1 with same port/path.
	parsedRedirect, err := url.Parse(authURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	callbackURL := "http://127.0.0.1:" + parsedRedirect.Port() + parsedRedirect.Path + "?code=auth-code-1&state=" + url.QueryEscape(state)
	response, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback get: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("callback status = %d body = %s", response.StatusCode, body)
	}
	if !exchanged.Load() {
		t.Fatal("expected token exchange")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		current, ok := manager.Get("service_test01")
		if ok && current.Status == contract.AuthorizationSessionStatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not complete: %#v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	tokens, err := store.Get(context.Background(), savedAccount)
	if err != nil {
		t.Fatalf("store.Get() = %v", err)
	}
	if tokens.AccessToken != "access-secret-token-value" || tokens.RefreshToken != "refresh-secret-token-value" {
		t.Fatalf("unexpected tokens: %#v", tokens)
	}
}

func TestSessionCompletionReleasesGlobalLockAndLogoutWaits(t *testing.T) {
	t.Parallel()
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "completion-access-marker",
			"refresh_token": "completion-refresh-marker",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(issuer.Close)

	const (
		completingService contract.ServiceID = "service_completion_primary"
		otherService      contract.ServiceID = "service_completion_other"
	)
	store := accountauth.NewMemoryCredentialStore()
	completionStarted := make(chan struct{})
	releaseCompletion := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseCompletion) }) })

	preferred, fallback := availablePortPair(t)
	var manager *accountauth.SessionManager
	manager = accountauth.NewSessionManager(accountauth.OAuthConfig{
		ClientID:      "astrlink_test_client",
		Issuer:        issuer.URL,
		HTTPClient:    issuer.Client(),
		PreferredPort: preferred,
		FallbackPort:  fallback,
	}, store, func(ctx context.Context, _ contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
		if _, ok := manager.Get(otherService); !ok {
			return errors.New("other authorization session disappeared")
		}
		close(completionStarted)
		<-releaseCompletion
		return store.Put(ctx, completingService, tokens)
	})

	primary, err := manager.Begin(
		context.Background(),
		completingService,
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin(primary) = %v", err)
	}
	_, err = manager.Begin(
		context.Background(),
		otherService,
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin(other) = %v", err)
	}
	t.Cleanup(func() { _, _ = manager.Cancel(context.Background(), otherService) })

	authURL, err := url.Parse(primary.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(authURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := "http://127.0.0.1:" + redirect.Port() + redirect.Path +
		"?code=completion-code&state=" + url.QueryEscape(authURL.Query().Get("state"))
	callbackDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get(callbackURL)
		if requestErr != nil {
			callbackDone <- requestErr
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode != http.StatusOK {
			callbackDone <- errors.New("authorization callback failed")
			return
		}
		callbackDone <- nil
	}()

	select {
	case <-completionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("authorization completion did not start or callback re-entry deadlocked")
	}

	lookupDone := make(chan bool, 1)
	go func() {
		_, ok := manager.Get(otherService)
		lookupDone <- ok
	}()
	select {
	case ok := <-lookupDone:
		if !ok {
			t.Fatal("other authorization session disappeared during completion")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("another service was blocked by credential persistence")
	}

	cleanupDone := make(chan struct{})
	go func() {
		manager.CancelAllForService(completingService)
		close(cleanupDone)
	}()
	select {
	case <-cleanupDone:
		t.Fatal("same-service cleanup returned before credential persistence finished")
	case <-time.After(25 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseCompletion) })
	select {
	case err := <-callbackDone:
		if err != nil {
			t.Fatalf("authorization callback = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authorization callback did not finish")
	}
	select {
	case <-cleanupDone:
	case <-time.After(2 * time.Second):
		t.Fatal("same-service cleanup did not resume after completion")
	}
	current, ok := manager.Get(completingService)
	if !ok || current.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("completed session = %#v, present=%v", current, ok)
	}
	if _, err := store.Get(context.Background(), completingService); err != nil {
		t.Fatalf("persisted completion credential = %v", err)
	}
}

func TestStateMismatchFailsClosed(t *testing.T) {
	t.Parallel()
	issuer := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(issuer.Close)
	store := accountauth.NewMemoryCredentialStore()
	preferred, fallback := availablePortPair(t)
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		ClientID:      "astrlink_test_client",
		Issuer:        issuer.URL,
		HTTPClient:    issuer.Client(),
		PreferredPort: preferred,
		FallbackPort:  fallback,
	}, store, nil)
	session, err := manager.Begin(
		context.Background(),
		"service_test02",
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	authURL, _ := url.Parse(session.AuthorizationURL)
	redirect, _ := url.Parse(authURL.Query().Get("redirect_uri"))
	callbackURL := "http://127.0.0.1:" + redirect.Port() + redirect.Path + "?code=x&state=wrong"
	response, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback get: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("expected non-OK callback response")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, _ := manager.Get("service_test02")
		if current.Status == contract.AuthorizationSessionStatusFailed {
			if current.Error == nil || current.Error.Code != accountauth.ErrCodeStateMismatch {
				t.Fatalf("unexpected error: %#v", current.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session status = %s", current.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBrowserAuthorizationUsesFallbackPortThenDeviceCode(t *testing.T) {
	preferred, fallback := availablePortPair(t)
	preferredListener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(preferred))
	if err != nil {
		t.Fatal(err)
	}
	defer preferredListener.Close()

	store := accountauth.NewMemoryCredentialStore()
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:        "https://auth.example.test",
		PreferredPort: preferred,
		FallbackPort:  fallback,
	}, store, nil)
	session, err := manager.Begin(
		context.Background(),
		"service_port_fallback",
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin() fallback port = %v", err)
	}
	redirect := authorizationRedirectURI(t, session.AuthorizationURL)
	if redirect.Port() != strconv.Itoa(fallback) {
		t.Fatalf("redirect port = %s, want %d", redirect.Port(), fallback)
	}
	_, _ = manager.Cancel(context.Background(), "service_port_fallback")

	fallbackListener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(fallback))
	if err != nil {
		t.Fatal(err)
	}
	defer fallbackListener.Close()
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "device-secret-fallback",
				"user_code":      "FALL-BACK",
				"interval":       "60",
			})
		case "/api/accounts/deviceauth/token":
			writer.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer issuer.Close()
	manager = accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:        issuer.URL,
		HTTPClient:    issuer.Client(),
		PreferredPort: preferred,
		FallbackPort:  fallback,
	}, store, nil)
	session, err = manager.Begin(
		context.Background(),
		"service_device_fallback",
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("Begin() device fallback = %v", err)
	}
	if session.Flow != contract.AuthorizationFlowDeviceCode ||
		session.DeviceCode == nil ||
		session.DeviceCode.UserCode != "FALL-BACK" {
		t.Fatalf("fallback session = %#v", session)
	}
	raw, _ := json.Marshal(session)
	if strings.Contains(string(raw), "device-secret-fallback") {
		t.Fatalf("session leaked device_auth_id: %s", raw)
	}
	if _, err := manager.Cancel(context.Background(), "service_device_fallback"); err != nil {
		t.Fatalf("cancel fallback session: %v", err)
	}

	session, err = manager.Begin(
		context.Background(),
		"service_device_explicit",
		contract.AuthorizationFlowDeviceCode,
	)
	if err != nil {
		t.Fatalf("Begin() explicit Device Code with both ports occupied = %v", err)
	}
	defer func() { _, _ = manager.Cancel(context.Background(), "service_device_explicit") }()
	if session.Flow != contract.AuthorizationFlowDeviceCode ||
		session.DeviceCode == nil {
		t.Fatalf("explicit device session = %#v", session)
	}
}

func TestDeviceCodeAuthorizationPollsExchangesAndPersists(t *testing.T) {
	var polls atomic.Int32
	var exchanges atomic.Int32
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("originator") != accountauth.DefaultCodexOriginator ||
			request.UserAgent() != accountauth.CodexUserAgent("") || request.Header.Get("version") != "" {
			t.Errorf("unexpected auth identity on %s", request.URL.Path)
		}
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			if input["client_id"] != accountauth.DefaultCodexOAuthClientID {
				http.Error(writer, "bad client", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "device-auth-secret",
				"usercode":       "ABCD-EFGH",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			if input["device_auth_id"] != "device-auth-secret" ||
				input["user_code"] != "ABCD-EFGH" {
				http.Error(writer, "bad poll", http.StatusBadRequest)
				return
			}
			if polls.Add(1) == 1 {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"authorization_code": "device-authorization-secret",
				"code_challenge":     "device-challenge-secret",
				"code_verifier":      "device-verifier-secret",
			})
		case "/oauth/token":
			exchanges.Add(1)
			body, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(body))
			if values.Get("code") != "device-authorization-secret" ||
				values.Get("code_verifier") != "device-verifier-secret" ||
				values.Get("redirect_uri") != issuer.URL+"/deviceauth/callback" {
				http.Error(writer, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token":  "device-access-secret",
				"refresh_token": "device-refresh-secret",
				"expires_in":    3600,
				"id_token":      tinyIDToken("acct_device"),
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer issuer.Close()

	store := accountauth.NewMemoryCredentialStore()
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:                issuer.URL,
		HTTPClient:            issuer.Client(),
		DeviceCodeTTL:         2 * time.Second,
		DevicePollMinInterval: 5 * time.Millisecond,
		DevicePollMaxInterval: 5 * time.Millisecond,
	}, store, func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
		return store.Put(ctx, session.ServiceID, tokens)
	})
	session, err := manager.Begin(
		context.Background(),
		"service_device_success",
		contract.AuthorizationFlowDeviceCode,
	)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	if session.Flow != contract.AuthorizationFlowDeviceCode ||
		session.AuthorizationURL != "" ||
		session.DeviceCode == nil ||
		session.DeviceCode.VerificationURL != issuer.URL+"/codex/device" {
		t.Fatalf("device session = %#v", session)
	}
	waitForSessionStatus(
		t,
		manager,
		"service_device_success",
		contract.AuthorizationSessionStatusCompleted,
	)
	current, _ := manager.Get("service_device_success")
	if current.DeviceCode != nil || current.AuthorizationURL != "" {
		t.Fatalf("terminal session retained login instructions: %#v", current)
	}
	if polls.Load() < 2 || exchanges.Load() != 1 {
		t.Fatalf("polls=%d exchanges=%d", polls.Load(), exchanges.Load())
	}
	tokens, err := store.Get(context.Background(), "service_device_success")
	if err != nil || tokens.AccessToken != "device-access-secret" {
		t.Fatalf("stored tokens = %#v, %v", tokens, err)
	}
}

func TestDeviceCodeCancellationPreventsLateCompletion(t *testing.T) {
	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	var exchanges atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "device-late-secret",
				"user_code":      "LATE-CODE",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			close(pollStarted)
			<-releasePoll
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"authorization_code": "late-code",
				"code_challenge":     "late-challenge",
				"code_verifier":      "late-verifier",
			})
		case "/oauth/token":
			exchanges.Add(1)
			http.Error(writer, "unexpected", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer issuer.Close()

	store := accountauth.NewMemoryCredentialStore()
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:                issuer.URL,
		HTTPClient:            issuer.Client(),
		DevicePollMinInterval: time.Millisecond,
		DevicePollMaxInterval: time.Millisecond,
	}, store, func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
		return store.Put(ctx, session.ServiceID, tokens)
	})
	_, err := manager.Begin(
		context.Background(),
		"service_device_cancel",
		contract.AuthorizationFlowDeviceCode,
	)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("device poll did not start")
	}
	cancelled, err := manager.Cancel(context.Background(), "service_device_cancel")
	if err != nil {
		t.Fatalf("Cancel() = %v", err)
	}
	close(releasePoll)
	time.Sleep(30 * time.Millisecond)
	if cancelled.Status != contract.AuthorizationSessionStatusCancelled ||
		cancelled.DeviceCode != nil {
		t.Fatalf("cancelled session = %#v", cancelled)
	}
	current, _ := manager.Get("service_device_cancel")
	if current.Status != contract.AuthorizationSessionStatusCancelled {
		t.Fatalf("late poll changed status: %#v", current)
	}
	if exchanges.Load() != 0 {
		t.Fatalf("late poll performed %d token exchanges", exchanges.Load())
	}
	if _, err := store.Get(context.Background(), "service_device_cancel"); err == nil {
		t.Fatal("late poll persisted credentials")
	}
}

func TestDeviceCodeUnavailableAndExpiryAreSanitized(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			unavailable := httptest.NewServer(http.HandlerFunc(
				func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(status)
				},
			))
			defer unavailable.Close()
			manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
				Issuer:     unavailable.URL,
				HTTPClient: unavailable.Client(),
			}, accountauth.NewMemoryCredentialStore(), nil)
			_, err := manager.Begin(
				context.Background(),
				"service_device_unavailable",
				contract.AuthorizationFlowDeviceCode,
			)
			if !errors.Is(err, accountauth.ErrDeviceCodeUnavailable) {
				t.Fatalf("Begin() = %v, want ErrDeviceCodeUnavailable", err)
			}
		})
	}

	pending := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/accounts/deviceauth/usercode" {
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "expiry-secret",
				"user_code":      "EXPI-REME",
				"interval":       "0",
			})
			return
		}
		writer.WriteHeader(http.StatusForbidden)
	}))
	defer pending.Close()
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:                pending.URL,
		HTTPClient:            pending.Client(),
		DeviceCodeTTL:         30 * time.Millisecond,
		DevicePollMinInterval: 5 * time.Millisecond,
		DevicePollMaxInterval: 5 * time.Millisecond,
	}, accountauth.NewMemoryCredentialStore(), nil)
	_, err := manager.Begin(
		context.Background(),
		"service_device_expiry",
		contract.AuthorizationFlowDeviceCode,
	)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	waitForSessionStatus(
		t,
		manager,
		"service_device_expiry",
		contract.AuthorizationSessionStatusExpired,
	)
	expired, _ := manager.Get("service_device_expiry")
	if expired.DeviceCode != nil ||
		expired.Error == nil ||
		expired.Error.Code != accountauth.ErrCodeSessionExpired {
		t.Fatalf("expired session = %#v", expired)
	}
}

func TestDeviceCodeNetworkFailureEndsPendingSession(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 &&
				request.URL.Path == "/api/accounts/deviceauth/usercode" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"device_auth_id":"network-secret","user_code":"NETW-ORK","interval":"1"}`,
					)),
					Request: request,
				}, nil
			}
			return nil, errors.New("simulated network failure")
		}),
	}
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:                "https://auth.example.test",
		HTTPClient:            client,
		DevicePollMinInterval: time.Millisecond,
		DevicePollMaxInterval: time.Millisecond,
	}, accountauth.NewMemoryCredentialStore(), nil)
	_, err := manager.Begin(
		context.Background(),
		"service_device_network",
		contract.AuthorizationFlowDeviceCode,
	)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	waitForSessionStatus(
		t,
		manager,
		"service_device_network",
		contract.AuthorizationSessionStatusFailed,
	)
	failed, _ := manager.Get("service_device_network")
	if failed.DeviceCode != nil ||
		failed.Error == nil ||
		failed.Error.Code != accountauth.ErrCodeDeviceCodePoll {
		t.Fatalf("failed session = %#v", failed)
	}
	raw, _ := json.Marshal(failed)
	if strings.Contains(string(raw), "network-secret") {
		t.Fatalf("failed session leaked device_auth_id: %s", raw)
	}
}

func TestRefreshSingleflightAndInvalidGrant(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			http.NotFound(writer, request)
			return
		}
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "access-rotated",
			"refresh_token": "refresh-rotated",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(issuer.Close)

	store := accountauth.NewMemoryCredentialStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	_ = store.Put(context.Background(), "subscription_01", accountauth.AccountTokens{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    now.Add(time.Minute),
	})
	client := accountauth.NewTokenClient(accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     issuer.URL,
		HTTPClient: issuer.Client(),
		Now:        func() time.Time { return now },
	})
	source := accountauth.NewTokenSource(store, client, 5*time.Minute, func() time.Time { return now })
	ctx := context.Background()
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := source.AccessToken(ctx, "subscription_01")
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("AccessToken() = %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}

	var invalidCalls atomic.Int32
	invalid := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		invalidCalls.Add(1)
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(invalid.Close)
	client = accountauth.NewTokenClient(accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     invalid.URL,
		HTTPClient: invalid.Client(),
		Now:        func() time.Time { return now },
	})
	var marked bool
	var reentryErr error
	source = accountauth.NewTokenSource(store, client, 5*time.Minute, func() time.Time { return now })
	source.SetHooks(nil, func(context.Context, contract.SubscriptionAccountID, error) error {
		marked = true
		_, reentryErr = source.AccessToken(context.Background(), "subscription_01")
		return nil
	})
	_ = store.Put(context.Background(), "subscription_01", accountauth.AccountTokens{
		AccessToken: "access-old", RefreshToken: "refresh-old", ExpiresAt: now,
	})
	_, err := source.AccessToken(ctx, "subscription_01")
	if err != accountauth.ErrInvalidGrant {
		t.Fatalf("AccessToken() = %v, want ErrInvalidGrant", err)
	}
	if !marked {
		t.Fatal("expected invalid_grant hook")
	}
	if !errors.Is(reentryErr, accountauth.ErrTokenSourceInvalidated) {
		t.Fatalf("invalid_grant hook reentry = %v, want ErrTokenSourceInvalidated", reentryErr)
	}
	_, err = source.AccessToken(ctx, "subscription_01")
	if !errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
		t.Fatalf("later AccessToken() = %v, want ErrTokenSourceInvalidated", err)
	}
	if invalidCalls.Load() != 1 {
		t.Fatalf("invalid_grant refresh calls = %d, want 1", invalidCalls.Load())
	}
}

func TestTokenSourceInvalidationFailsClosedUntilActivated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := accountauth.NewMemoryCredentialStore()
	const accountID contract.SubscriptionAccountID = "subscription_lifecycle"
	tokens := accountauth.AccountTokens{
		AccessToken:  "current-access",
		RefreshToken: "current-refresh",
		ExpiresAt:    now.Add(time.Hour),
	}
	if err := store.Put(context.Background(), accountID, tokens); err != nil {
		t.Fatal(err)
	}
	source := accountauth.NewTokenSource(
		store,
		accountauth.NewTokenClient(accountauth.OAuthConfig{}),
		5*time.Minute,
		func() time.Time { return now },
	)

	source.Invalidate(accountID)
	if _, err := source.AccessToken(context.Background(), accountID); !errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
		t.Fatalf("AccessToken() after Invalidate() = %v, want ErrTokenSourceInvalidated", err)
	}

	source.Activate(accountID)
	got, err := source.AccessToken(context.Background(), accountID)
	if err != nil {
		t.Fatalf("AccessToken() after Activate() = %v", err)
	}
	if got.AccessToken != tokens.AccessToken || got.RefreshToken != tokens.RefreshToken {
		t.Fatal("AccessToken() after Activate() did not return the stored credentials")
	}
}

func TestTokenSourceInvalidationDuringRemoteRefreshPreventsPut(t *testing.T) {
	t.Parallel()
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(refreshStarted)
		<-releaseRefresh
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "rotated-access",
			"refresh_token": "rotated-refresh",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRefresh) })
		issuer.Close()
	})

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := accountauth.NewMemoryCredentialStore()
	const accountID contract.SubscriptionAccountID = "subscription_blocked_remote"
	original := accountauth.AccountTokens{
		AccessToken:  "original-access",
		RefreshToken: "original-refresh",
		ExpiresAt:    now,
	}
	if err := store.Put(context.Background(), accountID, original); err != nil {
		t.Fatal(err)
	}
	source := accountauth.NewTokenSource(
		store,
		accountauth.NewTokenClient(accountauth.OAuthConfig{
			Issuer:     issuer.URL,
			HTTPClient: issuer.Client(),
		}),
		5*time.Minute,
		func() time.Time { return now },
	)

	refreshDone := make(chan error, 1)
	go func() {
		_, err := source.AccessToken(context.Background(), accountID)
		refreshDone <- err
	}()
	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh request did not start")
	}

	invalidationDone := make(chan struct{})
	go func() {
		source.Invalidate(accountID)
		close(invalidationDone)
	}()
	waitForTokenSourceInvalidation(t, source, accountID)
	select {
	case <-invalidationDone:
		t.Fatal("Invalidate() returned while the remote refresh was blocked")
	default:
	}

	releaseOnce.Do(func() { close(releaseRefresh) })
	if err := <-refreshDone; !errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
		t.Fatalf("refresh AccessToken() = %v, want ErrTokenSourceInvalidated", err)
	}
	select {
	case <-invalidationDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Invalidate() did not return after refresh finished")
	}
	got, err := store.Get(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != original.AccessToken || got.RefreshToken != original.RefreshToken {
		t.Fatal("invalidated remote refresh persisted replacement credentials")
	}
}

func TestTokenSourceInvalidationWaitsForPutBeforeCallerDeletes(t *testing.T) {
	t.Parallel()
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "rotated-access",
			"refresh_token": "rotated-refresh",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(issuer.Close)

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	const accountID contract.SubscriptionAccountID = "subscription_blocked_put"
	inner := accountauth.NewMemoryCredentialStore()
	if err := inner.Put(context.Background(), accountID, accountauth.AccountTokens{
		AccessToken:  "original-access",
		RefreshToken: "original-refresh",
		ExpiresAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	store := &blockingPutCredentialStore{
		inner:      inner,
		putStarted: make(chan struct{}),
		releasePut: make(chan struct{}),
	}
	store.block.Store(true)
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(store.releasePut) }) })
	source := accountauth.NewTokenSource(
		store,
		accountauth.NewTokenClient(accountauth.OAuthConfig{
			Issuer:     issuer.URL,
			HTTPClient: issuer.Client(),
		}),
		5*time.Minute,
		func() time.Time { return now },
	)

	refreshDone := make(chan error, 1)
	go func() {
		_, err := source.AccessToken(context.Background(), accountID)
		refreshDone <- err
	}()
	select {
	case <-store.putStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not reach credential Put")
	}

	invalidationDone := make(chan struct{})
	go func() {
		source.Invalidate(accountID)
		close(invalidationDone)
	}()
	waitForTokenSourceInvalidation(t, source, accountID)
	select {
	case <-invalidationDone:
		t.Fatal("Invalidate() returned while credential Put was blocked")
	default:
	}

	releaseOnce.Do(func() { close(store.releasePut) })
	if err := <-refreshDone; !errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
		t.Fatalf("refresh AccessToken() = %v, want ErrTokenSourceInvalidated", err)
	}
	select {
	case <-invalidationDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Invalidate() did not wait for credential Put")
	}
	persisted, err := inner.Get(context.Background(), accountID)
	if err != nil || persisted.AccessToken != "rotated-access" {
		t.Fatalf("credential Put did not finish before Invalidate(): %v", err)
	}

	if err := inner.Delete(context.Background(), accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Get(context.Background(), accountID); !errors.Is(err, accountauth.ErrCredentialNotFound) {
		t.Fatalf("credential after ordered Delete() = %v, want ErrCredentialNotFound", err)
	}
}

func TestTokenClientTreatsParsedInvalidGrantAsTerminalRegardlessOfStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []int{
		http.StatusOK,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
	} {
		status := status
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(
					`{"error":"invalid_grant","error_description":"private-response-marker"}`,
				))
			}))
			t.Cleanup(issuer.Close)

			client := accountauth.NewTokenClient(accountauth.OAuthConfig{
				ClientID:   "astrlink_test_client",
				Issuer:     issuer.URL,
				HTTPClient: issuer.Client(),
			})
			_, err := client.Refresh(context.Background(), "refresh-input-marker")
			if !errors.Is(err, accountauth.ErrInvalidGrant) {
				t.Fatalf("Refresh() = %v, want ErrInvalidGrant", err)
			}
			for _, secret := range []string{"private-response-marker", "refresh-input-marker"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("Refresh() error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestTokenClientOAuthFailuresDoNotLeakResponseBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{
			name: "malformed invalid grant",
			body: `{"error":"invalid_grant","error_description":"malformed-private-marker"`,
		},
		{
			name: "other oauth error",
			body: `{"error":"access_denied","error_description":"denied-private-marker"}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(test.body))
			}))
			t.Cleanup(issuer.Close)

			client := accountauth.NewTokenClient(accountauth.OAuthConfig{
				ClientID:   "astrlink_test_client",
				Issuer:     issuer.URL,
				HTTPClient: issuer.Client(),
			})
			_, err := client.Refresh(context.Background(), "refresh-input-marker")
			if err == nil {
				t.Fatal("Refresh() unexpectedly succeeded")
			}
			if errors.Is(err, accountauth.ErrInvalidGrant) {
				t.Fatalf("Refresh() = %v, malformed or different OAuth error must not classify as invalid_grant", err)
			}
			for _, secret := range []string{
				"malformed-private-marker",
				"denied-private-marker",
				"refresh-input-marker",
			} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("Refresh() error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type blockingPutCredentialStore struct {
	inner      *accountauth.MemoryCredentialStore
	block      atomic.Bool
	putOnce    sync.Once
	putStarted chan struct{}
	releasePut chan struct{}
}

func (store *blockingPutCredentialStore) Available(ctx context.Context) error {
	return store.inner.Available(ctx)
}

func (store *blockingPutCredentialStore) Get(
	ctx context.Context,
	id contract.SubscriptionAccountID,
) (accountauth.AccountTokens, error) {
	return store.inner.Get(ctx, id)
}

func (store *blockingPutCredentialStore) Put(
	ctx context.Context,
	id contract.SubscriptionAccountID,
	tokens accountauth.AccountTokens,
) error {
	if store.block.Load() {
		store.putOnce.Do(func() { close(store.putStarted) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-store.releasePut:
		}
	}
	return store.inner.Put(ctx, id, tokens)
}

func (store *blockingPutCredentialStore) Delete(
	ctx context.Context,
	id contract.SubscriptionAccountID,
) error {
	return store.inner.Delete(ctx, id)
}

func waitForTokenSourceInvalidation(
	t *testing.T,
	source *accountauth.TokenSource,
	accountID contract.SubscriptionAccountID,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		_, err := source.AccessToken(ctx, accountID)
		cancel()
		if errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
			return
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatalf("AccessToken() while waiting for invalidation = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("token source was not invalidated")
		}
	}
}

func tinyIDToken(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"chatgpt_account_id":"` + accountID + `"}`))
	return header + "." + payload + ".x"
}

func availablePortPair(t *testing.T) (int, int) {
	t.Helper()
	first := availablePort(t)
	second := availablePort(t)
	for second == first {
		second = availablePort(t)
	}
	return first, second
}

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func authorizationRedirectURI(t *testing.T, authorizationURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(parsed.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	return redirect
}

func waitForSessionStatus(
	t *testing.T,
	manager *accountauth.SessionManager,
	serviceID contract.ServiceID,
	status contract.AuthorizationSessionStatus,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		session, ok := manager.Get(serviceID)
		if ok && session.Status == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not reach %s: %#v", status, session)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

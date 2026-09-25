package subscription_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestBeginAuthorizationUsesOfficialPublicClientByDefault(t *testing.T) {
	accounts := subscription.NewMemoryAccountStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	account := contract.SubscriptionAccount{
		ID: "service_codex_01", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	preferred, fallback := subscriptionTestPortPair(t)
	manager, err := subscription.NewManager(
		accounts,
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{
			PreferredPort: preferred,
			FallbackPort:  fallback,
			Now:           func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	session, err := manager.BeginAuthorization(
		context.Background(),
		account.ID,
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("BeginAuthorization() = %v", err)
	}
	defer func() { _, _ = manager.CancelAuthorization(context.Background(), account.ID) }()
	if session.Flow != contract.AuthorizationFlowBrowser ||
		!strings.Contains(session.AuthorizationURL, accountauth.DefaultCodexOAuthClientID) {
		t.Fatalf("session = %#v", session)
	}
	account, err = manager.Get(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if account.Status != contract.SubscriptionStatusAuthorizing {
		t.Fatalf("status = %s", account.Status)
	}
	if account.AuthorizationBoundary != manager.AuthorizationBoundary() ||
		!strings.Contains(account.AuthorizationBoundary, "local-only") ||
		!strings.Contains(account.AuthorizationBoundary, "remote revocation is unavailable") {
		t.Fatalf("authorization boundary = %q", account.AuthorizationBoundary)
	}
}

func TestConnectedAccountModelsAndResponsesPath(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("originator") != accountauth.DefaultCodexOriginator ||
			request.UserAgent() != accountauth.CodexUserAgent("") ||
			request.Header.Get("version") != accountauth.DefaultCodexModelsClientVersion {
			t.Errorf("inconsistent Codex identity on %s", request.URL.Path)
		}
		auth := request.Header.Get("Authorization")
		if auth != "Bearer access-secret-token-value" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("ChatGPT-Account-ID") != "acct_12345678" {
			http.Error(writer, "missing account", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/api/codex/usage":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"plan_type": "plus",
				"rate_limit": map[string]any{
					"primary_window": map[string]any{
						"used_percent":         10,
						"limit_window_seconds": 18000,
						"reset_at":             1783090800,
					},
				},
			})
		case "/api/codex/rate-limit-reset-credits/consume":
			if request.Method != http.MethodPost {
				http.Error(writer, "method", http.StatusMethodNotAllowed)
				return
			}
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"redeem_request_id"`) || strings.Contains(string(body), "credit_id") {
				http.Error(writer, "bad consume body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code":          "reset",
				"windows_reset": 2,
			})
		case "/models":
			if request.URL.Query().Get("client_version") == "" {
				http.Error(writer, "missing client_version", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"models": []map[string]any{
					{"slug": "gpt-5", "visibility": "list", "supported_in_api": false},
				},
			})
		case "/responses":
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"model"`) {
				http.Error(writer, "bad body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":     "resp_1",
				"object": "response",
				"status": "completed",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		APIBaseURL: upstream.URL,
		HTTPClient: upstream.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	account := contract.SubscriptionAccount{
		ID: "service_codex_02", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatalf("PutAccount() = %v", err)
	}
	expires := now.Add(time.Hour)
	tokens := accountauth.AccountTokens{
		AccessToken:  "access-secret-token-value",
		RefreshToken: "refresh-secret-token-value",
		AccountID:    "acct_12345678",
		ExpiresAt:    expires,
	}
	if err := credentials.Put(context.Background(), account.ID, tokens); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	account.Status = contract.SubscriptionStatusConnected
	account.CredentialRef = accountauth.CredentialRefFor(account.ID)
	account.TokenExpiresAt = &expires
	account.UpdatedAt = now
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatalf("PutAccount() = %v", err)
	}

	liveTokens, err := manager.AccessToken(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("AccessToken() = %v", err)
	}
	models, err := manager.Provider().ListModels(context.Background(), liveTokens)
	if err != nil {
		t.Fatalf("ListModels() = %v", err)
	}
	if len(models.Data) != 2 || models.Data[0].ID != "gpt-5" || models.Data[1].ID != "codex-auto-review" {
		t.Fatalf("models = %#v", models)
	}
	body, status, err := manager.Provider().ProbeNonStreamingResponse(context.Background(), liveTokens, "gpt-5")
	if err != nil || status != http.StatusOK {
		t.Fatalf("ProbeNonStreamingResponse() status=%d err=%v body=%s", status, err, body)
	}
	if !strings.Contains(string(body), "resp_1") {
		t.Fatalf("unexpected response body %s", body)
	}
	usage, err := manager.Usage(context.Background(), account.ID)
	if err != nil || usage.PlanType != "plus" || usage.Primary == nil || usage.Primary.UsedPercent != 10 {
		t.Fatalf("Usage() = %#v err=%v", usage, err)
	}
	reset, err := manager.ConsumeReset(context.Background(), account.ID)
	if err != nil || reset.Outcome != contract.UsageResetOutcomeReset || reset.ServiceID != account.ID {
		t.Fatalf("ConsumeReset() = %#v err=%v", reset, err)
	}

	// Secret corpus: account JSON and errors must not contain token material.
	listed, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	raw, _ := json.Marshal(listed)
	for _, secret := range []string{"access-secret-token-value", "refresh-secret-token-value", "Bearer "} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("subscription list leaked %q: %s", secret, raw)
		}
	}
}

func TestUnavailableCredentialStoreFailsClosed(t *testing.T) {
	t.Parallel()
	accounts := subscription.NewMemoryAccountStore()
	now := time.Now().UTC()
	account := contract.SubscriptionAccount{
		ID: "service_codex_03", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(
		accounts,
		accountauth.UnavailableCredentialStore{},
		accountauth.OAuthConfig{ClientID: "astrlink_test_client"},
	)
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	_, err = manager.BeginAuthorization(
		context.Background(),
		account.ID,
		contract.AuthorizationFlowBrowser,
	)
	if err == nil {
		t.Fatal("BeginAuthorization() expected credential store failure")
	}
}

func TestLogoutRemovesLocalCredentialsAndDisclosesRemoteBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	account := connectedAccount(
		"service_codex_logout",
		now,
		now.Add(30*time.Minute),
		"acct_logout_123456",
	)
	account.AccountHint = "acct***56"
	account.LastRefreshAt = timePointer(now.Add(-time.Minute))
	account.LastError = &contract.SubscriptionError{
		Code: "previous_error", Message: "previous sanitized failure",
	}
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
		AccessToken:      "logout-access-secret",
		RefreshToken:     "logout-refresh-secret",
		AccountID:        account.ProviderAccountID,
		ExpiresAt:        *account.TokenExpiresAt,
		RawIDTokenClaims: "logout-identity-secret",
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		Now: func() time.Time { return now.Add(time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}

	loggedOut, err := manager.Logout(ctx, account.ID)
	if err != nil {
		t.Fatalf("Logout() = %v", err)
	}
	if _, err := credentials.Get(ctx, account.ID); !errors.Is(err, accountauth.ErrCredentialNotFound) {
		t.Fatalf("credential after Logout() = %v, want ErrCredentialNotFound", err)
	}
	persisted, err := accounts.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"credential_ref":      persisted.CredentialRef,
		"account_hint":        persisted.AccountHint,
		"provider_account_id": persisted.ProviderAccountID,
	} {
		if got != "" {
			t.Fatalf("%s after Logout() = %q", name, got)
		}
	}
	if persisted.Status != contract.SubscriptionStatusDisconnected ||
		persisted.TokenExpiresAt != nil ||
		persisted.LastRefreshAt != nil ||
		persisted.LastError != nil {
		t.Fatalf("persisted account after Logout() = %#v", persisted)
	}
	boundary := manager.AuthorizationBoundary()
	if boundary == "" ||
		!strings.Contains(boundary, "local-only") ||
		!strings.Contains(boundary, "remote revocation is unavailable") ||
		loggedOut.AuthorizationBoundary != boundary ||
		persisted.AuthorizationBoundary != boundary {
		t.Fatalf("authorization boundary = %q, account = %#v", boundary, loggedOut)
	}
	raw, err := json.Marshal(loggedOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"logout-access-secret",
		"logout-refresh-secret",
		"logout-identity-secret",
	} {
		if strings.Contains(string(raw), secret) || strings.Contains(boundary, secret) {
			t.Fatalf("logout response leaked secret marker %q: %s", secret, raw)
		}
	}
}

func TestRefreshRotationPersistsCredentialAndAccountMetadata(t *testing.T) {
	t.Parallel()
	var refreshCalls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		refreshCalls.Add(1)
		if err := request.ParseForm(); err != nil {
			http.Error(writer, "bad form", http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") != "refresh_token" ||
			request.Form.Get("refresh_token") != "rotation-refresh-original" {
			http.Error(writer, "bad refresh request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "rotation-access-new",
			"refresh_token": "rotation-refresh-new",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(issuer.Close)

	ctx := context.Background()
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	account := connectedAccount("service_codex_rotation", now, now, "acct_rotation_123456")
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
		AccessToken:  "rotation-access-original",
		RefreshToken: "rotation-refresh-original",
		AccountID:    account.ProviderAccountID,
		ExpiresAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     issuer.URL,
		HTTPClient: issuer.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := manager.AccessToken(ctx, account.ID)
	if err != nil {
		t.Fatalf("AccessToken() = %v", err)
	}
	if rotated.AccessToken != "rotation-access-new" ||
		rotated.RefreshToken != "rotation-refresh-new" ||
		rotated.AccountID != account.ProviderAccountID {
		t.Fatal("AccessToken() did not return the expected rotated credential fields")
	}
	stored, err := credentials.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != rotated.AccessToken || stored.RefreshToken != rotated.RefreshToken {
		t.Fatal("credential store did not persist the rotated credential fields")
	}
	persisted, err := accounts.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(time.Hour)
	if persisted.Status != contract.SubscriptionStatusConnected ||
		persisted.TokenExpiresAt == nil ||
		!persisted.TokenExpiresAt.Equal(wantExpiry) ||
		persisted.LastRefreshAt == nil ||
		!persisted.LastRefreshAt.Equal(now) ||
		persisted.AuthorizationBoundary != manager.AuthorizationBoundary() ||
		refreshCalls.Load() != 1 {
		t.Fatalf("persisted account = %#v, refresh calls = %d", persisted, refreshCalls.Load())
	}
}

func TestInvalidGrantMarksNeedsReauthAndLaterAccessFailsFast(t *testing.T) {
	t.Parallel()
	var refreshCalls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		refreshCalls.Add(1)
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","error_description":"private-error-marker"}`))
	}))
	t.Cleanup(issuer.Close)

	ctx := context.Background()
	now := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	account := connectedAccount("service_codex_invalid", now, now, "acct_invalid_123456")
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
		AccessToken:  "invalid-access-secret",
		RefreshToken: "invalid-refresh-secret",
		AccountID:    account.ProviderAccountID,
		ExpiresAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     issuer.URL,
		HTTPClient: issuer.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.AccessToken(ctx, account.ID); !errors.Is(err, accountauth.ErrInvalidGrant) {
		t.Fatalf("first AccessToken() = %v, want ErrInvalidGrant", err)
	}
	persisted, err := accounts.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != contract.SubscriptionStatusNeedsReauth ||
		persisted.LastError == nil ||
		persisted.LastError.Code != accountauth.ErrCodeInvalidGrant {
		t.Fatalf("account after invalid_grant = %#v", persisted)
	}
	_, err = manager.AccessToken(ctx, account.ID)
	if err == nil || !strings.Contains(err.Error(), "needs reauthorization") {
		t.Fatalf("later AccessToken() = %v, want needs_reauth failure", err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls.Load())
	}
	for _, secret := range []string{
		"private-error-marker",
		"invalid-access-secret",
		"invalid-refresh-secret",
	} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("later AccessToken() leaked %q: %v", secret, err)
		}
	}
}

func TestFailedAuthorizationRollbackPreservesConcurrentInvalidGrant(t *testing.T) {
	t.Parallel()
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/oauth/token":
			startOnce.Do(func() { close(refreshStarted) })
			<-releaseRefresh
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":"invalid_grant"}`))
		case "/api/accounts/deviceauth/usercode":
			http.Error(writer, "device flow unavailable", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRefresh) })
		issuer.Close()
	})

	ctx := context.Background()
	now := time.Date(2026, 7, 29, 11, 30, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	account := connectedAccount("service_codex_invalid_rollback", now, now, "acct_invalid_rollback")
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
		AccessToken:  "rollback-access-secret",
		RefreshToken: "rollback-refresh-secret",
		AccountID:    account.ProviderAccountID,
		ExpiresAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     issuer.URL,
		HTTPClient: issuer.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		_, err := manager.AccessToken(ctx, account.ID)
		refreshDone <- err
	}()
	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh request did not start")
	}

	authorizationDone := make(chan error, 1)
	go func() {
		_, err := manager.BeginAuthorization(ctx, account.ID, contract.AuthorizationFlowDeviceCode)
		authorizationDone <- err
	}()
	waitForManagerInvalidation(t, manager, account.ID)
	releaseOnce.Do(func() { close(releaseRefresh) })

	if err := <-refreshDone; !errors.Is(err, accountauth.ErrInvalidGrant) {
		t.Fatalf("racing AccessToken() = %v, want ErrInvalidGrant", err)
	}
	if err := <-authorizationDone; !errors.Is(err, accountauth.ErrDeviceCodeRequestFailed) {
		t.Fatalf("BeginAuthorization() = %v, want device-code request failure", err)
	}
	persisted, err := accounts.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != contract.SubscriptionStatusNeedsReauth ||
		persisted.LastError == nil ||
		persisted.LastError.Code != accountauth.ErrCodeInvalidGrant {
		t.Fatalf("rollback lost newer invalid_grant state: %#v", persisted)
	}
	if _, err := manager.AccessToken(ctx, account.ID); err == nil ||
		!strings.Contains(err.Error(), "needs reauthorization") {
		t.Fatalf("later AccessToken() = %v, want needs_reauth failure", err)
	}
}

func TestConcurrentRefreshAndLogoutCannotResurrectCredentials(t *testing.T) {
	t.Parallel()
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startOnce.Do(func() { close(refreshStarted) })
		<-releaseRefresh
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "race-access-new",
			"refresh_token": "race-refresh-new",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRefresh) })
		issuer.Close()
	})

	ctx := context.Background()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	account := connectedAccount("service_codex_logout_race", now, now, "acct_race_123456")
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
		AccessToken:  "race-access-original",
		RefreshToken: "race-refresh-original",
		AccountID:    account.ProviderAccountID,
		ExpiresAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     issuer.URL,
		HTTPClient: issuer.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		_, err := manager.AccessToken(ctx, account.ID)
		refreshDone <- err
	}()
	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh request did not start")
	}

	logoutDone := make(chan error, 1)
	go func() {
		_, err := manager.Logout(ctx, account.ID)
		logoutDone <- err
	}()
	waitForManagerInvalidation(t, manager, account.ID)
	releaseOnce.Do(func() { close(releaseRefresh) })

	if err := <-refreshDone; !errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
		t.Fatalf("racing AccessToken() = %v, want ErrTokenSourceInvalidated", err)
	}
	if err := <-logoutDone; err != nil {
		t.Fatalf("Logout() = %v", err)
	}
	if _, err := credentials.Get(ctx, account.ID); !errors.Is(err, accountauth.ErrCredentialNotFound) {
		t.Fatalf("credential after racing Logout() = %v", err)
	}
	persisted, err := accounts.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != contract.SubscriptionStatusDisconnected ||
		persisted.CredentialRef != "" ||
		persisted.ProviderAccountID != "" {
		t.Fatalf("account after racing Logout() = %#v", persisted)
	}
}

func TestAuthorizationSessionLimitAndDuplicateConflict(t *testing.T) {
	t.Parallel()
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "session-device-secret",
				"user_code":      "SAFE-CODE",
				"interval":       "60",
			})
		case "/api/accounts/deviceauth/token":
			writer.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(issuer.Close)

	ctx := context.Background()
	now := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	ids := []contract.ServiceID{
		"service_auth_limit_01",
		"service_auth_limit_02",
		"service_auth_limit_03",
		"service_auth_limit_04",
	}
	for _, id := range ids {
		account := disconnectedAccount(id, now)
		if err := accounts.PutAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		Issuer:     issuer.URL,
		HTTPClient: issuer.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[:3] {
		if _, err := manager.BeginAuthorization(ctx, id, contract.AuthorizationFlowDeviceCode); err != nil {
			t.Fatalf("BeginAuthorization(%s) = %v", id, err)
		}
		id := id
		t.Cleanup(func() { _, _ = manager.CancelAuthorization(context.Background(), id) })
	}
	if _, err := manager.BeginAuthorization(
		ctx,
		ids[0],
		contract.AuthorizationFlowDeviceCode,
	); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("duplicate BeginAuthorization() = %v, want active-session conflict", err)
	}
	if _, err := manager.BeginAuthorization(
		ctx,
		ids[3],
		contract.AuthorizationFlowDeviceCode,
	); err == nil || !strings.Contains(err.Error(), "too many authorization sessions") {
		t.Fatalf("fourth BeginAuthorization() = %v, want max-three conflict", err)
	}
	fourth, err := accounts.GetAccount(ctx, ids[3])
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Status != contract.SubscriptionStatusDisconnected ||
		fourth.AuthorizationBoundary != manager.AuthorizationBoundary() {
		t.Fatalf("fourth account after rejected authorization = %#v", fourth)
	}
}

func TestMissingInMemorySessionReconcilesPersistedAuthorizingState(t *testing.T) {
	t.Parallel()
	accounts := subscription.NewMemoryAccountStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	account := contract.SubscriptionAccount{
		ID: "service_codex_interrupted", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusAuthorizing, DisplayName: "Interrupted Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(
		accounts,
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{Now: func() time.Time { return now.Add(time.Minute) }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.GetAuthorization(context.Background(), account.ID); ok {
		t.Fatal("GetAuthorization() unexpectedly found an in-memory session")
	}
	reconciled, err := accounts.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != contract.SubscriptionStatusDisconnected ||
		reconciled.LastError == nil ||
		reconciled.LastError.Code != accountauth.ErrCodeSessionInterrupted {
		t.Fatalf("reconciled account = %#v", reconciled)
	}
}

func connectedAccount(
	id contract.ServiceID,
	now time.Time,
	expiresAt time.Time,
	providerAccountID string,
) contract.SubscriptionAccount {
	return contract.SubscriptionAccount{
		ID:                id,
		Provider:          contract.SubscriptionProviderOpenAICodex,
		Status:            contract.SubscriptionStatusConnected,
		DisplayName:       "Codex",
		ProviderAccountID: providerAccountID,
		CredentialRef:     accountauth.CredentialRefFor(id),
		Capabilities:      contract.DefaultOpenAICodexCapabilities(),
		TokenExpiresAt:    timePointer(expiresAt),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func disconnectedAccount(id contract.ServiceID, now time.Time) contract.SubscriptionAccount {
	return contract.SubscriptionAccount{
		ID:           id,
		Provider:     contract.SubscriptionProviderOpenAICodex,
		Status:       contract.SubscriptionStatusDisconnected,
		DisplayName:  "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func waitForManagerInvalidation(
	t *testing.T,
	manager *subscription.Manager,
	id contract.SubscriptionAccountID,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		_, err := manager.AccessToken(ctx, id)
		cancel()
		if errors.Is(err, accountauth.ErrTokenSourceInvalidated) {
			return
		}
		if !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) {
			t.Fatalf("AccessToken() while waiting for invalidation = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("manager did not enter logout transition: %v", err)
		}
	}
}

func subscriptionTestPortPair(t *testing.T) (int, int) {
	t.Helper()
	first := subscriptionTestPort(t)
	second := subscriptionTestPort(t)
	for first == second {
		second = subscriptionTestPort(t)
	}
	return first, second
}

func subscriptionTestPort(t *testing.T) int {
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

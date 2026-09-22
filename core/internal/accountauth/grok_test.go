package accountauth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func grokIDToken(subject string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"` + subject + `","email":"user@example.com"}`))
	return header + "." + payload + ".x"
}

func TestGrokDeviceCodeAuthorizationPollsSlowsDownAndPersists(t *testing.T) {
	var polls, refreshes atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
			request.Header.Get("X-Grok-Client-Version") == "" ||
			strings.HasPrefix(request.Header.Get("User-Agent"), "Go-http-client") {
			t.Errorf("missing Grok CLI identity on %s: %v", request.URL.Path, request.Header)
		}
		switch request.URL.Path {
		case "/oauth2/device/code":
			if form.Get("client_id") != accountauth.DefaultGrokClientID || !strings.Contains(form.Get("scope"), "grok-cli:access") {
				http.Error(writer, `{"error":"invalid_client"}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"device_code":               "device-code-secret",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://accounts.x.ai/oauth2/device",
				"verification_uri_complete": "https://accounts.x.ai/oauth2/device?user_code=ABCD-EFGH",
				"expires_in":                600,
				"interval":                  0,
			})
		case "/oauth2/token":
			switch form.Get("grant_type") {
			case "urn:ietf:params:oauth:grant-type:device_code":
				if form.Get("device_code") != "device-code-secret" || form.Get("client_id") != accountauth.DefaultGrokClientID {
					http.Error(writer, `{"error":"invalid_grant"}`, http.StatusBadRequest)
					return
				}
				switch polls.Add(1) {
				case 1:
					writer.WriteHeader(http.StatusBadRequest)
					io.WriteString(writer, `{"error":"authorization_pending"}`)
				case 2:
					writer.WriteHeader(http.StatusBadRequest)
					io.WriteString(writer, `{"error":"slow_down"}`)
				default:
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"access_token":  "grok-access-secret",
						"refresh_token": "grok-refresh-secret",
						"expires_in":    900,
						"id_token":      grokIDToken("user_grok_01"),
					})
				}
			case "refresh_token":
				refreshes.Add(1)
				if form.Get("refresh_token") != "grok-refresh-secret" || form.Get("client_id") != accountauth.DefaultGrokClientID {
					http.Error(writer, `{"error":"invalid_grant"}`, http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"access_token": "grok-rotated-secret",
					"expires_in":   900,
				})
			default:
				http.Error(writer, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer issuer.Close()

	store := accountauth.NewMemoryCredentialStore()
	config := accountauth.OAuthConfig{
		Provider:              contract.SubscriptionProviderXAIGrok,
		Issuer:                issuer.URL,
		HTTPClient:            issuer.Client(),
		DevicePollMinInterval: 5 * time.Millisecond,
		DevicePollMaxInterval: 20 * time.Millisecond,
	}
	manager := accountauth.NewSessionManager(config, store, func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
		if session.Provider != contract.SubscriptionProviderXAIGrok {
			t.Errorf("session provider = %s", session.Provider)
		}
		return store.Put(ctx, session.ServiceID, tokens)
	})
	if _, err := manager.Begin(context.Background(), "service_grok", contract.AuthorizationFlowBrowser); err == nil {
		t.Fatal("browser flow accepted for Grok")
	}
	if _, err := manager.Begin(context.Background(), "service_grok", contract.AuthorizationFlowCode); err == nil {
		t.Fatal("authorization_code flow accepted for Grok")
	}
	session, err := manager.Begin(context.Background(), "service_grok", contract.AuthorizationFlowDeviceCode)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	if session.Provider != contract.SubscriptionProviderXAIGrok || session.Flow != contract.AuthorizationFlowDeviceCode ||
		session.AuthorizationURL != "" || session.DeviceCode == nil || session.DeviceCode.UserCode != "ABCD-EFGH" ||
		session.DeviceCode.VerificationURL != "https://accounts.x.ai/oauth2/device?user_code=ABCD-EFGH" {
		t.Fatalf("device session = %#v", session)
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("session invalid: %v", err)
	}
	waitForSessionStatus(t, manager, "service_grok", contract.AuthorizationSessionStatusCompleted)
	current, _ := manager.Get("service_grok")
	if current.DeviceCode != nil {
		t.Fatalf("terminal session retained login instructions: %#v", current)
	}
	if polls.Load() != 3 {
		t.Fatalf("polls = %d", polls.Load())
	}
	tokens, err := store.Get(context.Background(), "service_grok")
	if err != nil || tokens.AccessToken != "grok-access-secret" || tokens.RefreshToken != "grok-refresh-secret" || tokens.AccountID != "user_grok_01" {
		t.Fatalf("stored tokens = %#v, %v", tokens, err)
	}

	rotated, err := accountauth.NewTokenClient(config).Refresh(context.Background(), tokens.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() = %v", err)
	}
	if rotated.AccessToken != "grok-rotated-secret" || rotated.RefreshToken != "grok-refresh-secret" || refreshes.Load() != 1 {
		t.Fatalf("rotated = %#v refreshes=%d", rotated, refreshes.Load())
	}
}

func TestGrokDeviceCodeDenialAndUnavailableAreSanitized(t *testing.T) {
	for _, mode := range []string{"denied", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/oauth2/device/code":
					if mode == "unavailable" {
						http.NotFound(writer, request)
						return
					}
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"device_code": "device-code-secret", "user_code": "WXYZ-1234",
						"verification_uri": "https://accounts.x.ai/oauth2/device", "expires_in": 600, "interval": 0,
					})
				case "/oauth2/token":
					writer.WriteHeader(http.StatusBadRequest)
					io.WriteString(writer, `{"error":"access_denied","error_description":"device-code-secret rejected"}`)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer issuer.Close()
			manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
				Provider: contract.SubscriptionProviderXAIGrok, Issuer: issuer.URL, HTTPClient: issuer.Client(),
				DevicePollMinInterval: 5 * time.Millisecond, DevicePollMaxInterval: 5 * time.Millisecond,
			}, accountauth.NewMemoryCredentialStore(), nil)
			session, err := manager.Begin(context.Background(), "service_grok", contract.AuthorizationFlowDeviceCode)
			if mode == "unavailable" {
				if err == nil || !strings.Contains(err.Error(), accountauth.ErrDeviceCodeUnavailable.Error()) {
					t.Fatalf("Begin() = %#v, %v", session, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Begin() = %v", err)
			}
			waitForSessionStatus(t, manager, "service_grok", contract.AuthorizationSessionStatusFailed)
			failed, _ := manager.Get("service_grok")
			if failed.Error == nil || failed.Error.Code != accountauth.ErrCodeDeviceCodePoll || strings.Contains(failed.Error.Message, "device-code-secret") {
				t.Fatalf("failed session = %#v", failed)
			}
		})
	}
}

func TestApplyGrokAPIHeadersUsesCLIIdentity(t *testing.T) {
	headers := make(http.Header)
	accountauth.ApplyGrokAPIHeaders(headers, accountauth.AccountTokens{AccessToken: "tok"}, "")
	if headers.Get("Authorization") != "Bearer tok" || headers.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
		headers.Get("X-Grok-Client-Version") != accountauth.DefaultGrokCLIClientVersion ||
		!strings.HasPrefix(headers.Get("User-Agent"), "xai-grok-workspace/") {
		t.Fatalf("headers = %v", headers)
	}
	normalized := accountauth.OAuthConfig{Provider: contract.SubscriptionProviderXAIGrok}.Normalize()
	if normalized.ClientID != accountauth.DefaultGrokClientID || normalized.Issuer != accountauth.DefaultGrokIssuer ||
		normalized.TokenURL != accountauth.DefaultGrokIssuer+"/oauth2/token" || normalized.APIBaseURL != accountauth.DefaultGrokAPIBaseURL {
		t.Fatalf("normalized = %#v", normalized)
	}
}

package accountauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func TestNoLoopbackCallbackSignsCodexInWithADeviceCode(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"device_auth_id": "device-secret-server",
				"user_code":      "SERV-ER01",
				"interval":       "60",
			})
		case "/api/accounts/deviceauth/token":
			writer.Header().Set("Connection", "close")
			writer.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer issuer.Close()
	preferred, fallback := availablePortPair(t)
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Issuer:             issuer.URL,
		HTTPClient:         issuer.Client(),
		PreferredPort:      preferred,
		FallbackPort:       fallback,
		NoLoopbackCallback: true,
	}, accountauth.NewMemoryCredentialStore(), nil)
	session, err := manager.Begin(context.Background(), "service_codex_server", contract.AuthorizationFlowBrowser)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	defer func() { _, _ = manager.Cancel(context.Background(), "service_codex_server") }()
	if session.Flow != contract.AuthorizationFlowDeviceCode || session.DeviceCode == nil ||
		session.DeviceCode.UserCode != "SERV-ER01" || session.AuthorizationURL != "" {
		t.Fatalf("session = %#v, want a device code session", session)
	}
	// Both callback ports stay free: nothing listens on this machine's loopback.
	for _, port := range []int{preferred, fallback} {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("callback port %d is held: %v", port, err)
		}
		_ = listener.Close()
	}
}

func TestNoLoopbackCallbackRefusesAntigravityBrowserSignIn(t *testing.T) {
	manager := accountauth.NewSessionManager(accountauth.OAuthConfig{
		Provider:           contract.SubscriptionProviderAntigravity,
		NoLoopbackCallback: true,
	}, accountauth.NewMemoryCredentialStore(), nil)
	_, err := manager.Begin(context.Background(), "service_antigravity_server", contract.AuthorizationFlowBrowser)
	if !errors.Is(err, accountauth.ErrLoopbackCallbackUnavailable) {
		t.Fatalf("Begin() = %v, want ErrLoopbackCallbackUnavailable", err)
	}
	if _, ok := manager.Get("service_antigravity_server"); ok {
		t.Fatal("a refused sign-in left a session behind")
	}
}

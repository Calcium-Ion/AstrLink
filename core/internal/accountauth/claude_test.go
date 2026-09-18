package accountauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestClaudeCodeCancellationExpiryAndConcurrentExchange(t *testing.T) {
	for _, action := range []string{"cancel", "expire"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var calls, persisted atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				close(entered)
				<-release
				io.WriteString(w, `{"access_token":"access-secret","refresh_token":"refresh-secret","expires_in":3600}`)
			}))
			defer upstream.Close()
			now := time.Now()
			var clock atomic.Int64
			clock.Store(now.UnixNano())
			manager := NewSessionManager(OAuthConfig{Provider: contract.SubscriptionProviderClaudeCode, TokenURL: upstream.URL,
				Now: func() time.Time { return time.Unix(0, clock.Load()) }}, NewMemoryCredentialStore(),
				func(context.Context, contract.AuthorizationSession, AccountTokens) error {
					persisted.Add(1)
					return nil
				})
			session, err := manager.Begin(context.Background(), "service_claude", contract.AuthorizationFlowCode)
			if err != nil {
				t.Fatal(err)
			}
			authorize, _ := url.Parse(session.AuthorizationURL)
			code := "secret#" + authorize.Query().Get("state")
			done := make(chan error, 1)
			go func() {
				_, err := manager.CompleteCode(context.Background(), "service_claude", session.ID, code)
				done <- err
			}()
			<-entered
			if _, err := manager.CompleteCode(context.Background(), "service_claude", session.ID, code); err == nil {
				t.Error("accepted concurrent code exchange")
			}
			if _, err := manager.CompleteCode(context.Background(), "service_other", session.ID, code); err == nil {
				t.Error("accepted code for another service")
			}
			if action == "cancel" {
				if _, err := manager.Cancel(context.Background(), "service_claude"); err != nil {
					t.Error(err)
				}
			} else {
				clock.Store(now.Add(time.Hour).UnixNano())
			}
			close(release)
			if err := <-done; err == nil {
				t.Error("late exchange completed")
			}
			if persisted.Load() != 0 || calls.Load() != 1 {
				t.Fatal("late credentials were persisted or exchange duplicated")
			}
			terminal, _ := manager.Get("service_claude")
			if terminal.Status == contract.AuthorizationSessionStatusPending || terminal.AuthorizationURL != "" {
				t.Fatal("terminal session retained login instructions")
			}
		})
	}
}

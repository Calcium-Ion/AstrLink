package vertexauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
)

type tokenTransport func(*http.Request) (*http.Response, error)

func (f tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testAccount(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := json.MarshalIndent(map[string]string{
		"type": "service_account", "project_id": "vertex-test", "client_email": "test@vertex-test.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": tokenURL,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func tokenResponse(index int) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"access_token":"token-%d","token_type":"Bearer","expires_in":3600}`, index)))}
}

func TestCredentialValidationAndCompaction(t *testing.T) {
	secret := testAccount(t)
	compact, err := Compact(secret)
	if err != nil || strings.ContainsAny(string(compact), "\r\n") || !json.Valid(compact) {
		t.Fatalf("Compact: %v", err)
	}
	var account map[string]string
	_ = json.Unmarshal(secret, &account)
	for name, value := range map[string]string{"type": "authorized_user", "project_id": "", "client_email": "", "private_key": "bad", "token_uri": "https://attacker.example/token", "universe_domain": "attacker.example"} {
		t.Run(name, func(t *testing.T) {
			copy := make(map[string]string)
			for k, v := range account {
				copy[k] = v
			}
			copy[name] = value
			bad, _ := json.Marshal(copy)
			if _, err := Compact(bad); !errors.Is(err, ErrCredential) {
				t.Fatalf("accepted invalid %s", name)
			}
		})
	}
	for _, bad := range [][]byte{nil, []byte(`{`), []byte(`null`), []byte(strings.Repeat(" ", 16_385))} {
		if _, err := Compact(bad); !errors.Is(err, ErrCredential) {
			t.Fatal("accepted invalid JSON")
		}
	}
}

func TestTokensCacheRefreshAndCredentialReplacement(t *testing.T) {
	secret := testAccount(t)
	var calls atomic.Int32
	manager := New(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != tokenURL || r.Method != http.MethodPost {
			t.Errorf("token target = %s %s", r.Method, r.URL)
		}
		if networkproxy.Binding(r.Context()) == "" {
			t.Error("lost proxy context")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Error("missing signed assertion")
			return nil, ErrCredential
		}
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(payload, &claims)
		if claims["aud"] != tokenURL || claims["scope"] != cloudPlatformScope {
			t.Errorf("unexpected assertion claims")
		}
		if r.Header.Get("X-AstrLink-Test") != "" {
			t.Error("gateway header in token request")
		}
		return tokenResponse(int(calls.Add(1))), nil
	})})
	ctx, err := networkproxy.BindConfig(context.Background(), "service_vertex", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		token, err := manager.AccessToken(ctx, "service_vertex", secret)
		if err != nil || token != "token-1" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	manager.now = func() time.Time { return time.Now().Add(59*time.Minute + 30*time.Second) }
	if token, err := manager.AccessToken(ctx, "service_vertex", secret); err != nil || token != "token-2" {
		t.Fatalf("refresh=%q %v", token, err)
	}
	manager.now = time.Now
	replacement := []byte(strings.Replace(string(secret), "test@vertex-test", "new@vertex-test", 1))
	if token, err := manager.AccessToken(ctx, "service_vertex", replacement); err != nil || token != "token-3" {
		t.Fatalf("replacement=%q %v", token, err)
	}
	if _, err := manager.AccessToken(ctx, "service_vertex", nil); !errors.Is(err, ErrCredential) {
		t.Fatal("missing credential reused cache")
	}
	if calls.Load() != 3 {
		t.Fatalf("exchanges=%d", calls.Load())
	}
}

func TestConcurrentRefreshAndCancelledWaiter(t *testing.T) {
	secret := testAccount(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	manager := New(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return tokenResponse(1), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var group sync.WaitGroup
	finished := make(chan struct{})
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			token, err := manager.AccessToken(ctx, "service_vertex", secret)
			if err != nil || token != "token-1" {
				t.Errorf("token=%q err=%v", token, err)
			}
		}()
	}
	go func() { group.Wait(); close(finished) }()
	select {
	case <-entered:
	case <-finished:
		t.Fatal("refresh completed without a token exchange")
	}
	waitCtx, stop := context.WithCancel(ctx)
	defer stop()
	observed := &observedWaitContext{Context: waitCtx, waiting: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() { _, err := manager.AccessToken(observed, "service_vertex", secret); waiterDone <- err }()
	select {
	case <-observed.waiting:
	case err := <-waiterDone:
		t.Fatalf("waiter did not join refresh: %v", err)
	}
	stop()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled waiter: %v", err)
	}
	close(release)
	<-finished
	if calls.Load() != 1 {
		t.Fatalf("exchanges=%d", calls.Load())
	}
}

func TestTokenFailuresAreSanitizedAndCancellable(t *testing.T) {
	secret := testAccount(t)
	manager := New(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"sensitive-private-key"}`))}, nil
	})})
	_, err := manager.AccessToken(context.Background(), "service_vertex", secret)
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unsanitized error: %v", err)
	}
	entered := make(chan struct{})
	manager = New(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("token exchange has no deadline")
		}
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := manager.AccessToken(ctx, "service_vertex", secret); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("refresh completed before cancellation: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh: %v", err)
	}
}

type observedWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *observedWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestTokenExchangeHonorsClientTimeout(t *testing.T) {
	manager := New(&http.Client{Timeout: time.Millisecond, Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	if _, err := manager.AccessToken(context.Background(), "service_vertex", testAccount(t)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

// A controlled deadline lets callers expire after every waiter has joined.
type refreshCallerContext struct {
	context.Context
	done chan struct{}
	err  error
}

func (ctx refreshCallerContext) Done() <-chan struct{} { return ctx.done }
func (ctx refreshCallerContext) Err() error {
	select {
	case <-ctx.done:
		return ctx.err
	default:
		return nil
	}
}

func TestCancelledRefreshCallerDoesNotFailLiveWaiters(t *testing.T) {
	secret := testAccount(t)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			entered, refreshed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			manager := New(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
				switch calls.Add(1) {
				case 1:
					close(entered)
					<-r.Context().Done()
					return nil, r.Context().Err()
				case 2:
					close(refreshed)
				}
				select {
				case <-release:
					return tokenResponse(2), nil
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			})})
			ctx := refreshCallerContext{Context: context.Background(), done: make(chan struct{}), err: cause}
			cancel := sync.OnceFunc(func() { close(ctx.done) })
			defer cancel()
			leaderDone := make(chan error, 1)
			go func() { _, err := manager.AccessToken(ctx, "service_vertex", secret); leaderDone <- err }()
			select {
			case <-entered:
			case err := <-leaderDone:
				t.Fatalf("refresh ended before exchange: %v", err)
			}
			waitCtx, stop := context.WithCancel(context.Background())
			defer stop()
			const waiters = 4
			waiterDone := make(chan error, waiters)
			for range waiters {
				observed := &observedWaitContext{Context: waitCtx, waiting: make(chan struct{})}
				go func() {
					token, err := manager.AccessToken(observed, "service_vertex", secret)
					if err == nil && token != "token-2" {
						err = fmt.Errorf("unexpected token %q", token)
					}
					waiterDone <- err
				}()
				select {
				case <-observed.waiting:
				case err := <-waiterDone:
					t.Fatalf("waiter did not join refresh: %v", err)
				}
			}
			cancel()
			if err := <-leaderDone; !errors.Is(err, cause) {
				t.Fatalf("leader error=%v", err)
			}
			select {
			case <-refreshed:
			case err := <-waiterDone:
				t.Fatalf("live waiter failed before retry: %v (own context=%v)", err, waitCtx.Err())
			}
			close(release)
			for range waiters {
				if err := <-waiterDone; err != nil {
					t.Errorf("live waiter: %v", err)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("exchanges=%d", calls.Load())
			}
		})
	}
}

func TestSharedTokenExchangeFailureIsNotRetried(t *testing.T) {
	secret := testAccount(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	manager := New(&http.Client{Transport: tokenTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil, context.DeadlineExceeded
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leaderDone, waiterDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := manager.AccessToken(ctx, "service_vertex", secret); leaderDone <- err }()
	select {
	case <-entered:
	case err := <-leaderDone:
		t.Fatalf("exchange did not start: %v", err)
	}
	observed := &observedWaitContext{Context: ctx, waiting: make(chan struct{})}
	go func() { _, err := manager.AccessToken(observed, "service_vertex", secret); waiterDone <- err }()
	select {
	case <-observed.waiting:
	case err := <-waiterDone:
		t.Fatalf("waiter did not join: %v", err)
	}
	close(release)
	leaderErr, waiterErr := <-leaderDone, <-waiterDone
	if leaderErr == nil || waiterErr == nil {
		t.Fatal("exchange failure was lost")
	}
	if calls.Load() != 1 {
		t.Fatalf("retried terminal exchange failure: %d calls", calls.Load())
	}
}

package vertexauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"golang.org/x/oauth2"
)

type refresh struct {
	done            chan struct{}
	token           *oauth2.Token
	err             error
	callerCancelled bool
}

type cacheEntry struct {
	key    [32]byte
	token  *oauth2.Token
	flight *refresh
}

type Manager struct {
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	entries map[contract.ServiceID]*cacheEntry
}

func New(client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{}
	}
	client = networkproxy.WrapClient(client)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Manager{client: client, now: time.Now, entries: make(map[contract.ServiceID]*cacheEntry)}
}

// AccessToken receives freshly loaded credentials, so replacement and deletion
// take effect even while an earlier token would still be valid.
func (manager *Manager) AccessToken(ctx context.Context, id contract.ServiceID, secret []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if manager == nil || len(secret) == 0 {
		return "", ErrCredential
	}
	hash := sha256.New()
	hash.Write(secret)
	hash.Write([]byte(networkproxy.Binding(ctx)))
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		manager.mu.Lock()
		entry := manager.entries[id]
		if entry == nil || entry.key != key {
			if entry == nil && len(manager.entries) >= 128 {
				for oldID := range manager.entries {
					delete(manager.entries, oldID)
					break
				}
			}
			entry = &cacheEntry{key: key}
			manager.entries[id] = entry
		}
		if entry.token != nil && entry.token.Expiry.After(manager.now().Add(time.Minute)) {
			token := entry.token.AccessToken
			manager.mu.Unlock()
			return token, nil
		}
		if flight := entry.flight; flight != nil {
			manager.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-flight.done:
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if flight.callerCancelled {
					continue
				}
				if flight.err != nil {
					return "", flight.err
				}
				return flight.token.AccessToken, nil
			}
		}
		flight := &refresh{done: make(chan struct{})}
		entry.flight = flight
		manager.mu.Unlock()

		flight.token, flight.err = manager.exchange(ctx, secret)
		// Only another caller's cancellation warrants retrying a shared exchange.
		flight.callerCancelled = ctx.Err() != nil && errors.Is(flight.err, ctx.Err())
		manager.mu.Lock()
		if flight.err == nil {
			entry.token = flight.token
		}
		entry.flight = nil
		close(flight.done)
		manager.mu.Unlock()
		if flight.err != nil {
			return "", flight.err
		}
		return flight.token.AccessToken, nil
	}
}

func (manager *Manager) exchange(ctx context.Context, secret []byte) (*oauth2.Token, error) {
	configuration, err := config(secret)
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Second
	if manager.client.Timeout > 0 && manager.client.Timeout < timeout {
		timeout = manager.client.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := *manager.client
	client.Timeout = 0 // The exchange context enforces the client timeout too.
	// jwt.TokenSource uses PostForm without a request context. Restore the
	// operation's cancellation and proxy snapshot before selecting a transport.
	client.Transport = exchangeTransport{ctx: ctx, base: client.Transport}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &client)
	token, err := configuration.TokenSource(ctx).Token()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		// oauth2 errors may include the response body; never expose it to logs/UI.
		return nil, errors.New("Google service account token exchange failed")
	}
	if token.AccessToken == "" || strings.ContainsAny(token.AccessToken, " \t\r\n") || !token.Expiry.After(manager.now()) {
		return nil, errors.New("Google token response is invalid")
	}
	return token, nil
}

type exchangeTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (transport exchangeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.base.RoundTrip(request.Clone(transport.ctx))
}

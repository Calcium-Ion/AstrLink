package networkproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type snapshotKey struct{}
type snapshot struct {
	service contract.ServiceID
	mode    string
	proxy   *url.URL
	key     string
}

// Bind freezes the effective instance configuration, including credentials, for
// an operation. A login keeps this context through polling and callback exchange.
func Bind(ctx context.Context, service contract.Service, secrets secretstore.SecretStore) (context.Context, error) {
	if bound, ok := ctx.Value(snapshotKey{}).(snapshot); ok && bound.service == service.ID {
		return ctx, nil
	}
	if service.ID == "" {
		return ctx, nil
	}
	var credential *contract.ProxyCredential
	if service.Proxy != nil && service.Proxy.CredentialRef != "" {
		if secrets == nil {
			return ctx, errors.New("instance proxy credential unavailable")
		}
		value, err := secrets.Get(ctx, secretstore.Ref(service.Proxy.CredentialRef))
		if err != nil {
			return ctx, errors.New("instance proxy credential unavailable")
		}
		defer clear(value)
		credential = &contract.ProxyCredential{}
		if json.Unmarshal(value, credential) != nil || credential.Validate() != nil {
			return ctx, errors.New("invalid instance proxy credential")
		}
	}
	return BindConfig(ctx, service.ID, service.Proxy, credential)
}

// BindConfig also supports an unsaved model probe, without persisting its secrets.
func BindConfig(ctx context.Context, id contract.ServiceID, config *contract.ServiceProxy, credential *contract.ProxyCredential) (context.Context, error) {
	if err := config.Validate(id); err != nil {
		return ctx, err
	}
	s := snapshot{service: id, mode: "inherit"}
	if config != nil {
		s.mode = config.Mode
		if config.Mode == "custom" {
			s.proxy, _ = url.Parse(config.URL)
			if credential != nil {
				if err := credential.Validate(); err != nil {
					return ctx, err
				}
				s.proxy.User = url.UserPassword(credential.Username, credential.Password)
			} else if config.CredentialRef != "" {
				return ctx, errors.New("instance proxy credential unavailable")
			}
		}
	}
	material := string(id) + "\x00" + s.mode
	if s.proxy != nil {
		material += "\x00" + s.proxy.String()
	}
	hash := sha256.Sum256([]byte(material))
	s.key = hex.EncodeToString(hash[:])
	return context.WithValue(ctx, snapshotKey{}, s), nil
}

type ServiceReader interface {
	GetService(context.Context, contract.ServiceID) (storage.ServiceRecord, error)
}

// Resolver returns a fresh snapshot, unless this operation already bound the instance.
func Resolver(services ServiceReader, secrets secretstore.SecretStore) func(context.Context, contract.ServiceID) (context.Context, error) {
	return func(ctx context.Context, id contract.ServiceID) (context.Context, error) {
		if s, ok := ctx.Value(snapshotKey{}).(snapshot); ok && s.service == id {
			return ctx, nil
		}
		record, err := services.GetService(ctx, id)
		if err != nil {
			return ctx, errors.New("instance proxy configuration unavailable")
		}
		return Bind(ctx, record.Service, secrets)
	}
}

// Copy preserves only proxy scope, not the initiating request's deadline or cancellation.
func Copy(ctx, source context.Context) context.Context {
	if source == nil {
		return ctx
	}
	if s, ok := source.Value(snapshotKey{}).(snapshot); ok {
		return context.WithValue(ctx, snapshotKey{}, s)
	}
	return ctx
}

func Binding(ctx context.Context) string {
	if s, ok := ctx.Value(snapshotKey{}).(snapshot); ok {
		return s.key
	}
	return ""
}

type poolEntry struct {
	key       string
	transport *http.Transport
}
type scopedTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	pools map[contract.ServiceID]poolEntry
}

// WrapTransport never mutates the supplied transport. Each instance/configuration
// owns a pool, so HTTP/2 and keepalive cannot retain another instance's exit.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*scopedTransport); ok {
		return base
	}
	return &scopedTransport{base: base, pools: make(map[contract.ServiceID]poolEntry)}
}

func WrapClient(client *http.Client) *http.Client {
	copy := *client
	copy.Transport = WrapTransport(client.Transport)
	return &copy
}

func (t *scopedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s, ok := req.Context().Value(snapshotKey{}).(snapshot)
	if !ok {
		return t.base.RoundTrip(req)
	}
	base, ok := t.base.(*http.Transport)
	if !ok {
		// Custom test/instrumentation transports must not silently bypass overrides.
		if s.mode != "inherit" {
			return nil, errors.New("transport does not support instance proxy configuration")
		}
		return t.base.RoundTrip(req)
	}
	t.mu.Lock()
	entry, found := t.pools[s.service]
	if !found || entry.key != s.key {
		if found {
			entry.transport.CloseIdleConnections()
		}
		if !found && len(t.pools) >= 128 {
			for id, old := range t.pools {
				old.transport.CloseIdleConnections()
				delete(t.pools, id)
				break
			}
		}
		configured := base.Clone()
		switch s.mode {
		case "direct":
			configured.Proxy = nil
		case "custom":
			configured.Proxy = http.ProxyURL(s.proxy)
		}
		entry = poolEntry{s.key, configured}
		t.pools[s.service] = entry
	}
	t.mu.Unlock()
	response, err := entry.transport.RoundTrip(req)
	if err != nil && s.mode == "custom" {
		return response, &connectionError{err}
	}
	return response, err
}

func (t *scopedTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, entry := range t.pools {
		entry.transport.CloseIdleConnections()
		delete(t.pools, id)
	}
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type connectionError struct{ cause error }

func (e *connectionError) Error() string { return "instance proxy connection failed" }
func (e *connectionError) Unwrap() error { return e.cause }

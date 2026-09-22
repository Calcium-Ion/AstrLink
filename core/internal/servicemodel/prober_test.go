package servicemodel

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func probeResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestProbeHTTPDecodesCompressedPagesWithinCumulativeLimit(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(fmt.Sprint("oversized=", oversized), func(t *testing.T) {
			calls := 0
			first := `{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`
			second := `{"data":[{"id":"second"}],"has_more":false}`
			if oversized {
				second += strings.Repeat(" ", maxResponseBytes-len(first)-len(second)+1)
			}
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, _ = writer.Write([]byte(second))
			_ = writer.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.Header.Get("Accept-Encoding") != transport.SupportedResponseEncodings {
					t.Errorf("Accept-Encoding = %q", request.Header.Get("Accept-Encoding"))
				}
				if calls == 1 {
					_, _ = io.WriteString(writer, first)
					return
				}
				writer.Header().Set("Content-Encoding", "gzip")
				_, _ = writer.Write(compressed.Bytes())
			}))
			defer upstream.Close()
			models, err := New(nil, nil, nil).ProbeHTTP(context.Background(), "service_test", contract.ServiceKindAnthropic,
				contract.HTTPConnection{BaseURL: upstream.URL, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
				nil, contract.ProtocolOpenAIModels)
			if oversized {
				if !errors.Is(err, ErrUpstream) || !errors.Is(err, transport.ErrResponseBodyTooLarge) {
					t.Fatalf("oversized probe error = %v", err)
				}
			} else if err != nil || strings.Join(models, ",") != "first,second" {
				t.Fatalf("models = %v, err = %v", models, err)
			}
			if calls != 2 {
				t.Fatalf("pages fetched = %d", calls)
			}
		})
	}
}

func TestProbeHTTPReadsOpenAIModelsWithCanonicalURLAndAuth(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.URL.String(), "https://api.example/proxy/v1/models"; got != want {
			t.Fatalf("URL = %q, want %q", got, want)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer draft-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		return probeResponse(`{"data":[{"id":"zeta"},{"id":"alpha"},{"id":"zeta"}]}`), nil
	})}
	prober := New(nil, nil, client)
	models, err := prober.ProbeHTTP(
		context.Background(),
		"service_test",
		contract.ServiceKindOpenAI,
		contract.HTTPConnection{
			BaseURL: "https://api.example/proxy/v1",
			Auth:    contract.ServiceAuth{Scheme: contract.AuthSchemeBearer},
		},
		[]byte("draft-secret"),
		contract.ProtocolOpenAIModels,
	)
	if err != nil {
		t.Fatalf("ProbeHTTP() error = %v", err)
	}
	if got, want := strings.Join(models, ","), "alpha,zeta"; got != want {
		t.Fatalf("models = %q, want %q", got, want)
	}
}

func TestProbeHTTPFollowsAnthropicCursorPagination(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("X-Api-Key") != "anthropic-secret" ||
			request.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Fatalf("headers = %#v", request.Header)
		}
		if request.URL.Query().Get("limit") != "1000" {
			t.Fatalf("query = %q", request.URL.RawQuery)
		}
		if calls == 1 {
			if request.URL.Query().Get("after_id") != "" {
				t.Fatalf("first query = %q", request.URL.RawQuery)
			}
			return probeResponse(`{"data":[{"id":"claude-z"}],"has_more":true,"last_id":"cursor-1"}`), nil
		}
		if request.URL.Query().Get("after_id") != "cursor-1" {
			t.Fatalf("second query = %q", request.URL.RawQuery)
		}
		return probeResponse(`{"data":[{"id":"claude-a"}],"has_more":false,"last_id":"claude-a"}`), nil
	})}
	prober := New(nil, nil, client)
	models, err := prober.ProbeHTTP(
		context.Background(), "service_test", contract.ServiceKindAnthropic,
		contract.HTTPConnection{
			BaseURL: "https://api.anthropic.example",
			Auth:    contract.ServiceAuth{Scheme: contract.AuthSchemeAnthropicAPIKey},
		},
		[]byte("anthropic-secret"), contract.ProtocolOpenAIModels,
	)
	if err != nil {
		t.Fatalf("ProbeHTTP() error = %v", err)
	}
	if calls != 2 || strings.Join(models, ",") != "claude-a,claude-z" {
		t.Fatalf("calls=%d models=%v", calls, models)
	}
}

func TestProbeHTTPFollowsGeminiPaginationAndNormalizesNames(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("X-Goog-Api-Key") != "google-secret" ||
			request.URL.Query().Get("pageSize") != "1000" {
			t.Fatalf("request = %s headers=%#v", request.URL, request.Header)
		}
		if calls == 1 {
			return probeResponse(`{"models":[{"name":"models/gemini-z"}],"nextPageToken":"next"}`), nil
		}
		if request.URL.Query().Get("pageToken") != "next" {
			t.Fatalf("second query = %q", request.URL.RawQuery)
		}
		return probeResponse(`{"models":[{"name":"models/gemini-a"}]}`), nil
	})}
	prober := New(nil, nil, client)
	models, err := prober.ProbeHTTP(
		context.Background(), "service_test", contract.ServiceKindGemini,
		contract.HTTPConnection{
			BaseURL: "https://generativelanguage.example/v1beta",
			Auth:    contract.ServiceAuth{Scheme: contract.AuthSchemeGoogleAPIKey},
		},
		[]byte("google-secret"), contract.ProtocolGoogleModels,
	)
	if err != nil {
		t.Fatalf("ProbeHTTP() error = %v", err)
	}
	if calls != 2 || strings.Join(models, ",") != "gemini-a,gemini-z" {
		t.Fatalf("calls=%d models=%v", calls, models)
	}
}

func TestProbeHTTPRejectsMalformedAndRepeatedPaginationResponses(t *testing.T) {
	t.Run("malformed response", func(t *testing.T) {
		prober := New(nil, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return probeResponse(`{"data":{}}`), nil
		})})
		_, err := prober.ProbeHTTP(
			context.Background(), "service_test", contract.ServiceKindOpenAI,
			contract.HTTPConnection{BaseURL: "https://api.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
			nil, contract.ProtocolOpenAIModels,
		)
		if !errors.Is(err, ErrUpstream) {
			t.Fatalf("ProbeHTTP() error = %v, want ErrUpstream", err)
		}
	})

	t.Run("repeated Gemini page token", func(t *testing.T) {
		calls := 0
		prober := New(nil, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return probeResponse(`{"models":[{"name":"models/gemini"}],"nextPageToken":"repeat"}`), nil
		})})
		_, err := prober.ProbeHTTP(
			context.Background(), "service_test", contract.ServiceKindGemini,
			contract.HTTPConnection{BaseURL: "https://api.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
			nil, contract.ProtocolGoogleModels,
		)
		if calls != 2 || !errors.Is(err, ErrUpstream) {
			t.Fatalf("calls=%d error=%v, want two calls and ErrUpstream", calls, err)
		}
	})
}

func TestProbeHTTPPropagatesDeadlineAndCapsModelCount(t *testing.T) {
	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	prober := New(nil, nil, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})})
	_, err := prober.ProbeHTTP(
		deadline, "service_test", contract.ServiceKindOpenAI,
		contract.HTTPConnection{BaseURL: "https://api.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
		nil, contract.ProtocolOpenAIModels,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProbeHTTP() error = %v, want deadline exceeded", err)
	}

	tooMany := make([]string, maxProbeModelIDs+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("model-%05d", index)
	}
	if _, err := normalizeProbeIDs(tooMany); !errors.Is(err, ErrUpstream) {
		t.Fatalf("normalizeProbeIDs() error = %v, want ErrUpstream", err)
	}
}

func TestProbeServiceUsesConnectedCodexAccountAndRejectsMalformedResponse(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	responseBody := `{"models":[{"slug":"gpt-z","visibility":"list"},{"slug":"gpt-a","visibility":"list"},{"slug":"gpt-6-astra","visibility":"list","supported_in_api":false}]}`
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		wantURL := "https://codex.example/backend-api/codex/models?client_version=" +
			accountauth.DefaultCodexModelsClientVersion
		if request.URL.String() != wantURL {
			t.Fatalf("URL = %q, want %q", request.URL.String(), wantURL)
		}
		if request.Header.Get("Authorization") != "Bearer codex-access" ||
			request.Header.Get("ChatGPT-Account-ID") != "acct_codex" ||
			request.Header.Get("OAI-Product-Sku") != "codex" ||
			request.Header.Get("originator") != accountauth.DefaultCodexOriginator ||
			request.Header.Get("User-Agent") != accountauth.CodexUserAgent("") ||
			request.Header.Get("version") != accountauth.DefaultCodexModelsClientVersion ||
			request.Header.Get("Accept") != "application/json" {
			t.Fatalf("headers = %#v", request.Header)
		}
		return probeResponse(responseBody), nil
	})}
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		credentials,
		accountauth.OAuthConfig{
			ClientID:   "astrlink_model_probe_test",
			APIBaseURL: "https://codex.example/backend-api/codex",
			HTTPClient: client,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	service := contract.Service{
		ID: "service_codex_probe", Name: "Codex", Kind: contract.ServiceKindCodexSubscription,
		Enabled: true, Models: []string{}, Capabilities: contract.DefaultOpenAICodexCapabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusConnected,
			ProviderAccountID: "acct_codex", CredentialRef: accountauth.CredentialRefFor("service_codex_probe"),
			TokenExpiresAt: &expiresAt, LastRefreshAt: &now,
		},
	}
	if _, err := store.CreateService(context.Background(), service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(context.Background(), service.ID, accountauth.AccountTokens{
		AccessToken: "codex-access", RefreshToken: "codex-refresh",
		AccountID: "acct_codex", ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	prober := New(store, manager, nil)
	models, err := prober.ProbeService(context.Background(), service, contract.ProtocolOpenAIModels)
	if err != nil || strings.Join(models, ",") != "gpt-6-astra,gpt-a,gpt-z" {
		t.Fatalf("models=%v err=%v", models, err)
	}
	responseBody = `{"models":[]}`
	empty, err := prober.ProbeService(context.Background(), service, contract.ProtocolOpenAIModels)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty Codex catalog = %v err=%v", empty, err)
	}
	responseBody = `{}`
	if _, err := prober.ProbeService(context.Background(), service, contract.ProtocolOpenAIModels); !errors.Is(err, ErrUpstream) {
		t.Fatalf("malformed Codex response error = %v, want ErrUpstream", err)
	}
}

func TestPayAsYouGoModelDiscovery(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindDeepSeek, contract.ServiceKindMoonshot, contract.ServiceKindMiniMax, contract.ServiceKindXAI} {
		t.Run(string(kind), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer api-key" {
					t.Fatalf("wrong discovery request: %s", r.URL)
				}
				return probeResponse(`{"data":[{"id":"model-test"}]}`), nil
			})}
			models, err := New(nil, nil, client).ProbeHTTP(context.Background(), "service_api", kind, contract.HTTPConnection{BaseURL: "https://api.example/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}}, []byte("api-key"), contract.ProtocolOpenAIModels)
			if err != nil || len(models) != 1 || models[0] != "model-test" {
				t.Fatalf("models=%v err=%v", models, err)
			}
		})
	}
	for _, kind := range []contract.ServiceKind{contract.ServiceKindQwen, contract.ServiceKindGLM, contract.ServiceKindDoubao} {
		if kindSupportsDiscovery(kind, contract.ProtocolOpenAIModels) {
			t.Fatalf("advertised unverified discovery for %s", kind)
		}
	}
}

func TestPayAsYouGoDiscoveryFromAnthropicSDKRoot(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindDeepSeek, contract.ServiceKindMoonshot, contract.ServiceKindMiniMax} {
		t.Run(string(kind), func(t *testing.T) {
			called := false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if r.URL.String() != "https://proxy.example/tenant%2Fone/v1/models" || r.Header.Get("Authorization") != "Bearer api-key" {
					t.Fatalf("wrong discovery request: %s", r.URL)
				}
				return probeResponse(`{"data":[{"id":"model-test"}]}`), nil
			})}
			models, err := New(nil, nil, client).ProbeHTTP(context.Background(), "service_api", kind, contract.HTTPConnection{
				BaseURL: "https://proxy.example/tenant%2Fone/anthropic/v1/", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer},
			}, []byte("api-key"), contract.ProtocolOpenAIModels)
			if !called || err != nil || len(models) != 1 || models[0] != "model-test" {
				t.Fatalf("called=%t models=%v err=%v", called, models, err)
			}
		})
	}
}

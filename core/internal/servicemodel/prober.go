// Package servicemodel discovers upstream model identifiers for configured
// and draft services without persisting probe state or credential material.
package servicemodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const (
	probeTimeout      = 60 * time.Second
	maxResponseBytes  = 8 << 20
	maxProbePages     = 20
	maxProbeModelIDs  = 10_000
	providerPageLimit = 1000
)

var (
	ErrUnsupported           = errors.New("model discovery protocol is unsupported")
	ErrCredentialUnavailable = errors.New("model discovery credential is unavailable")
	ErrNotConnected          = errors.New("subscription service is not connected")
	ErrUpstream              = errors.New("upstream model discovery failed")
)

type Prober struct {
	secrets       secretstore.SecretStore
	subscriptions *subscription.Manager
	client        *http.Client
}

func New(secrets secretstore.SecretStore, subscriptions *subscription.Manager, client *http.Client) *Prober {
	if client == nil {
		transportCopy := http.DefaultTransport
		if defaults, ok := http.DefaultTransport.(*http.Transport); ok {
			configured := defaults.Clone()
			configured.DisableCompression = true
			configured.ResponseHeaderTimeout = probeTimeout
			transportCopy = configured
		}
		client = &http.Client{Transport: transportCopy}
	}
	return &Prober{secrets: secrets, subscriptions: subscriptions, client: client}
}

func (prober *Prober) ProbeService(
	ctx context.Context,
	service contract.Service,
	protocol contract.ProtocolID,
) ([]string, error) {
	if !serviceSupportsDiscovery(service, protocol) {
		return nil, ErrUnsupported
	}
	if service.Kind.IsSubscription() {
		return prober.probeSubscription(ctx, service.ID, protocol)
	}
	if service.HTTP == nil {
		return nil, ErrUnsupported
	}
	return prober.ProbeHTTP(ctx, service.ID, service.Kind, *service.HTTP, nil, protocol)
}

func (prober *Prober) ProbeHTTP(
	ctx context.Context,
	serviceID contract.ServiceID,
	kind contract.ServiceKind,
	connection contract.HTTPConnection,
	credential []byte,
	protocol contract.ProtocolID,
) ([]string, error) {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if !kind.IsHTTP() || !kindSupportsDiscovery(kind, protocol) {
		return nil, ErrUnsupported
	}
	if err := connection.Validate(serviceID); err != nil {
		return nil, fmt.Errorf("%w: invalid HTTP connection", ErrUnsupported)
	}
	secret := append([]byte(nil), credential...)
	defer clear(secret)
	if connection.Auth.Scheme != contract.AuthSchemeNone && len(secret) == 0 {
		if connection.CredentialRef == "" || prober == nil || prober.secrets == nil {
			return nil, ErrCredentialUnavailable
		}
		ref, err := secretstore.ParseRef(connection.CredentialRef)
		if err != nil {
			return nil, ErrCredentialUnavailable
		}
		secret, err = prober.secrets.Get(probeContext, ref)
		if err != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			return nil, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
		}
	}
	headers, err := authorizationHeaders(connection.Auth, secret)
	if err != nil {
		return nil, err
	}
	if kind == contract.ServiceKindAnthropic || kind == contract.ServiceKindKimiCoding || kind == contract.ServiceKindMiniMaxCoding || kind == contract.ServiceKindGLMCoding {
		headers.Set("Anthropic-Version", "2023-06-01")
	}
	return prober.probeHTTPPages(probeContext, connection.BaseURL, headers, protocol, kind == contract.ServiceKindAnthropic)
}

func (prober *Prober) probeSubscription(
	ctx context.Context,
	serviceID contract.ServiceID,
	protocol contract.ProtocolID,
) ([]string, error) {
	if protocol != contract.ProtocolOpenAIModels || prober == nil || prober.subscriptions == nil {
		return nil, ErrUnsupported
	}
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	tokens, err := prober.subscriptions.AccessToken(probeContext, serviceID)
	if err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("%w: %v", ErrNotConnected, err)
	}
	account, err := prober.subscriptions.Get(probeContext, serviceID)
	if err != nil {
		return nil, ErrNotConnected
	}
	if account.Provider == contract.SubscriptionProviderClaudeCode {
		headers := make(http.Header)
		accountauth.ApplyClaudeAPIHeaders(headers, tokens)
		return prober.probeHTTPPages(probeContext, prober.subscriptions.APIBaseURLFor(account.Provider), headers, protocol, true)
	}
	models, err := prober.subscriptions.Provider().ListModels(probeContext, tokens)
	if err != nil {
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	ids := make([]string, 0, len(models.Data))
	for _, model := range models.Data {
		ids = append(ids, model.ID)
	}
	return normalizeProbeIDs(ids)
}

func (prober *Prober) probeHTTPPages(
	ctx context.Context,
	baseURL string,
	headers http.Header,
	protocol contract.ProtocolID,
	anthropic bool,
) ([]string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base URL", ErrUnsupported)
	}
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	path := "/v1/models"
	if protocol == contract.ProtocolGoogleModels {
		path = "/v1beta/models"
	}
	nextToken := ""
	seenTokens := make(map[string]struct{})
	ids := make([]string, 0)
	totalBytes := 0
	for page := 0; page < maxProbePages; page++ {
		incoming := &url.URL{Path: path}
		query := incoming.Query()
		if anthropic {
			query.Set("limit", fmt.Sprint(providerPageLimit))
			if nextToken != "" {
				query.Set("after_id", nextToken)
			}
		} else if protocol == contract.ProtocolGoogleModels {
			query.Set("pageSize", fmt.Sprint(providerPageLimit))
			if nextToken != "" {
				query.Set("pageToken", nextToken)
			}
		}
		incoming.RawQuery = query.Encode()
		target := transport.JoinTargetURL(base, incoming)
		request, requestErr := http.NewRequestWithContext(probeContext, http.MethodGet, target.String(), nil)
		if requestErr != nil {
			return nil, fmt.Errorf("%w: create request", ErrUnsupported)
		}
		request.Header = headers.Clone()
		request.Header.Set("Accept", "application/json")
		response, requestErr := prober.client.Do(request)
		if requestErr != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			return nil, fmt.Errorf("%w: request failed", ErrUpstream)
		}
		remaining := maxResponseBytes - totalBytes
		if remaining <= 0 {
			_ = response.Body.Close()
			return nil, fmt.Errorf("%w: cumulative response exceeds limit", ErrUpstream)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(remaining+1)))
		_ = response.Body.Close()
		if readErr != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			return nil, fmt.Errorf("%w: read response", ErrUpstream)
		}
		if len(body) > remaining {
			return nil, fmt.Errorf("%w: response exceeds limit", ErrUpstream)
		}
		totalBytes += len(body)
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("%w: status %d", ErrUpstream, response.StatusCode)
		}
		pageIDs, token, more, decodeErr := decodePage(body, protocol, anthropic)
		if decodeErr != nil {
			return nil, decodeErr
		}
		ids = append(ids, pageIDs...)
		if len(ids) > maxProbeModelIDs {
			return nil, fmt.Errorf("%w: too many model identifiers", ErrUpstream)
		}
		if !more {
			return normalizeProbeIDs(ids)
		}
		if token == "" {
			return nil, fmt.Errorf("%w: pagination token missing", ErrUpstream)
		}
		if _, repeated := seenTokens[token]; repeated {
			return nil, fmt.Errorf("%w: pagination token repeated", ErrUpstream)
		}
		seenTokens[token] = struct{}{}
		nextToken = token
	}
	return nil, fmt.Errorf("%w: pagination limit exceeded", ErrUpstream)
}

func decodePage(body []byte, protocol contract.ProtocolID, anthropic bool) ([]string, string, bool, error) {
	if protocol == contract.ProtocolGoogleModels {
		var page struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil || page.Models == nil {
			return nil, "", false, fmt.Errorf("%w: invalid Gemini response", ErrUpstream)
		}
		ids := make([]string, 0, len(page.Models))
		for _, model := range page.Models {
			ids = append(ids, strings.TrimPrefix(model.Name, "models/"))
		}
		return ids, page.NextPageToken, page.NextPageToken != "", nil
	}
	var page struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(body, &page); err != nil || page.Data == nil {
		return nil, "", false, fmt.Errorf("%w: invalid model response", ErrUpstream)
	}
	ids := make([]string, 0, len(page.Data))
	for _, model := range page.Data {
		ids = append(ids, model.ID)
	}
	return ids, page.LastID, anthropic && page.HasMore, nil
}

func normalizeProbeIDs(ids []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || utf8.RuneCountInString(id) > 256 {
			return nil, fmt.Errorf("%w: invalid model identifier", ErrUpstream)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
		if len(result) > maxProbeModelIDs {
			return nil, fmt.Errorf("%w: too many model identifiers", ErrUpstream)
		}
	}
	sort.Strings(result)
	return result, nil
}

func serviceSupportsDiscovery(service contract.Service, protocol contract.ProtocolID) bool {
	if service.Kind.IsSubscription() && protocol != contract.ProtocolOpenAIModels {
		return false
	}
	for _, capability := range service.Capabilities {
		if capability.Protocol == protocol {
			return true
		}
	}
	return false
}

func kindSupportsDiscovery(kind contract.ServiceKind, protocol contract.ProtocolID) bool {
	switch kind {
	case contract.ServiceKindNewAPI, contract.ServiceKindCustom:
		return protocol == contract.ProtocolOpenAIModels || protocol == contract.ProtocolGoogleModels
	case contract.ServiceKindGemini:
		return protocol == contract.ProtocolGoogleModels
	case contract.ServiceKindOpenAI, contract.ServiceKindOpenAICompatible, contract.ServiceKindAnthropic,
		contract.ServiceKindOpenCodeGo, contract.ServiceKindOpenCodeZen, contract.ServiceKindKimiCoding,
		contract.ServiceKindGLMCoding, contract.ServiceKindMiniMaxCoding:
		return protocol == contract.ProtocolOpenAIModels
	default:
		return false
	}
}

func authorizationHeaders(auth contract.ServiceAuth, secret []byte) (http.Header, error) {
	headers := make(http.Header)
	if auth.Scheme == contract.AuthSchemeNone {
		return headers, nil
	}
	if len(secret) == 0 {
		return nil, ErrCredentialUnavailable
	}
	value := string(secret)
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
		return nil, ErrCredentialUnavailable
	}
	switch auth.Scheme {
	case contract.AuthSchemeBearer:
		headers.Set("Authorization", "Bearer "+value)
	case contract.AuthSchemeAnthropicAPIKey:
		headers.Set("X-Api-Key", value)
	case contract.AuthSchemeGoogleAPIKey:
		headers.Set("X-Goog-Api-Key", value)
	case contract.AuthSchemeCustomHeader:
		headers.Set(auth.HeaderName, value)
	default:
		return nil, ErrUnsupported
	}
	return headers, nil
}

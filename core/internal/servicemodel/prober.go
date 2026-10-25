// Package servicemodel discovers upstream model identifiers for configured
// and draft services without persisting probe state or credential material.
package servicemodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/QuantumNous/astrlink/core/internal/vertexauth"
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

// Discovery is one model-list probe: the model ids, sorted, and the format
// the list names for each model it serves in exactly one.
type Discovery struct {
	Models    []string
	Protocols []contract.ModelProtocol
}

type Prober struct {
	secrets       secretstore.SecretStore
	subscriptions *subscription.Manager
	client        *http.Client
	vertex        *vertexauth.Manager
}

func New(secrets secretstore.SecretStore, subscriptions *subscription.Manager, client *http.Client) *Prober {
	if client == nil {
		transportCopy := http.DefaultTransport
		if defaults, ok := networkproxy.BaseTransport(http.DefaultTransport); ok {
			configured := defaults.Clone()
			configured.DisableCompression = true
			configured.ResponseHeaderTimeout = probeTimeout
			transportCopy = configured
		}
		client = &http.Client{Transport: transportCopy}
	}
	return &Prober{secrets: secrets, subscriptions: subscriptions, client: networkproxy.WrapClient(client), vertex: vertexauth.New(client)}
}

func (prober *Prober) WithVertexTokens(tokens *vertexauth.Manager) *Prober {
	prober.vertex = tokens
	return prober
}

func (prober *Prober) ProbeService(
	ctx context.Context,
	service contract.Service,
	protocol contract.ProtocolID,
) (Discovery, error) {
	var err error
	ctx, err = networkproxy.Bind(ctx, service, prober.secrets)
	if err != nil {
		return Discovery{}, err
	}
	if !serviceSupportsDiscovery(service, protocol) {
		return Discovery{}, ErrUnsupported
	}
	if service.Kind.IsSubscription() {
		return listed(prober.probeSubscription(ctx, service.ID, protocol))
	}
	if service.HTTP == nil {
		return Discovery{}, ErrUnsupported
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
) (Discovery, error) {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if !kind.IsHTTP() || !kindSupportsDiscovery(kind, protocol) {
		return Discovery{}, ErrUnsupported
	}
	if err := connection.Validate(serviceID); err != nil {
		return Discovery{}, fmt.Errorf("%w: invalid HTTP connection", ErrUnsupported)
	}
	if (kind == contract.ServiceKindVertex) != (connection.Vertex != nil) {
		return Discovery{}, ErrUnsupported
	}
	secret := append([]byte(nil), credential...)
	defer func() { clear(secret) }()
	if connection.Auth.Scheme != contract.AuthSchemeNone && len(secret) == 0 {
		if connection.CredentialRef == "" || prober == nil || prober.secrets == nil {
			return Discovery{}, ErrCredentialUnavailable
		}
		ref, err := secretstore.ParseRef(connection.CredentialRef)
		if err != nil {
			return Discovery{}, ErrCredentialUnavailable
		}
		secret, err = prober.secrets.Get(probeContext, ref)
		if err != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return Discovery{}, context.DeadlineExceeded
			}
			return Discovery{}, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
		}
	}
	if kind == contract.ServiceKindVertex {
		token, err := prober.vertex.AccessToken(probeContext, serviceID, secret)
		if err != nil {
			return Discovery{}, fmt.Errorf("%w: %w", ErrCredentialUnavailable, err)
		}
		return prober.probeVertex(probeContext, connection, token)
	}
	headers, err := authorizationHeaders(providerapi.Auth(kind, protocol, connection.Auth), secret)
	if err != nil {
		return Discovery{}, err
	}
	if kind == contract.ServiceKindAnthropic || kind == contract.ServiceKindKimiCoding || kind == contract.ServiceKindMiniMaxCoding || kind == contract.ServiceKindGLMCoding {
		headers.Set("Anthropic-Version", "2023-06-01")
	}
	baseURL, err := url.Parse(connection.BaseURL)
	if err != nil {
		return Discovery{}, fmt.Errorf("%w: invalid base URL", ErrUnsupported)
	}
	baseURL = providerapi.BaseURL(kind, protocol, baseURL)
	return prober.probeHTTPPages(probeContext, baseURL.String(), headers, protocol, kind == contract.ServiceKindAnthropic, connection.ModelListPath, false)
}

func (prober *Prober) probeSubscription(
	ctx context.Context,
	serviceID contract.ServiceID,
	protocol contract.ProtocolID,
) ([]string, error) {
	if !protocol.IsModelDiscovery() || prober == nil || prober.subscriptions == nil {
		return nil, ErrUnsupported
	}
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	probeContext, err := prober.subscriptions.ProxyContext(probeContext, serviceID)
	if err != nil {
		return nil, err
	}
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
	if account.Provider == contract.SubscriptionProviderAntigravity {
		return prober.subscriptions.AntigravityModels(probeContext, tokens)
	}
	if protocol != contract.ProtocolOpenAIModels {
		return nil, ErrUnsupported
	}
	if account.Provider == contract.SubscriptionProviderClaudeCode {
		headers := make(http.Header)
		accountauth.ApplyClaudeAPIHeaders(headers, tokens, prober.subscriptions.ClaudeIdentity(probeContext))
		discovery, err := prober.probeHTTPPages(probeContext, prober.subscriptions.APIBaseURLFor(account.Provider), headers, protocol, true, "", false)
		return discovery.Models, err
	}
	if account.Provider == contract.SubscriptionProviderXAIGrok {
		headers := make(http.Header)
		accountauth.ApplyGrokAPIHeaders(headers, tokens, prober.subscriptions.GrokClientVersion(probeContext))
		discovery, err := prober.probeHTTPPages(probeContext, prober.subscriptions.APIBaseURLFor(account.Provider), headers, protocol, false, "", false)
		return discovery.Models, err
	}
	if account.Provider == contract.SubscriptionProviderFactoryDroid {
		// Factory publishes no model endpoint; the list is the one it documents.
		return normalizeProbeIDs(subscription.DroidModels())
	}
	if account.Provider == contract.SubscriptionProviderGitHubCopilot {
		ids, err := prober.subscriptions.CopilotModels(probeContext, tokens)
		if err != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
		}
		return normalizeProbeIDs(ids)
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
	customPath string,
	vertex bool,
) (Discovery, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return Discovery{}, fmt.Errorf("%w: invalid base URL", ErrUnsupported)
	}
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	path := "/v1/models"
	if customPath != "" {
		path = customPath
	} else if protocol == contract.ProtocolGoogleModels {
		path = "/v1beta/models"
	}
	nextToken := ""
	seenTokens := make(map[string]struct{})
	models := make([]listedModel, 0)
	totalBytes := 0
	for page := 0; page < maxProbePages; page++ {
		incoming := &url.URL{Path: path}
		query := incoming.Query()
		if vertex {
			query.Set("listAllVersions", "true")
			query.Set("view", "PUBLISHER_MODEL_VIEW_FULL")
		}
		if anthropic {
			query.Set("limit", fmt.Sprint(providerPageLimit))
			if nextToken != "" {
				query.Set("after_id", nextToken)
			}
		} else if protocol == contract.ProtocolGoogleModels {
			pageSize := providerPageLimit
			if vertex {
				pageSize = 300 // Publisher Models has a lower limit than Gemini Models.
			}
			query.Set("pageSize", fmt.Sprint(pageSize))
			if nextToken != "" {
				query.Set("pageToken", nextToken)
			}
		}
		incoming.RawQuery = query.Encode()
		target := transport.JoinTargetURL(base, incoming)
		request, requestErr := http.NewRequestWithContext(probeContext, http.MethodGet, target.String(), nil)
		if requestErr != nil {
			return Discovery{}, fmt.Errorf("%w: create request", ErrUnsupported)
		}
		request.Header = headers.Clone()
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
		response, requestErr := prober.client.Do(request)
		if requestErr != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return Discovery{}, context.DeadlineExceeded
			}
			return Discovery{}, fmt.Errorf("%w: request failed", ErrUpstream)
		}
		remaining := maxResponseBytes - totalBytes
		if remaining <= 0 {
			_ = response.Body.Close()
			return Discovery{}, fmt.Errorf("%w: cumulative response exceeds limit", ErrUpstream)
		}
		body, readErr := transport.ReadResponseBody(response, int64(remaining))
		_ = response.Body.Close()
		if readErr != nil {
			if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
				return Discovery{}, context.DeadlineExceeded
			}
			return Discovery{}, fmt.Errorf("%w: read response: %w", ErrUpstream, readErr)
		}
		totalBytes += len(body)
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return Discovery{}, fmt.Errorf("%w: status %d", ErrUpstream, response.StatusCode)
		}
		var pageModels []listedModel
		var token string
		var more bool
		var decodeErr error
		if vertex {
			pageModels, token, more, decodeErr = decodeVertexPage(body)
		} else {
			pageModels, token, more, decodeErr = decodePage(body, protocol, anthropic)
		}
		if decodeErr != nil {
			return Discovery{}, decodeErr
		}
		models = append(models, pageModels...)
		if len(models) > maxProbeModelIDs {
			return Discovery{}, fmt.Errorf("%w: too many model identifiers", ErrUpstream)
		}
		if !more {
			return discoveryOf(models)
		}
		if token == "" {
			return Discovery{}, fmt.Errorf("%w: pagination token missing", ErrUpstream)
		}
		if _, repeated := seenTokens[token]; repeated {
			return Discovery{}, fmt.Errorf("%w: pagination token repeated", ErrUpstream)
		}
		seenTokens[token] = struct{}{}
		nextToken = token
	}
	return Discovery{}, fmt.Errorf("%w: pagination limit exceeded", ErrUpstream)
}

// listedModel is one entry of an upstream model list and the format the list
// names for it, if any.
type listedModel struct {
	id     string
	format contract.ProtocolID
}

func decodePage(body []byte, protocol contract.ProtocolID, anthropic bool) ([]listedModel, string, bool, error) {
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
		models := make([]listedModel, 0, len(page.Models))
		for _, model := range page.Models {
			models = append(models, listedModel{id: strings.TrimPrefix(model.Name, "models/")})
		}
		return models, page.NextPageToken, page.NextPageToken != "", nil
	}
	var page struct {
		Data []struct {
			ID                     string          `json:"id"`
			SupportedEndpointTypes json.RawMessage `json:"supported_endpoint_types"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(body, &page); err != nil || page.Data == nil {
		return nil, "", false, fmt.Errorf("%w: invalid model response", ErrUpstream)
	}
	models := make([]listedModel, 0, len(page.Data))
	for _, model := range page.Data {
		models = append(models, listedModel{id: model.ID, format: listedFormat(model.SupportedEndpointTypes)})
	}
	return models, page.LastID, anthropic && page.HasMore, nil
}

// newAPIFormats maps the chat entries of New API's supported_endpoint_types
// to model formats. Its other entries (compaction, search, images,
// embeddings, rerank, video) name no format.
var newAPIFormats = map[string]contract.ProtocolID{
	"openai":          contract.ProtocolOpenAIChat,
	"openai-response": contract.ProtocolOpenAIResponses,
	"anthropic":       contract.ProtocolAnthropicMessages,
	"gemini":          contract.ProtocolGoogleGenerateContent,
}

// listedFormat is the one format a New API model list names for a model, or
// empty when it names several or none. Chat alone pins nothing: New API lists
// an OpenAI channel's models that way yet takes their Responses requests as
// sent, so pinning Chat would only convert those requests.
func listedFormat(raw json.RawMessage) contract.ProtocolID {
	var types []string
	if len(raw) == 0 || json.Unmarshal(raw, &types) != nil {
		return ""
	}
	format := contract.ProtocolID("")
	for _, name := range types {
		next, ok := newAPIFormats[name]
		if !ok || next == format {
			continue
		}
		if format != "" {
			return ""
		}
		format = next
	}
	if format == contract.ProtocolOpenAIChat {
		return ""
	}
	return format
}

// discoveryOf normalizes the listed ids and keeps the format of each model's
// first listing.
func discoveryOf(models []listedModel) (Discovery, error) {
	ids := make([]string, 0, len(models))
	formats := make(map[string]contract.ProtocolID, len(models))
	for _, model := range models {
		ids = append(ids, model.id)
		if _, seen := formats[model.id]; !seen {
			formats[model.id] = model.format
		}
	}
	normalized, err := normalizeProbeIDs(ids)
	if err != nil {
		return Discovery{}, err
	}
	discovery := Discovery{Models: normalized}
	for _, id := range normalized {
		if format := formats[id]; format != "" {
			discovery.Protocols = append(discovery.Protocols, contract.ModelProtocol{Model: id, Protocol: format})
		}
	}
	return discovery, nil
}

// listed wraps a probe that reports model ids only.
func listed(ids []string, err error) (Discovery, error) {
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Models: ids}, nil
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
	case contract.ServiceKindNewAPI, contract.ServiceKindMagpie, contract.ServiceKindCustom:
		return protocol == contract.ProtocolOpenAIModels || protocol == contract.ProtocolGoogleModels
	case contract.ServiceKindVertex:
		return protocol == contract.ProtocolOpenAIModels || protocol == contract.ProtocolGoogleModels
	case contract.ServiceKindGemini:
		return protocol == contract.ProtocolGoogleModels
	case contract.ServiceKindOpenAI, contract.ServiceKindOpenAICompatible, contract.ServiceKindAnthropic,
		contract.ServiceKindOpenCodeGo, contract.ServiceKindOpenCodeZen, contract.ServiceKindKimiCoding,
		contract.ServiceKindGLMCoding, contract.ServiceKindMiniMaxCoding,
		contract.ServiceKindDeepSeek, contract.ServiceKindMoonshot, contract.ServiceKindMiniMax, contract.ServiceKindXAI:
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

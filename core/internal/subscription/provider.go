package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// CodexProvider calls the ChatGPT Codex backend using subscription tokens.
// Service addresses come from official openai/codex open-source evidence
// (ADR 0009); AstrLink never invents alternate private entitlement APIs.
type CodexProvider struct {
	apiBaseURL          string
	modelsClientVersion string
	originator          string
	httpClient          *http.Client
}

func NewCodexProvider(oauth accountauth.OAuthConfig) *CodexProvider {
	oauth = oauth.Normalize()
	return &CodexProvider{
		apiBaseURL:          strings.TrimRight(oauth.APIBaseURL, "/"),
		modelsClientVersion: oauth.ModelsClientVersion,
		originator:          oauth.Originator,
		httpClient:          oauth.HTTPClient,
	}
}

func (provider *CodexProvider) APIBaseURL() string {
	if provider == nil {
		return ""
	}
	return provider.apiBaseURL
}

type ModelList struct {
	Object string        `json:"object"`
	Data   []ModelRecord `json:"data"`
}

type ModelRecord struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by,omitempty"`
}

func (provider *CodexProvider) ModelsClientVersion() string {
	if provider == nil || strings.TrimSpace(provider.modelsClientVersion) == "" {
		return accountauth.DefaultCodexModelsClientVersion
	}
	return provider.modelsClientVersion
}

func (provider *CodexProvider) Usage(ctx context.Context, tokens accountauth.AccountTokens) (contract.SubscriptionUsage, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		CodexUsageURL(provider.apiBaseURL),
		nil,
	)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	applyCodexAuth(request, tokens, provider.originator, provider.ModelsClientVersion())
	response, err := provider.httpClient.Do(request)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Printf("codex usage GET %s returned status %d", request.URL.String(), response.StatusCode)
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: status %d", ErrUsageUnavailable, response.StatusCode)
	}
	usage, err := DecodeCodexUsage(body)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	return usage, nil
}

func (provider *CodexProvider) ConsumeReset(
	ctx context.Context,
	tokens accountauth.AccountTokens,
	redeemRequestID string,
) (contract.SubscriptionUsageReset, error) {
	redeemRequestID = strings.TrimSpace(redeemRequestID)
	if redeemRequestID == "" {
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%w: missing redeem_request_id", ErrResetUnavailable)
	}
	payload, err := json.Marshal(map[string]string{"redeem_request_id": redeemRequestID})
	if err != nil {
		return contract.SubscriptionUsageReset{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		CodexConsumeResetURL(provider.apiBaseURL),
		bytes.NewReader(payload),
	)
	if err != nil {
		return contract.SubscriptionUsageReset{}, err
	}
	applyCodexAuth(request, tokens, provider.originator, provider.ModelsClientVersion())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := provider.httpClient.Do(request)
	if err != nil {
		return contract.SubscriptionUsageReset{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return contract.SubscriptionUsageReset{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Printf("codex usage reset POST %s returned status %d", request.URL.String(), response.StatusCode)
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%w: status %d", ErrResetUnavailable, response.StatusCode)
	}
	result, err := DecodeCodexConsumeReset(body)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (provider *CodexProvider) ListModels(ctx context.Context, tokens accountauth.AccountTokens) (ModelList, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		CodexModelsURL(provider.apiBaseURL, provider.ModelsClientVersion()),
		nil,
	)
	if err != nil {
		return ModelList{}, err
	}
	applyCodexAuth(request, tokens, provider.originator, provider.ModelsClientVersion())
	response, err := provider.httpClient.Do(request)
	if err != nil {
		return ModelList{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return ModelList{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ModelList{}, fmt.Errorf("codex models returned status %d", response.StatusCode)
	}
	return DecodeCodexModels(body)
}

func (provider *CodexProvider) CreateResponse(ctx context.Context, tokens accountauth.AccountTokens, rawBody []byte) ([]byte, int, http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.apiBaseURL+"/responses", bytes.NewReader(rawBody))
	if err != nil {
		return nil, 0, nil, err
	}
	applyCodexAuth(request, tokens, provider.originator, provider.ModelsClientVersion())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := provider.httpClient.Do(request)
	if err != nil {
		return nil, 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, 0, nil, err
	}
	header := response.Header.Clone()
	return body, response.StatusCode, header, nil
}

func applyCodexAuth(request *http.Request, tokens accountauth.AccountTokens, originator, clientVersion string) {
	accountauth.ApplyCodexAPIHeaders(request.Header, tokens, originator, clientVersion)
}

// ProbeNonStreamingResponse is a tiny helper used by control/tests to exercise
// the connected-account Responses path without exposing credentials.
func (provider *CodexProvider) ProbeNonStreamingResponse(ctx context.Context, tokens accountauth.AccountTokens, model string) ([]byte, int, error) {
	if model == "" {
		model = "gpt-5"
	}
	payload := map[string]any{
		"model": model,
		"input": "ping",
		"store": false,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	body, status, _, err := provider.CreateResponse(ctx, tokens, raw)
	return body, status, err
}

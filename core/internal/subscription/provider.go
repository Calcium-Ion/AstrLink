package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// CodexProvider calls the ChatGPT Codex backend using subscription tokens.
// Service addresses come from official openai/codex open-source evidence
// (ADR 0009); AstrLink never invents alternate private entitlement APIs.
type CodexProvider struct {
	apiBaseURL string
	httpClient *http.Client
}

func NewCodexProvider(oauth accountauth.OAuthConfig) *CodexProvider {
	oauth = oauth.Normalize()
	return &CodexProvider{
		apiBaseURL: strings.TrimRight(oauth.APIBaseURL, "/"),
		httpClient: oauth.HTTPClient,
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

func (provider *CodexProvider) ListModels(ctx context.Context, tokens accountauth.AccountTokens) (ModelList, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.apiBaseURL+"/models", nil)
	if err != nil {
		return ModelList{}, err
	}
	applyCodexAuth(request, tokens)
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
	var list ModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return ModelList{}, fmt.Errorf("decode codex models: %w", err)
	}
	return list, nil
}

func (provider *CodexProvider) CreateResponse(ctx context.Context, tokens accountauth.AccountTokens, rawBody []byte) ([]byte, int, http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.apiBaseURL+"/responses", bytes.NewReader(rawBody))
	if err != nil {
		return nil, 0, nil, err
	}
	applyCodexAuth(request, tokens)
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

func applyCodexAuth(request *http.Request, tokens accountauth.AccountTokens) {
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	if tokens.AccountID != "" {
		request.Header.Set("ChatGPT-Account-ID", tokens.AccountID)
	}
	request.Header.Set("OAI-Product-Sku", "codex")
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

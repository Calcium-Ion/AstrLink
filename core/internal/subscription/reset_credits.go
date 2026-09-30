package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// ResetCredits fetches details on demand, independently of the cached usage summary.
func (manager *Manager) ResetCredits(ctx context.Context, id contract.ServiceID) (contract.ResetCreditsDetails, error) {
	ctx, err := manager.ProxyContext(ctx, id)
	if err != nil {
		return contract.ResetCreditsDetails{}, err
	}
	account, err := manager.Get(ctx, id)
	if err != nil {
		return contract.ResetCreditsDetails{}, err
	}
	if account.Provider != contract.SubscriptionProviderOpenAICodex {
		return contract.ResetCreditsDetails{}, ErrResetUnavailable
	}
	tokens, err := manager.AccessToken(ctx, id)
	if err != nil {
		return contract.ResetCreditsDetails{}, ErrNotConnected
	}
	return manager.provider.ResetCredits(ctx, tokens)
}

func (provider *CodexProvider) ResetCredits(ctx context.Context, tokens accountauth.AccountTokens) (contract.ResetCreditsDetails, error) {
	// The official backend-client lists credits at the consume path's parent.
	url := strings.TrimSuffix(CodexConsumeResetURL(provider.apiBaseURL), "/consume")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return contract.ResetCreditsDetails{}, fmt.Errorf("%w: %w", ErrResetUnavailable, err)
	}
	applyCodexAuth(request, tokens, provider.clientIdentity(ctx))
	request.Header.Set("Accept", "application/json")
	response, err := provider.httpClient.Do(request)
	if err != nil {
		return contract.ResetCreditsDetails{}, fmt.Errorf("%w: %w", ErrResetUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return contract.ResetCreditsDetails{}, fmt.Errorf("%w: status %d", ErrResetUnavailable, response.StatusCode)
	}
	body, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return contract.ResetCreditsDetails{}, fmt.Errorf("%w: %w", ErrResetUnavailable, err)
	}
	return decodeResetCredits(body)
}

func decodeResetCredits(body []byte) (contract.ResetCreditsDetails, error) {
	var payload struct {
		AvailableCount *int `json:"available_count"`
		Credits        *[]struct {
			Status    string     `json:"status"`
			ResetType string     `json:"reset_type"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.AvailableCount == nil ||
		*payload.AvailableCount < 0 || *payload.AvailableCount > 1000 || payload.Credits == nil || len(*payload.Credits) > 1000 {
		return contract.ResetCreditsDetails{}, fmt.Errorf("%w: invalid reset credit details", ErrResetUnavailable)
	}
	result := contract.ResetCreditsDetails{AvailableCount: *payload.AvailableCount, Credits: []contract.ResetCreditDetails{}}
	for _, credit := range *payload.Credits {
		if credit.Status != "available" || credit.ResetType != "codex_rate_limits" {
			continue
		}
		if credit.ExpiresAt != nil && credit.ExpiresAt.IsZero() {
			return contract.ResetCreditsDetails{}, fmt.Errorf("%w: invalid reset credit expiry", ErrResetUnavailable)
		}
		result.Credits = append(result.Credits, contract.ResetCreditDetails{ExpiresAt: credit.ExpiresAt})
	}
	return result, nil
}

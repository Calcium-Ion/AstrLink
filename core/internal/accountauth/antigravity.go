package accountauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// The installed-app OAuth client identifies the application, not a user account.
const (
	antigravityClientID          = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	antigravityClientSecret      = "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"
	DefaultAntigravityAPIBaseURL = "https://daily-cloudcode-pa.googleapis.com"
	antigravityVersion           = "2.17.0" // 2026-09-29
)

func normalizeAntigravityConfig(c OAuthConfig) OAuthConfig {
	if c.ClientID == "" {
		c.ClientID = antigravityClientID
	}
	if c.ClientSecret == "" {
		c.ClientSecret = antigravityClientSecret
	}
	if c.Issuer == "" {
		c.Issuer = "https://accounts.google.com"
	}
	if c.AuthorizeURL == "" {
		c.AuthorizeURL = c.Issuer + "/o/oauth2/v2/auth"
	}
	if c.TokenURL == "" {
		c.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if c.UserInfoURL == "" {
		c.UserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo?alt=json"
	}
	if c.APIBaseURL == "" {
		c.APIBaseURL = DefaultAntigravityAPIBaseURL
	}
	if c.ProjectBaseURL == "" {
		c.ProjectBaseURL = "https://cloudcode-pa.googleapis.com"
	}
	if c.RedirectPath == "" {
		c.RedirectPath = "/oauth-callback"
	}
	if c.PreferredPort <= 0 {
		c.PreferredPort = 51121
	}
	if c.FallbackPort <= 0 {
		c.FallbackPort = 51122
	}
	if len(c.Scopes) == 0 {
		c.Scopes = []string{"https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/cclog", "https://www.googleapis.com/auth/experimentsandconfigs"}
	}
	return c
}

func AntigravityUserAgent() string {
	return "antigravity/hub/" + antigravityVersion + " " + runtime.GOOS + "/" + runtime.GOARCH
}

func ApplyAntigravityHeaders(h http.Header, tokens AccountTokens) {
	h.Set("Authorization", "Bearer "+tokens.AccessToken)
	h.Set("User-Agent", AntigravityUserAgent())
	h.Set(providerapi.AntigravityProjectHeader, tokens.ProjectID)
	for _, key := range []string{"Originator", "Via", "X-Powered-By", "X-Goog-Api-Key", "X-Api-Key", "ChatGPT-Account-ID"} {
		h[http.CanonicalHeaderKey(key)] = nil
	}
}

func AntigravityCall(ctx context.Context, client *http.Client, base, method string, tokens AccountTokens, payload, result any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1internal:"+method, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("User-Agent", AntigravityUserAgent())
	if method == "onboardUser" {
		req.Header.Set("User-Agent", AntigravityUserAgent()+" google-api-nodejs-client/10.3.0")
		req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")
	}
	return antigravityJSON(client, req, result)
}

func antigravityJSON(client *http.Client, req *http.Request, result any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Antigravity returned status %d", res.StatusCode)
	}
	raw, err := transport.ReadResponseBody(res, 4<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

type googleProject string

func (p *googleProject) UnmarshalJSON(raw []byte) error {
	var id string
	if json.Unmarshal(raw, &id) != nil {
		var object struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		id = object.ID
	}
	*p = googleProject(strings.TrimSpace(id))
	return nil
}

func (client *TokenClient) completeAntigravityAccount(ctx context.Context, tokens *AccountTokens) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.config.UserInfoURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("User-Agent", AntigravityUserAgent())
	var user struct {
		ID string `json:"id"`
	}
	if err := antigravityJSON(client.config.HTTPClient, req, &user); err != nil {
		return err
	}
	if strings.TrimSpace(user.ID) == "" {
		return fmt.Errorf("Antigravity profile omitted account id")
	}
	tokens.AccountID = user.ID
	type tier struct {
		ID      string `json:"id"`
		Default bool   `json:"isDefault"`
	}
	var load struct {
		Project      googleProject `json:"cloudaicompanionProject"`
		CurrentTier  tier          `json:"currentTier"`
		PaidTier     tier          `json:"paidTier"`
		AllowedTiers []tier        `json:"allowedTiers"`
	}
	if err := AntigravityCall(ctx, client.config.HTTPClient, client.config.ProjectBaseURL, "loadCodeAssist", *tokens, map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}}, &load); err != nil {
		return err
	}
	tokens.PlanType = load.CurrentTier.ID
	if load.PaidTier.ID != "" {
		tokens.PlanType = load.PaidTier.ID
	}
	if load.Project != "" {
		tokens.ProjectID = string(load.Project)
		return nil
	}
	tierID := load.CurrentTier.ID
	for _, candidate := range load.AllowedTiers {
		if candidate.Default && candidate.ID != "" {
			tierID = candidate.ID
			break
		}
	}
	if tierID == "" {
		tierID = "free-tier"
	}
	for attempt := 0; attempt < 5; attempt++ {
		var op struct {
			Done     bool `json:"done"`
			Response struct {
				Project googleProject `json:"cloudaicompanionProject"`
			} `json:"response"`
		}
		body := map[string]any{"tier_id": tierID, "metadata": map[string]string{"ide_type": "ANTIGRAVITY", "ide_name": "antigravity", "ide_version": antigravityVersion}}
		if err := AntigravityCall(ctx, client.config.HTTPClient, client.config.APIBaseURL, "onboardUser", *tokens, body, &op); err != nil {
			return err
		}
		if op.Done {
			if op.Response.Project == "" {
				return fmt.Errorf("Antigravity onboarding omitted project")
			}
			tokens.ProjectID = string(op.Response.Project)
			if tokens.PlanType == "" {
				tokens.PlanType = tierID
			}
			return nil
		}
		if attempt < 4 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	return fmt.Errorf("Antigravity onboarding is still pending; finish setup in Antigravity and sign in again")
}

package accountauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// Factory Droid login follows the Droid CLI: RFC 8628 device authorization
// against WorkOS under Droid's public client, then Bearer requests to
// Factory's API with the CLI identity headers. Factory serves model requests
// only to a token scoped to an organization, so a sign-in that returns none is
// put into the account's first organization the way Droid does. AstrLink never
// embeds a client secret; the client ID below is the public one shipped in
// the Droid CLI.
const (
	DefaultDroidIssuer        = "https://api.workos.com/user_management"
	DefaultDroidClientID      = "client_01HNM792M5G5G1A2THWPXKFMXB"
	DefaultDroidAPIBaseURL    = "https://api.factory.ai"
	DefaultDroidEUAPIBaseURL  = "https://api.eu.factory.ai"
	DefaultDroidDeviceCodeTTL = 15 * time.Minute

	// DroidRegionEU marks an organization Factory serves from its EU region.
	DroidRegionEU = "eu"

	ErrCodeDroidNoOrganization = "factory_no_organization"

	droidDefaultTokenLifetime = time.Hour
	droidOrgHeader            = "X-Factory-Org-Id"
	droidWhoamiExtendedHeader = "X-Factory-Whoami-Extended"
)

// ErrDroidNoOrganization reports a Factory account outside any organization.
var ErrDroidNoOrganization = errors.New("factory account belongs to no organization")

func normalizeDroidConfig(config OAuthConfig) OAuthConfig {
	if strings.TrimSpace(config.ClientID) == "" {
		config.ClientID = DefaultDroidClientID
	}
	if config.Issuer == "" {
		config.Issuer = DefaultDroidIssuer
	}
	if config.TokenURL == "" {
		config.TokenURL = strings.TrimRight(config.Issuer, "/") + "/authenticate"
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = DefaultDroidAPIBaseURL
	}
	if config.DeviceCodeTTL <= 0 {
		config.DeviceCodeTTL = DefaultDroidDeviceCodeTTL
	}
	return config
}

// DroidAPIBaseURL is the Factory API the account's organization is served
// from: the EU region's host for an EU organization, otherwise the configured
// base. A base that is not Factory's public host (a test server) is kept.
func DroidAPIBaseURL(configured string, tokens AccountTokens) string {
	base := strings.TrimRight(configured, "/")
	if tokens.Region == DroidRegionEU && base == DefaultDroidAPIBaseURL {
		return DefaultDroidEUAPIBaseURL
	}
	return base
}

// ApplyDroidAPIHeaders writes the Droid CLI identity Factory's API expects:
// the Bearer token, the CLI User-Agent and client headers, and the account's
// organization. An EU organization's host rides a gateway-local overlay that
// providerapi.DroidRequest consumes and the forwarder strips.
func ApplyDroidAPIHeaders(header http.Header, tokens AccountTokens) {
	if header == nil {
		return
	}
	identity := DefaultDroidIdentity()
	header.Set("Authorization", "Bearer "+tokens.AccessToken)
	header.Set("User-Agent", identity.UserAgent)
	for name, value := range identity.Headers {
		header.Set(name, value)
	}
	// A nil overlay value deletes a client-supplied organization.
	header[http.CanonicalHeaderKey(droidOrgHeader)] = nil
	if tokens.AccountID != "" {
		header.Set(droidOrgHeader, tokens.AccountID)
	}
	header[http.CanonicalHeaderKey(providerapi.DroidEndpointHeader)] = nil
	if endpoint := DroidAPIBaseURL(DefaultDroidAPIBaseURL, tokens); endpoint != DefaultDroidAPIBaseURL {
		header.Set(providerapi.DroidEndpointHeader, endpoint)
	}
}

// droidFormRequest posts a form to WorkOS as the Droid CLI.
func droidFormRequest(ctx context.Context, endpoint string, form url.Values) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", DefaultDroidIdentity().UserAgent)
	request.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	return request, nil
}

// droidAPIRequest reads a Factory API path with the account's identity.
func droidAPIRequest(ctx context.Context, base, path string, tokens AccountTokens) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	ApplyDroidAPIHeaders(request.Header, tokens)
	request.Header.Del(providerapi.DroidEndpointHeader)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	return request, nil
}

type droidDeviceCodeResponse struct {
	DeviceCode              string          `json:"device_code"`
	UserCode                string          `json:"user_code"`
	VerificationURI         string          `json:"verification_uri"`
	VerificationURIComplete string          `json:"verification_uri_complete"`
	ExpiresIn               int64           `json:"expires_in"`
	Interval                json.RawMessage `json:"interval"`
}

type droidTokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	OrganizationID   string `json:"organization_id"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (manager *SessionManager) requestDroidDeviceAuthorization(ctx context.Context) (polledDeviceAuthorization, error) {
	form := url.Values{}
	form.Set("client_id", manager.config.ClientID)
	endpoint := strings.TrimRight(manager.config.Issuer, "/") + "/authorize/device"
	request, err := droidFormRequest(ctx, endpoint, form)
	if err != nil {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	response, err := manager.config.HTTPClient.Do(request)
	if err != nil {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return polledDeviceAuthorization{}, ErrDeviceCodeUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	raw, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	var parsed droidDeviceCodeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	if strings.TrimSpace(parsed.DeviceCode) == "" || len(parsed.DeviceCode) > 4096 ||
		!validDeviceUserCode(parsed.UserCode) {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	// Prefer the pre-filled URL so the user only confirms the code they see.
	verification := strings.TrimSpace(parsed.VerificationURIComplete)
	if verification == "" {
		verification = strings.TrimSpace(parsed.VerificationURI)
	}
	public := contract.AuthorizationDeviceCode{VerificationURL: verification, UserCode: parsed.UserCode}
	if len(verification) > 4096 || public.Validate() != nil {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	interval, err := parseDevicePollInterval(parsed.Interval)
	if err != nil {
		return polledDeviceAuthorization{}, fmt.Errorf("%w", ErrDeviceCodeRequestFailed)
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	device := polledDeviceAuthorization{
		VerificationURL: verification,
		UserCode:        parsed.UserCode,
		DeviceCode:      parsed.DeviceCode,
		PollInterval:    manager.clampDevicePollInterval(interval),
	}
	if parsed.ExpiresIn > 0 {
		device.ExpiresIn = time.Duration(parsed.ExpiresIn) * time.Second
	}
	return device, nil
}

// pollDroidDeviceAuthorizationOnce exchanges the device code once. WorkOS
// answers a pending grant with an error status and an OAuth error code.
func (manager *SessionManager) pollDroidDeviceAuthorizationOnce(
	ctx context.Context,
	device polledDeviceAuthorization,
) (AccountTokens, bool, bool, error) {
	form := url.Values{}
	form.Set("grant_type", standardDeviceGrantType)
	form.Set("device_code", device.DeviceCode)
	form.Set("client_id", manager.config.ClientID)
	request, err := droidFormRequest(ctx, manager.tokens.config.TokenURL, form)
	if err != nil {
		return AccountTokens{}, false, false, err
	}
	response, err := manager.config.HTTPClient.Do(request)
	if err != nil {
		return AccountTokens{}, false, false, err
	}
	defer response.Body.Close()
	body, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return AccountTokens{}, false, false, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		tokens, err := manager.tokens.parseDroidTokenResponse(body, AccountTokens{})
		if err != nil {
			return AccountTokens{}, false, false, err
		}
		tokens, err = manager.tokens.completeDroidAccount(ctx, tokens)
		return tokens, false, false, err
	}
	var failure droidTokenResponse
	if json.Unmarshal(bytes.TrimSpace(body), &failure) != nil || failure.Error == "" {
		return AccountTokens{}, false, false, fmt.Errorf("device token endpoint returned status %d", response.StatusCode)
	}
	switch failure.Error {
	case "authorization_pending":
		return AccountTokens{}, true, false, nil
	case "slow_down":
		return AccountTokens{}, true, true, nil
	default:
		// access_denied, expired_token, invalid_grant and unknown codes end the session.
		return AccountTokens{}, false, false, fmt.Errorf("device authorization ended: %s", failure.Error)
	}
}

// parseDroidTokenResponse maps a WorkOS token answer onto account tokens.
// Fields the answer does not carry are kept from previous: the refresh token
// when WorkOS did not rotate it, and the organization, plan and region.
func (client *TokenClient) parseDroidTokenResponse(body []byte, previous AccountTokens) (AccountTokens, error) {
	var parsed droidTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return AccountTokens{}, fmt.Errorf("decode token response: %w", err)
	}
	if parsed.Error == "invalid_grant" {
		return AccountTokens{}, ErrInvalidGrant
	}
	if parsed.Error != "" {
		return AccountTokens{}, fmt.Errorf("token endpoint returned %s", parsed.Error)
	}
	access := strings.TrimSpace(parsed.AccessToken)
	if access == "" || len(access) > 16_384 || !validHeaderToken(access) {
		return AccountTokens{}, fmt.Errorf("token response omitted a usable access_token")
	}
	refresh := strings.TrimSpace(parsed.RefreshToken)
	if refresh == "" {
		refresh = previous.RefreshToken
	}
	if refresh == "" {
		return AccountTokens{}, fmt.Errorf("token response missing refresh_token")
	}
	now := client.config.Now().UTC()
	expires := now.Add(droidDefaultTokenLifetime)
	if parsed.ExpiresIn > 0 {
		expires = now.Add(time.Duration(parsed.ExpiresIn) * time.Second)
	} else if exp := jwtNumericClaim(access, "exp"); exp > 0 {
		expires = time.Unix(exp, 0).UTC()
	}
	tokens := AccountTokens{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "bearer",
		AccountID:    previous.AccountID,
		ProjectID:    previous.ProjectID,
		PlanType:     previous.PlanType,
		Region:       previous.Region,
		ExpiresAt:    expires,
	}
	if organization := strings.TrimSpace(parsed.OrganizationID); organization != "" {
		tokens.ProjectID = organization
	}
	return tokens, nil
}

// droidOrganizationClaim is the organization a Factory access token is scoped
// to, as Droid reads it from the token's claims.
func droidOrganizationClaim(accessToken string) string {
	return jwtStringClaim(accessToken, "external_org_id", "org_id", "organization_id", "organizationId", "orgId")
}

// jwtNumericClaim returns a numeric claim of an unverified JWT payload, or 0.
func jwtNumericClaim(token, key string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return 0
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	if value, ok := claims[key].(float64); ok && value > 0 {
		return int64(value)
	}
	return 0
}

// droidWhoami is what GET /api/cli/whoami tells of an account: Factory's own
// id of its organization (sent as X-Factory-Org-Id) and the region serving it.
type droidWhoami struct {
	OrgID  string `json:"orgId"`
	Region string `json:"region"`
}

// completeDroidAccount turns a fresh sign-in into account tokens Factory will
// serve: a token outside any organization is put into the account's first
// one through an organization-scoped refresh, as Droid does, and whoami then
// names the organization and region Factory serves the account from.
func (client *TokenClient) completeDroidAccount(ctx context.Context, tokens AccountTokens) (AccountTokens, error) {
	organization := droidOrganizationClaim(tokens.AccessToken)
	if organization == "" {
		workosOrganization, err := client.droidFirstOrganization(ctx, tokens)
		if err != nil {
			return AccountTokens{}, err
		}
		if workosOrganization == "" {
			return AccountTokens{}, ErrDroidNoOrganization
		}
		tokens.ProjectID = workosOrganization
		if tokens, err = client.refreshDroid(ctx, tokens); err != nil {
			return AccountTokens{}, err
		}
		if organization = droidOrganizationClaim(tokens.AccessToken); organization == "" {
			return AccountTokens{}, ErrDroidNoOrganization
		}
	}
	tokens.AccountID = organization
	tokens.Region = ""
	// whoami refines the organization id and names the region; an account
	// whose whoami is unavailable keeps the token's organization.
	if who, err := client.droidWhoami(ctx, tokens); err == nil {
		if who.OrgID != "" {
			tokens.AccountID = who.OrgID
		}
		if strings.EqualFold(strings.TrimSpace(who.Region), DroidRegionEU) {
			tokens.Region = DroidRegionEU
		}
	}
	return tokens, nil
}

func (client *TokenClient) droidFirstOrganization(ctx context.Context, tokens AccountTokens) (string, error) {
	tokens.AccountID = ""
	request, err := droidAPIRequest(ctx, client.config.APIBaseURL, "/api/cli/org", tokens)
	if err != nil {
		return "", err
	}
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return "", err
	}
	if response.StatusCode == http.StatusUnauthorized {
		return "", ErrInvalidGrant
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("factory organization lookup returned status %d", response.StatusCode)
	}
	var organizations struct {
		IDs []string `json:"workosOrgIds"`
	}
	if err := json.Unmarshal(body, &organizations); err != nil {
		return "", fmt.Errorf("decode factory organizations: %w", err)
	}
	for _, id := range organizations.IDs {
		if id = strings.TrimSpace(id); id != "" && len(id) <= 256 && validHeaderToken(id) {
			return id, nil
		}
	}
	return "", nil
}

func (client *TokenClient) droidWhoami(ctx context.Context, tokens AccountTokens) (droidWhoami, error) {
	request, err := droidAPIRequest(ctx, DroidAPIBaseURL(client.config.APIBaseURL, tokens), "/api/cli/whoami", tokens)
	if err != nil {
		return droidWhoami{}, err
	}
	request.Header.Set(droidWhoamiExtendedHeader, "true")
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return droidWhoami{}, err
	}
	defer response.Body.Close()
	body, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return droidWhoami{}, err
	}
	if response.StatusCode != http.StatusOK {
		return droidWhoami{}, fmt.Errorf("factory whoami returned status %d", response.StatusCode)
	}
	var who droidWhoami
	if err := json.Unmarshal(body, &who); err != nil {
		return droidWhoami{}, fmt.Errorf("decode factory whoami: %w", err)
	}
	if who.OrgID = strings.TrimSpace(who.OrgID); len(who.OrgID) > 256 || !validHeaderToken(who.OrgID) {
		who.OrgID = ""
	}
	return who, nil
}

// refreshDroid trades the refresh token for a new pair, in the WorkOS
// organization the account was put into when one is known. WorkOS rotates
// the refresh token; any other 4xx than a rate limit means the pair is dead.
func (client *TokenClient) refreshDroid(ctx context.Context, current AccountTokens) (AccountTokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", current.RefreshToken)
	form.Set("client_id", client.config.ClientID)
	if current.ProjectID != "" {
		form.Set("organization_id", current.ProjectID)
	}
	request, err := droidFormRequest(ctx, client.config.TokenURL, form)
	if err != nil {
		return AccountTokens{}, err
	}
	response, err := client.config.HTTPClient.Do(request)
	if err != nil {
		return AccountTokens{}, err
	}
	defer response.Body.Close()
	body, err := transport.ReadResponseBody(response, 1<<20)
	if err != nil {
		return AccountTokens{}, err
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
		return AccountTokens{}, ErrInvalidGrant
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AccountTokens{}, fmt.Errorf("%s: token endpoint returned status %d", ErrCodeCallbackFailed, response.StatusCode)
	}
	return client.parseDroidTokenResponse(body, current)
}

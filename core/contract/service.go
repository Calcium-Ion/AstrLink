package contract

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxServiceModels = 2000

// ServiceID is the single stable identity used by configured API services,
// routes, execution plans, health state, and request records.
type ServiceID string

// ServiceKind is a product-level service type. HTTP gateways/providers and
// browser-authorized subscriptions deliberately share this one taxonomy.
type ServiceKind string

const (
	ServiceKindCodexSubscription  ServiceKind = "codex_subscription"
	ServiceKindClaudeSubscription ServiceKind = "claude_subscription"
	ServiceKindGrokSubscription   ServiceKind = "grok_subscription"
	ServiceKindOpenCodeGo         ServiceKind = "opencode_go"
	ServiceKindOpenCodeZen        ServiceKind = "opencode_zen"
	ServiceKindKimiCoding         ServiceKind = "kimi_coding"
	ServiceKindGLMCoding          ServiceKind = "glm_coding"
	ServiceKindMiniMaxCoding      ServiceKind = "minimax_coding"
	ServiceKindNewAPI             ServiceKind = "newapi"
	ServiceKindOpenAI             ServiceKind = "openai"
	ServiceKindAnthropic          ServiceKind = "anthropic"
	ServiceKindGemini             ServiceKind = "gemini"
	ServiceKindOpenAICompatible   ServiceKind = "openai_compatible"
	ServiceKindDeepSeek           ServiceKind = "deepseek"
	ServiceKindQwen               ServiceKind = "qwen"
	ServiceKindMoonshot           ServiceKind = "moonshot"
	ServiceKindGLM                ServiceKind = "glm"
	ServiceKindMiniMax            ServiceKind = "minimax"
	ServiceKindDoubao             ServiceKind = "doubao"
	ServiceKindXAI                ServiceKind = "xai"
	ServiceKindCustom             ServiceKind = "custom"
)

func (kind ServiceKind) Valid() bool {
	switch kind {
	case ServiceKindCodexSubscription, ServiceKindNewAPI, ServiceKindOpenAI,
		ServiceKindAnthropic, ServiceKindGemini, ServiceKindOpenAICompatible,
		ServiceKindCustom, ServiceKindClaudeSubscription, ServiceKindGrokSubscription, ServiceKindOpenCodeGo,
		ServiceKindOpenCodeZen, ServiceKindKimiCoding, ServiceKindGLMCoding, ServiceKindMiniMaxCoding,
		ServiceKindDeepSeek, ServiceKindQwen, ServiceKindMoonshot, ServiceKindGLM, ServiceKindMiniMax, ServiceKindDoubao, ServiceKindXAI:
		return true
	default:
		return false
	}
}

func (kind ServiceKind) IsSubscription() bool {
	return kind == ServiceKindCodexSubscription || kind == ServiceKindClaudeSubscription || kind == ServiceKindGrokSubscription
}

func (kind ServiceKind) SubscriptionProvider() SubscriptionProvider {
	switch kind {
	case ServiceKindClaudeSubscription:
		return SubscriptionProviderClaudeCode
	case ServiceKindGrokSubscription:
		return SubscriptionProviderXAIGrok
	case ServiceKindCodexSubscription:
		return SubscriptionProviderOpenAICodex
	default:
		return ""
	}
}

func (kind ServiceKind) IsHTTP() bool {
	return kind.Valid() && !kind.IsSubscription()
}

// HTTPConnection contains non-secret transport configuration for an HTTP API
// service. Credential bytes remain in the dedicated local credential table.
type HTTPConnection struct {
	BaseURL       string      `json:"base_url"`
	Auth          ServiceAuth `json:"auth"`
	CredentialRef string      `json:"credential_ref,omitempty"`
}

func (connection HTTPConnection) Validate(serviceID ServiceID) error {
	if err := connection.Auth.Validate(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if len(connection.BaseURL) > 2048 {
		return fmt.Errorf("base_url exceeds 2048 characters")
	}
	parsed, err := url.Parse(connection.BaseURL)
	if err != nil {
		return fmt.Errorf("parse base_url: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("base_url must be an absolute http(s) URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("base_url must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("base_url must not contain a query or fragment")
	}
	if connection.CredentialRef != "" {
		if err := ValidateCredentialRef(connection.CredentialRef); err != nil {
			return err
		}
		want := "local://service/" + string(serviceID)
		if connection.CredentialRef != want {
			return fmt.Errorf("http credential_ref must equal %q", want)
		}
	}
	return nil
}

// SubscriptionConnection is the non-secret lifecycle state for a
// browser-authorized subscription. OAuth tokens never enter this document.
type SubscriptionConnection struct {
	Provider              SubscriptionProvider `json:"provider"`
	Status                SubscriptionStatus   `json:"status"`
	AccountHint           string               `json:"account_hint,omitempty"`
	ProviderAccountID     string               `json:"provider_account_id,omitempty"`
	CredentialRef         string               `json:"credential_ref,omitempty"`
	AuthorizationBoundary string               `json:"authorization_boundary,omitempty"`
	TokenExpiresAt        *time.Time           `json:"token_expires_at,omitempty"`
	LastRefreshAt         *time.Time           `json:"last_refresh_at,omitempty"`
	LastError             *SubscriptionError   `json:"last_error,omitempty"`
}

func (connection SubscriptionConnection) Validate(serviceID ServiceID) error {
	account := SubscriptionAccount{
		ID:                    serviceID,
		Provider:              connection.Provider,
		Status:                connection.Status,
		DisplayName:           "service",
		AccountHint:           connection.AccountHint,
		ProviderAccountID:     connection.ProviderAccountID,
		CredentialRef:         connection.CredentialRef,
		Capabilities:          connection.Provider.Capabilities(),
		AuthorizationBoundary: connection.AuthorizationBoundary,
		TokenExpiresAt:        connection.TokenExpiresAt,
		LastRefreshAt:         connection.LastRefreshAt,
		LastError:             connection.LastError,
		CreatedAt:             time.Unix(1, 0).UTC(),
		UpdatedAt:             time.Unix(1, 0).UTC(),
	}
	return account.Validate()
}

// Service is the canonical configured API-service aggregate. Exactly one
// variant payload is present, determined by Kind.
type Service struct {
	Proxy *ServiceProxy `json:"proxy,omitempty"`
	// Nil preserves the provider default for existing documents; explicit false is retained.
	ResponsesWebSocketEnabled *bool                   `json:"responses_websocket_enabled,omitempty"`
	FailurePolicy             *FailurePolicy          `json:"failure_policy,omitempty"`
	ID                        ServiceID               `json:"id"`
	Name                      string                  `json:"name"`
	Kind                      ServiceKind             `json:"kind"`
	Enabled                   bool                    `json:"enabled"`
	Models                    []string                `json:"models"`
	Capabilities              []Capability            `json:"capabilities"`
	HTTP                      *HTTPConnection         `json:"http,omitempty"`
	Subscription              *SubscriptionConnection `json:"subscription,omitempty"`
	CreatedAt                 time.Time               `json:"created_at,omitempty"`
	UpdatedAt                 time.Time               `json:"updated_at,omitempty"`
}

// ResponsesWebSocket reports the effective per-channel transport setting.
func (service Service) ResponsesWebSocket() bool {
	if service.ResponsesWebSocketEnabled != nil {
		return *service.ResponsesWebSocketEnabled
	}
	return service.Kind == ServiceKindCodexSubscription
}

func (service Service) Validate() error {
	if err := service.Proxy.Validate(service.ID); err != nil {
		return err
	}
	if service.FailurePolicy != nil {
		if err := service.FailurePolicy.Validate(); err != nil {
			return fmt.Errorf("failure_policy: %w", err)
		}
	}
	if err := service.ID.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(service.Name) == "" || utf8.RuneCountInString(service.Name) > 128 {
		return fmt.Errorf("service name must contain 1 to 128 characters")
	}
	if !service.Kind.Valid() {
		return fmt.Errorf("unknown service kind %q", service.Kind)
	}
	switch {
	case service.Kind.IsHTTP():
		if service.HTTP == nil || service.Subscription != nil {
			return fmt.Errorf("http service requires only the http connection")
		}
		if err := service.HTTP.Validate(service.ID); err != nil {
			return fmt.Errorf("http: %w", err)
		}
	case service.Kind.IsSubscription():
		if service.Subscription == nil || service.HTTP != nil {
			return fmt.Errorf("subscription service requires only the subscription connection")
		}
		if service.Subscription.Provider != service.Kind.SubscriptionProvider() {
			return fmt.Errorf("%s requires provider %q", service.Kind, service.Kind.SubscriptionProvider())
		}
		if err := service.Subscription.Validate(service.ID); err != nil {
			return fmt.Errorf("subscription: %w", err)
		}
	}
	if service.Capabilities == nil {
		return fmt.Errorf("service capabilities must be a non-null array")
	}
	if err := validateServiceModels(service.Models); err != nil {
		return err
	}
	if service.Kind.IsSubscription() {
		if err := service.Kind.SubscriptionProvider().ValidateCapabilities(service.Capabilities); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(service.Capabilities))
	for index, capability := range service.Capabilities {
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("capabilities[%d]: %w", index, err)
		}
		key := string(capability.Protocol) + "\x00" + string(capability.Mode)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("capabilities[%d]: duplicate protocol %q and mode %q", index, capability.Protocol, capability.Mode)
		}
		seen[key] = struct{}{}
	}
	if !service.CreatedAt.IsZero() && !service.UpdatedAt.IsZero() && service.UpdatedAt.Before(service.CreatedAt) {
		return fmt.Errorf("updated_at must not precede created_at")
	}
	return nil
}

// ServiceFromEndpoint converts the legacy HTTP-only view into the canonical
// Service aggregate.
func ServiceFromEndpoint(endpoint Endpoint) Service {
	credentialRef := strings.Replace(endpoint.CredentialRef, "local://endpoint/", "local://service/", 1)
	return Service{
		ID: endpoint.ID, Name: endpoint.Name, Kind: endpoint.Kind,
		Enabled: endpoint.Enabled, Models: cloneServiceModels(endpoint.Models),
		Capabilities: append([]Capability(nil), endpoint.Capabilities...),
		HTTP: &HTTPConnection{
			BaseURL: endpoint.BaseURL, Auth: endpoint.Auth,
			CredentialRef: credentialRef,
		},
	}
}

func (service Service) EndpointView() (Endpoint, error) {
	if !service.Kind.IsHTTP() || service.HTTP == nil {
		return Endpoint{}, fmt.Errorf("service %q is not an HTTP service", service.ID)
	}
	endpoint := Endpoint{
		ID: service.ID, Name: service.Name, Kind: service.Kind,
		BaseURL: service.HTTP.BaseURL, Auth: service.HTTP.Auth,
		CredentialRef: service.HTTP.CredentialRef, Enabled: service.Enabled,
		Models:       cloneServiceModels(service.Models),
		Capabilities: append([]Capability(nil), service.Capabilities...),
	}
	return endpoint, endpoint.Validate()
}

func validateServiceModels(models []string) error {
	return validateModelList("models", models)
}

func validateModelList(field string, models []string) error {
	if len(models) > MaxServiceModels {
		return fmt.Errorf("service %s must contain at most %d items", field, MaxServiceModels)
	}
	seen := make(map[string]struct{}, len(models))
	for index, model := range models {
		if model == "" || utf8.RuneCountInString(model) > 256 {
			return fmt.Errorf("%s[%d] must contain 1 to 256 characters", field, index)
		}
		if _, duplicate := seen[model]; duplicate {
			return fmt.Errorf("%s[%d] duplicates model %q", field, index, model)
		}
		seen[model] = struct{}{}
	}
	return nil
}

func cloneServiceModels(models []string) []string {
	if models == nil {
		return nil
	}
	return append([]string{}, models...)
}

// NormalizeServiceModels returns the canonical service-level model allow-list.
func NormalizeServiceModels(models []string) ([]string, error) {
	if models == nil {
		models = []string{}
	}
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		if _, duplicate := seen[model]; duplicate {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	if result == nil {
		result = []string{}
	}
	sort.Strings(result)
	if err := validateServiceModels(result); err != nil {
		return nil, err
	}
	return result, nil
}

func ServiceFromSubscriptionAccount(account SubscriptionAccount) Service {
	return Service{
		ID: account.ID, Name: account.DisplayName, Kind: account.Provider.ServiceKind(),
		Enabled:      account.Status != SubscriptionStatusDisconnected,
		Models:       []string{},
		Capabilities: append([]Capability(nil), account.Capabilities...),
		Subscription: &SubscriptionConnection{
			Provider: account.Provider, Status: account.Status, AccountHint: account.AccountHint,
			ProviderAccountID: account.ProviderAccountID, CredentialRef: account.CredentialRef,
			AuthorizationBoundary: account.AuthorizationBoundary, TokenExpiresAt: account.TokenExpiresAt,
			LastRefreshAt: account.LastRefreshAt, LastError: account.LastError,
		},
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}
}

func (service Service) SubscriptionAccountView() (SubscriptionAccount, error) {
	if !service.Kind.IsSubscription() || service.Subscription == nil {
		return SubscriptionAccount{}, fmt.Errorf("service %q is not a subscription service", service.ID)
	}
	account := SubscriptionAccount{
		ID: service.ID, Provider: service.Subscription.Provider, Status: service.Subscription.Status,
		DisplayName: service.Name, AccountHint: service.Subscription.AccountHint,
		ProviderAccountID:     service.Subscription.ProviderAccountID,
		CredentialRef:         service.Subscription.CredentialRef,
		Capabilities:          append([]Capability(nil), service.Capabilities...),
		AuthorizationBoundary: service.Subscription.AuthorizationBoundary,
		TokenExpiresAt:        service.Subscription.TokenExpiresAt, LastRefreshAt: service.Subscription.LastRefreshAt,
		LastError: service.Subscription.LastError, CreatedAt: service.CreatedAt, UpdatedAt: service.UpdatedAt,
	}
	return account, account.Validate()
}

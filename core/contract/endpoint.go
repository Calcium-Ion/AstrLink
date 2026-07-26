package contract

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

type EndpointID string

type EndpointKind string

const (
	EndpointKindNewAPI           EndpointKind = "newapi"
	EndpointKindOpenAI           EndpointKind = "openai"
	EndpointKindAnthropic        EndpointKind = "anthropic"
	EndpointKindGemini           EndpointKind = "gemini"
	EndpointKindOpenAICompatible EndpointKind = "openai_compatible"
	EndpointKindCustom           EndpointKind = "custom"
)

func (kind EndpointKind) Valid() bool {
	switch kind {
	case EndpointKindNewAPI, EndpointKindOpenAI, EndpointKindAnthropic,
		EndpointKindGemini, EndpointKindOpenAICompatible, EndpointKindCustom:
		return true
	default:
		return false
	}
}

// CapabilityMode states how an Endpoint accepts the original ingress
// protocol. RelayKit is intentionally not an Endpoint mode; it is a local
// execution-plan type.
type CapabilityMode string

const (
	CapabilityModeNative    CapabilityMode = "native"
	CapabilityModeDelegated CapabilityMode = "delegated"
)

func (mode CapabilityMode) Valid() bool {
	return mode == CapabilityModeNative || mode == CapabilityModeDelegated
}

type AuthScheme string

const (
	AuthSchemeNone            AuthScheme = "none"
	AuthSchemeBearer          AuthScheme = "bearer"
	AuthSchemeAnthropicAPIKey AuthScheme = "anthropic_api_key"
	AuthSchemeGoogleAPIKey    AuthScheme = "google_api_key"
	AuthSchemeCustomHeader    AuthScheme = "custom_header"
)

func (scheme AuthScheme) Valid() bool {
	switch scheme {
	case AuthSchemeNone, AuthSchemeBearer, AuthSchemeAnthropicAPIKey,
		AuthSchemeGoogleAPIKey, AuthSchemeCustomHeader:
		return true
	default:
		return false
	}
}

var headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
var forbiddenCustomAuthHeaders = map[string]struct{}{
	"Connection":          {},
	"Content-Length":      {},
	"Host":                {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}
var credentialNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
var credentialPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*[A-Za-z0-9._~!$&'()*+,;=:@-]$`)

// EndpointAuth is non-secret authentication configuration. Credential bytes
// remain in SecretStore and are referenced separately by CredentialRef.
type EndpointAuth struct {
	Scheme     AuthScheme `json:"scheme"`
	HeaderName string     `json:"header_name,omitempty"`
}

func (auth EndpointAuth) Validate() error {
	if !auth.Scheme.Valid() {
		return fmt.Errorf("unknown auth scheme %q", auth.Scheme)
	}
	if auth.Scheme == AuthSchemeCustomHeader {
		if auth.HeaderName == "" || len(auth.HeaderName) > 128 || !headerNamePattern.MatchString(auth.HeaderName) {
			return fmt.Errorf("custom_header auth requires a valid header_name")
		}
		if _, forbidden := forbiddenCustomAuthHeaders[http.CanonicalHeaderKey(auth.HeaderName)]; forbidden {
			return fmt.Errorf("custom_header auth cannot use reserved header %q", auth.HeaderName)
		}
		return nil
	}
	if auth.HeaderName != "" {
		return fmt.Errorf("header_name is only valid for custom_header auth")
	}
	return nil
}

type Capability struct {
	Protocol  ProtocolID     `json:"protocol"`
	Mode      CapabilityMode `json:"mode"`
	Streaming bool           `json:"streaming"`
	Models    []string       `json:"models,omitempty"`
}

func (capability Capability) Validate() error {
	if err := capability.Protocol.Validate(); err != nil {
		return err
	}
	if !capability.Mode.Valid() {
		return fmt.Errorf("unknown capability mode %q", capability.Mode)
	}
	if descriptor, known := LookupProtocolDescriptor(capability.Protocol); known && capability.Streaming && !descriptor.Streaming {
		return fmt.Errorf("protocol %q does not support streaming", capability.Protocol)
	}
	seenModels := make(map[string]struct{}, len(capability.Models))
	for index, model := range capability.Models {
		if model == "" || utf8.RuneCountInString(model) > 256 {
			return fmt.Errorf("models[%d] must contain 1 to 256 characters", index)
		}
		if _, ok := seenModels[model]; ok {
			return fmt.Errorf("models[%d] duplicates model %q", index, model)
		}
		seenModels[model] = struct{}{}
	}
	return nil
}

// ValidateCredentialRef applies the same storage-neutral contract used by
// SecretStore. Local references identify the Endpoint row whose credential is
// stored in the dedicated local table; keyring references remain available for
// optional future adapters.
func ValidateCredentialRef(value string) error {
	if len(value) > 512 {
		return fmt.Errorf("credential_ref exceeds 512 characters")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("parse credential_ref: %w", err)
	}
	if parsed.User != nil || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("credential_ref must not contain credentials, query, or fragment")
	}
	if !credentialNamespacePattern.MatchString(parsed.Host) || !credentialPathPattern.MatchString(parsed.Path) || strings.HasSuffix(parsed.Path, "/") {
		return fmt.Errorf("credential_ref must use local://endpoint/<id> or keyring://<namespace>/<id>")
	}
	switch parsed.Scheme {
	case "local":
		identifier := strings.TrimPrefix(parsed.Path, "/")
		if parsed.Host != "endpoint" || strings.Contains(identifier, "/") || EndpointID(identifier).Validate() != nil {
			return fmt.Errorf("credential_ref must use local://endpoint/<id> or keyring://<namespace>/<id>")
		}
	case "keyring":
		// Keyring namespaces and nested adapter-specific paths remain opaque.
	default:
		return fmt.Errorf("credential_ref must use local://endpoint/<id> or keyring://<namespace>/<id>")
	}
	return nil
}

// Endpoint contains only a credential reference. Secret material must be read
// through secretstore.SecretStore and must never be embedded in this contract.
type Endpoint struct {
	ID            EndpointID   `json:"id"`
	Name          string       `json:"name"`
	Kind          EndpointKind `json:"kind"`
	BaseURL       string       `json:"base_url"`
	Auth          EndpointAuth `json:"auth"`
	CredentialRef string       `json:"credential_ref,omitempty"`
	Enabled       bool         `json:"enabled"`
	Capabilities  []Capability `json:"capabilities"`
}

func (endpoint Endpoint) Validate() error {
	if err := endpoint.ID.Validate(); err != nil {
		return err
	}
	if endpoint.Name == "" || utf8.RuneCountInString(endpoint.Name) > 128 {
		return fmt.Errorf("endpoint name must contain 1 to 128 characters")
	}
	if !endpoint.Kind.Valid() {
		return fmt.Errorf("unknown endpoint kind %q", endpoint.Kind)
	}
	if err := endpoint.Auth.Validate(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if len(endpoint.BaseURL) > 2048 {
		return fmt.Errorf("endpoint base_url exceeds 2048 characters")
	}
	parsed, err := url.Parse(endpoint.BaseURL)
	if err != nil {
		return fmt.Errorf("parse endpoint base_url: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("endpoint base_url must be an absolute http(s) URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("endpoint base_url must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("endpoint base_url must not contain a query or fragment")
	}
	if endpoint.CredentialRef != "" {
		if err := ValidateCredentialRef(endpoint.CredentialRef); err != nil {
			return fmt.Errorf("endpoint %w", err)
		}
	}
	if endpoint.Capabilities == nil {
		return fmt.Errorf("endpoint capabilities must be a non-null array")
	}

	seenCapabilities := make(map[string]struct{}, len(endpoint.Capabilities))
	for index, capability := range endpoint.Capabilities {
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("capabilities[%d]: %w", index, err)
		}
		key := string(capability.Protocol) + "\x00" + string(capability.Mode)
		if _, exists := seenCapabilities[key]; exists {
			return fmt.Errorf("capabilities[%d]: duplicate protocol %q and mode %q", index, capability.Protocol, capability.Mode)
		}
		seenCapabilities[key] = struct{}{}
	}
	return nil
}

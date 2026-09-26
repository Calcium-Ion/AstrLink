package accountauth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	// minLearnedClaudeVersion is the first Claude Code release that sends the
	// session header; an older client cannot supply a current identity.
	minLearnedClaudeVersion = "2.1.87"
	// maxLearnedMajorSkew rejects a learned major version this far past the
	// baseline as a spoofed or corrupted identity.
	maxLearnedMajorSkew = 2
	maxLearnedHeaders   = 16
	maxLearnedUserAgent = 1024
	maxLearnedValue     = 256
	maxLearnedDocument  = 16 << 10
	// sameVersionRefreshInterval bounds how often a client of the same version
	// with a different header set may replace the learned identity.
	sameVersionRefreshInterval = 10 * time.Minute
)

// IdentitySettingsSource reads the persisted forwarding identity settings.
type IdentitySettingsSource interface {
	GetRoutingSettings(context.Context) (contract.RoutingSettings, error)
}

// LearnedIdentityStore persists one learned identity document per provider.
type LearnedIdentityStore interface {
	ListLearnedIdentities(context.Context) (map[contract.SubscriptionProvider][]byte, error)
	PutLearnedIdentity(context.Context, contract.SubscriptionProvider, []byte) error
}

// IdentityRegistry resolves the Claude Code and Codex identity AstrLink sends
// when it must supply one, and learns it from recognized official clients.
// The base identity is the learned one when auto-learn is on, otherwise the
// baseline; the configured version is a floor on the version it declares. A
// nil registry resolves the baseline and learns nothing.
type IdentityRegistry struct {
	settings  IdentitySettingsSource
	store     LearnedIdentityStore
	now       func() time.Time
	persistMu sync.Mutex
	mu        sync.RWMutex
	learned   map[contract.SubscriptionProvider]ClientIdentity
	learnedAt map[contract.SubscriptionProvider]time.Time
}

func NewIdentityRegistry(settings IdentitySettingsSource, store LearnedIdentityStore) *IdentityRegistry {
	return &IdentityRegistry{
		settings:  settings,
		store:     store,
		now:       time.Now,
		learned:   make(map[contract.SubscriptionProvider]ClientIdentity),
		learnedAt: make(map[contract.SubscriptionProvider]time.Time),
	}
}

// Hydrate loads the persisted identities, dropping any document that no
// longer passes the learning guardrails.
func (registry *IdentityRegistry) Hydrate(ctx context.Context) error {
	if registry == nil || registry.store == nil {
		return nil
	}
	documents, err := registry.store.ListLearnedIdentities(ctx)
	if err != nil {
		return err
	}
	learned := make(map[contract.SubscriptionProvider]ClientIdentity, len(documents))
	for provider, document := range documents {
		if identity, ok := decodeLearnedIdentity(provider, document); ok {
			learned[provider] = identity
		}
	}
	registry.mu.Lock()
	registry.learned = learned
	registry.learnedAt = make(map[contract.SubscriptionProvider]time.Time)
	registry.mu.Unlock()
	return nil
}

// ClaudeIdentity resolves the Claude Code identity for settings.
func (registry *IdentityRegistry) ClaudeIdentity(settings contract.RoutingSettings) ClientIdentity {
	base := DefaultClaudeIdentity()
	if settings.ClaudeIdentityAutoLearn {
		if learned, ok := registry.learnedIdentity(contract.SubscriptionProviderClaudeCode); ok {
			base = learned
		}
	}
	return withVersionFloor(base, settings.ClaudeIdentityVersion)
}

// CodexIdentity resolves the Codex identity for settings. baselineVersion is
// the configured baseline release; an invalid one uses the default release.
func (registry *IdentityRegistry) CodexIdentity(settings contract.RoutingSettings, baselineVersion string) ClientIdentity {
	base := codexIdentityAt(baselineVersion)
	if settings.CodexIdentityAutoLearn {
		if learned, ok := registry.learnedIdentity(contract.SubscriptionProviderOpenAICodex); ok {
			base = learned
		}
	}
	return withVersionFloor(base, settings.CodexIdentityVersion)
}

// ClaudeIdentityFor resolves with the persisted settings, for requests that
// AstrLink originates. A failed read resolves with the defaults.
func (registry *IdentityRegistry) ClaudeIdentityFor(ctx context.Context) ClientIdentity {
	return registry.ClaudeIdentity(registry.routingSettings(ctx))
}

// CodexIdentityFor is ClaudeIdentityFor for Codex.
func (registry *IdentityRegistry) CodexIdentityFor(ctx context.Context, baselineVersion string) ClientIdentity {
	return registry.CodexIdentity(registry.routingSettings(ctx), baselineVersion)
}

// LearnClaude records the identity of a recognized official Claude Code
// request. It reports whether the learned identity changed; an error means
// the change could not be persisted and lasts until restart.
func (registry *IdentityRegistry) LearnClaude(ctx context.Context, header http.Header) (bool, error) {
	if registry == nil {
		return false, nil
	}
	identity, ok := claudeIdentityFromHeaders(header)
	if !ok {
		return false, nil
	}
	return registry.learn(ctx, contract.SubscriptionProviderClaudeCode, identity)
}

// LearnCodex records the identity of a recognized official Codex request.
func (registry *IdentityRegistry) LearnCodex(ctx context.Context, header http.Header) (bool, error) {
	if registry == nil {
		return false, nil
	}
	identity, ok := codexIdentityFromHeaders(header)
	if !ok {
		return false, nil
	}
	return registry.learn(ctx, contract.SubscriptionProviderOpenAICodex, identity)
}

func (registry *IdentityRegistry) routingSettings(ctx context.Context) contract.RoutingSettings {
	if registry == nil || registry.settings == nil {
		return contract.DefaultRoutingSettings()
	}
	settings, err := registry.settings.GetRoutingSettings(ctx)
	if err != nil {
		return contract.DefaultRoutingSettings()
	}
	return settings
}

func (registry *IdentityRegistry) learnedIdentity(provider contract.SubscriptionProvider) (ClientIdentity, bool) {
	if registry == nil {
		return ClientIdentity{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	identity, ok := registry.learned[provider]
	return identity.clone(), ok
}

func (registry *IdentityRegistry) learn(ctx context.Context, provider contract.SubscriptionProvider, identity ClientIdentity) (bool, error) {
	if !validLearnedIdentity(provider, identity) {
		return false, nil
	}
	// Repeated requests from the same client end on this read-locked check.
	registry.mu.RLock()
	replace := registry.replacesLocked(provider, identity)
	registry.mu.RUnlock()
	if !replace {
		return false, nil
	}
	registry.persistMu.Lock()
	defer registry.persistMu.Unlock()
	registry.mu.Lock()
	if !registry.replacesLocked(provider, identity) {
		registry.mu.Unlock()
		return false, nil
	}
	registry.learned[provider] = identity.clone()
	registry.learnedAt[provider] = registry.now()
	registry.mu.Unlock()
	if registry.store == nil {
		return true, nil
	}
	document, err := json.Marshal(identity)
	if err != nil {
		return true, err
	}
	// The request may end before the write; the identity outlives it.
	return true, registry.store.PutLearnedIdentity(context.WithoutCancel(ctx), provider, document)
}

// replacesLocked keeps learning one-way: a newer version replaces the learned
// identity, the same version with other headers refreshes it at a bounded
// rate, and an older version never does.
func (registry *IdentityRegistry) replacesLocked(provider contract.SubscriptionProvider, identity ClientIdentity) bool {
	current, ok := registry.learned[provider]
	if !ok {
		return true
	}
	order, valid := contract.CompareClientVersions(identity.Version, current.Version)
	switch {
	case !valid || order < 0:
		return false
	case order > 0:
		return true
	}
	if reflect.DeepEqual(identity, current) {
		return false
	}
	learnedAt, ok := registry.learnedAt[provider]
	return !ok || registry.now().Sub(learnedAt) >= sameVersionRefreshInterval
}

func (identity ClientIdentity) clone() ClientIdentity {
	if identity.Headers != nil {
		headers := make(map[string]string, len(identity.Headers))
		for name, value := range identity.Headers {
			headers[name] = value
		}
		identity.Headers = headers
	}
	return identity
}

// withVersionFloor raises the declared version to floor when floor is newer,
// rewriting only the version segment of the User-Agent. Every other field
// stays with the base identity.
func withVersionFloor(identity ClientIdentity, floor string) ClientIdentity {
	if order, ok := contract.CompareClientVersions(floor, identity.Version); !ok || order <= 0 {
		return identity
	}
	product, rest, _ := strings.Cut(identity.UserAgent, "/")
	identity.UserAgent = product + "/" + floor
	if _, tail, found := strings.Cut(rest, " "); found {
		identity.UserAgent += " " + tail
	}
	identity.Version = floor
	return identity
}

func claudeIdentityFromHeaders(header http.Header) (ClientIdentity, bool) {
	ua, version := recognizedClientIdentity(header, strings.TrimSuffix(ClaudeUserAgentPrefix, "/"))
	if ua == "" {
		return ClientIdentity{}, false
	}
	identity := ClientIdentity{UserAgent: ua, Version: version, Headers: make(map[string]string)}
	for name, values := range header {
		name = http.CanonicalHeaderKey(name)
		if !learnedClaudeHeader(name) {
			continue
		}
		if len(values) != 1 {
			return ClientIdentity{}, false
		}
		value := strings.TrimSpace(values[0])
		// A retry count describes one attempt, not the client.
		if name == "X-Stainless-Retry-Count" {
			value = "0"
		}
		identity.Headers[name] = value
	}
	return identity, true
}

func codexIdentityFromHeaders(header http.Header) (ClientIdentity, bool) {
	ua, name, version, ok := recognizedCodexClient(header)
	if !ok || strings.TrimSpace(header.Get("originator")) != name {
		return ClientIdentity{}, false
	}
	return ClientIdentity{UserAgent: ua, Version: version, Headers: map[string]string{"originator": name}}, true
}

// learnedClaudeHeader selects the client fingerprint that accompanies the
// Claude Code User-Agent. The streaming helper marker belongs to one call.
func learnedClaudeHeader(name string) bool {
	switch name {
	case "X-App", "Anthropic-Dangerous-Direct-Browser-Access":
		return true
	case "X-Stainless-Helper-Method":
		return false
	}
	return strings.HasPrefix(name, "X-Stainless-")
}

func decodeLearnedIdentity(provider contract.SubscriptionProvider, document []byte) (ClientIdentity, bool) {
	if len(document) > maxLearnedDocument {
		return ClientIdentity{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var identity ClientIdentity
	if decoder.Decode(&identity) != nil || decoder.More() {
		return ClientIdentity{}, false
	}
	if !validLearnedIdentity(provider, identity) {
		return ClientIdentity{}, false
	}
	return identity, true
}

// validLearnedIdentity applies the learning guardrails to a captured or
// stored identity: a supported, bounded, printable, self-consistent tuple
// within the provider's version range that carries no gateway branding.
func validLearnedIdentity(provider contract.SubscriptionProvider, identity ClientIdentity) bool {
	var product, floor, baseline string
	switch provider {
	case contract.SubscriptionProviderClaudeCode:
		product, floor, baseline = strings.TrimSuffix(ClaudeUserAgentPrefix, "/"), minLearnedClaudeVersion, claudeCLIVersion
		// A replayed User-Agent needs the SDK fingerprint it shipped with.
		if identity.Headers["X-Stainless-Package-Version"] == "" {
			return false
		}
	case contract.SubscriptionProviderOpenAICodex:
		product, floor, baseline = identity.Headers["originator"], contract.MinCodexClientVersion, DefaultCodexModelsClientVersion
		// The originator is the only header, keyed as ApplyCodexAuthIdentity reads it.
		if _, _, _, ok := recognizedCodexClient(http.Header{"User-Agent": {identity.UserAgent}}); !ok || len(identity.Headers) != 1 || product == "" {
			return false
		}
	default:
		return false
	}
	if len(identity.UserAgent) > maxLearnedUserAgent || !learnedText(identity.UserAgent) {
		return false
	}
	name, rest, _ := strings.Cut(identity.UserAgent, "/")
	declared, _, _ := strings.Cut(rest, " ")
	if name != product || declared != identity.Version {
		return false
	}
	if order, ok := contract.CompareClientVersions(identity.Version, floor); !ok || order < 0 {
		return false
	}
	major, _ := contract.ClientVersionMajor(identity.Version)
	baselineMajor, _ := contract.ClientVersionMajor(baseline)
	if major > baselineMajor+maxLearnedMajorSkew {
		return false
	}
	if len(identity.Headers) > maxLearnedHeaders {
		return false
	}
	for name, value := range identity.Headers {
		if len(value) > maxLearnedValue || value == "" || !learnedText(value) {
			return false
		}
		if provider == contract.SubscriptionProviderClaudeCode && (name != http.CanonicalHeaderKey(name) || !learnedClaudeHeader(name)) {
			return false
		}
	}
	return true
}

// learnedText accepts printable ASCII without gateway branding, so a replayed
// identity never names AstrLink upstream.
func learnedText(value string) bool {
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return !strings.Contains(strings.ToLower(value), "astrlink")
}

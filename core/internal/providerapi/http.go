// Package providerapi selects a provider's native HTTP surface without
// converting request or response bodies. Keep the source matrix in
// docs/guides/pay-as-you-go-providers.md in sync with these paths.
package providerapi

import (
	"net/url"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// BaseURL replaces only the provider API suffix, retaining the configured
// origin, region and any reverse-proxy prefix (including escaped segments).
func BaseURL(kind contract.ServiceKind, protocol contract.ProtocolID, base *url.URL) *url.URL {
	var roots []string
	var target string
	switch kind {
	case contract.ServiceKindDeepSeek, contract.ServiceKindMoonshot, contract.ServiceKindMiniMax:
		roots = []string{"/anthropic/v1", "/anthropic", "/v1"}
		switch protocol {
		case contract.ProtocolAnthropicMessages:
			target = "/anthropic"
		case contract.ProtocolOpenAIResponses:
			if kind != contract.ServiceKindDeepSeek {
				target = "/v1"
			}
		case contract.ProtocolOpenAIChat, contract.ProtocolOpenAIModels:
			target = "/v1"
		default:
			return base
		}
	case contract.ServiceKindQwen:
		roots = []string{"/api/v2/apps/protocols/compatible-mode/v1", "/compatible-mode/v1", "/apps/anthropic/v1", "/apps/anthropic"}
		switch protocol {
		case contract.ProtocolAnthropicMessages:
			target = "/apps/anthropic"
		case contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses:
			target = "/compatible-mode/v1"
		default:
			return base
		}
	case contract.ServiceKindGLM:
		roots = []string{"/api/paas/v4", "/api/anthropic/v1", "/api/anthropic"}
		switch protocol {
		case contract.ProtocolAnthropicMessages:
			target = "/api/anthropic"
		case contract.ProtocolOpenAIChat:
			target = "/api/paas/v4"
		default:
			return base
		}
	case contract.ServiceKindDoubao:
		roots = []string{"/api/compatible/v1", "/api/compatible", "/api/v3"}
		switch protocol {
		case contract.ProtocolAnthropicMessages:
			target = "/api/compatible"
		case contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses:
			target = "/api/v3"
		default:
			return base
		}
	default:
		return base
	}

	prefix := strings.TrimRight(base.EscapedPath(), "/")
	for _, root := range roots {
		if strings.HasSuffix(prefix, root) {
			prefix = strings.TrimSuffix(prefix, root)
			break
		}
	}
	updated := *base
	updated.RawPath = prefix + target
	updated.Path, _ = url.PathUnescape(updated.RawPath) // EscapedPath is always valid.
	if updated.RawPath == updated.Path {
		updated.RawPath = ""
	}
	return &updated
}

// RequestURL strips AstrLink's local version only where the provider uses a
// different version/root. Anthropic's /v1/messages must retain its version.
func RequestURL(kind contract.ServiceKind, protocol contract.ProtocolID, incoming *url.URL) *url.URL {
	stripVersion := (kind == contract.ServiceKindDeepSeek && protocol == contract.ProtocolOpenAIResponses) ||
		((kind == contract.ServiceKindGLM || kind == contract.ServiceKindDoubao) && protocol != contract.ProtocolAnthropicMessages)
	if !stripVersion || !strings.HasPrefix(incoming.Path, "/v1/") {
		return incoming
	}
	updated := *incoming
	updated.Path = strings.TrimPrefix(updated.Path, "/v1")
	if updated.RawPath != "" {
		updated.RawPath = strings.TrimPrefix(updated.RawPath, "/v1")
	}
	return &updated
}

// Auth selects the documented Messages authentication for API presets whose
// default Chat authentication is Bearer. Explicit custom auth is preserved.
func Auth(kind contract.ServiceKind, protocol contract.ProtocolID, configured contract.ServiceAuth) contract.ServiceAuth {
	if protocol == contract.ProtocolAnthropicMessages && configured.Scheme == contract.AuthSchemeBearer {
		switch kind {
		case contract.ServiceKindDeepSeek, contract.ServiceKindGLM, contract.ServiceKindDoubao:
			return contract.ServiceAuth{Scheme: contract.AuthSchemeAnthropicAPIKey}
		}
	}
	return configured
}

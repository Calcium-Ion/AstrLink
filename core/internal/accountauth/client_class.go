package accountauth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ClientClass records how a subscription request's originating client was
// recognized. An official Claude Code or Codex CLI request keeps its own
// identity; every other class keeps the enforced default behavior.
type ClientClass int

const (
	// ClientClassUnknown is a gateway-initiated request with no client marker.
	ClientClassUnknown ClientClass = iota
	// ClientClassThirdParty is a native-protocol request whose client identity
	// does not match an official CLI.
	ClientClassThirdParty
	// ClientClassOfficial is a recognized official Claude Code or Codex CLI
	// request, forwarded untouched.
	ClientClassOfficial
	// ClientClassConverted was produced by local protocol conversion.
	ClientClassConverted
)

type clientClassKey struct{}

// WithClientClass records the recognized class for the authorizer to read. It
// is set per attempt because a converted candidate and a native candidate of
// the same request are classified differently.
func WithClientClass(ctx context.Context, class ClientClass) context.Context {
	return context.WithValue(ctx, clientClassKey{}, class)
}

// ClientClassFrom reports the recognized class, or ClientClassUnknown when no
// marker was set.
func ClientClassFrom(ctx context.Context) ClientClass {
	if ctx == nil {
		return ClientClassUnknown
	}
	class, _ := ctx.Value(clientClassKey{}).(ClientClass)
	return class
}

// maxUserIDScan bounds the metadata.user_id recognition read of a request body.
const maxUserIDScan = 1 << 20

// RecognizedClaudeOfficialHeaders reports the header half of an official Claude
// Code identity: a versioned claude-cli User-Agent, the CLI app marker or the
// session header, and the claude-code feature beta. ClaudeMetadataUserIDRecognized
// completes the check against the request body.
func RecognizedClaudeOfficialHeaders(header http.Header) bool {
	if header == nil {
		return false
	}
	if ua, _ := recognizedClientIdentity(header, "claude-cli"); ua == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(header.Get("X-App")), "cli") &&
		strings.TrimSpace(header.Get(ClaudeCodeSessionHeader)) == "" {
		return false
	}
	return anthropicBetaContains(header.Values("Anthropic-Beta"), "claude-code-20250219")
}

// ClaudeMetadataUserIDRecognized reports whether the request body's
// metadata.user_id is a Claude Code identifier, in the JSON or legacy form.
func ClaudeMetadataUserIDRecognized(body []byte) bool {
	if len(body) == 0 || len(body) > maxUserIDScan {
		return false
	}
	var payload struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	id := strings.TrimSpace(payload.Metadata.UserID)
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, "{") {
		return json.Valid([]byte(id))
	}
	_, legacy := parseClaudeUserID(id)
	return legacy
}

// RecognizedCodexOfficialClient reports a genuine Codex CLI identity: a
// supported client User-Agent, an originator equal to that client's product
// name, and a session-id header. Codex classification needs no request body.
func RecognizedCodexOfficialClient(header http.Header) bool {
	if header == nil {
		return false
	}
	_, name, _, ok := recognizedCodexClient(header)
	if !ok {
		return false
	}
	if strings.TrimSpace(header.Get("originator")) != name {
		return false
	}
	return strings.TrimSpace(header.Get("Session-Id")) != ""
}

// splitAnthropicBetas flattens the comma-separated Anthropic-Beta header values
// into individual, trimmed feature betas, preserving their order.
func splitAnthropicBetas(values []string) []string {
	var betas []string
	for _, value := range values {
		for _, beta := range strings.Split(value, ",") {
			if beta = strings.TrimSpace(beta); beta != "" {
				betas = append(betas, beta)
			}
		}
	}
	return betas
}

func anthropicBetaContains(values []string, target string) bool {
	for _, beta := range splitAnthropicBetas(values) {
		if beta == target {
			return true
		}
	}
	return false
}

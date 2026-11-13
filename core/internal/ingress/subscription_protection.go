package ingress

import (
	"context"
	"log"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// subscriptionProtection holds the Routing switches of the subscription
// account protections for one request.
type subscriptionProtection struct {
	risk                bool
	codexRequests       bool
	claudeRequests      bool
	sessionIsolation    bool
	claudeIdentity      bool
	codexIdentity       bool
	grokIdentity        bool
	officialPassthrough bool
}

func subscriptionProtectionFrom(settings contract.RoutingSettings) subscriptionProtection {
	return subscriptionProtection{
		risk:                settings.SubscriptionRiskProtection,
		codexRequests:       settings.CodexRequestNormalization,
		claudeRequests:      settings.ClaudeRequestNormalization,
		sessionIsolation:    settings.SubscriptionSessionIsolation,
		claudeIdentity:      settings.ClaudeIdentityEnforcement,
		codexIdentity:       settings.CodexIdentityEnforcement,
		grokIdentity:        settings.GrokIdentityEnforcement,
		officialPassthrough: settings.OfficialClientPassthrough,
	}
}

// sendsClientIdentity reports whether an attempt to a subscription provider
// leaves under the subscription's own client identity instead of the
// caller's, as the authorizer decides: Codex and Claude unless an official
// client passes through, Grok while its enforcement is on, and the providers
// that always present one client.
func (protection subscriptionProtection) sendsClientIdentity(kind contract.ServiceKind, class accountauth.ClientClass) bool {
	official := protection.officialPassthrough && class == accountauth.ClientClassOfficial
	switch kind {
	case contract.ServiceKindCodexSubscription:
		return protection.codexIdentity && !official
	case contract.ServiceKindClaudeSubscription:
		return protection.claudeIdentity && !official
	case contract.ServiceKindGrokSubscription:
		return protection.grokIdentity
	case contract.ServiceKindAntigravitySubscription, contract.ServiceKindCopilotSubscription, contract.ServiceKindDroidSubscription:
		return true
	default:
		return false
	}
}

// learnClientIdentities records the identity of a recognized official client
// from its own request, whichever provider serves it: the headers describe
// the client, not the route, so a Claude Code request routed to an API key,
// a relay or an official account teaches the same identity. A client is
// recognized only in a protocol it sends itself. A failed write keeps the
// identity in memory until restart and is only logged: learning never fails
// the request.
func (handler *Handler) learnClientIdentities(ctx context.Context, classified Request, header http.Header, settings contract.RoutingSettings) {
	if handler.identities == nil {
		return
	}
	protocol := classified.Protocol
	if protocol == contract.ProtocolAnthropicMessages && settings.ClaudeIdentityAutoLearn &&
		classified.claudeCodeUserID && accountauth.RecognizedClaudeOfficialHeaders(header) {
		handler.learnClientIdentity(ctx, contract.SubscriptionProviderClaudeCode, handler.identities.LearnClaude, header)
	}
	if (protocol == contract.ProtocolOpenAIResponses || protocol == contract.ProtocolOpenAIResponsesCompact) &&
		settings.CodexIdentityAutoLearn && accountauth.RecognizedCodexOfficialClient(header) {
		handler.learnClientIdentity(ctx, contract.SubscriptionProviderOpenAICodex, handler.identities.LearnCodex, header)
	}
	// LearnGrok recognizes the Grok client from its User-Agent itself.
	if (protocol == contract.ProtocolOpenAIResponses || protocol == contract.ProtocolOpenAIChat) && settings.GrokIdentityAutoLearn {
		handler.learnClientIdentity(ctx, contract.SubscriptionProviderXAIGrok, handler.identities.LearnGrok, header)
	}
}

func (handler *Handler) learnClientIdentity(
	ctx context.Context,
	provider contract.SubscriptionProvider,
	learn func(context.Context, http.Header) (bool, error),
	header http.Header,
) {
	if _, err := learn(ctx, header); err != nil {
		logf := handler.recordLogger
		if logf == nil {
			logf = log.Printf
		}
		logf("learned %s client identity was not persisted: %v", provider, err)
	}
}

// requestRoutingSettings reuses the settings the request already read. A
// store without routing settings, or a failed read, yields the defaults:
// every protection on, no global prompt and no reasoning override. Turning a
// protection off is always an explicit choice.
func (handler *Handler) requestRoutingSettings(ctx context.Context) contract.RoutingSettings {
	if session := recordSessionFromContext(ctx); session != nil && session.routingSettings != nil {
		return *session.routingSettings
	}
	settings, loaded, err := handler.loadRoutingSettings(ctx)
	if err != nil || !loaded {
		settings = contract.DefaultRoutingSettings()
	}
	return settings
}

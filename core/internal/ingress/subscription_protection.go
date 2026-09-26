package ingress

import (
	"context"
	"log"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
)

// subscriptionProtection holds the Routing switches of the subscription
// account protections for one request.
type subscriptionProtection struct {
	risk                bool
	codexRequests       bool
	claudeRequests      bool
	sessionIsolation    bool
	claudeIdentity      bool
	officialPassthrough bool
	claudeAutoLearn     bool
	codexAutoLearn      bool
}

func subscriptionProtectionFrom(settings contract.RoutingSettings) subscriptionProtection {
	return subscriptionProtection{
		risk:                settings.SubscriptionRiskProtection,
		codexRequests:       settings.CodexRequestNormalization,
		claudeRequests:      settings.ClaudeRequestNormalization,
		sessionIsolation:    settings.SubscriptionSessionIsolation,
		claudeIdentity:      settings.ClaudeIdentityEnforcement,
		officialPassthrough: settings.OfficialClientPassthrough,
		claudeAutoLearn:     settings.ClaudeIdentityAutoLearn,
		codexAutoLearn:      settings.CodexIdentityAutoLearn,
	}
}

// learnClientIdentity records the identity of a recognized official client.
// A failed write keeps the identity in memory until restart and is only
// logged: learning never fails the request.
func (handler *Handler) learnClientIdentity(ctx context.Context, provider contract.SubscriptionProvider, header http.Header) {
	learn := handler.identities.LearnClaude
	if provider == contract.SubscriptionProviderOpenAICodex {
		learn = handler.identities.LearnCodex
	}
	if _, err := learn(ctx, header); err != nil {
		logf := handler.recordLogger
		if logf == nil {
			logf = log.Printf
		}
		logf("learned %s client identity was not persisted: %v", provider, err)
	}
}

// subscriptionProtection reuses the settings the request already read. A
// store without routing settings, or a failed read, keeps every protection
// on: turning one off is always an explicit choice.
func (handler *Handler) subscriptionProtection(ctx context.Context) subscriptionProtection {
	if session := recordSessionFromContext(ctx); session != nil && session.routingSettings != nil {
		return subscriptionProtectionFrom(*session.routingSettings)
	}
	settings, loaded, err := handler.loadRoutingSettings(ctx)
	if err != nil || !loaded {
		settings = contract.DefaultRoutingSettings()
	}
	return subscriptionProtectionFrom(settings)
}

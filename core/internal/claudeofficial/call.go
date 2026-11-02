// Package claudeofficial serves official Claude subscription accounts through
// the user's own claude CLI. Each conversation runs in one CLI process signed
// in to the account's config directory; the caller's tools are offered to it
// through an in-process MCP server and their calls are answered by the
// caller's next request. The CLI's own model calls come back through AstrLink
// on loopback, carrying a per-process secret, so the account's network proxy,
// request records and risk handling apply to the real upstream exchange.
package claudeofficial

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ProcessHeader carries a serving process's secret on its loopback requests.
// It is in the gateway's reserved namespace, which the transport strips
// before any upstream request.
const ProcessHeader = "X-AstrLink-Claude-Process"

// Call is the outer request a turn serves.
type Call struct {
	Service contract.Service
	// BaseURL is where the account's loopback requests go upstream.
	BaseURL string
	// RecordID is the outer request record; loopback records are its
	// children.
	RecordID contract.RequestID
	// AccessTokenID is the local access token the outer request used.
	AccessTokenID contract.AccessTokenID
	// SessionKey identifies the client conversation when the client names
	// one; empty leaves matching to the history alone.
	SessionKey string
}

type callKey struct{}

// WithCall attaches the outer request a forward serves.
func WithCall(ctx context.Context, call Call) context.Context {
	return context.WithValue(ctx, callKey{}, call)
}

// CallFrom returns the call WithCall attached.
func CallFrom(ctx context.Context) (Call, bool) {
	call, ok := ctx.Value(callKey{}).(Call)
	return call, ok
}

// Official reports whether a service is a Claude subscription in official
// mode.
func Official(service contract.Service) bool {
	return service.Kind == contract.ServiceKindClaudeSubscription && service.Subscription != nil &&
		service.Subscription.Mode == contract.SubscriptionModeOfficial
}

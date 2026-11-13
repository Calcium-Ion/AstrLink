package contract

// GatewayRewrite names a change the gateway made to a request before it left
// for the provider.
type GatewayRewrite string

const (
	// GatewayRewriteModelRedirect sent another model than the client asked
	// for, by a global or a provider redirect.
	GatewayRewriteModelRedirect GatewayRewrite = "model_redirect"
	// GatewayRewritePrivacy replaced personal data with placeholders.
	GatewayRewritePrivacy GatewayRewrite = "privacy"
	// GatewayRewriteImageCompression re-encoded at least one image.
	GatewayRewriteImageCompression GatewayRewrite = "image_compression"
	// GatewayRewriteSystemPrompt added the global system prompt.
	GatewayRewriteSystemPrompt GatewayRewrite = "global_prompt"
	// GatewayRewriteReasoning changed the reasoning level the client sent.
	GatewayRewriteReasoning GatewayRewrite = "reasoning_effort"
	// GatewayRewriteConversion converted the request to another protocol.
	GatewayRewriteConversion GatewayRewrite = "conversion"
	// GatewayRewriteClientIdentity sent the request under the subscription's
	// own client identity instead of the caller's.
	GatewayRewriteClientIdentity GatewayRewrite = "client_identity"
)

// GatewayRewrites lists every rewrite in the order the gateway applies them:
// the first five before routing, the last two for the chosen provider.
var GatewayRewrites = []GatewayRewrite{
	GatewayRewriteModelRedirect,
	GatewayRewritePrivacy,
	GatewayRewriteImageCompression,
	GatewayRewriteSystemPrompt,
	GatewayRewriteReasoning,
	GatewayRewriteConversion,
	GatewayRewriteClientIdentity,
}

// GatewayRewriteDiagnostic is the request diagnostic code that records a
// rewrite the record has no field for; its path names the rewrite.
const GatewayRewriteDiagnostic = "gateway_rewrite"

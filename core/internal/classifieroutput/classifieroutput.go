// Package classifieroutput turns one model generation reply into a bounded,
// typed classification fact. It is the shared extraction core for every
// classifier source that talks to an ordinary model: production routing and
// the control-plane dry-run both call the same three layers.
//
// Layer 1 (protocol.go) unpacks a gateway-native response into the single
// assistant-visible text. Layer 2 (extract.go) accepts exactly one complete
// JSON object under strict limits. Layer 3 (schema.go) validates fields
// against the calling catalog snapshot. Every failure is a typed error the
// caller maps onto its fail-open fallback; this package never blocks a
// request itself.
//
// Extraction is deliberately intolerant: no fence stripping, no "first { to
// last }", no auto-repair. The classification input is itself often JSON, and
// a lenient extractor would mistake input for the answer.
package classifieroutput

// Limits mirror the cloud provider envelope so every classifier source shares
// one budget shape. They bound untrusted output before any decoding work.
const (
	// MaxResponseBytes bounds one model reply, matching the cloud classifier
	// envelope; anything larger is a misrouted endpoint.
	MaxResponseBytes = 1 << 20
	// MaxJSONRunes bounds the assistant text handed to the strict JSON layer.
	MaxJSONRunes = 64 * 1024
	// MaxDepth bounds nested JSON structures.
	MaxDepth = 16
	// MaxObjectKeys bounds one JSON object's key count.
	MaxObjectKeys = 64
	// MaxCandidates bounds the ranked candidate list; the schema layer also
	// caps it at the catalog size.
	MaxCandidates = 64
)

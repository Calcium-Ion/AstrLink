package privacy

// astrlinkIdentifierShape is one identifier format AstrLink mints itself: a
// fixed prefix followed by a fixed number of lowercase hex digits.
type astrlinkIdentifierShape struct {
	prefix    string
	hexDigits int
}

// astrlinkIdentifierShapes are the identifiers AstrLink generates that name a
// record, token, model, or device without granting access to anything. They
// reach requests when an agent reads AstrLink's own records through
// astrlink-cli or the control API, and a model that learned to mask random
// hex masks them too, which breaks the agent's own lookups.
//
// Each entry mirrors one generator; if a generator changes its shape, its
// identifiers are masked again rather than leaked. Credentials are
// deliberately absent: the local access token (astr_ followed by base64url)
// and the raw grant token authenticate by themselves and stay protected.
var astrlinkIdentifierShapes = [...]astrlinkIdentifierShape{
	// accesstoken.Manager: the id that attributes requests to a local access
	// token; the token itself is never derived from it.
	{prefix: "access_token_", hexDigits: 32},
	// automodel.InstallationID: a digest of the classifier repo and revision.
	{prefix: "classifier_", hexDigits: 32},
	// The sync device identity, named in the remote lock.
	{prefix: "device_", hexDigits: 32},
	// privacymodel.InstallationID: contract.PrivacyModelID, a digest.
	{prefix: "model_", hexDigits: 32},
	// A raw audit grant's id; the operator decides by it, and the agent
	// presents the separate grant token.
	{prefix: "rawgrant_", hexDigits: 16},
	// The request id on ingress and control API error responses.
	{prefix: "req_", hexDigits: 24},
	// Request record and session ids. A call that opened no session reuses
	// its request id as the session id.
	{prefix: "request_", hexDigits: 24},
	{prefix: "session_", hexDigits: 24},
	// Service ids for subscription accounts and for services the control API
	// creates.
	{prefix: "service_", hexDigits: 16},
	{prefix: "service_", hexDigits: 24},
}

// isAstrLinkIdentifier reports whether value is exactly one identifier
// AstrLink generates, with nothing before or after it.
func isAstrLinkIdentifier(value string) bool {
	for _, shape := range astrlinkIdentifierShapes {
		if len(value) == len(shape.prefix)+shape.hexDigits &&
			value[:len(shape.prefix)] == shape.prefix &&
			isLowerHex(value[len(shape.prefix):]) {
			return true
		}
	}
	return false
}

// isAstrLinkIdentifierSpan reports whether the span text[start:end] covers an
// AstrLink identifier and nothing else. A model span may carry the quotes or
// whitespace around the value, or cover only part of it, such as the hex after
// the prefix. The identifier is therefore the whole run of word characters
// around the span, and that run must match a shape exactly: a value with a
// longer tail, another prefix, or uppercase hex stays a finding.
func isAstrLinkIdentifierSpan(text string, start, end int) bool {
	for start < end && isIdentifierSpanPadding(text[start]) {
		start++
	}
	for end > start && isIdentifierSpanPadding(text[end-1]) {
		end--
	}
	if start == end {
		return false
	}
	for index := start; index < end; index++ {
		if !isIdentifierWordByte(text[index]) {
			return false
		}
	}
	// The letter of an escape sequence such as \n belongs to the escape, not
	// to the word that follows it.
	for start > 0 && isIdentifierWordByte(text[start-1]) &&
		(start < 2 || text[start-2] != '\\') {
		start--
	}
	for end < len(text) && isIdentifierWordByte(text[end]) {
		end++
	}
	return isAstrLinkIdentifier(text[start:end])
}

func isIdentifierSpanPadding(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '\\':
		return true
	default:
		return false
	}
}

func isIdentifierWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == '-'
}

func isLowerHex(value string) bool {
	for index := 0; index < len(value); index++ {
		if (value[index] < '0' || value[index] > '9') && (value[index] < 'a' || value[index] > 'f') {
			return false
		}
	}
	return value != ""
}

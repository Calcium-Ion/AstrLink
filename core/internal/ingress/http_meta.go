package ingress

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// HTTP metadata capture (ADR 0008): the redacted method / URL / header
// envelope stored alongside opt-in body blobs. Redaction happens here, at
// capture time, so plaintext credentials never reach the persistence layer.
//
// Precedence per header name: allowlist → denylist → name heuristic → keep.
// The ordering is load-bearing: x-ratelimit-remaining-tokens contains
// "token" and would be falsely masked without the allowlist prefixes.

const (
	maxCapturedHeaders     = 64
	maxCapturedHeaderValue = 1024
	maxCapturedURLLength   = 2048
)

// Kept verbatim even when the heuristic would flag them — these are the
// debug payload this capture exists for.
var headerAllowlist = map[string]struct{}{
	"retry-after":          {},
	"x-request-id":         {},
	"request-id":           {},
	"openai-processing-ms": {},
	"openai-version":       {},
	"openai-organization":  {},
	"x-should-retry":       {},
}

var headerAllowPrefixes = []string{
	"x-ratelimit-",
	"anthropic-ratelimit-",
	"x-stainless-",
}

// Always redacted, regardless of casing.
var headerDenylist = map[string]struct{}{
	"authorization":                  {},
	"proxy-authorization":            {},
	"authentication":                 {},
	"cookie":                         {},
	"set-cookie":                     {},
	"x-api-key":                      {},
	"api-key":                        {},
	"x-goog-api-key":                 {},
	"x-goog-iam-authorization-token": {},
	"x-amz-security-token":           {},
	"x-auth-token":                   {},
	"x-access-token":                 {},
	"x-session-token":                {},
}

// Unknown custom headers: checked only after the allowlist misses.
var sensitiveNameFragments = []string{
	"key", "token", "secret", "auth", "credential",
	"password", "passwd", "signature", "session", "cookie",
}

// Query parameter names whose values must never be stored (Gemini clients
// pass the credential as ?key=...).
var sensitiveQueryFragments = []string{
	"key", "token", "secret", "auth", "credential",
	"password", "passwd", "signature", "session", "sig",
}

// Value schemes preserved in masked output so the shape stays diagnosable.
var maskedValueSchemes = []string{"Bearer", "Basic", "Digest", "Token"}

func headerNameSensitive(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := headerAllowlist[lower]; ok {
		return false
	}
	for _, prefix := range headerAllowPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	if _, ok := headerDenylist[lower]; ok {
		return true
	}
	for _, fragment := range sensitiveNameFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func maskHeaderValue(value string) string {
	if value == "" {
		return "<redacted:empty>"
	}
	for _, scheme := range maskedValueSchemes {
		rest, ok := strings.CutPrefix(value, scheme+" ")
		if ok {
			return fmt.Sprintf("%s <redacted:%d chars>", scheme, len(rest))
		}
	}
	return fmt.Sprintf("<redacted:%d chars>", len(value))
}

func queryNameSensitive(name string) bool {
	lower := strings.ToLower(name)
	for _, fragment := range sensitiveQueryFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

// redactURL strips userinfo and masks sensitive query parameter values while
// preserving parameter order as sent.
func redactURL(requestURL *url.URL) string {
	if requestURL == nil {
		return ""
	}
	cloned := *requestURL
	cloned.User = nil
	if cloned.RawQuery != "" {
		pairs := strings.Split(cloned.RawQuery, "&")
		for index, pair := range pairs {
			name, _, hasValue := strings.Cut(pair, "=")
			decoded, err := url.QueryUnescape(name)
			if err != nil {
				decoded = name
			}
			if hasValue && queryNameSensitive(decoded) {
				pairs[index] = name + "=<redacted>"
			}
		}
		cloned.RawQuery = strings.Join(pairs, "&")
	}
	rendered := cloned.String()
	if len(rendered) > maxCapturedURLLength {
		rendered = rendered[:maxCapturedURLLength] + "…"
	}
	return rendered
}

// redactHeaders converts one header map into ordered, redacted capture lines.
// Order within a name is preserved; names are emitted in the http.Header
// canonical form sorted by name for deterministic output.
func redactHeaders(headers http.Header) []contract.AuditHeader {
	if len(headers) == 0 {
		return []contract.AuditHeader{}
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	captured := make([]contract.AuditHeader, 0, len(headers))
	total := 0
	omitted := 0
	for _, name := range names {
		sensitive := headerNameSensitive(name)
		for _, value := range headers[name] {
			if total >= maxCapturedHeaders {
				omitted++
				continue
			}
			entry := contract.AuditHeader{Name: strings.ToLower(name)}
			if sensitive {
				entry.Value = maskHeaderValue(value)
				entry.Redacted = true
			} else {
				if len(value) > maxCapturedHeaderValue {
					value = value[:maxCapturedHeaderValue] + "…"
				}
				entry.Value = value
			}
			captured = append(captured, entry)
			total++
		}
	}
	if omitted > 0 {
		captured = append(captured, contract.AuditHeader{
			Name:  "…",
			Value: fmt.Sprintf("<%d more headers omitted>", omitted),
		})
	}
	return captured
}

// RedactRequestMeta snapshots the redacted HTTP envelope of an inbound
// request. It must be called before any privacy or routing rewrite so the
// capture reflects what the client actually sent.
func RedactRequestMeta(request *http.Request) contract.AuditHTTPMeta {
	meta := contract.AuditHTTPMeta{
		RequestHeaders:  []contract.AuditHeader{},
		ResponseHeaders: []contract.AuditHeader{},
	}
	if request == nil {
		return meta
	}
	meta.Method = request.Method
	meta.URL = redactURL(request.URL)
	meta.HTTPVersion = request.Proto
	meta.RequestHeaders = redactHeaders(request.Header)
	return meta
}

// RedactResponseHeaders converts the local response header map into redacted
// capture lines. Hop-by-hop headers are already stripped by the forwarder.
func RedactResponseHeaders(headers http.Header) []contract.AuditHeader {
	return redactHeaders(headers)
}

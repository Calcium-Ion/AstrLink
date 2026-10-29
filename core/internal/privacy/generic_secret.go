package privacy

import (
	"math"
	"regexp"
	"strings"
)

// The key-assignment and Bearer rules match whatever follows a key name, so
// they also see the code and documentation that only name a credential: an
// environment reference, a function call, a template, or a placeholder such as
// your_api_key. Like the generic rules of gitleaks and GitGuardian, they keep a
// value only when it looks generated: varied enough, with a digit, and free of
// placeholder words. The vendor-prefixed rules carry their own evidence and
// skip these checks.

// genericSecretMinEntropy is the Shannon entropy, in bits per character, below
// which a value repeats too much to be a generated credential. GitGuardian's
// generic detector uses the same floor.
const genericSecretMinEntropy = 3.0

// genericSecretStopwords mark a value as a placeholder or an example. A
// generated credential is random, so it does not spell any of them.
var genericSecretStopwords = []string{
	"example",
	"sample",
	"placeholder",
	"your",
	"changeme",
	"change_me",
	"change-me",
	"dummy",
	"fake",
	"test",
	"mock",
	"todo",
	"replace",
	"insert",
	"redacted",
	"xxxx",
}

// plausibleGenericSecret reports whether a value a generic rule matched looks
// like a credential rather than a reference to one or a stand-in for one.
func plausibleGenericSecret(value string) bool {
	if !strings.ContainsAny(value, "0123456789") {
		return false
	}
	if shannonEntropy(value) < genericSecretMinEntropy {
		return false
	}
	if identifierPath(value) {
		return false
	}
	lower := strings.ToLower(value)
	for _, word := range genericSecretStopwords {
		if strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

// genericSecretBoundary rejects a value that the text goes on to use as code
// or a template: a call such as config.get_token(), an index such as
// os.environ["TOKEN"], or a value that continues into ${...}, {{...}}, <...>,
// or %VAR%. The generic rules stop a value at the first character outside a
// credential's alphabet, so that character tells.
func genericSecretBoundary(text string, _, end int) bool {
	if end >= len(text) {
		return true
	}
	switch text[end] {
	case '(', '[', '{', '$', '<', '%':
		return false
	default:
		return true
	}
}

// identifierPath reports a dotted reference such as process.env.API_KEY,
// settings.SECRET_KEY, or this.props.authToken: a lowercase object or module
// name, then names written the way code writes them. A credential can be
// dotted too, as a JWT or a Google ya29. token is, but its random parts mix
// case and digits in no naming style, and a JWT starts with eyJ.
func identifierPath(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 || !lowerName.MatchString(parts[0]) {
		return false
	}
	for _, part := range parts[1:] {
		if !lowerName.MatchString(part) && !constantName.MatchString(part) &&
			!camelName.MatchString(part) && !pascalName.MatchString(part) {
			return false
		}
	}
	return true
}

var (
	lowerName    = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	constantName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	camelName    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+$`)
	pascalName   = regexp.MustCompile(`^(?:[A-Z][a-z0-9]+)+$`)
)

// shannonEntropy returns the entropy of value's bytes in bits per byte.
func shannonEntropy(value string) float64 {
	if value == "" {
		return 0
	}
	var counts [256]int
	for index := 0; index < len(value); index++ {
		counts[value[index]]++
	}
	length := float64(len(value))
	entropy := 0.0
	for _, count := range counts {
		if count == 0 {
			continue
		}
		share := float64(count) / length
		entropy -= share * math.Log2(share)
	}
	return entropy
}

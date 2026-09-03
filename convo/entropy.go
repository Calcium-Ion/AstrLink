package convo

import (
	"strings"
	"unicode/utf8"
)

// echoIDPrefixes are vendor prefixes that carry no entropy of their own. They
// are stripped before the remainder is judged. Longer prefixes come first so
// "srvtoolu_" is not mistaken for "toolu_".
var echoIDPrefixes = []string{
	"chatcmpl-tool-",
	"srvtoolu_",
	"mcptoolu_",
	"toolu_",
	"call_",
	"fc_",
	"msg_",
	"rs_",
	"ts_",
}

// HasCursorEntropy reports whether id looks like a randomly generated
// identifier rather than a sequence number. Some OpenAI-compatible servers
// emit tool call ids such as "call_0" or "call_1"; linking on those would merge
// unrelated conversations. The rule: after stripping a known vendor prefix the
// remainder must be at least 8 characters, not all digits, and contain at
// least 4 distinct characters. Whitespace and control characters disqualify
// the id outright.
func HasCursorEntropy(id string) bool {
	id = strings.TrimSpace(id)
	length := utf8.RuneCountInString(id)
	if length < 8 || length > MaxCursorRunes {
		return false
	}
	for _, r := range id {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	body := id
	for _, prefix := range echoIDPrefixes {
		if strings.HasPrefix(body, prefix) {
			body = body[len(prefix):]
			break
		}
	}
	if utf8.RuneCountInString(body) < 8 {
		return false
	}
	allDigits := true
	distinct := make(map[rune]struct{}, 16)
	for _, r := range body {
		if r < '0' || r > '9' {
			allDigits = false
		}
		distinct[r] = struct{}{}
	}
	if allDigits || len(distinct) < 4 {
		return false
	}
	return true
}

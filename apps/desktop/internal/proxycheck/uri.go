package proxycheck

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// The proxy settings Codex reads are parsed by the Rust http crate's URI
// parser, whose rules decide which values count: `127.0.0.1:7890` is a
// proxy, `127.0.0.1:7890/` and `http=h:80;https=s:443` are none. This is a
// port of the parts the matcher uses.

// uri is a parsed URI: scheme is "" when the text had none, authority ""
// when it had none.
type uri struct {
	scheme    string
	authority string
}

const uriChars = "!#$&'()*+,-./0123456789:;=?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]_abcdefghijklmnopqrstuvwxyz~"

func isURIChar(c byte) bool { return c != 0 && strings.IndexByte(uriChars, c) >= 0 }

func isSchemeChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("+-.~:", c) >= 0
}

const maxSchemeLen = 64

// parseURI parses text as http::Uri's FromStr does.
func parseURI(text string) (uri, bool) {
	switch {
	case text == "" || len(text) > 65534:
		return uri{}, false
	case text == "/" || text == "*":
		return uri{}, true
	case len(text) == 1:
		end, ok := authorityEnd(text)
		return uri{authority: text}, ok && end == len(text)
	case text[0] == '/':
		return uri{}, validPath(text)
	}
	scheme, rest, ok := parseScheme(text)
	if !ok {
		return uri{}, false
	}
	end, ok := authorityEnd(rest)
	if !ok {
		return uri{}, false
	}
	if scheme == "" {
		return uri{authority: rest}, end == len(rest)
	}
	if end == 0 {
		return uri{}, false
	}
	if path := rest[end:]; path != "" && !validPath(path) {
		return uri{}, false
	}
	return uri{scheme: scheme, authority: rest[:end]}, true
}

// parseScheme splits off a scheme and its `://`; scheme is "" for none.
func parseScheme(text string) (scheme, rest string, ok bool) {
	if len(text) >= 7 && strings.EqualFold(text[:7], "http://") {
		return "http", text[7:], true
	}
	if len(text) >= 8 && strings.EqualFold(text[:8], "https://") {
		return "https", text[8:], true
	}
	if len(text) > 3 {
		for i := 0; i < len(text); i++ {
			c := text[i]
			if !isSchemeChar(c) {
				break
			}
			if c != ':' {
				continue
			}
			if len(text) < i+3 || text[i+1:i+3] != "//" {
				break
			}
			if i > maxSchemeLen {
				return "", "", false
			}
			return text[:i], text[i+3:], true
		}
	}
	return "", text, true
}

// authorityEnd returns where the authority at the start of text ends.
func authorityEnd(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	colons, at, end := 0, len(text), len(text)
	open, closed, percent := false, false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '/' || c == '?' || c == '#' {
			end = i
			break
		}
		switch {
		case !isURIChar(c):
			if c != '%' {
				return 0, false
			}
			percent = true
		case c == ':':
			if colons >= 8 {
				return 0, false
			}
			colons++
		case c == '[':
			if percent || open {
				return 0, false
			}
			open = true
		case c == ']':
			if !open || closed {
				return 0, false
			}
			closed = true
			colons, percent = 0, false
		case c == '@':
			at = i
			colons, percent = 0, false
		}
	}
	if open != closed || colons > 1 || end > 0 && at == end-1 || percent {
		return 0, false
	}
	return end, true
}

// validPath reports whether text is a path and query http::Uri takes.
func validPath(text string) bool {
	if text == "*" {
		return true
	}
	if text == "" || !strings.ContainsRune("/?#", rune(text[0])) {
		return false
	}
	i, query := 0, false
	for ; i < len(text); i++ {
		c := text[i]
		if c == '?' {
			query = true
			i++
			break
		}
		if c == '#' {
			return utf8.ValidString(text[:i])
		}
		if !(c == 0x21 || c >= 0x24 && c <= 0x3B || c == 0x3D || c >= 0x40 && c <= 0x5F ||
			c >= 0x61 && c <= 0x7A || c == 0x7C || c == 0x7E || c >= 0x80 || c == '"' || c == '{' || c == '}') {
			return false
		}
	}
	if query {
		for ; i < len(text); i++ {
			c := text[i]
			if c == '#' {
				return utf8.ValidString(text[:i])
			}
			if !(c == 0x21 || c >= 0x24 && c <= 0x3B || c == 0x3D || c >= 0x3F && c <= 0x7E || c >= 0x80) {
				return false
			}
		}
	}
	return utf8.ValidString(text)
}

// host is an authority's host, IPv6 brackets kept.
func host(authority string) string {
	hostPort := authority[strings.LastIndexByte(authority, '@')+1:]
	if strings.HasPrefix(hostPort, "[") {
		return hostPort[:strings.IndexByte(hostPort, ']')+1]
	}
	host, _, _ := strings.Cut(hostPort, ":")
	return host
}

// port is an authority's port, as Rust's u16 parser reads what follows
// its last colon.
func port(authority string) (uint16, bool) {
	i := strings.LastIndexByte(authority, ':')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(authority[i+1:], "+"), 10, 16)
	if err != nil || strings.HasPrefix(authority[i+1:], "+-") {
		return 0, false
	}
	return uint16(n), true
}

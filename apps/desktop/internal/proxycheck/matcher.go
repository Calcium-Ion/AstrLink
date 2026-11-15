package proxycheck

import (
	"encoding/base64"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"
)

// The matcher is hyper-util's (client::proxy::matcher), which reqwest and
// Codex read the system proxy through: the environment first, then the
// system's settings for what it leaves unset. On Windows a bypass entry
// such as `127.*` is a domain name to it, so `127.0.0.1` can end up inside
// the proxy; Go's own proxy rules differ, and are not used here.

// intercept is where a matched request goes.
type intercept struct {
	scheme string
	// authority is the proxy's host and port, credentials removed.
	authority string
	// user and password are the credentials, decoded; hasUser is set when
	// there are any.
	user, password string
	hasUser        bool
	hasPassword    bool
}

// basicAuth is the Proxy-Authorization an HTTP proxy is sent.
func (i *intercept) basicAuth() string {
	if !i.hasUser || i.scheme != "http" && i.scheme != "https" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(i.user+":"+i.password))
}

// proxyURL is the proxy as Go's transport takes it.
func (i *intercept) proxyURL() *url.URL {
	u := &url.URL{Scheme: i.scheme, Host: i.authority}
	if i.hasUser {
		if i.hasPassword {
			u.User = url.UserPassword(i.user, i.password)
		} else {
			u.User = url.User(i.user)
		}
	}
	return u
}

// proxyAuthority is the proxy's host and port only: proxy credentials
// never leave this package.
func (i *intercept) proxyAuthority() string {
	h := host(i.authority)
	if p, ok := port(i.authority); ok {
		return h + ":" + itoa(int(p))
	}
	return h
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

type matcher struct {
	http, https *intercept
	no          noProxy
}

// settings are the raw values a matcher is built from.
type settings struct {
	cgi                  bool
	all, http, https, no string
}

// envSettings reads the environment as curl does.
func envSettings() settings {
	_, cgi := os.LookupEnv("REQUEST_METHOD")
	return settings{
		cgi:   cgi,
		all:   firstEnv("ALL_PROXY", "all_proxy"),
		http:  firstEnv("HTTP_PROXY", "http_proxy"),
		https: firstEnv("HTTPS_PROXY", "https_proxy"),
		no:    firstEnv("NO_PROXY", "no_proxy"),
	}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok && utf8.ValidString(value) {
			return value
		}
	}
	return ""
}

// systemMatcher reads the environment, then the system's settings for
// what it leaves unset.
func systemMatcher() *matcher {
	s := envSettings()
	withSystem(&s)
	return s.build()
}

func (s settings) build() *matcher {
	if s.cgi {
		return &matcher{}
	}
	all := parseProxy(s.all)
	m := &matcher{http: parseProxy(s.http), https: parseProxy(s.https), no: parseNoProxy(s.no)}
	if m.http == nil {
		m.http = all
	}
	if m.https == nil {
		m.https = all
	}
	return m
}

// parseProxy reads a proxy setting; nil when it names no usable proxy.
func parseProxy(value string) *intercept {
	u, ok := parseURI(value)
	if !ok || u.authority == "" {
		return nil
	}
	out := &intercept{scheme: "http"}
	switch u.scheme {
	case "", "http":
	case "https":
		out.scheme = "https"
	case "socks4", "socks4a", "socks5", "socks5h":
		out.scheme = u.scheme
	default:
		return nil
	}
	out.authority = u.authority
	if userinfo, hostPort, ok := strings.Cut(u.authority, "@"); ok {
		user, password, hasPassword := strings.Cut(userinfo, ":")
		out.user, out.password = percentDecode(user), percentDecode(password)
		out.hasUser, out.hasPassword = true, hasPassword
		end, ok := authorityEnd(hostPort)
		if !ok || end != len(hostPort) {
			return nil
		}
		out.authority = hostPort
	}
	return out
}

// percentDecode decodes as percent_encoding does: a `%` not followed by
// two hex digits stays, and invalid UTF-8 becomes U+FFFD.
func percentDecode(text string) string {
	var out []byte
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) && isHex(text[i+1]) && isHex(text[i+2]) {
			out = append(out, unhex(text[i+1])<<4|unhex(text[i+2]))
			i += 2
			continue
		}
		out = append(out, text[i])
	}
	return strings.ToValidUTF8(string(out), "�")
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// intercepts returns where a request to dst goes, nil for direct.
func (m *matcher) intercepts(dst string) *intercept {
	u, ok := parseURI(dst)
	if !ok || u.authority == "" || m.no.contains(host(u.authority)) {
		return nil
	}
	switch u.scheme {
	case "http":
		return m.http
	case "https":
		return m.https
	}
	return nil
}

// noProxy is a NO_PROXY list, as curl reads one.
type noProxy struct {
	ips     []netip.Prefix
	domains []string
}

func parseNoProxy(list string) noProxy {
	var no noProxy
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if prefix, ok := parseNet(part); ok {
			no.ips = append(no.ips, prefix)
		} else if addr, ok := parseIP(part); ok {
			no.ips = append(no.ips, netip.PrefixFrom(addr, addr.BitLen()))
		} else if part != "" {
			no.domains = append(no.domains, part)
		}
	}
	return no
}

// parseIP parses an address as Rust's IpAddr does: no zone.
func parseIP(text string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(text)
	return addr, err == nil && addr.Zone() == ""
}

// parseNet parses `address/length` as the ipnet crate does.
func parseNet(text string) (netip.Prefix, bool) {
	address, length, ok := strings.Cut(text, "/")
	addr, isIP := parseIP(address)
	if !ok || !isIP || length == "" || len(length) > 3 {
		return netip.Prefix{}, false
	}
	bits := 0
	for _, c := range length {
		if c < '0' || c > '9' {
			return netip.Prefix{}, false
		}
		bits = bits*10 + int(c-'0')
	}
	if bits > addr.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, bits), true
}

func (n noProxy) contains(host string) bool {
	if strings.HasPrefix(host, "[") {
		host = strings.Trim(host, "[]")
	}
	if addr, ok := parseIP(host); ok {
		for _, prefix := range n.ips {
			if prefix.Masked().Contains(addr) {
				return true
			}
		}
		return false
	}
	for _, d := range n.domains {
		switch {
		case strings.EqualFold(d, host) || strings.HasPrefix(d, ".") && strings.EqualFold(d[1:], host):
			return true
		case len(host) >= len(d) && strings.EqualFold(host[len(host)-len(d):], d):
			if strings.HasPrefix(d, ".") || len(host) > len(d) && host[len(host)-len(d)-1] == '.' {
				return true
			}
		case d == "*":
			return true
		}
	}
	return false
}

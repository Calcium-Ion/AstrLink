package clientupdates

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// macEnvironment turns `scutil --proxy` output into the variables
// command-line installers read, as Core applies the macOS system proxy.
// Unlike native HTTP clients, curl and npm need them spelled out.
func macEnvironment(raw string) ([]envVar, *clientError) {
	values := map[string]string{}
	bypass := []string{"localhost", "127.0.0.1", "::1"}
	depth := 0
	exceptions := false
	for _, line := range lines(raw) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasSuffix(line, "{"):
			if depth == 1 {
				exceptions = strings.HasPrefix(line, "ExceptionsList :")
			}
			depth++
		case line == "}":
			depth--
			if depth < 2 {
				exceptions = false
			}
		default:
			key, value, ok := strings.Cut(line, " : ")
			if !ok {
				continue
			}
			if depth == 1 {
				values[key] = value
			} else if depth == 2 && exceptions {
				bypass = append(bypass, strings.TrimPrefix(value, "*"))
			}
		}
	}
	if depth != 0 || !strings.HasPrefix(strings.TrimLeftFunc(raw, unicode.IsSpace), "<dictionary> {") {
		return nil, newError("proxy", "Invalid macOS proxy response")
	}
	if values["ProxyAutoConfigEnable"] == "1" || values["ProxyAutoDiscoveryEnable"] == "1" {
		return nil, newError("proxy", "PAC/WPAD is not supported by command-line updates. Configure a manual system HTTP/HTTPS/SOCKS proxy.")
	}
	address := func(protocol, scheme string) (string, *clientError) {
		if values[protocol+"Enable"] != "1" {
			return "", nil
		}
		host := values[protocol+"Proxy"]
		port, err := strconv.ParseUint(values[protocol+"Port"], 10, 16)
		if err != nil || port == 0 {
			return "", newError("proxy", "Invalid proxy port")
		}
		if host == "" || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("/@?#", r) }) {
			return "", newError("proxy", "Invalid proxy host")
		}
		host = strings.Trim(host, "[]")
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		address := scheme + "://" + host + ":" + strconv.FormatUint(port, 10)
		if parsed, err := url.Parse(address); err != nil || parsed.Hostname() == "" {
			return "", newError("proxy", "Invalid proxy host")
		}
		return address, nil
	}
	socks, failure := address("SOCKS", "socks5h")
	if failure != nil {
		return nil, failure
	}
	http, failure := address("HTTP", "http")
	if failure != nil {
		return nil, failure
	}
	https, failure := address("HTTPS", "http")
	if failure != nil {
		return nil, failure
	}
	if http == "" {
		http = socks
	}
	if https == "" {
		https = socks
	}
	noProxy := strings.Join(bypass, ",")
	return []envVar{
		{"HTTP_PROXY", http},
		{"http_proxy", http},
		{"HTTPS_PROXY", https},
		{"https_proxy", https},
		{"ALL_PROXY", socks},
		{"all_proxy", socks},
		{"NO_PROXY", noProxy},
		{"no_proxy", noProxy},
		{"npm_config_proxy", http},
		{"npm_config_https_proxy", https},
		{"npm_config_noproxy", noProxy},
	}, nil
}

// lines splits text as Rust's str::lines does: at \n, dropping a \r before
// it and an empty last line.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, part := range parts {
		parts[i] = strings.TrimSuffix(part, "\r")
	}
	return parts
}

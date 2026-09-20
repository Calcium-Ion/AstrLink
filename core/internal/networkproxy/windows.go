package networkproxy

import (
	"fmt"
	"net/url"
	"strings"
)

func parseWindowsSettings(enabled bool, server, bypass string) (settings, error) {
	config := settings{}
	var exceptions []string
	for _, host := range strings.Split(bypass, ";") {
		host = strings.TrimSpace(host)
		if host == "<local>" {
			config.excludeSimple = true
		} else if host != "" {
			exceptions = append(exceptions, host)
		}
	}
	config.bypass = strings.Join(exceptions, ",")
	if !enabled {
		return config, nil
	}
	if strings.TrimSpace(server) == "" {
		return settings{}, fmt.Errorf("enabled system proxy has no server")
	}
	for _, entry := range strings.Split(server, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		protocol, address, specific := strings.Cut(entry, "=")
		if !specific {
			address, protocol = protocol, ""
		}
		scheme := "http"
		if protocol == "socks" {
			scheme = "socks5"
		}
		if !strings.Contains(address, "://") {
			address = scheme + "://" + address
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return settings{}, fmt.Errorf("invalid system proxy server")
		}
		port := parsed.Port()
		if port == "" {
			switch parsed.Scheme {
			case "http":
				port = "80"
			case "https":
				port = "443"
			case "socks5":
				port = "1080"
			}
		}
		address, err = proxyAddress(parsed.Scheme, parsed.Hostname(), port)
		if err != nil {
			return settings{}, err
		}
		switch protocol {
		case "":
			config.http, config.https = address, address
		case "http":
			config.http = address
		case "https":
			config.https = address
		case "socks":
			config.socks = address
		}
	}
	return config, nil
}

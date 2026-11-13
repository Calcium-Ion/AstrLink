package proxycheck

import "strings"

// windowsSettings applies Internet Settings' ProxyServer and ProxyOverride
// to what the environment leaves unset.
func windowsSettings(s *settings, server string, hasServer bool, override string, hasOverride bool) {
	if hasServer {
		if s.http == "" {
			s.http = server
		}
		if s.https == "" {
			s.https = server
		}
	}
	if s.no == "" && hasOverride {
		parts := strings.Split(override, ";")
		for i, part := range parts {
			parts[i] = strings.TrimSpace(part)
		}
		s.no = strings.ReplaceAll(strings.Join(parts, ","), "*.", "")
	}
}

// macSetting is one protocol's proxy from the system's dynamic store:
// `host:port` when it is enabled, "" for none.
func macSetting(enabled int32, hasEnabled bool, host string, hasHost bool, port int32, hasPort bool) string {
	if !hasEnabled || enabled != 1 || !hasHost {
		return ""
	}
	if hasPort {
		return host + ":" + itoa32(port)
	}
	return host
}

func itoa32(n int32) string {
	if n < 0 {
		return "-" + itoa(-int(n))
	}
	return itoa(int(n))
}

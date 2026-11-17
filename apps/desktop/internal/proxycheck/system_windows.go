package proxycheck

import "golang.org/x/sys/windows/registry"

// withSystem fills what the environment leaves unset from the user's
// Internet Settings. ProxyOverride's `*.` prefixes are dropped and the rest
// kept as domain names, as hyper-util does: `127.*` never matches an
// address.
func withSystem(s *settings) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	if enabled, _, err := key.GetIntegerValue("ProxyEnable"); err != nil || uint32(enabled) == 0 {
		return
	}
	server, _, serverErr := key.GetStringValue("ProxyServer")
	override, _, overrideErr := key.GetStringValue("ProxyOverride")
	windowsSettings(s, server, serverErr == nil, override, overrideErr == nil)
}

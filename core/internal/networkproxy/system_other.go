//go:build !darwin && !windows

package networkproxy

// Linux has no universal desktop proxy store. Use the standard process proxy
// environment, including ALL_PROXY for SOCKS. This also supports headless use.
func systemSettings() (settings, error) {
	return environmentSettings(), nil
}

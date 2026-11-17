package agents

import "golang.org/x/sys/windows/registry"

// opensLinks reports whether an app is registered for `scheme:` links.
func opensLinks(scheme string) bool {
	key, err := registry.OpenKey(registry.CLASSES_ROOT, scheme+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	command, _, err := key.GetStringValue("")
	return err == nil && command != ""
}

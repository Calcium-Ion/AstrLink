package clientupdates

import "golang.org/x/sys/windows/registry"

// savedPath is the PATH a new terminal gets: the saved system Path, then
// the user's. Without the system part there is no usable PATH, so none is
// returned.
func savedPath() (string, bool) {
	path, ok := registryPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)
	if !ok {
		return "", false
	}
	if user, ok := registryPath(registry.CURRENT_USER, "Environment"); ok {
		path += ";" + user
	}
	return path, true
}

// registryPath is a key's saved Path value, its %NAME% references expanded
// as RegGetValueW expands a REG_EXPAND_SZ value.
func registryPath(root registry.Key, name string) (string, bool) {
	key, err := registry.OpenKey(root, name, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	value, kind, err := key.GetStringValue("Path")
	if err != nil {
		return "", false
	}
	if kind == registry.EXPAND_SZ {
		if value, err = registry.ExpandString(value); err != nil {
			return "", false
		}
	}
	return value, true
}

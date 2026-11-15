package ui

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// systemRunEntries are the user's Run entries in the registry.
type systemRunEntries struct{}

func (systemRunEntries) Value(name string) (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, legacyRunKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	return value, err == nil, err
}

// Delete removes the Run value and the state Task Manager keeps of it.
func (systemRunEntries) Delete(name string) error {
	for _, path := range []string{legacyRunKey, legacyApprovedKey} {
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = key.DeleteValue(name)
		key.Close()
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return nil
}

func systemLegacyRunEntries() legacyRunEntries { return systemRunEntries{} }

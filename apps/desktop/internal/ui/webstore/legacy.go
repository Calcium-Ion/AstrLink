// Package webstore reads the localStorage the Tauri shell's webview kept
// for the desktop pages. The pages now load from another origin (and on
// Windows from another WebView2 profile), so they start with an empty
// localStorage; the desktop hands them the old items once.
package webstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Prefix namespaces the desktop pages' items. The web console's items
// ("astrlink:") are not the desktop's.
const Prefix = "astrlink."

const (
	// maxValue and maxTotal bound what is handed to the page, in bytes
	// of UTF-8: localStorage holds about 5 MB, the pages' items far less.
	maxValue = 512 << 10
	maxTotal = 2 << 20
)

// source is one store the Tauri shell may have written.
type source struct {
	path string
	read func() (map[string]string, error)
}

// Legacy returns the desktop pages' items the Tauri shell stored for the
// app named identifier, under home with the environment getenv, as the
// webview of goos laid them out. Stores that are missing are skipped; a
// store that cannot be read is reported in errs and skipped.
func Legacy(goos, home string, getenv func(string) string, identifier string) (items map[string]string, errs []error) {
	items = map[string]string{}
	for _, s := range sources(goos, home, getenv, identifier) {
		found, err := s.read()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Later sources are newer layouts: they win.
		for k, v := range found {
			items[k] = v
		}
	}
	return Select(items), errs
}

// Select keeps the desktop pages' items, within the size bounds, the
// smaller ones first when they do not all fit.
func Select(items map[string]string) map[string]string {
	keys := make([]string, 0, len(items))
	for k, v := range items {
		if strings.HasPrefix(k, Prefix) && len(k)+len(v) <= maxValue {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := len(keys[i])+len(items[keys[i]]), len(keys[j])+len(items[keys[j]])
		if a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	selected := map[string]string{}
	total := 0
	for _, k := range keys {
		size := len(k) + len(items[k])
		if total+size > maxTotal {
			break
		}
		total += size
		selected[k] = items[k]
	}
	return selected
}

func sources(goos, home string, getenv func(string) string, identifier string) []source {
	switch goos {
	case "windows":
		base := getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		dir := filepath.Join(base, identifier, "EBWebView", "Default", "Local Storage", "leveldb")
		var list []source
		// Tauri serves its pages at http://tauri.localhost, or https:// with
		// useHttpsScheme.
		for _, origin := range []string{"http://tauri.localhost", "https://tauri.localhost"} {
			list = append(list, source{dir, func() (map[string]string, error) {
				if _, err := os.Stat(dir); err != nil {
					return nil, err
				}
				return ReadChromiumStorage(dir, origin)
			}})
		}
		return list
	case "darwin":
		// WKWebView's default data store, which wry used.
		base := filepath.Join(home, "Library", "WebKit", identifier, "WebsiteData")
		return webKitSources(filepath.Join(base, "LocalStorage"), filepath.Join(base, "Default"))
	default:
		// The WebContext wry made on the app's local data directory.
		data := getenv("XDG_DATA_HOME")
		if data == "" || !filepath.IsAbs(data) {
			data = filepath.Join(home, ".local", "share")
		}
		base := filepath.Join(data, identifier)
		return webKitSources(filepath.Join(base, "localstorage"), filepath.Join(base, "storage"))
	}
}

// webKitSources are WebKit's two layouts: one database per origin named
// after it in legacy, and, from WebKit 2.40 and Safari 17, a directory per
// origin under general, named by a salted hash, which an "origin" file
// beside its LocalStorage directory names.
func webKitSources(legacy, general string) []source {
	file := filepath.Join(legacy, "tauri_localhost_0.localstorage")
	list := []source{{file, func() (map[string]string, error) { return ReadWebKitStorage(file) }}}
	matches, _ := filepath.Glob(filepath.Join(general, "*", "*", "origin"))
	sort.Strings(matches)
	for _, origin := range matches {
		data, err := os.ReadFile(origin)
		if err != nil || !isTauriOrigin(data) {
			continue
		}
		db := filepath.Join(filepath.Dir(origin), "LocalStorage", "localstorage.sqlite3")
		list = append(list, source{db, func() (map[string]string, error) { return ReadWebKitStorage(db) }})
	}
	return list
}

// isTauriOrigin reports whether a WebKit origin file, the serialized top
// and frame origins, names tauri://localhost for both.
func isTauriOrigin(data []byte) bool {
	return bytes.Count(data, []byte("tauri")) >= 2 && bytes.Count(data, []byte("localhost")) >= 2
}

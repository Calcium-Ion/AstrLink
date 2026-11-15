package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/desktop/src/i18n/locales"
)

// Locale is a language the interface is written in.
type Locale string

const (
	LocaleEn   Locale = "en"
	LocaleZhCN Locale = "zh-CN"
	LocaleJa   Locale = "ja"
)

func (l *Locale) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(l), "locale", LocaleEn, LocaleZhCN, LocaleJa)
}

// SystemLocale is the operating system's most preferred supported language,
// used until the operator picks one in settings.
func SystemLocale() Locale { return LocaleFromPreferred(systemLanguages()) }

// LocaleFromPreferred walks BCP 47 (or POSIX) tags in preference order. Any
// Chinese variant maps to the simplified Chinese catalog, Japanese maps to
// Japanese, and unsupported languages are skipped.
func LocaleFromPreferred(tags []string) Locale {
	for _, tag := range tags {
		language, _, _ := strings.Cut(strings.NewReplacer("_", "-", ".", "-").Replace(tag), "-")
		switch strings.ToLower(language) {
		case "zh":
			return LocaleZhCN
		case "ja":
			return LocaleJa
		case "en":
			return LocaleEn
		}
	}
	return LocaleEn
}

// systemLanguages are the user's languages from the POSIX environment; the
// app layer passes the system's own list on macOS and Windows.
var systemLanguages = func() []string {
	var tags []string
	for _, name := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		for _, tag := range strings.Split(os.Getenv(name), ":") {
			if tag != "" && tag != "C" && tag != "POSIX" {
				tags = append(tags, tag)
			}
		}
	}
	return tags
}

// SetSystemLanguages replaces where SystemLocale reads the user's
// languages, as the app's PreferredLanguages once it runs.
func SetSystemLanguages(fn func() []string) { systemLanguages = fn }

var catalogs sync.Map // Locale → map[string]any

func catalog(locale Locale) map[string]any {
	if c, ok := catalogs.Load(locale); ok {
		return c.(map[string]any)
	}
	file := map[Locale]string{LocaleEn: "en.json", LocaleZhCN: "zh-CN.json", LocaleJa: "ja.json"}[locale]
	data, err := locales.FS.ReadFile(filepath.ToSlash(file))
	if err != nil {
		panic("prefs: missing catalog " + file)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		panic("prefs: invalid catalog " + file + ": " + err.Error())
	}
	c, _ := catalogs.LoadOrStore(locale, root)
	return c.(map[string]any)
}

// T returns the message key in locale, English when the catalog lacks it
// and the key itself when English does too, with {{name}} replaced by vars
// given as name, value pairs.
func T(locale Locale, key string, vars ...string) string {
	if template, ok := lookup(catalog(locale), key); ok {
		return interpolate(template, vars)
	}
	if locale != LocaleEn {
		if template, ok := lookup(catalog(LocaleEn), key); ok {
			return interpolate(template, vars)
		}
	}
	return key
}

// HasKey reports whether the catalog of locale has key.
func HasKey(locale Locale, key string) bool {
	_, ok := lookup(catalog(locale), key)
	return ok
}

func lookup(root map[string]any, key string) (string, bool) {
	var node any = root
	for _, part := range strings.Split(key, ".") {
		object, ok := node.(map[string]any)
		if !ok {
			return "", false
		}
		if node, ok = object[part]; !ok {
			return "", false
		}
	}
	s, ok := node.(string)
	return s, ok
}

func interpolate(template string, vars []string) string {
	for i := 0; i+1 < len(vars); i += 2 {
		template = strings.ReplaceAll(template, "{{"+vars[i]+"}}", vars[i+1])
	}
	return template
}

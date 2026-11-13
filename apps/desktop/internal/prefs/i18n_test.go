package prefs

import (
	"strings"
	"testing"
)

func TestHostKeysExistInEveryCatalog(t *testing.T) {
	for locale, want := range map[Locale]string{
		LocaleZhCN: "网关尚未就绪。",
		LocaleJa:   "ゲートウェイはまだ準備できていません。",
		LocaleEn:   "The gateway is not ready yet.",
	} {
		if got := T(locale, "host.sidecar.notReady"); got != want {
			t.Errorf("%s: %q", locale, got)
		}
	}
	for locale, want := range map[Locale]string{
		LocaleEn:   "Gateway running · 127.0.0.1:8317",
		LocaleZhCN: "网关运行中 · 127.0.0.1:8317",
		LocaleJa:   "ゲートウェイ実行中 · 127.0.0.1:8317",
	} {
		if got := T(locale, "host.tray.status.ready", "address", "127.0.0.1:8317"); got != want {
			t.Errorf("%s: %q", locale, got)
		}
	}
	// Every key the tray icon renders must exist in every catalog; a missing
	// one would surface as a raw key in the tooltip.
	for _, key := range []string{
		"host.tray.show", "host.tray.settings", "host.tray.quit", "host.tray.core.start",
		"host.tray.core.stop", "host.tray.core.restart", "host.tray.copied", "host.tray.tooltip",
		"host.tray.status.ready", "host.tray.status.fallback", "host.tray.status.stopped",
		"host.tray.status.starting", "host.tray.status.stopping", "host.tray.status.failed",
		"host.tray.status.observed", "host.tray.status.observedRaw", "host.tray.status.rawAccessPending",
		"host.tray.status.rawPasswordRequired", "host.tray.status.rawKeyReplaced", "host.tray.menubar.alert",
		"host.tray.hiddenMenuBarTitle", "host.tray.hiddenMenuBarBody", "host.tray.hiddenTitle",
		"host.tray.hiddenBody", "host.preferences.trayPagesDuplicate",
	} {
		for _, locale := range []Locale{LocaleEn, LocaleJa, LocaleZhCN} {
			if !HasKey(locale, key) {
				t.Errorf("%s missing for %s", key, locale)
			}
		}
	}
}

func TestSystemLocaleFollowsFirstSupportedPreference(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want Locale
	}{
		{[]string{"zh-Hans-CN", "en-US"}, LocaleZhCN},
		{[]string{"zh_CN.UTF-8"}, LocaleZhCN},
		{[]string{"zh-Hant-TW"}, LocaleZhCN},
		{[]string{"en-GB", "zh-CN"}, LocaleEn},
		{[]string{"ja-JP", "zh-Hans", "en"}, LocaleJa},
		{[]string{"ja_JP.UTF-8"}, LocaleJa},
		{[]string{"fr-FR", "C"}, LocaleEn},
		{nil, LocaleEn},
	} {
		if got := LocaleFromPreferred(c.tags); got != c.want {
			t.Errorf("LocaleFromPreferred(%q) = %s", c.tags, got)
		}
	}
}

func TestInterpolatesMustacheVars(t *testing.T) {
	message := T(LocaleEn, "host.sidecar.portBusy", "port", "8317", "error", "in use")
	if !strings.Contains(message, "8317") || !strings.Contains(message, "in use") {
		t.Fatalf("message %q", message)
	}
	if got := T(LocaleJa, "host.no.such.key"); got != "host.no.such.key" {
		t.Fatalf("missing key %q", got)
	}
}

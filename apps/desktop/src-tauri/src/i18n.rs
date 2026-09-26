use std::sync::OnceLock;

use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
pub enum Locale {
    #[default]
    #[serde(rename = "en")]
    En,
    #[serde(rename = "zh-CN")]
    ZhCN,
}

impl Locale {
    /// The operating system's most preferred supported language, used until the
    /// operator picks one in settings.
    pub fn system() -> Self {
        Self::from_preferred(sys_locale::get_locales())
    }

    /// Walks BCP 47 (or POSIX) tags in preference order; any Chinese variant
    /// maps to the only Chinese catalog, and unsupported languages are skipped.
    fn from_preferred(tags: impl IntoIterator<Item = String>) -> Self {
        for tag in tags {
            let language = tag.split(['-', '_', '.']).next().unwrap_or_default();
            if language.eq_ignore_ascii_case("zh") {
                return Self::ZhCN;
            }
            if language.eq_ignore_ascii_case("en") {
                return Self::En;
            }
        }
        Self::En
    }
}

fn catalog(locale: Locale) -> &'static Value {
    static EN: OnceLock<Value> = OnceLock::new();
    static ZH: OnceLock<Value> = OnceLock::new();
    match locale {
        Locale::En => EN.get_or_init(|| {
            serde_json::from_str(include_str!("../../src/i18n/locales/en.json"))
                .expect("en.json is valid")
        }),
        Locale::ZhCN => ZH.get_or_init(|| {
            serde_json::from_str(include_str!("../../src/i18n/locales/zh-CN.json"))
                .expect("zh-CN.json is valid")
        }),
    }
}

pub fn t(locale: Locale, key: &str, vars: &[(&str, &str)]) -> String {
    if let Some(template) = lookup(catalog(locale), key) {
        return interpolate(template, vars);
    }
    if locale != Locale::En {
        if let Some(template) = lookup(catalog(Locale::En), key) {
            return interpolate(template, vars);
        }
    }
    key.to_string()
}

fn lookup<'a>(root: &'a Value, key: &str) -> Option<&'a str> {
    let mut node = root;
    for part in key.split('.') {
        node = node.get(part)?;
    }
    node.as_str()
}

fn interpolate(template: &str, vars: &[(&str, &str)]) -> String {
    let mut out = template.to_string();
    for (name, value) in vars {
        out = out.replace(&format!("{{{{{name}}}}}"), value);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn host_keys_exist_in_both_catalogs() {
        assert_eq!(
            t(Locale::ZhCN, "host.sidecar.notReady", &[]),
            "网关尚未就绪。"
        );
        assert_eq!(
            t(Locale::En, "host.sidecar.notReady", &[]),
            "The gateway is not ready yet."
        );
        assert_eq!(
            t(
                Locale::En,
                "host.tray.status.ready",
                &[("address", "127.0.0.1:8317")]
            ),
            "Gateway running · 127.0.0.1:8317"
        );
        assert_eq!(
            t(
                Locale::ZhCN,
                "host.tray.status.ready",
                &[("address", "127.0.0.1:8317")]
            ),
            "网关运行中 · 127.0.0.1:8317"
        );
        // Every key the tray icon renders must exist in both catalogs; a
        // missing one would surface as a raw key in the tooltip.
        for key in [
            "host.tray.show",
            "host.tray.settings",
            "host.tray.quit",
            "host.tray.core.start",
            "host.tray.core.stop",
            "host.tray.core.restart",
            "host.tray.copied",
            "host.tray.tooltip",
            "host.tray.status.ready",
            "host.tray.status.fallback",
            "host.tray.status.stopped",
            "host.tray.status.starting",
            "host.tray.status.stopping",
            "host.tray.status.failed",
            "host.tray.status.observed",
            "host.tray.menubar.alert",
            "host.tray.hiddenMenuBarTitle",
            "host.tray.hiddenMenuBarBody",
            "host.tray.hiddenTitle",
            "host.tray.hiddenBody",
            "host.preferences.trayPagesDuplicate",
        ] {
            for locale in [Locale::En, Locale::ZhCN] {
                assert!(
                    lookup(catalog(locale), key).is_some(),
                    "{key} missing for {locale:?}"
                );
            }
        }
    }

    #[test]
    fn system_locale_follows_first_supported_preference() {
        let pick = |tags: &[&str]| Locale::from_preferred(tags.iter().map(|tag| tag.to_string()));
        assert_eq!(pick(&["zh-Hans-CN", "en-US"]), Locale::ZhCN);
        assert_eq!(pick(&["zh_CN.UTF-8"]), Locale::ZhCN);
        assert_eq!(pick(&["zh-Hant-TW"]), Locale::ZhCN);
        assert_eq!(pick(&["en-GB", "zh-CN"]), Locale::En);
        assert_eq!(pick(&["ja-JP", "zh-Hans", "en"]), Locale::ZhCN);
        assert_eq!(pick(&["fr-FR", "C"]), Locale::En);
        assert_eq!(pick(&[]), Locale::En);
    }

    #[test]
    fn interpolates_mustache_vars() {
        let message = t(
            Locale::En,
            "host.sidecar.portBusy",
            &[("port", "8317"), ("error", "in use")],
        );
        assert!(message.contains("8317"));
        assert!(message.contains("in use"));
    }
}

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
        assert_eq!(t(Locale::En, "host.tray.show", &[]), "Show AstrLink");
        assert_eq!(t(Locale::ZhCN, "host.tray.show", &[]), "显示 AstrLink");
        assert_eq!(t(Locale::En, "host.tray.quit", &[]), "Quit");
        assert_eq!(t(Locale::ZhCN, "host.tray.quit", &[]), "退出");
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

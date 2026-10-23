//! `make dev` runs an isolated AstrLink beside the one started with
//! `make start` or installed. Its identifier, set by `tauri.dev.conf.json`,
//! gives it separate data and config directories and its own single-instance
//! lock. This module picks the rest: the default inference port, the link
//! scheme and the name of its login item. Client configs, agent tools and
//! the agent locator stay in the user's home, shared with the other
//! AstrLink.

use std::{
    path::{Path, PathBuf},
    sync::atomic::{AtomicBool, Ordering},
};

pub const IDENTIFIER: &str = "com.astrlink.desktop";
pub const ISOLATED_IDENTIFIER: &str = "com.astrlink.desktop.isolated";
pub const ISOLATED_NAME: &str = "AstrLink Dev";
pub const ISOLATED_LINK_SCHEME: &str = "astrlink-isolated";
pub const LINK_SCHEME: &str = "astrlink";
const ISOLATED_INFERENCE_PORT: u16 = 18318;

static ISOLATED: AtomicBool = AtomicBool::new(false);

/// Applies the identity the build was configured with. Plugins read the link
/// schemes and the product name, which names the login item, when the app is
/// built.
pub fn configure<R: tauri::Runtime>(mut context: tauri::Context<R>) -> tauri::Context<R> {
    if context.config().identifier == ISOLATED_IDENTIFIER {
        ISOLATED.store(true, Ordering::Relaxed);
        isolate(context.config_mut());
        context.package_info_mut().name = ISOLATED_NAME.to_string();
    }
    context
}

/// Sets the isolated product name, main window title and link scheme.
fn isolate(config: &mut tauri::Config) {
    config.product_name = Some(ISOLATED_NAME.to_string());
    for window in &mut config.app.windows {
        if window.label == "main" {
            window.title = ISOLATED_NAME.to_string();
        }
    }
    if let Some(schemes) = config
        .plugins
        .0
        .get_mut("deep-link")
        .and_then(|deep_link| deep_link.pointer_mut("/desktop/schemes"))
    {
        *schemes = serde_json::json!([ISOLATED_LINK_SCHEME]);
    }
}

pub fn is_isolated() -> bool {
    ISOLATED.load(Ordering::Relaxed)
}

pub fn initialization_script() -> String {
    format!("window.__ASTRLINK_ISOLATED__ = {};", is_isolated())
}

/// The name this instance goes by, as its login item and app bundle show it.
pub fn product_name() -> &'static str {
    if is_isolated() {
        ISOLATED_NAME
    } else {
        "AstrLink"
    }
}

pub fn default_inference_port() -> u16 {
    if is_isolated() {
        ISOLATED_INFERENCE_PORT
    } else {
        crate::preferences::DEFAULT_INFERENCE_PORT
    }
}

pub fn link_scheme() -> &'static str {
    if is_isolated() {
        ISOLATED_LINK_SCHEME
    } else {
        LINK_SCHEME
    }
}

/// Tauri names an instance's data and config directories after its
/// identifier, so the other instance's sit beside this one's. Lists that
/// directory for every instance, in the same order whichever one asks; a
/// directory named otherwise stands alone.
pub fn every_instance_directory(directory: &Path) -> Vec<PathBuf> {
    let identifiers = [IDENTIFIER, ISOLATED_IDENTIFIER];
    match (
        directory.parent(),
        directory.file_name().and_then(|name| name.to_str()),
    ) {
        (Some(parent), Some(name)) if identifiers.contains(&name) => identifiers
            .iter()
            .map(|identifier| parent.join(identifier))
            .collect(),
        _ => vec![directory.to_path_buf()],
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_dev_config_selects_the_isolated_identifier() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.dev.conf.json")).unwrap();
        assert_eq!(config["identifier"], ISOLATED_IDENTIFIER);
        assert_eq!(config["productName"], ISOLATED_NAME);
    }

    #[test]
    fn the_shared_instance_goes_by_the_configured_product_name() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        assert_eq!(config["productName"], product_name());
        assert_eq!(config["identifier"], IDENTIFIER);
    }

    #[test]
    fn both_instances_list_the_same_directories() {
        let support = Path::new("/home/a/.local/share");
        let every = [support.join(IDENTIFIER), support.join(ISOLATED_IDENTIFIER)];
        for own in &every {
            assert_eq!(every_instance_directory(own), every);
        }
        let other = support.join("com.astrlink.desktop.dev");
        assert_eq!(
            every_instance_directory(&other),
            std::slice::from_ref(&other)
        );
        assert_eq!(
            every_instance_directory(Path::new("/")),
            [PathBuf::from("/")]
        );
    }

    #[test]
    fn the_isolated_identity_registers_its_own_link_scheme() {
        let mut config: tauri::Config =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        assert_eq!(
            config.plugins.0["deep-link"]["desktop"]["schemes"],
            serde_json::json!([LINK_SCHEME])
        );
        isolate(&mut config);
        assert_eq!(config.product_name.as_deref(), Some(ISOLATED_NAME));
        assert_eq!(config.app.windows[0].title, ISOLATED_NAME);
        assert_eq!(
            config.plugins.0["deep-link"]["desktop"]["schemes"],
            serde_json::json!([ISOLATED_LINK_SCHEME])
        );
    }
}

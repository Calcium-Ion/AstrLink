//! Give macOS a real application identity, including during `tauri dev`.

use std::{path::Path, sync::OnceLock};

use core_foundation::{
    base::TCFType,
    bundle::CFBundle,
    string::CFString,
    url::{CFURLRef, CFURL},
};

/// The app bundle a development build runs from. `make start` keeps the
/// system identity development builds always had; `make dev` stages its own,
/// so both can run at once.
#[cfg(any(dev, test))]
struct DevBundle {
    directory: &'static str,
    identifier: &'static str,
    name: &'static str,
    link_scheme: &'static str,
    icon: &'static [u8],
}

#[cfg(any(dev, test))]
const DEV_BUNDLE: DevBundle = DevBundle {
    directory: "AstrLink.app",
    identifier: "com.astrlink.desktop.dev",
    name: "AstrLink",
    link_scheme: crate::instance::LINK_SCHEME,
    icon: include_bytes!("../icons/icon.icns"),
};

#[cfg(any(dev, test))]
const ISOLATED_BUNDLE: DevBundle = DevBundle {
    directory: "AstrLink Dev.app",
    identifier: crate::instance::ISOLATED_IDENTIFIER,
    name: crate::instance::ISOLATED_NAME,
    link_scheme: crate::instance::ISOLATED_LINK_SCHEME,
    icon: include_bytes!("../icons/dev/icon.icns"),
};

#[cfg(any(dev, test))]
impl DevBundle {
    fn icon_filename(&self) -> String {
        use sha2::{Digest, Sha256};

        // Give an updated icon a new resource path for the bundle cache.
        format!("icon-{:x}.icns", Sha256::digest(self.icon))
    }
}

#[link(name = "CoreServices", kind = "framework")]
extern "C" {
    fn LSRegisterURL(url: CFURLRef, update: u8) -> i32;
}

pub(crate) fn bundle_for_executable(executable: &Path) -> Option<&Path> {
    let macos = executable.parent()?;
    let contents = macos.parent()?;
    let bundle = contents.parent()?;
    (macos.file_name()? == "MacOS"
        && contents.file_name()? == "Contents"
        && bundle.extension()? == "app")
        .then_some(bundle)
}

fn register_bundle(bundle: &Path) -> Result<(), String> {
    let url = CFURL::from_path(bundle, true).ok_or("invalid AstrLink app bundle path")?;
    // SAFETY: `url` remains alive throughout this synchronous Launch Services
    // call; update is the Core Foundation Boolean value true.
    let status = unsafe { LSRegisterURL(url.as_concrete_TypeRef(), 1) };
    if status != 0 {
        return Err(format!(
            "unable to register {}: OSStatus {status}",
            bundle.display()
        ));
    }
    Ok(())
}

/// The notification backend accepts an identity only after Launch Services has
/// registered its bundle. Never borrow Terminal's identity: it also changes the
/// app icon used when Cocoa restores our Dock entry.
pub fn notify(title: String, body: String) {
    static IDENTITY: OnceLock<Result<(), String>> = OnceLock::new();
    let identity = IDENTITY.get_or_init(|| {
        let executable = std::env::current_exe().map_err(|error| error.to_string())?;
        let bundle = bundle_for_executable(&executable)
            .ok_or("AstrLink notifications require an .app bundle")?;
        register_bundle(bundle)?;
        let native_bundle = CFURL::from_path(bundle, true)
            .and_then(CFBundle::new)
            .ok_or("unable to read AstrLink app bundle")?;
        let info = native_bundle.info_dictionary();
        let identifier = info
            .find(CFString::new("CFBundleIdentifier"))
            .and_then(|value| value.downcast::<CFString>())
            .ok_or("AstrLink app bundle has no identifier")?;
        mac_notification_sys::set_application(&identifier.to_string())
            .map_err(|error| error.to_string())
    });
    if let Err(error) = identity {
        eprintln!("unable to initialize AstrLink notifications: {error}");
        return;
    }
    // The native delivery call may wait for XPC. Keep it off the event loop and
    // async workers, and report its actual result instead of dropping errors.
    tauri::async_runtime::spawn_blocking(move || {
        if let Err(error) = mac_notification_sys::send_notification(&title, None, &body, None) {
            eprintln!("unable to send AstrLink notification: {error}");
        }
    });
}

#[cfg(dev)]
pub fn enter_dev_bundle() -> Result<(), Box<dyn std::error::Error>> {
    use std::{os::unix::process::CommandExt, process::Command};

    if !tauri::is_dev() {
        return Ok(());
    }
    let executable = std::env::current_exe()?;
    if bundle_for_executable(&executable).is_some() {
        return Ok(());
    }
    let frameworks = Path::new(env!("CARGO_MANIFEST_DIR")).join("binaries");
    let target = if crate::instance::is_isolated() {
        &ISOLATED_BUNDLE
    } else {
        &DEV_BUNDLE
    };
    let bundled_executable = stage_dev_bundle(&executable, &frameworks, target)?;
    let bundle = bundle_for_executable(&bundled_executable).ok_or("invalid development bundle")?;
    register_bundle(bundle)?;
    Err(Command::new(bundled_executable)
        .args(std::env::args_os().skip(1))
        .exec()
        .into())
}

/// Why AstrLink asks to control Terminal or iTerm2, as macOS shows it.
#[cfg(any(dev, test))]
const APPLE_EVENTS_USAGE: &str =
    "AstrLink opens a Terminal or iTerm2 window for the API provider you choose.";

#[cfg(any(dev, test))]
fn stage_dev_bundle(
    executable: &Path,
    frameworks: &Path,
    target: &DevBundle,
) -> std::io::Result<std::path::PathBuf> {
    use std::{fs, io};

    let directory = executable
        .parent()
        .ok_or_else(|| io::Error::other("executable has no parent"))?;
    let contents = directory.join(target.directory).join("Contents");
    let macos = contents.join("MacOS");
    let resources = contents.join("Resources");
    fs::create_dir_all(&macos)?;
    fs::create_dir_all(&resources)?;
    // ONNX workers resolve their dylib from Contents/Frameworks in an app bundle.
    replace_symlink(frameworks, &contents.join("Frameworks"))?;

    let icon_filename = target.icon_filename();
    let plist = format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>{identifier}</string>
<key>CFBundleName</key><string>{name}</string>
<key>CFBundleDisplayName</key><string>{name}</string>
<key>CFBundleExecutable</key><string>astrlink-desktop</string>
<key>CFBundleIconFile</key><string>{icon_filename}</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>CFBundleURLTypes</key><array><dict>
<key>CFBundleURLName</key><string>{name}</string>
<key>CFBundleURLSchemes</key><array><string>{link_scheme}</string></array>
</dict></array>
<key>LSMinimumSystemVersion</key><string>13.4</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSAppleEventsUsageDescription</key><string>{apple_events}</string>
</dict></plist>
"#,
        identifier = target.identifier,
        name = target.name,
        link_scheme = target.link_scheme,
        // Release builds take it from src-tauri/Info.plist.
        apple_events = APPLE_EVENTS_USAGE,
    );
    fs::write(contents.join("Info.plist"), plist)?;
    fs::write(resources.join(icon_filename), target.icon)?;

    // A symlink for the main executable gets canonicalized back to Cargo's bare
    // binary. Copy it atomically so both Cocoa and Tauri see the bundle path.
    let bundled_executable = macos.join("astrlink-desktop");
    let temporary = macos.join(format!(".astrlink-desktop-{}", std::process::id()));
    fs::copy(executable, &temporary)?;
    fs::rename(temporary, &bundled_executable)?;
    // Keep sidecars next to the host, as tauri-plugin-shell expects. Links track
    // Cargo/Tauri replacing these files on subsequent development rebuilds.
    for name in [
        "astrlink-core",
        "astrlink-privacy-worker",
        "astrlink-classifier-worker",
        "astrlink-cli",
    ] {
        replace_symlink(&directory.join(name), &macos.join(name))?;
    }
    Ok(bundled_executable)
}

#[cfg(any(dev, test))]
fn replace_symlink(target: &Path, link: &Path) -> std::io::Result<()> {
    use std::{fs, io, os::unix::fs::symlink};
    if fs::read_link(link).ok().as_deref() == Some(target) {
        return Ok(());
    }
    match fs::remove_file(link) {
        Ok(()) => {}
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => return Err(error),
    }
    symlink(target, link)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn recognizes_bundle_executables_without_relaunching_them() {
        for path in [
            "/Applications/AstrLink.app/Contents/MacOS/astrlink-desktop",
            "/tmp/target/debug/AstrLink Dev.app/Contents/MacOS/astrlink-desktop",
        ] {
            assert!(bundle_for_executable(Path::new(path)).is_some());
        }
        assert!(bundle_for_executable(Path::new("/tmp/target/debug/astrlink-desktop")).is_none());
        assert!(
            bundle_for_executable(Path::new("/tmp/app/Contents/MacOS/astrlink-desktop")).is_none()
        );
    }

    #[test]
    fn development_bundle_preserves_branding_sidecars_and_rebuilds() {
        use std::{fs, os::unix::fs::PermissionsExt};
        let directory =
            std::env::temp_dir().join(format!("astrlink-dev-bundle-{}", std::process::id()));
        fs::create_dir_all(&directory).unwrap();
        let executable = directory.join("astrlink-desktop");
        fs::write(&executable, b"first build").unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o755)).unwrap();
        fs::write(directory.join("astrlink-core"), b"core").unwrap();
        let frameworks = directory.join("frameworks");
        fs::create_dir_all(&frameworks).unwrap();
        fs::write(frameworks.join("runtime.dylib"), b"runtime").unwrap();
        let bundled = stage_dev_bundle(&executable, &frameworks, &DEV_BUNDLE).unwrap();
        assert_eq!(fs::read(&bundled).unwrap(), b"first build");
        assert!(!fs::symlink_metadata(&bundled)
            .unwrap()
            .file_type()
            .is_symlink());
        assert_eq!(
            fs::metadata(&bundled).unwrap().permissions().mode() & 0o777,
            0o755
        );
        assert_eq!(
            fs::read(bundled.parent().unwrap().join("astrlink-core")).unwrap(),
            b"core"
        );
        let contents = bundle_for_executable(&bundled).unwrap().join("Contents");
        assert_eq!(
            fs::read(contents.join("Frameworks/runtime.dylib")).unwrap(),
            b"runtime"
        );
        assert_eq!(
            fs::read(contents.join("Resources").join(DEV_BUNDLE.icon_filename())).unwrap(),
            include_bytes!("../icons/icon.icns")
        );
        // Read through macOS's bundle API, not a string match on generated XML.
        let bundle =
            CFBundle::new(CFURL::from_path(contents.parent().unwrap(), true).unwrap()).unwrap();
        let info = bundle.info_dictionary();
        for (key, expected) in [
            ("CFBundleIdentifier", DEV_BUNDLE.identifier),
            ("CFBundleDisplayName", "AstrLink"),
            ("CFBundleIconFile", DEV_BUNDLE.icon_filename().as_str()),
            ("NSAppleEventsUsageDescription", APPLE_EVENTS_USAGE),
        ] {
            let value = info.get(CFString::new(key)).downcast::<CFString>().unwrap();
            assert_eq!(value.to_string(), expected);
        }
        fs::write(&executable, b"second build").unwrap();
        assert_eq!(
            stage_dev_bundle(&executable, &frameworks, &DEV_BUNDLE).unwrap(),
            bundled
        );
        assert_eq!(fs::read(&bundled).unwrap(), b"second build");

        // `make dev` stages beside it, so both keep running.
        let isolated = stage_dev_bundle(&executable, &frameworks, &ISOLATED_BUNDLE).unwrap();
        assert_ne!(isolated, bundled);
        let isolated_bundle = bundle_for_executable(&isolated).unwrap();
        assert_eq!(isolated_bundle.file_name().unwrap(), "AstrLink Dev.app");
        assert_eq!(
            fs::read(
                isolated_bundle
                    .join("Contents/Resources")
                    .join(ISOLATED_BUNDLE.icon_filename())
            )
            .unwrap(),
            include_bytes!("../icons/dev/icon.icns")
        );
        let info = CFBundle::new(CFURL::from_path(isolated_bundle, true).unwrap())
            .unwrap()
            .info_dictionary();
        for (key, expected) in [
            ("CFBundleIdentifier", crate::instance::ISOLATED_IDENTIFIER),
            ("CFBundleDisplayName", crate::instance::ISOLATED_NAME),
            ("CFBundleIconFile", ISOLATED_BUNDLE.icon_filename().as_str()),
        ] {
            let value = info.get(CFString::new(key)).downcast::<CFString>().unwrap();
            assert_eq!(value.to_string(), expected);
        }
        assert_eq!(fs::read(&bundled).unwrap(), b"second build");
        fs::remove_dir_all(directory).unwrap();
    }
}

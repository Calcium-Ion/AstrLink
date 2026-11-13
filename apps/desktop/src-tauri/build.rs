fn main() {
    println!("cargo:rerun-if-env-changed=TAURI_UPDATER_PUBLIC_KEY");
    let update_key = std::env::var("TAURI_UPDATER_PUBLIC_KEY").unwrap_or_default();
    assert!(
        !update_key.contains(['\n', '\r']),
        "updater public key must be a single base64 line"
    );
    println!(
        "cargo:rustc-env=TAURI_UPDATER_PUBLIC_KEY={}",
        update_key.trim()
    );
    let attributes = tauri_build::Attributes::new();
    let attributes = if std::env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc") {
        let manifest =
            std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("windows-app-manifest.xml");
        // tauri-build links its manifest only into bins, leaving lib unit tests without v6 controls.
        println!("cargo:rerun-if-changed={}", manifest.display());
        println!("cargo:rustc-link-arg=/MANIFEST:EMBED");
        println!("cargo:rustc-link-arg=/MANIFESTINPUT:{}", manifest.display());
        attributes.windows_attributes(tauri_build::WindowsAttributes::new_without_app_manifest())
    } else {
        attributes
    };
    tauri_build::try_build(attributes).expect("failed to build Tauri application");
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        build_macos_widget_bridge();
    }
}

/// Compiles `src/macos_widget_bridge.swift`, the host's handle on WidgetKit
/// (a Swift-only API), into a static library and links the Swift runtime
/// macOS ships in /usr/lib/swift.
fn build_macos_widget_bridge() {
    use std::{path::PathBuf, process::Command};

    let source = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("src/macos_widget_bridge.swift");
    println!("cargo:rerun-if-changed={}", source.display());
    println!("cargo:rerun-if-env-changed=MACOSX_DEPLOYMENT_TARGET");
    let xcrun = |args: &[&str]| -> String {
        let output = Command::new("xcrun")
            .args(args)
            .output()
            .expect("xcrun is required to build the macOS widget bridge");
        assert!(output.status.success(), "xcrun {args:?} failed");
        String::from_utf8(output.stdout)
            .expect("xcrun output is UTF-8")
            .trim()
            .to_string()
    };
    let sdk = xcrun(&["--sdk", "macosx", "--show-sdk-path"]);
    let swiftc = PathBuf::from(xcrun(&["--find", "swiftc"]));
    let arch = match std::env::var("CARGO_CFG_TARGET_ARCH").as_deref() {
        Ok("aarch64") => "arm64",
        Ok("x86_64") => "x86_64",
        other => panic!("unsupported macOS architecture {other:?}"),
    };
    // tauri.conf.json's minimumSystemVersion; CI pins the same value.
    let deployment =
        std::env::var("MACOSX_DEPLOYMENT_TARGET").unwrap_or_else(|_| "13.4".to_string());
    let out = PathBuf::from(std::env::var("OUT_DIR").expect("OUT_DIR is set"));
    let status = Command::new(&swiftc)
        .args(["-emit-library", "-static", "-parse-as-library", "-O"])
        .args([
            "-swift-version",
            "5",
            "-module-name",
            "AstrLinkWidgetBridge",
        ])
        .args(["-target", &format!("{arch}-apple-macos{deployment}")])
        .args(["-sdk", &sdk])
        .arg(&source)
        .arg("-o")
        .arg(out.join("libastrlink_widget_bridge.a"))
        .current_dir(&out)
        .status()
        .expect("swiftc is required to build the macOS widget bridge");
    assert!(status.success(), "swiftc failed on {}", source.display());
    println!("cargo:rustc-link-search=native={}", out.display());
    println!("cargo:rustc-link-lib=static=astrlink_widget_bridge");
    // Link stubs for the system Swift runtime, and the toolchain's
    // back-deployment shims the Swift objects ask for by name.
    println!("cargo:rustc-link-search=native={sdk}/usr/lib/swift");
    if let Some(toolchain) = swiftc.parent().and_then(|bin| bin.parent()) {
        println!(
            "cargo:rustc-link-search=native={}",
            toolchain.join("lib/swift/macosx").display()
        );
    }
}

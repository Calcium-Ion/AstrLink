# AstrLink sidecar staging

Tauri resolves the Go Core, the Rust privacy worker, and the Rust classifier
worker as external binaries. Before `tauri dev` or `tauri build`, run
`bun run sidecar:build` from `apps/desktop`. The script builds those binaries
and writes the platform-specific names expected by Tauri:

```text
astrlink-core-<rust-host-target>[.exe]
astrlink-privacy-worker-<rust-host-target>[.exe]
astrlink-classifier-worker-<rust-host-target>[.exe]
astrlink-mcp-<rust-host-target>[.exe]
```

Generated sidecars are ignored and must not be committed.

The same build step verifies and stages the pinned ONNX Runtime license and
third-party notices. Tauri bundles them under
`Resources/notices/onnxruntime-1.23.2/` on every desktop target.

On Linux x64, the build also verifies and stages the official versioned
`libonnxruntime.so.1.23.2`. Tauri installs it under the application resource
directory, and the desktop shell passes its resolved absolute path to Core
through `ASTRLINK_ONNX_RUNTIME_PATH`; the privacy worker and classifier worker
inherit that internal environment variable. Both workers reuse the same library.
macOS bundles `libonnxruntime.1.23.2.dylib` in Frameworks and uses system-provided
Apple libraries.

On Windows x64, `ort` supplies the workers' runtime and the build stages their
matching `DirectML.dll`. It also downloads and verifies the pinned official
`vc_redist.x64.exe`, plus a generated `vc_redist.x64.nsh` version definition.
The NSIS preinstall hook embeds that executable as a temporary prerequisite;
users do not need to download the Visual C++ runtime themselves. Do not add the
redistributable to Tauri resources: it is only needed during installation and
must not be left in the installed application directory.

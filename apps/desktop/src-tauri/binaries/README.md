# AstrLink sidecar staging

Tauri resolves the Go Core and the Rust privacy worker as external binaries.
Before `tauri dev` or `tauri build`, run `bun run sidecar:build` from
`apps/desktop`. The script builds both binaries and writes the platform-specific
names expected by Tauri:

```text
astrlink-core-<rust-host-target>[.exe]
astrlink-privacy-worker-<rust-host-target>[.exe]
```

Generated sidecars are ignored and must not be committed.

The same build step verifies and stages the pinned ONNX Runtime license and
third-party notices. Tauri bundles them under
`Resources/notices/onnxruntime-1.23.2/` on every desktop target.

On Linux x64, the build also verifies and stages the official versioned
`libonnxruntime.so.1.23.2`. Tauri installs it under the application resource
directory, and the desktop shell passes its resolved absolute path to Core
through `ASTRLINK_ONNX_RUNTIME_PATH`; the privacy worker inherits that internal
environment variable. macOS keeps its Frameworks loading path, and Windows
keeps the `ort`-managed runtime staging path.

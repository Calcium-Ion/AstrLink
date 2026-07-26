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

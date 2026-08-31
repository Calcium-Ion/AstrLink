BUN ?= bun

.PHONY: check core-check core-race contracts-check contracts-race desktop-install desktop-check desktop-sidecar desktop-rust-check privacy-worker-check classifier-worker-check classifier-ort-gate classifier-bench model-safety-check dev

check: model-safety-check core-check contracts-check desktop-check privacy-worker-check classifier-worker-check desktop-rust-check

model-safety-check:
	$(BUN) scripts/check-no-production-models.mjs

core-check:
	cd core && ASTRLINK_CI_NO_REMOTE_MODELS=1 go test ./...

core-race:
	cd core && ASTRLINK_CI_NO_REMOTE_MODELS=1 go test -race ./...

contracts-check:
	ruby contracts/validate.rb
	cd contracts && go test ./...
	cd core && go test ./contract -run TestDefaultCapabilitiesMatchesFrozenFixture

contracts-race:
	cd contracts && go test -race ./...

desktop-install:
	cd apps/desktop && $(BUN) install --frozen-lockfile

desktop-check:
	cd apps/desktop && $(BUN) run check

desktop-sidecar:
	cd apps/desktop && $(BUN) run sidecar:build

privacy-worker-check: desktop-sidecar
	cargo fmt --manifest-path apps/privacy-worker/Cargo.toml -- --check
	cargo clippy --locked --manifest-path apps/privacy-worker/Cargo.toml --all-targets -- -D warnings
	ASTRLINK_CI_SYNTHETIC_MODELS_ONLY=1 cargo test --locked --manifest-path apps/privacy-worker/Cargo.toml --all-targets

classifier-worker-check: desktop-sidecar
	cargo fmt --manifest-path apps/classifier-worker/Cargo.toml -- --check
	cargo clippy --locked --manifest-path apps/classifier-worker/Cargo.toml --all-targets -- -D warnings
	ASTRLINK_CI_SYNTHETIC_MODELS_ONLY=1 cargo test --locked --manifest-path apps/classifier-worker/Cargo.toml --all-targets

# Dev-only: compare the local experimental bundle against the ORT 1.23.2 golden.
# Not part of check; ASTRLINK_CI_SYNTHETIC_MODELS_ONLY refuses this target.
classifier-ort-gate:
	cargo run --locked --offline --bin golden_align --manifest-path apps/classifier-worker/Cargo.toml

# Dev-only: score a holdout set against a local bundle. Insufficient sets print
# 「评测集不足，不产出判定」instead of a gate verdict. Not part of check.
classifier-bench:
	cargo run --locked --offline --bin classifier_bench --manifest-path apps/classifier-worker/Cargo.toml

desktop-rust-check: desktop-sidecar
	cd apps/desktop/src-tauri && cargo fmt --check --all
	cd apps/desktop/src-tauri && cargo clippy --locked --all-targets -- -D warnings
	cd apps/desktop/src-tauri && cargo test --locked --all-targets

# One-shot local desktop: install JS deps, build Go/Rust sidecars, start Tauri.
# Requires Bun, Go, Rust, and Tauri platform prerequisites on PATH.
dev: desktop-install
	@command -v $(BUN) >/dev/null || { echo "error: bun not found on PATH"; exit 1; }
	@command -v go >/dev/null || { echo "error: go not found on PATH (need Go 1.23+)"; exit 1; }
	@command -v rustc >/dev/null || { echo "error: rustc not found on PATH (need Rust stable)"; exit 1; }
	@command -v cargo >/dev/null || { echo "error: cargo not found on PATH"; exit 1; }
	cd apps/desktop && $(BUN) run desktop:dev

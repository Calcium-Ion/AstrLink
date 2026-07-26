BUN ?= bun

.PHONY: check core-check core-race contracts-check contracts-race desktop-install desktop-check desktop-sidecar desktop-rust-check privacy-worker-check model-safety-check

check: model-safety-check core-check contracts-check desktop-check privacy-worker-check desktop-rust-check

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

desktop-rust-check: desktop-sidecar
	cd apps/desktop/src-tauri && cargo fmt --check --all
	cd apps/desktop/src-tauri && cargo clippy --locked --all-targets -- -D warnings
	cd apps/desktop/src-tauri && cargo test --locked --all-targets

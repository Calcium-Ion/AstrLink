# syntax=docker/dockerfile:1.27.1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# AstrLink server edition: Core with the embedded web console, the privacy
# worker and ONNX Runtime, for linux/amd64 and linux/arm64.
#
#   docker buildx build --platform linux/arm64 --load -t astrlink:local .

ARG TARGETARCH

# Web console, built and staged like `make web`. Its output is the same on
# every platform, so it builds once on the build machine.
FROM --platform=$BUILDPLATFORM oven/bun:1.3.14@sha256:e10577f0db68676a7024391c6e5cb4b879ebd17188ab750cf10024a6d700e5c4 AS web
WORKDIR /src/apps/desktop
COPY apps/desktop/package.json apps/desktop/bun.lock apps/desktop/bunfig.toml ./
RUN --mount=type=cache,target=/root/.bun/install/cache \
    bun install --frozen-lockfile
COPY assets/branding/ /src/assets/branding/
COPY contracts/examples/ /src/contracts/examples/
COPY core/internal/console/webui/placeholder.html /src/core/internal/console/webui/
COPY apps/desktop/ ./
RUN bun run build:web && cd /src && bun apps/desktop/scripts/stage-web.mjs

# Core and the CLI, cross-compiled without cgo.
FROM --platform=$BUILDPLATFORM golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS go
ARG TARGETOS TARGETARCH
# Set by CI to stamp the source revision; the image has no .git to read it from.
ARG ASTRLINK_COMMIT=unknown
WORKDIR /src
COPY core/go.mod core/go.sum core/
COPY convo/go.mod convo/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd core && go mod download
COPY convo/ convo/
COPY core/ core/
COPY --from=web /src/core/internal/console/webui/ core/internal/console/webui/
COPY apps/desktop/package.json apps/desktop/
# Core reports the desktop package version, as the desktop sidecar build does.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    version=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' apps/desktop/package.json) \
    && test -n "$version" \
    && cd core \
    && export CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    && go build -trimpath -tags webui \
        -ldflags "-X github.com/QuantumNous/astrlink/core/internal/buildinfo.Version=$version -X github.com/QuantumNous/astrlink/core/internal/buildinfo.Commit=$ASTRLINK_COMMIT" \
        -o /out/astrlink-core ./cmd/astrlink-core \
    && go build -trimpath -o /out/astrlink-cli ./cmd/astrlink-cli

# Privacy worker. It builds natively for each platform: oniguruma is C code,
# and a native build needs no cross toolchain.
FROM rust:1.97.1-bookworm@sha256:0e2bcaef56d041a486784e54104a81aebe0da44bd03019bd70bc0401e42e4a97 AS rust
ARG TARGETARCH
WORKDIR /src/apps/privacy-worker
COPY apps/privacy-worker/Cargo.toml apps/privacy-worker/Cargo.lock ./
COPY apps/privacy-worker/src/ src/
RUN --mount=type=cache,target=/usr/local/cargo/registry,id=cargo-registry-$TARGETARCH \
    --mount=type=cache,target=/src/apps/privacy-worker/target,id=privacy-worker-target-$TARGETARCH \
    cargo build --locked --release \
    && install -D target/release/astrlink-privacy-worker /out/astrlink-privacy-worker

# ONNX Runtime 1.23.2, the official archive for each architecture.
FROM scratch AS onnxruntime-amd64
ADD --checksum=sha256:1fa4dcaef22f6f7d5cd81b28c2800414350c10116f5fdd46a2160082551c5f9b \
    https://github.com/microsoft/onnxruntime/releases/download/v1.23.2/onnxruntime-linux-x64-1.23.2.tgz /onnxruntime.tgz

FROM scratch AS onnxruntime-arm64
ADD --checksum=sha256:7c63c73560ed76b1fac6cff8204ffe34fe180e70d6582b5332ec094810241e5c \
    https://github.com/microsoft/onnxruntime/releases/download/v1.23.2/onnxruntime-linux-aarch64-1.23.2.tgz /onnxruntime.tgz

FROM onnxruntime-${TARGETARCH} AS onnxruntime-archive

FROM --platform=$BUILDPLATFORM debian:12-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS onnxruntime
COPY --from=onnxruntime-archive /onnxruntime.tgz /tmp/
# The license and notices are the files the desktop bundle pins.
RUN mkdir -p /tmp/ort /out \
    && tar -xzf /tmp/onnxruntime.tgz -C /tmp/ort --strip-components=1 \
    && cd /tmp/ort \
    && printf '%s  %s\n' \
        2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c LICENSE \
        e9e90971a8e75a9a8ac0c6412e29c1202d079998389915aa485f46c816c3b4cc ThirdPartyNotices.txt \
        | sha256sum -c - \
    && cp lib/libonnxruntime.so.1.23.2 LICENSE ThirdPartyNotices.txt /out/

FROM debian:12-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 astrlink \
    && useradd --system --uid 10001 --gid astrlink --home-dir /home/astrlink --shell /usr/sbin/nologin astrlink \
    && install -d -o astrlink -g astrlink -m 0700 /home/astrlink /data

# Core finds the worker beside its own executable, and the worker finds ONNX
# Runtime beside its own.
COPY --from=go /out/astrlink-core /out/astrlink-cli /opt/astrlink/
COPY --from=rust /out/astrlink-privacy-worker /opt/astrlink/
COPY --from=onnxruntime /out/libonnxruntime.so.1.23.2 /opt/astrlink/
COPY --chmod=0755 docker/run-as-astrlink.sh /opt/astrlink/run-as-astrlink
COPY --chmod=0755 docker/entrypoint.sh /usr/local/bin/astrlink-entrypoint
RUN ln -s /opt/astrlink/run-as-astrlink /usr/local/bin/astrlink \
    && ln -s /opt/astrlink/run-as-astrlink /usr/local/bin/astrlink-core

COPY LICENSE LICENSING.md /usr/share/doc/astrlink/licenses/
COPY LICENSES/AGPL-3.0.txt /usr/share/doc/astrlink/licenses/LICENSES/
COPY --from=onnxruntime /out/LICENSE /usr/share/doc/astrlink/notices/onnxruntime-1.23.2/LICENSE.txt
COPY --from=onnxruntime /out/ThirdPartyNotices.txt /usr/share/doc/astrlink/notices/onnxruntime-1.23.2/ThirdPartyNotices.txt

LABEL org.opencontainers.image.title="AstrLink" \
      org.opencontainers.image.description="AstrLink server edition: privacy gateway with a web console" \
      org.opencontainers.image.source="https://github.com/Calcium-Ion/AstrLink"

# `docker exec <container> astrlink ...` reaches Core through this socket.
ENV ASTRLINK_CONTROL_SOCKET=/data/control.sock
VOLUME /data
EXPOSE 8317
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --start-interval=2s --retries=3 \
    CMD ["astrlink-core", "healthcheck"]
# tini forwards SIGTERM to Core and reaps worker processes.
ENTRYPOINT ["tini", "--", "astrlink-entrypoint"]
CMD ["serve"]

# syntax=docker/dockerfile:1
# typst-server: Go handler (stdlib only) + prebuilt typst CLI.
#
# Nothing heavy is compiled on the build machine: the Go handler builds in
# seconds and the typst engine is copied from the official image. TYPST_VERSION
# is pinned for reproducible builds - PDF output can change between typst
# releases, so bump it deliberately (and update the README).
ARG TYPST_VERSION=0.15.1
# Service release tag; the shared build CLI passes VERSION from package.json.
ARG VERSION=dev

# The handler cross-compiles for the target (CGO off, pure Go), so foreign
# archs need no QEMU emulation: on an amd64 host an arm64 image compiles
# natively for arm64 instead of running the whole toolchain under emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
WORKDIR /app
COPY go.mod ./
COPY main.go ./
COPY internal ./internal
# Cache mounts: repeated builds reuse the Go build cache (the stdlib compile
# is the expensive part) and the module cache, instead of starting cold every
# time a source file changes.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o typst-server .

FROM ghcr.io/typst/typst:${TYPST_VERSION} AS typst

FROM alpine:3.24
ARG VERSION
LABEL org.opencontainers.image.title="typst-server" \
    org.opencontainers.image.description="Self-hosted Typst to PDF render service" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.source="https://github.com/adopon/typst-server" \
    org.opencontainers.image.licenses="MIT"
# Font discovery: XDG_DATA_HOME=/app makes typst scan /app/fonts, which is the
# mount point for the shared font pool. HOME=/tmp gives the non-root user a
# writable cache dir if a template ever pulls a package.
ENV XDG_DATA_HOME=/app \
    HOME=/tmp
# Liberation Sans is the baseline for invoice/certificate templates; the
# typst-embedded set only carries the fallback (Libertinus).
RUN apk add --no-cache font-liberation \
    && mkdir -p /app/fonts \
    && chmod 1777 /tmp
COPY --from=typst /bin/typst /usr/local/bin/typst
COPY --from=builder /app/typst-server /usr/local/bin/typst-server
USER 65534:65534
EXPOSE 3100
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:3100/health || exit 1
ENTRYPOINT ["/usr/local/bin/typst-server"]

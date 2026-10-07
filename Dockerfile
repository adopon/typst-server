# syntax=docker/dockerfile:1
# ponado-typst: Go handler (stdlib only) + prebuilt typst CLI.
#
# Nothing heavy is compiled on the build machine: the Go handler builds in
# seconds and the typst engine is copied from the official image. TYPST_VERSION
# is pinned for reproducible builds - PDF output can change between typst
# releases, so bump it deliberately (and update the README).
ARG TYPST_VERSION=0.15.1
# Service release tag; scripts/build-push.sh passes VERSION (from ./VERSION).
ARG VERSION=dev

# The handler cross-compiles for the target (CGO off, pure Go), so foreign
# archs need no QEMU emulation: on an amd64 host an arm64 image compiles
# natively for arm64 instead of running the whole toolchain under emulation.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
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
    -o ponado-typst .

FROM ghcr.io/typst/typst:${TYPST_VERSION} AS typst

FROM alpine:3.20
ARG VERSION
LABEL org.opencontainers.image.title="ponado-typst" \
    org.opencontainers.image.description="Shared Typst to PDF renderer" \
    org.opencontainers.image.source="https://git.ponado.lt/ponado/ponado-typst" \
    org.opencontainers.image.version="${VERSION}"
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
COPY --from=builder /app/ponado-typst /usr/local/bin/ponado-typst
USER 65534:65534
EXPOSE 3100
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:3100/health || exit 1
ENTRYPOINT ["/usr/local/bin/ponado-typst"]

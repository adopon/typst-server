# syntax=docker/dockerfile:1
# ponado-typst: Go handler (stdlib only) + prebuilt typst CLI.
#
# Nothing heavy is compiled on the build machine: the Go handler builds in
# seconds and the typst engine is copied from the official image. TYPST_VERSION
# is pinned for reproducible builds - PDF output can change between typst
# releases, so bump it deliberately (and update the README).
ARG TYPST_VERSION=0.15.1

FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod main.go ./
# build FOR the target arch (no --platform=$BUILDPLATFORM): cross-arch binaries
# from a foreign build platform produce go "exec format" traps at runtime.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ponado-typst .

FROM ghcr.io/typst/typst:${TYPST_VERSION} AS typst

FROM alpine:3.20
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

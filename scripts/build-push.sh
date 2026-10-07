#!/usr/bin/env bash
set -euo pipefail

# Build + push the ponado-typst image (multi-arch).
#
# Inputs (env or .env.local):
#   IMAGE          full image ref (default: git.ponado.lt/ponado/ponado-typst)
#   TAG            image tag (default: ./VERSION, then git describe, then latest)
#   PLATFORMS      buildx platforms (default: linux/amd64,linux/arm64)
#   PUSH           "1" (default) push, "0" build locally with --load
#   TYPST_VERSION  typst CLI version to bake in (default: Dockerfile pin)
#   CACHE_FROM     buildx --cache-from (e.g. type=registry,ref=<ref>:buildcache)
#   CACHE_TO       buildx --cache-to (needs a driver with cache export, e.g.
#                  docker-container; the default docker driver cannot export)
#
# The resolved TAG is also baked into the binary (-X main.version) and the
# image label, so /version reports the exact release.
#
# Usage:
#   scripts/build-push.sh              # tag from ./VERSION
#   scripts/build-push.sh v0.1.1       # positional tag
#   TAG=v0.1.1 PUSH=0 scripts/build-push.sh

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$DIR"

set -a
source "$DIR/.env.local" 2>/dev/null || true
set +a

IMAGE="${IMAGE:-git.ponado.lt/ponado/ponado-typst}"
VERSION_FILE="$(tr -d '[:space:]' < "$DIR/VERSION" 2>/dev/null || true)"
TAG="${1:-${TAG:-${VERSION_FILE:-$(git describe --tags --always --dirty 2>/dev/null || echo latest)}}}"
PUSH="${PUSH:-1}"

# Warn when the working copy was tagged but VERSION disagrees (release.sh
# keeps them in sync; manual tags can drift).
if [[ -n "${VERSION_FILE}" ]]; then
    exact_tag="$(git describe --tags --exact-match 2>/dev/null || true)"
    if [[ -n "${exact_tag}" && "${exact_tag}" != "v${VERSION_FILE}" ]]; then
        echo "==> warning: VERSION (${VERSION_FILE}) does not match git tag ${exact_tag}" >&2
    fi
fi

# Local builds (--load) need a single platform; default to the host's arch.
if [[ "${PUSH}" != "1" && -z "${PLATFORMS:-}" ]]; then
    case "$(uname -m)" in
        aarch64 | arm64) PLATFORMS="linux/arm64" ;;
        *) PLATFORMS="linux/amd64" ;;
    esac
fi
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
if [[ "${PUSH}" != "1" && "${PLATFORMS}" == *,* ]]; then
    echo "==> PUSH=0 cannot --load a multi-platform build; set PLATFORMS to one platform" >&2
    exit 1
fi

build_args=(--build-arg "VERSION=${TAG}")
if [[ -n "${TYPST_VERSION:-}" ]]; then
    build_args+=(--build-arg "TYPST_VERSION=${TYPST_VERSION}")
fi

cache_args=()
if [[ -n "${CACHE_FROM:-}" ]]; then
    cache_args+=(--cache-from "${CACHE_FROM}")
fi
if [[ -n "${CACHE_TO:-}" ]]; then
    cache_args+=(--cache-to "${CACHE_TO}")
fi

tags=(-t "${IMAGE}:${TAG}")
if [[ "${PUSH}" == "1" ]]; then
    tags+=(-t "${IMAGE}:latest")
fi

if [[ "${PUSH}" == "1" ]]; then
    echo "==> Building + pushing [${PLATFORMS}] -> ${IMAGE}:${TAG}"
    docker buildx build --platform "${PLATFORMS}" --push "${build_args[@]}" "${cache_args[@]}" "${tags[@]}" .
else
    echo "==> Building [${PLATFORMS}] -> ${IMAGE}:${TAG} (local, PUSH=0)"
    docker buildx build --platform "${PLATFORMS}" --load "${build_args[@]}" "${cache_args[@]}" "${tags[@]}" .
fi

echo "==> Done: ${IMAGE}:${TAG}"

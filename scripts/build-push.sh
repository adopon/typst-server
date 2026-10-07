#!/usr/bin/env bash
set -euo pipefail

# Build + push the ponado-typst image (multi-arch).
#
# Inputs (env or .env.local):
#   IMAGE          full image ref (default: git.ponado.lt/adomas/ponado-typst)
#   TAG            image tag (default: git describe --tags --always, else latest)
#   PLATFORMS      buildx platforms (default: linux/amd64,linux/arm64)
#   PUSH           "1" (default) push, "0" build locally with --load
#   TYPST_VERSION  typst CLI version to bake in (default: Dockerfile pin)
#
# Usage:
#   scripts/build-push.sh             # defaults
#   scripts/build-push.sh v0.1.1      # positional tag
#   TAG=v0.1.1 PUSH=0 scripts/build-push.sh

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$DIR"

set -a
source "$DIR/.env.local" 2>/dev/null || true
set +a

IMAGE="${IMAGE:-git.ponado.lt/ponado/ponado-typst}"
TAG="${1:-${TAG:-$(git describe --tags --always --dirty 2>/dev/null || echo latest)}}"
PUSH="${PUSH:-1}"

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

build_args=()
if [[ -n "${TYPST_VERSION:-}" ]]; then
    build_args+=(--build-arg "TYPST_VERSION=${TYPST_VERSION}")
fi

tags=(-t "${IMAGE}:${TAG}")
if [[ "${PUSH}" == "1" ]]; then
    tags+=(-t "${IMAGE}:latest")
fi

if [[ "${PUSH}" == "1" ]]; then
    echo "==> Building + pushing [${PLATFORMS}] -> ${IMAGE}:${TAG}"
    docker buildx build --platform "${PLATFORMS}" --push "${build_args[@]}" "${tags[@]}" .
else
    echo "==> Building [${PLATFORMS}] -> ${IMAGE}:${TAG} (local, PUSH=0)"
    docker buildx build --platform "${PLATFORMS}" --load "${build_args[@]}" "${tags[@]}" .
fi

echo "==> Done: ${IMAGE}:${TAG}"

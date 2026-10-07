#!/usr/bin/env bash
set -euo pipefail

# Cut a ponado-typst release: bump VERSION, commit, tag vX.Y.Z, push both, then
# build + push the image. VERSION is the single source of truth - the git tag
# and the image tag follow it (scripts/build-push.sh reads the same file).
#
# Usage:
#   scripts/release.sh patch|minor|major   # bump from the current VERSION
#   scripts/release.sh 1.2.3               # set an explicit version
#
# Env:
#   BUILD=0        skip the image build/push
#   DRY_RUN=1      print the actions without touching git or the registry
#   ALLOW_BRANCH=1 skip the "must be on main" check

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$DIR"

usage() {
	cat >&2 <<'EOF'
usage: scripts/release.sh patch|minor|major|<x.y.z>
env:   BUILD=0 (skip image), DRY_RUN=1 (preview), ALLOW_BRANCH=1 (any branch)
EOF
	exit 1
}

run() {
	if [[ "${DRY_RUN:-0}" == "1" ]]; then
		echo "DRY: $*"
	else
		"$@"
	fi
}

current="$(tr -d '[:space:]' < VERSION 2>/dev/null || true)"
if [[ ! "${current}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "VERSION must be x.y.z (found: '${current}')" >&2
	exit 1
fi

IFS=. read -r major minor patch <<<"${current}"

case "${1:-}" in
patch) patch=$((patch + 1)) ;;
minor) minor=$((minor + 1)); patch=0 ;;
major) major=$((major + 1)); minor=0; patch=0 ;;
[0-9]*.[0-9]*.[0-9]*) IFS=. read -r major minor patch <<<"$1" ;;
*) usage ;;
esac

next="${major}.${minor}.${patch}"
tag="v${next}"

# --- preflight -------------------------------------------------------------
branch="$(git branch --show-current)"
if [[ "${ALLOW_BRANCH:-0}" != "1" && "${branch}" != "main" ]]; then
	echo "not on main (branch: '${branch}'); set ALLOW_BRANCH=1 to override" >&2
	exit 1
fi
if [[ -n "$(git status --porcelain)" ]]; then
	echo "working tree is not clean; commit or stash first" >&2
	exit 1
fi

run git fetch --tags origin
if git rev-parse -q --verify "refs/tags/${tag}" >/dev/null; then
	echo "tag ${tag} already exists" >&2
	exit 1
fi

# --- bump, commit, tag, push ------------------------------------------------
if [[ "${next}" == "${current}" ]]; then
	echo "==> VERSION is already ${next}; tagging without a bump commit"
else
	if [[ "${DRY_RUN:-0}" == "1" ]]; then
		echo "DRY: write ${next} to VERSION && git add VERSION"
	else
		printf '%s\n' "${next}" > VERSION
		git add VERSION
	fi
	run git commit -m "chore(release): ${tag}"
fi

run git tag -a "${tag}" -m "${tag}"
run git push origin HEAD
run git push origin "${tag}"
echo "==> Released ${tag}"

# --- image ------------------------------------------------------------------
if [[ "${BUILD:-1}" == "1" ]]; then
	run "$DIR/scripts/build-push.sh"
else
	echo "==> Skipped image build (BUILD=0); run scripts/build-push.sh to publish ${next}"
fi

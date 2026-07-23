#!/usr/bin/env bash
# Build every cmd/ binary into dist-go/<goos>-<goarch>/.
#
# Two uses: a local `go build` convenience for one platform, and the
# cross-compile-for-shipping step that stage-bundle.sh drives once per Linux
# arch to fill the prebuilt "two files and a binary" bundle. It does NOT feed
# any in-jail run — a nested `yolo -- bash` compiles the live /workspace
# checkout from source itself (the dev-override fast loop is gone; see
# AGENTS.md "Build & deploy"). dist-go/ (NOT dist/ — `just build` does
# `rm -rf dist/`) is gitignored, so build output here never poisons a nested
# nix build (a dirty git flake ignores untracked bin/).
#
# Default: build for the host's own GOOS/GOARCH (native).  Pass GOOS/GOARCH in
# the environment to cross-compile (e.g. stage-bundle.sh builds
# CGO_ENABLED=0 GOOS=linux for both amd64 and arm64). goprobe is built too (a
# deployment tripwire); the ship-set filter that drops it lives in
# stage-bundle.sh.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
OUTDIR="dist-go/${GOOS}-${GOARCH}"
mkdir -p "$OUTDIR"

# Discover every main package under cmd/ (glob, not find — the yolo jail
# blocks `find`, and a shell glob is portable to the host build too).
CMDS=()
for d in cmd/*/; do
    [ -d "$d" ] || continue
    CMDS+=("$(basename "$d")")
done

if [ "${#CMDS[@]}" -eq 0 ]; then
    echo "build-go: no cmd/ binaries found" >&2
    exit 1
fi

# Version + commit stamp for -ldflags -X (see internal/version).  A caller may
# pass VERSION/COMMIT in the environment (the shipping pipeline does this so the
# bundle's image binaries carry the release version even though a Homebrew
# source tarball has no .git). Otherwise best-effort from git — stamp EMPTY when
# git is unavailable (never the literal "unknown": a stamp is authoritative
# (D18), and an "unknown" stamp would shadow the binary's live-describe
# fallback).
VERSION="${VERSION:-$(git describe --tags --dirty --always 2>/dev/null || true)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || true)}"

for cmd in "${CMDS[@]}"; do
    echo "build-go: ${cmd} -> ${OUTDIR}/${cmd} (${GOOS}/${GOARCH})"
    CGO_ENABLED="${CGO_ENABLED:-0}" GOOS="$GOOS" GOARCH="$GOARCH" \
        go build \
        -ldflags "-X github.com/mschulkind-oss/yolo-jail/internal/version.buildVersion=${VERSION} -X github.com/mschulkind-oss/yolo-jail/internal/version.GitCommit=${COMMIT}" \
        -o "${OUTDIR}/${cmd}" "./cmd/${cmd}"
done

echo "build-go: done -> ${OUTDIR}"

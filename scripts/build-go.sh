#!/usr/bin/env bash
# Build every cmd/ binary into dist-go/<goos>-<goarch>/ (go-port plan §3).
#
# dist-go/ (NOT dist/ — `just build` does `rm -rf dist/`) is the transition-era
# binary staging channel.  Because the workspace is live-mounted, the in-jail
# agent rebuilds host-consumable Linux binaries here and the host sees them
# immediately — artifact staleness during multi-week soaks is fixed by
# rebuilding, not by human action.
#
# Default: build for the host's own GOOS/GOARCH (native).  Pass GOOS/GOARCH in
# the environment to cross-compile (e.g. the Nix derivation builds
# CGO_ENABLED=0 GOOS=linux for both amd64 and arm64).
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

# Version + commit stamp for -ldflags -X (see internal/version).  Best effort:
# git may be unavailable in some build contexts — stamp EMPTY then (never the
# literal "unknown": a stamp is authoritative (D18), and an "unknown" stamp
# would shadow the binary's live-describe fallback).
VERSION="$(git describe --tags --dirty --always 2>/dev/null || true)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || true)"

for cmd in "${CMDS[@]}"; do
    echo "build-go: ${cmd} -> ${OUTDIR}/${cmd} (${GOOS}/${GOARCH})"
    CGO_ENABLED="${CGO_ENABLED:-0}" GOOS="$GOOS" GOARCH="$GOARCH" \
        go build \
        -ldflags "-X github.com/mschulkind-oss/yolo-jail/internal/version.buildVersion=${VERSION} -X github.com/mschulkind-oss/yolo-jail/internal/version.GitCommit=${COMMIT}" \
        -o "${OUTDIR}/${cmd}" "./cmd/${cmd}"
done

echo "build-go: done -> ${OUTDIR}"

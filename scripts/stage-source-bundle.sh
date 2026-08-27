#!/usr/bin/env bash
# Stage the "two files and a binary" bundle that ships beside an installed
# `yolo` so a checkout-less install (Homebrew bottle, release archive) can build
# the jail image without a Go toolchain or the source tree.
#
# `yolo -- <cmd>` runs `nix build .#ociImage` against a flake. An installed
# binary has no checkout, so reporoot.Resolve (internal/reporoot) finds a bundle
# at <exe>/../share/yolo-jail or <exe>/share/yolo-jail and builds THAT flake.
# This script produces that bundle.
#
# THE BUNDLE IS PREBUILT, NOT SOURCE. It contains exactly:
#   flake.nix
#   flake.lock
#   bin/linux-amd64/{yolo,yolo-entrypoint,yolo-jaild,yolo-ps}
#   bin/linux-arm64/{yolo,yolo-entrypoint,yolo-jaild,yolo-ps}
#
# When the flake evaluates from this bundle (a `path:` flake), it hits its own
# prebuilt short-circuit — `builtins.pathExists ./bin/linux-<arch>` in
# flake.nix:goBinaries — and copies the prebuilt binaries in instead of
# compiling. goSrc (go.mod/vendor/cmd/internal/…) is never forced, so it need
# not ship. This is why the bundle is "two files and a binary" and not the
# ~11 MB tracked tree the old git-archive bundle shipped. See
# docs/research/repo-root-and-distribution.md.
#
# goprobe is EXCLUDED — it is a dev-only deployment tripwire that must never
# reach a runtime PATH. The shippable set is the same four binaries the image
# bakes (flake.nix:shippedBinaries).
#
# Cross-compiling needs a Go toolchain (build-go.sh). The SHIPPED bundle is
# arch-agnostic on purpose: one bundle serves amd64 and arm64 hosts, and the
# flake picks the matching bin/linux-<arch> at eval time — so the release path
# (goreleaser, the brew formula) builds BOTH arches (the default).
#
# A LOCAL `just install`, though, only ever runs on the one host, so building
# the foreign arch is pure waste — a cold cross-compile of the entire module
# graph, which on an amd64 host recompiles the world for arm64 (and vice versa).
# Pass YOLO_BUNDLE_ARCHES to narrow it; `just install` sets the native arch, so
# the build reuses the warm cache from the `go install` that just ran.
#
# Usage: scripts/stage-source-bundle.sh <dest-dir>
#   e.g. scripts/stage-source-bundle.sh bundle/share/yolo-jail
#   YOLO_BUNDLE_ARCHES=amd64 scripts/stage-source-bundle.sh <dest-dir>
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DEST="${1:-}"
if [ -z "$DEST" ]; then
  echo "usage: $0 <dest-dir>" >&2
  exit 2
fi

# flake.nix / flake.lock must exist, or the bundle is useless.
for f in flake.nix flake.lock; do
  if [ ! -f "$REPO_ROOT/$f" ]; then
    echo "stage-source-bundle: $f missing; aborting" >&2
    exit 1
  fi
done

# SAFETY GUARD (learned the hard way). This script `rm -rf`s DEST to stage a
# fresh bundle. A caller that computes DEST wrong can therefore aim `rm -rf` at
# something precious — a first cut derived DEST from $GOBIN and hit
# ~/.local/share/yolo-jail (yolo's entire state dir). So REFUSE to clobber a
# non-empty directory unless it already looks like a bundle we produced
# (a flake.nix at its root). An empty or absent DEST is fine; a populated dir
# that is NOT a bundle aborts loudly rather than deleting the user's data.
if [ -e "$DEST" ]; then
  if [ ! -d "$DEST" ]; then
    echo "stage-source-bundle: refusing — DEST exists and is not a directory: $DEST" >&2
    exit 1
  fi
  if [ -n "$(ls -A "$DEST" 2>/dev/null)" ] && [ ! -f "$DEST/flake.nix" ]; then
    echo "stage-source-bundle: refusing to rm -rf a non-empty dir that is not a bundle:" >&2
    echo "  $DEST" >&2
    echo "  (a bundle has flake.nix at its root; this dir does not — aborting to" >&2
    echo "   avoid deleting something that is not ours). Pass an empty/new dir." >&2
    exit 1
  fi
fi

# The image bakes exactly these (flake.nix:shippedBinaries). goprobe is
# intentionally absent.
#
# THIS LIST MUST TRACK flake.nix's. It is not a convenience copy: a bundle
# staged here IS the source flake.nix's prebuilt short-circuit input, and that
# loop does `[ -e "$src" ] || continue` — so a binary missing HERE is silently
# skipped THERE and vanishes from any image built from a shipped bundle, while
# an image built from a source checkout still has it. The two lists are pinned
# together by internal/entrypoint/shippedclients_test.go.
SHIPPED_BINARIES=(yolo yolo-entrypoint yolo-jaild yolo-ps yolo-cglimit yolo-journalctl yolo-serial)

# Arches to build. Default is BOTH (the arch-agnostic shipped bundle); a local
# install narrows this to the native arch via YOLO_BUNDLE_ARCHES to avoid a cold
# cross-compile. Space-separated list.
if [ -n "${YOLO_BUNDLE_ARCHES:-}" ]; then
  # shellcheck disable=SC2206
  ARCHES=(${YOLO_BUNDLE_ARCHES})
else
  ARCHES=(amd64 arm64)
fi

rm -rf "$DEST"
mkdir -p "$DEST"

cp "$REPO_ROOT/flake.nix" "$DEST/flake.nix"
cp "$REPO_ROOT/flake.lock" "$DEST/flake.lock"

# Cross-compile each Linux arch into dist-go/linux-<arch>/, then copy just the
# ship set into the bundle. build-go.sh builds every cmd/ (goprobe included);
# we copy only SHIPPED_BINARIES, dropping goprobe.
for arch in "${ARCHES[@]}"; do
  echo "stage-source-bundle: cross-compiling linux/${arch}"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$REPO_ROOT/scripts/build-go.sh"

  src_dir="$REPO_ROOT/dist-go/linux-${arch}"
  dst_dir="$DEST/bin/linux-${arch}"
  mkdir -p "$dst_dir"
  for name in "${SHIPPED_BINARIES[@]}"; do
    if [ ! -f "$src_dir/$name" ]; then
      echo "stage-source-bundle: build-go.sh did not produce $src_dir/$name" >&2
      exit 1
    fi
    cp "$src_dir/$name" "$dst_dir/$name"
    chmod +x "$dst_dir/$name"
  done
done

# Sanity: every required member is present, and goprobe leaked into neither arch.
missing=0
for p in flake.nix flake.lock; do
  [ -e "$DEST/$p" ] || { echo "stage-source-bundle: missing $p" >&2; missing=1; }
done
for arch in "${ARCHES[@]}"; do
  for name in "${SHIPPED_BINARIES[@]}"; do
    [ -e "$DEST/bin/linux-${arch}/$name" ] || {
      echo "stage-source-bundle: missing bin/linux-${arch}/$name" >&2; missing=1; }
  done
  if [ -e "$DEST/bin/linux-${arch}/goprobe" ]; then
    echo "stage-source-bundle: goprobe leaked into bin/linux-${arch} — ship set is ${SHIPPED_BINARIES[*]}" >&2
    missing=1
  fi
done
[ "$missing" -eq 0 ] || exit 1

echo "stage-source-bundle: staged $(du -sh "$DEST" | cut -f1) prebuilt bundle at $DEST"

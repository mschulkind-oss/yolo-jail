#!/usr/bin/env bash
# Stage the source bundle that ships beside an installed `yolo` so a
# checkout-less install (Homebrew, release archive) can build the jail image.
#
# `yolo -- <cmd>` runs `nix build .#ociImage` against a repo checkout; an
# installed binary has none, so it looks for a bundle at
# <exe>/../share/yolo-jail/ (internal/cli/run/probes.go bundledSourceDir) and
# stages it into the nix build root. This script produces that bundle. It is the
# Go-era analog of the Python wheel's bundled package data — see
# docs/research/repo-root-and-distribution.md.
#
# The bundle must contain the flake's goSrc fileset (flake.nix:goSrc): flake.nix,
# flake.lock, go.mod, go.sum, vendor/, cmd/, internal/, bundled_loopholes/. We
# produce it with `git archive HEAD` — the ENTIRE tracked tree (~11MB, vendor/
# dominates), a clean SUPERSET of the fileset, so build artifacts and untracked
# files never leak in. (The Homebrew formula, a source build, installs only the
# 8 goSrc members; the release archive ships the whole tree. Both satisfy
# `nix build .#ociImage`, which reads only the fileset — the superset is
# harmless.) We assert the required members are present below.
#
# Usage: scripts/stage-source-bundle.sh <dest-dir>
#   e.g. scripts/stage-source-bundle.sh dist/share/yolo-jail
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DEST="${1:-}"
if [ -z "$DEST" ]; then
  echo "usage: $0 <dest-dir>" >&2
  exit 2
fi

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "stage-source-bundle: not a git checkout — cannot produce the bundle" >&2
  exit 1
fi

# flake.nix must be tracked, or the bundle is useless.
if ! git ls-files --error-unmatch flake.nix >/dev/null 2>&1; then
  echo "stage-source-bundle: flake.nix is not tracked; aborting" >&2
  exit 1
fi

rm -rf "$DEST"
mkdir -p "$DEST"

# Archive the tracked tree straight into DEST. `git archive` respects
# export-ignore attrs; there are none, so this is the full tracked tree.
git archive --format=tar HEAD | tar -x -C "$DEST"

# Sanity: every goSrc fileset member the flake needs must be present.
missing=0
for p in flake.nix flake.lock go.mod go.sum vendor cmd internal bundled_loopholes; do
  if [ ! -e "$DEST/$p" ]; then
    echo "stage-source-bundle: bundle missing required path: $p" >&2
    missing=1
  fi
done
[ "$missing" -eq 0 ] || exit 1

echo "stage-source-bundle: staged $(du -sh "$DEST" | cut -f1) bundle at $DEST"

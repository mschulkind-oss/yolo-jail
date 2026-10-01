#!/usr/bin/env bash
# Build the published user guide into dist/docs.
# Cloudflare Workers Builds runs THIS path: build command `bash scripts/build-site.sh`,
# deploy command `npx wrangler deploy --config docs-wrangler.toml`. Both commands
# live in Cloudflare's dashboard, not in this repo. Renaming this script without
# changing the dashboard leaves every build red while the old site keeps serving.
set -euo pipefail

cd "$(dirname "$0")/.."
# Vantage is the latest vantage-md release on every build (maintainer, 2026-10-01).

# The static export includes git history. A shallow CI clone would silently
# truncate it to the tip commit, so fetch the rest before building.
if [ -f .git/shallow ]; then
  git fetch --unshallow
fi

rm -rf dist/docs
if command -v uvx >/dev/null 2>&1; then
  uvx --from vantage-md@latest vantage build userguide/ -o dist/docs -n "YOLO Jail User Guide"
else
  # Workers Builds images are not guaranteed to carry uv. Python and pip are
  # sufficient; keep the install outside the published dist/docs directory.
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  python3 -m venv "$tmp/venv"
  "$tmp/venv/bin/pip" install --upgrade vantage-md
  "$tmp/venv/bin/vantage" build userguide/ -o dist/docs -n "YOLO Jail User Guide"
fi

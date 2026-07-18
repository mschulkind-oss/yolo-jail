#!/usr/bin/env bash
# Enable the Go front door for the go-port A/B transition — in BOTH places,
# still called `yolo` inside the jail so the jailed agent can't tell.
#
# How it works (no rename; `yolo` stays `yolo`):
#   * Host: the mise-installed `yolo` is the Python CLI, but its main() prelude
#     re-execs $YOLO_GO_BIN_DIR/yolo whenever YOLO_IMPL=go. So exporting the two
#     env vars this script prints makes plain `yolo` run Go on the host.
#   * Jail: `yolo run` (seam #11) forwards YOLO_IMPL=go + a jail-local
#     YOLO_GO_BIN_DIR into the container, so the in-jail ~/.yolo-shims/yolo shim
#     → Python main() → re-execs the Go binary. The agent types `yolo`, gets Go.
#   * Unported subcommands (prune/init/config-ref/broker/builder/macos-*)
#     transparently delegate back to Python in both places.
#
# Reversible: unset YOLO_IMPL → 100% Python everywhere. Nothing is renamed or
# replaced; this only sets env.
#
# Usage:
#   ./scripts/install-go-wrapper.sh          # print the export lines to eval
#   eval "$(./scripts/install-go-wrapper.sh)" # enable in the current shell
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"
BIN_DIR="${REPO_ROOT}/dist-go/${GOOS}-${GOARCH}"
GO_YOLO="${BIN_DIR}/yolo"

if [ ! -x "$GO_YOLO" ]; then
    echo "install-go: ${GO_YOLO} not found — run \`just build-go\` first." >&2
    exit 1
fi

# The venv python that can import src.cli (editable install) — handed to the Go
# binary as YOLO_PYTHON so its delegate-to-Python path works from any cwd on the
# host. (In-jail, seam #11 sets YOLO_PYTHON=python3 + PYTHONPATH itself.)
VENV_PY="$(env -u VIRTUAL_ENV uv run --project "$REPO_ROOT" python -c 'import sys; print(sys.executable)')"

# Emit shell export lines (eval this). Sending guidance to stderr keeps stdout
# a clean `eval` target.
cat >&2 <<EOF
Go front door enabled for this shell (eval the stdout to apply).
  host yolo  -> ${GO_YOLO}  (via YOLO_IMPL=go re-exec)
  jail yolo  -> Go too, still named 'yolo' (seam #11 forwards the gate)
  native now : run, check, doctor, ps   (rest delegate to Python)
  disable    : unset YOLO_IMPL YOLO_GO_BIN_DIR YOLO_PYTHON
EOF

cat <<EOF
export YOLO_IMPL=go
export YOLO_GO_BIN_DIR="${BIN_DIR}"
export YOLO_PYTHON="${VENV_PY}"
export YOLO_REPO_ROOT="${REPO_ROOT}"
EOF

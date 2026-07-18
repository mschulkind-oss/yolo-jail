"""Continuous drift suite (go-port plan §5.3) — the port's cross-session net.

Byte-diffs the Go drift dump (cmd/yolo-parity) against the live Python dump
(tools/parity/py_drift_dump.py). Runs in the FAST tier (`just check-ci`, the
pre-commit hook) on every commit, so any Python change to a pinned constant or
pure function without a matching Go change is a RED build here — not a
stage-N-later discovery.

Skips (does not fail) when the Go toolchain is unavailable, so the Python-only
dev path still works; CI always has Go. It builds the Go binary via
`just build-go` into dist-go/ and runs it — the same live-mount channel the
port uses everywhere.
"""

from __future__ import annotations

import platform
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parent.parent


def _goarch() -> str:
    machine = platform.machine()
    return {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        machine, machine
    )


def _goos() -> str:
    return "linux" if sys.platform.startswith("linux") else "darwin"


def _go_parity_binary() -> "Path | None":
    """Return the built yolo-parity binary, building dist-go/ if needed.

    Returns None when the Go toolchain isn't available (skip, don't fail).
    """
    binpath = REPO_ROOT / "dist-go" / f"{_goos()}-{_goarch()}" / "yolo-parity"
    if binpath.is_file():
        return binpath
    if shutil.which("go") is None:
        return None
    build = REPO_ROOT / "scripts" / "build-go.sh"
    try:
        subprocess.run(
            ["bash", str(build)], cwd=REPO_ROOT, check=True, capture_output=True
        )
    except (subprocess.CalledProcessError, OSError):
        return None
    return binpath if binpath.is_file() else None


def test_go_drift_dump_matches_python():
    """The Go and Python drift dumps must be byte-identical."""
    go_bin = _go_parity_binary()
    if go_bin is None:
        pytest.skip("Go toolchain unavailable — cannot run the drift suite")

    go_out = subprocess.run(
        [str(go_bin)], capture_output=True, text=True, check=True
    ).stdout
    py_out = subprocess.run(
        [sys.executable, str(REPO_ROOT / "tools" / "parity" / "py_drift_dump.py")],
        capture_output=True,
        text=True,
        check=True,
        cwd=REPO_ROOT,
    ).stdout

    if go_out != py_out:
        import difflib

        diff = "".join(
            difflib.unified_diff(
                py_out.splitlines(keepends=True),
                go_out.splitlines(keepends=True),
                fromfile="python (source of truth)",
                tofile="go (dist-go/yolo-parity)",
            )
        )
        pytest.fail(
            "Go drift dump diverged from Python. A pinned constant or pure "
            "function changed on one side but not the other. Update the Go "
            "port (internal/* + cmd/yolo-parity) to match, in the same "
            f"commit (freeze rule §1.9):\n\n{diff}"
        )

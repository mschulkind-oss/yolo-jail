#!/usr/bin/env python3
"""``just parity <suite>`` — run one parity suite against both implementations
and diff (go-port plan §5.2).

A *suite* produces two artifacts — one from the Python source of truth, one
from the Go port — and this driver diffs them:

* byte-exact for argv/files/templates/banners/errors;
* ANSI-stripped for rich terminal output;
* for wire-protocol JSON bodies, an order-preserving decode compared as key
  *sequences* + values (plain ``json.loads`` equality is the blindness that
  lets a reordered Go port pass while rewriting every byte — §5.2).

Suites are registered in ``SUITES`` below.  The first one, ``drift``, is the
continuous cross-session safety net: it byte-diffs ``cmd/yolo-parity``'s dump
of Go-side constants + pure-function outputs against the live Python values.
It runs on every commit inside ``just check-ci`` (via ``tests/``), so any
Python change without a matching Go change is a red build, not a stage-N-later
discovery.
"""

from __future__ import annotations

import argparse
import difflib
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent

_ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")


def strip_ansi(s: str) -> str:
    return _ANSI_RE.sub("", s)


def _unified(a: str, b: str, label_a: str, label_b: str) -> str:
    return "".join(
        difflib.unified_diff(
            a.splitlines(keepends=True),
            b.splitlines(keepends=True),
            fromfile=label_a,
            tofile=label_b,
        )
    )


def _go_binary(name: str) -> Path:
    """Locate a dist-go binary, building the whole channel if missing."""
    import platform

    goos = "linux" if sys.platform.startswith("linux") else "darwin"
    machine = platform.machine()
    goarch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        machine, machine
    )
    binpath = REPO_ROOT / "dist-go" / f"{goos}-{goarch}" / name
    if not binpath.is_file():
        subprocess.run(["just", "build-go"], cwd=REPO_ROOT, check=True)
    return binpath


def suite_drift() -> int:
    """Diff the Go drift dump against the live Python dump."""
    go_bin = _go_binary("yolo-parity")
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
    if go_out == py_out:
        print("drift: OK (Go dump byte-identical to Python)")
        return 0
    print("drift: MISMATCH", file=sys.stderr)
    print(_unified(py_out, go_out, "python", "go"), file=sys.stderr)
    return 1


SUITES = {
    "drift": suite_drift,
}


def main() -> int:
    parser = argparse.ArgumentParser(description="Run a go-port parity suite.")
    parser.add_argument("suite", choices=sorted(SUITES), help="suite name")
    args = parser.parse_args()
    return SUITES[args.suite]()


if __name__ == "__main__":
    raise SystemExit(main())

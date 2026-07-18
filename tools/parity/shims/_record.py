#!/usr/bin/env python3
"""Shared body for the parity PATH shims (podman/container/tmux/kitten/ps).

A shim is a tiny executable whose argv[0] basename is the tool it stands in
for.  It is symlinked (or hard-copied) under a name like ``podman`` and put
first on ``$PATH`` so the real CLI's spawn sites hit it instead of a live
runtime.  Two jobs, selected by env:

* **Record** — append one JSON line ``{"tool": ..., "argv": [...], "env": {...}}``
  to ``$YOLO_PARITY_CAPTURE``.  This is the Go-compatible replacement for the
  Python-only ``@patch`` argv assertions: both implementations are captured
  the same way and their capture files are byte-diffed.

* **Replay** — if ``$YOLO_PARITY_REPLAY_DIR/<tool>.stdout`` exists, write it to
  stdout (and ``<tool>.exit`` sets the exit code).  Lets a suite feed canned
  ``ps``/``podman ps`` output whose live form has volatile fields (etime,
  %cpu) that would make a naive byte gate flaky.

The shim is intentionally stdlib-only and side-effect-minimal so it perturbs
the captured environment as little as possible.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    tool = Path(sys.argv[0]).name

    capture = os.environ.get("YOLO_PARITY_CAPTURE")
    if capture:
        # Only forward a stable subset of the environment: the full env is
        # noisy (PID-specific, tmpdir-specific) and would defeat a byte diff.
        keep_prefixes = ("YOLO_",)
        env = {
            k: v
            for k, v in os.environ.items()
            if k.startswith(keep_prefixes) and k != "YOLO_PARITY_CAPTURE"
        }
        line = json.dumps(
            {"tool": tool, "argv": sys.argv[1:], "env": env},
            sort_keys=True,
        )
        # Append atomically enough for our single-writer-per-line use.
        with open(capture, "a", encoding="utf-8") as fh:
            fh.write(line + "\n")

    replay_dir = os.environ.get("YOLO_PARITY_REPLAY_DIR")
    if replay_dir:
        base = Path(replay_dir) / tool
        out = base.with_suffix(".stdout")
        if out.is_file():
            sys.stdout.buffer.write(out.read_bytes())
            sys.stdout.buffer.flush()
        err = base.with_suffix(".stderr")
        if err.is_file():
            sys.stderr.buffer.write(err.read_bytes())
            sys.stderr.buffer.flush()
        exit_file = base.with_suffix(".exit")
        if exit_file.is_file():
            try:
                return int(exit_file.read_text().strip())
            except ValueError:
                return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())

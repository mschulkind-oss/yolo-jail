#!/usr/bin/env python3
"""Materialize the parity PATH shims into a directory.

Usage: ``python tools/parity/install_shims.py <dir> [tool ...]``

Creates one executable per requested tool (default: the full set) in ``<dir>``,
each a thin wrapper that execs ``_record.py`` with ``argv[0]`` set to the tool
name.  Put ``<dir>`` first on ``$PATH`` and set ``YOLO_PARITY_CAPTURE`` /
``YOLO_PARITY_REPLAY_DIR`` per ``_record.py``.

A wrapper (rather than a symlink) is used so ``argv[0]``'s basename is exactly
the tool name regardless of how the caller resolves it, and so it works on
filesystems without symlink support.
"""

from __future__ import annotations

import stat
import sys
from pathlib import Path

DEFAULT_TOOLS = ("podman", "container", "tmux", "kitten", "ps")


def install(dest: Path, tools: "list[str]") -> None:
    dest.mkdir(parents=True, exist_ok=True)
    record = (Path(__file__).parent / "shims" / "_record.py").resolve()
    py = sys.executable
    for tool in tools:
        wrapper = dest / tool
        # The shim reads the tool name from YOLO_PARITY_TOOL (not sys.argv[0],
        # which Python sets to the script path regardless of exec -a).
        wrapper.write_text(
            f'#!/usr/bin/env bash\nexec env YOLO_PARITY_TOOL="{tool}" "{py}" "{record}" "$@"\n'
        )
        wrapper.chmod(
            wrapper.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH
        )


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: install_shims.py <dir> [tool ...]", file=sys.stderr)
        return 2
    dest = Path(sys.argv[1])
    tools = sys.argv[2:] or list(DEFAULT_TOOLS)
    install(dest, tools)
    print(f"installed {len(tools)} shim(s) in {dest}: {', '.join(tools)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

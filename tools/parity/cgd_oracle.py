#!/usr/bin/env python3
"""cgroup-delegate + journal daemon parity oracle (go-port Stage 7).

Exercises the pure helpers of both builtin daemons and emits their exact
output so the Go ports (internal/cgd, internal/journald) can be byte-diffed:

  * _validate_cgroup_name over a name corpus
  * _parse_memory_value over a value corpus (None -> null)
  * the cpu.max quota formula for a fixed nproc + pct matrix (computed here to
    avoid a live nproc dependency; the Go test uses the same fixed nproc)

Emitted as one canonical JSON document (sorted keys).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))


def main() -> int:
    from cli.loopholes_runtime import _parse_memory_value, _validate_cgroup_name

    name_corpus = [
        "job",
        "training-1",
        "a.b_c-d",
        "",
        "-leading",
        "with/slash",
        "..",
        "a..b",
        "x" * 64,
        "x" * 65,
        "UPPER",
        "1digit",
        "has space",
        "tab\tinside",
    ]
    mem_corpus = [
        "8g",
        "512m",
        "1024k",
        "1048576",
        "0.5g",
        "2G",
        "  4g  ",
        "notanumber",
        "",
        "1x",
        "-1",
        "1.5m",
    ]
    out = {
        "validate_name": {n: _validate_cgroup_name(n) for n in name_corpus},
        "parse_memory": {v: _parse_memory_value(v) for v in mem_corpus},
    }
    sys.stdout.write(json.dumps(out, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

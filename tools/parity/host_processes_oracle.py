#!/usr/bin/env python3
"""host-processes config-load oracle (go-port Stage 5).

Reads a JSON array of jsonc config STRINGS from stdin, runs each through
src/host_processes._load_config, and emits the resulting {visible, fields} so
the Go LoadConfig can be byte-diffed. This pins the config-load parity (the
str-filtering + `or DEFAULT_FIELDS` semantics) independent of the socket layer.
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))


def main() -> int:
    from host_processes import _load_config

    configs = json.loads(sys.stdin.read())
    out = []
    for content in configs:
        f = tempfile.NamedTemporaryFile("w", suffix=".jsonc", delete=False)
        try:
            f.write(content)
            f.close()
            cfg = _load_config(Path(f.name))
            out.append({"visible": cfg["visible"], "fields": cfg["fields"]})
        finally:
            Path(f.name).unlink(missing_ok=True)
    sys.stdout.write(json.dumps(out, ensure_ascii=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

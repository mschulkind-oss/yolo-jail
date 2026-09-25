#!/usr/bin/env python3
"""Ensure the site builder writes the directory Wrangler serves."""
from pathlib import Path
import re
import sys
import tomllib


def check(build_script: Path, wrangler: Path) -> str | None:
    script = build_script.read_text()
    # The two install paths must invoke the same Vantage output target.
    outputs = re.findall(r'\bbuild\s+userguide/\s+-o\s+([^\s"\']+)', script)
    target = tomllib.loads(wrangler.read_text())["assets"]["directory"]
    if not outputs or any(output != target for output in outputs):
        return f"Vantage build outputs {outputs!r} disagree with Wrangler assets directory {target!r}"
    return None


if __name__ == "__main__":
    problem = check(Path(sys.argv[1] if len(sys.argv) > 1 else "scripts/build-site.sh"),
                    Path(sys.argv[2] if len(sys.argv) > 2 else "docs-wrangler.toml"))
    if problem:
        sys.exit(problem)

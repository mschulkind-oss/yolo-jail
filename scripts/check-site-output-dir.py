#!/usr/bin/env python3
"""Ensure the site builder writes the directory Wrangler serves, and that the Worker reads only
bindings its Wrangler config declares."""
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


def check_bindings(worker: Path, wrangler: Path) -> str | None:
    # Every env.NAME the Worker reads must be declared: an undeclared one is undefined at run time,
    # so the Worker throws on each request that reaches it instead of serving the page.
    used = sorted(set(re.findall(r'\benv\.([A-Za-z_][A-Za-z0-9_]*)', worker.read_text())))
    declared = {tomllib.loads(wrangler.read_text()).get("assets", {}).get("binding")}
    missing = [name for name in used if name not in declared]
    if missing:
        return f"{worker} reads env.{', env.'.join(missing)}, which {wrangler} declares as no binding"
    return None


if __name__ == "__main__":
    problem = check(Path(sys.argv[1] if len(sys.argv) > 1 else "scripts/build-site.sh"),
                    Path(sys.argv[2] if len(sys.argv) > 2 else "docs-wrangler.toml"))
    problem = problem or check_bindings(Path("docs-worker.js"), Path(sys.argv[2] if len(sys.argv) > 2 else "docs-wrangler.toml"))
    if problem:
        sys.exit(problem)

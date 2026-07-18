"""Live-Python oracle for the Go `internal/prunecmd` parity test.

Runs the REAL ``src.cli.prune_cmd.prune_cmd`` body with every side-effecting
seam injected from a JSON payload (argv1), capturing its rich-console output to
stdout so the Go test can diff the ANSI-stripped text against the native port.

Payload keys (all paths absolute):
  gs, home, cache   — GLOBAL_STORAGE / GLOBAL_HOME / GLOBAL_CACHE overrides
  runtime           — value _detect_runtime returns (default "podman")
  mapping           — {"<argv joined by \\x00>": stdout} for subprocess.run
  live              — list of live yolo-* container names (for the relay sweep)
  flags             — prune_cmd option overrides (apply, keep_images, …)

The subprocess stub returns returncode 0 with the mapped stdout ("" when
unmapped), matching a benign empty listing. Missing-runtime / non-zero-RC
degrades are exercised by the Go-side unit tests, not this differential.

Invoked ONLY by internal/prunecmd's TestParityVsLivePython (which SKIPs when
python is absent). Not a pytest module — no test_ functions.
"""

from __future__ import annotations

import importlib
import io
import json
import sys
from pathlib import Path
from unittest.mock import MagicMock


def main() -> None:
    payload = json.loads(sys.argv[1])
    sys.path.insert(0, "src")

    import rich.console as rc

    cap = io.StringIO()
    console = rc.Console(
        file=cap,
        force_terminal=False,
        no_color=True,
        width=10000,
        highlight=False,
        emoji=False,
        markup=True,
    )

    pc = importlib.import_module("src.cli.prune_cmd")
    cmod = importlib.import_module("src.cli.console")
    cmod.console = console
    pc.console = console
    pc.GLOBAL_STORAGE = Path(payload["gs"])
    pc.GLOBAL_HOME = Path(payload["home"])
    pc.GLOBAL_CACHE = Path(payload["cache"])
    rt = payload.get("runtime", "podman")
    pc._detect_runtime = lambda: rt

    prune = importlib.import_module("src.prune")
    mapping = {tuple(k.split("\x00")): v for k, v in payload.get("mapping", {}).items()}

    def runner(cmd, **kw):
        return MagicMock(returncode=0, stdout=mapping.get(tuple(cmd), ""), stderr="")

    prune.subprocess.run = runner

    # The relay sweep is exercised via its own Go unit test; stub live-set +
    # reap so the differential focuses on prune_cmd's own section flow and the
    # engine reclaim numbers (the relay reap uses a real /tmp scan we don't want
    # to depend on here).
    live = set(payload.get("live", []))
    run_cmd = importlib.import_module("src.cli.run_cmd")
    lr = importlib.import_module("src.cli.loopholes_runtime")
    run_cmd._live_yolo_containers = lambda rt: live
    lr._relay_reap_orphans = lambda live_cnames, **kw: []

    flags = payload.get("flags", {})
    pc.prune_cmd(
        apply=flags.get("apply", False),
        no_hardlink=flags.get("no_hardlink", False),
        dedup_global=flags.get("dedup_global", False),
        no_containers=flags.get("no_containers", False),
        no_images=flags.get("no_images", False),
        keep_images=flags.get("keep_images", 2),
        no_image_cache=flags.get("no_image_cache", False),
        no_build_roots=flags.get("no_build_roots", False),
        no_shadowed_home=flags.get("no_shadowed_home", False),
        image_cache_keep=flags.get("image_cache_keep", 3),
        cache_age=flags.get("cache_age", 30),
        purge_heavy_caches=flags.get("purge_heavy_caches", False),
    )
    sys.stdout.write(cap.getvalue())


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Ordered-argv oracle for internal/runcmd (go-port plan Stage 16, sub-phase 3).

Drives the LIVE src/cli/run_cmd.py:run() over a controlled fixture and captures
the ordered container argv it hands to run_with_proxy — the byte-exact sequence
the Go port's assembleRunCmd must reproduce (the plan's ordered-argv golden
gate: flags-before-image, -e block, mount order).

The launch is made HERMETIC so the Go differential test can match without host
coupling:
  * all side effects stubbed (image load, proxy, loopholes, tracking, banners),
  * repo root pinned to /repo, git-describe pinned to a fixed version,
  * bundled-loophole discovery emptied (no --add-host / CA args),
  * in_container forced False, host nix skipped, TERM/TZ dropped,
  * find_running/find_existing → None (fresh-launch path).

Usage: run_argv_oracle.py <home> <workspace> [network]
The workspace must already contain yolo-jail.jsonc. `network` defaults to
"bridge". Prints the argv as a JSON array on stdout (the final internal command
tail is dropped — the Go argv golden compares through image + "yolo-entrypoint").
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))


def main() -> int:
    home, workspace = sys.argv[1], sys.argv[2]
    network = sys.argv[3] if len(sys.argv) > 3 else "bridge"
    os.environ["HOME"] = home
    for k in (
        "YOLO_RUNTIME",
        "YOLO_VERSION",
        "YOLO_ENTRYPOINT_IMPL",
        "TZ",
        "TERM",
        "YOLO_NIX_HOST_DAEMON",
    ):
        os.environ.pop(k, None)
    os.environ["YOLO_REPO_ROOT"] = "/repo-none"

    import cli.run_cmd as rc

    # Route rich console chatter to stderr so stdout carries ONLY the argv JSON
    # (warnings like "kvm not present" must not pollute the parsed output).
    from rich.console import Console as _Console

    rc.console = _Console(stderr=True)

    captured: dict = {}

    def fake_proxy(run_cmd, on_started=None, on_terminate=None):
        captured["argv"] = list(run_cmd)
        raise SystemExit(0)

    rc.run_with_proxy = fake_proxy
    rc.auto_load_image = lambda *a, **k: True
    rc.start_loopholes = lambda *a, **k: []
    rc.stop_loopholes = lambda *a, **k: None
    rc._broker_ensure = lambda *a, **k: None
    rc._ensure_broker_relay = lambda *a, **k: None
    rc._relay_reap_orphans = lambda *a, **k: None
    rc.write_container_tracking = lambda *a, **k: None
    rc._write_owner_pid = lambda *a, **k: None
    rc._clear_owner_pid = lambda *a, **k: None
    rc._tmux_rename_window = lambda *a, **k: None
    rc._print_startup_banner = lambda *a, **k: None
    rc.find_running_container = lambda *a, **k: None
    rc.find_existing_container = lambda *a, **k: None
    rc._remove_stale_container = lambda *a, **k: None
    rc._reap_orphaned_jails = lambda *a, **k: None
    rc.start_host_port_forwarding = lambda *a, **k: []
    rc.cleanup_port_forwarding = lambda *a, **k: None
    rc._retire_jail_made_venv = lambda *a, **k: None
    rc._resolve_repo_root = lambda: Path("/repo")
    rc._check_config_changes = lambda *a, **k: True
    rc._live_yolo_containers = lambda *a, **k: None  # unknown → no prune env
    rc._git_describe_version = lambda: "9.9.9-test"
    rc._get_yolo_version = lambda: "9.9.9-test"
    rc._detect_host_timezone = lambda: None
    # Hermetic host state.
    rc.IS_MACOS = False
    rc.IS_LINUX = True
    # Force the non-nested, no-nix, bridge path.
    import pathlib

    real_exists = pathlib.Path.exists

    def fake_exists(self):
        s = str(self)
        if s in (
            "/run/.containerenv",
            "/.dockerenv",
            "/nix/var/nix/daemon-socket",
            "/nix/store",
            "/dev/net/tun",
            "/dev/kvm",
            "/dev/kfd",
        ):
            return False
        return real_exists(self)

    pathlib.Path.exists = fake_exists

    # Empty loophole discovery (no bundled --add-host / CA args).
    rc._loopholes.discover_loopholes = lambda *a, **k: []
    rc._loopholes.runtime_args_for = lambda *a, **k: []

    os.chdir(workspace)

    class Ctx:
        args = []

    try:
        rc.run(Ctx(), network=network, new=False, profile=False, dry_run=False)
    except SystemExit:
        pass
    finally:
        pathlib.Path.exists = real_exists

    argv = captured.get("argv", [])
    # Drop the final internal command (last element after "yolo-entrypoint").
    if len(argv) >= 2 and argv[-2] == "yolo-entrypoint":
        argv = argv[:-1]
    print(json.dumps(argv))
    return 0


if __name__ == "__main__":
    sys.exit(main())

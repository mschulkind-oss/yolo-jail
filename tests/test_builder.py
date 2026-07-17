"""Tests for the on-demand macOS Linux builder (src/cli/builder.py).

Exercises the Linux-verifiable core: reachability, setup-state probing, the
ensure-orchestration (start + poll), status, and the pure content generators.
The privileged apply (writing /etc/nix, launchctl) is macOS-only and verified
on a Mac — here we only assert the COMMANDS it constructs.
"""

from __future__ import annotations

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

import cli.builder as b  # noqa: E402


# ── content generators (pure) ───────────────────────────────────────────────


def test_ssh_config_block_has_required_fields():
    block = b.ssh_config_block()
    assert f"Host {b.BUILDER_SSH_HOST}" in block
    assert f"Port {b.BUILDER_PORT}" in block
    assert f"User {b.BUILDER_USER}" in block
    assert f"IdentityFile {b.BUILDER_KEY_PATH}" in block


def test_nix_builders_line_names_aarch64_and_substitutes():
    line = b.nix_builders_line(max_jobs=8)
    assert "ssh-ng://builder@linux-builder" in line
    assert "aarch64-linux" in line
    assert " 8 " in line  # max_jobs interpolated
    assert "builders-use-substitutes = true" in line


def test_trusted_users_line_merges_preserving_existing():
    # Adds `me`, keeps root + any existing entries, dedups, root first.
    line = b.trusted_users_line(["root", "alice"], "matt")
    assert line == "trusted-users = root alice matt"


def test_trusted_users_line_none_when_already_trusted():
    assert b.trusted_users_line(["root", "matt"], "matt") is None


def test_trusted_users_line_none_when_admin_group_covers():
    # @admin / @wheel group membership already covers the user.
    assert b.trusted_users_line(["root", "@admin"], "matt") is None
    assert b.trusted_users_line(["@wheel"], "matt") is None


def test_setup_root_script_batches_all_steps_and_guards():
    script = b.setup_root_script(4, "matt", ["root"], Path("/etc/nix/nix.conf"))
    # one script, all privileged steps present
    assert "set -euo pipefail" in script
    assert "ssh-ng://builder@linux-builder" in script  # builders line
    assert "grep -qs" in script  # idempotency guard on the builders line
    assert "trusted-users = root matt" in script  # merged trust
    assert "100-linux-builder.conf" in script  # ssh config
    assert "launchctl kickstart -k system/" in script  # apply (daemon restart)


def test_setup_root_script_omits_trusted_users_when_already_trusted():
    script = b.setup_root_script(
        4, "matt", ["root", "@admin"], Path("/etc/nix/nix.conf")
    )
    assert "trusted-users" not in script  # nothing to change → not written


def test_run_setup_pipes_script_to_single_sudo(monkeypatch):
    from types import SimpleNamespace

    captured = {}

    def fake_run(cmd, input=None, text=None, timeout=None):
        captured["cmd"] = cmd
        captured["input"] = input
        return SimpleNamespace(returncode=0)

    monkeypatch.setattr(b, "_current_trusted_users", lambda: ["root"])
    monkeypatch.setattr(b, "_builder_conf_path", lambda: Path("/etc/nix/nix.conf"))
    ok, err = b.run_setup(4, "matt", _run=fake_run)
    assert ok is True and err is None
    # exactly one sudo, script on stdin (single password prompt)
    assert captured["cmd"] == ["sudo", "bash", "-s"]
    assert "ssh-ng://builder@linux-builder" in captured["input"]


def test_run_setup_reports_nonzero_exit(monkeypatch):
    from types import SimpleNamespace

    monkeypatch.setattr(b, "_current_trusted_users", lambda: ["root"])
    monkeypatch.setattr(b, "_builder_conf_path", lambda: Path("/etc/nix/nix.conf"))
    ok, err = b.run_setup(4, "matt", _run=lambda *a, **k: SimpleNamespace(returncode=3))
    assert ok is False and "exited 3" in err


# ── reachability ─────────────────────────────────────────────────────────────


def test_builder_reachable_true_when_socket_connects(monkeypatch):
    class _Conn:
        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

    monkeypatch.setattr(b.socket, "create_connection", lambda *a, **k: _Conn())
    assert b.builder_reachable() is True


def test_builder_reachable_false_on_oserror(monkeypatch):
    def boom(*a, **k):
        raise OSError("refused")

    monkeypatch.setattr(b.socket, "create_connection", boom)
    assert b.builder_reachable() is False


# ── setup-state probing ──────────────────────────────────────────────────────


def test_builder_setup_state_all_present(monkeypatch):
    monkeypatch.setattr(b.Path, "is_file", lambda self: True)
    monkeypatch.setattr(b, "_nix_conf_has_builder", lambda: True)
    st = b.builder_setup_state()
    assert st == {
        "ssh_config": True,
        "nix_builder": True,
        "key": True,
        "done": True,
    }


def test_builder_setup_state_incomplete_is_not_done(monkeypatch):
    # ssh config + key present, but nix.conf not wired → not done.
    monkeypatch.setattr(b.Path, "is_file", lambda self: True)
    monkeypatch.setattr(b, "_nix_conf_has_builder", lambda: False)
    st = b.builder_setup_state()
    assert st["done"] is False
    assert st["nix_builder"] is False


def test_builder_setup_state_done_without_key(monkeypatch):
    # The ssh KEY is installed only on the VM's first boot, so `done` must NOT
    # require it — otherwise setup could never register complete before a
    # build, and ensure_builder would never start the VM.  nix.conf +
    # ssh_config present, key absent → still done.
    def is_file(self):
        return str(self) != b.BUILDER_KEY_PATH  # key missing

    monkeypatch.setattr(b.Path, "is_file", is_file)
    monkeypatch.setattr(b, "_nix_conf_has_builder", lambda: True)
    st = b.builder_setup_state()
    assert st["key"] is False
    assert st["done"] is True  # gate is the daemon wiring, not the key


# ── ensure_builder orchestration ─────────────────────────────────────────────


def test_ensure_builder_not_macos(monkeypatch):
    monkeypatch.setattr(b, "IS_MACOS", False)
    ok, err = b.ensure_builder()
    assert ok is False
    assert err == "not macOS"


def test_ensure_builder_already_reachable_is_instant(monkeypatch):
    monkeypatch.setattr(b, "IS_MACOS", True)
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: True)
    # start must NOT be called if already reachable.
    monkeypatch.setattr(
        b, "start_builder", lambda: (_ for _ in ()).throw(AssertionError("started"))
    )
    ok, err = b.ensure_builder()
    assert ok is True and err is None


def test_ensure_builder_not_set_up_points_at_setup(monkeypatch):
    monkeypatch.setattr(b, "IS_MACOS", True)
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: False)
    monkeypatch.setattr(b, "builder_setup_state", lambda: {"done": False})
    ok, err = b.ensure_builder()
    assert ok is False
    assert err == "not set up"


def test_ensure_builder_starts_and_polls_to_ready(monkeypatch):
    monkeypatch.setattr(b, "IS_MACOS", True)
    monkeypatch.setattr(b, "builder_setup_state", lambda: {"done": True})
    # Not reachable at first; reachable after start (poll succeeds).
    calls = {"n": 0}

    def reachable(*a, **k):
        # first call (pre-start check) False; poll sees True
        calls["n"] += 1
        return calls["n"] > 1

    monkeypatch.setattr(b, "builder_reachable", reachable)
    monkeypatch.setattr(b, "start_builder", lambda: (True, None))
    progressed = []
    ok, err = b.ensure_builder(on_progress=progressed.append)
    assert ok is True and err is None
    assert progressed  # user saw a "starting…" message


def test_ensure_builder_start_failure_surfaces_error(monkeypatch):
    monkeypatch.setattr(b, "IS_MACOS", True)
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: False)
    monkeypatch.setattr(b, "builder_setup_state", lambda: {"done": True})
    monkeypatch.setattr(b, "start_builder", lambda: (False, "launchd boom"))
    ok, err = b.ensure_builder()
    assert ok is False
    assert "launchd boom" in err


def test_ensure_builder_times_out_if_never_reachable(monkeypatch):
    monkeypatch.setattr(b, "IS_MACOS", True)
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: False)
    monkeypatch.setattr(b, "builder_setup_state", lambda: {"done": True})
    monkeypatch.setattr(b, "start_builder", lambda: (True, None))
    # Drive the poll clock so it "times out" without real sleeping.
    monkeypatch.setattr(b, "_poll_until_reachable", lambda *a, **k: False)
    ok, err = b.ensure_builder()
    assert ok is False
    assert "did not become reachable" in err


# ── _poll_until_reachable with injected clock (no real waits) ─────────────────


def test_poll_returns_true_as_soon_as_reachable(monkeypatch):
    seq = iter([False, False, True])
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: next(seq))
    t = {"v": 0.0}

    def now():
        return t["v"]

    def sleep(dt):
        t["v"] += dt

    assert (
        b._poll_until_reachable(timeout_s=10, interval_s=1, _sleep=sleep, _now=now)
        is True
    )


def test_poll_times_out_when_never_reachable(monkeypatch):
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: False)
    t = {"v": 0.0}

    def now():
        return t["v"]

    def sleep(dt):
        t["v"] += dt

    assert (
        b._poll_until_reachable(timeout_s=3, interval_s=1, _sleep=sleep, _now=now)
        is False
    )


# ── start/stop: detached VM process + PID file ───────────────────────────────


def test_start_builder_spawns_detached_and_writes_pid(monkeypatch, tmp_path):
    # No existing builder; start must Popen `nix run …` detached and record a PID.
    monkeypatch.setattr(b, "BUILDER_PID_FILE", tmp_path / "linux-builder.pid")
    monkeypatch.setattr(b, "GLOBAL_STORAGE", tmp_path)
    monkeypatch.setattr(b, "_read_builder_pid", lambda: None)
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: False)
    captured = {}

    class FakeProc:
        pid = 4242

        def poll(self):
            return None  # still running

    def fake_popen(cmd, **kwargs):
        captured["cmd"] = cmd
        captured["kwargs"] = kwargs
        return FakeProc()

    ok, err = b.start_builder(_popen=fake_popen)
    assert ok is True and err is None
    assert captured["cmd"] == ["nix", "run", "nixpkgs#darwin.linux-builder"]
    assert captured["kwargs"]["start_new_session"] is True  # survives yolo exit
    assert (tmp_path / "linux-builder.pid").read_text().strip() == "4242"


def test_start_builder_noop_when_already_reachable(monkeypatch, tmp_path):
    monkeypatch.setattr(b, "BUILDER_PID_FILE", tmp_path / "pid")
    monkeypatch.setattr(b, "_read_builder_pid", lambda: None)
    monkeypatch.setattr(b, "builder_reachable", lambda *a, **k: True)

    def no_popen(*a, **k):
        raise AssertionError("must not spawn when already reachable")

    ok, err = b.start_builder(_popen=no_popen)
    assert ok is True and err is None


def test_stop_builder_terminates_process_group(monkeypatch, tmp_path):
    pid_file = tmp_path / "linux-builder.pid"
    pid_file.write_text("9001\n")
    monkeypatch.setattr(b, "BUILDER_PID_FILE", pid_file)
    monkeypatch.setattr(b, "_read_builder_pid", lambda: 9001)
    monkeypatch.setattr(b, "_pid_is_live", lambda pid: True)
    killed = {}
    monkeypatch.setattr(b.os, "getpgid", lambda pid: pid)
    monkeypatch.setattr(
        b.os, "killpg", lambda pgid, sig: killed.update(pgid=pgid, sig=sig)
    )
    ok, err = b.stop_builder()
    assert ok is True and err is None
    assert killed["pgid"] == 9001
    assert not pid_file.exists()  # PID file cleared


def test_stop_builder_noop_when_not_running(monkeypatch, tmp_path):
    monkeypatch.setattr(b, "BUILDER_PID_FILE", tmp_path / "pid")
    monkeypatch.setattr(b, "_read_builder_pid", lambda: None)
    ok, err = b.stop_builder()
    assert ok is True and err is None

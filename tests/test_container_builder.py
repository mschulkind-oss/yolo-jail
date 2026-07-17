"""Tests for src/cli/container_builder.py — the container Linux builder.

Pure argv/URI builders are asserted directly; the lifecycle (pull/run/wait/
stop, and the builder_session context manager) is driven with subprocess +
socket mocked. Fully Linux-runnable; a real-podman end-to-end proof lives in
the integration check (test_jail / manual), not here.
"""

from __future__ import annotations

import sys
from pathlib import Path
from types import SimpleNamespace

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

import cli.container_builder as cb  # noqa: E402


# ── pure builders ────────────────────────────────────────────────────────────


def test_pull_argv_per_runtime():
    assert cb.pull_argv("podman")[:2] == ["podman", "pull"]
    assert cb.pull_argv("container") == ["container", "image", "pull", cb.BUILDER_IMAGE]


def test_run_argv_podman_publishes_port():
    argv = cb.run_argv("podman", "ssh-ed25519 AAA k@y")
    assert argv[0] == "podman"
    assert "-d" in argv and "--rm" in argv
    # publishes guest 22 to a fixed host loopback port
    assert f"127.0.0.1:{cb.BUILDER_HOST_PORT}:22" in argv
    # pubkey passed at run time (keyless image)
    assert any(a.startswith("YOLO_BUILDER_PUBKEY=") for a in argv)


def test_run_argv_apple_container_no_publish():
    # AC has no -p; the container gets its own VM IP instead.
    argv = cb.run_argv("container", "ssh-ed25519 AAA k@y")
    assert argv[0] == "container"
    assert not any(a == "-p" for a in argv)
    assert any(a.startswith("YOLO_BUILDER_PUBKEY=") for a in argv)


def test_builder_uri_and_builders_line():
    uri = cb.builder_uri("127.0.0.1")
    assert uri.startswith("ssh-ng://root@127.0.0.1:")
    assert "ssh-key=" in uri
    line = cb.builders_line("192.168.64.2", port=22)
    assert line.startswith("ssh-ng://root@192.168.64.2:22 aarch64-linux ")
    assert line.endswith(" 4")  # default max_jobs


def test_nix_ssh_opts_disables_hostkey_pinning():
    opts = cb.nix_ssh_opts()
    assert "StrictHostKeyChecking=no" in opts
    assert "UserKnownHostsFile=/dev/null" in opts


# ── ensure_keypair ───────────────────────────────────────────────────────────


def test_ensure_keypair_reuses_existing(monkeypatch, tmp_path):
    key = tmp_path / "id_ed25519"
    key.write_text("PRIV")
    key.with_suffix(".pub").write_text("ssh-ed25519 AAAExisting k@y\n")
    monkeypatch.setattr(cb, "BUILDER_KEY", key)
    monkeypatch.setattr(cb, "BUILDER_KEY_DIR", tmp_path)

    def no_run(*a, **k):
        raise AssertionError("must not ssh-keygen when a key already exists")

    assert cb.ensure_keypair(_run=no_run) == "ssh-ed25519 AAAExisting k@y"


def test_ensure_keypair_generates_when_absent(monkeypatch, tmp_path):
    key = tmp_path / "id_ed25519"
    monkeypatch.setattr(cb, "BUILDER_KEY", key)
    monkeypatch.setattr(cb, "BUILDER_KEY_DIR", tmp_path)
    calls = []

    def fake_keygen(argv, **k):
        calls.append(argv)
        # simulate ssh-keygen writing the pair
        key.write_text("PRIV")
        key.with_suffix(".pub").write_text("ssh-ed25519 AAAGen k@y\n")
        return SimpleNamespace(returncode=0)

    pub = cb.ensure_keypair(_run=fake_keygen)
    assert pub == "ssh-ed25519 AAAGen k@y"
    assert calls and calls[0][0] == "ssh-keygen"


# ── reachable_address (runtime-specific host discovery) ──────────────────────


def test_reachable_address_podman_is_loopback():
    assert cb.reachable_address("podman") == ("127.0.0.1", cb.BUILDER_HOST_PORT)


def test_reachable_address_apple_container_reads_vm_ip(monkeypatch):
    table = (
        "ID                  IMAGE                     STATE    ADDR\n"
        f"{cb.BUILDER_CONTAINER}  yolo-jail-builder:latest  running  192.168.64.2/24\n"
    )
    monkeypatch.setattr(
        cb.subprocess,
        "run",
        lambda *a, **k: SimpleNamespace(returncode=0, stdout=table, stderr=""),
    )
    assert cb.reachable_address("container") == ("192.168.64.2", 22)


def test_reachable_address_apple_container_absent_is_none(monkeypatch):
    monkeypatch.setattr(
        cb.subprocess,
        "run",
        lambda *a, **k: SimpleNamespace(
            returncode=0, stdout="ID IMAGE STATE ADDR\n", stderr=""
        ),
    )
    assert cb.reachable_address("container") is None


# ── builder_session lifecycle (start → yield builders line → teardown) ───────


def _fake_runtime(monkeypatch, *, run_ok=True):
    """Drive a fake podman: pull ok, run ok, reachable immediately, track stop."""
    events = []

    def fake_run(argv, **k):
        events.append(argv)
        rc = 0 if run_ok else 1
        return SimpleNamespace(returncode=rc, stdout="", stderr="")

    monkeypatch.setattr(cb.subprocess, "run", fake_run)
    monkeypatch.setattr(cb, "ensure_keypair", lambda _run=None: "ssh-ed25519 AAA k@y")
    monkeypatch.setattr(cb, "reachable", lambda *a, **k: True)
    return events


def test_builder_session_yields_builders_line_and_tears_down(monkeypatch):
    events = _fake_runtime(monkeypatch)
    got = {}
    with cb.builder_session("podman") as line:
        got["line"] = line
    assert got["line"] is not None
    assert got["line"].startswith("ssh-ng://root@127.0.0.1:")
    # teardown ran: an `rm -f` for the container happened (pre-start clean +
    # post-use stop both call it, so at least one).
    assert any(a[:2] == ["podman", "rm"] for a in events)


def test_builder_session_none_when_run_fails(monkeypatch):
    _fake_runtime(monkeypatch, run_ok=False)
    with cb.builder_session("podman") as line:
        assert line is None  # caller falls back (QEMU / clear error)


def test_builder_session_none_when_never_reachable(monkeypatch):
    _fake_runtime(monkeypatch)
    monkeypatch.setattr(cb, "reachable", lambda *a, **k: False)
    # short-circuit the wait so the test doesn't sleep
    monkeypatch.setattr(cb, "_wait_reachable", lambda *a, **k: None)
    with cb.builder_session("podman") as line:
        assert line is None

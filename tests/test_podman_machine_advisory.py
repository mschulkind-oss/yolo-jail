"""Podman Machine memory advisories — ported from PR #21 (kurt-hs).

Two surfaces, both strictly advisory:
  * `yolo check` reports the VM's memory and warns below a floor or below
    the workspace's ``resources.memory`` request.
  * `yolo run` prints an OOM hint when a jail exits 137 on macOS+podman
    under an undersized VM.
"""

import json
import sys
from pathlib import Path
from unittest.mock import MagicMock

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

from cli.runtime import (  # noqa: E402
    PODMAN_MACHINE_MEMORY_FLOOR_MB,
    _podman_machine_memory,
    _podman_machine_resize_hint,
)


def _inspect_json(machines):
    return json.dumps(machines)


class TestPodmanMachineMemory:
    def _mock_inspect(self, monkeypatch, stdout, returncode=0, raises=None):
        def fake_run(cmd, **kwargs):
            assert cmd == ["podman", "machine", "inspect"]
            if raises:
                raise raises
            return MagicMock(returncode=returncode, stdout=stdout)

        monkeypatch.setattr("cli.runtime.subprocess.run", fake_run)

    def test_prefers_running_machine(self, monkeypatch):
        self._mock_inspect(
            monkeypatch,
            _inspect_json(
                [
                    {
                        "Name": "stopped",
                        "State": "stopped",
                        "Resources": {"Memory": 1024},
                    },
                    {"Name": "live", "State": "running", "Resources": {"Memory": 8192}},
                ]
            ),
        )
        assert _podman_machine_memory() == ("live", 8192)

    def test_falls_back_to_first_machine(self, monkeypatch):
        self._mock_inspect(
            monkeypatch,
            _inspect_json(
                [{"Name": "only", "State": "stopped", "Resources": {"Memory": 2048}}]
            ),
        )
        assert _podman_machine_memory() == ("only", 2048)

    def test_defaults_missing_name(self, monkeypatch):
        self._mock_inspect(
            monkeypatch,
            _inspect_json([{"State": "running", "Resources": {"Memory": 4096}}]),
        )
        assert _podman_machine_memory() == ("podman-machine-default", 4096)

    def test_none_on_bad_json(self, monkeypatch):
        self._mock_inspect(monkeypatch, "not json {")
        assert _podman_machine_memory() is None

    def test_none_on_nonzero_exit(self, monkeypatch):
        self._mock_inspect(monkeypatch, "", returncode=125)
        assert _podman_machine_memory() is None

    def test_none_on_missing_podman(self, monkeypatch):
        self._mock_inspect(monkeypatch, "", raises=FileNotFoundError())
        assert _podman_machine_memory() is None

    def test_none_on_non_int_memory(self, monkeypatch):
        self._mock_inspect(
            monkeypatch,
            _inspect_json(
                [{"Name": "m", "State": "running", "Resources": {"Memory": "lots"}}]
            ),
        )
        assert _podman_machine_memory() is None


class TestCheckPodmanMachineResources:
    def _events(self):
        events = []
        return (
            events,
            lambda m: events.append(("ok", m)),
            lambda m, note="": events.append(("warn", m, note)),
        )

    def _run(self, monkeypatch, tmp_path, mem_mb, ws_config=None):
        from cli import check_cmd

        monkeypatch.setattr(
            "cli.check_cmd._podman_machine_memory", lambda: ("vm", mem_mb)
        )
        monkeypatch.setattr(
            "cli.check_cmd.load_config", lambda ws, strict=False: ws_config or {}
        )
        events, ok, warn = self._events()
        check_cmd._check_podman_machine_resources(tmp_path, ok=ok, warn=warn)
        return events

    def test_below_floor_warns_with_resize_hint(self, monkeypatch, tmp_path):
        events = self._run(monkeypatch, tmp_path, 2048)
        assert events[0][0] == "warn"
        assert "2048 MB" in events[0][1]
        assert "podman machine set --memory" in events[0][2]

    def test_below_workspace_request_warns(self, monkeypatch, tmp_path):
        events = self._run(
            monkeypatch,
            tmp_path,
            PODMAN_MACHINE_MEMORY_FLOOR_MB,
            ws_config={"resources": {"memory": "8g"}},
        )
        assert events[0][0] == "warn"
        assert "resources.memory=8g" in events[0][1]

    def test_healthy_reports_ok(self, monkeypatch, tmp_path):
        events = self._run(monkeypatch, tmp_path, 8192)
        assert events == [("ok", "Podman Machine 'vm' memory: 8192 MB")]

    def test_silent_when_inspect_unavailable(self, monkeypatch, tmp_path):
        from cli import check_cmd

        monkeypatch.setattr("cli.check_cmd._podman_machine_memory", lambda: None)
        events, ok, warn = self._events()
        check_cmd._check_podman_machine_resources(tmp_path, ok=ok, warn=warn)
        assert events == []


class TestOomKillerHint:
    def _run(
        self, monkeypatch, capsys, *, exit_code, macos=True, mem=2048, rt="podman"
    ):
        from cli import run_cmd

        monkeypatch.setattr("cli.run_cmd.IS_MACOS", macos)
        monkeypatch.setattr(
            "cli.run_cmd._podman_machine_memory",
            (lambda: ("vm", mem)) if mem is not None else (lambda: None),
        )
        run_cmd._maybe_warn_about_oom_killer(exit_code, rt)
        return capsys.readouterr().out

    def test_fires_on_137_small_vm(self, monkeypatch, capsys):
        out = self._run(monkeypatch, capsys, exit_code=137)
        assert "OOM-killer" in out
        assert "2048 MB" in out

    def test_silent_on_other_exit_codes(self, monkeypatch, capsys):
        assert self._run(monkeypatch, capsys, exit_code=1) == ""

    def test_silent_off_macos(self, monkeypatch, capsys):
        assert self._run(monkeypatch, capsys, exit_code=137, macos=False) == ""

    def test_silent_on_adequate_vm(self, monkeypatch, capsys):
        assert (
            self._run(
                monkeypatch, capsys, exit_code=137, mem=PODMAN_MACHINE_MEMORY_FLOOR_MB
            )
            == ""
        )

    def test_silent_when_inspect_unavailable(self, monkeypatch, capsys):
        assert self._run(monkeypatch, capsys, exit_code=137, mem=None) == ""

    def test_silent_on_apple_container(self, monkeypatch, capsys):
        assert self._run(monkeypatch, capsys, exit_code=137, rt="container") == ""


class TestResizeHint:
    def test_hint_names_the_restart_cost(self):
        hint = _podman_machine_resize_hint()
        assert "podman machine set --memory" in hint
        assert "restarts the VM" in hint

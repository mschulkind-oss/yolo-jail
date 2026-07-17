"""Unit tests for cli.py — check command, auto_load_image, tmux/kitty, ps, and run internals.

These tests mock subprocess and filesystem to exercise cli.py's heavier logic
without spinning up actual containers.
"""

import json
import socket
import struct
import subprocess
import os
import sys
import threading
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

from typer.testing import CliRunner  # noqa: E402

import cli  # noqa: E402
from cli import (  # noqa: E402
    _build_image_store_path,
    _host_service_sockets_dir,
    _kitty_setup_jail_tab,
    _tmux_rename_window,
    _tmux_setup_jail_pane,
    auto_load_image,
    container_name_for_workspace,
    start_host_port_forwarding,
    app,
)


# ═══════════════════════════════════════════════════════════════════════════════
# Test: auto_load_image
# ═══════════════════════════════════════════════════════════════════════════════


class TestAutoLoadImage:
    """Test the nix image build + load pipeline."""

    @patch("cli.image._build_image_store_path")
    @patch("cli.image._read_loaded_paths")
    @patch("cli.image._add_loaded_path")
    @patch("subprocess.Popen")
    @patch("subprocess.run")
    @patch("cli.image._estimate_image_size", return_value=0)
    def test_skips_load_when_already_loaded_and_image_present(
        self, mock_est, mock_run, mock_popen, mock_add, mock_read, mock_build, tmp_path
    ):
        mock_build.return_value = ("/nix/store/abc", [])
        mock_read.return_value = {"/nix/store/abc"}  # Already loaded
        mock_run.return_value = MagicMock(returncode=0)  # image inspect succeeds
        with patch("cli.image.BUILD_DIR", tmp_path):
            auto_load_image(tmp_path, runtime="podman")
        mock_popen.assert_not_called()  # No streaming needed
        mock_add.assert_not_called()  # Sentinel not rewritten

    @patch("cli.image._build_image_store_path")
    @patch("cli.image._read_loaded_paths")
    @patch("cli.image._add_loaded_path")
    @patch("cli.image._estimate_image_size", return_value=100_000_000)
    def test_reloads_when_sentinel_says_loaded_but_image_missing(
        self, mock_est, mock_add, mock_read, mock_build, tmp_path
    ):
        """Regression: sentinel can lie if podman storage was pruned/reset.
        Without re-verifying, podman run falls back to a registry pull
        ('Trying to pull docker://localhost/yolo-jail:latest')."""
        mock_build.return_value = ("/nix/store/abc", [])
        mock_read.return_value = {"/nix/store/abc"}  # Sentinel claims loaded

        stream_proc = MagicMock()
        stream_proc.stdout.read.side_effect = [b"x" * 1024, b""]
        stream_proc.returncode = 0
        stream_proc.wait.return_value = None

        # First subprocess.run is the image inspect (returncode=1 → missing).
        # Subsequent subprocess.run calls are the load — return rc=0.
        run_results = [
            MagicMock(returncode=1, stderr=b""),  # inspect: image NOT present
            MagicMock(returncode=0, stderr=b""),  # load: success
        ]

        with (
            patch("cli.image.BUILD_DIR", tmp_path),
            patch("cli.image.GLOBAL_CACHE", tmp_path / "cache"),
            patch("subprocess.Popen", return_value=stream_proc),
            patch("subprocess.run", side_effect=run_results),
        ):
            auto_load_image(tmp_path, runtime="podman")

        mock_add.assert_called_once()  # Reloaded → sentinel rewritten

    @patch("cli.image._build_image_store_path")
    @patch("subprocess.run")
    def test_warns_on_build_failure_with_existing_image(
        self, mock_run, mock_build, tmp_path
    ):
        mock_build.return_value = (None, ["error: something broke"])
        mock_run.return_value = MagicMock(returncode=0)  # Image exists
        with patch("cli.image.BUILD_DIR", tmp_path):
            ok = auto_load_image(tmp_path, runtime="podman")
        # Build failed but a usable image is already loaded → runnable.
        assert ok is True
        mock_run.assert_called()

    @patch("cli.image._build_image_store_path")
    @patch("subprocess.run")
    def test_errors_on_build_failure_no_image(self, mock_run, mock_build, tmp_path):
        mock_build.return_value = (None, ["error: nope"])
        mock_run.return_value = MagicMock(returncode=1)  # No image
        with patch("cli.image.BUILD_DIR", tmp_path):
            ok = auto_load_image(tmp_path, runtime="podman")
        # No image and can't build one → NOT runnable; caller must abort
        # (rather than fall through to a registry-pull 401).
        assert ok is False

    def test_macos_container_runtime_builds_via_container_builder(
        self, tmp_path, monkeypatch
    ):
        """On macOS + a container runtime, a from-source build must open the
        on-demand container builder and hand its --builders line to the nix
        build (roadmap #3). Regression guard for the wiring."""
        import contextlib

        import cli.image as im

        seen = {}

        @contextlib.contextmanager
        def fake_session(runtime):
            seen["runtime"] = runtime
            yield "ssh-ng://root@127.0.0.1:31022 aarch64-linux /k 4"

        def fake_build(
            repo_root, extra_packages=None, *, out_link, status_message, builders=None
        ):
            seen["builders"] = builders
            return "/nix/store/fake-ociImage", []

        monkeypatch.setattr(im, "IS_MACOS", True)
        monkeypatch.setattr(
            "cli.check_cmd._nix_dry_run_will_build", lambda *a: (True, ["x.drv"])
        )
        monkeypatch.setattr("cli.container_builder.builder_session", fake_session)
        monkeypatch.setattr(im, "_build_image_store_path", fake_build)
        # short-circuit the load path after the build so we only test the build wiring
        monkeypatch.setattr(
            im, "_read_loaded_paths", lambda *a: {"/nix/store/fake-ociImage"}
        )
        monkeypatch.setattr(
            im.subprocess, "run", lambda *a, **k: MagicMock(returncode=0)
        )
        with patch("cli.image.BUILD_DIR", tmp_path):
            auto_load_image(tmp_path, extra_packages=["jq"], runtime="container")

        assert seen["runtime"] == "container"  # session opened for the AC runtime
        assert seen["builders"].startswith("ssh-ng://root@")  # line threaded to build

    @patch("cli.image._build_image_store_path")
    @patch("cli.image._read_loaded_paths", return_value=set())
    @patch("cli.image._add_loaded_path")
    @patch("cli.image._estimate_image_size", return_value=100_000_000)
    def test_caches_and_loads_image_on_new_path(
        self, mock_est, mock_add, mock_read, mock_build, tmp_path
    ):
        mock_build.return_value = ("/nix/store/new", [])

        # Mock the streaming to cache file
        stream_proc = MagicMock()
        stream_proc.stdout.read.side_effect = [b"x" * 1024, b""]  # One chunk
        stream_proc.returncode = 0
        stream_proc.wait.return_value = None

        load_result = MagicMock(returncode=0, stderr=b"")

        with (
            patch("cli.image.BUILD_DIR", tmp_path),
            patch("cli.image.GLOBAL_CACHE", tmp_path / "cache"),
            patch("subprocess.Popen", return_value=stream_proc),
            patch("subprocess.run", return_value=load_result),
        ):
            auto_load_image(tmp_path, runtime="podman")

        mock_add.assert_called_once()

    @patch("cli.image._build_image_store_path")
    @patch("cli.image._read_loaded_paths", return_value=set())
    @patch("cli.image._add_loaded_path")
    def test_reuses_cached_tar(self, mock_add, mock_read, mock_build, tmp_path):
        """When the tar cache file already exists, skip materialization."""
        mock_build.return_value = ("/nix/store/cached", [])

        # Pre-create the cache file
        cache_dir = tmp_path / "cache" / "images"
        cache_dir.mkdir(parents=True)
        import hashlib

        path_hash = hashlib.sha256(b"/nix/store/cached").hexdigest()[:16]
        (cache_dir / f"{path_hash}.tar").write_bytes(b"fake tar")

        load_result = MagicMock(returncode=0, stderr=b"")

        with (
            patch("cli.image.BUILD_DIR", tmp_path),
            patch("cli.image.GLOBAL_CACHE", tmp_path / "cache"),
            patch("subprocess.Popen") as mock_popen,
            patch("subprocess.run", return_value=load_result),
        ):
            auto_load_image(tmp_path, runtime="podman")

        # Should NOT have streamed (Popen not called for image generation)
        mock_popen.assert_not_called()
        mock_add.assert_called_once()


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _build_image_store_path
# ═══════════════════════════════════════════════════════════════════════════════


def _make_nix_proc(lines, returncode=0):
    """Create a mock subprocess for nix build with readline-compatible stderr."""
    proc = MagicMock()
    proc.stderr = MagicMock()
    proc.stderr.readline = MagicMock(side_effect=lines)
    proc.returncode = returncode
    proc.wait.return_value = returncode
    return proc


class TestBuildImageStorePath:
    @patch("subprocess.Popen")
    def test_successful_build(self, mock_popen, tmp_path):
        out_link = tmp_path / "result"
        out_link.symlink_to(tmp_path)
        mock_popen.return_value = _make_nix_proc(["evaluating\n", ""], 0)

        path, tail = _build_image_store_path(
            tmp_path,
            out_link=out_link,
            status_message="Building...",
        )
        assert path is not None

    @patch("subprocess.Popen")
    def test_failed_build(self, mock_popen, tmp_path):
        out_link = tmp_path / "result"
        mock_popen.return_value = _make_nix_proc(["error: something bad\n", ""], 1)

        path, tail = _build_image_store_path(
            tmp_path,
            out_link=out_link,
            status_message="Building...",
        )
        assert path is None
        assert len(tail) > 0

    @patch("subprocess.Popen", side_effect=FileNotFoundError)
    def test_nix_not_found(self, mock_popen, tmp_path):
        out_link = tmp_path / "result"
        path, tail = _build_image_store_path(
            tmp_path,
            out_link=out_link,
            status_message="Building...",
        )
        assert path is None
        assert "nix command not found" in tail[0]

    @patch("subprocess.Popen")
    def test_extra_packages_env(self, mock_popen, tmp_path):
        out_link = tmp_path / "result"
        out_link.symlink_to(tmp_path)
        mock_popen.return_value = _make_nix_proc([""], 0)

        _build_image_store_path(
            tmp_path,
            extra_packages=["postgresql"],
            out_link=out_link,
            status_message="Building...",
        )
        call_kwargs = mock_popen.call_args
        env = call_kwargs[1].get("env") or call_kwargs.kwargs.get("env")
        assert env is not None
        assert "YOLO_EXTRA_PACKAGES" in env

    @patch("subprocess.Popen")
    def test_extra_packages_env_threads_library_package(self, mock_popen, tmp_path):
        """A package added for its shared library still threads through
        YOLO_EXTRA_PACKAGES so the flake's lib-link loop receives it.

        The flake side (getLib → symlink into /lib) is covered by the
        integration tests in test_jail.py; this is the cheap fast-suite
        anchor that the env wiring is intact for a lib-only package.
        """
        out_link = tmp_path / "result"
        out_link.symlink_to(tmp_path)
        mock_popen.return_value = _make_nix_proc([""], 0)

        _build_image_store_path(
            tmp_path,
            extra_packages=["zbar"],
            out_link=out_link,
            status_message="Building...",
        )
        call_kwargs = mock_popen.call_args
        env = call_kwargs[1].get("env") or call_kwargs.kwargs.get("env")
        assert env is not None
        assert json.loads(env["YOLO_EXTRA_PACKAGES"]) == ["zbar"]

    @patch("subprocess.Popen")
    def test_stderr_tail_capped(self, mock_popen, tmp_path):
        out_link = tmp_path / "result"
        lines = [f"line {i}\n" for i in range(50)] + [""]
        mock_popen.return_value = _make_nix_proc(lines, 1)

        path, tail = _build_image_store_path(
            tmp_path,
            out_link=out_link,
            status_message="Building...",
        )
        assert len(tail) <= 30


# ═══════════════════════════════════════════════════════════════════════════════
# Test: tmux helpers
# ═══════════════════════════════════════════════════════════════════════════════


class TestTmuxRenameWindow:
    def test_noop_when_no_tmux(self, monkeypatch):
        monkeypatch.delenv("TMUX", raising=False)
        monkeypatch.delenv("YOLO_NO_TMUX", raising=False)
        with patch("subprocess.run") as mock_run:
            _tmux_rename_window("JAIL")
            mock_run.assert_not_called()

    def test_noop_when_yolo_no_tmux(self, monkeypatch):
        monkeypatch.setenv("YOLO_NO_TMUX", "1")
        monkeypatch.setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
        with patch("subprocess.run") as mock_run:
            _tmux_rename_window("JAIL")
            mock_run.assert_not_called()

    def test_renames_in_tmux(self, monkeypatch):
        monkeypatch.setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
        monkeypatch.delenv("YOLO_NO_TMUX", raising=False)
        with patch("subprocess.run") as mock_run:
            _tmux_rename_window("JAIL")
            mock_run.assert_called_once()
            args = mock_run.call_args[0][0]
            assert "tmux" in args
            assert "rename-window" in args
            assert "JAIL" in args


class TestTmuxSetupJailPane:
    def test_noop_when_no_tmux(self, monkeypatch):
        monkeypatch.delenv("TMUX", raising=False)
        monkeypatch.delenv("YOLO_NO_TMUX", raising=False)
        result = _tmux_setup_jail_pane()
        assert result is None

    def test_noop_when_yolo_no_tmux(self, monkeypatch):
        monkeypatch.setenv("YOLO_NO_TMUX", "1")
        result = _tmux_setup_jail_pane()
        assert result is None

    def test_noop_when_not_tty(self, monkeypatch):
        monkeypatch.setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
        monkeypatch.delenv("YOLO_NO_TMUX", raising=False)
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            result = _tmux_setup_jail_pane()
            assert result is None

    @patch("subprocess.run")
    def test_sets_indicators_and_returns_restore(self, mock_run, monkeypatch):
        monkeypatch.setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
        monkeypatch.setenv("TMUX_PANE", "%0")
        monkeypatch.delenv("YOLO_NO_TMUX", raising=False)
        mock_run.return_value = MagicMock(returncode=0, stdout="old-window\n")
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            result = _tmux_setup_jail_pane()
            assert result is not None  # restore function returned
            assert callable(result)

    @patch("subprocess.run")
    def test_restore_function_calls_tmux(self, mock_run, monkeypatch):
        monkeypatch.setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
        monkeypatch.setenv("TMUX_PANE", "%0")
        monkeypatch.delenv("YOLO_NO_TMUX", raising=False)
        mock_run.return_value = MagicMock(returncode=0, stdout="old-window\n")
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            restore = _tmux_setup_jail_pane()
        # Call restore
        restore()
        # Should have called tmux to restore settings
        assert mock_run.call_count > 0


class TestKittySetupJailTab:
    def test_noop_no_kitty(self, monkeypatch):
        monkeypatch.delenv("KITTY_PID", raising=False)
        result = _kitty_setup_jail_tab()
        assert result is None

    def test_noop_not_tty(self, monkeypatch):
        monkeypatch.setenv("KITTY_PID", "1234")
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            result = _kitty_setup_jail_tab()
            assert result is None

    @patch("subprocess.run")
    @patch("subprocess.check_output", return_value=b"old-title\n")
    def test_sets_tab_title(self, mock_check, mock_run, monkeypatch):
        monkeypatch.setenv("KITTY_PID", "1234")
        monkeypatch.setenv("KITTY_WINDOW_ID", "42")
        monkeypatch.delenv("SM_PROJECT", raising=False)
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            result = _kitty_setup_jail_tab()
            assert result is not None
            assert callable(result)

    @patch("subprocess.run", side_effect=Exception("kitten not found"))
    @patch("subprocess.check_output", return_value=b"")
    def test_graceful_on_kitten_failure(self, mock_check, mock_run, monkeypatch):
        monkeypatch.setenv("KITTY_PID", "1234")
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            result = _kitty_setup_jail_tab()
            assert result is None  # Fails gracefully


# ═══════════════════════════════════════════════════════════════════════════════
# Test: check command (via CliRunner with mocked subprocess)
# ═══════════════════════════════════════════════════════════════════════════════


class TestCheckCommand:
    """Test the check/doctor command by mocking external calls."""

    def _mock_subprocess_run(self, cmd, **kwargs):
        """Smart mock that returns expected output for different commands."""
        if isinstance(cmd, list):
            prog = cmd[0] if cmd else ""
            if prog == "podman":
                if "--version" in cmd:
                    return MagicMock(returncode=0, stdout=f"{prog} version 4.9.0\n")
                if "image" in cmd and "inspect" in cmd:
                    return MagicMock(returncode=0)
                if "ps" in cmd:
                    return MagicMock(returncode=0, stdout="")
                if "images" in cmd:
                    return MagicMock(returncode=0, stdout="yolo-jail:latest (1.2 GB)\n")
                return MagicMock(returncode=0, stdout="")
            if prog == "nix":
                return MagicMock(returncode=0, stdout="nix (Nix) 2.18.1\n")
            if prog == sys.executable:
                return MagicMock(returncode=0, stdout="ok\n", stderr="")
        return MagicMock(returncode=0, stdout="", stderr="")

    @patch("subprocess.run")
    @patch("subprocess.Popen")
    @patch("shutil.which")
    def test_check_no_build(
        self, mock_which, mock_popen, mock_run, tmp_path, monkeypatch
    ):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )
        mock_run.side_effect = self._mock_subprocess_run

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        # Should complete without crashing
        assert "Summary" in result.output or result.exit_code == 0

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_native_macos_user_skips_container_runtime_probe(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        """runtime: macos-user (native) must short-circuit the Container
        Runtime section with the native PASS instead of probing for / failing
        on a missing container runtime. This is the earliest `is_native_runtime`
        gate and — unlike the later Image Build / liveness sections — it prints
        BEFORE the macOS-Platform `/nix` check, so it's assertable regardless of
        whether the host has /nix (a real Mac CI runner does not, which the
        downstream sections' early-exit depends on). Regression: the first
        version of this test asserted a downstream line a real Mac never
        reached because `/nix not found` ends the run first."""
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        monkeypatch.setenv("YOLO_RUNTIME", "macos-user")
        # macos-user is macOS-only; force the native runtime to resolve.
        monkeypatch.setattr("cli.runtime.IS_MACOS", True)
        monkeypatch.setattr("cli.check_cmd.IS_MACOS", True)
        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("nix", "sandbox-exec") else None
        )
        mock_run.side_effect = self._mock_subprocess_run

        result = CliRunner().invoke(app, ["check", "--no-build"])
        out = result.output
        # The native short-circuit fired (no container-runtime probe/fail).
        assert "no container runtime needed" in out
        assert "No container runtime installed" not in out

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_reports_missing_runtime(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.return_value = None  # Nothing found
        mock_run.return_value = MagicMock(returncode=1, stdout="", stderr="")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        # Should report failure
        assert (
            result.exit_code != 0
            or "No container runtime" in result.output
            or "failed" in result.output
        )

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_validates_workspace_config(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )
        mock_run.side_effect = self._mock_subprocess_run

        # Write an invalid config
        (tmp_path / "yolo-jail.jsonc").write_text('{"bogus_key": "bad"}')

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "unknown" in result.output.lower() or result.exit_code != 0

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_invalid_json(self, mock_which, mock_run, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )
        mock_run.side_effect = self._mock_subprocess_run

        # Write broken JSON
        (tmp_path / "yolo-jail.jsonc").write_text("{broken json}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert result.exit_code != 0


class TestDoctorAlias:
    @patch("subprocess.run")
    @patch("shutil.which")
    def test_doctor_is_alias_for_check(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )
        mock_run.return_value = MagicMock(returncode=0, stdout="podman version 4.9.0\n")

        runner = CliRunner()
        result = runner.invoke(app, ["doctor", "--no-build"])
        # Doctor should behave like check
        assert "Summary" in result.output or result.exit_code == 0


# ═══════════════════════════════════════════════════════════════════════════════
# Test: ps command
# ═══════════════════════════════════════════════════════════════════════════════


class TestPsCommand:
    @patch("cli.run_cmd._runtime", return_value="podman")
    @patch("subprocess.run")
    def test_no_running_jails(self, mock_run, mock_runtime):
        mock_run.return_value = MagicMock(returncode=0, stdout="")
        runner = CliRunner()
        result = runner.invoke(app, ["ps"])
        assert "No running jails" in result.output

    @patch("cli.run_cmd._runtime", return_value="podman")
    @patch("subprocess.run")
    @patch("cli.run_cmd.find_running_container")
    @patch("cli.CONTAINER_DIR")
    def test_shows_running_jails(
        self, mock_dir, mock_find, mock_run, mock_runtime, tmp_path
    ):
        mock_run.return_value = MagicMock(
            returncode=0,
            stdout="NAMES\tSTATUS\tRUNNING\nyolo-abc123\tUp 5 minutes\t5 minutes ago\n",
        )
        mock_dir.exists.return_value = True
        tracking = tmp_path / "yolo-abc123"
        tracking.write_text("/home/user/project\n")
        mock_dir.iterdir.return_value = [tracking]
        mock_find.return_value = "abc123"

        runner = CliRunner()
        result = runner.invoke(app, ["ps"])
        assert result.exit_code == 0


# ═══════════════════════════════════════════════════════════════════════════════
# Test: start_host_port_forwarding
# ═══════════════════════════════════════════════════════════════════════════════


class TestStartHostPortForwarding:
    @staticmethod
    def _socat_stub(mock_popen):
        """Make the mocked Popen behave like socat: create the
        UNIX-LISTEN socket file.  start_host_port_forwarding polls for
        those files (instead of a fixed sleep), so the stub keeps the
        readiness wait on its instant fast path."""

        def spawn(cmd, **_kwargs):
            listen = next(a for a in cmd if a.startswith("UNIX-LISTEN:"))
            Path(listen.split(":", 1)[1].split(",", 1)[0]).touch()
            return MagicMock()

        mock_popen.side_effect = spawn

    def test_empty_list_returns_empty(self, tmp_path):
        result = start_host_port_forwarding([], "yolo-test", tmp_path)
        assert result == []

    @patch("subprocess.Popen")
    def test_creates_socat_processes(self, mock_popen, tmp_path):
        self._socat_stub(mock_popen)
        socket_dir = tmp_path / "sockets"
        Path.home() / ".local" / "share" / "yolo-jail" / "logs"
        result = start_host_port_forwarding([5432], "yolo-test", socket_dir)
        assert len(result) == 1
        mock_popen.assert_called_once()
        # The readiness poll saw the socket file appear.
        assert (socket_dir / "port-5432.sock").exists()

    @patch("subprocess.Popen", side_effect=FileNotFoundError)
    def test_socat_not_found(self, mock_popen, tmp_path, capsys):
        socket_dir = tmp_path / "sockets"
        result = start_host_port_forwarding([5432], "yolo-test", socket_dir)
        assert result == []

    @patch("subprocess.Popen")
    def test_multiple_ports(self, mock_popen, tmp_path):
        self._socat_stub(mock_popen)
        socket_dir = tmp_path / "sockets"
        result = start_host_port_forwarding(
            [5432, "8080:9090"], "yolo-test", socket_dir
        )
        assert len(result) == 2

    @patch("subprocess.Popen")
    def test_removes_stale_socket(self, mock_popen, tmp_path):
        socket_dir = tmp_path / "sockets"
        socket_dir.mkdir()
        stale = socket_dir / "port-5432.sock"
        stale.touch()
        seen_at_spawn = {}

        def spawn(cmd, **_kwargs):
            # Record whether the stale socket was gone when "socat"
            # started, then create the fresh one like socat would.
            seen_at_spawn["stale_removed"] = not stale.exists()
            stale.touch()
            return MagicMock()

        mock_popen.side_effect = spawn
        start_host_port_forwarding([5432], "yolo-test", socket_dir)
        # Stale socket was removed before socat spawned (socat then
        # creates a new one).
        assert seen_at_spawn["stale_removed"] is True


# ═══════════════════════════════════════════════════════════════════════════════
# Test: init-user-config command
# ═══════════════════════════════════════════════════════════════════════════════


class TestInitUserConfig:
    @patch("cli.init_cmd.USER_CONFIG_PATH")
    def test_creates_user_config(self, mock_path, tmp_path, monkeypatch):
        config_path = tmp_path / "config.jsonc"
        monkeypatch.setattr("cli.init_cmd.USER_CONFIG_PATH", config_path)
        runner = CliRunner()
        result = runner.invoke(app, ["init-user-config"])
        assert result.exit_code == 0
        assert config_path.exists()

    def test_init_user_config_idempotent(self, tmp_path, monkeypatch):
        config_path = tmp_path / "config.jsonc"
        monkeypatch.setattr("cli.init_cmd.USER_CONFIG_PATH", config_path)
        runner = CliRunner()
        runner.invoke(app, ["init-user-config"])
        result = runner.invoke(app, ["init-user-config"])
        assert result.exit_code == 0
        assert "already exists" in result.output


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _print_init_briefing (via init)
# ═══════════════════════════════════════════════════════════════════════════════


class TestInitBriefing:
    def test_briefing_contains_key_info(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        runner = CliRunner()
        result = runner.invoke(app, ["init"])
        assert "YOLO" in result.output.upper() or "jail" in result.output.lower()
        # Briefing should mention key setup steps
        assert "yolo-jail.jsonc" in result.output or "config" in result.output.lower()


# ═══════════════════════════════════════════════════════════════════════════════
# Test: run() command internal logic (mocked)
# ═══════════════════════════════════════════════════════════════════════════════


class TestRunCommandInternals:
    """Test run() internals by mocking container interactions."""

    @patch("subprocess.run")
    @patch("subprocess.Popen")
    @patch("subprocess.check_output")
    @patch("cli.run_cmd.find_running_container", return_value="abc123")
    @patch("cli.run_cmd._runtime", return_value="podman")
    @patch("cli.run_cmd.auto_load_image")
    def test_exec_into_existing_container(
        self,
        mock_load,
        mock_runtime,
        mock_find,
        mock_check_output,
        mock_popen,
        mock_run,
        tmp_path,
        monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        mock_check_output.side_effect = FileNotFoundError  # No git/jj identity
        mock_run.return_value = MagicMock(returncode=0)

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "echo", "hello"])
        # Should try to exec into existing container (not start new one)
        mock_load.assert_not_called()

    @patch("subprocess.run")
    @patch("subprocess.Popen")
    @patch("subprocess.check_output")
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("cli.run_cmd._runtime", return_value="podman")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.generate_agents_md")
    @patch("cli.start_host_port_forwarding", return_value=[])
    @patch("cli.cleanup_port_forwarding")
    @patch("cli.run_cmd.write_container_tracking")
    @patch("cli._tmux_rename_window")
    @patch("cli.run_cmd._jail_mise_store_dir")
    @patch("cli._seed_agent_dir")
    def test_new_container_creation(
        self,
        mock_seed_agent,
        mock_mise_dir,
        mock_tmux,
        mock_write_track,
        mock_cleanup_fwd,
        mock_start_fwd,
        mock_agents,
        mock_config_changes,
        mock_load,
        mock_runtime,
        mock_find,
        mock_check_output,
        mock_popen,
        mock_run,
        tmp_path,
        monkeypatch,
    ):
        # Shared hermetic setup: redirects every GLOBAL_* path to
        # tmp_path, no-ops time.sleep, and stubs the broker/relay/
        # loophole plumbing (real subprocesses + a ~6s socket.accept
        # join in stop_loopholes) that this test doesn't assert on.
        _run_monkeypatch(monkeypatch, tmp_path)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        mock_check_output.side_effect = FileNotFoundError
        mock_mise_dir.return_value = tmp_path / "mise"
        agents_dir = tmp_path / "agents" / "yolo-test"
        agents_dir.mkdir(parents=True)
        (agents_dir / "AGENTS-copilot.md").write_text("test")
        (agents_dir / "AGENTS-gemini.md").write_text("test")
        (agents_dir / "CLAUDE.md").write_text("test")
        mock_agents.return_value = agents_dir

        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 0
        mock_popen.return_value = proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "echo", "hello"])
        # When no existing container and config changes approved,
        # should attempt to load the image (or fail gracefully trying)
        # The key assertion is that it didn't exec into an existing container
        assert not any(
            "Attaching to existing" in str(c) for c in mock_run.call_args_list
        )

    @patch("subprocess.Popen")
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("cli.run_cmd.find_running_container", return_value="abc123")
    @patch("cli.run_cmd._runtime", return_value="podman")
    def test_copilot_yolo_injection(
        self,
        mock_runtime,
        mock_find,
        mock_check_output,
        mock_run,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        """Test that --yolo is injected for copilot command."""
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError
        mock_run.return_value = MagicMock(returncode=0)
        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "copilot"])
        # The exec-into-existing path runs through tty_proxy.run_with_proxy →
        # subprocess.Popen (CliRunner has no TTY so the proxy falls back).
        # Search every Popen call for the agent flags.
        if mock_popen.called:
            joined = " ".join(
                " ".join(str(c) for c in call.args[0])
                for call in mock_popen.call_args_list
            )
            assert "--yolo" in joined or "--no-auto-update" in joined

    @patch("subprocess.Popen")
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("cli.run_cmd.find_running_container", return_value="abc123")
    @patch("cli.run_cmd._runtime", return_value="podman")
    def test_gemini_yolo_injection(
        self,
        mock_runtime,
        mock_find,
        mock_check_output,
        mock_run,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        """Test that --yolo is injected for gemini command."""
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError
        mock_run.return_value = MagicMock(returncode=0)
        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "gemini"])
        if mock_popen.called:
            joined = " ".join(
                " ".join(str(c) for c in call.args[0])
                for call in mock_popen.call_args_list
            )
            assert "--yolo" in joined


class TestInjectAgentYoloFlags:
    """``_inject_agent_yolo_flags`` mutates the command in place.  It's
    the single source of truth for "did we make this agent actually
    yolo" — testing it directly is faster and more robust than
    exercising the entire ``yolo run`` attach path through CliRunner."""

    def _inject(self, argv):
        from cli import _inject_agent_yolo_flags

        cmd = list(argv)
        _inject_agent_yolo_flags(cmd)
        return cmd

    def test_claude_gets_dangerously_skip_permissions(self):
        """YOLO mode is ``--dangerously-skip-permissions``.  The
        settings.json allow-list that used to serve as "yolo" was
        half-broken (bare ``"Bash"`` doesn't match invocations); the
        flag bypasses the permission system entirely.  IS_SANDBOX=1
        in the jail env suppresses the flag's own launch confirmation
        so it runs cleanly."""
        out = self._inject(["claude", "--continue"])
        assert "--dangerously-skip-permissions" in out

    def test_claude_flag_goes_before_user_args(self):
        """Flag must land as argv[1] so the rest of the user's args
        stay in order (Claude parses positional-like options)."""
        out = self._inject(["claude", "-p", "hello"])
        assert out[:2] == ["claude", "--dangerously-skip-permissions"]

    def test_claude_does_not_duplicate_dangerously_flag(self):
        """If the user happened to pass it themselves, don't duplicate
        — Claude rejects the duplicate with a clear error."""
        out = self._inject(["claude", "--dangerously-skip-permissions"])
        assert out.count("--dangerously-skip-permissions") == 1

    def test_non_claude_command_left_alone(self):
        """Plain bash / ls / anything-else must not get the flag."""
        for argv in (
            ["bash", "-lc", "true"],
            ["ls", "-la"],
            ["python", "-c", "print(1)"],
        ):
            out = self._inject(argv)
            assert "--dangerously-skip-permissions" not in out

    def test_gemini_and_copilot_yolo_preserved(self):
        """Regression-safety for the existing gemini/copilot behavior."""
        assert "--yolo" in self._inject(["gemini"])
        copilot = self._inject(["copilot"])
        assert "--yolo" in copilot
        assert "--no-auto-update" in copilot

    def test_gemini_short_yolo_alias_not_duplicated(self):
        """``-y`` is the same switch as ``--yolo`` — don't add both."""
        out = self._inject(["gemini", "-y"])
        assert "--yolo" not in out
        assert out == ["gemini", "-y"]

    def test_copilot_flag_order_preserved(self):
        """Both copilot flags land before user args, in registry order."""
        out = self._inject(["copilot", "chat"])
        assert out == ["copilot", "--yolo", "--no-auto-update", "chat"]

    def test_opencode_and_pi_get_no_launch_flags(self):
        """opencode/pi auto-approve via their config files, not a flag."""
        assert self._inject(["opencode", "run", "hi"]) == ["opencode", "run", "hi"]
        assert self._inject(["pi", "-p", "x"]) == ["pi", "-p", "x"]

    def test_codex_gets_bypass_flag(self):
        """codex YOLO = --dangerously-bypass-approvals-and-sandbox (disables
        both its approval prompts and its own sandbox)."""
        out = self._inject(["codex", "exec", "do it"])
        assert out == [
            "codex",
            "--dangerously-bypass-approvals-and-sandbox",
            "exec",
            "do it",
        ]

    def test_codex_does_not_duplicate_bypass_flag(self):
        out = self._inject(["codex", "--dangerously-bypass-approvals-and-sandbox"])
        assert out.count("--dangerously-bypass-approvals-and-sandbox") == 1

    def test_empty_command_no_crash(self):
        """Defensive — empty list must be a no-op, not IndexError."""
        out = self._inject([])
        assert out == []


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _resolve_repo_root (installed package path)
# ═══════════════════════════════════════════════════════════════════════════════


class TestResolveRepoRootInstalled:
    """Test installed-package code path of _resolve_repo_root."""

    def test_installed_package_path(self, tmp_path, monkeypatch):
        """When running from installed package, should copy files to build root."""
        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)

        pkg_dir = tmp_path / "pkg" / "src"
        cli_dir = pkg_dir / "cli"
        cli_dir.mkdir(parents=True)
        (pkg_dir / "flake.nix").write_text("{ }")
        (pkg_dir / "flake.lock").write_text("{}")
        (pkg_dir / "entrypoint.py").write_text("")
        (cli_dir / "__init__.py").write_text("")

        build_root = tmp_path / "storage" / "nix-build-root"
        monkeypatch.setattr("cli.run_cmd.GLOBAL_STORAGE", tmp_path / "storage")

        import cli

        original_file = cli.__file__
        try:
            cli.__file__ = str(cli_dir / "__init__.py")
            result = _resolve_repo_root()
            assert result == build_root.resolve()
            assert (build_root / "flake.nix").exists()
            assert (build_root / "src").exists()
        finally:
            cli.__file__ = original_file

    def test_installed_package_path_is_idempotent(self, tmp_path, monkeypatch):
        """Regression: every ``yolo`` invocation was re-copying the
        package into nix-build-root via an atomic rename dance.  If
        something raced or failed mid-copy, the resulting build_root
        could be empty (bug 6 in the handoff).  The copy should be
        a no-op when the existing build_root already matches the
        wheel's flake.nix mtime."""
        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)

        pkg_dir = tmp_path / "pkg" / "src"
        cli_dir = pkg_dir / "cli"
        cli_dir.mkdir(parents=True)
        (pkg_dir / "flake.nix").write_text("{ }")
        (pkg_dir / "flake.lock").write_text("{}")
        (pkg_dir / "entrypoint.py").write_text("")
        (cli_dir / "__init__.py").write_text("")

        build_root = tmp_path / "storage" / "nix-build-root"
        monkeypatch.setattr("cli.run_cmd.GLOBAL_STORAGE", tmp_path / "storage")

        import cli

        original_file = cli.__file__
        try:
            cli.__file__ = str(cli_dir / "__init__.py")

            # First call: populates build_root.
            _resolve_repo_root()
            staged_init = build_root / "src" / "cli" / "__init__.py"
            assert staged_init.exists()
            first_mtime = (build_root / "flake.nix").stat().st_mtime_ns
            first_inode = staged_init.stat().st_ino

            # Second call: should be a no-op.  Build root should be
            # the SAME directory — not replaced via atomic rename —
            # so inode is preserved and mtime is unchanged.
            _resolve_repo_root()
            second_inode = staged_init.stat().st_ino
            second_mtime = (build_root / "flake.nix").stat().st_mtime_ns
            assert second_inode == first_inode, (
                "second call should reuse existing build_root, not recreate"
            )
            assert second_mtime == first_mtime
        finally:
            cli.__file__ = original_file

    def test_installed_package_path_recovers_from_empty_build_root(
        self, tmp_path, monkeypatch
    ):
        """If a prior invocation left build_root empty (bug 6), the
        next call should detect that and re-populate — not silently
        return an empty path that'd make the jail unusable."""
        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)

        pkg_dir = tmp_path / "pkg" / "src"
        cli_dir = pkg_dir / "cli"
        cli_dir.mkdir(parents=True)
        (pkg_dir / "flake.nix").write_text("{ }")
        (pkg_dir / "flake.lock").write_text("{}")
        (pkg_dir / "entrypoint.py").write_text("")
        (cli_dir / "__init__.py").write_text("")

        build_root = tmp_path / "storage" / "nix-build-root"
        # Simulate the empty-dir bug: build_root exists but has no content.
        build_root.mkdir(parents=True)
        monkeypatch.setattr("cli.run_cmd.GLOBAL_STORAGE", tmp_path / "storage")

        import cli

        original_file = cli.__file__
        try:
            cli.__file__ = str(cli_dir / "__init__.py")
            _resolve_repo_root()
            assert (build_root / "flake.nix").is_file()
            assert (build_root / "src" / "cli" / "__init__.py").is_file()
        finally:
            cli.__file__ = original_file

    def test_repopulate_preserves_previous_generation_dir(self, tmp_path, monkeypatch):
        """A repopulate must NOT delete the previous build_root — a jail
        launched from it may still hold its inode bound :ro at
        /opt/yolo-jail.  The old tree must be renamed aside to
        nix-build-root.old.<uuid> (same inode, same content), not rmtree'd.

        Regression for the `//deleted` race: in-jail `yolo` died with
        `No module named 'src'` because the repopulate's
        ``rmtree(nix-build-root.old)`` unlinked the inode a live jail still
        had mounted.
        """
        import os

        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)
        pkg_dir = tmp_path / "pkg" / "src"
        cli_dir = pkg_dir / "cli"
        cli_dir.mkdir(parents=True)
        (pkg_dir / "flake.nix").write_text("{ }")
        (pkg_dir / "flake.lock").write_text("{}")
        (pkg_dir / "entrypoint.py").write_text("")
        (cli_dir / "__init__.py").write_text("# v1")

        storage = tmp_path / "storage"
        build_root = storage / "nix-build-root"
        monkeypatch.setattr("cli.run_cmd.GLOBAL_STORAGE", storage)

        import cli

        original_file = cli.__file__
        try:
            cli.__file__ = str(cli_dir / "__init__.py")
            first = _resolve_repo_root()
            first_marker = first / "src" / "cli" / "__init__.py"
            first_inode = first_marker.stat().st_ino

            # Force a repopulate: bump pkg flake.nix mtime past the staged
            # copy so the idempotence skip falls through.
            os.utime(
                pkg_dir / "flake.nix",
                ((build_root / "flake.nix").stat().st_mtime + 10,) * 2,
            )
            (cli_dir / "__init__.py").write_text("# v2")
            _resolve_repo_root()

            # The repopulate actually happened (guards against a future
            # idempotence change silently turning this test green).
            assert (build_root / "src" / "cli" / "__init__.py").read_text() == "# v2"

            # The generation live at first resolve must still be reachable
            # by path (renamed aside, NOT rmtree'd), same inode + content.
            survivors = [
                p
                for p in storage.iterdir()
                if p.is_dir() and p.name.startswith("nix-build-root.old.")
            ]
            assert survivors, (
                "repopulate must rename the old build_root aside as "
                "nix-build-root.old.<uuid>, not delete it"
            )
            surv_marker = survivors[0] / "src" / "cli" / "__init__.py"
            assert surv_marker.stat().st_ino == first_inode
            assert surv_marker.read_text() == "# v1"
        finally:
            cli.__file__ = original_file

    def test_repopulate_aside_names_are_unique(self, tmp_path, monkeypatch):
        """Two repopulates must mint two DISTINCT aside dirs — a single
        fixed `.old` name would collide (rename onto a non-empty dir
        raises ENOTEMPTY and aborts one launch)."""
        import os

        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)
        pkg_dir = tmp_path / "pkg" / "src"
        cli_dir = pkg_dir / "cli"
        cli_dir.mkdir(parents=True)
        (pkg_dir / "flake.nix").write_text("{ }")
        (pkg_dir / "flake.lock").write_text("{}")
        (pkg_dir / "entrypoint.py").write_text("")
        (cli_dir / "__init__.py").write_text("# a")

        storage = tmp_path / "storage"
        build_root = storage / "nix-build-root"
        monkeypatch.setattr("cli.run_cmd.GLOBAL_STORAGE", storage)

        import cli

        original_file = cli.__file__
        try:
            cli.__file__ = str(cli_dir / "__init__.py")
            _resolve_repo_root()
            for tag in ("# b", "# c"):
                os.utime(
                    pkg_dir / "flake.nix",
                    ((build_root / "flake.nix").stat().st_mtime + 10,) * 2,
                )
                (cli_dir / "__init__.py").write_text(tag)
                _resolve_repo_root()
            asides = [
                p.name
                for p in storage.iterdir()
                if p.is_dir() and p.name.startswith("nix-build-root.old.")
            ]
            assert len(asides) == 2, "two repopulates must mint two asides"
            assert len(set(asides)) == 2, "aside names must be unique (uuid)"
        finally:
            cli.__file__ = original_file

    def test_user_config_repo_path(self, tmp_path, monkeypatch):
        """When user config has repo_path, use it."""
        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)

        import cli

        original_file = cli.__file__
        try:
            fake_dir = tmp_path / "no-flake" / "src" / "cli"
            fake_dir.mkdir(parents=True)
            (fake_dir / "__init__.py").write_text("")
            cli.__file__ = str(fake_dir / "__init__.py")

            user_config = tmp_path / "config.jsonc"
            repo_dir = tmp_path / "repo"
            repo_dir.mkdir()
            (repo_dir / "flake.nix").write_text("{ }")
            user_config.write_text(json.dumps({"repo_path": str(repo_dir)}))
            monkeypatch.setattr("cli.run_cmd.USER_CONFIG_PATH", user_config)

            result = _resolve_repo_root()
            assert result == repo_dir.resolve()
        finally:
            cli.__file__ = original_file

    def test_fails_when_nothing_found(self, tmp_path, monkeypatch):
        """When no repo root is found, exits with error."""
        from cli import _resolve_repo_root

        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)

        import cli

        original_file = cli.__file__
        try:
            fake_dir = tmp_path / "no-flake" / "src" / "cli"
            fake_dir.mkdir(parents=True)
            (fake_dir / "__init__.py").write_text("")
            cli.__file__ = str(fake_dir / "__init__.py")
            monkeypatch.setattr("cli.run_cmd.USER_CONFIG_PATH", tmp_path / "no-config")

            with pytest.raises((SystemExit, RuntimeError)):
                _resolve_repo_root()
        finally:
            cli.__file__ = original_file


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _validate_config missing edges
# ═══════════════════════════════════════════════════════════════════════════════


class TestValidateConfigEdges:
    """Cover _validate_config branches not yet tested."""

    def _validate(self, config, workspace=None):
        from cli import _validate_config

        return _validate_config(config, workspace=workspace or Path("/tmp/test"))

    def test_packages_object_with_nixpkgs_and_version_conflict(self):
        errors, _ = self._validate(
            {
                "packages": [
                    {
                        "name": "pkg",
                        "nixpkgs": "abc123",
                        "version": "1.0",
                        "url": "x",
                        "hash": "y",
                    }
                ]
            }
        )
        assert any("not both" in e for e in errors)

    def test_packages_object_missing_name(self):
        errors, _ = self._validate({"packages": [{"nixpkgs": "abc123"}]})
        assert any("name" in e for e in errors)

    def test_packages_object_version_without_required_fields(self):
        errors, _ = self._validate(
            {"packages": [{"name": "pkg", "version": 123, "url": 456, "hash": 789}]}
        )
        assert any("expected a string" in e for e in errors)

    def test_packages_object_no_pinning(self):
        errors, _ = self._validate({"packages": [{"name": "pkg"}]})
        assert any("nixpkgs" in e or "version" in e for e in errors)

    def test_mounts_non_string(self):
        errors, _ = self._validate({"mounts": [123]})
        assert any("expected a string" in e for e in errors)

    def test_mounts_host_not_found_warning(self):
        _, warnings = self._validate({"mounts": ["/nonexistent/path/xyz"]})
        assert any("does not exist" in w for w in warnings)

    def test_network_not_dict(self):
        errors, _ = self._validate({"network": "bad"})
        assert any("expected an object" in e for e in errors)

    def test_network_ports_not_list(self):
        errors, _ = self._validate({"network": {"ports": "bad"}})
        assert any("expected a list" in e for e in errors)

    def test_network_forward_host_ports_not_list(self):
        errors, _ = self._validate({"network": {"forward_host_ports": "bad"}})
        assert any("expected a list" in e for e in errors)

    def test_network_host_mode_warnings(self):
        _, warnings = self._validate(
            {
                "network": {
                    "mode": "host",
                    "ports": ["8080:8080"],
                    "forward_host_ports": [5432],
                }
            }
        )
        assert any("ignored" in w for w in warnings)

    def test_security_not_dict(self):
        errors, _ = self._validate({"security": "bad"})
        assert any("expected an object" in e for e in errors)

    def test_security_blocked_tools_not_list(self):
        errors, _ = self._validate({"security": {"blocked_tools": "bad"}})
        assert any("expected a list" in e for e in errors)

    def test_security_blocked_tool_bad_type(self):
        errors, _ = self._validate({"security": {"blocked_tools": [123]}})
        assert any("expected a string or object" in e for e in errors)

    def test_security_blocked_tool_missing_name(self):
        errors, _ = self._validate({"security": {"blocked_tools": [{"message": "hi"}]}})
        assert any("name" in e for e in errors)

    def test_security_blocked_tool_bad_message_type(self):
        errors, _ = self._validate(
            {"security": {"blocked_tools": [{"name": "x", "message": 123}]}}
        )
        assert any("expected a string" in e for e in errors)

    def test_mise_tools_not_dict(self):
        errors, _ = self._validate({"mise_tools": "bad"})
        assert any("expected an object" in e for e in errors)

    def test_mise_tools_bad_value(self):
        errors, _ = self._validate({"mise_tools": {"node": 123}})
        assert any("expected a version string" in e for e in errors)

    def test_lsp_servers_not_dict(self):
        errors, _ = self._validate({"lsp_servers": "bad"})
        assert any("expected an object" in e for e in errors)

    def test_lsp_server_not_object(self):
        errors, _ = self._validate({"lsp_servers": {"python": "bad"}})
        assert any("expected an object" in e for e in errors)

    def test_lsp_server_missing_command(self):
        errors, _ = self._validate(
            {"lsp_servers": {"python": {"fileExtensions": {".py": "python"}}}}
        )
        assert any("command" in e for e in errors)

    def test_lsp_server_bad_file_extensions(self):
        errors, _ = self._validate(
            {"lsp_servers": {"python": {"command": "pyright", "fileExtensions": "bad"}}}
        )
        assert any("fileExtensions" in e for e in errors)

    def test_lsp_server_bad_ext_values(self):
        errors, _ = self._validate(
            {
                "lsp_servers": {
                    "python": {"command": "pyright", "fileExtensions": {".py": 123}}
                }
            }
        )
        assert any("strings" in e for e in errors)

    def test_mcp_presets_not_list(self):
        errors, _ = self._validate({"mcp_presets": "bad"})
        assert any("expected an array" in e for e in errors)

    def test_mcp_presets_non_string(self):
        errors, _ = self._validate({"mcp_presets": [123]})
        assert any("expected a string" in e for e in errors)

    def test_mcp_servers_not_dict(self):
        errors, _ = self._validate({"mcp_servers": "bad"})
        assert any("expected an object" in e for e in errors)

    def test_mcp_servers_bad_entry(self):
        errors, _ = self._validate({"mcp_servers": {"foo": "bar"}})
        assert any("expected an object or null" in e for e in errors)

    def test_mcp_server_missing_command(self):
        errors, _ = self._validate({"mcp_servers": {"foo": {"args": []}}})
        assert any("command" in e for e in errors)

    def test_devices_not_list(self):
        errors, _ = self._validate({"devices": "bad"})
        assert any("expected a list" in e for e in errors)

    def test_device_bad_type(self):
        errors, _ = self._validate({"devices": [123]})
        assert any("expected a string or object" in e for e in errors)

    def test_device_both_usb_and_cgroup(self):
        errors, _ = self._validate(
            {"devices": [{"usb": "0bda:2838", "cgroup_rule": "c 189:* rwm"}]}
        )
        assert any("exactly one" in e for e in errors)

    def test_device_usb_bad_format(self):
        errors, _ = self._validate({"devices": [{"usb": "bad-format"}]})
        assert any("hex format" in e for e in errors)

    def test_device_usb_bad_type(self):
        errors, _ = self._validate({"devices": [{"usb": 123}]})
        assert any("expected a string" in e for e in errors)

    def test_device_cgroup_bad_type(self):
        errors, _ = self._validate({"devices": [{"cgroup_rule": 123}]})
        assert any("expected a string" in e for e in errors)

    def test_device_description_bad_type(self):
        errors, _ = self._validate(
            {"devices": [{"usb": "0bda:2838", "description": 123}]}
        )
        assert any("description" in e for e in errors)

    def test_repo_path_bad_type(self):
        errors, _ = self._validate({"repo_path": 123})
        assert any("expected a string" in e for e in errors)

    def test_validate_port_number_invalid(self):
        from cli import _validate_port_number

        errors = []
        _validate_port_number("abc", "test", errors)
        assert len(errors) == 1

    def test_validate_publish_port_three_parts(self):
        from cli import _validate_publish_port

        errors = []
        _validate_publish_port("127.0.0.1:8080:9090", "test", errors)
        assert len(errors) == 0

    def test_validate_publish_port_with_protocol(self):
        from cli import _validate_publish_port

        errors = []
        _validate_publish_port("8080:9090/udp", "test", errors)
        assert len(errors) == 0

    def test_validate_publish_port_bad_protocol(self):
        from cli import _validate_publish_port

        errors = []
        _validate_publish_port("8080:9090/icmp", "test", errors)
        assert any("protocol" in e for e in errors)


# ═══════════════════════════════════════════════════════════════════════════════
# Test: check() command thorough happy path
# ═══════════════════════════════════════════════════════════════════════════════


class TestCheckCommandDetailed:
    """More thorough check command tests to cover inner branches."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_happy_path_all_green(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        monkeypatch.setattr("cli.GLOBAL_HOME", tmp_path / "home")
        monkeypatch.setattr("cli.GLOBAL_MISE", tmp_path / "mise")
        monkeypatch.setattr("cli.CONTAINER_DIR", tmp_path / "containers")
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        monkeypatch.setattr("cli.BUILD_DIR", tmp_path / "build")
        monkeypatch.setattr("cli.USER_CONFIG_PATH", tmp_path / "user-config.jsonc")
        for d in ("home", "mise", "containers", "agents", "build"):
            (tmp_path / d).mkdir()

        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )

        def smart_run(cmd, **kwargs):
            if isinstance(cmd, list):
                prog = cmd[0]
                if prog == "podman":
                    if "--version" in cmd:
                        return MagicMock(returncode=0, stdout="podman version 4.9.0\n")
                    if "images" in cmd:
                        return MagicMock(
                            returncode=0, stdout="yolo-jail:latest (1.2 GB)\n"
                        )
                    if "ps" in cmd:
                        return MagicMock(returncode=0, stdout="")
                    return MagicMock(returncode=0, stdout="")
                if prog == "nix":
                    return MagicMock(returncode=0, stdout="nix (Nix) 2.18.1\n")
                if prog == sys.executable:
                    return MagicMock(returncode=0, stdout="ok\n", stderr="")
            return MagicMock(returncode=0, stdout="", stderr="")

        mock_run.side_effect = smart_run
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"security": {"blocked_tools": ["grep"]}}'
        )

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "passed" in result.output
        assert result.exit_code == 0

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_with_warnings(self, mock_which, mock_run, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        monkeypatch.setattr("cli.GLOBAL_HOME", tmp_path / "home")
        monkeypatch.setattr("cli.GLOBAL_MISE", tmp_path / "mise")
        monkeypatch.setattr("cli.CONTAINER_DIR", tmp_path / "containers")
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        monkeypatch.setattr("cli.BUILD_DIR", tmp_path / "build")
        monkeypatch.setattr("cli.USER_CONFIG_PATH", tmp_path / "user-config.jsonc")

        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )

        def smart_run(cmd, **kwargs):
            if isinstance(cmd, list):
                prog = cmd[0]
                if prog == "podman":
                    if "--version" in cmd:
                        return MagicMock(returncode=0, stdout="podman version 4.9.0\n")
                    if "images" in cmd:
                        return MagicMock(returncode=0, stdout="")
                    if "ps" in cmd:
                        return MagicMock(returncode=0, stdout="")
                    return MagicMock(returncode=0, stdout="")
                if prog == "nix":
                    return MagicMock(returncode=0, stdout="nix (Nix) 2.18.1\n")
                if prog == sys.executable:
                    return MagicMock(returncode=0, stdout="ok\n", stderr="")
            return MagicMock(returncode=0, stdout="", stderr="")

        mock_run.side_effect = smart_run

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "passed" in result.output or "warning" in result.output.lower()

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_nix_not_found(self, mock_which, mock_run, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.side_effect = lambda x: f"/usr/bin/{x}" if x == "podman" else None
        mock_run.return_value = MagicMock(returncode=0, stdout="podman version 4.9.0\n")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "nix not found" in result.output or result.exit_code != 0

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_runtime_exception(self, mock_which, mock_run, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
        mock_which.side_effect = lambda x: (
            f"/usr/bin/{x}" if x in ("podman", "nix") else None
        )

        def run_with_exception(cmd, **kwargs):
            if isinstance(cmd, list) and cmd[0] == "podman" and "--version" in cmd:
                raise subprocess.TimeoutExpired(cmd, 5)
            if isinstance(cmd, list) and cmd[0] == "nix":
                return MagicMock(returncode=0, stdout="nix 2.18.1\n")
            return MagicMock(returncode=0, stdout="", stderr="")

        mock_run.side_effect = run_with_exception

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "not working" in result.output or result.exit_code != 0


# ═══════════════════════════════════════════════════════════════════════════════
# Test: main() function
# ═══════════════════════════════════════════════════════════════════════════════


class TestMainFunction:
    """Cover the main() entry point."""

    @patch("cli.app")
    @patch("cli._tmux_setup_jail_pane", return_value=None)
    @patch("cli._kitty_setup_jail_tab", return_value=None)
    def test_main_no_tmux_no_kitty(self, mock_kitty, mock_tmux, mock_app, monkeypatch):
        monkeypatch.delenv("KITTY_PID", raising=False)
        monkeypatch.delenv("TMUX", raising=False)
        monkeypatch.setattr("sys.argv", ["yolo"])
        from cli import main

        main()
        mock_app.assert_called_once()

    @patch("cli.app")
    @patch("cli._kitty_setup_jail_tab")
    def test_main_kitty_mode(self, mock_kitty, mock_app, monkeypatch):
        monkeypatch.setenv("KITTY_PID", "1234")
        monkeypatch.delenv("TMUX", raising=False)
        mock_kitty.return_value = lambda: None
        monkeypatch.setattr("sys.argv", ["yolo"])
        from cli import main

        main()
        mock_kitty.assert_called_once()

    @patch("cli.app")
    @patch("cli._tmux_setup_jail_pane")
    def test_main_tmux_mode(self, mock_tmux, mock_app, monkeypatch):
        monkeypatch.setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
        monkeypatch.delenv("KITTY_PID", raising=False)
        mock_tmux.return_value = lambda: None
        monkeypatch.setattr("sys.argv", ["yolo"])
        from cli import main

        main()
        mock_tmux.assert_called_once()

    @patch("cli.app")
    @patch("cli._tmux_setup_jail_pane", return_value=None)
    def test_main_argv_rewrite_inserts_run(self, mock_tmux, mock_app, monkeypatch):
        monkeypatch.delenv("KITTY_PID", raising=False)
        monkeypatch.delenv("TMUX", raising=False)
        monkeypatch.setattr("sys.argv", ["yolo", "--", "echo", "hello"])
        from cli import main

        main()
        assert "run" in sys.argv

    @patch("cli.app")
    @patch("cli._tmux_setup_jail_pane", return_value=None)
    def test_main_argv_no_rewrite_for_subcommand(
        self, mock_tmux, mock_app, monkeypatch
    ):
        monkeypatch.delenv("KITTY_PID", raising=False)
        monkeypatch.delenv("TMUX", raising=False)
        monkeypatch.setattr("sys.argv", ["yolo", "check", "--no-build"])
        from cli import main

        main()
        assert sys.argv == ["yolo", "check", "--no-build"]


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _runtime() missing paths
# ═══════════════════════════════════════════════════════════════════════════════


class TestRuntimeNoRuntime:
    """Cover _runtime when no runtime is found."""

    @patch("shutil.which", return_value=None)
    def test_exits_when_no_runtime(self, mock_which, monkeypatch):
        from cli import _runtime

        monkeypatch.delenv("YOLO_RUNTIME", raising=False)
        with pytest.raises((SystemExit, RuntimeError)):
            _runtime({})

    @patch("shutil.which")
    def test_runtime_from_env(self, mock_which, monkeypatch):
        from cli import _runtime

        monkeypatch.setenv("YOLO_RUNTIME", "podman")
        mock_which.return_value = "/usr/bin/podman"
        assert _runtime({}) == "podman"

    @patch("shutil.which")
    def test_runtime_from_config(self, mock_which, monkeypatch):
        from cli import _runtime

        monkeypatch.delenv("YOLO_RUNTIME", raising=False)
        mock_which.side_effect = lambda x: "/usr/bin/podman" if x == "podman" else None
        assert _runtime({"runtime": "podman"}) == "podman"


# ═══════════════════════════════════════════════════════════════════════════════
# Test: check() additional branch coverage
# ═══════════════════════════════════════════════════════════════════════════════


def _check_monkeypatch(monkeypatch, tmp_path, *, create_dirs=True):
    """Common monkeypatching for check command tests."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
    monkeypatch.setattr("cli.GLOBAL_HOME", tmp_path / "home")
    monkeypatch.setattr("cli.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.CONTAINER_DIR", tmp_path / "containers")
    monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
    monkeypatch.setattr("cli.BUILD_DIR", tmp_path / "build")
    monkeypatch.setattr("cli.USER_CONFIG_PATH", tmp_path / "user-config.jsonc")
    # The check command body lives in cli.check_cmd; patch its module-local
    # bindings too so the redirection actually reaches the call sites.
    monkeypatch.setattr("cli.check_cmd.GLOBAL_HOME", tmp_path / "home")
    monkeypatch.setattr("cli.check_cmd.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.check_cmd.CONTAINER_DIR", tmp_path / "containers")
    monkeypatch.setattr("cli.check_cmd.AGENTS_DIR", tmp_path / "agents")
    monkeypatch.setattr("cli.check_cmd.BUILD_DIR", tmp_path / "build")
    monkeypatch.setattr(
        "cli.check_cmd.USER_CONFIG_PATH", tmp_path / "user-config.jsonc"
    )
    # cli.storage bindings — keep ensure_global_storage's layout migration
    # off the real host dirs, and pre-stamp its marker so it's a no-op.
    monkeypatch.setattr("cli.storage.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.storage.GLOBAL_STORAGE", tmp_path / "storage")
    (tmp_path / "storage").mkdir(exist_ok=True)
    (tmp_path / "storage" / "layout-version").write_text("2\n")
    monkeypatch.setattr("cli.runtime._runtime_is_connectable", lambda rt: True)
    if create_dirs:
        for d in ("home", "mise", "containers", "agents", "build"):
            (tmp_path / d).mkdir()


def _mock_runtimes(mock_which, runtimes=("podman", "nix")):
    """Configure shutil.which to find specified runtimes."""
    mock_which.side_effect = lambda x: f"/usr/bin/{x}" if x in runtimes else None


def _default_smart_run(cmd, **kwargs):
    """Default subprocess.run mock that handles common runtime commands."""
    if isinstance(cmd, list):
        prog = cmd[0]
        if prog == "podman":
            if "--version" in cmd:
                return MagicMock(returncode=0, stdout="podman version 4.9.0\n")
            if "images" in cmd:
                return MagicMock(returncode=0, stdout="yolo-jail:latest (1.2 GB)\n")
            if "ps" in cmd:
                return MagicMock(returncode=0, stdout="")
            return MagicMock(returncode=0, stdout="")
        if prog == "nix":
            return MagicMock(returncode=0, stdout="nix (Nix) 2.18.1\n")
        if prog == sys.executable:
            return MagicMock(returncode=0, stdout="ok\n", stderr="")
    return MagicMock(returncode=0, stdout="", stderr="")


class TestCheckGlobalStorageWarnings:
    """Test check() warns about missing global storage directories."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_warns_missing_dirs(self, mock_which, mock_run, tmp_path, monkeypatch):
        _check_monkeypatch(monkeypatch, tmp_path, create_dirs=False)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "missing" in result.output.lower() or "warning" in result.output.lower()


class TestCheckUserConfigError:
    """Test check() handles user config parse errors."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_invalid_user_config_exits(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run

        (tmp_path / "user-config.jsonc").write_text("{invalid json!!")
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert result.exit_code != 0 or "failed" in result.output.lower()

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_user_config_parsed_ok(self, mock_which, mock_run, tmp_path, monkeypatch):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run

        (tmp_path / "user-config.jsonc").write_text('{"runtime": "podman"}')
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "user config" in result.output.lower()


class TestCheckFlakeNotFound:
    """Test check() warns when flake.nix is not found."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_flake_missing_warns(self, mock_which, mock_run, tmp_path, monkeypatch):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        # Point to a directory with no flake.nix
        fake_repo = tmp_path / "fake-repo"
        fake_repo.mkdir()
        monkeypatch.setenv("YOLO_REPO_ROOT", str(fake_repo))

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "flake.nix" in result.output.lower()


class TestCheckRuntimeError:
    """Test check() handles runtime detection errors in merged config."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_runtime_error_in_config(self, mock_which, mock_run, tmp_path, monkeypatch):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which, runtimes=("nix",))  # No container runtime
        mock_run.side_effect = _default_smart_run
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        monkeypatch.delenv("YOLO_RUNTIME", raising=False)

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert result.exit_code != 0 or "runtime" in result.output.lower()


class TestCheckRepoPathInWorkspace:
    """Test check() warns when repo_path is in workspace config."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_repo_path_in_workspace_warns(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run
        (tmp_path / "yolo-jail.jsonc").write_text('{"repo_path": "/some/path"}')

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "repo_path" in result.output or "warning" in result.output.lower()


class TestCheckEntrypointPreflight:
    """Test check() entrypoint preflight section."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_entrypoint_preflight_fails(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)

        def run_with_entrypoint_fail(cmd, **kwargs):
            if isinstance(cmd, list) and cmd[0] == sys.executable:
                return MagicMock(returncode=1, stdout="", stderr="crash")
            return _default_smart_run(cmd, **kwargs)

        mock_run.side_effect = run_with_entrypoint_fail
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        # Entrypoint preflight failure should be reported
        assert "preflight" in result.output.lower() or "failed" in result.output.lower()


class TestCheckNixBuildSection:
    """Test check() nix build section."""

    @patch("subprocess.run")
    @patch("shutil.which")
    @patch("cli.check_cmd._build_image_store_path")
    def test_nix_build_success(
        self, mock_build, mock_which, mock_run, tmp_path, monkeypatch
    ):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run
        mock_build.return_value = ("/nix/store/abc-result", [])
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--build"])
        assert (
            "nix build succeeded" in result.output.lower() or "passed" in result.output
        )

    @patch("subprocess.run")
    @patch("shutil.which")
    @patch("cli.check_cmd._build_image_store_path")
    def test_nix_build_failure(
        self, mock_build, mock_which, mock_run, tmp_path, monkeypatch
    ):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.side_effect = _default_smart_run
        mock_build.return_value = (None, ["error: undefined variable"])
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--build"])
        assert "nix build failed" in result.output.lower() or result.exit_code != 0


class TestCheckImageAndContainers:
    """Test check() image and running container sections."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_image_check_exception(self, mock_which, mock_run, tmp_path, monkeypatch):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)

        def run_with_image_error(cmd, **kwargs):
            if isinstance(cmd, list):
                prog = cmd[0]
                if prog == "podman":
                    if "--version" in cmd:
                        return MagicMock(returncode=0, stdout="podman version 4.9.0\n")
                    if "images" in cmd:
                        raise subprocess.TimeoutExpired(cmd, 10)
                    if "ps" in cmd:
                        raise subprocess.TimeoutExpired(cmd, 5)
                if prog == "nix":
                    return MagicMock(returncode=0, stdout="nix (Nix) 2.18.1\n")
                if prog == sys.executable:
                    return MagicMock(returncode=0, stdout="ok\n", stderr="")
            return MagicMock(returncode=0, stdout="", stderr="")

        mock_run.side_effect = run_with_image_error
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert (
            "could not check" in result.output.lower()
            or "warning" in result.output.lower()
        )

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_running_jails_found(self, mock_which, mock_run, tmp_path, monkeypatch):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)

        def run_with_jails(cmd, **kwargs):
            if isinstance(cmd, list):
                prog = cmd[0]
                if prog == "podman":
                    if "--version" in cmd:
                        return MagicMock(returncode=0, stdout="podman version 4.9.0\n")
                    if "images" in cmd:
                        return MagicMock(
                            returncode=0, stdout="yolo-jail:latest (1.2 GB)\n"
                        )
                    if "ps" in cmd:
                        return MagicMock(
                            returncode=0, stdout="yolo-abc123\nyolo-def456\n"
                        )
                if prog == "nix":
                    return MagicMock(returncode=0, stdout="nix (Nix) 2.18.1\n")
                if prog == sys.executable:
                    return MagicMock(returncode=0, stdout="ok\n", stderr="")
            return MagicMock(returncode=0, stdout="", stderr="")

        mock_run.side_effect = run_with_jails
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert "2 jail" in result.output or "jail" in result.output.lower()


class TestCheckSummaryWithFailures:
    """Test check() summary line with failures and warnings combined."""

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_check_summary_failed_plus_warnings(
        self, mock_which, mock_run, tmp_path, monkeypatch
    ):
        _check_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which, runtimes=("nix",))  # No container runtime
        monkeypatch.delenv("YOLO_RUNTIME", raising=False)
        mock_run.side_effect = _default_smart_run
        # Valid config but storage dirs missing → warnings, no runtime → fail
        _check_monkeypatch(monkeypatch, tmp_path, create_dirs=False)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--no-build"])
        assert result.exit_code != 0


# ═══════════════════════════════════════════════════════════════════════════════
# Test: doctor — per-jail broker relay probe (relay unification, Round 2)
# ═══════════════════════════════════════════════════════════════════════════════


def _serve_unix(sock_path, handler):
    """Bind an AF_UNIX listener at ``sock_path`` and run ``handler(conn)``
    per accepted connection on a daemon thread.  Returns the server
    socket; closing it stops the accept loop."""
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(str(sock_path))
    srv.listen(8)

    def _loop():
        while True:
            try:
                conn, _ = srv.accept()
            except OSError:
                return
            handler(conn)

    threading.Thread(target=_loop, daemon=True).start()
    return srv


def _pong_handler(conn):
    """Speak just enough of the loophole frame protocol to answer
    ``_broker_ping``: read one length-prefixed request, reply with a
    pong on stream 0 and an exit frame on stream 2.  The probe's first
    connection is a bare connect+close, so EOF anywhere is tolerated."""
    try:
        hdr = b""
        while len(hdr) < 4:
            chunk = conn.recv(4 - len(hdr))
            if not chunk:
                return
            hdr += chunk
        (ln,) = struct.unpack(">I", hdr)
        body = b""
        while len(body) < ln:
            chunk = conn.recv(ln - len(body))
            if not chunk:
                return
            body += chunk
        payload = b'{"pong": true}'
        conn.sendall(struct.pack(">BI", 0, len(payload)) + payload)
        conn.sendall(struct.pack(">BI", 2, 1) + b"0")
    except OSError:
        pass
    finally:
        try:
            conn.close()
        except OSError:
            pass


class TestDoctorBrokerRelayProbe:
    """Per-jail relay probe (relay unification, Round 2).

    A dead relay reproduces exactly the symptom the token-logout doc
    exists for: one jail 502s while ``yolo doctor`` grades the singleton
    broker healthy.  So doctor must probe each running jail's relay
    socket end-to-end and NAME THE FAILING LAYER — relay (socket
    missing / connect refused) vs broker (relay accepts but no pong
    comes back through it)."""

    def _events(self):
        events = []
        return (
            events,
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("warn", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
        )

    def test_healthy_relay_reports_ok(self, sock_dir):
        sock_path = sock_dir / "claude-oauth-broker.sock"
        srv = _serve_unix(sock_path, _pong_handler)
        try:
            events, ok, warn, fail = self._events()
            cli.check_cmd._check_broker_relay(
                ok, fail, "loophole claude-oauth-broker @ yolo-ws-abc12345", sock_path
            )
        finally:
            srv.close()
        assert [k for k, _ in events] == ["ok"]
        assert "relay ok" in events[0][1]

    def test_missing_socket_fails_naming_jail_and_relay(self, tmp_path):
        events, ok, warn, fail = self._events()
        cli.check_cmd._check_broker_relay(
            ok,
            fail,
            "loophole claude-oauth-broker @ yolo-ws-abc12345",
            tmp_path / "claude-oauth-broker.sock",
        )
        assert [k for k, _ in events] == ["fail"]
        assert "yolo-ws-abc12345" in events[0][1]
        assert "relay" in events[0][1]

    def test_stale_socket_connect_refused_is_relay_layer(self, sock_dir):
        # Bind then close: the socket FILE stays behind but nothing
        # listens — the signature of a relay process that died.
        sock_path = sock_dir / "claude-oauth-broker.sock"
        srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        srv.bind(str(sock_path))
        srv.listen(1)
        srv.close()
        events, ok, warn, fail = self._events()
        cli.check_cmd._check_broker_relay(
            ok, fail, "loophole claude-oauth-broker @ yolo-ws-abc12345", sock_path
        )
        assert [k for k, _ in events] == ["fail"]
        assert "relay socket dead" in events[0][1]

    def test_relay_up_broker_down_is_broker_layer(self, sock_dir):
        # Relay-alike that accepts and immediately closes — what the real
        # relay does to its client when the singleton is unreachable.
        sock_path = sock_dir / "claude-oauth-broker.sock"
        srv = _serve_unix(sock_path, lambda conn: conn.close())
        try:
            events, ok, warn, fail = self._events()
            cli.check_cmd._check_broker_relay(
                ok, fail, "loophole claude-oauth-broker @ yolo-ws-abc12345", sock_path
            )
        finally:
            srv.close()
        assert [k for k, _ in events] == ["fail"]
        assert "broker unreachable" in events[0][1]

    # ── the caller: _check_host_service_liveness routes the broker to
    #    the relay probe and must never false-pass on enumeration errors ──

    def _liveness_setup(self, monkeypatch, sockets_base, *, ps_stdout, ps_rc=0):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        lp = MagicMock()
        lp.name = cli.BROKER_LOOPHOLE_NAME
        lp.enabled = True
        lp.requirements_met = True
        lp.host_daemon = MagicMock()
        monkeypatch.setattr(
            cli._loopholes, "validate_loopholes", lambda: [(None, lp, None)]
        )
        monkeypatch.setattr(
            "cli.check_cmd._detect_runtime_for_listing", lambda: "podman"
        )
        # The probe lists jails via runtime.list_running_jail_names (imported
        # into check_cmd); stub it to a (names, error) tuple.  A non-zero rc
        # models a listing failure that must warn, not read as "no jails".
        names = [n.strip() for n in ps_stdout.splitlines() if n.strip()]
        err = "boom" if ps_rc != 0 else None
        monkeypatch.setattr(
            "cli.check_cmd.list_running_jail_names",
            lambda *a, **kw: ([], err) if err else (names, None),
        )
        sockets_dir = sockets_base / "sockets"
        sockets_dir.mkdir()
        monkeypatch.setattr(
            "cli.check_cmd._host_service_sockets_dir", lambda _cname: sockets_dir
        )
        return sockets_dir

    def test_liveness_probes_broker_relay_per_jail(self, monkeypatch, sock_dir):
        """The old broker skip is gone: a running jail whose relay socket
        answers gets an ok line naming the jail; a jail without the
        socket gets a relay-layer fail."""
        sockets_dir = self._liveness_setup(
            monkeypatch, sock_dir, ps_stdout="yolo-ws-abc12345\n"
        )
        srv = _serve_unix(sockets_dir / "claude-oauth-broker.sock", _pong_handler)
        try:
            events, ok, warn, fail = self._events()
            cli._check_host_service_liveness(ok, warn, fail)
        finally:
            srv.close()
        oks = [m for k, m in events if k == "ok"]
        assert any("yolo-ws-abc12345" in m and "relay ok" in m for m in oks), events
        assert not [m for k, m in events if k == "fail"]

    def test_liveness_fails_when_relay_socket_missing(self, monkeypatch, tmp_path):
        self._liveness_setup(monkeypatch, tmp_path, ps_stdout="yolo-ws-abc12345\n")
        events, ok, warn, fail = self._events()
        cli._check_host_service_liveness(ok, warn, fail)
        fails = [m for k, m in events if k == "fail"]
        assert any("yolo-ws-abc12345" in m and "relay" in m for m in fails), events

    def test_liveness_warns_when_jail_listing_fails(self, monkeypatch, tmp_path):
        """`podman ps` failing must warn — NOT read as "no jails running",
        which would false-pass every per-jail relay probe."""
        self._liveness_setup(monkeypatch, tmp_path, ps_stdout="", ps_rc=1)
        events, ok, warn, fail = self._events()
        cli._check_host_service_liveness(ok, warn, fail)
        assert [k for k, _ in events] == ["warn"], events
        assert "could not list running jails" in events[0][1]


# ═══════════════════════════════════════════════════════════════════════════════
# Test: run() command — early setup paths
# ═══════════════════════════════════════════════════════════════════════════════


def _run_monkeypatch(monkeypatch, tmp_path, *, broker_socket_exists=True):
    """Common monkeypatching for run command tests."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
    monkeypatch.setattr("cli.GLOBAL_HOME", tmp_path / "home")
    monkeypatch.setattr("cli.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.GLOBAL_STORAGE", tmp_path / "storage")
    monkeypatch.setattr("cli.CONTAINER_DIR", tmp_path / "containers")
    monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
    monkeypatch.setattr("cli.BUILD_DIR", tmp_path / "build")
    monkeypatch.setattr("cli.USER_CONFIG_PATH", tmp_path / "user-config.jsonc")
    # The run command body lives in cli.run_cmd; patch its module-local
    # bindings too so the redirection actually reaches the call sites.
    monkeypatch.setattr("cli.run_cmd.GLOBAL_HOME", tmp_path / "home")
    monkeypatch.setattr("cli.run_cmd.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.run_cmd.GLOBAL_STORAGE", tmp_path / "storage")
    monkeypatch.setattr("cli.run_cmd.CONTAINER_DIR", tmp_path / "containers")
    monkeypatch.setattr("cli.run_cmd.AGENTS_DIR", tmp_path / "agents")
    monkeypatch.setattr("cli.run_cmd.BUILD_DIR", tmp_path / "build")
    monkeypatch.setattr("cli.run_cmd.USER_CONFIG_PATH", tmp_path / "user-config.jsonc")
    # cli.storage holds its own bindings: _jail_mise_store_dir reads
    # GLOBAL_MISE and the layout migration reads GLOBAL_STORAGE there.
    monkeypatch.setattr("cli.storage.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.storage.GLOBAL_STORAGE", tmp_path / "storage")
    monkeypatch.setattr("cli.runtime._runtime_is_connectable", lambda rt: True)
    monkeypatch.setattr("time.sleep", lambda _: None)
    # The Claude OAuth broker singleton + loophole daemons run real
    # subprocesses and worker threads; ``stop_loopholes`` then blocks ~6s
    # joining a thread parked in ``socket.accept()``.  None of the run-
    # command unit tests below care about broker plumbing — they assert
    # on the constructed podman/docker argv.  Stub the lot out so each
    # test runs in milliseconds instead of seconds.
    fake_sock = tmp_path / "broker.sock"
    monkeypatch.setattr("cli.run_cmd._broker_ensure", lambda: fake_sock)
    monkeypatch.setattr("cli._broker_spawn", lambda: fake_sock)
    monkeypatch.setattr("cli.run_cmd.start_loopholes", lambda *a, **kw: [])
    monkeypatch.setattr("cli.run_cmd.stop_loopholes", lambda *a, **kw: None)
    # run_cmd binds the broker/relay names by from-import, so patch the
    # run_cmd bindings — patching the cli re-exports would leave the real
    # ones reachable.  Point the singleton socket at a tmp path so the
    # broker block never keys off the dev machine's real
    # /tmp/yolo-claude-oauth-broker.sock; it EXISTS by default so the
    # block is exercised deterministically.  The relay ensure is a
    # MagicMock so no supervised relay process leaks out of a unit test
    # and wiring tests can assert on the calls.
    if broker_socket_exists:
        fake_sock.touch()
    monkeypatch.setattr("cli.run_cmd.BROKER_SINGLETON_SOCKET", fake_sock)
    monkeypatch.setattr("cli.run_cmd._relay_ensure", MagicMock())
    # The orphan-relay sweep enumerates containers through the SAME
    # mocked subprocess.run these tests install (→ live = empty set) —
    # unstubbed it would reap REAL relays on the dev machine.
    monkeypatch.setattr("cli.run_cmd._relay_reap_orphans", MagicMock(return_value=[]))
    for d in (
        "home",
        "mise",
        "containers",
        "agents",
        "build",
        "storage",
        "storage/locks",
    ):
        (tmp_path / d).mkdir(parents=True, exist_ok=True)
    # Pre-stamp the storage layout marker so the one-time migration
    # (which scans the real host mise dir) stays a no-op in unit tests.
    (tmp_path / "storage" / "layout-version").write_text("2\n")


def _launch_argv(tmp_path, monkeypatch, *, config_text="{}"):
    """Invoke ``yolo run`` with mocks and return the container argv."""
    _run_monkeypatch(monkeypatch, tmp_path)
    (tmp_path / "yolo-jail.jsonc").write_text(config_text)
    with (
        patch("shutil.which") as mock_which,
        patch("subprocess.check_output", side_effect=FileNotFoundError),
        patch("subprocess.run", return_value=MagicMock(returncode=0, stdout="")),
        patch("cli.run_cmd.find_running_container", return_value=None),
        patch("cli.run_cmd._check_config_changes", return_value=True),
        patch("cli.run_cmd.auto_load_image"),
        patch("subprocess.Popen") as mock_popen,
    ):
        _mock_runtimes(mock_which)
        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 0
        mock_popen.return_value = proc
        result = CliRunner().invoke(app, ["run", "--", "true"])
        assert mock_popen.called, f"launch argv expected; output: {result.output}"
        return [str(a) for a in mock_popen.call_args[0][0]]


class TestRunConfigErrors:
    """Test run() exits on config load/validation errors."""

    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_run_exits_on_invalid_config_json(
        self, mock_which, mock_check, mock_run, tmp_path, monkeypatch
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.return_value = MagicMock(returncode=0)
        mock_check.side_effect = FileNotFoundError
        (tmp_path / "yolo-jail.jsonc").write_text("{invalid json!!")

        runner = CliRunner()
        result = runner.invoke(app, ["run"])
        assert result.exit_code != 0

    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_run_exits_on_validation_error(
        self, mock_which, mock_check, mock_run, tmp_path, monkeypatch
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        mock_run.return_value = MagicMock(returncode=0)
        mock_check.side_effect = FileNotFoundError
        # mcp_presets with string that isn't a valid preset
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"mcp_presets": ["nonexistent-preset"]}'
        )

        runner = CliRunner()
        result = runner.invoke(app, ["run"])
        assert result.exit_code != 0


class TestRunIdentityEnvCollection:
    """Test run() collects git/jj identity env vars."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_collects_git_identity(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")

        def check_output_router(cmd, **kwargs):
            if isinstance(cmd, list):
                if "user.name" in cmd:
                    return b"Test User\n"
                if "user.email" in cmd:
                    return b"test@example.com\n"
            raise FileNotFoundError

        mock_check_output.side_effect = check_output_router

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "echo", "hello"])

        # Verify Popen was called and the runtime command contains identity env
        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            cmd_str = " ".join(str(a) for a in run_cmd)
            assert "YOLO_GIT_NAME=Test User" in cmd_str
            assert "YOLO_GIT_EMAIL=test@example.com" in cmd_str


class TestRunYoloInjection:
    """Test run() injects --yolo for gemini/copilot commands."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_injects_yolo_flag_for_copilot(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "copilot"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            # The final arg is the internal command string
            final_cmd = run_cmd[-1]
            assert "--yolo" in final_cmd
            assert "--no-auto-update" in final_cmd


class TestRunExecIntoExisting:
    """Test run() exec path when container already exists."""

    @patch("cli.run_cmd.find_running_container")
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_exec_into_existing_container(
        self, mock_which, mock_check_output, mock_run, mock_find, tmp_path, monkeypatch
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError
        mock_find.return_value = "abc123"
        mock_run.return_value = MagicMock(returncode=0)

        runner = CliRunner()
        result = runner.invoke(app, ["run", "--", "echo", "hello"])
        assert "Attaching" in result.output

    @patch("cli.run_cmd.find_running_container")
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_exec_runtime_not_found(
        self, mock_which, mock_check_output, mock_run, mock_find, tmp_path, monkeypatch
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError
        mock_find.return_value = "abc123"
        mock_run.side_effect = FileNotFoundError("podman not found")

        runner = CliRunner()
        result = runner.invoke(app, ["run", "--", "echo", "hello"])
        assert result.exit_code != 0


def _invoke_run_with_relay(
    tmp_path, monkeypatch, relay, *, existing=(None, None), broker_socket_exists=True
):
    """Invoke ``yolo run`` with the broker block armed: the (patched)
    singleton socket exists (unless ``broker_socket_exists=False``) and
    ``cli.run_cmd._relay_ensure`` is ``relay``.  ``existing`` feeds
    ``find_running_container`` — one value per expected call (pre-lock
    check, then post-lock re-check).  Returns (CliRunner result, mocked
    subprocess.Popen)."""
    _run_monkeypatch(monkeypatch, tmp_path, broker_socket_exists=broker_socket_exists)
    monkeypatch.setattr("cli.run_cmd._relay_ensure", relay)
    (tmp_path / "yolo-jail.jsonc").write_text("{}")
    with (
        patch("shutil.which") as mock_which,
        patch("subprocess.check_output", side_effect=FileNotFoundError),
        patch("subprocess.run", return_value=MagicMock(returncode=0, stdout="")),
        # Padding covers the post-launch lock-release poll (up to 20 calls).
        patch(
            "cli.run_cmd.find_running_container",
            side_effect=list(existing) + [None] * 25,
        ),
        patch("cli.run_cmd._check_config_changes", return_value=True),
        patch("cli.run_cmd.auto_load_image"),
        patch("subprocess.Popen") as mock_popen,
    ):
        _mock_runtimes(mock_which)
        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 0
        mock_popen.return_value = proc
        result = CliRunner().invoke(app, ["run", "--", "true"])
    return result, mock_popen


class TestRunBrokerRelayWiring:
    """Relay unification wiring in run() (Round 2).

    The singleton broker socket is never bind-mounted into the jail any
    more (a socket-FILE mount pins the inode; a broker restart left
    every running jail holding the dead socket).  Instead a per-jail
    relay — a supervised standalone process — listens inside the
    already-mounted sockets dir, and ``_relay_ensure`` must fire on the
    fresh-run path AND both exec/attach branches (the container outlives
    any single ``yolo`` invocation, so attach must heal a dead relay).
    A relay failure must never block the jail."""

    def test_fresh_run_mounts_no_socket_file_and_keeps_env(self, tmp_path, monkeypatch):
        relay = MagicMock()
        result, mock_popen = _invoke_run_with_relay(tmp_path, monkeypatch, relay)
        assert mock_popen.called, f"launch expected; output: {result.output}"
        argv = [str(a) for a in mock_popen.call_args[0][0]]

        # The Round-2 bug: no `-v` spec may reference the singleton
        # socket file (that mount pinned the inode across broker restarts).
        singleton = str(tmp_path / "broker.sock")
        mounts = [argv[i + 1] for i, a in enumerate(argv) if a == "-v"]
        assert not [m for m in mounts if m.startswith(f"{singleton}:")], mounts

        # The terminator still finds the (relay) socket through the
        # unchanged env wiring + the existing sockets-DIR mount.
        assert (
            "YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET="
            "/run/yolo-services/claude-oauth-broker.sock" in argv
        )
        cname = container_name_for_workspace(tmp_path)
        assert any(
            m == f"{_host_service_sockets_dir(cname)}:/run/yolo-services:rw"
            for m in mounts
        ), mounts

    def test_fresh_run_ensures_relay_for_this_jail(self, tmp_path, monkeypatch):
        relay = MagicMock()
        result, mock_popen = _invoke_run_with_relay(tmp_path, monkeypatch, relay)
        assert mock_popen.called, f"launch expected; output: {result.output}"
        assert relay.call_count == 1
        cname, sockets_dir = relay.call_args[0]
        assert cname == container_name_for_workspace(tmp_path)
        assert sockets_dir == _host_service_sockets_dir(cname)

    def test_fresh_run_relay_failure_is_nonfatal(self, tmp_path, monkeypatch):
        relay = MagicMock(side_effect=RuntimeError("relay spawn boom"))
        result, mock_popen = _invoke_run_with_relay(tmp_path, monkeypatch, relay)
        assert mock_popen.called, "a broken relay must not block the jail launch"
        assert result.exit_code == 0, result.output
        assert "relay not ensured" in result.output

    def test_attach_existing_heals_relay(self, tmp_path, monkeypatch):
        relay = MagicMock()
        result, mock_popen = _invoke_run_with_relay(
            tmp_path, monkeypatch, relay, existing=("abc123",)
        )
        assert "Attaching to existing" in result.output
        # The exec path starts no container — but it must heal a relay
        # whose spawning `yolo run` process died (terminal close, SIGKILL).
        assert relay.call_count == 1
        assert relay.call_args[0][0] == container_name_for_workspace(tmp_path)
        assert result.exit_code == 0, result.output

    def test_attach_race_branch_heals_relay(self, tmp_path, monkeypatch):
        relay = MagicMock()
        result, mock_popen = _invoke_run_with_relay(
            tmp_path, monkeypatch, relay, existing=(None, "abc123")
        )
        assert "Attaching to jail started by another process" in result.output
        assert relay.call_count == 1
        assert result.exit_code == 0, result.output

    def test_attach_relay_failure_is_nonfatal(self, tmp_path, monkeypatch):
        relay = MagicMock(side_effect=RuntimeError("relay spawn boom"))
        result, mock_popen = _invoke_run_with_relay(
            tmp_path, monkeypatch, relay, existing=("abc123",)
        )
        assert "Attaching to existing" in result.output
        assert result.exit_code == 0, result.output
        assert "relay not ensured" in result.output

    def test_fresh_run_skips_relay_without_singleton_socket(
        self, tmp_path, monkeypatch
    ):
        """No singleton socket = nothing to relay to.  The relay must not
        spawn and the terminator env var must not be injected —
        ``_broker_ensure`` owns that layer."""
        relay = MagicMock()
        result, mock_popen = _invoke_run_with_relay(
            tmp_path, monkeypatch, relay, broker_socket_exists=False
        )
        assert mock_popen.called, f"launch expected; output: {result.output}"
        argv = [str(a) for a in mock_popen.call_args[0][0]]
        relay.assert_not_called()
        assert not any("YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET" in a for a in argv), (
            argv
        )

    def test_attach_skips_relay_without_singleton_socket(self, tmp_path, monkeypatch):
        relay = MagicMock()
        result, _ = _invoke_run_with_relay(
            tmp_path,
            monkeypatch,
            relay,
            existing=("abc123",),
            broker_socket_exists=False,
        )
        assert "Attaching to existing" in result.output
        relay.assert_not_called()


class TestRunNewContainerMounts:
    """Test run() new container path with mounts and network config."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_new_container_with_network_host(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text('{"network": {"mode": "host"}}')
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            assert "--net=host" in run_cmd

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_new_container_with_ports(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"network": {"mode": "bridge", "ports": ["8000:8000", "3000:3000"]}}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            assert "-p" in run_cmd
            assert "8000:8000" in run_cmd

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_new_container_with_extra_mounts(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        # Create a real dir to mount
        mount_dir = tmp_path / "extra-data"
        mount_dir.mkdir()
        (tmp_path / "yolo-jail.jsonc").write_text(
            json.dumps({"mounts": [str(mount_dir)]})
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            cmd_str = " ".join(str(a) for a in run_cmd)
            assert "extra-data" in cmd_str
            assert ":ro" in cmd_str

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_new_container_mount_path_missing_warns(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"mounts": ["/nonexistent/path/should/skip"]}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        result = runner.invoke(app, ["run", "--", "bash"])
        assert "skipping" in result.output.lower() or "warning" in result.output.lower()


class TestRunRepoMountSource:
    """The /opt/yolo-jail mount source.  A workspace that is itself a
    yolo-jail source tree backs the mount (dev loop: nested jails must run
    the live edits, not the frozen uv-tool install at repo_root); every
    other workspace keeps repo_root."""

    @staticmethod
    def _make_yolo_tree(tmp_path, name="yolo-jail"):
        (tmp_path / "src" / "cli").mkdir(parents=True)
        (tmp_path / "src" / "cli" / "__init__.py").write_text("")
        (tmp_path / "pyproject.toml").write_text(f'[project]\nname = "{name}"\n')

    def test_yolo_source_workspace_backs_mount(self, tmp_path, monkeypatch):
        self._make_yolo_tree(tmp_path)
        argv = _launch_argv(tmp_path, monkeypatch)
        assert f"{tmp_path}:/opt/yolo-jail:ro" in argv
        assert f"{REPO_ROOT}:/opt/yolo-jail:ro" not in argv

    def test_normal_workspace_keeps_repo_root(self, tmp_path, monkeypatch):
        argv = _launch_argv(tmp_path, monkeypatch)
        assert f"{REPO_ROOT}:/opt/yolo-jail:ro" in argv
        assert f"{tmp_path}:/opt/yolo-jail:ro" not in argv

    def test_foreign_pyproject_name_keeps_repo_root(self, tmp_path, monkeypatch):
        self._make_yolo_tree(tmp_path, name="some-other-project")
        argv = _launch_argv(tmp_path, monkeypatch)
        assert f"{REPO_ROOT}:/opt/yolo-jail:ro" in argv
        assert f"{tmp_path}:/opt/yolo-jail:ro" not in argv

    def test_absent_pyproject_keeps_repo_root(self, tmp_path, monkeypatch):
        self._make_yolo_tree(tmp_path)
        (tmp_path / "pyproject.toml").unlink()
        argv = _launch_argv(tmp_path, monkeypatch)
        assert f"{REPO_ROOT}:/opt/yolo-jail:ro" in argv
        assert f"{tmp_path}:/opt/yolo-jail:ro" not in argv

    @pytest.mark.parametrize("raw", [b'name = "yolo-jail', b"\xff\xfe not utf-8 \xff"])
    def test_unreadable_pyproject_keeps_repo_root(self, tmp_path, monkeypatch, raw):
        self._make_yolo_tree(tmp_path)
        (tmp_path / "pyproject.toml").write_bytes(raw)
        argv = _launch_argv(tmp_path, monkeypatch)
        assert f"{REPO_ROOT}:/opt/yolo-jail:ro" in argv
        assert f"{tmp_path}:/opt/yolo-jail:ro" not in argv


class TestRunPodman:
    """Test run() runtime-specific command wiring for podman."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_podman_adds_uidmap_on_host(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            assert "--uidmap" in run_cmd or "--userns" in run_cmd

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_podman_grants_net_caps_for_nested_bridge_networking(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        """The jail's own podman runs as jail-root without a second userns,
        so netavark bridge networking (and even --network=none loopback
        setup) is capability-checked against the jail's userns.  Without
        NET_ADMIN/NET_RAW in the jail, plain `podman run` fails with
        'netavark: Netlink error: Operation not permitted'.  Both the host
        and in-container launch branches must grant them."""
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        caps = {run_cmd[i + 1] for i, a in enumerate(run_cmd[:-1]) if a == "--cap-add"}
        assert {"NET_ADMIN", "NET_RAW", "SYS_ADMIN"} <= caps
        # Caps alone aren't enough: the OCI default mounts /proc/sys
        # read-only, which kills netavark's netns sysctl writes.  The
        # unmask must ride along or bridge networking still fails with
        # "set sysctl ...: Read-only file system".
        sec_opts = {
            run_cmd[i + 1] for i, a in enumerate(run_cmd[:-1]) if a == "--security-opt"
        }
        assert "unmask=/proc/sys" in sec_opts


class TestRunDevicePassthrough:
    """Test run() device passthrough configuration."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_raw_device_missing_warns(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"devices": ["/dev/nonexistent_device_xyz"]}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        result = runner.invoke(app, ["run", "--", "bash"])
        assert (
            "skipping" in result.output.lower() or "not found" in result.output.lower()
        )

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_cgroup_rule_device(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"devices": [{"cgroup_rule": "c 189:* rwm"}]}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            assert "--device-cgroup-rule" in run_cmd


class TestRunKvm:
    """KVM passthrough flag wiring (opt-in via `kvm: true`)."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_kvm_disabled_adds_nothing(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            # No /dev/kvm device, no group-add.
            assert "/dev/kvm" not in run_cmd
            assert "keep-groups" not in run_cmd

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_kvm_enabled_podman_adds_device_and_keep_groups(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text('{"kvm": true}')
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        # Surgically pretend /dev/kvm exists without affecting other Path.exists
        # calls in the run path (which there are many of).
        import cli as _cli

        original_exists = _cli.Path.exists

        def fake_exists(self):
            if str(self) == "/dev/kvm":
                return True
            return original_exists(self)

        with patch.object(_cli.Path, "exists", fake_exists):
            runner = CliRunner()
            runner.invoke(app, ["run", "--", "bash"])

        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        # --device /dev/kvm appears as two consecutive elements.
        assert "/dev/kvm" in run_cmd
        idx = run_cmd.index("/dev/kvm")
        assert run_cmd[idx - 1] == "--device"
        # Podman path: group-add keep-groups.
        assert "keep-groups" in run_cmd
        ga_idx = run_cmd.index("keep-groups")
        assert run_cmd[ga_idx - 1] == "--group-add"

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_kvm_enabled_but_device_missing_warns_and_skips(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text('{"kvm": true}')
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        # Pretend /dev/kvm is absent even if it's actually present on the
        # test runner (covers both host situations deterministically).
        import cli as _cli

        original_exists = _cli.Path.exists

        def fake_exists(self):
            if str(self) == "/dev/kvm":
                return False
            return original_exists(self)

        with patch.object(_cli.Path, "exists", fake_exists):
            runner = CliRunner()
            result = runner.invoke(app, ["run", "--", "bash"])

        # Container still launches; just no kvm flags.
        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        assert "/dev/kvm" not in run_cmd
        assert "keep-groups" not in run_cmd
        # A warn is printed on the way through.
        assert "kvm" in result.output.lower()


class TestRunRocm:
    """AMD ROCm passthrough flag wiring (opt-in via `gpu.vendor: amd`)."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_rocm_enabled_podman_adds_device_nodes_and_keep_groups(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        # rocminfo is deliberately absent — the probe must still pass on the
        # device nodes alone (ROCm userspace lives in the image, not the host).
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"gpu": {"enabled": true, "vendor": "amd"}}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        # Surgically pretend the AMD device nodes exist without affecting the
        # many other Path.exists/glob calls in the run path.  The probe checks
        # /sys/module/amdgpu, /dev/kfd, and /dev/dri (renderD* via glob); the
        # injection block checks /dev/kfd and emits --device /dev/dri for the
        # "all" case.
        import cli as _cli

        original_exists = _cli.Path.exists
        original_glob = _cli.Path.glob

        def fake_exists(self):
            if str(self) in ("/dev/kfd", "/dev/dri", "/sys/module/amdgpu"):
                return True
            return original_exists(self)

        def fake_glob(self, pattern):
            if str(self) == "/dev/dri" and pattern == "renderD*":
                return iter([_cli.Path("/dev/dri/renderD128")])
            return original_glob(self, pattern)

        with (
            patch.object(_cli.Path, "exists", fake_exists),
            patch.object(_cli.Path, "glob", fake_glob),
        ):
            runner = CliRunner()
            runner.invoke(app, ["run", "--", "bash"])

        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        # --device /dev/kfd appears as two consecutive elements.
        assert "/dev/kfd" in run_cmd
        idx = run_cmd.index("/dev/kfd")
        assert run_cmd[idx - 1] == "--device"
        # devices=="all" → whole render dir.
        assert "/dev/dri" in run_cmd
        # Podman path: group-add keep-groups (crun-only; never a numeric GID).
        assert "keep-groups" in run_cmd
        ga_idx = run_cmd.index("keep-groups")
        assert run_cmd[ga_idx - 1] == "--group-add"
        # Locked-memory limit raised so KFD can pin the queue ring buffer
        # (CREATE_QUEUE EINVAL otherwise; verified on gfx1151).  A --ulimit
        # memlock flag must be present; its value depends on the host hard cap
        # (covered precisely by the dedicated memlock tests below), so here we
        # only assert the flag exists.
        assert "--ulimit" in run_cmd
        ul_idx = run_cmd.index("--ulimit")
        assert run_cmd[ul_idx + 1].startswith("memlock=")
        # ROCm in-container selector env: the ROCr/HSA selector does NOT
        # accept the literal "all" (it would hide every GPU, verified on
        # gfx1151 hardware).  For devices=="all" we leave it UNSET — ROCm's
        # own "all GPUs visible" default — so it must NOT appear in argv.
        assert not any(
            isinstance(a, str) and a.startswith("ROCR_VISIBLE_DEVICES") for a in run_cmd
        )
        assert not any(
            isinstance(a, str) and a.startswith("HIP_VISIBLE_DEVICES") for a in run_cmd
        )
        # No NVIDIA leakage into the AMD path.
        assert "nvidia.com/gpu=all" not in run_cmd
        assert not any(
            isinstance(a, str) and a.startswith("nvidia.com/gpu") for a in run_cmd
        )
        assert not any(
            isinstance(a, str) and a.startswith("NVIDIA_VISIBLE_DEVICES")
            for a in run_cmd
        )
        # AMD stays on crun: it must NOT inherit NVIDIA's --runtime runc pin.
        assert not ("--runtime" in run_cmd and "runc" in run_cmd)
        # vaapi not requested → no LIBVA env and no implied mesa package.
        assert not any(
            isinstance(a, str) and a.startswith("LIBVA_DRIVERS_PATH") for a in run_cmd
        )

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_rocm_vaapi_adds_libva_env_and_image_packages(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"gpu": {"enabled": true, "vendor": "amd", "vaapi": true}}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        import cli as _cli

        original_exists = _cli.Path.exists
        original_glob = _cli.Path.glob

        def fake_exists(self):
            if str(self) in ("/dev/kfd", "/dev/dri", "/sys/module/amdgpu"):
                return True
            return original_exists(self)

        def fake_glob(self, pattern):
            if str(self) == "/dev/dri" and pattern == "renderD*":
                return iter([_cli.Path("/dev/dri/renderD128")])
            return original_glob(self, pattern)

        with (
            patch.object(_cli.Path, "exists", fake_exists),
            patch.object(_cli.Path, "glob", fake_glob),
        ):
            runner = CliRunner()
            runner.invoke(app, ["run", "--", "bash"])

        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        # libva's compiled-in search path lacks /lib/dri (where the image
        # contents merge puts mesa's drivers) — the env var bridges it.
        assert "LIBVA_DRIVERS_PATH=/lib/dri:/usr/lib/dri" in run_cmd
        env_idx = run_cmd.index("LIBVA_DRIVERS_PATH=/lib/dri:/usr/lib/dri")
        assert run_cmd[env_idx - 1] == "-e"
        # The image build was asked for the implied VAAPI packages.
        image_packages = mock_auto_load.call_args.kwargs.get("extra_packages")
        assert "mesa" in image_packages
        assert "libva-utils" in image_packages

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_rocm_enabled_but_device_missing_warns_and_skips(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"gpu": {"enabled": true, "vendor": "amd"}}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        # Pretend /dev/kfd is absent even if the test runner happens to have an
        # AMD GPU.  The host probe then fails and the run warns-and-continues
        # without GPU flags (deterministic on any host).
        import cli as _cli

        original_exists = _cli.Path.exists

        def fake_exists(self):
            if str(self) == "/dev/kfd":
                return False
            return original_exists(self)

        with patch.object(_cli.Path, "exists", fake_exists):
            runner = CliRunner()
            result = runner.invoke(app, ["run", "--", "bash"])

        # Container still launches; just no ROCm flags.
        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        assert "/dev/kfd" not in run_cmd
        assert "keep-groups" not in run_cmd
        # No GPU → no memlock lift (it's gated on gpu_enabled).
        assert not any(isinstance(a, str) and a.startswith("memlock=") for a in run_cmd)
        assert not any(
            isinstance(a, str) and a.startswith("ROCR_VISIBLE_DEVICES") for a in run_cmd
        )
        # A warn is printed on the way through.
        assert "gpu" in result.output.lower() or "rocm" in result.output.lower()

    def _run_rocm_with_host_memlock(
        self, mock_which, mock_popen, tmp_path, monkeypatch, *, hard_limit
    ):
        """Invoke `yolo run` with AMD GPU enabled, forcing the host's
        RLIMIT_MEMLOCK hard cap to ``hard_limit``.  Returns (run_cmd, output).

        A rootless container can't raise memlock above the host hard cap, so
        the emitted --ulimit value must track that cap, not blindly request
        unlimited (which crun rejects with EPERM, failing container startup).
        """
        import resource

        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"gpu": {"enabled": true, "vendor": "amd"}}'
        )

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        import cli as _cli

        original_exists = _cli.Path.exists
        original_glob = _cli.Path.glob

        def fake_exists(self):
            s = str(self)
            if s in ("/dev/kfd", "/sys/module/amdgpu", "/dev/dri"):
                return True
            return original_exists(self)

        def fake_glob(self, pattern):
            if str(self) == "/dev/dri" and pattern == "renderD*":
                return iter([_cli.Path("/dev/dri/renderD128")])
            return original_glob(self, pattern)

        def fake_getrlimit(which):
            if which == resource.RLIMIT_MEMLOCK:
                return (hard_limit, hard_limit)
            return resource.getrlimit(which)

        with (
            patch.object(_cli.Path, "exists", fake_exists),
            patch.object(_cli.Path, "glob", fake_glob),
            patch("cli.run_cmd.resource.getrlimit", fake_getrlimit),
        ):
            runner = CliRunner()
            result = runner.invoke(app, ["run", "--", "bash"])

        assert mock_popen.called
        return mock_popen.call_args[0][0], result.output

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_rocm_memlock_clamped_to_finite_host_cap(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        # Finite host hard cap (8 MB): clamp the --ulimit to exactly the host
        # cap (the most a rootless container can get), NOT -1 (which can be
        # rejected by crun on some hosts).  Current ROCm runs fine at 8 MB, so
        # there is no low-cap warning.
        mock_check_output.side_effect = FileNotFoundError
        eight_mb = 8 * 1024 * 1024
        run_cmd, output = self._run_rocm_with_host_memlock(
            mock_which, mock_popen, tmp_path, monkeypatch, hard_limit=eight_mb
        )
        assert "--ulimit" in run_cmd
        ul_idx = run_cmd.index("--ulimit")
        assert run_cmd[ul_idx + 1] == f"memlock={eight_mb}:{eight_mb}"
        # Must NOT request unlimited on a finite-cap rootless host.
        assert "memlock=-1:-1" not in run_cmd

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_rocm_memlock_unlimited_when_host_allows(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        # Host hard cap is unlimited: request unlimited.
        import resource

        mock_check_output.side_effect = FileNotFoundError
        run_cmd, output = self._run_rocm_with_host_memlock(
            mock_which,
            mock_popen,
            tmp_path,
            monkeypatch,
            hard_limit=resource.RLIM_INFINITY,
        )
        assert "memlock=-1:-1" in run_cmd
        ul_idx = run_cmd.index("memlock=-1:-1")
        assert run_cmd[ul_idx - 1] == "--ulimit"

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_rocm_explicit_index_sets_visible_devices_env(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        # For an explicit non-"all" selection the ROCr/HSA selector IS valid
        # (it takes indices/UUIDs), so the env vars must be emitted and the
        # per-index render node (renderD128 for index 0) must be passed.
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"gpu": {"enabled": true, "vendor": "amd", "devices": "0"}}'
        )
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        import cli as _cli

        original_exists = _cli.Path.exists
        original_glob = _cli.Path.glob

        def fake_exists(self):
            if str(self) in (
                "/dev/kfd",
                "/dev/dri",
                "/dev/dri/renderD128",
                "/sys/module/amdgpu",
            ):
                return True
            return original_exists(self)

        def fake_glob(self, pattern):
            if str(self) == "/dev/dri" and pattern == "renderD*":
                return iter([_cli.Path("/dev/dri/renderD128")])
            return original_glob(self, pattern)

        with (
            patch.object(_cli.Path, "exists", fake_exists),
            patch.object(_cli.Path, "glob", fake_glob),
        ):
            runner = CliRunner()
            runner.invoke(app, ["run", "--", "bash"])

        assert mock_popen.called
        run_cmd = mock_popen.call_args[0][0]
        # Per-index selection: renderD128 for index 0, alongside /dev/kfd.
        assert "/dev/dri/renderD128" in run_cmd
        # Whole /dev/dri dir must NOT be passed for an explicit index.
        assert "/dev/dri" not in run_cmd
        # Explicit indices ARE valid for the selector env.
        assert "ROCR_VISIBLE_DEVICES=0" in run_cmd
        assert "HIP_VISIBLE_DEVICES=0" in run_cmd


class TestRunProfile:
    """Test run() with --profile flag."""

    @patch("subprocess.Popen")
    @patch("cli.run_cmd.auto_load_image")
    @patch("cli.run_cmd._check_config_changes", return_value=True)
    @patch("cli.run_cmd.find_running_container", return_value=None)
    @patch("subprocess.run")
    @patch("subprocess.check_output")
    @patch("shutil.which")
    def test_profile_adds_timing_env(
        self,
        mock_which,
        mock_check_output,
        mock_run,
        mock_find,
        mock_config_changes,
        mock_auto_load,
        mock_popen,
        tmp_path,
        monkeypatch,
    ):
        _run_monkeypatch(monkeypatch, tmp_path)
        _mock_runtimes(mock_which)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        mock_check_output.side_effect = FileNotFoundError

        mock_proc = MagicMock()
        mock_proc.wait.return_value = None
        mock_proc.returncode = 0
        mock_popen.return_value = mock_proc

        runner = CliRunner()
        runner.invoke(app, ["run", "--profile", "--", "bash"])

        if mock_popen.called:
            run_cmd = mock_popen.call_args[0][0]
            assert "YOLO_PROFILE=1" in run_cmd


# ═══════════════════════════════════════════════════════════════════════════════
# Test: generate_agents_md edge cases
# ═══════════════════════════════════════════════════════════════════════════════


class TestGenerateAgentsMdEdges:
    """Test generate_agents_md with forwarded ports and blocked tools."""

    def test_with_forwarded_ports_int(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # generate_agents_md reads AGENTS_DIR from cli.agents_md's namespace;
        # the cli.AGENTS_DIR re-export is never consulted, so patch the module.
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        (tmp_path / "agents").mkdir()

        result = generate_agents_md(
            "yolo-test",
            tmp_path,
            [],
            [],
            net_mode="bridge",
            runtime="podman",
            forward_host_ports=[5432],
            agents=["copilot"],
        )
        agents_copilot = (result / "AGENTS-copilot.md").read_text()
        assert "5432" in agents_copilot

    def test_with_forwarded_ports_string(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # generate_agents_md reads AGENTS_DIR from cli.agents_md's namespace;
        # the cli.AGENTS_DIR re-export is never consulted, so patch the module.
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        (tmp_path / "agents").mkdir()

        result = generate_agents_md(
            "yolo-test",
            tmp_path,
            [],
            [],
            net_mode="bridge",
            runtime="podman",
            forward_host_ports=["3000:3000"],
            agents=["copilot"],
        )
        agents_copilot = (result / "AGENTS-copilot.md").read_text()
        assert "3000" in agents_copilot

    def test_with_blocked_tools_suggestion(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # generate_agents_md reads AGENTS_DIR from cli.agents_md's namespace;
        # the cli.AGENTS_DIR re-export is never consulted, so patch the module.
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        (tmp_path / "agents").mkdir()

        blocked = [{"name": "grep", "message": "Use rg", "suggestion": "rg"}]
        result = generate_agents_md(
            "yolo-test",
            tmp_path,
            blocked,
            [],
            net_mode="bridge",
            runtime="podman",
            agents=["copilot"],
        )
        agents_copilot = (result / "AGENTS-copilot.md").read_text()
        assert "grep" in agents_copilot
        assert "rg" in agents_copilot

    def test_with_mount_descriptions(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # generate_agents_md reads AGENTS_DIR from cli.agents_md's namespace;
        # the cli.AGENTS_DIR re-export is never consulted, so patch the module.
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        (tmp_path / "agents").mkdir()

        mounts = ["/home/user/data:/ctx/data"]
        result = generate_agents_md(
            "yolo-test",
            tmp_path,
            [],
            mounts,
            net_mode="bridge",
            runtime="podman",
            agents=["copilot"],
        )
        agents_copilot = (result / "AGENTS-copilot.md").read_text()
        assert "data" in agents_copilot

    def test_agents_md_extra_appended_to_all_agents(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # generate_agents_md reads AGENTS_DIR from cli.agents_md's namespace;
        # the cli.AGENTS_DIR re-export is never consulted, so patch the module.
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        (tmp_path / "agents").mkdir()

        extra = "## Cerebras MCP\n\nUse cerebras-mcp for ultra-fast completions."
        result = generate_agents_md(
            "yolo-test",
            tmp_path,
            [],
            [],
            net_mode="bridge",
            runtime="podman",
            agents_md_extra=extra,
            agents=["copilot", "gemini", "claude"],
        )
        for name in ("AGENTS-copilot.md", "AGENTS-gemini.md", "CLAUDE.md"):
            content = (result / name).read_text()
            assert "## Cerebras MCP" in content
            assert "cerebras-mcp" in content

    def test_agents_md_extra_none_is_noop(self, tmp_path, monkeypatch):
        from cli import generate_agents_md

        # generate_agents_md reads AGENTS_DIR from cli.agents_md's namespace;
        # the cli.AGENTS_DIR re-export is never consulted, so patch the module.
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
        (tmp_path / "agents").mkdir()

        result = generate_agents_md(
            "yolo-test",
            tmp_path,
            [],
            [],
            net_mode="bridge",
            runtime="podman",
            agents_md_extra=None,
        )
        content = (result / "CLAUDE.md").read_text()
        assert "## Cerebras MCP" not in content


class TestRefreshJailBriefings:
    """End-to-end: skill + AGENTS staging refresh reflects host-side deletions.

    Repro of the bug this guards: the staging dir is bind-mounted into the
    running jail.  Without an attach-time refresh, deleting a host skill or
    editing a host AGENTS.md wouldn't propagate until the container was
    fully restarted.
    """

    def test_helper_rebuilds_after_host_skill_deletion(self, tmp_path, monkeypatch):
        from cli import _refresh_jail_briefings

        fake_home = tmp_path / "home"
        fake_agents = tmp_path / "agents"
        fake_home.mkdir()
        fake_agents.mkdir()

        # Seed a host skill in each agent's dir.
        for dotdir, sname in (
            (".copilot", "skill-a"),
            (".gemini", "skill-b"),
            (".claude", "skill-c"),
        ):
            sd = fake_home / dotdir / "skills" / sname
            sd.mkdir(parents=True)
            (sd / "SKILL.md").write_text(f"# {sname}\n")

        monkeypatch.setattr(Path, "home", staticmethod(lambda: fake_home))
        monkeypatch.setattr("cli.run_cmd.AGENTS_DIR", fake_agents)
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", fake_agents)

        workspace = tmp_path / "ws"
        workspace.mkdir()
        config = {
            "network": {"mode": "bridge"},
            "agents": ["copilot", "gemini", "claude"],
        }

        agents_path = _refresh_jail_briefings(
            "test-cname", workspace, config, "podman", "bridge"
        )
        assert (agents_path / "skills-copilot" / "skill-a").exists()
        assert (agents_path / "skills-gemini" / "skill-b").exists()
        assert (agents_path / "skills-claude" / "skill-c").exists()
        # jail-startup builtin always present.
        assert (agents_path / "skills-copilot" / "jail-startup").exists()
        # AGENTS files generated.
        assert (agents_path / "AGENTS-copilot.md").exists()
        assert (agents_path / "CLAUDE.md").exists()

        # Delete two skills from the host.
        import shutil

        shutil.rmtree(fake_home / ".copilot" / "skills" / "skill-a")
        shutil.rmtree(fake_home / ".claude" / "skills" / "skill-c")

        # Refresh again — deletions reflected, surviving skills retained.
        _refresh_jail_briefings("test-cname", workspace, config, "podman", "bridge")
        assert not (agents_path / "skills-copilot" / "skill-a").exists()
        assert not (agents_path / "skills-claude" / "skill-c").exists()
        assert (agents_path / "skills-gemini" / "skill-b").exists()
        assert (agents_path / "skills-copilot" / "jail-startup").exists()

    def test_skills_dir_inode_is_stable_across_refreshes(self, tmp_path, monkeypatch):
        """Regression guard: `_prepare_skills` must NOT unlink+recreate the
        per-agent skills dir.  That dir is the bind-mount source for
        `/home/agent/.<agent>/skills` inside a running container; recreating
        it allocates a new inode and orphans the container's mount, so all
        future host-side edits silently no-op for any attached jail.
        """
        from cli import _refresh_jail_briefings

        fake_home = tmp_path / "home"
        fake_agents = tmp_path / "agents"
        fake_home.mkdir()
        fake_agents.mkdir()

        for dotdir, sname in (
            (".copilot", "skill-a"),
            (".gemini", "skill-b"),
            (".claude", "skill-c"),
        ):
            sd = fake_home / dotdir / "skills" / sname
            sd.mkdir(parents=True)
            (sd / "SKILL.md").write_text(f"# {sname}\n")

        monkeypatch.setattr(Path, "home", staticmethod(lambda: fake_home))
        monkeypatch.setattr("cli.run_cmd.AGENTS_DIR", fake_agents)
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", fake_agents)

        workspace = tmp_path / "ws"
        workspace.mkdir()
        config: dict = {"agents": ["copilot", "gemini", "claude"]}

        agents_path = _refresh_jail_briefings(
            "test-cname", workspace, config, "podman", "bridge"
        )
        ino_before = {
            suffix: os.stat(agents_path / f"skills-{suffix}").st_ino
            for suffix in ("copilot", "gemini", "claude")
        }

        # Mutate host state (add one skill, remove another) — exactly what
        # the user would do between two `yolo` invocations.
        import shutil

        (fake_home / ".copilot" / "skills" / "skill-new").mkdir()
        (fake_home / ".copilot" / "skills" / "skill-new" / "SKILL.md").write_text(
            "# new\n"
        )
        shutil.rmtree(fake_home / ".claude" / "skills" / "skill-c")

        _refresh_jail_briefings("test-cname", workspace, config, "podman", "bridge")
        ino_after = {
            suffix: os.stat(agents_path / f"skills-{suffix}").st_ino
            for suffix in ("copilot", "gemini", "claude")
        }

        for suffix in ("copilot", "gemini", "claude"):
            assert ino_before[suffix] == ino_after[suffix], (
                f"skills-{suffix} dir was recreated "
                f"({ino_before[suffix]} -> {ino_after[suffix]}); "
                "this orphans any running container's bind mount."
            )
        # And the host mutations are reflected in the directory listing.
        assert (agents_path / "skills-copilot" / "skill-new").exists()
        assert not (agents_path / "skills-claude" / "skill-c").exists()

    def test_helper_propagates_agents_md_extra_changes(self, tmp_path, monkeypatch):
        from cli import _refresh_jail_briefings

        fake_home = tmp_path / "home"
        fake_agents = tmp_path / "agents"
        fake_home.mkdir()
        fake_agents.mkdir()

        monkeypatch.setattr(Path, "home", staticmethod(lambda: fake_home))
        monkeypatch.setattr("cli.run_cmd.AGENTS_DIR", fake_agents)
        monkeypatch.setattr("cli.agents_md.AGENTS_DIR", fake_agents)

        workspace = tmp_path / "ws"
        workspace.mkdir()

        agents_path = _refresh_jail_briefings(
            "test-cname",
            workspace,
            {"agents_md_extra": "## Note v1"},
            "podman",
            "bridge",
        )
        assert "## Note v1" in (agents_path / "CLAUDE.md").read_text()

        # User edits the extra; next attach-time refresh rewrites the file.
        _refresh_jail_briefings(
            "test-cname",
            workspace,
            {"agents_md_extra": "## Note v2"},
            "podman",
            "bridge",
        )
        text = (agents_path / "CLAUDE.md").read_text()
        assert "## Note v2" in text
        assert "## Note v1" not in text


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _seed_agent_dir
# ═══════════════════════════════════════════════════════════════════════════════


class TestSeedAgentDirCommands:
    """Test _seed_agent_dir seeds auth files from GLOBAL_HOME into per-workspace overlay."""

    def test_seeds_auth_files(self, tmp_path):
        from cli import _seed_agent_dir

        src = tmp_path / "src"
        src.mkdir()
        (src / "hosts.json").write_text('{"token": "x"}')
        dst = tmp_path / "dst"
        dst.mkdir()
        _seed_agent_dir(src, dst)
        assert (dst / "hosts.json").read_text() == '{"token": "x"}'

    def test_does_not_overwrite_existing(self, tmp_path):
        from cli import _seed_agent_dir

        src = tmp_path / "src"
        src.mkdir()
        (src / "hosts.json").write_text("old")
        dst = tmp_path / "dst"
        dst.mkdir()
        (dst / "hosts.json").write_text("kept")
        _seed_agent_dir(src, dst)
        assert (dst / "hosts.json").read_text() == "kept"


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _sync_claude_json_seed
# ═══════════════════════════════════════════════════════════════════════════════


class TestSyncClaudeJsonSeed:
    """Test the two-way claude.json login-state sync between the
    GLOBAL_HOME seed and a per-workspace overlay."""

    @staticmethod
    def _paths(tmp_path):
        seed = tmp_path / "global" / ".claude" / "claude.json"
        ws = tmp_path / "ws" / "claude" / "claude.json"
        ws.parent.mkdir(parents=True)
        return seed, ws

    def test_seed_merged_into_workspace(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        seed.parent.mkdir(parents=True)
        seed.write_text(
            json.dumps({"oauthAccount": {"uuid": "u1"}, "hasCompletedOnboarding": True})
        )
        ws.write_text(json.dumps({"mcpServers": {"foo": {}}}))
        _sync_claude_json_seed(seed, ws)
        data = json.loads(ws.read_text())
        assert data["oauthAccount"] == {"uuid": "u1"}
        assert data["hasCompletedOnboarding"] is True
        # Workspace-specific config preserved.
        assert data["mcpServers"] == {"foo": {}}

    def test_seed_does_not_overwrite_workspace_keys(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        seed.parent.mkdir(parents=True)
        seed.write_text(json.dumps({"oauthAccount": {"uuid": "seed"}}))
        ws.write_text(json.dumps({"oauthAccount": {"uuid": "ws"}}))
        _sync_claude_json_seed(seed, ws)
        assert json.loads(ws.read_text())["oauthAccount"] == {"uuid": "ws"}

    def test_backpropagates_to_missing_seed_allowlist_only(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        ws.write_text(
            json.dumps(
                {
                    "oauthAccount": {"uuid": "u1"},
                    "hasCompletedOnboarding": True,
                    "mcpServers": {"foo": {}},
                    "projects": {"/x": {}},
                }
            )
        )
        _sync_claude_json_seed(seed, ws)
        data = json.loads(seed.read_text())
        assert data == {
            "oauthAccount": {"uuid": "u1"},
            "hasCompletedOnboarding": True,
        }

    def test_backpropagates_into_existing_seed_preserving_keys(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        seed.parent.mkdir(parents=True)
        seed.write_text(json.dumps({"numStartups": 7}))
        ws.write_text(
            json.dumps({"oauthAccount": {"uuid": "u1"}, "hasCompletedOnboarding": True})
        )
        _sync_claude_json_seed(seed, ws)
        data = json.loads(seed.read_text())
        assert data["oauthAccount"] == {"uuid": "u1"}
        assert data["hasCompletedOnboarding"] is True
        assert data["numStartups"] == 7

    def test_no_write_when_neither_side_logged_in(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        ws.write_text(json.dumps({"mcpServers": {"foo": {}}}))
        _sync_claude_json_seed(seed, ws)
        assert not seed.exists()

    def test_no_backprop_when_seed_already_has_account(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        seed.parent.mkdir(parents=True)
        seed.write_text(json.dumps({"oauthAccount": {"uuid": "seed"}}))
        ws.write_text(json.dumps({"oauthAccount": {"uuid": "ws"}}))
        _sync_claude_json_seed(seed, ws)
        # Seed untouched — workspace state must not clobber it.
        assert json.loads(seed.read_text()) == {"oauthAccount": {"uuid": "seed"}}

    def test_corrupt_seed_does_not_crash_and_gets_repaired(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        seed.parent.mkdir(parents=True)
        seed.write_text("{not json")
        ws.write_text(json.dumps({"oauthAccount": {"uuid": "u1"}}))
        _sync_claude_json_seed(seed, ws)
        assert json.loads(seed.read_text())["oauthAccount"] == {"uuid": "u1"}

    def test_corrupt_workspace_does_not_crash(self, tmp_path):
        from cli import _sync_claude_json_seed

        seed, ws = self._paths(tmp_path)
        seed.parent.mkdir(parents=True)
        seed.write_text(json.dumps({"hasCompletedOnboarding": True}))
        ws.write_text("{not json")
        _sync_claude_json_seed(seed, ws)
        # Corrupt workspace reads as empty → seed keys fill it.
        assert json.loads(ws.read_text())["hasCompletedOnboarding"] is True


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _host_mise_dir
# ═══════════════════════════════════════════════════════════════════════════════


class TestHostMiseDir:
    """_host_mise_dir names the host's own store — no env overrides, no
    side effects (the jail store lives in _jail_mise_store_dir)."""

    def test_default_path(self, monkeypatch):
        from cli import _host_mise_dir

        monkeypatch.delenv("MISE_DATA_DIR", raising=False)
        assert _host_mise_dir() == Path.home() / ".local" / "share" / "mise"

    def test_ignores_env_and_does_not_mkdir(self, tmp_path, monkeypatch):
        from cli import _host_mise_dir

        env_dir = tmp_path / "env-mise"
        monkeypatch.setenv("MISE_DATA_DIR", str(env_dir))
        assert _host_mise_dir() == Path.home() / ".local" / "share" / "mise"
        assert not env_dir.exists()


# ═══════════════════════════════════════════════════════════════════════════════
# Test: cleanup_port_forwarding
# ═══════════════════════════════════════════════════════════════════════════════


class TestCleanupPortForwarding:
    """Test cleanup_port_forwarding terminates procs and removes dir."""

    def test_cleanup_terminates_procs(self, tmp_path):
        from cli import cleanup_port_forwarding

        mock_proc1 = MagicMock()
        mock_proc2 = MagicMock()
        socket_dir = tmp_path / "sockets"
        socket_dir.mkdir()

        cleanup_port_forwarding([mock_proc1, mock_proc2], socket_dir)

        mock_proc1.terminate.assert_called_once()
        mock_proc2.terminate.assert_called_once()
        assert not socket_dir.exists()

    def test_cleanup_kills_on_timeout(self, tmp_path):
        from cli import cleanup_port_forwarding

        mock_proc = MagicMock()
        mock_proc.wait.side_effect = subprocess.TimeoutExpired("socat", 2)

        cleanup_port_forwarding([mock_proc], None)

        mock_proc.terminate.assert_called_once()
        mock_proc.kill.assert_called_once()

    def test_cleanup_no_socket_dir(self):
        from cli import cleanup_port_forwarding

        cleanup_port_forwarding([], None)  # Should not raise


# ═══════════════════════════════════════════════════════════════════════════════
# Test: port forwarding socat error path
# ═══════════════════════════════════════════════════════════════════════════════


class TestPortForwardingErrors:
    """Test start_host_port_forwarding error handling."""

    @patch("subprocess.Popen")
    def test_socat_generic_error(self, mock_popen, tmp_path):
        mock_popen.side_effect = OSError("Permission denied")
        socket_dir = tmp_path / "sockets"

        result = start_host_port_forwarding(
            [{"host": 8080, "container": 8080}], "yolo-test", socket_dir
        )
        assert result == []

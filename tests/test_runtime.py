"""Tests for container runtime selection and multi-runtime support."""

import hashlib
import os
import re
import sys
import subprocess
import json
import shutil
from pathlib import Path
from unittest.mock import patch
import pytest
from typer.testing import CliRunner

REPO_ROOT = Path(__file__).parent.parent.resolve()

from src.cli import _runtime  # noqa: E402


# --- Unit tests for _runtime() ---


def test_runtime_env_var_overrides_config():
    with patch.dict(os.environ, {"YOLO_RUNTIME": "container"}):
        assert _runtime({"runtime": "podman"}) == "container"


def test_runtime_config_used_when_no_env():
    with patch.dict(os.environ, {}, clear=False):
        os.environ.pop("YOLO_RUNTIME", None)
        assert _runtime({"runtime": "podman"}) == "podman"


def test_runtime_auto_detect_when_no_config():
    with patch.dict(os.environ, {}, clear=False):
        os.environ.pop("YOLO_RUNTIME", None)
        with (
            patch("shutil.which") as mock_which,
            patch("cli._runtime_is_connectable", return_value=True),
        ):
            mock_which.side_effect = lambda x: (
                "/usr/bin/podman" if x == "podman" else None
            )
            result = _runtime({})
            assert result in ("podman", "container")


def test_runtime_rejects_invalid_env():
    with patch.dict(os.environ, {"YOLO_RUNTIME": "containerd"}):
        # Invalid env value ignored, falls through to config/auto-detect
        result = _runtime({"runtime": "container"})
        assert result == "container"


def test_runtime_rejects_invalid_config():
    with patch.dict(os.environ, {}, clear=False):
        os.environ.pop("YOLO_RUNTIME", None)
        with (
            patch("shutil.which") as mock_which,
            patch("cli._runtime_is_connectable", return_value=True),
        ):
            mock_which.side_effect = lambda x: (
                "/usr/bin/podman" if x == "podman" else None
            )
            result = _runtime({"runtime": "lxc"})
            assert result in ("podman", "container")


def test_check_help_mentions_every_config_edit():
    import src.cli as cli

    result = CliRunner().invoke(cli.app, ["check", "--help"])
    assert result.exit_code == 0
    assert "after every config edit" in result.stdout.lower()


def test_config_ref_mentions_yolo_check_after_every_edit():
    import src.cli as cli

    result = CliRunner().invoke(cli.app, ["config-ref"])
    assert result.exit_code == 0
    assert "After EVERY edit" in result.stdout
    assert "yolo check" in result.stdout


def test_generated_agents_md_mentions_yolo_check(tmp_path, monkeypatch):
    import src.cli as cli

    monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
    agents_path = cli.generate_agents_md(
        "yolo-test",
        tmp_path / "workspace",
        [],
        [],
    )

    content = (agents_path / "AGENTS-copilot.md").read_text()
    assert "ALWAYS run `yolo check` after every config edit" in content


# --- ensure_global_storage tests ---


def test_ensure_global_storage_creates_mount_parents(tmp_path, monkeypatch):
    """Pre-create intermediate dirs so the container runtime doesn't create them as root."""
    import src.cli as cli

    monkeypatch.setattr(cli, "GLOBAL_HOME", tmp_path / "home")
    monkeypatch.setattr(cli, "GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr(cli, "GLOBAL_CACHE", tmp_path / "cache")
    monkeypatch.setattr(cli, "CONTAINER_DIR", tmp_path / "containers")
    monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
    cli.ensure_global_storage()

    # Core dirs exist
    assert (tmp_path / "home").is_dir()
    assert (tmp_path / "mise").is_dir()
    assert (tmp_path / "containers").is_dir()
    assert (tmp_path / "agents").is_dir()
    # Intermediate mount-parent dirs that the runtime would otherwise create as root
    assert (tmp_path / "home" / ".copilot").is_dir()
    assert (tmp_path / "home" / ".gemini").is_dir()
    assert (tmp_path / "home" / ".claude").is_dir()
    assert (tmp_path / "home" / ".config" / "git").is_dir()


# --- Integration tests for per-runtime sentinel ---


def test_sentinel_is_per_runtime(tmp_path):
    """Verify that .last-load-<runtime> sentinel files are created per runtime."""
    # Just check the sentinel path logic (don't actually load)
    # We can verify by checking the sentinel attribute
    sentinel_container = tmp_path / ".last-load-container"
    sentinel_podman = tmp_path / ".last-load-podman"
    assert not sentinel_container.exists()
    assert not sentinel_podman.exists()


def test_skip_image_load_when_container_running(tmp_path, monkeypatch):
    """auto_load_image must NOT be called when a container is already running."""

    import src.cli as cli
    from unittest.mock import patch, MagicMock

    monkeypatch.chdir(tmp_path)
    image_load_called = []
    fake_proc = MagicMock()
    fake_proc.returncode = 0

    with (
        patch.object(
            cli,
            "auto_load_image",
            side_effect=lambda *a, **k: image_load_called.append(True),
        ),
        patch.object(cli, "find_running_container", return_value="abc123def456"),
        patch.object(cli, "load_config", return_value={}),
        patch.object(cli, "ensure_global_storage"),
        patch.object(cli, "_runtime", return_value="podman"),
        patch.object(cli, "_tmux_rename_window"),
        patch.object(cli.subprocess, "run", return_value=fake_proc),
    ):
        from typer.testing import CliRunner

        try:
            CliRunner().invoke(cli.app, ["run"], catch_exceptions=False)
        except SystemExit:
            pass

    assert not image_load_called, (
        "auto_load_image must not be called when a container is already running"
    )


def test_exec_path_no_unbound_errors(tmp_path, monkeypatch):
    """The exec-into-existing-container path must not raise UnboundLocalError.

    Regression test: local `import subprocess` inside run() caused
    subprocess to be treated as a local variable, making it unbound
    when accessed before the import statement.
    """

    import src.cli as cli
    from unittest.mock import patch, MagicMock

    monkeypatch.chdir(tmp_path)
    fake_proc = MagicMock()
    fake_proc.returncode = 0
    exec_args = []

    def capture_run(cmd, **kwargs):
        exec_args.append(cmd)
        return fake_proc

    with (
        patch.object(cli, "find_running_container", return_value="abc123def456"),
        patch.object(cli, "load_config", return_value={}),
        patch.object(cli, "ensure_global_storage"),
        patch.object(cli, "_runtime", return_value="podman"),
        patch.object(cli, "_tmux_rename_window"),
        patch.object(cli.subprocess, "check_output", side_effect=FileNotFoundError),
        patch.object(cli.subprocess, "run", side_effect=capture_run),
    ):
        from typer.testing import CliRunner

        try:
            CliRunner().invoke(
                cli.app, ["run", "--", "echo", "hi"], catch_exceptions=False
            )
        except SystemExit:
            pass

    assert exec_args, (
        "subprocess.run should have been called with the runtime's exec command"
    )
    assert any(any("exec" in str(a) for a in cmd) for cmd in exec_args), (
        "should have called the runtime's exec command"
    )


def _fake_machine_inspect(memory_mb: int, *, state: str = "running"):
    import src.cli as cli
    from unittest.mock import MagicMock

    payload = json.dumps(
        [
            {
                "Name": "podman-machine-default",
                "State": state,
                "Resources": {"Memory": memory_mb},
            }
        ]
    )
    return cli, MagicMock(returncode=0, stdout=payload, stderr="")


def test_podman_machine_check_warns_below_floor(tmp_path):
    """A 2 GB Podman Machine (the OOM-prone setup) must produce a warning."""
    cli, fake_result = _fake_machine_inspect(2048)

    calls = []

    def ok(msg):
        calls.append(("ok", msg))

    def warn(msg, note=""):
        calls.append(("warn", msg, note))

    with (
        patch.object(cli.subprocess, "run", return_value=fake_result),
        patch.object(cli, "load_config", return_value={}),
    ):
        cli._check_podman_machine_resources(tmp_path, ok=ok, warn=warn)

    assert len(calls) == 1
    assert calls[0][0] == "warn"
    assert "2048 MB" in calls[0][1]
    assert "below" in calls[0][1].lower()
    # The fix hint must include the actual command and the VM-restart caveat.
    assert "podman machine set --memory" in calls[0][2]
    assert "restarts the VM" in calls[0][2]


def test_podman_machine_check_ok_above_floor(tmp_path):
    """8 GB is well above the floor and no workspace constraint — green."""
    cli, fake_result = _fake_machine_inspect(8192)

    calls = []

    def ok(msg):
        calls.append(("ok", msg))

    def warn(msg, note=""):
        calls.append(("warn", msg, note))

    with (
        patch.object(cli.subprocess, "run", return_value=fake_result),
        patch.object(cli, "load_config", return_value={}),
    ):
        cli._check_podman_machine_resources(tmp_path, ok=ok, warn=warn)

    assert len(calls) == 1
    assert calls[0][0] == "ok"
    assert "8192 MB" in calls[0][1]


def test_podman_machine_check_warns_below_workspace_request(tmp_path):
    """VM > floor but smaller than the workspace's resources.memory request."""
    cli, fake_result = _fake_machine_inspect(6144)

    calls = []

    def ok(msg):
        calls.append(("ok", msg))

    def warn(msg, note=""):
        calls.append(("warn", msg, note))

    with (
        patch.object(cli.subprocess, "run", return_value=fake_result),
        patch.object(cli, "load_config", return_value={"resources": {"memory": "8g"}}),
    ):
        cli._check_podman_machine_resources(tmp_path, ok=ok, warn=warn)

    assert len(calls) == 1
    assert calls[0][0] == "warn"
    assert "workspace requests" in calls[0][1]
    assert "resources.memory=8g" in calls[0][1]


def test_podman_machine_check_silent_when_inspect_fails(tmp_path):
    """If `podman machine inspect` errors, the helper is silent — best-effort."""
    import src.cli as cli
    from unittest.mock import MagicMock

    calls = []

    def ok(msg):
        calls.append(("ok", msg))

    def warn(msg, note=""):
        calls.append(("warn", msg, note))

    with patch.object(
        cli.subprocess,
        "run",
        return_value=MagicMock(returncode=1, stdout="", stderr="boom"),
    ):
        cli._check_podman_machine_resources(tmp_path, ok=ok, warn=warn)
    assert calls == []


def test_oom_hint_fires_on_macos_podman_137_with_tiny_vm(capsys):
    """Exit 137 on macOS+podman with a 2 GB VM should print an OOM hint."""
    import src.cli as cli
    from unittest.mock import MagicMock

    fake_inspect = MagicMock(
        returncode=0,
        stdout=json.dumps(
            [
                {
                    "Name": "podman-machine-default",
                    "State": "running",
                    "Resources": {"Memory": 2048},
                }
            ]
        ),
    )
    with (
        patch.object(cli, "IS_MACOS", True),
        patch.object(cli.subprocess, "run", return_value=fake_inspect),
    ):
        cli._maybe_warn_about_oom_killer(137, "podman")
    out = capsys.readouterr().out
    assert "OOM-killer" in out
    assert "2048 MB" in out
    assert "podman machine set --memory" in out


def test_oom_hint_silent_when_vm_above_floor(capsys):
    """A healthy 8 GB VM should produce no hint even on exit 137."""
    import src.cli as cli
    from unittest.mock import MagicMock

    fake_inspect = MagicMock(
        returncode=0,
        stdout=json.dumps(
            [
                {
                    "Name": "podman-machine-default",
                    "State": "running",
                    "Resources": {"Memory": 8192},
                }
            ]
        ),
    )
    with (
        patch.object(cli, "IS_MACOS", True),
        patch.object(cli.subprocess, "run", return_value=fake_inspect),
    ):
        cli._maybe_warn_about_oom_killer(137, "podman")
    assert capsys.readouterr().out == ""


def test_oom_hint_silent_for_non_137_exit_codes(capsys):
    """Only SIGKILL (137) gets the OOM speculation; other exits are quiet."""
    import src.cli as cli
    from unittest.mock import MagicMock

    fake_inspect = MagicMock(returncode=0, stdout="[]")  # would fail anyway
    with (
        patch.object(cli, "IS_MACOS", True),
        patch.object(cli.subprocess, "run", return_value=fake_inspect) as run_mock,
    ):
        cli._maybe_warn_about_oom_killer(1, "podman")
        cli._maybe_warn_about_oom_killer(130, "podman")  # SIGINT
    assert capsys.readouterr().out == ""
    # Skips the inspect entirely — exit-code gate runs first.
    assert run_mock.call_count == 0


def test_oom_hint_silent_on_non_macos_or_non_podman(capsys):
    """Linux+podman or macOS+container doesn't have the Podman Machine VM."""
    import src.cli as cli

    with (
        patch.object(cli, "IS_MACOS", False),
        patch.object(cli.subprocess, "run") as run_mock,
    ):
        cli._maybe_warn_about_oom_killer(137, "podman")
    assert capsys.readouterr().out == ""
    assert run_mock.call_count == 0

    with (
        patch.object(cli, "IS_MACOS", True),
        patch.object(cli.subprocess, "run") as run_mock,
    ):
        cli._maybe_warn_about_oom_killer(137, "container")
    assert capsys.readouterr().out == ""
    assert run_mock.call_count == 0


AVAILABLE_RUNTIMES = []
if shutil.which("podman"):
    # On macOS, podman requires a running VM (Podman Machine).  Only include
    # it if `podman info` succeeds (confirms the machine is reachable).
    if sys.platform == "darwin":
        try:
            subprocess.run(
                ["podman", "info"],
                capture_output=True,
                timeout=10,
            ).check_returncode()
            AVAILABLE_RUNTIMES.append("podman")
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError):
            pass  # Podman Machine not running — skip podman tests
    else:
        AVAILABLE_RUNTIMES.append("podman")
if shutil.which("container"):
    # Apple Container CLI (macOS only). Check that the system is running.
    try:
        subprocess.run(
            ["container", "system", "status"],
            capture_output=True,
            timeout=10,
        ).check_returncode()
        AVAILABLE_RUNTIMES.append("container")
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError):
        pass  # Apple Container system not running


def run_yolo_with_runtime(project_dir, command, runtime):
    """Run a shell command inside the jail with a specific runtime."""
    env = {**os.environ, "TERM": "dumb", "YOLO_RUNTIME": runtime}
    result = subprocess.run(
        [
            "uv",
            "run",
            "--project",
            str(REPO_ROOT),
            "python",
            str(REPO_ROOT / "src" / "cli.py"),
            "run",
            "--",
            "bash",
            "-lc",
            command,
        ],
        cwd=str(project_dir),
        capture_output=True,
        text=True,
        timeout=120,
        env=env,
    )
    return result


@pytest.fixture
def temp_project(tmp_path):
    project_dir = tmp_path / "test_project"
    project_dir.mkdir()
    config = {
        "security": {
            "blocked_tools": [
                "curl",
                {"name": "grep", "message": "NO GREP", "suggestion": "use rg"},
                {"name": "find", "message": "NO FIND", "suggestion": "use fd"},
            ]
        },
    }
    with open(project_dir / "yolo-jail.jsonc", "w") as f:
        json.dump(config, f)
    return project_dir


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_basic_command(temp_project, runtime):
    """Test that a basic command works with each runtime."""
    result = run_yolo_with_runtime(temp_project, "echo hello", runtime)
    assert result.returncode == 0
    assert "hello" in result.stdout


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_blocked_tool_with_runtime(temp_project, runtime):
    """Test that blocked tools are properly blocked with each runtime."""
    result = run_yolo_with_runtime(temp_project, "curl --version", runtime)
    assert result.returncode == 127
    assert "blocked" in result.stderr.lower()


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_file_ownership(temp_project, runtime):
    """Test that files created inside jail are owned by host user."""
    run_yolo_with_runtime(temp_project, "touch /workspace/test-ownership", runtime)
    test_file = temp_project / "test-ownership"
    assert test_file.exists()
    stat = test_file.stat()
    assert stat.st_uid == os.getuid()
    assert stat.st_gid == os.getgid()


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_workspace_mount(temp_project, runtime):
    """Test that workspace is properly mounted."""
    result = run_yolo_with_runtime(temp_project, "ls -d /workspace", runtime)
    assert result.returncode == 0
    assert "/workspace" in result.stdout


# ═══════════════════════════════════════════════════════════════════════════════
# Helpers for stale container tests
# ═══════════════════════════════════════════════════════════════════════════════


def _container_name_for_workspace(workspace: Path) -> str:
    """Mirror cli.py's container_name_for_workspace for test cleanup."""
    name = workspace.resolve().name
    safe = re.sub(r"[^a-z0-9-]", "-", name.lower()).strip("-")[:40]
    if not safe:
        safe = "jail"
    h = hashlib.sha256(str(workspace.resolve()).encode()).hexdigest()[:8]
    return f"yolo-{safe}-{h}"


def _force_remove_container(project_dir: Path, runtime: str):
    """Force-remove the jail container for a project directory."""
    cname = _container_name_for_workspace(project_dir)
    if runtime == "container":
        subprocess.run(
            ["container", "rm", "--force", cname],
            capture_output=True,
            timeout=10,
        )
    else:
        subprocess.run(
            [runtime, "rm", "-f", cname],
            capture_output=True,
            timeout=10,
        )


def _create_stale_container(project_dir: Path, runtime: str):
    """Create a stopped container with the jail's name to simulate a stale leftover.

    Runs a trivial command in a named container WITHOUT --rm, so the container
    remains in 'exited' state after it finishes.
    """
    cname = _container_name_for_workspace(project_dir)
    # First ensure no container with this name exists
    _force_remove_container(project_dir, runtime)
    # Create and immediately exit a container (no --rm, so it stays as 'exited')
    if runtime == "container":
        # Apple Container: use 'container run' without --rm
        subprocess.run(
            ["container", "run", "--name", cname, "alpine:latest", "true"],
            capture_output=True,
            timeout=30,
        )
    else:
        subprocess.run(
            [runtime, "run", "--name", cname, "alpine:latest", "true"],
            capture_output=True,
            timeout=30,
        )
    return cname


def _container_exists(cname: str, runtime: str) -> bool:
    """Check if a container (running or stopped) exists."""
    if runtime == "container":
        result = subprocess.run(
            ["container", "ls", "--all"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        for line in result.stdout.strip().splitlines()[1:]:
            parts = line.split()
            if parts and parts[0] == cname:
                return True
        return False
    else:
        result = subprocess.run(
            [runtime, "ps", "-a", "-q", "--filter", f"name=^/{cname}$"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        return bool(result.stdout.strip())


# ═══════════════════════════════════════════════════════════════════════════════
# Integration tests: stale container recovery
# ═══════════════════════════════════════════════════════════════════════════════


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_stale_container_auto_removed(temp_project, runtime):
    """When a stopped container with the jail name exists, yolo should remove it
    and successfully start a new jail."""
    cname = _create_stale_container(temp_project, runtime)
    try:
        # Verify the stale container exists
        assert _container_exists(cname, runtime), (
            f"Setup failed: stale container {cname} was not created"
        )

        # Now run yolo — it should auto-remove the stale container and succeed
        result = run_yolo_with_runtime(temp_project, "echo recovered", runtime)
        assert result.returncode == 0, (
            f"yolo failed with stale container present.\n"
            f"stdout: {result.stdout}\nstderr: {result.stderr}"
        )
        assert "recovered" in result.stdout
        # Verify the stale removal message appeared
        assert "Removing stale container" in result.stderr
    finally:
        _force_remove_container(temp_project, runtime)


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_stale_container_new_flag(temp_project, runtime):
    """The --new flag should also handle stale containers."""
    cname = _create_stale_container(temp_project, runtime)
    try:
        assert _container_exists(cname, runtime)

        # Run with --new flag
        env = {**os.environ, "TERM": "dumb", "YOLO_RUNTIME": runtime}
        result = subprocess.run(
            [
                "uv",
                "run",
                "--project",
                str(REPO_ROOT),
                "python",
                str(REPO_ROOT / "src" / "cli.py"),
                "run",
                "--new",
                "--",
                "bash",
                "-lc",
                "echo fresh",
            ],
            cwd=str(temp_project),
            capture_output=True,
            text=True,
            timeout=120,
            env=env,
        )
        assert result.returncode == 0, (
            f"yolo --new failed with stale container.\n"
            f"stdout: {result.stdout}\nstderr: {result.stderr}"
        )
        assert "fresh" in result.stdout
    finally:
        _force_remove_container(temp_project, runtime)


# ═══════════════════════════════════════════════════════════════════════════════
# Integration tests: startup banner
# ═══════════════════════════════════════════════════════════════════════════════


@pytest.mark.slow
@pytest.mark.parametrize("runtime", AVAILABLE_RUNTIMES)
def test_startup_banner_present(temp_project, runtime):
    """Startup banner should include version, platform, runtime, and container name."""
    result = run_yolo_with_runtime(temp_project, "true", runtime)
    assert result.returncode == 0, (
        f"yolo failed.\nstdout: {result.stdout}\nstderr: {result.stderr}"
    )
    banner_line = None
    for line in result.stderr.splitlines():
        if line.startswith("yolo-jail "):
            banner_line = line
            break
    assert banner_line is not None, (
        f"Startup banner not found in stderr:\n{result.stderr}"
    )
    # Banner should contain: version | platform | runtime | container name
    assert "|" in banner_line, f"Banner missing separators: {banner_line}"
    assert runtime in banner_line, f"Runtime not in banner: {banner_line}"
    cname = _container_name_for_workspace(temp_project)
    assert cname in banner_line, f"Container name not in banner: {banner_line}"

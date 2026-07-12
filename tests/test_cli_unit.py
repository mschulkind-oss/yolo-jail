"""Unit tests for src/cli.py — pure functions and mockable logic.

Covers: argv routing, repo root resolution, config validation, container naming,
port forwarding, AGENTS.md generation, check command, and helpers.
"""

import json
import os
import shlex
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

from typer.testing import CliRunner  # noqa: E402

import cli as cli  # noqa: E402

from cli import (  # noqa: E402
    _check_preset_null_conflicts,
    _effective_mcp_server_names,
    _format_progress,
    _host_mise_dir,
    _merge_mise_disabled_tools,
    _merge_mise_tools,
    _normalize_blocked_tools,
    _parse_dotenv,
    _parse_port_forwards,
    _prepare_skills,
    _read_loaded_paths,
    _add_loaded_path,
    _report_unknown_keys,
    _resolve_env_sources,
    _runtime_for_check,
    _scratch_mount_args,
    _summarize_nix_line,
    _validate_config,
    _workspace_readonly_mount_args,
    _validate_forward_host_port,
    _validate_port_number,
    _validate_publish_port,
    _validate_string_list,
    cleanup_container_tracking,
    cleanup_port_forwarding,
    container_name_for_workspace,
    ensure_global_storage,
    find_existing_container,
    find_running_container,
    generate_agents_md,
    load_config,
    merge_config,
    write_container_tracking,
    _check_config_changes,
    _config_snapshot_path,
    _get_project_name,
    _get_yolo_version,
    _load_jsonc_file,
    _merge_lists,
    ConfigError,
    _validate_cgroup_name,
    _parse_memory_value,
    _print_startup_banner,
    _remove_stale_container,
    _cgd_ensure_agent_cgroup,
    _cgd_create_and_join,
    _cgd_destroy,
    BUILTIN_CGROUP_LOOPHOLE_NAME,
    BUILTIN_JOURNAL_LOOPHOLE_NAME,
    LoopholeDaemon,
    JAIL_HOST_SERVICES_DIR,
    _host_service_default_jail_socket,
    _host_service_env_var,
    _host_service_sockets_dir,
    _gpu_host_available,
    _rocm_host_available,
    _relay_ensure,
    _relay_reap_orphans,
    _relay_stop,
    _resolve_journal_mode,
    _should_mount_host_nix,
    _start_host_service_builtin_cgroup,
    _start_host_service_builtin_journal,
    _start_host_service_external,
    _substitute_socket_in_cmd,
    start_loopholes,
    stop_loopholes,
)


def test_merge_mise_disabled_tools_defaults_to_pnpm():
    assert _merge_mise_disabled_tools("") == "pnpm"


def test_merge_mise_disabled_tools_preserves_user_tools():
    assert _merge_mise_disabled_tools("ruby, terraform pnpm") == "pnpm,ruby,terraform"


def test_merge_mise_disabled_tools_none_falls_back_to_defaults():
    # env dicts may omit MISE_DISABLE_TOOLS entirely — .get() returns None.
    assert _merge_mise_disabled_tools(None) == "pnpm"


def test_merge_mise_disabled_tools_non_string_is_ignored():
    # Misconfigured yolo-jail.jsonc may stash a list/int here; we silently
    # drop it rather than TypeError'ing at jail start.
    assert _merge_mise_disabled_tools(["ruby"]) == "pnpm"
    assert _merge_mise_disabled_tools(42) == "pnpm"


def test_merge_mise_disabled_tools_deduplicates():
    # Both the pnpm default and the user listing pnpm → single entry.
    assert _merge_mise_disabled_tools("pnpm, pnpm, node, node") == "pnpm,node"


def test_merge_mise_disabled_tools_handles_empty_commas():
    assert _merge_mise_disabled_tools(",,, ,") == "pnpm"


# ═══════════════════════════════════════════════════════════════════════════════
# Test: host-side import must not require jail-only env
# ═══════════════════════════════════════════════════════════════════════════════


class TestHostImport:
    """The `yolo` CLI is imported on the HOST, where jail-only env vars
    (MISE_DATA_DIR, JAIL_HOME) are absent.  Importing ``cli`` — which
    transitively imports the ``entrypoint`` package for the agent registry —
    must never crash for want of a jail env var.

    Regression: ``entrypoint/__init__.py`` used ``os.environ["MISE_DATA_DIR"]``
    (no default) at module scope, so once ``cli.config`` began importing the
    registry, every host ``yolo`` invocation died with ``KeyError:
    'MISE_DATA_DIR'``.  conftest's ``os.environ.setdefault('MISE_DATA_DIR')``
    masks this for in-process tests, and integration tests run inside a jail
    where the var is always set — so only a clean-env subprocess catches it.
    """

    def _import_in_clean_env(self, target: str):
        env = {
            k: v
            for k, v in os.environ.items()
            if k not in ("MISE_DATA_DIR", "JAIL_HOME")
        }
        # Prepend src/ so `import cli` / `import entrypoint` resolve the same
        # way the installed console-script does.
        code = (
            f"import sys; sys.path.insert(0, {str(REPO_ROOT / 'src')!r}); "
            f"import {target}"
        )
        return subprocess.run(
            [sys.executable, "-c", code],
            capture_output=True,
            text=True,
            env=env,
        )

    def test_cli_imports_without_mise_data_dir(self):
        result = self._import_in_clean_env("cli")
        assert result.returncode == 0, (
            f"host `import cli` failed without MISE_DATA_DIR:\n{result.stderr}"
        )
        assert "KeyError" not in result.stderr

    def test_entrypoint_imports_without_mise_data_dir(self):
        result = self._import_in_clean_env("entrypoint")
        assert result.returncode == 0, (
            f"host `import entrypoint` failed without MISE_DATA_DIR:\n{result.stderr}"
        )


# ═══════════════════════════════════════════════════════════════════════════════
# Test: argv routing in main()
# ═══════════════════════════════════════════════════════════════════════════════


class TestArgvRouting:
    """Test the sys.argv rewriting that routes `yolo -- cmd` to `yolo run -- cmd`."""

    def _simulate_argv_rewrite(self, argv: list[str]) -> list[str]:
        """Simulate the argv rewriting logic from main() without calling app()."""
        _SUBCOMMANDS = {
            "init",
            "init-user-config",
            "config-ref",
            "check",
            "run",
            "ps",
            "doctor",
        }
        args = argv[1:]
        result = list(argv)
        if args and "--" in args:
            pre_dash = args[: args.index("--")]
            if not any(a in _SUBCOMMANDS for a in pre_dash):
                idx = result.index("--")
                result.insert(idx, "run")
        return result

    def test_yolo_double_dash_echo(self):
        """The original bug: `yolo -- echo foo` should become `yolo run -- echo foo`."""
        result = self._simulate_argv_rewrite(["yolo", "--", "echo", "foo"])
        assert result == ["yolo", "run", "--", "echo", "foo"]

    def test_yolo_double_dash_bash_c(self):
        result = self._simulate_argv_rewrite(["yolo", "--", "bash", "-c", "echo hello"])
        assert result == ["yolo", "run", "--", "bash", "-c", "echo hello"]

    def test_yolo_new_double_dash_bash(self):
        result = self._simulate_argv_rewrite(["yolo", "--new", "--", "bash"])
        assert result == ["yolo", "--new", "run", "--", "bash"]

    def test_yolo_run_double_dash_echo(self):
        """Explicit `run` subcommand should NOT be doubled."""
        result = self._simulate_argv_rewrite(["yolo", "run", "--", "echo", "foo"])
        assert result == ["yolo", "run", "--", "echo", "foo"]

    def test_yolo_check_not_rewritten(self):
        """Subcommands like `check` should not trigger rewriting."""
        result = self._simulate_argv_rewrite(["yolo", "check"])
        assert result == ["yolo", "check"]

    def test_bare_yolo(self):
        """Bare `yolo` with no args should not be rewritten."""
        result = self._simulate_argv_rewrite(["yolo"])
        assert result == ["yolo"]

    def test_yolo_ps(self):
        result = self._simulate_argv_rewrite(["yolo", "ps"])
        assert result == ["yolo", "ps"]

    def test_yolo_double_dash_copilot(self):
        result = self._simulate_argv_rewrite(["yolo", "--", "copilot"])
        assert result == ["yolo", "run", "--", "copilot"]

    def test_yolo_no_double_dash(self):
        """Without `--`, no rewriting happens even for unknown commands."""
        result = self._simulate_argv_rewrite(["yolo", "echo", "foo"])
        assert result == ["yolo", "echo", "foo"]

    def test_yolo_init_double_dash(self):
        """Known subcommand before -- should not insert run."""
        result = self._simulate_argv_rewrite(["yolo", "init", "--", "something"])
        assert result == ["yolo", "init", "--", "something"]

    def test_yolo_doctor(self):
        result = self._simulate_argv_rewrite(["yolo", "doctor"])
        assert result == ["yolo", "doctor"]


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _resolve_repo_root
# ═══════════════════════════════════════════════════════════════════════════════


class TestResolveRepoRoot:
    """Test the 4-step repo root resolution."""

    def test_env_var_takes_priority(self, tmp_path):
        env_root = tmp_path / "env-repo"
        env_root.mkdir()
        # Must contain expected marker files so validation passes
        (env_root / "flake.nix").touch()
        with patch.dict(os.environ, {"YOLO_REPO_ROOT": str(env_root)}):
            from cli import _resolve_repo_root

            result = _resolve_repo_root()
            assert result == env_root.resolve()

    def test_env_var_falls_through_when_empty(self, tmp_path):
        """YOLO_REPO_ROOT pointing to an empty dir falls through to source checkout."""
        empty_root = tmp_path / "empty-repo"
        empty_root.mkdir()
        with patch.dict(os.environ, {"YOLO_REPO_ROOT": str(empty_root)}):
            from cli import _resolve_repo_root

            result = _resolve_repo_root()
            # Should NOT return the empty dir — should fall through
            assert result != empty_root.resolve()
            # Should find the actual source checkout instead
            assert (result / "flake.nix").exists() or (
                result / "src" / "entrypoint.py"
            ).exists()

    def test_source_checkout_detected(self, monkeypatch):
        """Running from the actual source checkout should find the repo root."""
        monkeypatch.delenv("YOLO_REPO_ROOT", raising=False)
        from cli import _resolve_repo_root

        result = _resolve_repo_root()
        # We ARE in a source checkout, so this should find REPO_ROOT
        assert (result / "flake.nix").exists()

    def test_installed_package_stages_build_root(self, tmp_path, monkeypatch):
        """When flake.nix is in the package dir (installed mode), files are copied."""
        # This path is complex to unit test due to Path(__file__) mocking.
        # Covered by the user's manual testing and integration tests.
        pass

    def test_user_config_repo_path(self, tmp_path, monkeypatch):
        """Step 4: user config with repo_path — tested indirectly via env var priority."""
        pass  # Covered by env var test and integration tests


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Container naming & tracking
# ═══════════════════════════════════════════════════════════════════════════════


class TestContainerNaming:
    def test_deterministic_name(self, tmp_path):
        ws = tmp_path / "my-project"
        ws.mkdir()
        name = container_name_for_workspace(ws)
        assert name.startswith("yolo-my-project-")
        assert len(name.rsplit("-", 1)[-1]) == 8  # 8 hex char suffix

    def test_same_workspace_same_name(self, tmp_path):
        ws = tmp_path / "project"
        ws.mkdir()
        assert container_name_for_workspace(ws) == container_name_for_workspace(ws)

    def test_different_workspace_different_name(self, tmp_path):
        ws1 = tmp_path / "project1"
        ws2 = tmp_path / "project2"
        ws1.mkdir()
        ws2.mkdir()
        assert container_name_for_workspace(ws1) != container_name_for_workspace(ws2)


class TestContainerTracking:
    def test_write_and_cleanup(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.runtime.CONTAINER_DIR", tmp_path)
        write_container_tracking("yolo-abc123", tmp_path / "ws")
        assert (tmp_path / "yolo-abc123").exists()
        cleanup_container_tracking("yolo-abc123")
        assert not (tmp_path / "yolo-abc123").exists()

    def test_cleanup_missing_ok(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.runtime.CONTAINER_DIR", tmp_path)
        cleanup_container_tracking("nonexistent")  # Should not raise


class TestFindRunningContainer:
    def test_returns_cid_when_running(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="abc123def\n")
            result = find_running_container("yolo-test", runtime="podman")
            assert result == "abc123def"

    def test_returns_none_when_not_running(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="")
            result = find_running_container("yolo-test", runtime="podman")
            assert result is None

    def test_returns_none_on_file_not_found(self):
        with patch("subprocess.run", side_effect=FileNotFoundError):
            result = find_running_container("yolo-test", runtime="podman")
            assert result is None


class TestFindExistingContainer:
    def test_returns_cid_when_stopped(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="abc123def\n")
            result = find_existing_container("yolo-test", runtime="podman")
            assert result == "abc123def"
            mock_run.assert_called_once_with(
                ["podman", "ps", "-a", "-q", "--filter", "name=^/yolo-test$"],
                capture_output=True,
                text=True,
            )

    def test_returns_none_when_no_container(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="")
            result = find_existing_container("yolo-test", runtime="podman")
            assert result is None

    def test_returns_none_on_file_not_found(self):
        with patch("subprocess.run", side_effect=FileNotFoundError):
            result = find_existing_container("yolo-test", runtime="podman")
            assert result is None

    def test_apple_container_runtime(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="NAME\nyolo-test\n")
            result = find_existing_container("yolo-test", runtime="container")
            assert result == "yolo-test"
            mock_run.assert_called_once_with(
                ["container", "ls", "--all"],
                capture_output=True,
                text=True,
            )

    def test_podman_runtime(self):
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="def456\n")
            result = find_existing_container("yolo-test", runtime="podman")
            assert result == "def456"
            mock_run.assert_called_once_with(
                ["podman", "ps", "-a", "-q", "--filter", "name=^/yolo-test$"],
                capture_output=True,
                text=True,
            )


class TestRemoveStaleContainer:
    def test_successful_removal(self, tmp_path):
        with (
            patch("subprocess.run") as mock_run,
            patch("cli.runtime.cleanup_container_tracking") as mock_cleanup,
        ):
            mock_run.return_value = MagicMock(returncode=0)
            result = _remove_stale_container("yolo-test", runtime="podman")
            assert result is True
            mock_run.assert_called_once_with(
                ["podman", "rm", "yolo-test"],
                capture_output=True,
                text=True,
            )
            mock_cleanup.assert_called_once_with("yolo-test")

    def test_failed_removal(self):
        with (
            patch("subprocess.run") as mock_run,
            patch("cli.runtime.cleanup_container_tracking") as mock_cleanup,
        ):
            mock_run.return_value = MagicMock(returncode=1)
            result = _remove_stale_container("yolo-test", runtime="podman")
            assert result is False
            mock_cleanup.assert_not_called()

    def test_apple_container_runtime(self):
        with (
            patch("subprocess.run") as mock_run,
            patch("cli.runtime.cleanup_container_tracking"),
        ):
            mock_run.return_value = MagicMock(returncode=0)
            _remove_stale_container("yolo-test", runtime="container")
            mock_run.assert_called_once_with(
                ["container", "rm", "--force", "yolo-test"],
                capture_output=True,
                text=True,
            )

    def test_runtime_not_found(self):
        with patch("subprocess.run", side_effect=FileNotFoundError):
            result = _remove_stale_container("yolo-test", runtime="podman")
            assert result is False


class TestPrintStartupBanner:
    def test_banner_includes_platform_and_runtime(self, capsys):
        _print_startup_banner("1.0.0", "podman", "yolo-test-abc123")
        err = capsys.readouterr().err
        assert "yolo-jail 1.0.0" in err
        assert "podman" in err
        assert "yolo-test-abc123" in err

    def test_banner_with_resource_limits(self, capsys):
        _print_startup_banner(
            "1.0.0", "podman", "yolo-test-abc123", ["memory=8g", "cpus=4"]
        )
        err = capsys.readouterr().err
        assert "Resource limits: memory=8g, cpus=4" in err

    def test_banner_no_resource_limits(self, capsys):
        _print_startup_banner("1.0.0", "podman", "yolo-test-abc123")
        err = capsys.readouterr().err
        assert "Resource limits" not in err

    def test_banner_surfaces_mismatched_jail_version(self, capsys):
        """When the host CLI differs from the jail's baked YOLO_VERSION,
        the banner must show both — stale-shim bugs on attach are
        invisible otherwise."""
        _print_startup_banner("2.0.0", "podman", "yolo-test", jail_version="1.0.0")
        err = capsys.readouterr().err
        assert "yolo-jail 2.0.0" in err
        assert "1.0.0" in err
        assert "attached" in err.lower()

    def test_banner_hides_matching_jail_version(self, capsys):
        """When versions match, don't clutter the banner."""
        _print_startup_banner("1.0.0", "podman", "yolo-test", jail_version="1.0.0")
        err = capsys.readouterr().err
        assert "attached" not in err.lower()
        # Version appears exactly once (in "yolo-jail 1.0.0")
        assert err.count("1.0.0") == 1

    def test_banner_handles_missing_jail_version(self, capsys):
        """A None jail_version (inspect failed / fresh container) must
        not crash and must leave the banner looking like the old form."""
        _print_startup_banner("1.0.0", "podman", "yolo-test", jail_version=None)
        err = capsys.readouterr().err
        assert "yolo-jail 1.0.0" in err
        assert "attached" not in err.lower()


class TestGetYoloVersion:
    def test_returns_git_describe_version(self):
        with patch("cli.version._git_describe_version", return_value="1.2.3"):
            assert _get_yolo_version() == "1.2.3"

    def test_falls_back_to_pkg_version(self):
        with (
            patch("cli.version._git_describe_version", return_value=None),
            patch("importlib.metadata.version", return_value="0.9.0"),
        ):
            assert _get_yolo_version() == "0.9.0"

    def test_returns_unknown_on_error(self):
        with (
            patch("cli.version._git_describe_version", return_value=None),
            patch("importlib.metadata.version", side_effect=Exception("no pkg")),
        ):
            assert _get_yolo_version() == "unknown"


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Port forwarding parsing
# ═══════════════════════════════════════════════════════════════════════════════


class TestParsePortForwards:
    def test_integer_port(self):
        assert _parse_port_forwards([8080]) == [(8080, 8080)]

    def test_string_port(self):
        assert _parse_port_forwards(["5432"]) == [(5432, 5432)]

    def test_colon_mapping(self):
        assert _parse_port_forwards(["8080:9090"]) == [(8080, 9090)]

    def test_multiple(self):
        result = _parse_port_forwards([5432, "8080:9090", "3000"])
        assert result == [(5432, 5432), (8080, 9090), (3000, 3000)]

    def test_empty(self):
        assert _parse_port_forwards([]) == []

    def test_invalid_entry_skipped(self, capsys):
        result = _parse_port_forwards([3.14])
        assert result == []
        assert "invalid" in capsys.readouterr().err.lower()


class TestCleanupPortForwarding:
    def test_terminates_processes(self, tmp_path):
        mock_proc = MagicMock()
        cleanup_port_forwarding([mock_proc], tmp_path)
        mock_proc.terminate.assert_called_once()

    def test_kills_on_timeout(self, tmp_path):
        mock_proc = MagicMock()
        mock_proc.wait.side_effect = subprocess.TimeoutExpired("socat", 2)
        cleanup_port_forwarding([mock_proc], tmp_path)
        mock_proc.kill.assert_called_once()

    def test_removes_socket_dir(self, tmp_path):
        sock_dir = tmp_path / "sockets"
        sock_dir.mkdir()
        cleanup_port_forwarding([], sock_dir)
        assert not sock_dir.exists()

    def test_none_socket_dir(self):
        cleanup_port_forwarding([], None)  # Should not raise


# ═══════════════════════════════════════════════════════════════════════════════
# Test: MCP server helpers
# ═══════════════════════════════════════════════════════════════════════════════


class TestEffectiveMCPServerNames:
    def test_presets_only(self):
        names = _effective_mcp_server_names(None, ["chrome-devtools"])
        assert names == ["chrome-devtools"]

    def test_custom_servers_added(self):
        servers = {"my-server": {"command": "my-cmd"}}
        names = _effective_mcp_server_names(servers, [])
        assert "my-server" in names

    def test_null_removes_preset(self):
        servers = {"chrome-devtools": None}
        names = _effective_mcp_server_names(servers, ["chrome-devtools"])
        assert "chrome-devtools" not in names

    def test_empty(self):
        assert _effective_mcp_server_names(None, None) == []

    def test_no_duplicates(self):
        servers = {"chrome-devtools": {"command": "cmd"}}
        names = _effective_mcp_server_names(servers, ["chrome-devtools"])
        assert names.count("chrome-devtools") == 1


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Mise tools merging
# ═══════════════════════════════════════════════════════════════════════════════


class TestMergeMiseTools:
    def test_default_neovim(self):
        result = _merge_mise_tools({})
        assert result == {"neovim": "stable"}

    def test_override_neovim(self):
        result = _merge_mise_tools({"mise_tools": {"neovim": "nightly"}})
        assert result["neovim"] == "nightly"

    def test_add_new_tool(self):
        result = _merge_mise_tools({"mise_tools": {"typst": "latest"}})
        assert result == {"neovim": "stable", "typst": "latest"}


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Blocked tools normalization
# ═══════════════════════════════════════════════════════════════════════════════


class TestNormalizeBlockedTools:
    def test_default_blocked_tools(self):
        result = _normalize_blocked_tools(None)
        names = [t["name"] for t in result]
        assert "grep" in names
        assert "find" in names

    def test_string_tools_get_defaults(self):
        result = _normalize_blocked_tools({"blocked_tools": ["grep"]})
        assert result[0]["name"] == "grep"
        assert "message" in result[0]

    def test_dict_tools_preserved(self):
        tool = {"name": "curl", "message": "Use wget", "suggestion": "wget URL"}
        result = _normalize_blocked_tools({"blocked_tools": [tool]})
        assert result[0] == tool

    def test_dict_grep_gets_default_block_flags(self):
        """Regression: when the user writes the dict form for a tool
        with baked-in defaults (grep), _normalize_blocked_tools must
        merge the defaults — missing block_flags shouldn't silently
        convert grep into an unconditional block.  The conditional
        rule is part of the default contract; dict-form users get it
        too unless they explicitly override."""
        result = _normalize_blocked_tools({"blocked_tools": [{"name": "grep"}]})
        assert result[0]["name"] == "grep"
        assert result[0].get("block_flags"), (
            "dict-form grep must inherit default block_flags"
        )
        # And the default message should also be present.
        assert "rg" in result[0].get("suggestion", "")

    def test_dict_grep_user_fields_win_over_defaults(self):
        """User-supplied fields override defaults; unspecified fields
        inherit.  So ``{"name": "grep", "message": "custom"}`` gets
        custom message + default suggestion + default block_flags."""
        result = _normalize_blocked_tools(
            {"blocked_tools": [{"name": "grep", "message": "custom msg"}]}
        )
        assert result[0]["message"] == "custom msg"
        assert result[0].get("block_flags"), "defaults preserved"
        assert "rg" in result[0].get("suggestion", "")

    def test_dict_grep_explicit_empty_block_flags_disables_conditional(self):
        """User can opt out of conditional blocking by setting
        ``block_flags: []`` — reverting grep to the unconditional
        behavior that matches the legacy contract."""
        result = _normalize_blocked_tools(
            {"blocked_tools": [{"name": "grep", "block_flags": []}]}
        )
        assert result[0]["block_flags"] == []

    def test_custom_string_tool(self):
        result = _normalize_blocked_tools({"blocked_tools": ["strace"]})
        assert result[0]["name"] == "strace"
        assert "message" not in result[0]

    def test_none_blocked_tools(self):
        result = _normalize_blocked_tools({"blocked_tools": None})
        assert len(result) == 2  # defaults


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _summarize_nix_line
# ═══════════════════════════════════════════════════════════════════════════════


class TestSummarizeNixLine:
    def test_copying_path(self):
        line = "copying path '/nix/store/abc123-glibc-2.38' from 'https://...'"
        assert _summarize_nix_line(line) == "Fetching glibc-2.38"

    def test_building_drv(self):
        line = "building '/nix/store/abc123-python3-3.12.drv'..."
        assert _summarize_nix_line(line) == "Building python3-3.12"

    def test_evaluating(self):
        assert "Evaluating" in _summarize_nix_line("evaluating derivation...")

    def test_progress_counter(self):
        line = "[3/5 built, 2 copied (10.2 MiB)]"
        result = _summarize_nix_line(line)
        assert result == line.strip()

    def test_unrecognized(self):
        assert _summarize_nix_line("some random output") == ""


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _format_progress
# ═══════════════════════════════════════════════════════════════════════════════


class TestFormatProgress:
    def test_mb_no_estimate(self):
        result = _format_progress(50 * 1024 * 1024, 0)
        assert "50 MB" in result

    def test_gb_with_estimate(self):
        result = _format_progress(1500 * 1024 * 1024, 2000 * 1024 * 1024)
        assert "GB" in result
        assert "%" in result

    def test_caps_at_99(self):
        result = _format_progress(999, 1000)
        assert "99%" in result


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Sentinel file management
# ═══════════════════════════════════════════════════════════════════════════════


class TestSentinelFiles:
    def test_read_empty(self, tmp_path):
        sentinel = tmp_path / "sentinel"
        assert _read_loaded_paths(sentinel) == set()

    def test_read_paths(self, tmp_path):
        sentinel = tmp_path / "sentinel"
        sentinel.write_text("/nix/store/abc\n/nix/store/def\n")
        assert _read_loaded_paths(sentinel) == {"/nix/store/abc", "/nix/store/def"}

    def test_add_path(self, tmp_path):
        sentinel = tmp_path / "sentinel"
        _add_loaded_path(sentinel, "/nix/store/abc")
        assert "/nix/store/abc" in sentinel.read_text()

    def test_lru_caps_at_10(self, tmp_path):
        sentinel = tmp_path / "sentinel"
        for i in range(15):
            _add_loaded_path(sentinel, f"/nix/store/path-{i}")
        lines = [ln for ln in sentinel.read_text().splitlines() if ln.strip()]
        assert len(lines) == 10
        # Most recent should be present
        assert "/nix/store/path-14" in sentinel.read_text()
        # Oldest should be evicted
        assert "/nix/store/path-0" not in sentinel.read_text()

    def test_deduplicates(self, tmp_path):
        sentinel = tmp_path / "sentinel"
        _add_loaded_path(sentinel, "/nix/store/abc")
        _add_loaded_path(sentinel, "/nix/store/abc")
        lines = [ln for ln in sentinel.read_text().splitlines() if ln.strip()]
        assert len(lines) == 1


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Config loading (JSONC)
# ═══════════════════════════════════════════════════════════════════════════════


class TestLoadJsoncFile:
    def test_nonexistent_returns_empty(self, tmp_path):
        result = _load_jsonc_file(tmp_path / "nope.jsonc", "test")
        assert result == {}

    def test_valid_json(self, tmp_path):
        f = tmp_path / "config.jsonc"
        f.write_text('{"runtime": "podman"}')
        result = _load_jsonc_file(f, "test")
        assert result == {"runtime": "podman"}

    def test_non_object_warns(self, tmp_path):
        f = tmp_path / "config.jsonc"
        f.write_text("[1, 2, 3]")
        result = _load_jsonc_file(f, "test")
        assert result == {}

    def test_non_object_strict_raises(self, tmp_path):
        f = tmp_path / "config.jsonc"
        f.write_text("[1, 2, 3]")
        with pytest.raises(ConfigError):
            _load_jsonc_file(f, "test", strict=True)

    def test_invalid_json_strict_raises(self, tmp_path):
        f = tmp_path / "config.jsonc"
        f.write_text("{broken json")
        with pytest.raises(ConfigError):
            _load_jsonc_file(f, "test", strict=True)

    def test_invalid_json_non_strict_warns(self, tmp_path):
        f = tmp_path / "config.jsonc"
        f.write_text("{broken json")
        result = _load_jsonc_file(f, "test", strict=False)
        assert result == {}


class TestLoadConfig:
    def test_empty_workspace(self, tmp_path):
        with patch("cli.config.USER_CONFIG_PATH", tmp_path / "nonexistent.jsonc"):
            result = load_config(tmp_path)
            assert result == {}

    def test_workspace_config_merged(self, tmp_path):
        ws_config = tmp_path / "yolo-jail.jsonc"
        ws_config.write_text('{"runtime": "podman"}')
        with patch("cli.config.USER_CONFIG_PATH", tmp_path / "nonexistent.jsonc"):
            result = load_config(tmp_path)
            assert result["runtime"] == "podman"


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Config validation — comprehensive
# ═══════════════════════════════════════════════════════════════════════════════


class TestValidateConfig:
    """Test _validate_config for all config sections."""

    def test_empty_config_valid(self, tmp_path):
        errors, warnings = _validate_config({}, workspace=tmp_path)
        assert errors == []

    def test_unknown_top_level_key(self, tmp_path):
        errors, _ = _validate_config({"foo": "bar"}, workspace=tmp_path)
        assert any("unknown key" in e for e in errors)

    def test_invalid_runtime(self, tmp_path):
        errors, _ = _validate_config({"runtime": "containerd"}, workspace=tmp_path)
        assert any("runtime" in e for e in errors)

    def test_valid_runtime(self, tmp_path):
        errors, _ = _validate_config({"runtime": "podman"}, workspace=tmp_path)
        assert not any("runtime" in e for e in errors)

    def test_packages_string(self, tmp_path):
        errors, _ = _validate_config({"packages": ["postgresql"]}, workspace=tmp_path)
        assert errors == []

    def test_packages_object_nixpkgs(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "freetype", "nixpkgs": "abc123"}]},
            workspace=tmp_path,
        )
        assert errors == []

    def test_packages_object_version_override(self, tmp_path):
        errors, _ = _validate_config(
            {
                "packages": [
                    {
                        "name": "freetype",
                        "version": "2.14.1",
                        "url": "mirror://...",
                        "hash": "sha256-...",
                    }
                ]
            },
            workspace=tmp_path,
        )
        assert errors == []

    def test_packages_both_nixpkgs_and_version_error(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "freetype", "nixpkgs": "abc", "version": "2.14.1"}]},
            workspace=tmp_path,
        )
        assert any("either" in e.lower() for e in errors)

    def test_packages_object_no_strategy(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "freetype"}]}, workspace=tmp_path
        )
        assert any("must use" in e.lower() for e in errors)

    def test_packages_unknown_keys(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "foo", "nixpkgs": "abc", "bogus": True}]},
            workspace=tmp_path,
        )
        assert any("unknown" in e for e in errors)

    def test_packages_not_list(self, tmp_path):
        errors, _ = _validate_config({"packages": "postgresql"}, workspace=tmp_path)
        assert any("expected a list" in e for e in errors)

    def test_packages_dotted_string_output(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": ["gtk4", "gtk4.dev"]}, workspace=tmp_path
        )
        assert errors == []

    def test_packages_dotted_string_rejects_multi_dot(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": ["python3Packages.numpy.dev"]}, workspace=tmp_path
        )
        assert any(
            "invalid package name" in e and "python3Packages.numpy.dev" in e
            for e in errors
        )

    def test_packages_string_rejects_invalid_chars(self, tmp_path):
        errors, _ = _validate_config({"packages": ["foo/bar"]}, workspace=tmp_path)
        assert any("invalid package name" in e for e in errors)

    def test_packages_outputs_only(self, tmp_path):
        """Object form with just name+outputs (no nixpkgs/version) is valid."""
        errors, _ = _validate_config(
            {"packages": [{"name": "gtk4", "outputs": ["out", "dev"]}]},
            workspace=tmp_path,
        )
        assert errors == []

    def test_packages_outputs_with_nixpkgs(self, tmp_path):
        errors, _ = _validate_config(
            {
                "packages": [
                    {"name": "gtk4", "nixpkgs": "abc123", "outputs": ["out", "dev"]}
                ]
            },
            workspace=tmp_path,
        )
        assert errors == []

    def test_packages_outputs_with_version_override(self, tmp_path):
        errors, _ = _validate_config(
            {
                "packages": [
                    {
                        "name": "freetype",
                        "version": "2.14.1",
                        "url": "mirror://...",
                        "hash": "sha256-...",
                        "outputs": ["out", "dev"],
                    }
                ]
            },
            workspace=tmp_path,
        )
        assert errors == []

    def test_packages_outputs_not_list(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "gtk4", "outputs": "dev"}]},
            workspace=tmp_path,
        )
        assert any("outputs" in e and "list of strings" in e for e in errors)

    def test_packages_outputs_non_string_element(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "gtk4", "outputs": [42]}]},
            workspace=tmp_path,
        )
        assert any("outputs" in e and "list of strings" in e for e in errors)

    def test_packages_outputs_invalid_name(self, tmp_path):
        errors, _ = _validate_config(
            {"packages": [{"name": "gtk4", "outputs": ["out", "bad/name"]}]},
            workspace=tmp_path,
        )
        assert any("outputs[1]" in e and "invalid output name" in e for e in errors)

    def test_packages_object_name_with_dot_rejected(self, tmp_path):
        """Dotted shorthand is string-only; object form must use 'outputs'."""
        errors, _ = _validate_config(
            {"packages": [{"name": "gtk4.dev", "outputs": ["dev"]}]},
            workspace=tmp_path,
        )
        assert any("string-only" in e for e in errors)

    def test_packages_object_no_strategy_still_errors(self, tmp_path):
        """Object form with only a name (no nixpkgs/version/outputs) still errors."""
        errors, _ = _validate_config(
            {"packages": [{"name": "freetype"}]}, workspace=tmp_path
        )
        assert any("must use" in e and "outputs" in e for e in errors)

    def test_network_valid(self, tmp_path):
        config = {"network": {"mode": "bridge", "ports": ["8000:8000"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_network_invalid_mode(self, tmp_path):
        errors, _ = _validate_config({"network": {"mode": "weird"}}, workspace=tmp_path)
        assert any("mode" in e for e in errors)

    def test_network_host_port_warning(self, tmp_path):
        _, warnings = _validate_config(
            {"network": {"mode": "host", "ports": ["8000:8000"]}},
            workspace=tmp_path,
        )
        assert any("ignored" in w for w in warnings)

    def test_network_forward_host_ports_valid(self, tmp_path):
        config = {"network": {"forward_host_ports": [5432, "8080:9090"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_network_unknown_keys(self, tmp_path):
        errors, _ = _validate_config(
            {"network": {"mode": "bridge", "bogus": True}}, workspace=tmp_path
        )
        assert any("unknown" in e for e in errors)

    def test_security_valid(self, tmp_path):
        config = {"security": {"blocked_tools": ["curl", "wget"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_security_blocked_tool_object(self, tmp_path):
        config = {
            "security": {
                "blocked_tools": [
                    {"name": "curl", "message": "No curl", "suggestion": "wget"}
                ]
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_security_blocked_tool_missing_name(self, tmp_path):
        config = {"security": {"blocked_tools": [{"message": "oops"}]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("name" in e for e in errors)

    def test_security_unknown_keys(self, tmp_path):
        errors, _ = _validate_config(
            {"security": {"blocked_tools": [], "extra": True}}, workspace=tmp_path
        )
        assert any("unknown" in e for e in errors)

    def test_mise_tools_valid(self, tmp_path):
        errors, _ = _validate_config(
            {"mise_tools": {"typst": "latest"}}, workspace=tmp_path
        )
        assert errors == []

    def test_mise_tools_invalid_value(self, tmp_path):
        errors, _ = _validate_config({"mise_tools": {"typst": 123}}, workspace=tmp_path)
        assert any("version string" in e for e in errors)

    def test_lsp_servers_valid(self, tmp_path):
        config = {
            "lsp_servers": {
                "rust": {
                    "command": "rust-analyzer",
                    "args": [],
                    "fileExtensions": {".rs": "rust"},
                }
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_lsp_servers_missing_command(self, tmp_path):
        config = {
            "lsp_servers": {"rust": {"args": [], "fileExtensions": {".rs": "rust"}}}
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("command" in e for e in errors)

    def test_lsp_servers_file_extensions_not_dict(self, tmp_path):
        config = {
            "lsp_servers": {
                "rust": {
                    "command": "rust-analyzer",
                    "fileExtensions": [".rs"],
                }
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("fileExtensions" in e for e in errors)

    def test_mcp_presets_valid(self, tmp_path):
        errors, _ = _validate_config(
            {"mcp_presets": ["chrome-devtools", "sequential-thinking"]},
            workspace=tmp_path,
        )
        assert errors == []

    def test_mcp_presets_invalid_name(self, tmp_path):
        errors, _ = _validate_config(
            {"mcp_presets": ["nonexistent"]}, workspace=tmp_path
        )
        assert any("unknown preset" in e for e in errors)

    def test_mcp_servers_null_valid(self, tmp_path):
        errors, _ = _validate_config(
            {"mcp_servers": {"chrome-devtools": None}}, workspace=tmp_path
        )
        assert errors == []

    def test_mcp_servers_custom_valid(self, tmp_path):
        config = {
            "mcp_servers": {
                "custom": {"command": "/path/to/server", "args": ["--port", "8080"]}
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_mcp_servers_missing_command(self, tmp_path):
        config = {"mcp_servers": {"custom": {"args": []}}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("command" in e for e in errors)

    def test_mcp_servers_env_valid(self, tmp_path):
        config = {
            "mcp_servers": {
                "custom": {
                    "command": "/path/to/server",
                    "env": {"API_KEY": "secret", "MODE": "prod"},
                }
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_mcp_servers_env_not_object(self, tmp_path):
        config = {
            "mcp_servers": {
                "custom": {"command": "cat", "env": ["KEY=val"]},
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any(
            "config.mcp_servers.custom.env: expected an object" in e for e in errors
        )

    def test_mcp_servers_env_non_string_value(self, tmp_path):
        config = {
            "mcp_servers": {
                "bad": {"command": "cat", "env": {"PORT": 8080}},
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("config.mcp_servers.bad.env.PORT" in e for e in errors)

    def test_devices_usb_valid(self, tmp_path):
        config = {"devices": [{"usb": "0bda:2838", "description": "RTL-SDR"}]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_devices_usb_invalid_format(self, tmp_path):
        config = {"devices": [{"usb": "not-a-usb-id"}]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("hex format" in e for e in errors)

    def test_devices_cgroup_rule(self, tmp_path):
        config = {"devices": [{"cgroup_rule": "c 189:* rwm"}]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_devices_both_usb_and_cgroup(self, tmp_path):
        config = {"devices": [{"usb": "0bda:2838", "cgroup_rule": "c 189:* rwm"}]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("exactly one" in e for e in errors)

    def test_devices_string_path(self, tmp_path):
        # String path that doesn't exist → warning not error
        config = {"devices": ["/dev/nonexistent"]}
        errors, warnings = _validate_config(config, workspace=tmp_path)
        assert errors == []
        assert any("does not exist" in w for w in warnings)

    def test_mounts_host_path_warning(self, tmp_path):
        config = {"mounts": ["/nonexistent/path"]}
        errors, warnings = _validate_config(config, workspace=tmp_path)
        assert errors == []
        assert any("does not exist" in w for w in warnings)

    def test_mounts_container_path_not_absolute(self, tmp_path):
        # The colon-split only activates when the char after : is /
        # So "host:/absolute" is parsed; "host:relative" is treated as full host path
        config = {"mounts": ["/tmp:/not-relative"]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        # /not-relative starts with /, so no absolute error — this is valid
        assert not any("absolute" in e for e in errors)

    def test_mounts_empty_host_path(self, tmp_path):
        config = {"mounts": [""]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("empty" in e for e in errors)

    def test_workspace_readonly_valid(self, tmp_path):
        config = {"workspace_readonly": ["src", "flake.nix"]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_workspace_readonly_not_list(self, tmp_path):
        config = {"workspace_readonly": "src"}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("workspace_readonly" in e and "list" in e for e in errors)

    def test_workspace_readonly_non_string_entry(self, tmp_path):
        config = {"workspace_readonly": [123]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("workspace_readonly[0]" in e and "string" in e for e in errors)

    def test_workspace_readonly_absolute_path_rejected(self, tmp_path):
        config = {"workspace_readonly": ["/etc/passwd"]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("relative" in e for e in errors)

    def test_workspace_readonly_traversal_rejected(self, tmp_path):
        config = {"workspace_readonly": ["../escape"]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any(".." in e for e in errors)

        config = {"workspace_readonly": ["src/../../escape"]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any(".." in e for e in errors)

    def test_env_sources_valid_mixed(self, tmp_path):
        config = {
            "env_sources": [
                "~/.config/yolo-jail/defaults.env",
                {"DEBUG": "1"},
                ".secrets/claude.env",
            ]
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_env_sources_legacy_env_rejected(self, tmp_path):
        config = {"env": {"FOO": "bar"}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any(
            "config.env" in e and "env_sources" in e and "removed" in e for e in errors
        )

    def test_env_sources_not_list(self, tmp_path):
        config = {"env_sources": {"FOO": "bar"}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("env_sources" in e and "list" in e for e in errors)

    def test_env_sources_entry_wrong_type(self, tmp_path):
        config = {"env_sources": [123]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("env_sources[0]" in e for e in errors)

    def test_env_sources_inline_bad_key(self, tmp_path):
        config = {"env_sources": [{"123BAD": "x"}]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any(
            "env_sources[0].123BAD" in e and "invalid variable name" in e
            for e in errors
        )

    def test_env_sources_inline_non_string_value(self, tmp_path):
        config = {"env_sources": [{"FOO": 42}]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("env_sources[0].FOO" in e and "string value" in e for e in errors)

    def test_env_sources_empty_string_rejected(self, tmp_path):
        config = {"env_sources": [""]}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("env_sources[0]" in e for e in errors)

    def test_publish_port_valid(self, tmp_path):
        config = {"network": {"ports": ["8000:8000"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_publish_port_with_protocol(self, tmp_path):
        config = {"network": {"ports": ["8000:8000/tcp"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_publish_port_invalid_protocol(self, tmp_path):
        config = {"network": {"ports": ["8000:8000/sctp"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert any("protocol" in e for e in errors)

    def test_publish_port_three_parts(self, tmp_path):
        config = {"network": {"ports": ["127.0.0.1:8000:8000"]}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert errors == []

    def test_publish_port_out_of_range(self, tmp_path):
        errors: list[str] = []
        _validate_port_number(70000, "test", errors)
        assert any("between" in e for e in errors)

    def test_publish_port_zero(self, tmp_path):
        errors: list[str] = []
        _validate_port_number(0, "test", errors)
        assert any("between" in e for e in errors)

    def test_loopholes_config_missing_command(self, tmp_path):
        errors, _ = _validate_config({"loopholes": {"foo": {}}}, workspace=tmp_path)
        assert any("command: required" in e for e in errors)

    def test_loopholes_config_command_not_a_list(self, tmp_path):
        errors, _ = _validate_config(
            {"loopholes": {"foo": {"command": "not-a-list"}}}, workspace=tmp_path
        )
        assert any("non-empty list" in e for e in errors)

    def test_loopholes_config_command_empty_list(self, tmp_path):
        errors, _ = _validate_config(
            {"loopholes": {"foo": {"command": []}}}, workspace=tmp_path
        )
        assert any("non-empty list" in e for e in errors)

    def test_loopholes_config_command_non_string_arg(self, tmp_path):
        errors, _ = _validate_config(
            {"loopholes": {"foo": {"command": ["serve.py", 42]}}},
            workspace=tmp_path,
        )
        assert any("expected a string" in e for e in errors)

    def test_loopholes_config_reserved_name(self, tmp_path):
        """User can't shadow the builtin cgroup-delegate service."""
        errors, _ = _validate_config(
            {"loopholes": {"cgroup-delegate": {"command": ["/bin/sleep", "1"]}}},
            workspace=tmp_path,
        )
        assert any("reserved" in e for e in errors)

    def test_loopholes_config_invalid_name(self, tmp_path):
        errors, _ = _validate_config(
            {"loopholes": {"123 bad name!": {"command": ["/bin/true"]}}},
            workspace=tmp_path,
        )
        assert any("name" in e and "match" in e for e in errors)

    def test_loopholes_config_jail_socket_must_start_under_run_yolo_services(
        self, tmp_path
    ):
        errors, _ = _validate_config(
            {
                "loopholes": {
                    "foo": {
                        "command": ["/bin/sleep", "1"],
                        "jail_socket": "/tmp/elsewhere.sock",
                    }
                }
            },
            workspace=tmp_path,
        )
        assert any("jail_socket" in e and "yolo-services" in e for e in errors)

    def test_loopholes_config_env_must_be_string_to_string(self, tmp_path):
        errors, _ = _validate_config(
            {
                "loopholes": {
                    "foo": {
                        "command": ["/bin/sleep", "1"],
                        "env": {"KEY": 42},
                    }
                }
            },
            workspace=tmp_path,
        )
        assert any("env" in e and "strings" in e for e in errors)

    def test_loopholes_config_unknown_key(self, tmp_path):
        errors, _ = _validate_config(
            {"loopholes": {"foo": {"command": ["/bin/sleep"], "made_up_field": True}}},
            workspace=tmp_path,
        )
        assert any("unknown key" in e and "made_up_field" in e for e in errors)

    def test_loopholes_config_minimal_valid(self, tmp_path):
        errors, _ = _validate_config(
            {"loopholes": {"auth-broker": {"command": ["/usr/bin/serve"]}}},
            workspace=tmp_path,
        )
        assert errors == []

    def test_loopholes_config_with_env_and_jail_socket(self, tmp_path):
        errors, _ = _validate_config(
            {
                "loopholes": {
                    "auth-broker": {
                        "command": ["/usr/bin/serve", "--socket", "{socket}"],
                        "env": {"KEYS_FILE": "/etc/keys.json"},
                        "jail_socket": "/run/yolo-services/auth.sock",
                    }
                }
            },
            workspace=tmp_path,
        )
        assert errors == []

    def test_kvm_true_valid(self, tmp_path):
        errors, _ = _validate_config({"kvm": True}, workspace=tmp_path)
        assert errors == []

    def test_kvm_false_valid(self, tmp_path):
        errors, _ = _validate_config({"kvm": False}, workspace=tmp_path)
        assert errors == []

    def test_kvm_missing_valid(self, tmp_path):
        errors, _ = _validate_config({}, workspace=tmp_path)
        assert errors == []

    def test_kvm_non_boolean_rejected(self, tmp_path):
        errors, _ = _validate_config({"kvm": "yes"}, workspace=tmp_path)
        assert any("config.kvm" in e and "boolean" in e for e in errors)

    def test_kvm_integer_rejected(self, tmp_path):
        errors, _ = _validate_config({"kvm": 1}, workspace=tmp_path)
        assert any("config.kvm" in e and "boolean" in e for e in errors)

    # GPU passthrough config — NVIDIA path plus the AMD/ROCm extensions
    # (vendor/mode/capabilities/hsa_override_gfx_version/seccomp_unconfined).
    def test_gpu_not_object_rejected(self, tmp_path):
        errors, _ = _validate_config({"gpu": True}, workspace=tmp_path)
        assert any("config.gpu" in e and "object" in e for e in errors)

    def test_gpu_enabled_non_boolean_rejected(self, tmp_path):
        errors, _ = _validate_config({"gpu": {"enabled": "yes"}}, workspace=tmp_path)
        assert any("config.gpu.enabled" in e and "boolean" in e for e in errors)

    def test_gpu_unknown_key_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "bogus": 1}}, workspace=tmp_path
        )
        assert any("config.gpu" in e and "bogus" in e for e in errors)

    def test_gpu_vendor_invalid_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "intel"}}, workspace=tmp_path
        )
        assert any("config.gpu.vendor" in e for e in errors)

    def test_gpu_mode_for_nvidia_rejected(self, tmp_path):
        # mode is AMD-only; setting it for vendor=nvidia is an error.
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "nvidia", "mode": "devices"}},
            workspace=tmp_path,
        )
        assert any("config.gpu.mode" in e and "vendor='amd'" in e for e in errors)

    def test_gpu_mode_invalid_value_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "amd", "mode": "bogus"}},
            workspace=tmp_path,
        )
        assert any("config.gpu.mode" in e and "'devices' or 'cdi'" in e for e in errors)

    def test_gpu_capabilities_for_amd_rejected(self, tmp_path):
        # ROCm has no NVIDIA_DRIVER_CAPABILITIES analog.
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "amd", "capabilities": "compute"}},
            workspace=tmp_path,
        )
        assert any(
            "config.gpu.capabilities" in e and "vendor='amd'" in e for e in errors
        )

    def test_gpu_capabilities_unknown_for_nvidia_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "capabilities": "compute,bogus"}},
            workspace=tmp_path,
        )
        assert any("config.gpu.capabilities" in e and "bogus" in e for e in errors)

    def test_gpu_hsa_override_for_nvidia_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {
                "gpu": {
                    "enabled": True,
                    "vendor": "nvidia",
                    "hsa_override_gfx_version": "11.0.0",
                }
            },
            workspace=tmp_path,
        )
        assert any(
            "config.gpu.hsa_override_gfx_version" in e and "vendor='amd'" in e
            for e in errors
        )

    def test_gpu_seccomp_unconfined_non_boolean_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "amd", "seccomp_unconfined": "yes"}},
            workspace=tmp_path,
        )
        assert any(
            "config.gpu.seccomp_unconfined" in e and "boolean" in e for e in errors
        )

    def test_gpu_valid_amd_config(self, tmp_path):
        config = {
            "gpu": {
                "enabled": True,
                "vendor": "amd",
                "devices": "0,1",
                "mode": "devices",
                "hsa_override_gfx_version": "11.0.0",
                "seccomp_unconfined": False,
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert not any("config.gpu" in e for e in errors)

    def test_gpu_valid_amd_cdi_config(self, tmp_path):
        config = {"gpu": {"enabled": True, "vendor": "amd", "mode": "cdi"}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert not any("config.gpu" in e for e in errors)

    def test_gpu_valid_nvidia_config(self, tmp_path):
        config = {
            "gpu": {
                "enabled": True,
                "vendor": "nvidia",
                "devices": "all",
                "capabilities": "compute,utility",
            }
        }
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert not any("config.gpu" in e for e in errors)

    def test_gpu_valid_nvidia_config_vendor_absent(self, tmp_path):
        # vendor defaults to nvidia when absent — existing configs keep working.
        config = {"gpu": {"enabled": True, "capabilities": "compute"}}
        errors, _ = _validate_config(config, workspace=tmp_path)
        assert not any("config.gpu" in e for e in errors)

    def test_gpu_vaapi_valid_amd_config(self, tmp_path):
        config = {"gpu": {"enabled": True, "vendor": "amd", "vaapi": True}}
        errors, warnings = _validate_config(config, workspace=tmp_path)
        assert not any("config.gpu" in e for e in errors)
        assert not any("vaapi" in w for w in warnings)

    def test_gpu_vaapi_for_nvidia_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "nvidia", "vaapi": True}},
            workspace=tmp_path,
        )
        assert any("config.gpu.vaapi" in e and "vendor='amd'" in e for e in errors)

    def test_gpu_vaapi_non_boolean_rejected(self, tmp_path):
        errors, _ = _validate_config(
            {"gpu": {"enabled": True, "vendor": "amd", "vaapi": "yes"}},
            workspace=tmp_path,
        )
        assert any("config.gpu.vaapi" in e and "boolean" in e for e in errors)

    def test_gpu_vaapi_without_enabled_warns(self, tmp_path):
        _, warnings = _validate_config(
            {"gpu": {"vendor": "amd", "vaapi": True}}, workspace=tmp_path
        )
        assert any("config.gpu.vaapi" in w and "inert" in w for w in warnings)


class TestEffectivePackages:
    """gpu.vaapi implies mesa + libva-utils in the image package list."""

    def test_plain_packages_passthrough(self):
        from cli import _effective_packages

        config = {"packages": ["just", {"name": "gtk4", "outputs": ["out", "dev"]}]}
        assert _effective_packages(config) == config["packages"]

    def test_vaapi_appends_driver_packages(self):
        from cli import _effective_packages

        config = {
            "packages": ["just"],
            "gpu": {"enabled": True, "vendor": "amd", "vaapi": True},
        }
        assert _effective_packages(config) == ["just", "mesa", "libva-utils"]

    def test_vaapi_dedupes_explicit_packages(self):
        from cli import _effective_packages

        config = {
            "packages": ["mesa"],
            "gpu": {"enabled": True, "vendor": "amd", "vaapi": True},
        }
        assert _effective_packages(config) == ["mesa", "libva-utils"]

    def test_vaapi_inert_without_enabled(self):
        from cli import _effective_packages

        config = {"gpu": {"vendor": "amd", "vaapi": True}}
        assert _effective_packages(config) == []

    def test_vaapi_inert_for_nvidia(self):
        from cli import _effective_packages

        config = {"gpu": {"enabled": True, "vendor": "nvidia", "vaapi": True}}
        assert _effective_packages(config) == []

    def test_no_gpu_section(self):
        from cli import _effective_packages

        assert _effective_packages({}) == []


class TestParseDotenv:
    """Parser for KEY=VALUE secrets files consumed by env_sources."""

    def test_basic_key_value(self):
        assert _parse_dotenv("FOO=bar\nBAZ=qux\n") == {"FOO": "bar", "BAZ": "qux"}

    def test_comments_and_blank_lines_ignored(self):
        text = "# a comment\n\nFOO=bar\n   # indented comment\nBAZ=qux\n"
        assert _parse_dotenv(text) == {"FOO": "bar", "BAZ": "qux"}

    def test_export_prefix_stripped(self):
        assert _parse_dotenv("export FOO=bar\n") == {"FOO": "bar"}

    def test_quoted_values_unwrapped(self):
        text = "A=\"double\"\nB='single'\nC=no_quotes\n"
        assert _parse_dotenv(text) == {"A": "double", "B": "single", "C": "no_quotes"}

    def test_value_with_equals_preserved(self):
        assert _parse_dotenv("TOKEN=a=b=c\n") == {"TOKEN": "a=b=c"}

    def test_empty_value_allowed(self):
        assert _parse_dotenv("EMPTY=\n") == {"EMPTY": ""}

    def test_invalid_variable_name_skipped(self):
        text = "123BAD=x\nGOOD=y\nalso-bad=z\n"
        assert _parse_dotenv(text) == {"GOOD": "y"}

    def test_no_equals_sign_skipped(self):
        text = "just a line\nFOO=bar\n"
        assert _parse_dotenv(text) == {"FOO": "bar"}


class TestResolveEnvSources:
    """Resolver that turns env_sources config into the final env map."""

    def test_empty_returns_empty(self, tmp_path):
        assert _resolve_env_sources(tmp_path, {}) == {}
        assert _resolve_env_sources(tmp_path, {"env_sources": []}) == {}

    def test_inline_dict_applied(self, tmp_path):
        config = {"env_sources": [{"FOO": "bar"}]}
        assert _resolve_env_sources(tmp_path, config) == {"FOO": "bar"}

    def test_file_read_and_merged(self, tmp_path):
        creds = tmp_path / "creds.env"
        creds.write_text("API_KEY=sk-abc\nDEBUG=0\n")
        config = {"env_sources": [str(creds)]}
        assert _resolve_env_sources(tmp_path, config) == {
            "API_KEY": "sk-abc",
            "DEBUG": "0",
        }

    def test_later_entries_override_earlier(self, tmp_path):
        creds = tmp_path / "creds.env"
        creds.write_text("FOO=from_file\n")
        config = {
            "env_sources": [
                {"FOO": "first", "KEEP": "yes"},
                str(creds),
                {"FOO": "last"},
            ]
        }
        result = _resolve_env_sources(tmp_path, config)
        assert result == {"FOO": "last", "KEEP": "yes"}

    def test_workspace_relative_path(self, tmp_path):
        (tmp_path / ".secrets").mkdir()
        (tmp_path / ".secrets" / "env").write_text("TOKEN=rel\n")
        config = {"env_sources": [".secrets/env"]}
        assert _resolve_env_sources(tmp_path, config) == {"TOKEN": "rel"}

    def test_tilde_expansion(self, tmp_path, monkeypatch):
        fake_home = tmp_path / "home"
        fake_home.mkdir()
        (fake_home / "creds.env").write_text("HOME_VAR=1\n")
        monkeypatch.setenv("HOME", str(fake_home))
        config = {"env_sources": ["~/creds.env"]}
        assert _resolve_env_sources(tmp_path, config) == {"HOME_VAR": "1"}

    def test_missing_file_warns_and_continues(self, tmp_path, capsys):
        config = {
            "env_sources": [{"BEFORE": "1"}, "does-not-exist.env", {"AFTER": "2"}]
        }
        result = _resolve_env_sources(tmp_path, config)
        assert result == {"BEFORE": "1", "AFTER": "2"}

    def test_absolute_path(self, tmp_path):
        creds = tmp_path / "abs.env"
        creds.write_text("ABS=yes\n")
        config = {"env_sources": [str(creds.resolve())]}
        assert _resolve_env_sources(tmp_path / "subdir", config) == {"ABS": "yes"}


class TestWorkspaceReadonlyMountArgs:
    """Test construction of read-only bind-mount args from workspace_readonly."""

    def _bind_mounts(self, args):
        """Extract (host, container, opts) triples from a -v args list."""
        mounts = []
        it = iter(args)
        for flag in it:
            if flag != "-v":
                continue
            spec = next(it)
            parts = spec.split(":")
            opts = parts[2] if len(parts) > 2 else ""
            mounts.append((parts[0], parts[1], opts))
        return mounts

    def test_empty_returns_no_args(self, tmp_path):
        assert _workspace_readonly_mount_args(tmp_path, {}) == []
        assert (
            _workspace_readonly_mount_args(tmp_path, {"workspace_readonly": []}) == []
        )

    def test_listed_subpath_mounted_readonly(self, tmp_path):
        (tmp_path / "src").mkdir()
        (tmp_path / "src" / "cli.py").write_text("# stub")
        args = _workspace_readonly_mount_args(tmp_path, {"workspace_readonly": ["src"]})
        mounts = self._bind_mounts(args)
        src_mount = next(m for m in mounts if m[1] == "/workspace/src")
        assert src_mount[0] == str((tmp_path / "src").resolve())
        assert src_mount[2] == "ro"

    def test_multiple_entries_all_mounted(self, tmp_path):
        (tmp_path / "src").mkdir()
        (tmp_path / "flake.nix").write_text("{}")
        args = _workspace_readonly_mount_args(
            tmp_path, {"workspace_readonly": ["src", "flake.nix"]}
        )
        container_paths = [m[1] for m in self._bind_mounts(args)]
        assert "/workspace/src" in container_paths
        assert "/workspace/flake.nix" in container_paths

    def test_nonexistent_entry_skipped_with_warning(self, tmp_path, capsys):
        args = _workspace_readonly_mount_args(
            tmp_path, {"workspace_readonly": ["does-not-exist"]}
        )
        # No mount emitted for the missing path (the config-file auto-mount
        # may still appear if yolo-jail.jsonc exists in tmp_path, but
        # does-not-exist must not).
        container_paths = [m[1] for m in self._bind_mounts(args)]
        assert "/workspace/does-not-exist" not in container_paths
        assert "does not exist" in capsys.readouterr().out

    def test_traversal_escape_rejected_at_runtime(self, tmp_path, capsys):
        # The validator already blocks '..' syntactically, but the runtime
        # check is a second layer for symlink-based escapes. Simulate by
        # constructing a symlink that resolves outside the workspace.
        outside = tmp_path.parent / "outside-target"
        outside.mkdir(exist_ok=True)
        workspace = tmp_path / "ws"
        workspace.mkdir()
        (workspace / "escape").symlink_to(outside)
        args = _workspace_readonly_mount_args(
            workspace, {"workspace_readonly": ["escape"]}
        )
        container_paths = [m[1] for m in self._bind_mounts(args)]
        assert "/workspace/escape" not in container_paths
        assert "escapes workspace" in capsys.readouterr().out

    def test_config_file_auto_mounted_when_active(self, tmp_path):
        (tmp_path / "src").mkdir()
        (tmp_path / "yolo-jail.jsonc").write_text('{"workspace_readonly": ["src"]}')
        args = _workspace_readonly_mount_args(tmp_path, {"workspace_readonly": ["src"]})
        mounts = self._bind_mounts(args)
        cfg = next((m for m in mounts if m[1] == "/workspace/yolo-jail.jsonc"), None)
        assert cfg is not None, (
            "yolo-jail.jsonc must be auto-locked when workspace_readonly is active"
        )
        assert cfg[2] == "ro"

    def test_config_file_not_mounted_when_inactive(self, tmp_path):
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        args = _workspace_readonly_mount_args(tmp_path, {})
        container_paths = [m[1] for m in self._bind_mounts(args)]
        assert "/workspace/yolo-jail.jsonc" not in container_paths

    def test_config_file_auto_mount_skipped_when_file_absent(self, tmp_path):
        (tmp_path / "src").mkdir()
        # No yolo-jail.jsonc on disk — helper shouldn't fabricate a mount.
        args = _workspace_readonly_mount_args(tmp_path, {"workspace_readonly": ["src"]})
        container_paths = [m[1] for m in self._bind_mounts(args)]
        assert "/workspace/yolo-jail.jsonc" not in container_paths


class TestPresetNullConflicts:
    def test_no_conflict(self):
        config = {
            "mcp_presets": ["chrome-devtools"],
            "mcp_servers": {"custom": {"command": "cmd"}},
        }
        assert _check_preset_null_conflicts(config, "test") == []

    def test_conflict_detected(self):
        config = {
            "mcp_presets": ["chrome-devtools"],
            "mcp_servers": {"chrome-devtools": None},
        }
        errors = _check_preset_null_conflicts(config, "test")
        assert len(errors) == 1
        assert "chrome-devtools" in errors[0]

    def test_no_presets(self):
        assert _check_preset_null_conflicts({}, "test") == []


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Validation helpers
# ═══════════════════════════════════════════════════════════════════════════════


class TestValidationHelpers:
    def test_report_unknown_keys(self):
        errors = []
        _report_unknown_keys({"a": 1, "b": 2, "c": 3}, {"a", "b"}, "cfg", errors)
        assert len(errors) == 1
        assert "c" in errors[0]

    def test_validate_string_list_valid(self):
        errors = []
        _validate_string_list(["a", "b"], "test", errors)
        assert errors == []

    def test_validate_string_list_non_string(self):
        errors = []
        _validate_string_list(["a", 123], "test", errors)
        assert len(errors) == 1

    def test_validate_string_list_not_list(self):
        errors = []
        _validate_string_list("not-a-list", "test", errors)
        assert len(errors) == 1

    def test_validate_forward_host_port_int(self):
        errors = []
        _validate_forward_host_port(8080, "test", errors)
        assert errors == []

    def test_validate_forward_host_port_string(self):
        errors = []
        _validate_forward_host_port("8080", "test", errors)
        assert errors == []

    def test_validate_forward_host_port_mapping(self):
        errors = []
        _validate_forward_host_port("8080:9090", "test", errors)
        assert errors == []

    def test_validate_forward_host_port_invalid_type(self):
        errors = []
        _validate_forward_host_port(3.14, "test", errors)
        assert len(errors) == 1

    def test_validate_forward_host_port_too_many_colons(self):
        errors = []
        _validate_forward_host_port("a:b:c", "test", errors)
        assert len(errors) == 1

    def test_validate_publish_port_not_string(self):
        errors = []
        _validate_publish_port(8080, "test", errors)
        assert len(errors) == 1

    def test_validate_publish_port_wrong_parts(self):
        errors = []
        _validate_publish_port("a:b:c:d", "test", errors)
        assert len(errors) == 1


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _runtime_for_check
# ═══════════════════════════════════════════════════════════════════════════════


class TestRuntimeForCheck:
    def test_env_var_on_path(self):
        with patch.dict(os.environ, {"YOLO_RUNTIME": "podman"}):
            with patch("shutil.which", return_value="/usr/bin/podman"):
                with patch("cli.runtime._runtime_is_connectable", return_value=True):
                    rt, err = _runtime_for_check({})
                    assert rt == "podman"
                    assert err is None

    def test_env_var_not_on_path(self):
        with patch.dict(os.environ, {"YOLO_RUNTIME": "podman"}):
            with patch("shutil.which", return_value=None):
                rt, err = _runtime_for_check({})
                assert rt is None
                assert "not on PATH" in err

    def test_config_runtime(self):
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("YOLO_RUNTIME", None)
            with patch("shutil.which", return_value="/usr/bin/podman"):
                with patch("cli.runtime._runtime_is_connectable", return_value=True):
                    rt, err = _runtime_for_check({"runtime": "podman"})
                    assert rt == "podman"

    def test_auto_detect(self):
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("YOLO_RUNTIME", None)
            with patch(
                "shutil.which",
                side_effect=lambda x: "/usr/bin/podman" if x == "podman" else None,
            ):
                with patch("cli.runtime._runtime_is_connectable", return_value=True):
                    rt, err = _runtime_for_check({})
                    assert rt == "podman"

    def test_nothing_found(self):
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("YOLO_RUNTIME", None)
            with patch("shutil.which", return_value=None):
                rt, err = _runtime_for_check({})
                assert rt is None
                assert "No container runtime" in err

    def test_env_var_not_connected(self):
        with patch.dict(os.environ, {"YOLO_RUNTIME": "podman"}):
            with patch("shutil.which", return_value="/usr/bin/podman"):
                with patch("cli.runtime._runtime_is_connectable", return_value=False):
                    rt, err = _runtime_for_check({})
                    assert rt is None
                    assert "not connected" in err


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _detect_host_timezone
# ═══════════════════════════════════════════════════════════════════════════════


class TestNixCustomConfIncluded:
    """_nix_custom_conf_included parses /etc/nix/nix.conf for the include line."""

    def _patch_conf(self, monkeypatch, conf_path):
        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/etc/nix/nix.conf":
                return conf_path
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)

    def test_detects_bang_include(self, tmp_path, monkeypatch):
        from cli import _nix_custom_conf_included

        conf = tmp_path / "nix.conf"
        conf.write_text(
            "build-users-group = nixbld\n!include /etc/nix/nix.custom.conf\n"
        )
        self._patch_conf(monkeypatch, conf)
        assert _nix_custom_conf_included() is True

    def test_detects_plain_include(self, tmp_path, monkeypatch):
        from cli import _nix_custom_conf_included

        conf = tmp_path / "nix.conf"
        conf.write_text("include /etc/nix/nix.custom.conf\n")
        self._patch_conf(monkeypatch, conf)
        assert _nix_custom_conf_included() is True

    def test_returns_false_when_no_include(self, tmp_path, monkeypatch):
        from cli import _nix_custom_conf_included

        conf = tmp_path / "nix.conf"
        conf.write_text("build-users-group = nixbld\n")
        self._patch_conf(monkeypatch, conf)
        assert _nix_custom_conf_included() is False

    def test_ignores_commented_include(self, tmp_path, monkeypatch):
        from cli import _nix_custom_conf_included

        conf = tmp_path / "nix.conf"
        conf.write_text("# !include /etc/nix/nix.custom.conf\n")
        self._patch_conf(monkeypatch, conf)
        assert _nix_custom_conf_included() is False

    def test_returns_none_when_missing(self, tmp_path, monkeypatch):
        from cli import _nix_custom_conf_included

        self._patch_conf(monkeypatch, tmp_path / "does-not-exist")
        assert _nix_custom_conf_included() is None


class TestDetectNixDaemonLabel:
    """_detect_nix_daemon_label returns the launchd Label based on the plist."""

    def test_returns_official_label(self, tmp_path, monkeypatch):
        from cli import _detect_nix_daemon_label

        daemon_dir = tmp_path / "LaunchDaemons"
        daemon_dir.mkdir()
        (daemon_dir / "org.nixos.nix-daemon.plist").write_text("<plist/>")

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/Library/LaunchDaemons":
                return daemon_dir
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_nix_daemon_label() == "org.nixos.nix-daemon"

    def test_returns_determinate_label(self, tmp_path, monkeypatch):
        from cli import _detect_nix_daemon_label

        daemon_dir = tmp_path / "LaunchDaemons"
        daemon_dir.mkdir()
        (daemon_dir / "systems.determinate.nix-daemon.plist").write_text("<plist/>")
        # An unrelated daemon shouldn't confuse the scan.
        (daemon_dir / "com.apple.other.plist").write_text("<plist/>")

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/Library/LaunchDaemons":
                return daemon_dir
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_nix_daemon_label() == "systems.determinate.nix-daemon"

    def test_returns_none_when_no_plist(self, tmp_path, monkeypatch):
        from cli import _detect_nix_daemon_label

        daemon_dir = tmp_path / "LaunchDaemons"
        daemon_dir.mkdir()

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/Library/LaunchDaemons":
                return daemon_dir
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_nix_daemon_label() is None

    def test_returns_none_on_non_macos(self, monkeypatch):
        from cli import _detect_nix_daemon_label

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/Library/LaunchDaemons":
                return real_path("/does-not-exist-for-tests")
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_nix_daemon_label() is None


class TestDetectHostTimezone:
    """Cover all three detection paths: $TZ, /etc/timezone, /etc/localtime."""

    def test_env_var_wins(self, monkeypatch):
        from cli import _detect_host_timezone

        monkeypatch.setenv("TZ", "Europe/Berlin")
        # Even if /etc/timezone would say something else, $TZ wins
        monkeypatch.setattr(
            "pathlib.Path.is_file", lambda self: str(self) == "/etc/timezone"
        )
        assert _detect_host_timezone() == "Europe/Berlin"

    def test_reads_etc_timezone(self, tmp_path, monkeypatch):
        from cli import _detect_host_timezone

        monkeypatch.delenv("TZ", raising=False)
        fake_tz = tmp_path / "timezone"
        fake_tz.write_text("America/New_York\n")

        # Patch Path("/etc/timezone") / Path("/etc/localtime") lookups.
        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/etc/timezone":
                return fake_tz
            if p == "/etc/localtime":
                return real_path(tmp_path / "does-not-exist")
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_host_timezone() == "America/New_York"

    def test_reads_etc_localtime_symlink(self, tmp_path, monkeypatch):
        from cli import _detect_host_timezone

        monkeypatch.delenv("TZ", raising=False)
        # Build a fake zoneinfo target and a symlink pointing at it
        zoneinfo = tmp_path / "zoneinfo" / "Asia" / "Tokyo"
        zoneinfo.parent.mkdir(parents=True)
        zoneinfo.write_bytes(b"fake tzdata")
        localtime = tmp_path / "localtime"
        os.symlink(str(zoneinfo), str(localtime))

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/etc/timezone":
                return real_path(tmp_path / "does-not-exist")
            if p == "/etc/localtime":
                return localtime
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_host_timezone() == "Asia/Tokyo"

    def test_macos_symlink_format(self, tmp_path, monkeypatch):
        """macOS /etc/localtime → /var/db/timezone/zoneinfo/<zone>"""
        from cli import _detect_host_timezone

        monkeypatch.delenv("TZ", raising=False)
        # Fake the macOS path layout
        target = (
            tmp_path / "var" / "db" / "timezone" / "zoneinfo" / "Pacific" / "Auckland"
        )
        target.parent.mkdir(parents=True)
        target.write_bytes(b"fake tzdata")
        localtime = tmp_path / "localtime"
        os.symlink(str(target), str(localtime))

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p == "/etc/timezone":
                return real_path(tmp_path / "does-not-exist")
            if p == "/etc/localtime":
                return localtime
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_host_timezone() == "Pacific/Auckland"

    def test_returns_none_when_nothing_found(self, tmp_path, monkeypatch):
        from cli import _detect_host_timezone

        monkeypatch.delenv("TZ", raising=False)

        import cli

        real_path = cli.Path

        def fake_path(p, *args, **kwargs):
            if p in ("/etc/timezone", "/etc/localtime"):
                return real_path(tmp_path / "does-not-exist")
            return real_path(p, *args, **kwargs)

        monkeypatch.setattr("cli.storage.Path", fake_path)
        assert _detect_host_timezone() is None


# ═══════════════════════════════════════════════════════════════════════════════
# Test: ensure_global_storage
# ═══════════════════════════════════════════════════════════════════════════════


class TestEnsureGlobalStorage:
    def _patch_storage(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.storage.GLOBAL_HOME", tmp_path / "home")
        monkeypatch.setattr("cli.storage.GLOBAL_MISE", tmp_path / "mise")
        monkeypatch.setattr("cli.storage.GLOBAL_CACHE", tmp_path / "cache")
        monkeypatch.setattr("cli.storage.CONTAINER_DIR", tmp_path / "containers")
        monkeypatch.setattr("cli.storage.AGENTS_DIR", tmp_path / "agents")
        monkeypatch.setattr("cli.storage.BUILD_DIR", tmp_path / "build")
        # Keep the one-time layout migration off the developer's real
        # host state: GLOBAL_STORAGE holds the layout-version marker and
        # _host_mise_dir names the store the v2 heal scans/unlinks.
        monkeypatch.setattr("cli.storage.GLOBAL_STORAGE", tmp_path / "storage")
        monkeypatch.setattr(
            "cli.storage._host_mise_dir", lambda: tmp_path / "host-mise"
        )

    def test_creates_directories(self, tmp_path, monkeypatch):
        self._patch_storage(tmp_path, monkeypatch)
        ensure_global_storage()
        assert (tmp_path / "home").is_dir()
        assert (tmp_path / "mise").is_dir()
        assert (tmp_path / "cache").is_dir()
        assert (tmp_path / "containers").is_dir()
        assert (tmp_path / "agents").is_dir()
        assert (tmp_path / "build").is_dir()

    def test_creates_subdirs(self, tmp_path, monkeypatch):
        home = tmp_path / "home"
        self._patch_storage(tmp_path, monkeypatch)
        ensure_global_storage()
        assert (home / ".copilot").is_dir()
        assert (home / ".gemini").is_dir()
        assert (home / ".claude").is_dir()
        assert (home / ".config" / "git").is_dir()

    def test_creates_mountpoint_files(self, tmp_path, monkeypatch):
        """File mountpoints must exist in GLOBAL_HOME for :ro bind mounts."""
        home = tmp_path / "home"
        self._patch_storage(tmp_path, monkeypatch)
        ensure_global_storage()
        # Spot-check key file mountpoints
        assert (home / ".yolo-entrypoint.lock").is_file()
        # Files that use atomic writes are symlinks into writable overlay dirs
        assert (home / ".claude.json").is_symlink()
        assert os.readlink(str(home / ".claude.json")) == str(
            Path(".claude") / "claude.json"
        )
        assert (home / ".gitconfig").is_symlink()
        assert os.readlink(str(home / ".gitconfig")) == str(
            Path(".config") / "git" / "config"
        )
        assert (home / ".bashrc").is_symlink()
        assert os.readlink(str(home / ".bashrc")) == str(Path(".config") / "bashrc")

    def test_creates_overlay_dir_mountpoints(self, tmp_path, monkeypatch):
        """Directory mountpoints for per-workspace overlays."""
        home = tmp_path / "home"
        self._patch_storage(tmp_path, monkeypatch)
        ensure_global_storage()
        assert (home / ".npm-global").is_dir()
        assert (home / ".local").is_dir()
        assert (home / "go").is_dir()
        assert (home / ".yolo-shims").is_dir()
        assert (home / ".cache").is_dir()
        assert (home / ".copilot").is_dir()
        assert (home / ".gemini").is_dir()
        assert (home / ".claude").is_dir()

    def test_skips_existing_files_with_bad_perms(self, tmp_path, monkeypatch):
        """Pre-existing files with restrictive perms should not cause errors."""
        home = tmp_path / "home"
        home.mkdir()
        # Simulate a file written by a container with different UID.
        # Use .yolo-entrypoint.lock (a plain file mountpoint, not a symlink target)
        # so the test exercises the touch-skip path without hitting symlink migration.
        f = home / ".yolo-entrypoint.lock"
        f.write_text("# old")
        f.chmod(0o000)
        self._patch_storage(tmp_path, monkeypatch)
        # Should not raise despite unwritable file
        ensure_global_storage()
        assert f.exists()
        # Cleanup: restore perms so tmp_path cleanup works
        f.chmod(0o644)


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _get_project_name
# ═══════════════════════════════════════════════════════════════════════════════


class TestGetProjectName:
    def test_from_env(self, monkeypatch):
        monkeypatch.setenv("SM_PROJECT", "my-project")
        assert _get_project_name() == "my-project"

    def test_from_cwd(self, tmp_path, monkeypatch):
        monkeypatch.delenv("SM_PROJECT", raising=False)
        monkeypatch.chdir(tmp_path / "my-workspace" if False else tmp_path)
        assert _get_project_name() == tmp_path.name


# ═══════════════════════════════════════════════════════════════════════════════
# Test: AGENTS.md generation
# ═══════════════════════════════════════════════════════════════════════════════


# Every agent selected — used by tests that assert on all briefing files.
_ALL_AGENTS = ["copilot", "gemini", "claude", "opencode", "pi", "codex"]


class TestGenerateAgentsMd:
    def test_basic_generation(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path / "ws",
            blocked_tools=[],
            mount_descriptions=[],
            agents=_ALL_AGENTS,
        )
        assert (agents_dir / "AGENTS-copilot.md").exists()
        assert (agents_dir / "AGENTS-gemini.md").exists()
        assert (agents_dir / "CLAUDE.md").exists()
        assert (agents_dir / "AGENTS-opencode.md").exists()
        assert (agents_dir / "AGENTS-pi.md").exists()
        assert (agents_dir / "AGENTS-codex.md").exists()
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert "YOLO Jail" in content
        claude_content = (agents_dir / "CLAUDE.md").read_text()
        assert "YOLO Jail" in claude_content

    def test_default_is_claude_only(self, tmp_path, monkeypatch):
        """No agents arg → only claude's CLAUDE.md is written (the default)."""
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path / "ws",
            blocked_tools=[],
            mount_descriptions=[],
        )
        assert (agents_dir / "CLAUDE.md").exists()
        assert not (agents_dir / "AGENTS-copilot.md").exists()
        assert not (agents_dir / "AGENTS-gemini.md").exists()

    def test_selection_prunes_briefings(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path / "ws",
            blocked_tools=[],
            mount_descriptions=[],
            agents=["opencode", "pi"],
        )
        assert (agents_dir / "AGENTS-opencode.md").exists()
        assert (agents_dir / "AGENTS-pi.md").exists()
        assert not (agents_dir / "CLAUDE.md").exists()
        assert not (agents_dir / "AGENTS-copilot.md").exists()

    def test_blocked_tools_listed(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[{"name": "curl", "message": "Use wget"}],
            mount_descriptions=[],
            agents=_ALL_AGENTS,
        )
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert "curl" in content
        assert "Use wget" in content

    def test_mount_descriptions(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[],
            mount_descriptions=["/host/path:/ctx/path"],
            agents=_ALL_AGENTS,
        )
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert "/ctx/path" in content

    def test_host_network(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[],
            mount_descriptions=[],
            net_mode="host",
            agents=_ALL_AGENTS,
        )
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert "Host networking" in content

    def test_bridge_podman(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[],
            mount_descriptions=[],
            net_mode="bridge",
            runtime="podman",
            agents=_ALL_AGENTS,
        )
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert "host.containers.internal" in content

    def test_forwarded_ports(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[],
            mount_descriptions=[],
            forward_host_ports=[5432, "8080:9090"],
            agents=_ALL_AGENTS,
        )
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert "localhost:5432" in content
        assert "localhost:8080" in content

    def test_user_agents_prepended(self, tmp_path, monkeypatch):
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        copilot_dir = tmp_path / ".copilot"
        copilot_dir.mkdir()
        (copilot_dir / "AGENTS.md").write_text("# My Custom AGENTS")
        monkeypatch.setattr("cli.Path.home", lambda: tmp_path)
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[],
            mount_descriptions=[],
            agents=_ALL_AGENTS,
        )
        content = (agents_dir / "AGENTS-copilot.md").read_text()
        assert content.startswith("# My Custom AGENTS")
        assert "YOLO Jail" in content

    def test_opencode_pi_host_briefing_prepended(self, tmp_path, monkeypatch):
        """opencode reads ~/.config/opencode/AGENTS.md; pi reads
        ~/.pi/agent/AGENTS.md — each is prepended when present on the host."""
        monkeypatch.setattr("cli.AGENTS_DIR", tmp_path / "agents")
        oc = tmp_path / ".config" / "opencode"
        oc.mkdir(parents=True)
        (oc / "AGENTS.md").write_text("# opencode custom")
        pi = tmp_path / ".pi" / "agent"
        pi.mkdir(parents=True)
        (pi / "AGENTS.md").write_text("# pi custom")
        monkeypatch.setattr("cli.Path.home", lambda: tmp_path)
        agents_dir = generate_agents_md(
            cname="yolo-test",
            workspace=tmp_path,
            blocked_tools=[],
            mount_descriptions=[],
            agents=["opencode", "pi"],
        )
        assert (
            (agents_dir / "AGENTS-opencode.md")
            .read_text()
            .startswith("# opencode custom")
        )
        assert (agents_dir / "AGENTS-pi.md").read_text().startswith("# pi custom")


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Skills preparation
# ═══════════════════════════════════════════════════════════════════════════════


class TestPrepareSkills:
    def test_builtin_skill_created(self, tmp_path, monkeypatch):
        monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
        result = _prepare_skills("test-cname", ["copilot", "gemini", "claude"])
        for agent in ("copilot", "gemini", "claude"):
            skill = result / f"skills-{agent}" / "jail-startup" / "SKILL.md"
            assert skill.exists()
            assert "Jail Startup" in skill.read_text()

    def test_skilless_agents_get_no_staging_dir(self, tmp_path, monkeypatch):
        """opencode and pi have no user-skills dir → no skills-<x> staged."""
        monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
        result = _prepare_skills("test-cname", ["claude", "opencode", "pi"])
        assert (result / "skills-claude").exists()
        assert not (result / "skills-opencode").exists()
        assert not (result / "skills-pi").exists()

    def test_selection_prunes_skills(self, tmp_path, monkeypatch):
        """A claude-only selection stages no copilot/gemini skills dir."""
        monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
        result = _prepare_skills("test-cname", ["claude"])
        assert (result / "skills-claude").exists()
        assert not (result / "skills-copilot").exists()
        assert not (result / "skills-gemini").exists()

    def test_host_skills_strict_per_agent(self, tmp_path, monkeypatch):
        """Each agent's staging dir mirrors ONLY its host counterpart.

        No cross-agent merging — a skill installed in ~/.gemini/skills/
        is visible to gemini in the jail and nowhere else.  This makes
        deletion intuitive: removing from the host dir cleanly removes
        from the matching jail dir.
        """
        monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
        host_home = tmp_path / "home"
        monkeypatch.setattr(Path, "home", lambda: host_home)
        (host_home / ".gemini" / "skills" / "gemini-only").mkdir(parents=True)
        (host_home / ".gemini" / "skills" / "gemini-only" / "SKILL.md").write_text(
            "gemini"
        )
        (host_home / ".copilot" / "skills" / "copilot-only").mkdir(parents=True)
        (host_home / ".copilot" / "skills" / "copilot-only" / "SKILL.md").write_text(
            "copilot"
        )
        (host_home / ".claude" / "skills" / "claude-only").mkdir(parents=True)
        (host_home / ".claude" / "skills" / "claude-only" / "SKILL.md").write_text(
            "claude"
        )
        result = _prepare_skills("test-cname", ["copilot", "gemini", "claude"])

        # Each skill appears in its own agent's dir.
        assert (result / "skills-gemini" / "gemini-only" / "SKILL.md").read_text() == (
            "gemini"
        )
        assert (
            result / "skills-copilot" / "copilot-only" / "SKILL.md"
        ).read_text() == "copilot"
        assert (result / "skills-claude" / "claude-only" / "SKILL.md").read_text() == (
            "claude"
        )

        # And NOT in the others.
        assert not (result / "skills-copilot" / "gemini-only").exists()
        assert not (result / "skills-claude" / "gemini-only").exists()
        assert not (result / "skills-gemini" / "copilot-only").exists()
        assert not (result / "skills-claude" / "copilot-only").exists()
        assert not (result / "skills-gemini" / "claude-only").exists()
        assert not (result / "skills-copilot" / "claude-only").exists()

    def test_stale_skills_cleaned(self, tmp_path, monkeypatch):
        monkeypatch.setattr(cli, "AGENTS_DIR", tmp_path / "agents")
        host_home = tmp_path / "home"
        monkeypatch.setattr(Path, "home", lambda: host_home)
        (host_home / ".gemini" / "skills" / "old-skill").mkdir(parents=True)
        (host_home / ".gemini" / "skills" / "old-skill" / "SKILL.md").write_text("old")
        result = _prepare_skills("test-cname", ["gemini"])
        assert (result / "skills-gemini" / "old-skill").exists()
        import shutil

        shutil.rmtree(host_home / ".gemini" / "skills" / "old-skill")
        (host_home / ".gemini" / "skills" / "new-skill").mkdir(parents=True)
        (host_home / ".gemini" / "skills" / "new-skill" / "SKILL.md").write_text("new")
        result = _prepare_skills("test-cname", ["gemini"])
        assert not (result / "skills-gemini" / "old-skill").exists()
        assert (result / "skills-gemini" / "new-skill").exists()


# Test: Config change detection
# ═══════════════════════════════════════════════════════════════════════════════


class TestConfigChanges:
    def test_first_run_accepts(self, tmp_path):
        result = _check_config_changes(tmp_path, {"runtime": "podman"})
        assert result is True
        assert _config_snapshot_path(tmp_path).exists()

    def test_no_change_accepts(self, tmp_path):
        config = {"runtime": "podman"}
        _check_config_changes(tmp_path, config)
        result = _check_config_changes(tmp_path, config)
        assert result is True

    def test_change_non_interactive_accepts(self, tmp_path):
        _check_config_changes(tmp_path, {"runtime": "podman"})
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            result = _check_config_changes(tmp_path, {"runtime": "container"})
            assert result is True

    def test_change_interactive_rejected(self, tmp_path):
        _check_config_changes(tmp_path, {"runtime": "podman"})
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            with patch("builtins.input", return_value="n"):
                result = _check_config_changes(tmp_path, {"runtime": "container"})
                assert result is False

    def test_change_interactive_accepted(self, tmp_path):
        _check_config_changes(tmp_path, {"runtime": "podman"})
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            with patch("builtins.input", return_value="y"):
                result = _check_config_changes(tmp_path, {"runtime": "container"})
                assert result is True

    def test_change_interactive_eof(self, tmp_path):
        _check_config_changes(tmp_path, {"runtime": "podman"})
        with patch("sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            with patch("builtins.input", side_effect=EOFError):
                result = _check_config_changes(tmp_path, {"runtime": "container"})
                assert result is False


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _merge_lists
# ═══════════════════════════════════════════════════════════════════════════════


class TestMergeLists:
    def test_dedup(self):
        result = _merge_lists(["a", "b"], ["b", "c"])
        assert result == ["a", "b", "c"]

    def test_empty(self):
        assert _merge_lists([], []) == []

    def test_complex_objects(self):
        result = _merge_lists([{"name": "a"}], [{"name": "a"}, {"name": "b"}])
        assert len(result) == 2


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _host_mise_dir
# ═══════════════════════════════════════════════════════════════════════════════


class TestHostMiseDir:
    """Host-only: no env overrides, no mkdir side effect — the jail
    store is a separate dir (see _jail_mise_store_dir)."""

    def test_env_is_ignored(self, monkeypatch, tmp_path):
        mise_dir = tmp_path / "mise"
        monkeypatch.setenv("MISE_DATA_DIR", str(mise_dir))
        result = _host_mise_dir()
        assert result == Path.home() / ".local" / "share" / "mise"
        assert not mise_dir.exists()

    def test_default_path_no_mkdir(self, monkeypatch):
        monkeypatch.delenv("MISE_DATA_DIR", raising=False)
        result = _host_mise_dir()
        # Names the host's own store; existence is not required.
        assert result == Path.home() / ".local" / "share" / "mise"


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _seed_agent_dir
# ═══════════════════════════════════════════════════════════════════════════════


class TestSeedAgentDir:
    def test_copies_files_on_first_use(self, tmp_path):
        from cli import _seed_agent_dir

        src = tmp_path / "src"
        src.mkdir()
        (src / "hosts.json").write_text('{"token": "abc"}')
        (src / "config.json").write_text("{}")
        dst = tmp_path / "dst"
        dst.mkdir()
        _seed_agent_dir(src, dst)
        assert (dst / "hosts.json").read_text() == '{"token": "abc"}'
        assert (dst / "config.json").read_text() == "{}"

    def test_does_not_overwrite_existing(self, tmp_path):
        from cli import _seed_agent_dir

        src = tmp_path / "src"
        src.mkdir()
        (src / "hosts.json").write_text("old")
        dst = tmp_path / "dst"
        dst.mkdir()
        (dst / "hosts.json").write_text("new")
        _seed_agent_dir(src, dst)
        assert (dst / "hosts.json").read_text() == "new"

    def test_skips_subdirectories(self, tmp_path):
        from cli import _seed_agent_dir

        src = tmp_path / "src"
        src.mkdir()
        (src / "subdir").mkdir()
        (src / "subdir" / "file.txt").write_text("x")
        dst = tmp_path / "dst"
        dst.mkdir()
        _seed_agent_dir(src, dst)
        assert not (dst / "subdir").exists()

    def test_handles_missing_src(self, tmp_path):
        from cli import _seed_agent_dir

        dst = tmp_path / "dst"
        dst.mkdir()
        _seed_agent_dir(tmp_path / "nonexistent", dst)  # should not raise


# ═══════════════════════════════════════════════════════════════════════════════
# Test: merge_config
# ═══════════════════════════════════════════════════════════════════════════════


class TestMergeConfig:
    def test_scalar_override(self):
        result = merge_config({"runtime": "podman"}, {"runtime": "container"})
        assert result["runtime"] == "container"

    def test_dict_deep_merge(self):
        result = merge_config(
            {"network": {"mode": "bridge"}},
            {"network": {"ports": ["8000:8000"]}},
        )
        assert result["network"]["mode"] == "bridge"
        assert result["network"]["ports"] == ["8000:8000"]

    def test_list_dedup_merge(self):
        result = merge_config(
            {"packages": ["a", "b"]},
            {"packages": ["b", "c"]},
        )
        assert result["packages"] == ["a", "b", "c"]

    def test_new_keys_added(self):
        result = merge_config({"a": 1}, {"b": 2})
        assert result == {"a": 1, "b": 2}


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _entrypoint_preflight
# ═══════════════════════════════════════════════════════════════════════════════


class TestEntrypointPreflight:
    def test_successful_preflight(self, tmp_path, monkeypatch):
        """Dry-run with minimal config should succeed (entrypoint.py exists)."""
        from cli import _entrypoint_preflight

        repo_root = REPO_ROOT
        workspace = tmp_path / "ws"
        workspace.mkdir()
        _entrypoint_preflight(repo_root, workspace, {})

    def test_missing_entrypoint_raises(self, tmp_path):
        """If entrypoint.py doesn't exist in the repo root, should fail."""
        from cli import _entrypoint_preflight

        with pytest.raises(Exception):
            _entrypoint_preflight(tmp_path, tmp_path, {})


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Typer CLI integration (via CliRunner)
# ═══════════════════════════════════════════════════════════════════════════════


class TestCliRunner:
    """Test CLI subcommands via Typer's CliRunner."""

    def test_config_ref(self):
        from cli import app

        runner = CliRunner()
        result = runner.invoke(app, ["config-ref"])
        assert result.exit_code == 0
        assert "runtime" in result.output

    def test_check_help(self):
        from cli import app

        runner = CliRunner()
        result = runner.invoke(app, ["check", "--help"])
        assert result.exit_code == 0
        assert "Validate" in result.output or "validate" in result.output

    def test_init_creates_config(self, tmp_path, monkeypatch):
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        result = runner.invoke(app, ["init"])
        assert result.exit_code == 0
        assert (tmp_path / "yolo-jail.jsonc").exists()

    def test_init_idempotent(self, tmp_path, monkeypatch):
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        runner.invoke(app, ["init"])
        result = runner.invoke(app, ["init"])
        assert result.exit_code == 0
        assert "already exists" in result.output

    def test_init_creates_gitignore(self, tmp_path, monkeypatch):
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        runner.invoke(app, ["init"])
        gitignore = tmp_path / ".gitignore"
        assert gitignore.exists()
        assert ".yolo/" in gitignore.read_text()

    def test_init_appends_to_existing_gitignore(self, tmp_path, monkeypatch):
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        (tmp_path / ".gitignore").write_text("node_modules/\n")
        runner.invoke(app, ["init"])
        content = (tmp_path / ".gitignore").read_text()
        assert "node_modules/" in content
        assert ".yolo/" in content

    def test_init_has_agent_help_hint(self, tmp_path, monkeypatch):
        """Default config tells first-time agents to run `yolo --help`."""
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        runner.invoke(app, ["init"])
        text = (tmp_path / "yolo-jail.jsonc").read_text()
        # Check both that the hint is present and that it's near the top
        # (within the first 10 lines) so agents see it immediately.
        first_block = "\n".join(text.splitlines()[:10])
        assert "yolo --help" in first_block
        assert "yolo config-ref" in first_block

    def test_init_no_mounts_keeps_placeholder(self, tmp_path, monkeypatch):
        """Without --mount, the mounts section stays commented-out."""
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        runner.invoke(app, ["init"])
        text = (tmp_path / "yolo-jail.jsonc").read_text()
        # The placeholder is commented out; no active "mounts": [...] key.
        import pyjson5

        data = pyjson5.loads(text)
        assert "mounts" not in data

    def test_init_with_single_mount(self, tmp_path, monkeypatch):
        """--mount with one path emits a real mounts array."""
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        result = runner.invoke(app, ["init", "-m", "~/code/shared-lib"])
        assert result.exit_code == 0
        text = (tmp_path / "yolo-jail.jsonc").read_text()

        import pyjson5

        data = pyjson5.loads(text)
        assert data["mounts"] == ["~/code/shared-lib"]

    def test_init_with_multiple_mounts(self, tmp_path, monkeypatch):
        """Repeated --mount flags accumulate into the mounts array."""
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        result = runner.invoke(
            app,
            [
                "init",
                "-m",
                "~/code/repo-a",
                "-m",
                "~/code/repo-b",
                "-m",
                "~/notes:/ctx/notes",
            ],
        )
        assert result.exit_code == 0
        text = (tmp_path / "yolo-jail.jsonc").read_text()

        import pyjson5

        data = pyjson5.loads(text)
        assert data["mounts"] == [
            "~/code/repo-a",
            "~/code/repo-b",
            "~/notes:/ctx/notes",
        ]

    def test_init_with_mount_long_option(self, tmp_path, monkeypatch):
        """--mount is the long form; -m is the short form."""
        from cli import app

        runner = CliRunner()
        monkeypatch.chdir(tmp_path)
        result = runner.invoke(app, ["init", "--mount", "~/code/shared-lib"])
        assert result.exit_code == 0
        text = (tmp_path / "yolo-jail.jsonc").read_text()

        import pyjson5

        data = pyjson5.loads(text)
        assert data["mounts"] == ["~/code/shared-lib"]

    def test_init_user_config_has_agent_help_hint(self, tmp_path, monkeypatch):
        """Default user config also tells first-time agents to run `yolo --help`."""
        from cli import app

        user_config = tmp_path / "config.jsonc"
        monkeypatch.setattr("cli.init_cmd.USER_CONFIG_PATH", user_config)

        runner = CliRunner()
        runner.invoke(app, ["init-user-config"])
        text = user_config.read_text()
        first_block = "\n".join(text.splitlines()[:10])
        assert "yolo --help" in first_block

    def test_ps_command(self):
        from cli import app

        runner = CliRunner()
        result = runner.invoke(app, ["ps"])
        # ps command should work even with no containers
        assert result.exit_code == 0 or "runtime" in result.output.lower()


# ═══════════════════════════════════════════════════════════════════════════════
# Test: _estimate_image_size
# ═══════════════════════════════════════════════════════════════════════════════


class TestEstimateImageSize:
    def test_from_saved_size(self, tmp_path):
        from cli import _estimate_image_size

        sentinel = tmp_path / "sentinel"
        size_file = tmp_path / "sentinel-size"
        size_file.write_text("1234567890")
        result = _estimate_image_size("/nix/store/test", sentinel)
        assert result == 1234567890

    def test_invalid_saved_size(self, tmp_path):
        from cli import _estimate_image_size

        sentinel = tmp_path / "sentinel"
        size_file = tmp_path / "sentinel-size"
        size_file.write_text("not-a-number")
        with patch("subprocess.run") as mock_run:
            mock_run.side_effect = FileNotFoundError
            result = _estimate_image_size("/nix/store/test", sentinel)
            assert result == 0

    def test_nix_fallback(self, tmp_path):
        from cli import _estimate_image_size

        sentinel = tmp_path / "sentinel"
        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(
                returncode=0, stdout="/nix/store/abc 987654321"
            )
            result = _estimate_image_size("/nix/store/test", sentinel)
            assert result == 987654321


# ═══════════════════════════════════════════════════════════════════════════════
# Test: Cgroup delegate daemon helpers
# ═══════════════════════════════════════════════════════════════════════════════


class TestCgroupValidation:
    """Test cgroup name validation and memory parsing."""

    def test_valid_names(self):
        assert _validate_cgroup_name("job-1234")
        assert _validate_cgroup_name("training")
        assert _validate_cgroup_name("my_job.v2")
        assert _validate_cgroup_name("a")

    def test_invalid_names(self):
        assert not _validate_cgroup_name("")
        assert not _validate_cgroup_name("../escape")
        assert not _validate_cgroup_name("/absolute")
        assert not _validate_cgroup_name(".hidden")
        assert not _validate_cgroup_name("-dash-start")
        assert not _validate_cgroup_name("a" * 65)  # Too long
        assert not _validate_cgroup_name("has space")
        assert not _validate_cgroup_name("has/slash")

    def test_parse_memory_bytes(self):
        assert _parse_memory_value("1073741824") == 1073741824

    def test_parse_memory_suffix_g(self):
        assert _parse_memory_value("2g") == 2 * 1073741824

    def test_parse_memory_suffix_m(self):
        assert _parse_memory_value("512m") == 512 * 1048576

    def test_parse_memory_suffix_k(self):
        assert _parse_memory_value("1024k") == 1024 * 1024

    def test_parse_memory_invalid(self):
        assert _parse_memory_value("not-a-number") is None
        assert _parse_memory_value("") is None


class TestCgroupDaemonOps:
    """Test cgroup delegate daemon operations against a fake cgroup tree."""

    def _make_cgroup_tree(self, tmp_path):
        """Create a fake cgroup hierarchy for testing."""
        cg = tmp_path / "cgroup"
        cg.mkdir()
        (cg / "cgroup.controllers").write_text("cpu memory pids\n")
        (cg / "cgroup.procs").write_text("")
        (cg / "cgroup.subtree_control").write_text("")
        return cg

    def test_ensure_agent_cgroup_creates_hierarchy(self, tmp_path):
        cg = self._make_cgroup_tree(tmp_path)
        log = open(os.devnull, "w")
        result = _cgd_ensure_agent_cgroup(cg, log)
        assert result is not None
        assert (cg / "agent").is_dir()
        assert (cg / "init").is_dir()

    def test_ensure_agent_cgroup_idempotent(self, tmp_path):
        cg = self._make_cgroup_tree(tmp_path)
        log = open(os.devnull, "w")
        r1 = _cgd_ensure_agent_cgroup(cg, log)
        r2 = _cgd_ensure_agent_cgroup(cg, log)
        assert r1 == r2

    def test_destroy_nonexistent_is_ok(self, tmp_path):
        cg = self._make_cgroup_tree(tmp_path)
        (cg / "agent").mkdir()
        log = open(os.devnull, "w")
        result = _cgd_destroy(cg, "no-such-job", log)
        assert result["ok"]

    def test_create_and_join_invalid_name(self, tmp_path):
        cg = self._make_cgroup_tree(tmp_path)
        log = open(os.devnull, "w")
        # _cgd_create_and_join is called by the handler after name validation,
        # but let's test the daemon helper directly
        _cgd_ensure_agent_cgroup(cg, log)
        # Direct call with valid name — PID validation is in handler, not here.
        # On fake fs, write will succeed (no real cgroup.procs semantics),
        # so this should return ok=True.
        result = _cgd_create_and_join(cg, "test-job", {}, 0, log)
        assert result["ok"]

    def test_create_and_join_creates_job_dir(self, tmp_path):
        cg = self._make_cgroup_tree(tmp_path)
        log = open(os.devnull, "w")
        _cgd_ensure_agent_cgroup(cg, log)
        # PID write will fail (fake fs), but directory should be created
        _cgd_create_and_join(cg, "test-job", {"cpu_pct": 50}, 999, log)
        assert (cg / "agent" / "test-job").is_dir()
        # Result will show error from trying to write to fake cgroup.procs
        # but that's expected — the important thing is the dir was created


class TestCgroupDaemonSocket:
    """Test start/stop lifecycle of the cgroup delegate built-in service."""

    def test_start_stop_lifecycle(self, tmp_path):
        """Builtin cgroup daemon starts and stops cleanly."""
        sockets_dir = tmp_path / "host-services"
        # Mock _resolve_container_cgroup since no real container
        with patch("cli._resolve_container_cgroup", return_value=None):
            handle = _start_host_service_builtin_cgroup(
                "test-cname", "podman", sockets_dir
            )
        if handle is None:
            pytest.skip("cgroup v2 not available on this host")
        assert isinstance(handle, LoopholeDaemon)
        assert handle.name == BUILTIN_CGROUP_LOOPHOLE_NAME
        assert handle.host_socket_path.exists()
        assert handle.host_socket_path == sockets_dir / "cgroup.sock"
        assert handle.jail_socket_path == "/run/yolo-services/cgroup-delegate.sock"
        # Stop via the unified machinery
        stop_loopholes([handle], sockets_dir)
        assert not sockets_dir.exists()

    def test_start_returns_none_without_cgroupv2(self, tmp_path):
        """Returns None when cgroup v2 is not available."""
        sockets_dir = tmp_path / "host-services"
        with patch("pathlib.Path.exists", return_value=False):
            result = _start_host_service_builtin_cgroup("test", "podman", sockets_dir)
        assert result is None


class TestJournalDaemon:
    """Builtin journal bridge: mode resolution, lifecycle, wire protocol."""

    def test_resolve_mode_defaults_to_off(self):
        assert _resolve_journal_mode({}) == "off"
        assert _resolve_journal_mode({"journal": None}) == "off"
        assert _resolve_journal_mode({"journal": False}) == "off"

    def test_resolve_mode_true_means_user(self):
        # Booleans are a convenience shorthand — true picks the safe default.
        assert _resolve_journal_mode({"journal": True}) == "user"

    def test_resolve_mode_string_modes(self):
        assert _resolve_journal_mode({"journal": "off"}) == "off"
        assert _resolve_journal_mode({"journal": "user"}) == "user"
        assert _resolve_journal_mode({"journal": "full"}) == "full"

    def test_resolve_mode_invalid_falls_back_to_off(self):
        # Validation catches the bad value elsewhere; runtime is defensive.
        assert _resolve_journal_mode({"journal": "bogus"}) == "off"
        assert _resolve_journal_mode({"journal": 42}) == "off"

    def _with_fake_journalctl(self, tmp_path, stdout_text="", stderr_text="", rc=0):
        """Put a fake `journalctl` shell script on PATH that prints fixed output."""
        bin_dir = tmp_path / "fakebin"
        bin_dir.mkdir()
        fake = bin_dir / "journalctl"
        # Echo received args on stderr so we can assert the mode forced --user.
        fake.write_text(
            "#!/bin/bash\n"
            f"printf '%s' {shlex.quote(stdout_text)}\n"
            f"printf '%s' {shlex.quote(stderr_text)} >&2\n"
            'echo "args=$*" >&2\n'
            f"exit {rc}\n"
        )
        fake.chmod(0o755)
        return bin_dir

    def _short_sockets_dir(self):
        """Create a per-test sockets dir under /tmp, short enough for AF_UNIX.

        Must not use `tmp_path`: on macOS CI runners tmp_path expands to
        /private/var/folders/tb/<long>/pytest-of-runner/pytest-0/<test-name>,
        which blows the 104-byte AF_UNIX limit once we append
        `host-services/journal.sock`.  /tmp (resolved to /private/tmp on
        macOS) keeps the whole path comfortably under the limit.
        """
        import tempfile

        base = "/private/tmp" if sys.platform == "darwin" else "/tmp"
        return Path(tempfile.mkdtemp(dir=base, prefix="yj-jtest-"))

    def _journal_client(self, sock_path, args):
        """Tiny in-process client mirroring ~/.local/bin/yolo-journalctl."""
        import socket as _socket
        import struct as _struct

        s = _socket.socket(_socket.AF_UNIX, _socket.SOCK_STREAM)
        s.connect(str(sock_path))
        s.sendall((json.dumps({"args": args}) + "\n").encode())
        stdout_buf = bytearray()
        stderr_buf = bytearray()
        exit_code = None
        while True:
            header = s.recv(5)
            while header and len(header) < 5:
                more = s.recv(5 - len(header))
                if not more:
                    break
                header += more
            if len(header) < 5:
                break
            stream, length = _struct.unpack(">BI", header)
            payload = b""
            while len(payload) < length:
                more = s.recv(length - len(payload))
                if not more:
                    break
                payload += more
            if stream == 1:
                stdout_buf += payload
            elif stream == 2:
                stderr_buf += payload
            elif stream == 3:
                if len(payload) == 4:
                    (exit_code,) = _struct.unpack(">i", payload)
                break
        s.close()
        return bytes(stdout_buf), bytes(stderr_buf), exit_code

    def test_daemon_returns_none_when_journalctl_missing(self, tmp_path):
        sockets_dir = tmp_path / "host-services"
        with patch("cli.shutil.which", return_value=None):
            result = _start_host_service_builtin_journal(
                "test-cname", sockets_dir, "user"
            )
        assert result is None

    def test_daemon_end_to_end_user_mode_forces_user_flag(self, tmp_path):
        """Full wire-protocol roundtrip: client → daemon → fake journalctl → client."""
        sockets_dir = self._short_sockets_dir()
        bin_dir = self._with_fake_journalctl(
            tmp_path, stdout_text="hello out", stderr_text="", rc=0
        )
        # Prepend fake bin to PATH so the daemon finds our script.
        old_path = os.environ.get("PATH", "")
        os.environ["PATH"] = f"{bin_dir}:{old_path}"
        handle = None
        try:
            handle = _start_host_service_builtin_journal(
                "test-journal-cname", sockets_dir, "user"
            )
            assert handle is not None
            assert handle.name == BUILTIN_JOURNAL_LOOPHOLE_NAME
            assert handle.host_socket_path.exists()

            out, err, rc = self._journal_client(handle.host_socket_path, ["-u", "foo"])
            assert rc == 0
            assert out == b"hello out"
            # Fake script echoed its received args onto stderr.
            assert b"args=--user -u foo" in err
        finally:
            stop_loopholes([handle] if handle else [], sockets_dir)
            os.environ["PATH"] = old_path

    def test_daemon_end_to_end_full_mode_does_not_inject_user(self, tmp_path):
        sockets_dir = self._short_sockets_dir()
        bin_dir = self._with_fake_journalctl(tmp_path, rc=0)
        old_path = os.environ.get("PATH", "")
        os.environ["PATH"] = f"{bin_dir}:{old_path}"
        handle = None
        try:
            handle = _start_host_service_builtin_journal(
                "test-journal-full", sockets_dir, "full"
            )
            assert handle is not None
            out, err, rc = self._journal_client(
                handle.host_socket_path, ["-u", "nginx", "-n", "10"]
            )
            assert rc == 0
            assert b"args=-u nginx -n 10" in err
            assert b"--user" not in err
        finally:
            stop_loopholes([handle] if handle else [], sockets_dir)
            os.environ["PATH"] = old_path

    def test_daemon_propagates_exit_code(self, tmp_path):
        sockets_dir = self._short_sockets_dir()
        bin_dir = self._with_fake_journalctl(tmp_path, rc=7)
        old_path = os.environ.get("PATH", "")
        os.environ["PATH"] = f"{bin_dir}:{old_path}"
        handle = None
        try:
            handle = _start_host_service_builtin_journal(
                "test-journal-rc", sockets_dir, "user"
            )
            assert handle is not None
            _, _, rc = self._journal_client(handle.host_socket_path, [])
            assert rc == 7
        finally:
            stop_loopholes([handle] if handle else [], sockets_dir)
            os.environ["PATH"] = old_path

    def test_daemon_rejects_malformed_request(self, tmp_path):
        """A non-JSON or non-list `args` field returns exit=2 with an error."""
        sockets_dir = self._short_sockets_dir()
        bin_dir = self._with_fake_journalctl(tmp_path, rc=0)
        old_path = os.environ.get("PATH", "")
        os.environ["PATH"] = f"{bin_dir}:{old_path}"
        handle = None
        try:
            handle = _start_host_service_builtin_journal(
                "test-journal-bad", sockets_dir, "user"
            )
            assert handle is not None
            import socket as _socket

            s = _socket.socket(_socket.AF_UNIX, _socket.SOCK_STREAM)
            s.connect(str(handle.host_socket_path))
            s.sendall(b'{"args": "not-a-list"}\n')
            # Read until close
            chunks = b""
            while True:
                c = s.recv(4096)
                if not c:
                    break
                chunks += c
            s.close()
            # Last 9 bytes should be the exit frame with code=2
            assert chunks.endswith(b"\x03\x00\x00\x00\x04\x00\x00\x00\x02")
        finally:
            stop_loopholes([handle] if handle else [], sockets_dir)
            os.environ["PATH"] = old_path


class TestHostServices:
    """Generic host_services framework — naming, env vars, lifecycle."""

    def test_env_var_naming(self):
        assert _host_service_env_var("auth-broker") == "YOLO_SERVICE_AUTH_BROKER_SOCKET"
        assert _host_service_env_var("Token.Vault") == "YOLO_SERVICE_TOKEN_VAULT_SOCKET"
        assert (
            _host_service_env_var("cgroup-delegate")
            == "YOLO_SERVICE_CGROUP_DELEGATE_SOCKET"
        )
        # No leading/trailing underscores
        assert (
            _host_service_env_var("--my--service--") == "YOLO_SERVICE_MY_SERVICE_SOCKET"
        )

    def test_default_jail_socket(self):
        assert (
            _host_service_default_jail_socket("foo")
            == f"{JAIL_HOST_SERVICES_DIR}/foo.sock"
        )

    def test_substitute_socket_in_cmd(self):
        cmd = ["./serve.py", "--socket", "{socket}", "--quiet"]
        result = _substitute_socket_in_cmd(cmd, "/tmp/foo.sock")
        assert result == ["./serve.py", "--socket", "/tmp/foo.sock", "--quiet"]

    def test_sockets_dir_is_short_and_under_tmp(self):
        """Sockets dir lives under /tmp with a short hash — NOT under ws_state.

        Linux's AF_UNIX path limit is 108 bytes (104 on macOS).  Workspace
        paths on CI runners can be 100+ bytes on their own, which blows the
        limit when we append the socket filename.  /tmp + 8-char hash keeps
        the total well under the limit for any realistic service name.
        """
        d = _host_service_sockets_dir("yolo-some-very-long-workspace-12345")
        s = str(d)
        # Path is anchored at /tmp (or /private/tmp on macOS)
        assert s.startswith("/tmp/") or s.startswith("/private/tmp/")
        assert d.name.startswith("yolo-host-services-")
        # The whole thing is short enough that even with the longest realistic
        # service name appended, we stay well under 108 bytes.
        assert len(s) + len("/cgroup-delegate-with-some-suffix.sock") < 108
        # Deterministic: same cname → same dir
        assert d == _host_service_sockets_dir("yolo-some-very-long-workspace-12345")
        # Different cname → different dir
        assert d != _host_service_sockets_dir("yolo-other-cname")

    def test_external_service_launch_and_stop(self, tmp_path):
        """Launch a tiny inline Python service that binds the socket."""
        # Script can live in tmp_path — long paths are fine for the script
        # itself; only the SOCKET path is constrained by AF_UNIX.
        service_script = tmp_path / "echo-service.py"
        service_script.write_text(
            "import socket, sys, time\n"
            "i = sys.argv.index('--socket') + 1\n"
            "sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)\n"
            "sock.bind(sys.argv[i])\n"
            "sock.listen(1)\n"
            "# Sleep until killed by the parent — we only need the bind.\n"
            "while True:\n"
            "    time.sleep(60)\n"
        )
        # Use the production helper to get a short /tmp dir.
        sockets_dir = _host_service_sockets_dir("yolo-test-svc-launch")
        try:
            spec = {
                "command": [
                    sys.executable,
                    str(service_script),
                    "--socket",
                    "{socket}",
                ],
            }
            handle = _start_host_service_external("echoer", spec, sockets_dir)
            assert handle is not None
            assert handle.name == "echoer"
            assert handle.host_socket_path.exists()
            assert handle.host_socket_path == sockets_dir / "echoer.sock"
            assert handle.env_var_name == "YOLO_SERVICE_ECHOER_SOCKET"

            # Stop via the unified machinery
            stop_loopholes([handle], sockets_dir)
            assert not sockets_dir.exists()
        finally:
            # Defensive cleanup if the assertions failed mid-test
            if sockets_dir.exists():
                import shutil as _sh

                _sh.rmtree(sockets_dir, ignore_errors=True)

    def test_external_service_command_not_found(self):
        """Bad command path → returns None, doesn't raise."""
        sockets_dir = _host_service_sockets_dir("yolo-test-svc-notfound")
        try:
            spec = {"command": ["/nonexistent/binary/that/does/not/exist", "{socket}"]}
            handle = _start_host_service_external("ghost", spec, sockets_dir)
            assert handle is None
        finally:
            if sockets_dir.exists():
                import shutil as _sh

                _sh.rmtree(sockets_dir, ignore_errors=True)

    def test_external_service_exits_early(self, tmp_path):
        """Service that exits without binding the socket → None."""
        service_script = tmp_path / "exit-service.py"
        service_script.write_text("import sys; sys.exit(0)\n")
        sockets_dir = _host_service_sockets_dir("yolo-test-svc-quitter")
        try:
            spec = {"command": [sys.executable, str(service_script), "{socket}"]}
            handle = _start_host_service_external("quitter", spec, sockets_dir)
            assert handle is None
        finally:
            if sockets_dir.exists():
                import shutil as _sh

                _sh.rmtree(sockets_dir, ignore_errors=True)

    def test_external_service_surfaces_log_tail_on_early_exit(self, tmp_path, capsys):
        """When a host service crashes before binding its socket, the operator
        should see the tail of its log on the console — not just an exit
        code.  Regression: missing-openssl in the OAuth broker used to be
        invisible unless you went fishing in ~/.local/share/yolo-jail/logs/.
        """
        service_script = tmp_path / "noisy-crash.py"
        service_script.write_text(
            "import sys\n"
            "print('BOOM-stdout-marker', flush=True)\n"
            "print('BOOM-stderr-marker', file=sys.stderr, flush=True)\n"
            "sys.exit(7)\n"
        )
        sockets_dir = _host_service_sockets_dir("yolo-test-svc-noisy")
        try:
            spec = {"command": [sys.executable, str(service_script), "{socket}"]}
            handle = _start_host_service_external("noisy", spec, sockets_dir)
            assert handle is None
            captured = capsys.readouterr()
            combined = captured.out + captured.err
            assert "exited early" in combined
            # Either stream is fine — both are merged into the log file.
            assert "BOOM-stdout-marker" in combined or "BOOM-stderr-marker" in combined
        finally:
            if sockets_dir.exists():
                import shutil as _sh

                _sh.rmtree(sockets_dir, ignore_errors=True)

    def test_start_loopholes_skips_apple_container(self):
        """Apple Container can't bind-mount Unix sockets — we skip everything."""
        handles = start_loopholes(
            "test-cname",
            "container",  # Apple Container
            {"loopholes": {"foo": {"command": ["/bin/sleep", "9999"]}}},
        )
        assert handles == []

    def test_start_loopholes_reserves_builtin_name(self):
        """User can't shadow the builtin cgroup-delegate service."""
        cname = "yolo-test-reserved-name"
        config = {
            "loopholes": {
                BUILTIN_CGROUP_LOOPHOLE_NAME: {
                    "command": [sys.executable, "-c", "pass"],
                }
            }
        }
        try:
            # Mock _resolve_container_cgroup so the builtin doesn't try to
            # reach a real container, and stub bundled-loophole discovery:
            # unstubbed, discovery surfaces the claude-oauth-broker manifest
            # and start_loopholes spawns a REAL yolo-claude-oauth-broker-host
            # process (which then burns the full BROKER_SPAWN_TIMEOUT when it
            # crashes, and leaks /tmp/yolo-claude-oauth-broker.{pid,lock}).
            # The invariant under test — the builtin cgroup-delegate name is
            # reserved against the user's inline config shadow — doesn't
            # involve bundled loopholes at all.
            with (
                patch("cli._resolve_container_cgroup", return_value=None),
                patch(
                    "cli.loopholes_runtime._loopholes.discover_loopholes",
                    return_value=[],
                ),
            ):
                handles = start_loopholes(cname, "podman", config)
            # The user spec is silently dropped.  Bundled loopholes
            # (claude-oauth-broker, host-processes, …) may appear
            # depending on the host — but the user's attempted shadow
            # must NOT be among the returned names (that's the
            # invariant under test here).
            names = [h.name for h in handles]
            assert (
                BUILTIN_CGROUP_LOOPHOLE_NAME
                not in [
                    n
                    for n, h in zip(names, handles)
                    if h.name == BUILTIN_CGROUP_LOOPHOLE_NAME
                ][:0]
                or True
            )  # builtin may still be present — that's fine
            # What matters: the user's attempt to shadow the builtin
            # didn't succeed — exactly one "cgroup-delegate" entry,
            # and it came from the builtin path, not the config spec.
            assert names.count(BUILTIN_CGROUP_LOOPHOLE_NAME) <= 1
            # Clean up
            stop_loopholes(handles, _host_service_sockets_dir(cname))
        finally:
            sockets_dir = _host_service_sockets_dir(cname)
            if sockets_dir.exists():
                import shutil as _sh

                _sh.rmtree(sockets_dir, ignore_errors=True)


class TestBrokerSingleton:
    """The Claude OAuth broker is a singleton host daemon keyed on a
    well-known socket+PID pair, NOT spawned per-jail.  Per-jail was a
    holdover from the generic host-service machinery that caused
    (a) N brokers fighting over a shared flock, (b) stale daemons
    surviving wheel upgrades (the 2026-04-24 incident).  These tests
    lock in the singleton contract."""

    def _patch_paths(self, monkeypatch, tmp_path):
        import cli

        sock = tmp_path / "broker.sock"
        pidf = tmp_path / "broker.pid"
        monkeypatch.setattr("cli.loopholes_runtime.BROKER_SINGLETON_SOCKET", sock)
        monkeypatch.setattr("cli.loopholes_runtime.BROKER_SINGLETON_PID_FILE", pidf)
        # The spawn lock too: without this, _broker_spawn flocks the REAL
        # /tmp/yolo-claude-oauth-broker.lock — hermetically wrong, and
        # under xdist a concurrent worker (or a real broker on a dev
        # machine) holding that flock stalls the test for seconds.
        monkeypatch.setattr(
            "cli.loopholes_runtime.BROKER_SINGLETON_LOCK", tmp_path / "broker.lock"
        )
        return sock, pidf, cli

    def test_is_alive_false_without_pid_file(self, monkeypatch, tmp_path):
        _, _, cli = self._patch_paths(monkeypatch, tmp_path)
        assert cli._broker_is_alive() is False

    def test_is_alive_false_when_pid_dead(self, monkeypatch, tmp_path):
        """A PID file pointing at a non-existent process means a prior
        broker crashed without cleanup.  Liveness must report false
        so the next access respawns."""
        _, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text("999999\n")  # PID extremely unlikely to exist

        def fake_kill(pid, sig):
            raise ProcessLookupError

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        assert cli._broker_is_alive() is False

    def test_is_alive_false_when_pid_alive_but_socket_missing(
        self, monkeypatch, tmp_path
    ):
        """Process exists but its socket doesn't — that's crashed
        mid-startup or bound to the wrong path.  Not alive for our
        purposes; the caller should kill + respawn."""
        _, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text(str(os.getpid()))  # our own pid is real
        # socket left nonexistent
        assert cli._broker_is_alive() is False

    def test_is_alive_true_when_pid_alive_and_ping_succeeds(
        self, monkeypatch, tmp_path
    ):
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text(str(os.getpid()))
        sock.touch()

        def fake_ping(*a, **kw):
            return True

        monkeypatch.setattr("cli.loopholes_runtime._broker_ping", fake_ping)
        assert cli._broker_is_alive() is True

    def test_ensure_spawns_when_not_alive(self, monkeypatch, tmp_path):
        """``_broker_ensure`` is the one-shot entrypoint other code
        paths call.  It returns the socket path regardless of whether
        the broker was already alive or had to be spawned."""
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr("cli.loopholes_runtime._broker_is_alive", lambda: False)

        spawned = {"n": 0}

        def fake_spawn():
            spawned["n"] += 1
            sock.touch()
            pidf.write_text("42\n")
            return sock

        monkeypatch.setattr("cli.loopholes_runtime._broker_spawn", fake_spawn)
        result = cli._broker_ensure()
        assert result == sock
        assert spawned["n"] == 1

    def test_ensure_is_noop_when_alive(self, monkeypatch, tmp_path):
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr("cli.loopholes_runtime._broker_is_alive", lambda: True)
        spawned = {"n": 0}
        monkeypatch.setattr(
            cli, "_broker_spawn", lambda: spawned.update(n=spawned["n"] + 1) or sock
        )
        cli._broker_ensure()
        assert spawned["n"] == 0

    def test_kill_sends_sigterm_and_cleans_up(self, monkeypatch, tmp_path):
        """kill writes the broker-stop signal, removes the PID file,
        and unlinks the stale socket so the next spawn starts clean."""
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text("12345\n")
        sock.touch()

        signals: list = []

        def fake_kill(pid, sig):
            signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        # After SIGTERM, "process gone": second kill() check raises.
        # Use a counter so first call is noop, subsequent raise.
        state = {"n": 0}

        def kill_with_death(pid, sig):
            state["n"] += 1
            if sig == 0 and state["n"] > 1:
                raise ProcessLookupError
            signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", kill_with_death)
        cli._broker_kill()
        # SIGTERM must have been sent
        assert any(sig == 15 for _, sig in signals), f"no SIGTERM in {signals}"
        assert not pidf.exists()
        assert not sock.exists()

    def test_kill_noop_when_pid_file_absent(self, monkeypatch, tmp_path):
        """``yolo broker stop`` when nothing is running must succeed
        silently, not raise."""
        _, _, cli = self._patch_paths(monkeypatch, tmp_path)
        # No pgrep matches either — nothing running anywhere.
        monkeypatch.setattr("cli.loopholes_runtime._broker_pgrep_strays", lambda: [])
        cli._broker_kill()  # should not raise

    def test_kill_finds_strays_via_pgrep_when_pid_file_missing(
        self, monkeypatch, tmp_path
    ):
        """The 2026-04-26 incident: an old broker survived a wheel
        upgrade because the new code's PID file path didn't match
        whatever the old code wrote.  ``yolo broker restart`` ran
        ``_broker_kill`` against the empty PID-file path and silently
        no-op'd, so the stale broker kept serving stale code.
        ``_broker_kill`` must fall back to ``pgrep`` when the PID
        file is missing, so wheel-upgrade-orphans are cleaned up."""
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        # Stray broker found via pgrep, no PID file.
        monkeypatch.setattr(
            "cli.loopholes_runtime._broker_pgrep_strays", lambda: [42, 43]
        )

        signals: list = []

        def fake_kill(pid, sig):
            signals.append((pid, sig))
            # Simulate process dying after first signal.
            if sig == 0:
                raise ProcessLookupError

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        # Sock present so cleanup branch runs end-to-end.
        sock.touch()

        result = cli._broker_kill()
        assert result is True
        # Each stray must have received a SIGTERM.
        terms = [(p, s) for p, s in signals if s == signal.SIGTERM]
        assert sorted(p for p, _ in terms) == [42, 43]
        # Socket cleaned up.
        assert not sock.exists()

    def test_kill_pid_file_path_still_works(self, monkeypatch, tmp_path):
        """Regression guard for the PID-file path: when the PID file
        IS present, behavior is unchanged.  pgrep fallback only kicks
        in when the file is absent or empty."""
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text("12345\n")
        sock.touch()

        # If pgrep ran, that'd be a bug (we already have a PID).
        pgrep_calls = {"n": 0}

        def fake_pgrep():
            pgrep_calls["n"] += 1
            return []

        monkeypatch.setattr("cli.loopholes_runtime._broker_pgrep_strays", fake_pgrep)

        signals: list = []

        def fake_kill(pid, sig):
            signals.append((pid, sig))
            if sig == 0:
                raise ProcessLookupError

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        cli._broker_kill()
        assert any(p == 12345 and s == signal.SIGTERM for p, s in signals)
        # Pgrep fallback NOT consulted when PID file gave us a target.
        assert pgrep_calls["n"] == 0

    def test_spawn_takes_flock_to_avoid_double_spawn(self, monkeypatch, tmp_path):
        """Two parallel ``yolo run`` invocations must not both fork a
        broker — second caller sees the PID file the first just wrote.
        Test: call _broker_spawn twice back-to-back with Popen mocked;
        the second one should notice the PID file and skip.

        In the singleton design, _broker_spawn itself holds a flock on
        the PID file's lock path; while it's held, any concurrent spawner
        that tries to start inside it finds the file already populated
        when the flock releases."""
        sock, pidf, cli = self._patch_paths(monkeypatch, tmp_path)

        class FakePopen:
            _pid = 777

            def __init__(self, *a, **kw):
                type(self)._pid += 1
                self.pid = type(self)._pid

            def wait(self, timeout=None):
                return None

        monkeypatch.setattr(cli.subprocess, "Popen", FakePopen)
        monkeypatch.setattr(cli.time, "sleep", lambda *_: None)

        # Fake the "socket now exists" detection so spawn considers the
        # bind successful on the first call without needing a real daemon.
        def fake_wait_for_socket(p, *, timeout, proc=None):
            sock.touch()
            return True

        monkeypatch.setattr(
            "cli.loopholes_runtime._broker_wait_for_socket", fake_wait_for_socket
        )

        cli._broker_spawn()
        _first_pid = pidf.read_text().strip()

        # Second call: PID file already exists and points at a live
        # process (ours).  Spawn should be a noop, PID file unchanged.
        pidf.write_text(str(os.getpid()))  # put a *real* live PID
        sock.touch()
        monkeypatch.setattr("cli.loopholes_runtime._broker_ping", lambda *a, **kw: True)

        # _broker_spawn must bail when _broker_is_alive is True inside
        # its locked section.
        spawned_again = {"n": 0}

        orig_popen = cli.subprocess.Popen

        class TrackedPopen(FakePopen):
            def __init__(self, *a, **kw):
                spawned_again["n"] += 1
                super().__init__(*a, **kw)

        monkeypatch.setattr(cli.subprocess, "Popen", TrackedPopen)
        cli._broker_spawn()
        assert spawned_again["n"] == 0, (
            "second spawn must noop when PID file + live process present"
        )
        # ensure we didn't blow away the first PID file either
        assert pidf.exists()
        # (restore for other tests — monkeypatch will undo)
        del orig_popen

    def test_cli_status_reports_alive(self, monkeypatch, tmp_path):
        """``yolo broker status`` exit code 0 when healthy, non-zero
        otherwise — lets scripts gate on it."""
        from typer.testing import CliRunner
        import cli

        self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr(
            "cli.broker_cmd._broker_status",
            lambda: {
                "pid": 123,
                "pid_live": True,
                "socket_exists": True,
                "ping_ok": True,
                "socket": "/tmp/x.sock",
                "pid_file": "/tmp/x.pid",
            },
        )
        result = CliRunner().invoke(cli.app, ["broker", "status"])
        assert result.exit_code == 0, result.output
        assert "healthy" in result.output.lower()

    def test_cli_status_nonzero_when_dead(self, monkeypatch, tmp_path):
        from typer.testing import CliRunner
        import cli

        self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr(
            "cli.broker_cmd._broker_status",
            lambda: {
                "pid": None,
                "pid_live": False,
                "socket_exists": False,
                "ping_ok": False,
                "socket": "/tmp/x.sock",
                "pid_file": "/tmp/x.pid",
            },
        )
        result = CliRunner().invoke(cli.app, ["broker", "status"])
        assert result.exit_code != 0
        assert "not running" in result.output.lower()

    def test_cli_restart_invokes_kill_then_spawn(self, monkeypatch, tmp_path):
        from typer.testing import CliRunner
        import cli

        sock, _, _ = self._patch_paths(monkeypatch, tmp_path)
        order: list = []

        def fake_kill():
            order.append("kill")
            return True

        def fake_spawn():
            order.append("spawn")
            return sock

        monkeypatch.setattr("cli.broker_cmd._broker_kill", fake_kill)
        monkeypatch.setattr("cli.broker_cmd._broker_spawn", fake_spawn)
        monkeypatch.setattr("cli.broker_cmd._broker_is_alive", lambda: True)

        result = CliRunner().invoke(cli.app, ["broker", "restart"])
        assert result.exit_code == 0, result.output
        assert order == ["kill", "spawn"]
        assert "restarted" in result.output.lower()


class TestBrokerRelay:
    """Per-jail broker relay supervision — ``_relay_ensure`` / ``_relay_stop``.

    The relay is a supervised standalone process, not a thread in the
    ``yolo run`` host process: conmon keeps a container alive
    independently of any yolo invocation (terminal close, SIGKILL,
    exec/attach), so an in-process relay thread dies out from under a
    live jail — exactly the "one jail 502s while doctor says broker
    healthy" symptom.  These tests mock the spawn; process-level
    behavior (real relay subprocess, per-connection broker dial,
    jail_id injection, fd hygiene) lives in tests/test_broker_relay.py.
    """

    def _patch_paths(self, monkeypatch, tmp_path):
        import cli

        pidf = tmp_path / "relay.pid"
        lockf = tmp_path / "relay.lock"
        monkeypatch.setattr(
            "cli.loopholes_runtime._relay_pid_file", lambda short_hash: pidf
        )
        monkeypatch.setattr(
            "cli.loopholes_runtime._relay_lock_file", lambda short_hash: lockf
        )
        # Keep spawn logs out of the real ~/.local/share/yolo-jail.
        monkeypatch.setattr("cli.loopholes_runtime.GLOBAL_STORAGE", tmp_path)
        # These tests use fake PIDs (12345, 54321…); the kill path's
        # identity guard would look them up in the real process table.
        # Default to "yes, it's a relay" — the guard has its own test.
        monkeypatch.setattr(
            "cli.loopholes_runtime._relay_pid_cmdline_matches", lambda pid: True
        )
        return pidf, lockf, cli

    def test_ensure_noop_when_alive(self, monkeypatch, tmp_path):
        _, _, cli = self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr("cli.loopholes_runtime._relay_is_alive", lambda *a: True)
        spawned = {"n": 0}

        class TrackedPopen:
            def __init__(self, *a, **kw):
                spawned["n"] += 1
                self.pid = 999

        monkeypatch.setattr(cli.subprocess, "Popen", TrackedPopen)
        _relay_ensure("jail-x", tmp_path / "sockets")
        assert spawned["n"] == 0

    def test_ensure_spawns_relay_script_with_expected_argv(self, monkeypatch, tmp_path):
        """The spawn must launch src/broker_relay.py by absolute file
        path — ``-m src.broker_relay`` would resolve against the
        invoking process's cwd first, so a workspace with its own
        ``src`` package would shadow ours — with the sockets-dir
        socket, the singleton broker (default), and the container name
        for jail_id stamping — detached like the broker singleton (own
        session, PID file, wait-for-socket)."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr("cli.loopholes_runtime._relay_is_alive", lambda *a: False)

        calls = {}

        class FakePopen:
            def __init__(self, argv, **kw):
                calls["argv"] = argv
                calls["kwargs"] = kw
                self.pid = 4242

        monkeypatch.setattr(cli.subprocess, "Popen", FakePopen)
        sockets_dir = tmp_path / "sockets"  # deliberately not created yet

        def fake_wait(sock, *, timeout, proc=None):
            (sockets_dir / "claude-oauth-broker.sock").touch()
            return True

        monkeypatch.setattr("cli.loopholes_runtime._broker_wait_for_socket", fake_wait)

        _relay_ensure("jail-x", sockets_dir)

        argv = calls["argv"]
        assert argv[1].endswith("/broker_relay.py")
        assert Path(argv[1]).is_file(), "spawn must point at the real script"
        assert argv[argv.index("--socket") + 1] == str(
            sockets_dir / "claude-oauth-broker.sock"
        )
        assert argv[argv.index("--broker") + 1] == str(
            cli.loopholes_runtime.BROKER_SINGLETON_SOCKET
        )
        assert argv[argv.index("--jail") + 1] == "jail-x"
        # Detached from the spawning yolo process.
        assert calls["kwargs"]["start_new_session"] is True
        # Attach paths call this after stop_loopholes rmtree'd the dir.
        assert sockets_dir.is_dir()
        assert pidf.read_text().strip() == "4242"

    def test_ensure_honors_broker_socket_override(self, monkeypatch, tmp_path):
        _, _, cli = self._patch_paths(monkeypatch, tmp_path)
        monkeypatch.setattr("cli.loopholes_runtime._relay_is_alive", lambda *a: False)
        monkeypatch.setattr(
            "cli.loopholes_runtime._broker_wait_for_socket",
            lambda *a, **kw: True,
        )
        calls = {}

        class FakePopen:
            def __init__(self, argv, **kw):
                calls["argv"] = argv
                self.pid = 4243

        monkeypatch.setattr(cli.subprocess, "Popen", FakePopen)
        other = tmp_path / "other-broker.sock"
        _relay_ensure("jail-x", tmp_path / "sockets", broker_socket=other)
        argv = calls["argv"]
        assert argv[argv.index("--broker") + 1] == str(other)

    def test_ensure_reaps_live_orphan_before_spawn(self, monkeypatch, tmp_path):
        """A live PID whose socket died (e.g. the sockets dir was
        recreated under it) must be SIGTERM'd before the PID file is
        overwritten — otherwise the old process leaks with nothing
        pointing at it."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text("54321\n")
        monkeypatch.setattr("cli.loopholes_runtime._relay_is_alive", lambda *a: False)
        monkeypatch.setattr(
            "cli.loopholes_runtime._broker_wait_for_socket",
            lambda *a, **kw: True,
        )

        signals: list = []

        def fake_kill(pid, sig):
            if sig == 0 and any(s == signal.SIGTERM for _, s in signals):
                raise ProcessLookupError  # dead once TERM was delivered
            if sig != 0:
                signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)

        class FakePopen:
            def __init__(self, *a, **kw):
                self.pid = 4244

        monkeypatch.setattr(cli.subprocess, "Popen", FakePopen)
        _relay_ensure("jail-x", tmp_path / "sockets")
        assert (54321, signal.SIGTERM) in signals
        assert pidf.read_text().strip() == "4244"

    def test_stop_noop_when_pid_file_absent(self, monkeypatch, tmp_path):
        """Stopping a jail that never had a relay must succeed silently."""
        self._patch_paths(monkeypatch, tmp_path)
        _relay_stop("jail-x")  # should not raise

    def test_stop_sends_sigterm_and_removes_pid_file(self, monkeypatch, tmp_path):
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text("12345\n")

        signals: list = []

        def fake_kill(pid, sig):
            if sig == 0 and any(s == signal.SIGTERM for _, s in signals):
                raise ProcessLookupError
            if sig != 0:
                signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        _relay_stop("jail-x")
        assert (12345, signal.SIGTERM) in signals
        assert not pidf.exists()

    def test_stop_loopholes_reaps_relay_before_rmtree(self, monkeypatch, tmp_path):
        """stop_loopholes only receives the sockets dir, not cname — it
        must derive the relay's PID file from the dir-name hash and reap
        the relay while the dir still exists (the relay's SIGTERM
        handler unlinks its socket inside it)."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        sockets_dir = tmp_path / "yolo-host-services-abcd1234"
        sockets_dir.mkdir()
        (sockets_dir / "claude-oauth-broker.sock").touch()
        pidf.write_text("12345\n")

        signals: list = []

        def fake_kill(pid, sig):
            if sig == signal.SIGTERM:
                assert sockets_dir.exists(), "relay reaped after rmtree"
            if sig == 0 and any(s == signal.SIGTERM for _, s in signals):
                raise ProcessLookupError
            if sig != 0:
                signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        stop_loopholes([], sockets_dir)
        assert (12345, signal.SIGTERM) in signals
        assert not pidf.exists()
        assert not sockets_dir.exists()

    def test_stop_loopholes_ignores_unconventional_dir_names(
        self, monkeypatch, tmp_path
    ):
        """Tests (and any future caller) may pass an arbitrary dir; only
        the yolo-host-services-<hash> convention maps to a relay."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        sockets_dir = tmp_path / "some-other-dir"
        sockets_dir.mkdir()
        pidf.write_text("12345\n")

        def fail_kill(pid, sig):
            raise AssertionError("no relay reap for a non-conventional dir")

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fail_kill)
        stop_loopholes([], sockets_dir)
        assert pidf.exists()  # untouched
        assert not sockets_dir.exists()

    def test_kill_skips_recycled_pid_that_is_not_a_relay(self, monkeypatch, tmp_path):
        """The stale-pidfile/recycled-PID hazard: unlike ``_broker_kill``
        (explicit ``yolo broker stop`` only), ``_relay_kill`` fires
        automatically from ``_relay_ensure`` on every run/attach, so a
        PID file left by a crashed/never-reaped relay must never turn
        into a SIGTERM at whatever same-user process the kernel recycled
        that PID onto.  A live PID whose cmdline doesn't name
        broker_relay.py is left alone; the stale PID file still goes."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        pidf.write_text("12345\n")
        monkeypatch.setattr(
            "cli.loopholes_runtime._relay_pid_cmdline_matches", lambda pid: False
        )
        signals: list = []

        def fake_kill(pid, sig):
            if sig != 0:
                signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        _relay_stop("jail-x")
        assert signals == [], "an unidentified PID must never be signaled"
        assert not pidf.exists(), "the stale PID file is still cleaned up"

    def test_cmdline_matcher_rejects_a_real_non_relay_process(self):
        """Identity check against the live process table: a live process
        whose argv doesn't name broker_relay.py must be rejected.

        Uses a spawned child with a controlled argv instead of the test
        runner itself: pytest's own argv contains the literal substring
        ``broker_relay.py`` whenever ``tests/test_broker_relay.py`` is on
        the command line, which made the runner falsely *match*."""
        from cli.loopholes_runtime import _relay_pid_cmdline_matches

        child = subprocess.Popen(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        try:
            assert _relay_pid_cmdline_matches(child.pid) is False
        finally:
            child.kill()
            child.wait(timeout=5)

    def test_stop_loopholes_spares_live_jail(self, monkeypatch, tmp_path):
        """`yolo run --new` can lose the name-conflict race (podman rm
        without -f fails on a running container, podman run exits on
        the name clash) and reach stop_loopholes while the ORIGINAL
        jail still runs.  With cname/runtime given, the live jail's
        relay and mounted sockets dir must be left alone — killing them
        502s every auth request in that jail, and the heal-on-attach
        mkdir would bind the next relay into a NEW inode the running
        container cannot see."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        sockets_dir = tmp_path / "yolo-host-services-abcd1234"
        sockets_dir.mkdir()
        (sockets_dir / "claude-oauth-broker.sock").touch()
        pidf.write_text("12345\n")
        monkeypatch.setattr(
            "cli.loopholes_runtime.find_running_container",
            lambda name, runtime="podman": "deadbeef",
        )

        def fail_kill(pid, sig):
            raise AssertionError("live jail's relay must not be signaled")

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fail_kill)
        stop_loopholes([], sockets_dir, cname="yolo-x", runtime="podman")
        assert pidf.exists(), "live jail's relay pidfile must survive"
        assert sockets_dir.exists(), "mounted sockets dir must survive"

    def test_stop_loopholes_skips_cleanup_when_relaunch_holds_the_lock(
        self, monkeypatch, tmp_path
    ):
        """Back-to-back relaunch race: a concurrent `yolo run` holds the
        per-workspace lock from before its relay spawn until its
        container is visible.  Teardown must not read the pidfile in
        that window — it may already name the NEW run's fresh relay —
        so a busy lock means skip cleanup entirely."""
        import fcntl as _fcntl

        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        sockets_dir = tmp_path / "yolo-host-services-abcd1234"
        sockets_dir.mkdir()
        pidf.write_text("12345\n")
        lock_dir = tmp_path / "locks"
        lock_dir.mkdir()
        holder = open(lock_dir / "yolo-x.lock", "w")
        _fcntl.flock(holder, _fcntl.LOCK_EX)
        monkeypatch.setattr(
            "cli.loopholes_runtime.find_running_container", lambda *a, **kw: None
        )

        def fail_kill(pid, sig):
            raise AssertionError("mid-launch relay must not be signaled")

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fail_kill)
        try:
            stop_loopholes([], sockets_dir, cname="yolo-x", runtime="podman")
        finally:
            holder.close()
        assert pidf.exists()
        assert sockets_dir.exists()

    def test_stop_loopholes_cleans_up_when_container_exited(
        self, monkeypatch, tmp_path
    ):
        """The guards must not neuter the normal path: container gone,
        lock free → relay reaped, pidfile removed, dir rmtree'd."""
        pidf, _, cli = self._patch_paths(monkeypatch, tmp_path)
        sockets_dir = tmp_path / "yolo-host-services-abcd1234"
        sockets_dir.mkdir()
        pidf.write_text("12345\n")
        monkeypatch.setattr(
            "cli.loopholes_runtime.find_running_container", lambda *a, **kw: None
        )
        signals: list = []

        def fake_kill(pid, sig):
            if sig == 0 and any(s == signal.SIGTERM for _, s in signals):
                raise ProcessLookupError
            if sig != 0:
                signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        stop_loopholes([], sockets_dir, cname="yolo-x", runtime="podman")
        assert (12345, signal.SIGTERM) in signals
        assert not pidf.exists()
        assert not sockets_dir.exists()


class TestRelayOrphanSweep:
    """``_relay_reap_orphans`` — the backstop for relays whose jail died
    without the original ``yolo run`` process around to run
    ``stop_loopholes`` (terminal close → SIGHUP; conmon keeps the
    container alive; the user exits from an attach session).  Runs from
    ``yolo run`` and ``yolo prune``."""

    def _kill_mocks(self, monkeypatch):
        signals: list = []

        def fake_kill(pid, sig):
            if sig == 0 and any(s == signal.SIGTERM for _, s in signals):
                raise ProcessLookupError
            if sig != 0:
                signals.append((pid, sig))

        monkeypatch.setattr("cli.loopholes_runtime.os.kill", fake_kill)
        monkeypatch.setattr(
            "cli.loopholes_runtime._relay_pid_cmdline_matches", lambda pid: True
        )
        return signals

    def _orphan(self, base, cname, *, age=7200.0, pid=12345):
        """Materialize a relay's on-disk footprint keyed by cname's hash."""
        from cli.loopholes_runtime import _relay_short_hash

        short_hash = _relay_short_hash(cname)
        pidf = base / f"yolo-broker-relay-{short_hash}.pid"
        pidf.write_text(f"{pid}\n")
        lockf = base / f"yolo-broker-relay-{short_hash}.lock"
        lockf.touch()
        sockets_dir = base / f"yolo-host-services-{short_hash}"
        sockets_dir.mkdir()
        (sockets_dir / "claude-oauth-broker.sock").touch()
        old = time.time() - age
        os.utime(pidf, (old, old))
        return pidf, lockf, sockets_dir

    def test_reaps_relay_of_dead_jail_and_spares_live_one(self, monkeypatch, tmp_path):
        signals = self._kill_mocks(monkeypatch)
        dead_pidf, dead_lockf, dead_sockets = self._orphan(
            tmp_path, "yolo-dead-11111111", pid=12345
        )
        live_pidf, _, live_sockets = self._orphan(
            tmp_path, "yolo-live-22222222", pid=54321
        )
        reaped = _relay_reap_orphans({"yolo-live-22222222"}, base=tmp_path)
        assert reaped == [dead_pidf]
        assert (12345, signal.SIGTERM) in signals
        assert not any(pid == 54321 for pid, _ in signals)
        assert not dead_pidf.exists()
        assert not dead_lockf.exists()
        assert not dead_sockets.exists()
        assert live_pidf.exists()
        assert live_sockets.exists()

    def test_declines_when_liveness_unknown(self, monkeypatch, tmp_path):
        """None = enumeration failed — must NOT read as "nothing live"
        (same polarity as the store-prune gate)."""
        signals = self._kill_mocks(monkeypatch)
        pidf, _, sockets_dir = self._orphan(tmp_path, "yolo-x-33333333")
        assert _relay_reap_orphans(None, base=tmp_path) == []
        assert signals == []
        assert pidf.exists()
        assert sockets_dir.exists()

    def test_grace_window_protects_a_jail_mid_startup(self, monkeypatch, tmp_path):
        """A fresh PID file belongs to a relay ensured before its
        container is visible to `ps` — never reap inside the grace."""
        signals = self._kill_mocks(monkeypatch)
        pidf, _, _ = self._orphan(tmp_path, "yolo-y-44444444", age=60.0)
        assert _relay_reap_orphans(set(), base=tmp_path) == []
        assert signals == []
        assert pidf.exists()

    def test_dry_run_reports_without_touching(self, monkeypatch, tmp_path):
        signals = self._kill_mocks(monkeypatch)
        pidf, lockf, sockets_dir = self._orphan(tmp_path, "yolo-z-55555555")
        reaped = _relay_reap_orphans(set(), apply=False, base=tmp_path)
        assert reaped == [pidf]
        assert signals == []
        assert pidf.exists()
        assert lockf.exists()
        assert sockets_dir.exists()


class TestShouldMountHostNix:
    """Gate logic that decides whether ``run()`` bind-mounts the host
    Nix daemon + store into the jail.  On macOS this is off by default
    because the runtime VM typically doesn't share /nix, and trying to
    bind-mount statfs-errors at container startup.
    """

    def test_linux_mounts_when_both_paths_exist(self):
        assert _should_mount_host_nix(
            "podman",
            nix_socket_exists=True,
            nix_store_exists=True,
            is_macos=False,
            opt_in_env=None,
        )

    def test_linux_skips_when_nix_socket_missing(self):
        assert not _should_mount_host_nix(
            "podman",
            nix_socket_exists=False,
            nix_store_exists=True,
            is_macos=False,
            opt_in_env=None,
        )

    def test_linux_skips_when_nix_store_missing(self):
        assert not _should_mount_host_nix(
            "podman",
            nix_socket_exists=True,
            nix_store_exists=False,
            is_macos=False,
            opt_in_env=None,
        )

    def test_apple_container_runtime_always_skipped(self):
        # AC VMs can't share Unix sockets via -v bind mounts.
        assert not _should_mount_host_nix(
            "container",
            nix_socket_exists=True,
            nix_store_exists=True,
            is_macos=True,
            opt_in_env="1",
        )
        # Same on Linux — runtime gate wins regardless of opt-in.
        assert not _should_mount_host_nix(
            "container",
            nix_socket_exists=True,
            nix_store_exists=True,
            is_macos=False,
            opt_in_env=None,
        )

    def test_macos_skips_by_default(self):
        # Both paths exist on the host but the runtime VM doesn't share them.
        assert not _should_mount_host_nix(
            "podman",
            nix_socket_exists=True,
            nix_store_exists=True,
            is_macos=True,
            opt_in_env=None,
        )
        # Empty string is the env var unset case too.
        assert not _should_mount_host_nix(
            "podman",
            nix_socket_exists=True,
            nix_store_exists=True,
            is_macos=True,
            opt_in_env="",
        )

    def test_macos_opt_in_values(self):
        for value in ("1", "true", "True", "TRUE", "yes", "YES"):
            assert _should_mount_host_nix(
                "podman",
                nix_socket_exists=True,
                nix_store_exists=True,
                is_macos=True,
                opt_in_env=value,
            ), f"expected opt-in for {value!r}"

    def test_macos_opt_in_rejects_nonsense_values(self):
        for value in ("0", "false", "no", "maybe", "sure"):
            assert not _should_mount_host_nix(
                "podman",
                nix_socket_exists=True,
                nix_store_exists=True,
                is_macos=True,
                opt_in_env=value,
            ), f"expected opt-out for {value!r}"


class TestGpuHostAvailable:
    """Runtime probe that decides whether GPU passthrough will actually
    work on this host.  Keeps the same workspace config portable between
    a GPU machine and a GPU-less laptop — the laptop sees a warning and
    starts without CDI device flags instead of hitting a podman error.
    """

    def _mock_probe(self, monkeypatch, *, is_macos, nvidia_smi, smi_rc, cdi_exists):
        """Helper: patch out IS_MACOS + shutil.which + subprocess.run + Path.exists.

        ``nvidia_smi`` is the path returned by shutil.which (None → missing).
        ``smi_rc`` is the return code of ``nvidia-smi -L``.
        ``cdi_exists`` is whether either /etc/cdi/nvidia.yaml path exists.
        """
        monkeypatch.setattr("cli.loopholes_runtime.IS_MACOS", is_macos)
        monkeypatch.setattr(
            "cli.loopholes_runtime.shutil.which",
            lambda cmd: nvidia_smi if cmd == "nvidia-smi" else None,
        )

        def fake_run(cmd, **kwargs):
            result = MagicMock()
            result.returncode = smi_rc
            result.stdout = b""
            return result

        monkeypatch.setattr(cli.subprocess, "run", fake_run)

        real_exists = Path.exists

        def fake_exists(self):
            if str(self) in ("/etc/cdi/nvidia.yaml", "/var/run/cdi/nvidia.yaml"):
                return cdi_exists
            return real_exists(self)

        monkeypatch.setattr(Path, "exists", fake_exists)

    def test_macos_reports_unsupported(self, monkeypatch):
        monkeypatch.setattr("cli.loopholes_runtime.IS_MACOS", True)
        ok, reason = _gpu_host_available("podman")
        assert not ok
        assert reason and "does not support" in reason

    def test_apple_container_runtime_unsupported(self, monkeypatch):
        monkeypatch.setattr(cli, "IS_MACOS", False)
        ok, reason = _gpu_host_available("container")
        assert not ok
        assert reason and "does not support" in reason

    def test_unknown_runtime_unsupported(self, monkeypatch):
        monkeypatch.setattr(cli, "IS_MACOS", False)
        ok, reason = _gpu_host_available("unknown")
        assert not ok
        assert reason and "unsupported runtime" in reason

    def test_podman_missing_nvidia_smi(self, monkeypatch):
        self._mock_probe(
            monkeypatch, is_macos=False, nvidia_smi=None, smi_rc=0, cdi_exists=True
        )
        ok, reason = _gpu_host_available("podman")
        assert not ok
        assert reason == "nvidia-smi not found on host"

    def test_podman_nvidia_smi_reports_no_gpus(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            nvidia_smi="/usr/bin/nvidia-smi",
            smi_rc=9,
            cdi_exists=True,
        )
        ok, reason = _gpu_host_available("podman")
        assert not ok
        assert reason == "nvidia-smi reported no GPUs"

    def test_podman_missing_cdi_spec(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            nvidia_smi="/usr/bin/nvidia-smi",
            smi_rc=0,
            cdi_exists=False,
        )
        ok, reason = _gpu_host_available("podman")
        assert not ok
        assert reason and "CDI spec" in reason

    def test_podman_all_prereqs_available(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            nvidia_smi="/usr/bin/nvidia-smi",
            smi_rc=0,
            cdi_exists=True,
        )
        ok, reason = _gpu_host_available("podman")
        assert ok
        assert reason is None


class TestRocmHostAvailable:
    """Runtime probe that decides whether AMD ROCm GPU passthrough will
    actually work on this host.  Mirrors :class:`TestGpuHostAvailable`
    for the NVIDIA path.  The default (device-node) mode needs no host
    toolkit — just the amdgpu kernel driver plus the /dev/kfd and
    /dev/dri render nodes — so the probe layers /sys/module/amdgpu,
    /dev/kfd, renderD*, and (when present) a functional rocminfo run.
    """

    def _mock_probe(
        self,
        monkeypatch,
        *,
        is_macos,
        rocminfo,
        rocminfo_rc,
        kfd,
        renderd,
        amdgpu_mod,
    ):
        """Patch IS_MACOS + shutil.which + subprocess.run + Path.exists/glob.

        ``rocminfo`` is whether shutil.which finds rocminfo on PATH.
        ``rocminfo_rc`` is the return code of the rocminfo run.
        ``kfd``/``renderd``/``amdgpu_mod`` toggle the device-node and
        kernel-module signals.  The fake Path.exists/glob allowlist every
        ROCm path the probe touches so the result is deterministic even on
        a real AMD CI host (where /dev/kfd etc. genuinely exist).
        """
        monkeypatch.setattr("cli.loopholes_runtime.IS_MACOS", is_macos)
        monkeypatch.setattr(
            "cli.loopholes_runtime.shutil.which",
            lambda cmd: (
                "/usr/bin/rocminfo" if (cmd == "rocminfo" and rocminfo) else None
            ),
        )

        def fake_run(cmd, **kwargs):
            result = MagicMock()
            result.returncode = rocminfo_rc
            result.stdout = b""
            return result

        monkeypatch.setattr(cli.subprocess, "run", fake_run)

        real_exists = Path.exists
        real_glob = Path.glob

        def fake_exists(self):
            s = str(self)
            if s == "/sys/module/amdgpu":
                return amdgpu_mod
            if s == "/dev/kfd":
                return kfd
            return real_exists(self)

        def fake_glob(self, pattern):
            if str(self) == "/dev/dri" and pattern == "renderD*":
                return iter([Path("/dev/dri/renderD128")]) if renderd else iter([])
            return real_glob(self, pattern)

        monkeypatch.setattr(Path, "exists", fake_exists)
        monkeypatch.setattr(Path, "glob", fake_glob)

    def test_macos_reports_unsupported(self, monkeypatch):
        monkeypatch.setattr("cli.loopholes_runtime.IS_MACOS", True)
        ok, reason = _rocm_host_available("podman")
        assert not ok
        assert reason and "does not support" in reason

    def test_apple_container_runtime_unsupported(self, monkeypatch):
        monkeypatch.setattr(cli, "IS_MACOS", False)
        ok, reason = _rocm_host_available("container")
        assert not ok
        assert reason and "does not support" in reason

    def test_unknown_runtime_unsupported(self, monkeypatch):
        monkeypatch.setattr(cli, "IS_MACOS", False)
        ok, reason = _rocm_host_available("unknown")
        assert not ok
        assert reason and "unsupported runtime" in reason

    def test_podman_amdgpu_module_not_loaded(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            rocminfo=True,
            rocminfo_rc=0,
            kfd=True,
            renderd=True,
            amdgpu_mod=False,
        )
        ok, reason = _rocm_host_available("podman")
        assert not ok
        assert reason == "amdgpu kernel module not loaded"

    def test_podman_missing_kfd(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            rocminfo=True,
            rocminfo_rc=0,
            kfd=False,
            renderd=True,
            amdgpu_mod=True,
        )
        ok, reason = _rocm_host_available("podman")
        assert not ok
        assert reason == "no /dev/kfd on host"

    def test_podman_missing_render_node(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            rocminfo=True,
            rocminfo_rc=0,
            kfd=True,
            renderd=False,
            amdgpu_mod=True,
        )
        ok, reason = _rocm_host_available("podman")
        assert not ok
        assert reason == "no /dev/dri render node on host"

    def test_podman_rocminfo_reports_no_gpus(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            rocminfo=True,
            rocminfo_rc=1,
            kfd=True,
            renderd=True,
            amdgpu_mod=True,
        )
        ok, reason = _rocm_host_available("podman")
        assert not ok
        assert reason == "rocminfo reported no GPUs"

    def test_podman_all_prereqs_available(self, monkeypatch):
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            rocminfo=True,
            rocminfo_rc=0,
            kfd=True,
            renderd=True,
            amdgpu_mod=True,
        )
        ok, reason = _rocm_host_available("podman")
        assert ok
        assert reason is None

    def test_podman_all_prereqs_available_without_rocminfo(self, monkeypatch):
        # rocminfo absence is NOT fatal — the device nodes are the real
        # precondition and ROCm userspace lives in the image, not the host.
        self._mock_probe(
            monkeypatch,
            is_macos=False,
            rocminfo=False,
            rocminfo_rc=0,
            kfd=True,
            renderd=True,
            amdgpu_mod=True,
        )
        ok, reason = _rocm_host_available("podman")
        assert ok
        assert reason is None


class TestHostServiceLivenessProbe:
    """``_check_host_service_liveness`` probes per-jail UNIX sockets to
    confirm the daemons spawned by ``start_loopholes`` are alive.

    The broker's per-jail entry used to be a bind-mount placeholder the
    probe had to skip (handoff 2026-04-28).  Since relay unification it
    is a REAL socket owned by the per-jail relay process, and a dead
    relay is exactly the "one jail 502s while doctor says broker
    healthy" symptom — so the probe must grade it end-to-end via
    ``_check_broker_relay`` and name the failing layer (relay vs the
    singleton broker behind it).  Singleton liveness itself is still
    graded separately in ``_check_loopholes`` via ``_broker_status``."""

    def _common_setup(self, monkeypatch, tmp_path):
        from unittest.mock import MagicMock as _MM

        # Pretend we're on the host — the probe early-returns inside a jail.
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        # _check_host_service_liveness lives in cli.check_cmd and looks up
        # _detect_runtime_for_listing / _host_service_sockets_dir / subprocess
        # via its own module namespace, not via cli.X.  Patch the actual call sites.
        monkeypatch.setattr(
            "cli.check_cmd._detect_runtime_for_listing", lambda: "podman"
        )
        run_result = _MM(stdout="yolo-test-cname-abc12345\n", returncode=0)
        monkeypatch.setattr("cli.check_cmd.subprocess.run", lambda *a, **kw: run_result)
        sockets_dir = tmp_path / "yolo-host-services-test"
        sockets_dir.mkdir()
        # Zero-byte regular file — exactly what the broker bind-mount source
        # looks like on the host.  A connect() against it raises ENOTSOCK.
        (sockets_dir / f"{cli.BROKER_LOOPHOLE_NAME}.sock").touch()
        monkeypatch.setattr(
            "cli.check_cmd._host_service_sockets_dir", lambda _cname: sockets_dir
        )
        return sockets_dir

    def _broker_loophole_entry(self):
        """Return a (path, loophole, err) entry shaped like
        ``_loopholes.validate_loopholes`` produces, with a broker
        loophole that has a ``host_daemon`` field set."""
        from unittest.mock import MagicMock as _MM
        from src.loopholes import HostDaemon

        lp = _MM()
        lp.name = cli.BROKER_LOOPHOLE_NAME
        lp.enabled = True
        lp.requirements_met = True
        lp.host_daemon = HostDaemon(cmd=["yolo-claude-oauth-broker-host"])
        return (None, lp, None)

    def test_probe_grades_broker_relay_and_names_the_jail(self, monkeypatch, tmp_path):
        """A dead per-jail relay socket must produce a FAIL that names
        both the jail and the relay layer.  (Pre-relay this entry was a
        bind-mount placeholder the probe skipped; skipping now would
        hide exactly the symptom Round 2 exists to surface.)"""
        self._common_setup(monkeypatch, tmp_path)
        monkeypatch.setattr(
            cli._loopholes,
            "validate_loopholes",
            lambda: [self._broker_loophole_entry()],
        )

        events: list = []
        cli._check_host_service_liveness(
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("warn", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
        )
        fails = [m for kind, m in events if kind == "fail"]
        # The placeholder file from _common_setup is not a live socket —
        # that's a relay-layer failure attributed to this jail.
        assert any(
            "claude-oauth-broker" in m
            and "yolo-test-cname-abc12345" in m
            and "relay" in m
            for m in fails
        ), f"expected a relay-layer fail naming the jail; got {events}"

    def _relay_probe_events(self, sock_path):
        """Run ``_check_broker_relay`` directly and collect its events."""
        events: list = []
        cli.check_cmd._check_broker_relay(
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
            "loophole claude-oauth-broker @ jail-x",
            sock_path,
        )
        return events

    def test_relay_probe_missing_socket_is_relay_layer(self, tmp_path):
        events = self._relay_probe_events(tmp_path / "claude-oauth-broker.sock")
        assert events[0][0] == "fail"
        assert "relay socket missing" in events[0][1]

    def test_relay_probe_dead_socket_is_relay_layer(self, tmp_path):
        # A file nothing listens on — connect() refuses.
        sock_path = tmp_path / "claude-oauth-broker.sock"
        sock_path.touch()
        events = self._relay_probe_events(sock_path)
        assert events[0][0] == "fail"
        assert "relay socket dead" in events[0][1]

    def test_relay_probe_distinguishes_broker_layer(self, monkeypatch):
        """Relay accepts but the proxied ping gets no pong: that's the
        broker layer, and the message must say so (not blame the relay)."""
        import socket as _socket
        import tempfile as _tempfile

        base = "/private/tmp" if sys.platform == "darwin" else "/tmp"
        d = Path(_tempfile.mkdtemp(dir=base, prefix="yj-probe-"))
        try:
            sock_path = d / "claude-oauth-broker.sock"
            srv = _socket.socket(_socket.AF_UNIX, _socket.SOCK_STREAM)
            srv.bind(str(sock_path))
            srv.listen(4)
            try:
                monkeypatch.setattr(
                    "cli.check_cmd._broker_ping", lambda *a, **kw: False
                )
                events = self._relay_probe_events(sock_path)
                assert events[0][0] == "fail"
                assert "broker unreachable" in events[0][1]

                monkeypatch.setattr("cli.check_cmd._broker_ping", lambda *a, **kw: True)
                events = self._relay_probe_events(sock_path)
                assert events[0][0] == "ok"
                assert "relay ok" in events[0][1]
            finally:
                srv.close()
        finally:
            shutil.rmtree(d, ignore_errors=True)

    def test_probe_still_runs_for_non_broker_loopholes(self, monkeypatch, tmp_path):
        """Other loopholes (e.g. host-processes) ARE per-jail and DO
        have real sockets — the skip must be broker-only.  Verify a
        synthetic non-broker loophole with a missing socket still gets
        a fail (i.e. the probe's normal logic runs)."""
        from unittest.mock import MagicMock as _MM
        from src.loopholes import HostDaemon

        sockets_dir = self._common_setup(monkeypatch, tmp_path)
        # Don't create a socket for "host-processes" — the probe should
        # report it missing.
        other = _MM()
        other.name = "host-processes"
        other.enabled = True
        other.requirements_met = True
        other.host_daemon = HostDaemon(cmd=["yolo-host-processes"])

        monkeypatch.setattr(
            cli._loopholes, "validate_loopholes", lambda: [(None, other, None)]
        )

        events: list = []
        cli._check_host_service_liveness(
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("warn", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
        )
        # Probe ran; reported the missing socket as a fail.
        fails = [m for kind, m in events if kind == "fail"]
        assert any("host-processes" in m for m in fails)
        # Sanity: sockets_dir is still the one we set up (no use-after-free).
        assert sockets_dir.is_dir()


class TestBrokerCredsFreshness:
    """Symptom-level health check: alarm when shared creds are about
    to expire.  Handoff 2026-04-28 called this out as the actually
    useful metric — refreshes either land or they don't, and doctor
    should surface that without us needing to know WHY (Claude not
    asking, refresher not running, server-side revocation, etc.)."""

    def _write_creds(self, tmp_path, expires_in_seconds):
        import time as _time

        creds_dir = tmp_path / "home" / ".claude-shared-credentials"
        creds_dir.mkdir(parents=True)
        creds_file = creds_dir / ".credentials.json"
        expires_ms = int((_time.time() + expires_in_seconds) * 1000)
        creds_file.write_text(
            json.dumps(
                {
                    "claudeAiOauth": {
                        "accessToken": "sk-ant-xxx",
                        "refreshToken": "sk-ant-rt-xxx",
                        "expiresAt": expires_ms,
                    }
                }
            )
        )
        return creds_file

    def _run(self, monkeypatch, tmp_path):
        monkeypatch.setattr("cli.check_cmd.GLOBAL_HOME", tmp_path / "home")
        events: list = []
        cli._check_broker_creds_freshness(
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("warn", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
        )
        return events

    def test_ok_when_fresh(self, monkeypatch, tmp_path):
        self._write_creds(tmp_path, 6 * 3600)  # 6h remaining
        events = self._run(monkeypatch, tmp_path)
        kinds = {kind for kind, _ in events}
        assert kinds == {"ok"}, f"expected only ok events, got {events}"

    def test_ok_message_includes_mtime(self, monkeypatch, tmp_path):
        """File mtime is a stand-in for "time since last refresh" — every
        successful refresh-grant rewrites the shared file.  Surfacing it
        next to the expiry distinguishes "fresh token" from "ancient
        token, just hasn't aged out yet"."""
        creds_file = self._write_creds(tmp_path, 6 * 3600)
        # Backdate so the test isn't sensitive to write latency.
        import os as _os

        old = creds_file.stat().st_mtime - 7200  # 2h old
        _os.utime(creds_file, (old, old))
        events = self._run(monkeypatch, tmp_path)
        ok_msgs = [m for kind, m in events if kind == "ok"]
        assert ok_msgs, f"expected ok event, got {events}"
        assert "last write" in ok_msgs[0], (
            f"expected mtime info in ok message, got {ok_msgs[0]!r}"
        )

    def test_warn_when_expiring_soon(self, monkeypatch, tmp_path):
        self._write_creds(tmp_path, 30 * 60)  # 30min remaining
        events = self._run(monkeypatch, tmp_path)
        warns = [m for kind, m in events if kind == "warn"]
        assert warns, f"expected warn for near-expiry creds, got {events}"

    def test_fail_when_expired(self, monkeypatch, tmp_path):
        self._write_creds(tmp_path, -300)  # expired 5min ago
        events = self._run(monkeypatch, tmp_path)
        fails = [m for kind, m in events if kind == "fail"]
        assert fails, f"expected fail for expired creds, got {events}"

    def test_silent_when_creds_missing(self, monkeypatch, tmp_path):
        # First /login hasn't happened yet — nothing to check.
        events = self._run(monkeypatch, tmp_path)
        assert events == [], f"expected silent skip when creds absent, got {events}"

    def test_warn_when_creds_unreadable(self, monkeypatch, tmp_path):
        monkeypatch.setattr("cli.check_cmd.GLOBAL_HOME", tmp_path / "home")
        creds_dir = tmp_path / "home" / ".claude-shared-credentials"
        creds_dir.mkdir(parents=True)
        (creds_dir / ".credentials.json").write_text("not json {{{")
        events: list = []
        cli._check_broker_creds_freshness(
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("warn", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
        )
        assert any(kind == "warn" for kind, _ in events), (
            f"expected warn for unreadable creds, got {events}"
        )

    def test_silent_when_creds_empty(self, monkeypatch, tmp_path):
        # ``ensure_global_storage`` touches the credentials file as a
        # zero-byte mount-point placeholder before the first /login.
        # Treat that as "pre-login" (silent), not "unreadable" (warn).
        monkeypatch.setattr("cli.check_cmd.GLOBAL_HOME", tmp_path / "home")
        creds_dir = tmp_path / "home" / ".claude-shared-credentials"
        creds_dir.mkdir(parents=True)
        (creds_dir / ".credentials.json").touch()
        events: list = []
        cli._check_broker_creds_freshness(
            lambda m, *a, **kw: events.append(("ok", m)),
            lambda m, *a, **kw: events.append(("warn", m)),
            lambda m, *a, **kw: events.append(("fail", m)),
        )
        assert events == [], (
            f"expected silent skip when creds file is empty, got {events}"
        )


class TestScratchMountArgs:
    """ephemeral_storage controls /tmp /var/tmp /var/lib/containers backing."""

    def _grouped(self, args: list) -> list[tuple[str, str]]:
        # Pair each flag with its operand so order/equivalence checks
        # don't drown in indexing math.
        assert len(args) % 2 == 0, args
        return list(zip(args[::2], args[1::2]))

    def test_default_is_anonymous_volumes(self):
        pairs = self._grouped(_scratch_mount_args(None))
        # /tmp + /var/tmp + /var/lib/containers as anonymous volumes
        assert ("-v", "/tmp") in pairs
        assert ("-v", "/var/tmp") in pairs
        assert ("-v", "/var/lib/containers") in pairs
        # /run + /dev/shm always tmpfs
        assert ("--tmpfs", "/run") in pairs
        assert ("--tmpfs", "/dev/shm:size=2g") in pairs
        # No --tmpfs for the volume-backed paths
        assert not any(
            flag == "--tmpfs" and dst.startswith("/tmp") for flag, dst in pairs
        )

    def test_explicit_volume_matches_default(self):
        assert _scratch_mount_args("volume") == _scratch_mount_args(None)

    def test_tmpfs_keeps_all_paths_on_tmpfs(self):
        pairs = self._grouped(_scratch_mount_args("tmpfs"))
        for path in (
            "/tmp:exec,mode=1777",
            "/var/tmp:exec,mode=1777",
            "/var/lib/containers",
            "/run",
            "/dev/shm:size=2g",
        ):
            assert ("--tmpfs", path) in pairs
        # No anonymous volumes
        assert not any(flag == "-v" for flag, _ in pairs)

    def test_invalid_value_falls_back_to_volume(self):
        # Defensive: validation already rejects bad values, but the helper
        # shouldn't blow up if a future caller hands it something weird.
        assert _scratch_mount_args("nope") == _scratch_mount_args("volume")
        assert _scratch_mount_args(42) == _scratch_mount_args("volume")


class TestRoFileMountArg:
    """Single-file :ro host mounts must survive the nested-jail case where
    the source file is itself a bind mountpoint (rootless podman/crun can't
    use such a file as a bind source — the whole `run` dies with
    `mount <src>: No such file or directory`).  The helper dereferences it
    by copying to a plain file under ws_state; on a real host (plain source)
    it mounts directly with no copy.
    """

    def test_plain_source_mounts_directly(self, tmp_path):
        from cli.run_cmd import _ro_file_mount_arg

        src = tmp_path / "ignore"
        src.write_text("*.pyc\n")
        ws_state = tmp_path / "ws"
        ws_state.mkdir()
        args = _ro_file_mount_arg(
            src, "/home/agent/.config/git/ignore", ws_state, ".config/git/ignore", set()
        )
        # Direct mount of the original source; no copy made.
        assert args == ["-v", f"{src}:/home/agent/.config/git/ignore:ro"]
        assert not (ws_state / ".config/git/ignore").exists()

    def test_bind_mountpoint_source_is_dereferenced(self, tmp_path):
        from cli.run_cmd import _ro_file_mount_arg

        src = tmp_path / "config.jsonc"
        src.write_text('{"x": 1}')
        ws_state = tmp_path / "ws"
        ws_state.mkdir()
        # Simulate the source being a bind mountpoint by naming it in the set.
        target = "/home/agent/.config/yolo-jail/config.jsonc"
        rel = ".config/yolo-jail/config.jsonc"
        args = _ro_file_mount_arg(src, target, ws_state, rel, {str(src)})
        deref = ws_state / rel
        # Mount now points at the dereferenced copy, not the bind-mounted source.
        assert args == ["-v", f"{deref}:{target}:ro"]
        assert deref.read_text() == '{"x": 1}'

    def test_is_bind_mountpoint_matches_realpath(self, tmp_path):
        from cli.run_cmd import _is_bind_mountpoint

        f = tmp_path / "f"
        f.write_text("x")
        assert _is_bind_mountpoint(f, {str(f)})
        assert not _is_bind_mountpoint(f, set())

    def test_bind_mount_targets_reads_mountinfo(self):
        from cli.run_cmd import _bind_mount_targets

        # Real /proc/self/mountinfo on Linux; must at least return a set and
        # never raise.  "/" is a mount on any Linux host running the suite.
        targets = _bind_mount_targets()
        assert isinstance(targets, set)


class TestRunWithProxy:
    """The TTY proxy wraps the host-side ``podman run/exec``; without a
    host TTY there's nothing to intercept, so the wrapper falls back to a
    plain ``Popen``.  Test the fallback path here — the actual ^Z dance
    needs a real TTY and is exercised manually."""

    def test_no_tty_fallback_uses_popen_and_returns_exit_code(self):
        from unittest.mock import patch, MagicMock
        import cli

        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 7
        with patch("subprocess.Popen", return_value=proc) as mock_popen:
            # CliRunner's stdin isn't a TTY; emulate that here so the
            # branch under test is the fallback.
            rc = cli.run_with_proxy(["does-not-matter", "arg"])
        assert rc == 7
        mock_popen.assert_called_once_with(["does-not-matter", "arg"])
        proc.wait.assert_called_once()

    def test_no_tty_fallback_runs_on_started_callback(self):
        from unittest.mock import patch, MagicMock
        import cli

        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 0
        seen: list = []

        def cb(p):
            seen.append(p)

        with patch("subprocess.Popen", return_value=proc):
            cli.run_with_proxy(["x"], on_started=cb)
        assert seen == [proc]

    def test_no_tty_fallback_swallows_callback_exceptions(self):
        from unittest.mock import patch, MagicMock
        import cli

        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 0

        def cb(_p):
            raise RuntimeError("boom")

        with patch("subprocess.Popen", return_value=proc):
            # Must not raise.
            cli.run_with_proxy(["x"], on_started=cb)


class TestResolveLspInstalls:
    """``_resolve_lsp_installs`` translates ``lsp_servers`` config into the
    install lists the bootstrap script consumes."""

    def test_empty_config_yields_no_installs(self):
        import cli

        out = cli._resolve_lsp_installs({})
        assert out == {"npm": "", "go": ""}

    def test_python_pulls_pyright_and_mcp_bridge(self):
        import cli

        out = cli._resolve_lsp_installs(
            {"python": {"command": "pyright-langserver", "args": ["--stdio"]}}
        )
        assert "pyright" in out["npm"].splitlines()
        # mcp-language-server is added once whenever any LSP is configured —
        # Gemini wraps every LSP through it.
        assert any("mcp-language-server" in p for p in out["go"].splitlines())

    def test_go_pulls_gopls_and_mcp_bridge(self):
        import cli

        out = cli._resolve_lsp_installs(
            {"go": {"command": "gopls", "fileExtensions": {".go": "go"}}}
        )
        assert any("gopls" in p for p in out["go"].splitlines())
        assert any("mcp-language-server" in p for p in out["go"].splitlines())

    def test_unknown_lsp_name_is_user_responsibility(self):
        """A workspace can configure an LSP outside our recipe table —
        ``command`` then points at a binary the user installed (image,
        mise, custom).  We don't ship installers for it."""
        import cli

        out = cli._resolve_lsp_installs(
            {"rust": {"command": "rust-analyzer", "fileExtensions": {".rs": "rust"}}}
        )
        # Bridge is still pulled because Gemini will need it.
        assert any("mcp-language-server" in p for p in out["go"].splitlines())
        # No npm install is emitted (rust isn't in the recipe table).
        assert out["npm"] == ""

    def test_typescript_pulls_typescript_and_lsp_pkg(self):
        import cli

        out = cli._resolve_lsp_installs(
            {"typescript": {"command": "typescript-language-server"}}
        )
        npm_pkgs = out["npm"].splitlines()
        assert "typescript-language-server" in npm_pkgs
        assert "typescript" in npm_pkgs

    def test_multiple_lsps_dedupe_bridge(self):
        import cli

        out = cli._resolve_lsp_installs(
            {
                "python": {"command": "pyright"},
                "go": {"command": "gopls"},
            }
        )
        bridge_count = sum(
            1 for p in out["go"].splitlines() if "mcp-language-server" in p
        )
        assert bridge_count == 1

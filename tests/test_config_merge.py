import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))
import cli
from src import loopholes
from cli import (
    ConfigError,
    _check_config_changes,
    _load_jsonc_file,
    _load_jsonc_with_includes,
    _validate_config,
    load_config,
    merge_config,
)


def test_merge_config_lists_dedup_and_override_scalars():
    user = {
        "packages": ["sqlite", "postgresql"],
        "mounts": ["~/code/shared:/ctx/shared"],
        "network": {"mode": "bridge"},
        "security": {"blocked_tools": ["wget", {"name": "grep"}]},
    }
    workspace = {
        "packages": ["postgresql", "redis"],
        "mounts": ["~/code/extra:/ctx/extra"],
        "network": {"mode": "host"},
        "security": {"blocked_tools": [{"name": "grep"}, "curl"]},
    }

    merged = merge_config(user, workspace)

    assert merged["packages"] == ["sqlite", "postgresql", "redis"]
    assert merged["mounts"] == ["~/code/shared:/ctx/shared", "~/code/extra:/ctx/extra"]
    assert merged["network"]["mode"] == "host"
    assert merged["security"]["blocked_tools"] == ["wget", {"name": "grep"}, "curl"]


def test_merge_config_agents_overrides_not_unions():
    """Unlike other list fields, ``agents`` from the workspace REPLACES the
    user default so a workspace can narrow the selected set."""
    user = {"agents": ["claude", "gemini"]}
    workspace = {"agents": ["claude"]}
    merged = merge_config(user, workspace)
    assert merged["agents"] == ["claude"]


def test_merge_config_agents_user_default_used_when_workspace_omits():
    """A workspace with no ``agents`` inherits the user-level list verbatim."""
    user = {"agents": ["opencode", "pi"]}
    workspace = {"packages": ["ripgrep"]}
    merged = merge_config(user, workspace)
    assert merged["agents"] == ["opencode", "pi"]


def test_selected_agents_default_is_claude():
    from cli.config import selected_agents

    assert selected_agents({}) == ["claude"]


def test_selected_agents_filters_and_dedups():
    from cli.config import selected_agents

    assert selected_agents({"agents": ["gemini", "gemini", "bogus", "pi"]}) == [
        "gemini",
        "pi",
    ]


def test_selected_agents_honors_empty_list():
    from cli.config import selected_agents

    assert selected_agents({"agents": []}) == []


def test_validate_config_accepts_known_agents():
    errors, _ = _validate_config(
        {"agents": ["claude", "copilot", "gemini", "opencode", "pi", "codex"]},
        workspace=Path.cwd(),
    )
    assert not errors


def test_validate_config_rejects_unknown_agent():
    errors, _ = _validate_config({"agents": ["nope"]}, workspace=Path.cwd())
    assert any("unknown agent 'nope'" in e for e in errors)


def test_validate_config_accepts_macos_user_runtime():
    errors, _ = _validate_config({"runtime": "macos-user"}, workspace=Path.cwd())
    assert not errors


def test_validate_config_rejects_unknown_runtime():
    errors, _ = _validate_config({"runtime": "bogus"}, workspace=Path.cwd())
    assert any("expected 'podman', 'container', or 'macos-user'" in e for e in errors)


def test_validate_config_accepts_macos_log_modes():
    for mode in ("off", "user", "full"):
        errors, _ = _validate_config({"macos_log": mode}, workspace=Path.cwd())
        assert not errors, mode


def test_validate_config_rejects_bad_macos_log():
    errors, _ = _validate_config({"macos_log": "loud"}, workspace=Path.cwd())
    assert any("config.macos_log" in e for e in errors)


def test_validate_config_accepts_neutral_macos_shared_root():
    for root in ("/Users/Shared/yolo", "/opt/yolo", "/Volumes/ext/work"):
        errors, _ = _validate_config({"macos_shared_root": root}, workspace=Path.cwd())
        assert not errors, root


def test_validate_config_rejects_home_macos_shared_root():
    # A path inside a user home defeats the neutral-ground boundary.
    for root in ("/Users/matt", "/Users/matt/private/share"):
        errors, _ = _validate_config({"macos_shared_root": root}, workspace=Path.cwd())
        assert any("config.macos_shared_root" in e for e in errors), root


def test_validate_config_rejects_relative_macos_shared_root():
    errors, _ = _validate_config(
        {"macos_shared_root": "rel/path"}, workspace=Path.cwd()
    )
    assert any("config.macos_shared_root" in e for e in errors)


def test_validate_config_rejects_non_list_agents():
    errors, _ = _validate_config({"agents": "claude"}, workspace=Path.cwd())
    assert any("config.agents: expected a list" in e for e in errors)


def test_validate_config_warns_on_empty_agents():
    errors, warnings = _validate_config({"agents": []}, workspace=Path.cwd())
    assert not errors
    assert any("no coding agents" in w for w in warnings)


def test_merge_config_merges_mcp_servers_dicts():
    user = {
        "mcp_servers": {
            "foo": {"command": "/bin/foo", "args": ["--a"]},
            "bar": {"command": "/bin/bar", "args": []},
        }
    }
    workspace = {
        "mcp_servers": {
            "bar": {"command": "/workspace/bar", "args": ["--override"]},
            "baz": {"command": "/workspace/baz", "args": []},
        }
    }

    merged = merge_config(user, workspace)

    assert merged["mcp_servers"]["foo"]["command"] == "/bin/foo"
    assert merged["mcp_servers"]["bar"]["command"] == "/workspace/bar"
    assert merged["mcp_servers"]["baz"]["command"] == "/workspace/baz"


def test_merge_config_mcp_servers_can_disable_inherited_server():
    user = {
        "mcp_servers": {
            "foo": {"command": "/bin/foo", "args": ["--a"]},
        }
    }
    workspace = {
        "mcp_servers": {
            "foo": None,
        }
    }

    merged = merge_config(user, workspace)

    assert merged["mcp_servers"]["foo"] is None


def test_load_jsonc_file_strict_raises_for_invalid_json(tmp_path):
    config_path = tmp_path / "yolo-jail.jsonc"
    config_path.write_text("{invalid json")

    with pytest.raises(ConfigError):
        _load_jsonc_file(config_path, "yolo-jail.jsonc", strict=True)


def test_validate_config_rejects_unknown_top_level_keys():
    errors, warnings = _validate_config({"mcp_server": {}}, workspace=Path.cwd())

    assert warnings == []
    assert "config.mcp_server: unknown key" in errors


def test_validate_config_requires_file_extensions_for_lsp_servers():
    errors, warnings = _validate_config(
        {
            "lsp_servers": {
                "python": {
                    "command": "/custom/pyright",
                    "args": ["--stdio"],
                }
            }
        },
        workspace=Path.cwd(),
    )

    assert warnings == []
    assert "config.lsp_servers.python.fileExtensions: expected an object" in errors


def test_validate_config_accepts_valid_env_sources():
    errors, warnings = _validate_config(
        {
            "env_sources": [
                {"DATABASE_URL": "postgres://localhost/dev", "DEBUG": "1"},
                "~/.config/yolo-jail/secrets.env",
            ]
        },
        workspace=Path.cwd(),
    )
    assert errors == []
    assert warnings == []


def test_validate_config_rejects_legacy_env_key():
    errors, _ = _validate_config({"env": {"FOO": "bar"}}, workspace=Path.cwd())
    assert any(
        "config.env" in e and "env_sources" in e and "removed" in e for e in errors
    )


def test_validate_config_rejects_non_list_env_sources():
    errors, _ = _validate_config({"env_sources": {"FOO": "bar"}}, workspace=Path.cwd())
    assert any("env_sources" in e and "list" in e for e in errors)


def test_validate_config_rejects_non_string_env_sources_value():
    errors, _ = _validate_config({"env_sources": [{"DEBUG": 1}]}, workspace=Path.cwd())
    assert any("env_sources[0].DEBUG" in e and "string value" in e for e in errors)


def test_validate_config_rejects_invalid_env_sources_var_name():
    errors, _ = _validate_config(
        {"env_sources": [{"123BAD": "val"}]}, workspace=Path.cwd()
    )
    assert any("invalid variable name" in e for e in errors)


def test_validate_config_accepts_valid_ephemeral_storage():
    for value in ("volume", "tmpfs"):
        errors, warnings = _validate_config(
            {"ephemeral_storage": value}, workspace=Path.cwd()
        )
        assert errors == [], (value, errors)
        assert warnings == []


def test_validate_config_rejects_invalid_ephemeral_storage():
    errors, _ = _validate_config({"ephemeral_storage": "ramdisk"}, workspace=Path.cwd())
    assert any("ephemeral_storage" in e for e in errors)
    errors, _ = _validate_config({"ephemeral_storage": True}, workspace=Path.cwd())
    assert any("ephemeral_storage" in e for e in errors)


def test_merge_config_env_sources_concatenates_user_then_workspace():
    user = {"env_sources": [{"A": "1", "B": "2"}]}
    workspace = {"env_sources": [{"B": "override", "C": "3"}]}
    merged = merge_config(user, workspace)
    # Lists concatenate: user entries first, workspace entries appended.
    # Final precedence is resolved at load time (later wins), not at merge.
    assert merged["env_sources"] == [
        {"A": "1", "B": "2"},
        {"B": "override", "C": "3"},
    ]


def test_same_file_preset_null_conflict_is_reported():
    conflicts = cli._check_preset_null_conflicts(
        {
            "mcp_presets": ["chrome-devtools", "sequential-thinking"],
            "mcp_servers": {"chrome-devtools": None},
        },
        "yolo-jail.jsonc",
    )

    assert conflicts == [
        "yolo-jail.jsonc: preset 'chrome-devtools' is enabled in mcp_presets but "
        "null-removed in mcp_servers within the same config file"
    ]


def test_cross_hierarchy_preset_null_override_is_allowed():
    user = {"mcp_presets": ["chrome-devtools", "sequential-thinking"]}
    workspace = {"mcp_servers": {"chrome-devtools": None}}

    merged = merge_config(user, workspace)

    assert cli._check_preset_null_conflicts(user, "user") == []
    assert cli._check_preset_null_conflicts(workspace, "workspace") == []
    assert cli._effective_mcp_server_names(
        merged.get("mcp_servers"), merged.get("mcp_presets")
    ) == ["sequential-thinking"]


def test_seed_agent_dir_copies_auth_files(tmp_path):
    """_seed_agent_dir copies files from GLOBAL_HOME agent dir into per-workspace overlay."""
    src = tmp_path / "shared-home" / ".gemini"
    src.mkdir(parents=True)
    (src / "hosts.json").write_text('{"auth": true}')
    (src / "settings.json").write_text('{"theme": "dark"}')

    dst = tmp_path / "workspace" / ".yolo" / "home" / "gemini"
    dst.mkdir(parents=True)

    cli._seed_agent_dir(src, dst)

    assert (dst / "hosts.json").read_text() == '{"auth": true}'
    assert (dst / "settings.json").read_text() == '{"theme": "dark"}'

    # Second call should not overwrite
    (dst / "hosts.json").write_text("modified")
    cli._seed_agent_dir(src, dst)
    assert (dst / "hosts.json").read_text() == "modified"


class TestIncludeIfFound:
    def test_no_include_key_returns_raw(self, tmp_path):
        base = tmp_path / "base.jsonc"
        base.write_text('{"packages": ["just"]}')
        assert _load_jsonc_with_includes(base, "base") == {"packages": ["just"]}

    def test_missing_include_silently_skipped(self, tmp_path):
        base = tmp_path / "base.jsonc"
        base.write_text(
            '{"packages": ["just"], "include_if_found": ["does-not-exist.jsonc"]}'
        )
        merged = _load_jsonc_with_includes(base, "base")
        assert merged == {"packages": ["just"]}
        assert "include_if_found" not in merged

    def test_existing_include_is_merged_and_overrides_win(self, tmp_path):
        base = tmp_path / "base.jsonc"
        base.write_text(
            '{"packages": ["just"], "network": {"mode": "bridge"}, '
            '"include_if_found": ["overrides.jsonc"]}'
        )
        (tmp_path / "overrides.jsonc").write_text(
            '{"packages": ["htop"], "network": {"mode": "host"}}'
        )
        merged = _load_jsonc_with_includes(base, "base")
        assert merged["network"]["mode"] == "host"
        assert merged["packages"] == ["just", "htop"]
        assert "include_if_found" not in merged

    def test_include_resolves_relative_to_including_file(self, tmp_path):
        sub = tmp_path / "sub"
        sub.mkdir()
        base = sub / "base.jsonc"
        base.write_text('{"include_if_found": ["sibling.jsonc"]}')
        (sub / "sibling.jsonc").write_text('{"packages": ["fd"]}')
        assert _load_jsonc_with_includes(base, "base") == {"packages": ["fd"]}

    def test_recursive_includes(self, tmp_path):
        a = tmp_path / "a.jsonc"
        a.write_text('{"packages": ["a"], "include_if_found": ["b.jsonc"]}')
        b = tmp_path / "b.jsonc"
        b.write_text('{"packages": ["b"], "include_if_found": ["c.jsonc"]}')
        c = tmp_path / "c.jsonc"
        c.write_text('{"packages": ["c"]}')
        merged = _load_jsonc_with_includes(a, "a")
        assert merged["packages"] == ["a", "b", "c"]

    def test_cycle_is_broken(self, tmp_path):
        a = tmp_path / "a.jsonc"
        a.write_text('{"packages": ["a"], "include_if_found": ["b.jsonc"]}')
        b = tmp_path / "b.jsonc"
        b.write_text('{"packages": ["b"], "include_if_found": ["a.jsonc"]}')
        merged = _load_jsonc_with_includes(a, "a")
        assert "a" in merged["packages"]
        assert "b" in merged["packages"]

    def test_absolute_path_rejected_strict(self, tmp_path):
        base = tmp_path / "base.jsonc"
        base.write_text('{"include_if_found": ["/etc/passwd"]}')
        with pytest.raises(ConfigError):
            _load_jsonc_with_includes(base, "base", strict=True)

    def test_tilde_path_rejected_strict(self, tmp_path):
        base = tmp_path / "base.jsonc"
        base.write_text('{"include_if_found": ["~/secret.jsonc"]}')
        with pytest.raises(ConfigError):
            _load_jsonc_with_includes(base, "base", strict=True)

    def test_non_list_include_rejected_strict(self, tmp_path):
        base = tmp_path / "base.jsonc"
        base.write_text('{"include_if_found": "single.jsonc"}')
        with pytest.raises(ConfigError):
            _load_jsonc_with_includes(base, "base", strict=True)

    def test_validate_accepts_relative_includes(self):
        errors, warnings = _validate_config(
            {"include_if_found": ["overrides.jsonc", "secret/local.jsonc"]},
            workspace=Path.cwd(),
        )
        assert errors == []
        assert warnings == []

    def test_validate_rejects_absolute_include(self):
        errors, _ = _validate_config(
            {"include_if_found": ["/etc/passwd"]}, workspace=Path.cwd()
        )
        assert any("must be a relative path" in e for e in errors)

    def test_validate_rejects_non_list_include(self):
        errors, _ = _validate_config(
            {"include_if_found": "single.jsonc"}, workspace=Path.cwd()
        )
        assert any(
            "include_if_found" in e and "list of relative path strings" in e
            for e in errors
        )


class TestWorkspaceLocalConfig:
    @pytest.fixture(autouse=True)
    def _no_user_config(self, tmp_path, monkeypatch):
        monkeypatch.setattr(
            "cli.config.USER_CONFIG_PATH", tmp_path / "no-user-config.jsonc"
        )

    def test_local_file_auto_merged_and_wins(self, tmp_path):
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"packages": ["just"], "network": {"mode": "bridge"}}'
        )
        (tmp_path / "yolo-jail.local.jsonc").write_text(
            '{"packages": ["htop"], "network": {"mode": "host"}}'
        )
        merged = load_config(tmp_path)
        assert merged["packages"] == ["just", "htop"]
        assert merged["network"]["mode"] == "host"

    def test_local_file_absent_is_noop(self, tmp_path):
        (tmp_path / "yolo-jail.jsonc").write_text('{"packages": ["just"]}')
        assert load_config(tmp_path) == {"packages": ["just"]}

    def test_local_file_alone_works_without_main_config(self, tmp_path):
        (tmp_path / "yolo-jail.local.jsonc").write_text('{"packages": ["fd"]}')
        assert load_config(tmp_path) == {"packages": ["fd"]}

    def test_explicit_include_of_local_does_not_merge_twice(self, tmp_path):
        (tmp_path / "yolo-jail.jsonc").write_text(
            '{"packages": ["just"], "include_if_found": ["yolo-jail.local.jsonc"]}'
        )
        (tmp_path / "yolo-jail.local.jsonc").write_text('{"packages": ["htop"]}')
        merged = load_config(tmp_path)
        assert merged["packages"] == ["just", "htop"]

    def test_local_file_may_declare_its_own_includes(self, tmp_path):
        (tmp_path / "yolo-jail.jsonc").write_text('{"packages": ["just"]}')
        (tmp_path / "yolo-jail.local.jsonc").write_text(
            '{"packages": ["htop"], "include_if_found": ["extra.jsonc"]}'
        )
        (tmp_path / "extra.jsonc").write_text('{"packages": ["fd"]}')
        merged = load_config(tmp_path)
        assert merged["packages"] == ["just", "htop", "fd"]

    def test_local_file_overrides_user_config(self, tmp_path, monkeypatch):
        user = tmp_path / "user-config.jsonc"
        user.write_text('{"journal": "off"}')
        monkeypatch.setattr("cli.config.USER_CONFIG_PATH", user)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        (tmp_path / "yolo-jail.local.jsonc").write_text('{"journal": "full"}')
        assert load_config(tmp_path)["journal"] == "full"

    def test_malformed_local_file_raises_in_strict_mode(self, tmp_path):
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        (tmp_path / "yolo-jail.local.jsonc").write_text("{not valid json")
        with pytest.raises(ConfigError):
            load_config(tmp_path, strict=True)


class TestMcpRequiresEnv:
    def test_validate_accepts_requires_env(self):
        errors, warnings = _validate_config(
            {
                "mcp_servers": {
                    "tavily": {
                        "command": "npx",
                        "args": ["-y", "tavily-mcp"],
                        "requires_env": ["TAVILY_API_KEY"],
                    }
                }
            },
            workspace=Path.cwd(),
        )
        assert errors == []
        assert warnings == []

    def test_validate_rejects_non_list_requires_env(self):
        errors, _ = _validate_config(
            {"mcp_servers": {"x": {"command": "cat", "requires_env": "KEY"}}},
            workspace=Path.cwd(),
        )
        assert any("requires_env" in e and "list" in e for e in errors)

    def test_validate_rejects_invalid_var_name(self):
        errors, _ = _validate_config(
            {"mcp_servers": {"x": {"command": "cat", "requires_env": ["123BAD"]}}},
            workspace=Path.cwd(),
        )
        assert any("requires_env[0]" in e and "invalid env var" in e for e in errors)

    def test_filter_drops_unsatisfied_server(self):
        servers = {
            "tavily": {"command": "npx", "requires_env": ["TAVILY_API_KEY"]},
            "always": {"command": "cat"},
            "removed-preset": None,
        }
        filtered = cli._filter_mcp_servers_by_env(servers, {})
        assert "tavily" not in filtered
        assert "always" in filtered
        # Null preset-removals pass through untouched.
        assert "removed-preset" in filtered and filtered["removed-preset"] is None

    def test_filter_keeps_satisfied_server(self):
        servers = {"tavily": {"command": "npx", "requires_env": ["TAVILY_API_KEY"]}}
        filtered = cli._filter_mcp_servers_by_env(servers, {"TAVILY_API_KEY": "tvly-x"})
        assert "tavily" in filtered

    def test_filter_empty_value_counts_as_unset(self):
        servers = {"tavily": {"command": "npx", "requires_env": ["TAVILY_API_KEY"]}}
        filtered = cli._filter_mcp_servers_by_env(servers, {"TAVILY_API_KEY": ""})
        assert "tavily" not in filtered

    def test_filter_none_passthrough(self):
        assert cli._filter_mcp_servers_by_env(None, {}) is None


class TestAgentsMdExtra:
    def test_validate_accepts_string(self):
        errors, warnings = _validate_config(
            {"agents_md_extra": "## My MCPs\n\nUse cerebras-mcp for X."},
            workspace=Path.cwd(),
        )
        assert errors == []
        assert warnings == []

    def test_validate_rejects_non_string(self):
        errors, _ = _validate_config(
            {"agents_md_extra": ["a", "b"]}, workspace=Path.cwd()
        )
        assert any("agents_md_extra" in e and "string of markdown" in e for e in errors)


class TestPerSidePaths:
    """per_side_paths — workspace sub-paths that get per-side (host vs
    jail) venv-shadow backing."""

    def test_validate_accepts_relative_paths(self):
        errors, warnings = _validate_config(
            {"per_side_paths": [".venv-alt", ".cargo", "data/models"]},
            workspace=Path.cwd(),
        )
        assert errors == []
        assert warnings == []

    def test_validate_rejects_non_list(self):
        errors, _ = _validate_config({"per_side_paths": ".venv"}, workspace=Path.cwd())
        assert any("per_side_paths" in e and "list" in e for e in errors)

    def test_validate_rejects_non_string_entry(self):
        errors, _ = _validate_config({"per_side_paths": [42]}, workspace=Path.cwd())
        assert any("per_side_paths[0]" in e and "string" in e for e in errors)

    def test_validate_rejects_absolute(self):
        errors, _ = _validate_config(
            {"per_side_paths": ["/abs/path"]}, workspace=Path.cwd()
        )
        assert any("must be a relative path" in e for e in errors)

    def test_validate_rejects_dotdot(self):
        errors, _ = _validate_config(
            {"per_side_paths": ["a/../b"]}, workspace=Path.cwd()
        )
        assert any("'..'" in e for e in errors)

    def test_validate_rejects_dot_and_empty(self):
        errors, _ = _validate_config(
            {"per_side_paths": [".", ""]}, workspace=Path.cwd()
        )
        assert sum("must name a workspace sub-path" in e for e in errors) == 2

    def test_merge_concatenates_and_dedups(self):
        merged = merge_config(
            {"per_side_paths": [".cargo", ".venv-x"]},
            {"per_side_paths": [".venv-x", "models"]},
        )
        assert merged["per_side_paths"] == [".cargo", ".venv-x", "models"]


class TestConfigSnapshot:
    def test_first_run_saves_snapshot(self, tmp_path):
        workspace = tmp_path / "project"
        workspace.mkdir()
        config = {"packages": ["strace"]}
        assert _check_config_changes(workspace, config) is True
        snapshot = workspace / ".yolo" / "config-snapshot.json"
        assert snapshot.exists()
        assert json.loads(snapshot.read_text()) == config

    def test_unchanged_config_passes(self, tmp_path):
        workspace = tmp_path / "project"
        workspace.mkdir()
        config = {"packages": ["strace"]}
        _check_config_changes(workspace, config)
        assert _check_config_changes(workspace, config) is True

    def test_changed_config_rejects_on_no(self, tmp_path, monkeypatch):
        workspace = tmp_path / "project"
        workspace.mkdir()
        config = {"packages": ["strace"]}
        _check_config_changes(workspace, config)

        # Simulate non-interactive (no tty) — should auto-accept
        monkeypatch.setattr("sys.stdin", open("/dev/null"))
        new_config = {"packages": ["strace", "htop"]}
        assert _check_config_changes(workspace, new_config) is True

    def test_changed_config_interactive_yes(self, tmp_path, monkeypatch):
        workspace = tmp_path / "project"
        workspace.mkdir()
        config = {"packages": ["strace"]}
        _check_config_changes(workspace, config)

        import io

        monkeypatch.setattr("sys.stdin", io.StringIO("y\n"))
        # Make isatty return True on our StringIO
        monkeypatch.setattr("sys.stdin.isatty", lambda: True)
        new_config = {"packages": ["strace", "htop"]}
        assert _check_config_changes(workspace, new_config) is True
        # Snapshot should be updated
        snapshot = json.loads(
            (workspace / ".yolo" / "config-snapshot.json").read_text()
        )
        assert snapshot == new_config

    def test_changed_config_interactive_no(self, tmp_path, monkeypatch):
        workspace = tmp_path / "project"
        workspace.mkdir()
        config = {"packages": ["strace"]}
        _check_config_changes(workspace, config)

        import io

        monkeypatch.setattr("sys.stdin", io.StringIO("n\n"))
        monkeypatch.setattr("sys.stdin.isatty", lambda: True)
        new_config = {"packages": ["strace", "htop"]}
        assert _check_config_changes(workspace, new_config) is False
        # Snapshot should NOT be updated
        snapshot = json.loads(
            (workspace / ".yolo" / "config-snapshot.json").read_text()
        )
        assert snapshot == config


class TestLoopholeOverrides:
    """Workspace ``loopholes:`` entries whose name matches an existing
    (bundled or user-dir) loophole are overrides — the runtime merge in
    ``loopholes._apply_workspace_overrides`` honors exactly
    {enabled, env, jail_env} for them — so the validator must not demand
    the inline-service shape (``command`` etc.) for those names."""

    @pytest.fixture
    def known_loophole(self, tmp_path, monkeypatch):
        """A user-dir loophole named 'my-hole' (daemon-backed, so 'env'
        overrides are applicable) plus a daemonless 'no-daemon-hole';
        bundled dir pointed at nothing so the known set is fully
        test-controlled."""
        mods_root = tmp_path / "loopholes"
        mod = mods_root / "my-hole"
        mod.mkdir(parents=True)
        (mod / "manifest.jsonc").write_text(
            json.dumps(
                {
                    "name": "my-hole",
                    "description": "test loophole",
                    "transport": "unix-socket",
                    "lifecycle": "spawned",
                    "host_daemon": {"cmd": ["true"]},
                }
            )
        )
        plain = mods_root / "no-daemon-hole"
        plain.mkdir()
        (plain / "manifest.jsonc").write_text(
            json.dumps({"name": "no-daemon-hole", "description": "no daemon"})
        )
        monkeypatch.setattr(loopholes, "user_loopholes_dir", lambda: mods_root)
        monkeypatch.setattr(
            loopholes, "bundled_loopholes_dir", lambda: tmp_path / "nobundled"
        )
        return "my-hole"

    def test_bundled_disable_validates_clean(self, tmp_path, monkeypatch):
        # The reproduced bug: {"enabled": false} on the bundled 'audio'
        # loophole failed 'command: required' + 'enabled: unknown key'.
        # Real bundled dir; user dir isolated from host state.
        monkeypatch.setattr(loopholes, "user_loopholes_dir", lambda: tmp_path / "none")
        errors, warnings = _validate_config(
            {"loopholes": {"audio": {"enabled": False}}}, workspace=Path.cwd()
        )
        assert errors == []
        assert warnings == []

    def test_override_full_shape_validates_clean(self, known_loophole):
        errors, warnings = _validate_config(
            {
                "loopholes": {
                    known_loophole: {
                        "enabled": False,
                        "env": {"FOO": "bar"},
                        "jail_env": {"BAZ": "qux"},
                    }
                }
            },
            workspace=Path.cwd(),
        )
        assert errors == []
        assert warnings == []

    def test_override_enabled_must_be_bool(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {known_loophole: {"enabled": "yes"}}},
            workspace=Path.cwd(),
        )
        assert any(".enabled" in e and "boolean" in e for e in errors)

    def test_override_command_is_not_overridable(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {known_loophole: {"command": ["echo", "hi"]}}},
            workspace=Path.cwd(),
        )
        assert any(".command" in e and "not overridable" in e for e in errors)
        # Rejected with the dedicated message, not as an unknown key.
        assert not any("command: unknown key" in e for e in errors)

    def test_override_unknown_key_reported(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {known_loophole: {"enabled": True, "transport": "none"}}},
            workspace=Path.cwd(),
        )
        assert any(".transport: unknown key" in e for e in errors)

    def test_override_env_type_validation(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {known_loophole: {"env": {"FOO": 1}}}},
            workspace=Path.cwd(),
        )
        assert any(".env" in e and "must be strings" in e for e in errors)

        errors, _ = _validate_config(
            {"loopholes": {known_loophole: {"jail_env": "PATH=/x"}}},
            workspace=Path.cwd(),
        )
        assert any(".jail_env" in e and "expected an object" in e for e in errors)

    def test_unknown_name_still_requires_command(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {"other-svc": {}}}, workspace=Path.cwd()
        )
        assert any("other-svc.command: required" in e for e in errors)

    def test_inline_env_type_validation_unchanged(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {"other-svc": {"command": ["run"], "env": {"FOO": 1}}}},
            workspace=Path.cwd(),
        )
        assert any("other-svc.env" in e and "must be strings" in e for e in errors)
        # jail_env stays an inline-service unknown key — only overrides
        # of existing loopholes may set it.
        errors, _ = _validate_config(
            {"loopholes": {"other-svc": {"command": ["run"], "jail_env": {}}}},
            workspace=Path.cwd(),
        )
        assert any("other-svc.jail_env: unknown key" in e for e in errors)

    def test_env_override_without_host_daemon_rejected(self, known_loophole):
        # 'no-daemon-hole' has no host_daemon — the runtime merge only
        # applies 'env' into host_daemon.env, so the override would be
        # silently dropped.  The validator must reject it; 'jail_env'
        # remains the supported way to reach the jail side.
        errors, _ = _validate_config(
            {"loopholes": {"no-daemon-hole": {"env": {"FOO": "bar"}}}},
            workspace=Path.cwd(),
        )
        assert any("no-daemon-hole.env" in e and "no host daemon" in e for e in errors)
        errors, warnings = _validate_config(
            {"loopholes": {"no-daemon-hole": {"jail_env": {"FOO": "bar"}}}},
            workspace=Path.cwd(),
        )
        assert errors == []
        assert warnings == []

    def test_unknown_name_override_shape_warns_instead_of_erroring(
        self, known_loophole
    ):
        # A host user-dir loophole isn't discoverable when validating
        # inside a jail (or after it was uninstalled on the host) — an
        # override-shaped entry must degrade to a warning, not hard-fail
        # with misleading inline-service errors like 'command: required'.
        errors, warnings = _validate_config(
            {"loopholes": {"host-only-hole": {"enabled": False}}},
            workspace=Path.cwd(),
        )
        assert errors == []
        assert any("host-only-hole" in w and "no loophole" in w for w in warnings)

    def test_unknown_name_override_shape_still_type_checked(self, known_loophole):
        errors, _ = _validate_config(
            {"loopholes": {"host-only-hole": {"enabled": "yes"}}},
            workspace=Path.cwd(),
        )
        assert any(".enabled" in e and "boolean" in e for e in errors)

    def test_unreadable_dirs_degrade_to_override_warning(self, monkeypatch):
        # Discovery failing (unreadable dirs) must not turn a valid
        # override into hard errors — override-shaped entries take the
        # same warn-not-error path as the in-jail case.
        def _boom(*a, **kw):
            raise OSError("permission denied")

        monkeypatch.setattr(loopholes, "discover_loopholes", _boom)
        errors, warnings = _validate_config(
            {"loopholes": {"audio": {"enabled": False}}}, workspace=Path.cwd()
        )
        assert errors == []
        assert any("audio" in w for w in warnings)

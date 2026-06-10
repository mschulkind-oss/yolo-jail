import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))
import cli
from cli import (
    ConfigError,
    _check_config_changes,
    _load_jsonc_file,
    _load_jsonc_with_includes,
    _validate_config,
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

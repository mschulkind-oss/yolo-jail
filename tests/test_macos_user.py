"""Unit tests for the native macOS-user backend builders.

The macos-user backend isolates an agent in a dedicated macOS user +
Seatbelt profile (SandVault's model).  The account/ACL/sandbox-exec
machinery is Darwin-only, but every *artifact* it produces is a pure
function returning data, so its SandVault-parity security properties are
asserted here on Linux CI — no Mac required.  The guarantees under test:

  * writes denied everywhere but the workspace + sandbox home + scratch;
  * host credentials unreachable: /Users reads denied (bar traversal +
    workspace + sandbox home), /Library/Keychains denied (load-bearing);
  * raw disk + packet capture denied;
  * the launch scrubs the env (env -i) and the HOME/USER/SHELL identity
    trio cannot be overridden by a caller;
  * the account is hidden + stripped from staff;
  * the workspace ACL is the dir/file split (files never gain execute).
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))

from cli import macos_user as m  # noqa: E402


class TestSeatbeltProfile:
    def _profile(self, ws="/Users/Shared/proj"):
        return m.seatbelt_profile(Path(ws))

    def test_is_allow_default_base(self):
        # SandVault parity: permissive base with targeted denies.
        assert "(allow default)" in self._profile()

    def test_denies_all_writes_then_reallows_workspace(self):
        p = self._profile()
        assert '(deny file-write* (subpath "/"))' in p
        # workspace + sandbox home + scratch re-allowed for writes
        assert '(subpath "/Users/Shared/proj")' in p
        assert '(subpath "/Users/_yolojail")' in p
        assert '(subpath "/tmp")' in p
        assert '(subpath "/var/folders")' in p

    def test_keychain_read_denied_load_bearing(self):
        # System.keychain is world-readable (0644) — this deny is the
        # credential boundary for keychain files.
        assert '(deny file-read* (subpath "/Library/Keychains"))' in self._profile()

    def test_other_user_homes_read_denied(self):
        p = self._profile()
        assert '(deny file-read* (subpath "/Users"))' in p
        # but traversal entries + workspace + sandbox home re-allowed
        assert '(literal "/Users")' in p
        assert '(subpath "/Users/Shared/proj")' in p

    def test_raw_disk_and_bpf_denied(self):
        p = self._profile()
        assert '#"^/dev/r?disk"' in p
        assert '#"^/dev/bpf"' in p

    def test_volumes_denied_except_boot(self):
        p = self._profile()
        assert '(deny file-read* (subpath "/Volumes"))' in p
        assert '(allow file-read* (subpath "/Volumes/Macintosh HD"))' in p

    def test_deny_precedes_reallow_last_match_wins(self):
        # Seatbelt is last-match-wins: every re-allow must come AFTER its
        # deny or the deny would win and break the agent (or the re-allow
        # would be dead).  Check ordering for the two layered rules.
        p = self._profile()
        assert p.index('(deny file-write* (subpath "/"))') < p.index(
            "(allow file-write*"
        )
        assert p.index('(deny file-read* (subpath "/Users"))') < p.index(
            '(literal "/Users")'
        )

    def test_workspace_path_is_sbpl_escaped(self):
        # A workspace path with a quote/backslash must not break the SBPL.
        p = m.seatbelt_profile(Path('/Users/Shared/a"b'))
        assert r"\"" in p  # the embedded quote is escaped


class TestLaunchArgv:
    def _argv(self, **kw):
        kw.setdefault("profile_path", Path("/var/yolo-jail/p.sb"))
        kw.setdefault("sandbox_env", {})
        return m.launch_argv(["claude", "--dangerously-skip-permissions"], **kw)

    def test_runs_as_sandbox_user_via_sudo(self):
        argv = self._argv()
        assert argv[0] == "sudo"
        assert "--user=_yolojail" in argv

    def test_scrubs_env_with_env_dash_i(self):
        argv = self._argv()
        i = argv.index("/usr/bin/env")
        assert argv[i + 1] == "-i"  # env -i clears inherited env

    def test_sets_home_to_sandbox_home(self):
        # The #1 footgun: without a scrubbed HOME the agent reads the host
        # user's ~/.gitconfig / ~/.ssh.
        assert "HOME=/Users/_yolojail" in self._argv()

    def test_identity_trio_not_overridable_by_caller(self):
        argv = self._argv(
            sandbox_env={"HOME": "/evil", "USER": "root", "SHELL": "/x", "OK": "1"}
        )
        assert "HOME=/evil" not in argv
        assert "USER=root" not in argv
        assert "HOME=/Users/_yolojail" in argv
        assert "OK=1" in argv  # non-identity extra env is passed through

    def test_wraps_agent_in_sandbox_exec(self):
        argv = self._argv()
        assert "/usr/bin/sandbox-exec" in argv
        assert "-f" in argv
        assert str(Path("/var/yolo-jail/p.sb")) in argv
        # agent argv comes last, after the -- separator
        assert argv[-3:] == ["--", "claude", "--dangerously-skip-permissions"]


class TestUserProvisioning:
    def test_create_hides_and_destaffs(self):
        cmds = m.create_user_commands(601, 601, host_user="matt")
        assert [
            "dscl",
            ".",
            "-create",
            "/Users/_yolojail",
            "IsHidden",
            "1",
        ] in cmds
        assert [
            "dseditgroup",
            "-o",
            "edit",
            "-d",
            "_yolojail",
            "-t",
            "user",
            "staff",
        ] in cmds

    def test_create_adds_both_users_to_shared_group(self):
        cmds = m.create_user_commands(601, 601, host_user="matt")
        assert [
            "dseditgroup",
            "-o",
            "edit",
            "-a",
            "_yolojail",
            "-t",
            "user",
            "_yolojail",
        ] in cmds
        assert [
            "dseditgroup",
            "-o",
            "edit",
            "-a",
            "matt",
            "-t",
            "user",
            "_yolojail",
        ] in cmds

    def test_create_never_embeds_password(self):
        # The password must be set via dscl -passwd with a value from
        # openssl rand, NEVER a literal argv (it would show in `ps`).
        cmds = m.create_user_commands(601, 601, host_user="matt")
        flat = " ".join(tok for cmd in cmds for tok in cmd)
        assert "-passwd" not in flat

    def test_home_provisioned_0750(self):
        cmds = m.create_user_commands(601, 601, host_user="matt")
        assert ["chmod", "750", "/Users/_yolojail"] in cmds

    def test_delete_removes_user_group_and_home(self):
        cmds = m.delete_user_commands(host_user="matt")
        assert ["dscl", ".", "-delete", "/Users/_yolojail"] in cmds
        assert ["dscl", ".", "-delete", "/Groups/_yolojail"] in cmds
        assert ["rm", "-rf", "/Users/_yolojail"] in cmds


class TestWorkspaceAcl:
    def test_dir_ace_has_inherit_and_traversal(self):
        aces = m.workspace_acl_aces()
        assert "directory_inherit" in aces["dir"]
        assert "search" in aces["dir"] and "list" in aces["dir"]

    def test_file_ace_never_grants_execute_or_search(self):
        # SandVault's split: plain files must not gain search/list (execute).
        aces = m.workspace_acl_aces()
        assert "search" not in aces["file"]
        assert "list" not in aces["file"]

    def test_file_inherit_template_is_only_inherit(self):
        aces = m.workspace_acl_aces()
        assert "only_inherit" in aces["file_inherit"]
        assert "file_inherit" in aces["file_inherit"]

    def test_apply_script_splits_dirs_and_files(self):
        script = m.workspace_acl_apply_script(Path("/Users/Shared/proj"))
        assert "-type d" in script
        assert "! -type d" in script
        assert "chmod -h +a" in script  # -h: don't follow symlinks
        assert "'/Users/Shared/proj'" in script


class TestEntrypointBootstrap:
    def _script(self, agents=("claude", "codex")):
        return m.entrypoint_bootstrap_script(
            Path("/opt/yolo-jail/src"),
            workspace=Path("/Users/Shared/proj"),
            sandbox_home=m.SANDBOX_HOME,
            agents=list(agents),
        )

    def test_runs_the_reused_generators(self):
        s = self._script()
        for gen in (
            "generate_shims",
            "generate_agent_launchers",
            "generate_bashrc",
            "generate_mise_config",
            "generate_mcp_wrappers",
            "CONFIG_WRITERS",
        ):
            assert gen in s

    def test_skips_linux_only_boot_steps(self):
        # The Linux-only generator/setup CALLS must not appear (checked as
        # invocations, not substrings, so prose comments don't false-match).
        s = self._script()
        for call in (
            "generate_ld_cache(",
            "setup_cgroup_delegation(",
            "start_container_port_forwarding(",
            "configure_timezone(",
        ):
            assert call not in s

    def test_selects_only_configured_agents(self):
        s = self._script(agents=["codex"])
        # YOLO_AGENTS drives the per-agent loop (double-encoded JSON string).
        assert "codex" in s
        assert "YOLO_AGENTS" in s

    def test_rebinds_home_to_sandbox_home(self):
        assert "/Users/_yolojail" in self._script()


class TestOrchestratorGuards:
    def test_run_macos_user_fails_closed_off_macos(self, monkeypatch):
        # On non-macOS the orchestrator must refuse rather than half-run —
        # and must NOT shell out to any provisioning command.
        monkeypatch.setattr(m, "_is_macos", lambda: False)
        called = []
        monkeypatch.setattr(
            m.subprocess, "run", lambda *a, **k: called.append(a) or None
        )
        rc = m.run_macos_user(
            Path("/tmp/ws"),
            {},
            ["claude"],
            ["claude"],
            repo_src=Path("/opt/yolo-jail/src"),
        )
        assert rc == 1
        assert called == []  # failed closed before any subprocess

    def test_session_profile_path_is_root_owned_state_dir(self):
        p = m.session_profile_path("yolo-proj-abcd1234")
        assert str(p).startswith("/var/yolo-jail/")
        assert p.name == "profile-yolo-proj-abcd1234.sb"


class TestMacosLogHelper:
    def test_off_mode_is_a_disabled_stub(self):
        s = m.macos_log_wrapper_script("off")
        assert "disabled" in s
        assert "exit 1" in s
        assert "/usr/bin/log" not in s  # off must not exec log

    def test_full_mode_passes_through(self):
        s = m.macos_log_wrapper_script("full")
        assert 'exec /usr/bin/log "$@"' in s

    def test_user_mode_defaults_to_show(self):
        s = m.macos_log_wrapper_script("user")
        assert "/usr/bin/log show" in s
        assert "stream" in s  # still allows explicit stream

    def test_unknown_mode_falls_back_to_off(self):
        assert m.macos_log_wrapper_script("bogus") == m.macos_log_wrapper_script("off")

    def test_bootstrap_installs_yolo_log(self):
        s = m.entrypoint_bootstrap_script(
            Path("/opt/yolo-jail/src"),
            workspace=Path("/Users/Shared/proj"),
            sandbox_home=m.SANDBOX_HOME,
            agents=["claude"],
            macos_log="user",
        )
        assert "yolo-log" in s
        assert "/usr/bin/log show" in s  # the user-mode helper body is embedded


class TestBrokerSocketGrant:
    def test_grants_group_access_to_socket_and_parent(self):
        cmds = m.broker_socket_grant_commands(Path("/tmp/yolo-broker/broker.sock"))
        assert ["chgrp", "_yolojail", "/tmp/yolo-broker/broker.sock"] in cmds
        assert ["chmod", "0660", "/tmp/yolo-broker/broker.sock"] in cmds
        # parent dir group-scoped (not world) so only the sandbox group connects
        assert ["chgrp", "_yolojail", "/tmp/yolo-broker"] in cmds
        assert ["chmod", "0750", "/tmp/yolo-broker"] in cmds


class TestSandboxEnv:
    def test_includes_git_identity_only(self, monkeypatch):
        monkeypatch.setattr(
            m,
            "_git_config",
            lambda key: {"user.name": "Ada", "user.email": "ada@x.dev"}.get(key),
        )
        monkeypatch.setenv("TERM", "xterm-kitty")
        env = m.macos_sandbox_env({})
        assert env["YOLO_GIT_NAME"] == "Ada"
        assert env["YOLO_GIT_EMAIL"] == "ada@x.dev"
        assert env["TERM"] == "xterm-kitty"

    def test_omits_missing_identity(self, monkeypatch):
        monkeypatch.setattr(m, "_git_config", lambda key: None)
        monkeypatch.delenv("TERM", raising=False)
        monkeypatch.delenv("COLORTERM", raising=False)
        # No host creds, no git identity, no TERM → empty (nothing leaks in).
        assert m.macos_sandbox_env({}) == {}

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
        kw.setdefault("workspace", Path("/Users/Shared/proj"))
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

    def test_path_leads_with_sandbox_bin_dirs(self):
        # The scrubbed env must still find the agent launchers + mise tools.
        argv = self._argv()
        path = next(a for a in argv if a.startswith("PATH="))
        assert path.startswith("PATH=/Users/_yolojail/.yolo-shims:")
        assert "/Users/_yolojail/.npm-global/bin" in path

    def test_identity_and_path_not_overridable_by_caller(self):
        argv = self._argv(
            sandbox_env={
                "HOME": "/evil",
                "USER": "root",
                "SHELL": "/x",
                "PATH": "/evil/bin",
                "OK": "1",
            }
        )
        assert "HOME=/evil" not in argv
        assert "USER=root" not in argv
        assert "PATH=/evil/bin" not in argv
        assert "HOME=/Users/_yolojail" in argv
        assert "OK=1" in argv  # non-protected extra env is passed through

    def test_workspace_centric_cd(self):
        # Workspace-centric: the agent starts cd'd into the workspace, not
        # the sandbox home (which --login would otherwise impose).
        argv = self._argv(workspace=Path("/Users/Shared/proj"))
        inner = argv[-1]
        assert argv[-3:-1] == ["/bin/zsh", "-c"]
        assert inner.startswith("cd '/Users/Shared/proj' && exec ")
        # agent args are individually shell-quoted after `exec`
        assert "'claude' '--dangerously-skip-permissions'" in inner

    def test_wraps_agent_in_sandbox_exec(self):
        argv = self._argv()
        assert "/usr/bin/sandbox-exec" in argv
        assert "-f" in argv
        assert str(Path("/var/yolo-jail/p.sb")) in argv
        # sandbox-exec runs zsh -c "<cd ws && exec agent>"
        i = argv.index("/usr/bin/sandbox-exec")
        assert argv[i + 1] == "-f"
        assert "--" in argv[i:]


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

    def test_fix_permissions_script_splits_dirs_and_files(self):
        # The on-demand retrofit (yolo macos-fix-permissions), NOT the hot path.
        script = m.fix_permissions_script(Path("/Users/Shared/yolo"))
        assert "-type d" in script
        assert "! -type d" in script
        assert "chmod -h +a" in script  # -h: don't follow symlinks
        assert "'/Users/Shared/yolo'" in script

    def test_fix_permissions_script_is_batched_not_per_item(self):
        # Regression: the old per-run walk forked chmod once per file via a
        # `while read` loop (the multi-minute .venv hang). The retrofit must
        # batch with `find ... -exec chmod {} +`.
        script = m.fix_permissions_script(Path("/Users/Shared/yolo"))
        assert "-exec chmod -h +a" in script
        assert "{} +" in script
        assert "while" not in script  # no per-item read loop
        assert "this can take a moment" in script  # progress note


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


class TestSetup:
    def test_next_free_id_skips_taken(self):
        assert m.next_free_id({600, 601, 603}, floor=600) == 602

    def test_next_free_id_floor_when_empty(self):
        assert m.next_free_id(set(), floor=600) == 600

    def test_setup_requires_macos(self, monkeypatch):
        import typer

        monkeypatch.setattr(m, "_is_macos", lambda: False)
        called = []
        monkeypatch.setattr(
            m.subprocess, "run", lambda *a, **k: called.append(a) or None
        )
        try:
            m.macos_setup()
            raised = False
        except typer.Exit:
            raised = True
        assert raised
        assert called == []  # no provisioning shelled out off-macОS

    def test_teardown_requires_macos(self, monkeypatch):
        import typer

        monkeypatch.setattr(m, "_is_macos", lambda: False)
        try:
            m.macos_teardown()
            raised = False
        except typer.Exit:
            raised = True
        assert raised


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


# --- Hardening (B1–B4) — pure builders, asserted on Linux --------------------


class TestPythonResolution:
    def test_candidates_have_stub_last(self):
        # B2: /usr/bin/python3 (the xcode-select stub risk) must never be
        # preferred over a real Homebrew/Nix python3.
        cands = m.python_candidates()
        assert cands[-1] == "/usr/bin/python3"
        assert "/opt/homebrew/bin/python3" in cands
        assert cands.index("/opt/homebrew/bin/python3") < cands.index(
            "/usr/bin/python3"
        )

    def test_resolve_prefers_homebrew_over_stub(self):
        # Both exist → the Homebrew one wins.
        got = m.resolve_python(exists=lambda p: True)
        assert got == "/opt/homebrew/bin/python3"

    def test_resolve_falls_back_to_stub(self):
        got = m.resolve_python(exists=lambda p: p == "/usr/bin/python3")
        assert got == "/usr/bin/python3"

    def test_resolve_none_when_absent(self):
        assert m.resolve_python(exists=lambda p: False) is None


class TestNoSudoPolicyChange:
    def test_no_sudoers_machinery(self):
        # We must NOT ship any host sudo-policy mutation — changing the user's
        # sudo config is their call, not ours (SandVault prompts every run).
        assert not hasattr(m, "sudoers_rule")
        assert not hasattr(m, "_install_sudoers")
        assert not hasattr(m, "SUDOERS_PATH")
        assert not hasattr(m, "_passwordless_sudo_ok")


class TestStageEntrypoint:
    def test_copies_into_root_owned_state_dir(self):
        cmds = m.stage_entrypoint_commands(Path("/opt/yolo-jail/src"))
        flat = [" ".join(c) for c in cmds]
        assert any(c.startswith("/bin/mkdir -p /var/yolo-jail") for c in flat)
        assert any(
            "/bin/cp -R /opt/yolo-jail/src/entrypoint/. /var/yolo-jail/entrypoint" == c
            for c in flat
        )
        # world-readable, dirs traversable
        assert any("/bin/chmod -R a+rX /var/yolo-jail/entrypoint" == c for c in flat)


class TestWorkspaceLocation:
    """The workspace must be neutral ground — never inside a user's home.

    This is the model's clear-semantics property: sharing happens only on a
    dedicated location outside every home, so no access grant is ever
    threaded through anyone's home directory.
    """

    def test_home_path_is_rejected(self):
        # A project nested in the host home → the offending home is returned.
        assert m.home_containing(Path("/Users/matt/code/proj")) == Path("/Users/matt")
        # The home dir itself, too.
        assert m.home_containing(Path("/Users/matt")) == Path("/Users/matt")

    def test_shared_location_is_neutral(self):
        # /Users/Shared/... is explicitly NOT a home → accepted.
        assert m.home_containing(Path("/Users/Shared/yolo/proj")) is None
        assert m.home_containing(Path("/Users/Shared")) is None

    def test_non_users_paths_are_neutral(self):
        assert m.home_containing(Path("/opt/yolo/proj")) is None
        assert m.home_containing(Path("/Volumes/ext/work")) is None

    def test_default_shared_root_is_under_users_shared(self):
        assert str(m.SHARED_ROOT_DEFAULT).startswith("/Users/Shared/")
        assert m.home_containing(m.SHARED_ROOT_DEFAULT) is None

    def test_seatbelt_has_no_ancestor_metadata_block(self):
        # No per-ancestor traversal grant of any kind now.
        p = m.seatbelt_profile(Path("/Users/Shared/yolo/proj"))
        assert "file-read-metadata" not in p
        # The /Users deny + /Users/Shared traversal literal still stand.
        assert '(deny file-read* (subpath "/Users"))' in p
        assert '(literal "/Users/Shared")' in p

    def test_shared_root_provision_setgid_and_inheriting_acl(self):
        # The whole sharing story lives here: setgid + group ownership + the
        # INHERITING ACEs on the root itself, so children inherit for free and
        # the hot path needs no walk.
        cmds = m.shared_root_provision_commands(host_user="matt")
        flat = [" ".join(c) for c in cmds]
        assert any(c.startswith("mkdir -p /Users/Shared/yolo") for c in flat)
        assert "chown matt:_yolojail /Users/Shared/yolo" in flat
        assert "chmod 2770 /Users/Shared/yolo" in flat  # setgid + group rwx
        # inheriting ACEs applied to the root itself:
        aces = m.workspace_acl_aces()
        assert ["chmod", "+a", aces["dir"], "/Users/Shared/yolo"] in cmds
        assert ["chmod", "+a", aces["file_inherit"], "/Users/Shared/yolo"] in cmds
        # the dir ACE carries directory_inherit; the template carries file_inherit
        assert "directory_inherit" in aces["dir"]
        assert "file_inherit" in aces["file_inherit"]

    def test_acl_strip_script_removes_all_acls(self):
        s = m.workspace_acl_strip_script(Path("/Users/Shared/yolo/proj"))
        assert "chmod -h -N" in s
        assert "'/Users/Shared/yolo/proj'" in s


class TestBootstrapHardening:
    def _script(self, **kw):
        kw.setdefault("workspace", Path("/Users/matt/code/proj"))
        kw.setdefault("sandbox_home", m.SANDBOX_HOME)
        kw.setdefault("agents", ["claude"])
        return m.entrypoint_bootstrap_script(Path("/opt/yolo-jail/src"), **kw)

    def test_imports_from_staged_state_dir_not_host_checkout(self):
        # B3: import path is the root-owned staged dir, never repo_src.
        s = self._script()
        assert "sys.path.insert(0, '/var/yolo-jail')" in s
        # the host checkout path only appears in a comment, not an insert
        assert "sys.path.insert(0, '/opt/yolo-jail/src')" not in s

    def test_sets_jail_home_before_importing_entrypoint(self):
        # HOME-derived path constants freeze at import — JAIL_HOME must be set
        # BEFORE `import entrypoint`.
        s = self._script()
        assert s.index('os.environ["JAIL_HOME"]') < s.index("import entrypoint")

    def test_bakes_git_identity_into_bootstrap_env(self):
        s = self._script(
            git_identity={"YOLO_GIT_NAME": "Ada", "YOLO_GIT_EMAIL": "ada@x.dev"}
        )
        assert "os.environ['YOLO_GIT_NAME'] = 'Ada'" in s
        assert "os.environ['YOLO_GIT_EMAIL'] = 'ada@x.dev'" in s


class TestBootstrapArgv:
    def test_no_login_flags(self):
        # --login/--set-home would source _yolojail's login zsh + /etc/zprofile
        # and try to cd into a 0750 home; dropped in favor of staged import.
        argv = m._bootstrap_argv(
            "/opt/homebrew/bin/python3", Path("/var/yolo-jail/b.py")
        )
        assert "--login" not in argv
        assert "--set-home" not in argv
        assert argv[:2] == ["sudo", "--user=_yolojail"]
        assert argv[2] == "/opt/homebrew/bin/python3"


class TestRunPlan:
    def _plan(self, ws="/Users/Shared/yolo/proj", interp="/opt/homebrew/bin/python3"):
        return m.build_run_plan(
            Path(ws),
            {},
            ["claude"],
            ["claude"],
            repo_src=Path("/opt/yolo-jail/src"),
            sandbox_env={"YOLO_GIT_NAME": "Ada", "TERM": "xterm"},
            interp=interp,
        )

    def test_clean_plan_has_no_violations(self):
        assert m.plan_invariants(self._plan()) == []

    def test_flags_unresolved_interpreter(self):
        probs = m.plan_invariants(self._plan(interp=None))
        assert any("no python3" in p for p in probs)

    def test_git_identity_lifted_into_plan_and_bootstrap(self):
        plan = self._plan()
        assert plan.git_identity == {"YOLO_GIT_NAME": "Ada"}
        assert "YOLO_GIT_NAME" in plan.bootstrap

    def test_launch_uses_sandbox_exec_with_session_profile(self):
        plan = self._plan()
        assert "/usr/bin/sandbox-exec" in plan.launch_argv
        assert str(plan.profile_path) in plan.launch_argv

    def test_home_workspace_is_a_plan_violation(self):
        probs = m.plan_invariants(self._plan(ws="/Users/matt/code/proj"))
        assert any("inside the home directory" in p for p in probs)


class TestDryRun:
    def test_dry_run_prints_and_executes_nothing(self, monkeypatch):
        # Works off-macOS (pure); must not shell out.
        monkeypatch.setattr(m, "_is_macos", lambda: False)
        monkeypatch.setattr(m, "resolve_python", lambda: "/opt/homebrew/bin/python3")
        monkeypatch.setattr(m, "_git_config", lambda key: None)
        called = []
        monkeypatch.setattr(
            m.subprocess, "run", lambda *a, **k: called.append(a) or None
        )
        rc = m.run_macos_user(
            Path("/Users/Shared/yolo/proj"),
            {},
            ["claude"],
            ["claude"],
            repo_src=Path("/opt/yolo-jail/src"),
            dry_run=True,
        )
        assert rc == 0  # clean plan
        assert called == []  # nothing executed

    def test_dry_run_nonzero_when_plan_broken(self, monkeypatch):
        monkeypatch.setattr(m, "_is_macos", lambda: False)
        monkeypatch.setattr(m, "resolve_python", lambda: None)  # no interpreter
        monkeypatch.setattr(m, "_git_config", lambda key: None)
        rc = m.run_macos_user(
            Path("/Users/Shared/yolo/proj"),
            {},
            ["claude"],
            ["claude"],
            repo_src=Path("/opt/yolo-jail/src"),
            dry_run=True,
        )
        assert rc == 1

    def test_dry_run_rejects_home_workspace(self, monkeypatch):
        # A workspace inside the host home is a hard plan violation → rc 1,
        # nothing executed.
        monkeypatch.setattr(m, "_is_macos", lambda: False)
        monkeypatch.setattr(m, "resolve_python", lambda: "/opt/homebrew/bin/python3")
        monkeypatch.setattr(m, "_git_config", lambda key: None)
        called = []
        monkeypatch.setattr(
            m.subprocess, "run", lambda *a, **k: called.append(a) or None
        )
        rc = m.run_macos_user(
            Path("/Users/matt/code/proj"),
            {},
            ["claude"],
            ["claude"],
            repo_src=Path("/opt/yolo-jail/src"),
            dry_run=True,
        )
        assert rc == 1
        assert called == []

"""Host↔jail state separation (docs/jail-state-separation-design.md).

Covers the split mise store (/mise mount + env contract), the
nested-jail store rule, per-side venv shadow mounts, the layout-v2
migration, jail-made venv retirement, the zero-live-jails store-prune
gate, and the reworked provisioning shell.
"""

import shutil
import subprocess
import sys
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

import cli  # noqa: E402
from cli import app  # noqa: E402
from cli.run_cmd import (  # noqa: E402
    _live_yolo_containers,
    _mise_config_venv_path,
    _retire_jail_made_venv,
    _venv_shadow_mount_args,
)
from cli.storage import (  # noqa: E402
    _jail_mise_store_dir,
    _migrate_storage_layout,
)


# ---------------------------------------------------------------------------
# Helpers — mirror the run-command scaffolding from test_cli_commands.py
# ---------------------------------------------------------------------------


def _run_monkeypatch(monkeypatch, tmp_path):
    """Common monkeypatching for run command tests."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("YOLO_REPO_ROOT", str(REPO_ROOT))
    for mod in ("cli", "cli.run_cmd"):
        monkeypatch.setattr(f"{mod}.GLOBAL_HOME", tmp_path / "home")
        monkeypatch.setattr(f"{mod}.GLOBAL_MISE", tmp_path / "mise")
        monkeypatch.setattr(f"{mod}.GLOBAL_STORAGE", tmp_path / "storage")
        monkeypatch.setattr(f"{mod}.CONTAINER_DIR", tmp_path / "containers")
        monkeypatch.setattr(f"{mod}.AGENTS_DIR", tmp_path / "agents")
        monkeypatch.setattr(f"{mod}.BUILD_DIR", tmp_path / "build")
        monkeypatch.setattr(f"{mod}.USER_CONFIG_PATH", tmp_path / "user-config.jsonc")
    # cli.storage holds its own bindings: _jail_mise_store_dir reads
    # GLOBAL_MISE and the layout migration reads GLOBAL_STORAGE there.
    monkeypatch.setattr("cli.storage.GLOBAL_MISE", tmp_path / "mise")
    monkeypatch.setattr("cli.storage.GLOBAL_STORAGE", tmp_path / "storage")
    # Keep the briefing generation inside tmp_path as well.
    monkeypatch.setattr("cli.agents_md.AGENTS_DIR", tmp_path / "agents")
    monkeypatch.setattr("cli.runtime._runtime_is_connectable", lambda rt: True)
    monkeypatch.setattr("time.sleep", lambda _: None)
    # Broker/relay hermeticity — same rationale as test_cli_commands's
    # _run_monkeypatch: point the singleton socket at a tmp path so the
    # run() broker block never keys off the dev machine's real
    # /tmp/yolo-claude-oauth-broker.sock (it EXISTS so the block is
    # exercised deterministically), and stub the relay ensure/sweep so
    # no supervised relay process, /tmp PID file, or log under the real
    # ~/.local/share/yolo-jail leaks out of a unit test.  Applies to the
    # exec-into-existing attach path too (_ensure_broker_relay).
    fake_sock = tmp_path / "broker.sock"
    fake_sock.touch()
    monkeypatch.setattr("cli.run_cmd._broker_ensure", lambda: fake_sock)
    monkeypatch.setattr("cli.run_cmd.BROKER_SINGLETON_SOCKET", fake_sock)
    monkeypatch.setattr("cli.run_cmd._relay_ensure", MagicMock())
    monkeypatch.setattr("cli.run_cmd._relay_reap_orphans", MagicMock(return_value=[]))
    monkeypatch.setattr("cli.loopholes_runtime.GLOBAL_STORAGE", tmp_path / "storage")
    monkeypatch.setattr("cli.run_cmd.start_loopholes", lambda *a, **kw: [])
    monkeypatch.setattr("cli.run_cmd.stop_loopholes", lambda *a, **kw: None)
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
    # Pre-stamp the layout marker so the one-time migration (which scans
    # the real host mise dir) stays a no-op unless a test opts in.
    (tmp_path / "storage" / "layout-version").write_text("2\n")


def _set_macos(monkeypatch):
    monkeypatch.setattr(cli, "IS_MACOS", True)
    monkeypatch.setattr(cli, "IS_LINUX", False)
    monkeypatch.setattr("cli.runtime.IS_MACOS", True)
    monkeypatch.setattr("cli.image.IS_MACOS", True)
    monkeypatch.setattr("cli.paths.IS_MACOS", True)
    monkeypatch.setattr("cli.paths.IS_LINUX", False)
    monkeypatch.setattr("cli.run_cmd.IS_MACOS", True)
    monkeypatch.setattr("cli.run_cmd.IS_LINUX", False)
    monkeypatch.setattr("cli.loopholes_runtime.IS_MACOS", True)
    monkeypatch.setattr("cli.loopholes_runtime.IS_LINUX", False)
    monkeypatch.setattr("cli.check_cmd.IS_MACOS", True)


@contextmanager
def _mocked_launch(runtimes=("podman", "nix")):
    """Patch out subprocess + container plumbing; yield the Popen mock."""
    with (
        patch("shutil.which") as mock_which,
        patch("subprocess.check_output", side_effect=FileNotFoundError),
        patch("subprocess.run", return_value=MagicMock(returncode=0, stdout="")),
        patch("cli.run_cmd.find_running_container", return_value=None),
        patch("cli.run_cmd._check_config_changes", return_value=True),
        patch("cli.run_cmd.auto_load_image"),
        patch("subprocess.Popen") as mock_popen,
    ):
        mock_which.side_effect = lambda x: f"/usr/bin/{x}" if x in runtimes else None
        proc = MagicMock()
        proc.wait.return_value = None
        proc.returncode = 0
        mock_popen.return_value = proc
        yield mock_popen


def _launch_argv(tmp_path, monkeypatch, *, config_text="{}", macos=False):
    """Invoke ``yolo run`` with mocks and return the container argv."""
    _run_monkeypatch(monkeypatch, tmp_path)
    if macos:
        _set_macos(monkeypatch)
    (tmp_path / "yolo-jail.jsonc").write_text(config_text)
    with _mocked_launch() as mock_popen:
        result = CliRunner().invoke(app, ["run", "--", "true"])
        assert mock_popen.called, f"launch argv expected; output: {result.output}"
        return [str(a) for a in mock_popen.call_args[0][0]]


# ---------------------------------------------------------------------------
# Store mount per runtime
# ---------------------------------------------------------------------------


class TestStoreMount:
    def test_linux_podman_binds_jail_store_at_mise(self, tmp_path, monkeypatch):
        argv = _launch_argv(tmp_path, monkeypatch)
        assert f"{tmp_path / 'mise'}:/mise" in argv
        # The old same-path host mount is gone.
        assert not any(a.endswith(f":{tmp_path / 'mise'}") for a in argv), (
            "host-path mise mount target must not exist anymore"
        )

    def test_macos_podman_uses_named_volume_at_mise(self, tmp_path, monkeypatch):
        argv = _launch_argv(tmp_path, monkeypatch, macos=True)
        # v2 name: the pre-split volume was mounted at the host path
        # string, so its path-embedding installs (pipx-backend venvs)
        # would break if remounted at /mise — fresh volume, cold start.
        assert "yolo-mise-data-v2:/mise" in argv
        assert not any(a.startswith("yolo-mise-data:") for a in argv)

    def test_no_host_mise_path_anywhere_in_argv(self, tmp_path, monkeypatch):
        argv = _launch_argv(tmp_path, monkeypatch)
        host_mise = str(Path.home() / ".local" / "share" / "mise")
        assert not any(host_mise in a for a in argv), (
            "no host mise path may leak into the jail argv"
        )


# ---------------------------------------------------------------------------
# Env contract
# ---------------------------------------------------------------------------


class TestJailEnv:
    def test_mise_env_contract(self, tmp_path, monkeypatch):
        argv = _launch_argv(tmp_path, monkeypatch)
        assert "MISE_DATA_DIR=/mise" in argv
        assert "MISE_TRUSTED_CONFIG_PATHS=/workspace" in argv
        assert "MISE_ENV=jail" in argv
        assert "MISE_TRUST=1" not in argv
        assert not any(a.startswith("YOLO_OUTER_MISE_PATH") for a in argv)

    def test_rustup_cargo_homes_point_into_the_store(self, tmp_path, monkeypatch):
        """mise's rust backend drives rustup, which defaults to ~/.rustup /
        ~/.cargo — read-only in-jail.  The jail defaults must land in the
        writable store; a workspace's own mise [env] overrides them on
        activation (empirically verified, mise 2026.6.13)."""
        argv = _launch_argv(tmp_path, monkeypatch)
        assert "RUSTUP_HOME=/mise/rustup" in argv
        assert "CARGO_HOME=/mise/cargo" in argv

    def test_preflight_uses_fixed_store_path(self, tmp_path, monkeypatch):
        from cli.run_cmd import _entrypoint_preflight

        captured = {}

        def fake_run(cmd, **kwargs):
            captured["env"] = kwargs.get("env")
            return MagicMock(returncode=0, stdout="ok", stderr="")

        monkeypatch.setattr("cli.run_cmd.subprocess.run", fake_run)
        _entrypoint_preflight(REPO_ROOT, tmp_path, {})
        assert captured["env"]["MISE_DATA_DIR"] == "/mise"


# ---------------------------------------------------------------------------
# Nested-jail store rule
# ---------------------------------------------------------------------------


class TestJailMiseStoreDir:
    def test_host_side_returns_global_mise(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        monkeypatch.setattr("cli.storage.GLOBAL_MISE", tmp_path / "mise")
        assert _jail_mise_store_dir() == tmp_path / "mise"

    def test_inside_jail_remounts_slash_mise(self, monkeypatch):
        monkeypatch.setenv("YOLO_VERSION", "1.2.3")
        assert _jail_mise_store_dir() == Path("/mise")


# ---------------------------------------------------------------------------
# Venv shadow mounts
# ---------------------------------------------------------------------------


class TestVenvShadowMounts:
    def _ws(self, tmp_path):
        ws = tmp_path / "ws"
        ws_state = ws / ".yolo" / "home"
        ws_state.mkdir(parents=True)
        return ws, ws_state

    def _mounts(self, args):
        return [args[i + 1] for i, a in enumerate(args) if a == "-v"]

    def test_default_venv_shadow(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        args = _venv_shadow_mount_args(ws, ws_state, {})
        assert self._mounts(args) == [
            f"{ws_state / 'venv-shadows' / '.venv'}:/workspace/.venv"
        ]
        assert (ws_state / "venv-shadows" / ".venv").is_dir()

    def test_mise_toml_string_form(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text('[env]\n_.python.venv = ".venv-custom"\n')
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert (
            f"{ws_state / 'venv-shadows' / '.venv-custom'}:/workspace/.venv-custom"
            in mounts
        )
        # .venv is always shadowed too
        assert any(m.endswith(":/workspace/.venv") for m in mounts)

    def test_mise_toml_dict_form(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text(
            '[env]\n_.python.venv = { path = ".venv-d", create = true }\n'
        )
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert any(m.endswith(":/workspace/.venv-d") for m in mounts)

    def test_mise_toml_dict_form_default_path(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text("[env]\n_.python.venv = { create = true }\n")
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert mounts == [f"{ws_state / 'venv-shadows' / '.venv'}:/workspace/.venv"]

    def test_dotted_mise_toml_wins(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text('[env]\n_.python.venv = ".from-plain"\n')
        (ws / ".mise.toml").write_text('[env]\n_.python.venv = ".from-dotted"\n')
        assert _mise_config_venv_path(ws) == ".from-dotted"
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert any(m.endswith(":/workspace/.from-dotted") for m in mounts)
        assert not any(m.endswith(":/workspace/.from-plain") for m in mounts)

    def test_mise_jail_toml_wins_over_base_pair(self, tmp_path):
        # Every jail exports MISE_ENV=jail, so a checked-in
        # mise.jail.toml overrides mise.toml — its venv path must join
        # the shadow set or the jail venv leaks into the host workspace.
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text('[env]\n_.python.venv = ".venv-base"\n')
        (ws / "mise.jail.toml").write_text(
            '[env]\n_.python.venv = { path = ".venv-jail", create = true }\n'
        )
        assert _mise_config_venv_path(ws) == ".venv-jail"
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert any(m.endswith(":/workspace/.venv-jail") for m in mounts)

    def test_unparseable_mise_toml_reads_as_absent(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text("not [valid toml ===")
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert mounts == [f"{ws_state / 'venv-shadows' / '.venv'}:/workspace/.venv"]

    def test_non_utf8_mise_toml_reads_as_absent(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_bytes(b"\xff\xfe[env]\n")
        assert _mise_config_venv_path(ws) is None
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert mounts == [f"{ws_state / 'venv-shadows' / '.venv'}:/workspace/.venv"]

    def test_config_root_template_resolves_to_workspace(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text(
            '[env]\n_.python.venv = { path = "{{config_root}}/.venv-t", create = true }\n'
        )
        assert _mise_config_venv_path(ws) == ".venv-t"
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert any(m.endswith(":/workspace/.venv-t") for m in mounts)

    def test_unresolvable_template_never_materializes(self, tmp_path):
        # A tera template we can't resolve host-side must not become a
        # literal '{{…}}' mountpoint dir inside the host workspace.
        ws, ws_state = self._ws(tmp_path)
        (ws / "mise.toml").write_text(
            '[env]\n_.python.venv = "{{env.HOME}}/venvs/dev"\n'
        )
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert mounts == [f"{ws_state / 'venv-shadows' / '.venv'}:/workspace/.venv"]
        assert not any("{{" in m for m in mounts)

    def test_symlink_venv_is_not_shadowed(self, tmp_path):
        # A directory mount over a symlink aborts container creation
        # (crun: openat2 ENOENT) — skip the shadow instead of the boot.
        ws, ws_state = self._ws(tmp_path)
        (ws / ".venv").symlink_to(tmp_path / "elsewhere")
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert mounts == []

    def test_regular_file_venv_is_not_shadowed(self, tmp_path):
        # pipenv's '.venv' path-file convention: a directory mount over
        # a file fails with 'not a directory' — skip the shadow.
        ws, ws_state = self._ws(tmp_path)
        (ws / ".venv").write_text("/home/user/.virtualenvs/proj\n")
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, {}))
        assert mounts == []

    def test_per_side_paths_config(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        config = {"per_side_paths": [".cargo", "data/models"]}
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, config))
        assert f"{ws_state / 'venv-shadows' / '.cargo'}:/workspace/.cargo" in mounts
        # Nested path: slash becomes __ in the backing dir name.
        assert (
            f"{ws_state / 'venv-shadows' / 'data__models'}:/workspace/data/models"
            in mounts
        )
        assert (ws_state / "venv-shadows" / "data__models").is_dir()

    def test_invalid_entries_skipped(self, tmp_path):
        ws, ws_state = self._ws(tmp_path)
        config = {"per_side_paths": ["..", "/abs", ".", "", "a/../b"]}
        mounts = self._mounts(_venv_shadow_mount_args(ws, ws_state, config))
        assert mounts == [f"{ws_state / 'venv-shadows' / '.venv'}:/workspace/.venv"]

    def test_shadow_mounted_after_rw_workspace(self, tmp_path, monkeypatch):
        argv = _launch_argv(tmp_path, monkeypatch)
        ws_idx = argv.index(f"{tmp_path}:/workspace")
        shadow_idx = next(
            i for i, a in enumerate(argv) if a.endswith(":/workspace/.venv")
        )
        assert ws_idx < shadow_idx, "shadow must come after the rw workspace mount"

    def test_shadow_sources_are_directories(self, tmp_path, monkeypatch):
        """Directory mounts only — the AC-safe kind (apple/container#1089)."""
        argv = _launch_argv(tmp_path, monkeypatch)
        for i, a in enumerate(argv):
            if a != "-v":
                continue
            spec = argv[i + 1]
            src = spec.split(":", 1)[0]
            if "venv-shadows" in src:
                assert Path(src).is_dir(), spec


# ---------------------------------------------------------------------------
# Layout migration (v2)
# ---------------------------------------------------------------------------


class TestStorageLayoutMigration:
    def _setup(self, tmp_path, monkeypatch, *, live=frozenset()):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        storage = tmp_path / "storage"
        storage.mkdir()
        host_mise = tmp_path / "host-mise"
        monkeypatch.setattr("cli.storage.GLOBAL_STORAGE", storage)
        monkeypatch.setattr("cli.storage._host_mise_dir", lambda: host_mise)
        # The heal is gated on zero live jails (a dangling-on-the-host
        # entry can be live inside a running old-model jail).  Default:
        # runtime resolvable, nothing running — the gate is open.
        monkeypatch.setattr(
            "cli.runtime._runtime_for_check", lambda cfg: ("podman", None)
        )
        monkeypatch.setattr(
            "cli.run_cmd._live_yolo_containers",
            lambda rt: set(live) if live is not None else None,
        )
        tool = host_mise / "installs" / "rust"
        tool.mkdir(parents=True)
        # Dangling symlink — jail-written debris under the old model.
        dangling = tool / "1.95.0"
        dangling.symlink_to(tmp_path / "nonexistent" / "bin")
        # Resolving symlink — a healthy entry.
        target = tmp_path / "real-toolchain"
        target.mkdir()
        healthy = tool / "1.90.0"
        healthy.symlink_to(target)
        # Regular file and dir — never touched.
        regular_file = tool / ".keep"
        regular_file.write_text("x")
        regular_dir = tool / "1.80.0"
        regular_dir.mkdir()
        return storage, dangling, healthy, regular_file, regular_dir

    def test_heals_dangling_and_stamps_marker(self, tmp_path, monkeypatch):
        storage, dangling, healthy, regular_file, regular_dir = self._setup(
            tmp_path, monkeypatch
        )
        _migrate_storage_layout()
        assert not dangling.is_symlink(), "dangling symlink must be removed"
        assert healthy.is_symlink() and healthy.exists(), "resolving link kept"
        assert regular_file.is_file()
        assert regular_dir.is_dir()
        assert (storage / "layout-version").read_text().strip() == "2"

    def test_second_run_is_noop(self, tmp_path, monkeypatch):
        storage, dangling, *_ = self._setup(tmp_path, monkeypatch)
        _migrate_storage_layout()
        # New debris after the marker is stamped stays put — the heal is
        # a one-time migration, not a recurring sweep.
        late = dangling.parent / "2.0.0"
        late.symlink_to(tmp_path / "still-nonexistent")
        _migrate_storage_layout()
        assert late.is_symlink()

    def test_skipped_inside_jail(self, tmp_path, monkeypatch):
        storage, dangling, *_ = self._setup(tmp_path, monkeypatch)
        monkeypatch.setenv("YOLO_VERSION", "1.2.3")
        _migrate_storage_layout()
        assert dangling.is_symlink(), "in-jail run must not touch the store"
        assert not (storage / "layout-version").exists()

    def test_missing_host_store_still_stamps(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        storage = tmp_path / "storage"
        storage.mkdir()
        monkeypatch.setattr("cli.storage.GLOBAL_STORAGE", storage)
        monkeypatch.setattr("cli.storage._host_mise_dir", lambda: tmp_path / "absent")
        _migrate_storage_layout()
        assert (storage / "layout-version").read_text().strip() == "2"

    def test_deferred_while_a_jail_is_live(self, tmp_path, monkeypatch):
        # A dangling-on-the-host entry (→ /workspace/…) can be live for
        # a running old-model jail — the heal must wait, and the marker
        # must stay unstamped so a later invocation retries.
        storage, dangling, *_ = self._setup(
            tmp_path, monkeypatch, live={"yolo-other-12345678"}
        )
        _migrate_storage_layout()
        assert dangling.is_symlink(), "heal must not run under a live jail"
        assert not (storage / "layout-version").exists()

    def test_deferred_when_liveness_unknown(self, tmp_path, monkeypatch):
        storage, dangling, *_ = self._setup(tmp_path, monkeypatch, live=None)
        _migrate_storage_layout()
        assert dangling.is_symlink()
        assert not (storage / "layout-version").exists()

    def test_deferred_when_no_runtime_resolvable(self, tmp_path, monkeypatch):
        storage, dangling, *_ = self._setup(tmp_path, monkeypatch)
        monkeypatch.setattr(
            "cli.runtime._runtime_for_check", lambda cfg: (None, "not connected")
        )
        _migrate_storage_layout()
        assert dangling.is_symlink()
        assert not (storage / "layout-version").exists()


# ---------------------------------------------------------------------------
# Jail-made venv retirement
# ---------------------------------------------------------------------------


class TestVenvRetirement:
    def _venv(self, ws: Path, home: str) -> Path:
        venv = ws / ".venv"
        venv.mkdir(parents=True)
        (venv / "pyvenv.cfg").write_text(f"home = {home}\nversion = 3.13.1\n")
        (venv / "bin").mkdir()
        return venv

    def test_jail_flavored_dangling_removed(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        venv = self._venv(tmp_path, "/mise/installs/python/3.13.1/bin")
        _retire_jail_made_venv(tmp_path)
        assert not venv.exists()

    def test_workspace_flavored_dangling_removed(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        venv = self._venv(tmp_path, "/workspace/.tools/python/bin/nonexistent-xyz")
        _retire_jail_made_venv(tmp_path)
        assert not venv.exists()

    def test_old_shared_store_dangling_removed(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        monkeypatch.setenv("HOME", str(tmp_path / "fake-home"))
        home = tmp_path / "fake-home" / ".local" / "share" / "mise" / "py" / "bin"
        venv = self._venv(tmp_path, str(home))  # never created → dangling
        _retire_jail_made_venv(tmp_path)
        assert not venv.exists()

    def test_jail_flavored_but_resolving_kept(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        monkeypatch.setenv("HOME", str(tmp_path / "fake-home"))
        home = tmp_path / "fake-home" / ".local" / "share" / "mise" / "py" / "bin"
        home.mkdir(parents=True)
        venv = self._venv(tmp_path, str(home))
        _retire_jail_made_venv(tmp_path)
        assert venv.exists(), "a resolving venv is host-owned — never touched"

    def test_non_jail_flavored_dangling_kept(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        venv = self._venv(tmp_path, "/opt/some-host-python/bin")
        _retire_jail_made_venv(tmp_path)
        assert venv.exists(), "not jail-made — leave host state alone"

    def test_missing_pyvenv_cfg_is_noop(self, tmp_path, monkeypatch):
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        (tmp_path / ".venv").mkdir()
        _retire_jail_made_venv(tmp_path)
        assert (tmp_path / ".venv").exists()

    def test_skipped_inside_jail(self, tmp_path, monkeypatch):
        monkeypatch.setenv("YOLO_VERSION", "1.2.3")
        venv = self._venv(tmp_path, "/mise/installs/python/3.13.1/bin")
        _retire_jail_made_venv(tmp_path)
        assert venv.exists()

    def test_custom_mise_venv_path_also_retired(self, tmp_path, monkeypatch):
        # The shadow set is computed from the mise config; retirement
        # must cover the same paths or a jail-made custom-path venv
        # stays broken on the host forever (shadowed from every jail).
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        (tmp_path / "mise.toml").write_text('[env]\n_.python.venv = ".venv-app"\n')
        venv = tmp_path / ".venv-app"
        venv.mkdir()
        (venv / "pyvenv.cfg").write_text(
            "home = /mise/installs/python/3.13.1/bin\nversion = 3.13.1\n"
        )
        _retire_jail_made_venv(tmp_path)
        assert not venv.exists()

    def test_symlinked_venv_left_alone(self, tmp_path, monkeypatch):
        # A symlinked .venv is host-made by definition (jails create
        # real dirs) — never retired, even when it dangles.
        monkeypatch.delenv("YOLO_VERSION", raising=False)
        link = tmp_path / ".venv"
        link.symlink_to(tmp_path / "elsewhere")
        _retire_jail_made_venv(tmp_path)
        assert link.is_symlink()

    def test_attach_to_running_jail_skips_retirement(self, tmp_path, monkeypatch):
        # Exec-into-existing must not retire: a still-running old-model
        # jail may be using the venv through the shared workspace bind.
        _run_monkeypatch(monkeypatch, tmp_path)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        venv = tmp_path / ".venv"
        venv.mkdir()
        (venv / "pyvenv.cfg").write_text(
            "home = /mise/installs/python/3.13.1/bin\nversion = 3.13.1\n"
        )
        with (
            patch("shutil.which") as mock_which,
            patch("subprocess.check_output", side_effect=FileNotFoundError),
            patch("subprocess.run", return_value=MagicMock(returncode=0, stdout="")),
            patch("cli.run_cmd.find_running_container", return_value="cid123"),
            patch("cli.run_cmd.run_with_proxy", return_value=0) as mock_proxy,
        ):
            mock_which.side_effect = lambda x: (
                f"/usr/bin/{x}" if x in ("podman", "nix") else None
            )
            CliRunner().invoke(app, ["run", "--", "true"])
            assert mock_proxy.called, "expected the exec-into-existing path"
        assert venv.exists(), "attach must not retire a possibly-live venv"


# ---------------------------------------------------------------------------
# Store-prune gate
# ---------------------------------------------------------------------------


class TestLiveYoloContainers:
    def _res(self, stdout="", returncode=0):
        return MagicMock(returncode=returncode, stdout=stdout)

    def test_parses_live_yolo_names(self, monkeypatch):
        out = (
            "yolo-a-11111111 running\n"
            "yolo-b-22222222 exited\n"
            "yolo-c-33333333 Paused\n"
            "other-container running\n"
            "malformed\n"
        )
        with patch("subprocess.run", return_value=self._res(out)):
            assert _live_yolo_containers("podman") == {
                "yolo-a-11111111",
                "yolo-c-33333333",
            }

    def test_no_containers_is_empty_set(self):
        with patch("subprocess.run", return_value=self._res("")):
            assert _live_yolo_containers("podman") == set()

    def test_enumeration_failure_returns_none(self):
        with patch("subprocess.run", return_value=self._res("", returncode=125)):
            assert _live_yolo_containers("podman") is None
        with patch("subprocess.run", side_effect=FileNotFoundError):
            assert _live_yolo_containers("podman") is None
        with patch(
            "subprocess.run",
            side_effect=subprocess.TimeoutExpired("podman", 10),
        ):
            assert _live_yolo_containers("podman") is None

    def test_apple_container_uses_ls_table_scan(self):
        # The Apple Container CLI has no docker-style `ps`/--format;
        # `container ls` lists running containers only, so any yolo-*
        # row is live.  Without this branch the prune gate could never
        # open on AC.
        out = (
            "ID              IMAGE            OS     ARCH   STATE    ADDR\n"
            "yolo-a-11111111 yolo-jail:latest linux  arm64  running  192.168.64.2\n"
            "other-container some:img         linux  arm64  running  192.168.64.3\n"
        )
        with patch("subprocess.run", return_value=self._res(out)) as mock_run:
            assert _live_yolo_containers("container") == {"yolo-a-11111111"}
            assert mock_run.call_args[0][0] == ["container", "ls"]

    def test_apple_container_failure_returns_none(self):
        with patch("subprocess.run", return_value=self._res("", returncode=1)):
            assert _live_yolo_containers("container") is None
        with patch("subprocess.run", side_effect=FileNotFoundError):
            assert _live_yolo_containers("container") is None

    def test_apple_container_no_jails_is_empty_set(self):
        out = "ID  IMAGE  OS  ARCH  STATE  ADDR\n"
        with patch("subprocess.run", return_value=self._res(out)):
            assert _live_yolo_containers("container") == set()


class TestStorePruneGate:
    def _argv(self, tmp_path, monkeypatch, live):
        _run_monkeypatch(monkeypatch, tmp_path)
        monkeypatch.setattr("cli.run_cmd._live_yolo_containers", lambda runtime: live)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        with _mocked_launch() as mock_popen:
            CliRunner().invoke(app, ["run", "--", "true"])
            assert mock_popen.called
            return [str(a) for a in mock_popen.call_args[0][0]]

    def test_no_live_jails_grants_prune(self, tmp_path, monkeypatch):
        argv = self._argv(tmp_path, monkeypatch, live=set())
        assert "YOLO_STORE_PRUNE_OK=1" in argv

    def test_live_jail_blocks_prune(self, tmp_path, monkeypatch):
        argv = self._argv(tmp_path, monkeypatch, live={"yolo-other-12345678"})
        assert "YOLO_STORE_PRUNE_OK=1" not in argv

    def test_enumeration_failure_blocks_prune(self, tmp_path, monkeypatch):
        # None = liveness unknown — must NOT read as "nothing live".
        argv = self._argv(tmp_path, monkeypatch, live=None)
        assert "YOLO_STORE_PRUNE_OK=1" not in argv

    def test_in_jail_cli_never_grants_prune(self, tmp_path, monkeypatch):
        _run_monkeypatch(monkeypatch, tmp_path)
        monkeypatch.setenv("YOLO_VERSION", "1.2.3")
        monkeypatch.setattr("cli.run_cmd._live_yolo_containers", lambda runtime: set())
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        with _mocked_launch() as mock_popen:
            CliRunner().invoke(app, ["run", "--", "true"])
            assert mock_popen.called
            argv = [str(a) for a in mock_popen.call_args[0][0]]
        assert "YOLO_STORE_PRUNE_OK=1" not in argv


# ---------------------------------------------------------------------------
# Provisioning shell
# ---------------------------------------------------------------------------


class TestProvisioningShell:
    def _final_cmd(self, tmp_path, monkeypatch, *, profile=False):
        _run_monkeypatch(monkeypatch, tmp_path)
        (tmp_path / "yolo-jail.jsonc").write_text("{}")
        args = ["run"] + (["--profile"] if profile else []) + ["--", "true"]
        with _mocked_launch() as mock_popen:
            CliRunner().invoke(app, args)
            assert mock_popen.called
            return str(mock_popen.call_args[0][0][-1])

    def test_startup_log_teed(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch)
        assert "tee -a /workspace/.yolo/startup.log" in cmd
        # Fresh, timestamped log header per container.
        assert ">/workspace/.yolo/startup.log" in cmd
        assert "date" in cmd

    def test_failure_marker_and_prompt(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch)
        assert "PROVISIONING FAILED (exit %s)" in cmd
        assert "continue anyway? [Y/n]" in cmd
        assert "YOLO_PROVISION_PROMPT" in cmd
        # n/N aborts with the provisioning exit code.
        assert '[nN]*) exit "$_prc"' in cmd

    def test_trust_hook_ungated(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch)
        assert "mise trust --all --quiet" in cmd
        assert "if [ -f mise.toml ]" not in cmd, "filename gate must be gone"

    def test_store_prune_snippet_gated(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch)
        assert '"${YOLO_STORE_PRUNE_OK:-0}" = "1"' in cmd
        assert '"$MISE_DATA_DIR"/installs/*/*' in cmd
        assert '[ -L "$_p" ] && [ ! -e "$_p" ]' in cmd

    def test_upgrade_status_not_swallowed(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch)
        # Real status captured before the grep/sed filter runs …
        assert "_urc=$?" in cmd
        assert '[ "$_urc" -eq 0 ]' in cmd
        # … and the provisioning pipeline status via bash PIPESTATUS.
        assert "PIPESTATUS" in cmd

    def test_profile_variant_keeps_provision_gate(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch, profile=True)
        assert "tee -a /workspace/.yolo/startup.log" in cmd
        assert "PROVISIONING FAILED (exit %s)" in cmd
        assert "YOLO Jail Profile" in cmd

    @pytest.mark.skipif(shutil.which("bash") is None, reason="bash not available")
    def test_shell_syntax_is_valid(self, tmp_path, monkeypatch):
        cmd = self._final_cmd(tmp_path, monkeypatch)
        script = tmp_path / "final_cmd.sh"
        script.write_text(cmd)
        res = subprocess.run(
            ["bash", "-n", str(script)], capture_output=True, text=True
        )
        assert res.returncode == 0, res.stderr
        # The embedded provisioning body must be valid POSIX sh, too.
        inner_start = cmd.index("sh -c '") + len("sh -c '")
        inner_end = cmd.index("')", inner_start)
        inner = tmp_path / "inner.sh"
        inner.write_text(cmd[inner_start:inner_end])
        res = subprocess.run(["sh", "-n", str(inner)], capture_output=True, text=True)
        assert res.returncode == 0, res.stderr

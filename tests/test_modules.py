"""Tests for src.modules — the host-side module loader."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from src import modules


def _write_manifest(path: Path, data: dict) -> None:
    (path / "manifest.jsonc").write_text(json.dumps(data, indent=2))


@pytest.fixture
def mods_dir(tmp_path: Path) -> Path:
    root = tmp_path / "modules"
    root.mkdir()
    return root


def test_discover_empty_dir_returns_empty(mods_dir: Path):
    assert modules.discover_modules(mods_dir) == []


def test_discover_nonexistent_returns_empty(tmp_path: Path):
    assert modules.discover_modules(tmp_path / "does-not-exist") == []


def test_loads_minimal_manifest(mods_dir: Path):
    mod = mods_dir / "my-mod"
    mod.mkdir()
    _write_manifest(mod, {"name": "my-mod", "description": "test"})

    loaded = modules.discover_modules(mods_dir)
    assert len(loaded) == 1
    assert loaded[0].name == "my-mod"
    assert loaded[0].enabled is True
    assert loaded[0].intercepts == []
    assert loaded[0].ca_cert is None


def test_name_must_match_directory(mods_dir: Path):
    mod = mods_dir / "dir-name"
    mod.mkdir()
    _write_manifest(mod, {"name": "different-name", "description": "x"})
    # validate surfaces the error; discover silently skips.
    assert modules.discover_modules(mods_dir) == []
    entries = modules.validate_modules(mods_dir)
    assert len(entries) == 1
    path, module, err = entries[0]
    assert module is None
    assert err is not None and "disagrees with directory" in err


def test_disabled_skipped_by_default(mods_dir: Path):
    mod = mods_dir / "off"
    mod.mkdir()
    _write_manifest(mod, {"name": "off", "description": "x", "enabled": False})

    assert modules.discover_modules(mods_dir) == []
    included = modules.discover_modules(mods_dir, include_disabled=True)
    assert len(included) == 1 and included[0].name == "off"


def test_docker_args_intercept_and_ca(mods_dir: Path):
    mod = mods_dir / "broker"
    mod.mkdir()
    ca = mod / "ca.crt"
    ca.write_text("-----FAKE CA-----\n")
    _write_manifest(
        mod,
        {
            "name": "broker",
            "description": "x",
            "intercepts": [{"host": "example.test"}, {"host": "api.example.test"}],
            "broker_ip": "10.0.0.1",
            "ca_cert": "ca.crt",
            "jail_env": {"FOO": "bar"},
        },
    )
    loaded = modules.discover_modules(mods_dir)
    args = modules.docker_args_for(loaded)
    assert "--add-host" in args
    # Each intercept emits a --add-host pair.
    assert args.count("--add-host") == 2
    # --add-host uses the custom broker_ip.
    assert "example.test:10.0.0.1" in args
    assert "api.example.test:10.0.0.1" in args
    # CA cert is mounted into the jail under /etc/yolo-jail/modules/<name>/.
    assert any(f"{ca}:/etc/yolo-jail/modules/broker/ca.crt:ro" in a for a in args)
    # NODE_EXTRA_CA_CERTS gets set once with the container paths.
    assert "FOO=bar" in args
    assert any(a.startswith("NODE_EXTRA_CA_CERTS=") for a in args)


def test_docker_args_no_ca_no_env(mods_dir: Path):
    mod = mods_dir / "plain"
    mod.mkdir()
    _write_manifest(
        mod,
        {
            "name": "plain",
            "description": "x",
            "intercepts": [{"host": "plain.test"}],
        },
    )
    args = modules.docker_args_for(modules.discover_modules(mods_dir))
    assert args == ["--add-host", "plain.test:host-gateway"]


def test_multiple_modules_merge_ca_paths(mods_dir: Path):
    for name in ("a", "b"):
        mod = mods_dir / name
        mod.mkdir()
        (mod / "ca.crt").write_text(f"ca-for-{name}")
        _write_manifest(
            mod,
            {"name": name, "description": "x", "ca_cert": "ca.crt"},
        )
    args = modules.docker_args_for(modules.discover_modules(mods_dir))
    node_ca = next(a for a in args if a.startswith("NODE_EXTRA_CA_CERTS="))
    # Both CAs are present, separated by the OS pathsep.
    assert "/etc/yolo-jail/modules/a/ca.crt" in node_ca
    assert "/etc/yolo-jail/modules/b/ca.crt" in node_ca


def test_set_enabled_roundtrip(mods_dir: Path):
    mod = mods_dir / "togg"
    mod.mkdir()
    _write_manifest(
        mod,
        {"name": "togg", "description": "x", "enabled": True},
    )

    modules.set_enabled(mod, False)
    assert modules.discover_modules(mods_dir) == []
    assert len(modules.discover_modules(mods_dir, include_disabled=True)) == 1

    modules.set_enabled(mod, True)
    loaded = modules.discover_modules(mods_dir)
    assert len(loaded) == 1 and loaded[0].enabled is True


def test_invalid_manifest_does_not_break_others(mods_dir: Path):
    good = mods_dir / "good"
    good.mkdir()
    _write_manifest(good, {"name": "good", "description": "x"})
    bad = mods_dir / "bad"
    bad.mkdir()
    (bad / "manifest.jsonc").write_text("{not: json")
    loaded = modules.discover_modules(mods_dir)
    assert [m.name for m in loaded] == ["good"]


def test_hidden_dirs_skipped(mods_dir: Path):
    hidden = mods_dir / ".git"
    hidden.mkdir()
    _write_manifest(hidden, {"name": ".git", "description": "x"})
    assert modules.discover_modules(mods_dir) == []


def test_run_doctor_checks_no_cmd(mods_dir: Path):
    mod = mods_dir / "nocmd"
    mod.mkdir()
    _write_manifest(mod, {"name": "nocmd", "description": "x"})
    results = modules.run_doctor_checks(modules.discover_modules(mods_dir))
    assert len(results) == 1
    assert results[0].returncode is None


def test_run_doctor_checks_success(mods_dir: Path):
    mod = mods_dir / "truecmd"
    mod.mkdir()
    _write_manifest(
        mod,
        {"name": "truecmd", "description": "x", "doctor_cmd": ["true"]},
    )
    results = modules.run_doctor_checks(modules.discover_modules(mods_dir))
    assert results[0].returncode == 0


def test_run_doctor_checks_failure(mods_dir: Path):
    mod = mods_dir / "falsecmd"
    mod.mkdir()
    _write_manifest(
        mod,
        {"name": "falsecmd", "description": "x", "doctor_cmd": ["false"]},
    )
    results = modules.run_doctor_checks(modules.discover_modules(mods_dir))
    assert results[0].returncode == 1


def test_run_doctor_checks_missing_cmd(mods_dir: Path):
    mod = mods_dir / "missing"
    mod.mkdir()
    _write_manifest(
        mod,
        {
            "name": "missing",
            "description": "x",
            "doctor_cmd": ["/no/such/binary/anywhere"],
        },
    )
    results = modules.run_doctor_checks(modules.discover_modules(mods_dir))
    assert results[0].returncode is None
    assert (
        "No such file" in results[0].output or "not found" in results[0].output.lower()
    )

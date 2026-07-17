"""Tests for src/cli/darwin_packages.py — native aarch64-darwin materialization.

Everything except materialize()'s real nix calls is pure; materialize is
tested by monkeypatching subprocess.run (mirrors test_macos_user.py). The only
thing NOT Linux-testable is a real `nix print-dev-env` of a darwin closure
(deferred to a Mac — see docs/handoff-macos-user-revive-plan.md §5).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from types import SimpleNamespace

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

import cli.darwin_packages as dp  # noqa: E402


# ── pure argv builders ───────────────────────────────────────────────────────


def test_print_dev_env_argv_targets_darwin_devshell():
    argv = dp.print_dev_env_argv()
    assert argv[0] == "nix"
    assert "print-dev-env" in argv
    assert "--impure" in argv and "--json" in argv
    assert argv[-1] == ".#devShells.aarch64-darwin.yoloDarwinPackages"
    # experimental features so it works regardless of host nix.conf
    assert "nix-command flakes" in argv


def test_unavailable_eval_argv_targets_skip_list():
    argv = dp.unavailable_eval_argv()
    assert "eval" in argv and "--json" in argv and "--impure" in argv
    assert argv[-1] == ".#darwinUnavailablePackages.aarch64-darwin"


def test_argv_honors_system_override():
    assert dp.print_dev_env_argv("x86_64-darwin")[-1].endswith(
        "devShells.x86_64-darwin.yoloDarwinPackages"
    )


# ── build_env ────────────────────────────────────────────────────────────────


def test_build_env_marshals_packages_json():
    env = dp.build_env(["jq", "ripgrep"])
    assert json.loads(env["YOLO_EXTRA_PACKAGES"]) == ["jq", "ripgrep"]


def test_build_env_omits_var_when_no_packages(monkeypatch):
    monkeypatch.setenv("YOLO_EXTRA_PACKAGES", "leftover")
    env = dp.build_env([])
    assert "YOLO_EXTRA_PACKAGES" not in env  # cleared, not inherited


# ── parse_dev_env ────────────────────────────────────────────────────────────


def test_parse_dev_env_keeps_only_exported_scalars():
    blob = json.dumps(
        {
            "variables": {
                "PATH": {"type": "exported", "value": "/nix/store/a/bin:/usr/bin"},
                "PKG_CONFIG_PATH": {"type": "exported", "value": "/nix/store/a/lib/pkgconfig"},
                "shellHook": {"type": "exported", "value": "echo hi"},
                "some_fn": {"type": "function", "value": "() { :; }"},
                "an_array": {"type": "array", "value": ["x"]},
            }
        }
    )
    out = dp.parse_dev_env(blob)
    assert out["PATH"].startswith("/nix/store/a/bin")
    assert out["PKG_CONFIG_PATH"] == "/nix/store/a/lib/pkgconfig"
    assert "shellHook" in out  # exported scalar kept at parse time
    assert "some_fn" not in out and "an_array" not in out  # non-scalars dropped


# ── split_env: store-only PATH + strict whitelist (the pollution guard) ──────


def test_split_env_keeps_only_store_path_dirs():
    dev = {"PATH": "/nix/store/a-jq/bin:/usr/bin:/nix/store/b-rg/bin:/bin"}
    prefix, extra = dp.split_env(dev)
    assert prefix == ["/nix/store/a-jq/bin", "/nix/store/b-rg/bin"]  # host dirs dropped
    assert extra == {}


def test_split_env_whitelist_blocks_build_env_pollution():
    # print-dev-env dumps the whole stdenv; only the whitelist may pass —
    # out/TMPDIR/shellHook/stdenv etc. must NOT leak into the sandbox env.
    dev = {
        "PATH": "/nix/store/a/bin",
        "PKG_CONFIG_PATH": "/nix/store/a/lib/pkgconfig",
        "out": "/nix/store/zzz",
        "TMPDIR": "/tmp/nix-build",
        "shellHook": "echo hi",
        "stdenv": "/nix/store/std",
        "SOURCE_DATE_EPOCH": "1",
    }
    _, extra = dp.split_env(dev)
    assert extra == {"PKG_CONFIG_PATH": "/nix/store/a/lib/pkgconfig"}
    for polluter in ("out", "TMPDIR", "shellHook", "stdenv", "SOURCE_DATE_EPOCH"):
        assert polluter not in extra


def test_split_env_no_path_is_empty_prefix():
    assert dp.split_env({}) == ([], {})


# ── locked_nixpkgs_rev ───────────────────────────────────────────────────────


def test_locked_nixpkgs_rev_reads_flake_lock(tmp_path):
    lock = tmp_path / "flake.lock"
    lock.write_text(
        json.dumps({"nodes": {"nixpkgs": {"locked": {"rev": "deadbeef123"}}}})
    )
    assert dp.locked_nixpkgs_rev(lock) == "deadbeef123"


def test_locked_nixpkgs_rev_matches_repo_lock():
    # Sanity: the real repo lock parses and yields a 40-char sha.
    rev = dp.locked_nixpkgs_rev(REPO_ROOT / "flake.lock")
    assert len(rev) == 40 and all(c in "0123456789abcdef" for c in rev)


# ── materialize (subprocess mocked) ──────────────────────────────────────────


def _dev_env_json(path="/nix/store/a-jq/bin:/usr/bin", **extra):
    variables = {"PATH": {"type": "exported", "value": path}}
    for k, v in extra.items():
        variables[k] = {"type": "exported", "value": v}
    return json.dumps({"variables": variables})


def test_materialize_success(monkeypatch):
    calls = []

    def fake_run(argv, **kw):
        calls.append(argv)
        if "eval" in argv:  # skip-list read
            return SimpleNamespace(returncode=0, stdout='["nolinux"]', stderr="")
        return SimpleNamespace(
            returncode=0,
            stdout=_dev_env_json(PKG_CONFIG_PATH="/nix/store/a-jq/lib/pkgconfig"),
            stderr="",
        )

    monkeypatch.setattr(dp.subprocess, "run", fake_run)
    result = dp.materialize(Path("/repo"), ["jq"])
    assert result.path_prefix == ["/nix/store/a-jq/bin"]
    assert result.env == {"PKG_CONFIG_PATH": "/nix/store/a-jq/lib/pkgconfig"}
    assert result.skipped == ["nolinux"]
    # both nix invocations happened, cwd threaded
    assert any("print-dev-env" in c for c in calls)
    assert any("eval" in c for c in calls)


def test_materialize_raises_when_nix_missing(monkeypatch):
    def boom(argv, **kw):
        if "eval" in argv:
            raise FileNotFoundError("nix")
        raise FileNotFoundError("nix")

    monkeypatch.setattr(dp.subprocess, "run", boom)
    try:
        dp.materialize(Path("/repo"), ["jq"])
        assert False, "expected DarwinPackagesError"
    except dp.DarwinPackagesError as e:
        assert "not found" in str(e)


def test_materialize_raises_on_build_failure(monkeypatch):
    def fake_run(argv, **kw):
        if "eval" in argv:
            return SimpleNamespace(returncode=0, stdout="[]", stderr="")
        return SimpleNamespace(
            returncode=1, stdout="", stderr="error: build of jq failed"
        )

    monkeypatch.setattr(dp.subprocess, "run", fake_run)
    try:
        dp.materialize(Path("/repo"), ["jq"])
        assert False, "expected DarwinPackagesError"
    except dp.DarwinPackagesError as e:
        assert "build of jq failed" in str(e)


def test_materialize_skip_list_failure_is_nonfatal(monkeypatch):
    # A failed skip-list read must not abort — just yields no skipped names.
    def fake_run(argv, **kw):
        if "eval" in argv:
            return SimpleNamespace(returncode=1, stdout="", stderr="eval boom")
        return SimpleNamespace(returncode=0, stdout=_dev_env_json(), stderr="")

    monkeypatch.setattr(dp.subprocess, "run", fake_run)
    result = dp.materialize(Path("/repo"), ["jq"])
    assert result.skipped == []
    assert result.path_prefix == ["/nix/store/a-jq/bin"]

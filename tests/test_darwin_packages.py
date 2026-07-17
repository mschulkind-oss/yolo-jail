"""Tests for src/cli/darwin_packages.py — native aarch64-darwin materialization.

Everything except materialize()'s real nix calls is pure; materialize is
tested by monkeypatching subprocess.run (mirrors test_macos_user.py). The only
thing NOT Linux-testable is a real `nix print-dev-env` of a darwin closure
(deferred to a Mac — see docs/implementation/handoff-macos-user-revive-plan.md §5).
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


def test_build_profile_argv_targets_darwin_buildenv():
    argv = dp.build_profile_argv()
    assert argv[0] == "nix"
    assert "build" in argv
    assert "--impure" in argv and "--print-out-paths" in argv and "--no-link" in argv
    assert argv[-1] == ".#packages.aarch64-darwin.yoloDarwinPackages"
    # experimental features so it works regardless of host nix.conf
    assert "nix-command flakes" in argv


def test_unavailable_eval_argv_targets_skip_list():
    argv = dp.unavailable_eval_argv()
    assert "eval" in argv and "--json" in argv and "--impure" in argv
    assert argv[-1] == ".#darwinUnavailablePackages.aarch64-darwin"


def test_argv_honors_system_override():
    assert dp.build_profile_argv("x86_64-darwin")[-1].endswith(
        "packages.x86_64-darwin.yoloDarwinPackages"
    )


# ── build_env ────────────────────────────────────────────────────────────────


def test_build_env_marshals_packages_json():
    env = dp.build_env(["jq", "ripgrep"])
    assert json.loads(env["YOLO_EXTRA_PACKAGES"]) == ["jq", "ripgrep"]


def test_build_env_omits_var_when_no_packages(monkeypatch):
    monkeypatch.setenv("YOLO_EXTRA_PACKAGES", "leftover")
    env = dp.build_env([])
    assert "YOLO_EXTRA_PACKAGES" not in env  # cleared, not inherited


# ── profile_paths: EXACTLY the declared packages, no stdenv toolchain ────────


def test_profile_paths_is_only_the_out_bin(tmp_path):
    # The buildEnv profile's single bin — NOT a scraped devShell PATH — so the
    # sandbox gets exactly the declared packages, never the GNU stdenv
    # toolchain (the confirmed high-sev bug this rewrite fixes).
    out = tmp_path / "nix" / "store" / "abc-yolo-darwin-packages"
    (out / "bin").mkdir(parents=True)
    prefix, env = dp.profile_paths(str(out))
    assert prefix == [f"{out}/bin"]  # one dir, the profile bin
    assert env == {}  # no pkgconfig dir → no PKG_CONFIG_PATH


def test_profile_paths_exposes_pkgconfig_when_present(tmp_path):
    out = tmp_path / "store" / "abc-prof"
    (out / "bin").mkdir(parents=True)
    (out / "lib" / "pkgconfig").mkdir(parents=True)
    prefix, env = dp.profile_paths(str(out))
    assert prefix == [f"{out}/bin"]
    assert env == {"PKG_CONFIG_PATH": f"{out}/lib/pkgconfig"}


def test_profile_paths_empty_out_is_empty():
    assert dp.profile_paths("") == ([], {})
    assert dp.profile_paths("  \n") == ([], {})


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

# A realized buildEnv store out path (its /bin has ONLY the declared packages).
_OUT = "/nix/store/abc-yolo-darwin-packages"


class _FakeProc:
    """Minimal Popen stand-in: streams stderr lines, returns stdout via
    communicate(), reports returncode.  materialize now uses Popen (to stream
    build progress live), so the build step is mocked here, not subprocess.run.
    """

    def __init__(self, stdout="", stderr="", returncode=0):
        import io

        self._stdout = stdout
        self.stderr = io.StringIO(stderr)
        self.returncode = returncode

    def communicate(self):
        return self._stdout, ""


def _mock_build(
    monkeypatch, *, stdout="", stderr="", returncode=0, skip="[]", raise_exc=None
):
    """Mock the eval skip-list (subprocess.run) + the build (subprocess.Popen)."""
    monkeypatch.setattr(
        dp.subprocess,
        "run",
        lambda argv, **kw: SimpleNamespace(returncode=0, stdout=skip, stderr=""),
    )

    def fake_popen(argv, **kw):
        if raise_exc:
            raise raise_exc
        return _FakeProc(stdout=stdout, stderr=stderr, returncode=returncode)

    monkeypatch.setattr(dp.subprocess, "Popen", fake_popen)


def test_materialize_success(monkeypatch):
    _mock_build(monkeypatch, stdout=_OUT + "\n", skip='["nolinux"]')
    # No pkgconfig dir on the fake out path → env stays empty.
    result = dp.materialize(Path("/repo"), ["jq"])
    assert result.path_prefix == [f"{_OUT}/bin"]  # exactly the profile bin
    assert result.skipped == ["nolinux"]


def test_materialize_raises_when_nix_missing(monkeypatch):
    _mock_build(monkeypatch, raise_exc=FileNotFoundError("nix"))
    try:
        dp.materialize(Path("/repo"), ["jq"])
        assert False, "expected DarwinPackagesError"
    except dp.DarwinPackagesError as e:
        assert "not found" in str(e)


def test_materialize_raises_on_build_failure(monkeypatch):
    _mock_build(monkeypatch, returncode=1, stderr="error: build of jq failed\n")
    try:
        dp.materialize(Path("/repo"), ["jq"])
        assert False, "expected DarwinPackagesError"
    except dp.DarwinPackagesError as e:
        assert "build of jq failed" in str(e)


def test_materialize_raises_on_empty_out(monkeypatch):
    _mock_build(monkeypatch, stdout="\n")
    try:
        dp.materialize(Path("/repo"), ["jq"])
        assert False, "expected DarwinPackagesError"
    except dp.DarwinPackagesError as e:
        assert "no store path" in str(e)


def test_materialize_skip_list_failure_is_nonfatal(monkeypatch):
    # A failed skip-list read must not abort — just yields no skipped names.
    monkeypatch.setattr(
        dp.subprocess,
        "run",
        lambda argv, **kw: SimpleNamespace(returncode=1, stdout="", stderr="eval boom"),
    )
    monkeypatch.setattr(
        dp.subprocess, "Popen", lambda argv, **kw: _FakeProc(stdout=_OUT + "\n")
    )
    result = dp.materialize(Path("/repo"), ["jq"])
    assert result.skipped == []
    assert result.path_prefix == [f"{_OUT}/bin"]

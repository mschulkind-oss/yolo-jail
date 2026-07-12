"""Tests for the yolo-doctor formatting helpers.

Exercise the splitter that turns a broker self-check blob into one
(title, detail) pair per problem.  Rendering is visual so we test the
structure, not the byte-exact output.
"""

from __future__ import annotations

from src.cli import _finalize_problem, _split_self_check_problems


def test_split_empty_output_returns_empty():
    assert _split_self_check_problems("") == []


def test_split_single_problem_with_continuation():
    blob = "FAIL: thing is broken.\nFix: do X.\nSee docs.\n"
    problems = _split_self_check_problems(blob)
    assert len(problems) == 1
    title, detail = problems[0]
    assert title == "thing is broken."
    assert detail == "Fix: do X.\nSee docs."


def test_split_two_problems_each_with_continuation():
    blob = (
        "FAIL: first problem.\n"
        "hint for first.\n"
        "FAIL: second problem.\n"
        "hint for second.\n"
        "extra line for second.\n"
    )
    problems = _split_self_check_problems(blob)
    assert len(problems) == 2
    assert problems[0] == ("first problem.", "hint for first.")
    assert problems[1] == (
        "second problem.",
        "hint for second.\nextra line for second.",
    )


def test_split_ignores_preamble_before_first_fail():
    blob = "Some preamble that is not a problem.\nFAIL: the real problem.\n"
    problems = _split_self_check_problems(blob)
    assert problems == [("the real problem.", "")]


def test_split_strips_blank_continuation_lines():
    blob = "FAIL: x.\n\n  \nhint.\n\n"
    problems = _split_self_check_problems(blob)
    assert problems == [("x.", "hint.")]


def test_finalize_problem_single_line():
    assert _finalize_problem(["only title"]) == ("only title", "")


# --- nix build failure diagnosis (opaque "1 dependency failed" -> guidance) ---

from src.cli.check_cmd import _diagnose_nix_build_failure  # noqa: E402
import src.cli.check_cmd as _cc  # noqa: E402


def test_diagnose_explicit_cross_build_leads_to_colima(monkeypatch):
    monkeypatch.setattr(_cc, "IS_MACOS", True)
    tail = [
        "error: a 'aarch64-linux' with features {} is required to build "
        "'/nix/store/x.drv', but I am a 'aarch64-darwin'"
    ]
    title, note = _diagnose_nix_build_failure(tail)
    assert "Linux builder" in title
    # nix-darwin linux-builder is the recommended remedy (not Colima).
    assert "linux-builder" in note.lower()


def test_diagnose_ambiguous_mac_dependency_failure(monkeypatch):
    monkeypatch.setattr(_cc, "IS_MACOS", True)
    title, note = _diagnose_nix_build_failure(
        ["error: Build failed due to failed dependency", "1 dependency failed"]
    )
    assert "Linux builder or a cached package" in title
    assert "linux-builder" in note.lower()
    # names the custom-package cause too
    assert "override" in note.lower()


def test_diagnose_unrelated_failure_falls_through(monkeypatch):
    monkeypatch.setattr(_cc, "IS_MACOS", True)
    title, note = _diagnose_nix_build_failure(["error: attribute 'foo' missing"])
    assert title == "nix build failed"
    assert "foo" in note


def test_diagnose_non_mac_dependency_failure_is_not_builder(monkeypatch):
    # On Linux a dependency failure isn't a "needs a builder" situation.
    monkeypatch.setattr(_cc, "IS_MACOS", False)
    title, _ = _diagnose_nix_build_failure(["1 dependency failed"])
    assert title == "nix build failed"


# --- nix --dry-run "will build?" detection ---------------------------------

from pathlib import Path  # noqa: E402
from types import SimpleNamespace  # noqa: E402


def _fake_run(stdout="", stderr="", returncode=0):
    return lambda *a, **k: SimpleNamespace(
        stdout=stdout, stderr=stderr, returncode=returncode
    )


_DRY_BUILD = (
    "these 2 derivations will be built:\n"
    "  /nix/store/aaa-yolo-jail-conf.json.drv\n"
    "  /nix/store/bbb-stream-yolo-jail.drv\n"
    "these 40 paths will be fetched (12 MB):\n"
    "  /nix/store/ccc-bash\n"
)
_DRY_FETCH = "these 40 paths will be fetched (300 MB download):\n  /nix/store/c-bash\n"


def test_dry_run_detects_build(monkeypatch):
    monkeypatch.setattr(_cc.subprocess, "run", _fake_run(stderr=_DRY_BUILD))
    will, drvs = _cc._nix_dry_run_will_build(Path("/repo"), None)
    assert will is True
    assert "aaa-yolo-jail-conf.json.drv" in drvs


def test_dry_run_fetch_only_is_not_build(monkeypatch):
    monkeypatch.setattr(_cc.subprocess, "run", _fake_run(stderr=_DRY_FETCH))
    will, drvs = _cc._nix_dry_run_will_build(Path("/repo"), None)
    assert will is False
    assert drvs == []


def test_dry_run_offline_is_inconclusive(monkeypatch):
    # Non-zero exit with a network error (no plan) -> None, never a false miss.
    monkeypatch.setattr(
        _cc.subprocess,
        "run",
        _fake_run(
            stderr="error: unable to download 'https://cache.nixos.org'", returncode=1
        ),
    )
    will, _ = _cc._nix_dry_run_will_build(Path("/repo"), None)
    assert will is None


def test_dry_run_subprocess_error_is_inconclusive(monkeypatch):
    def boom(*a, **k):
        raise OSError("nix missing")

    monkeypatch.setattr(_cc.subprocess, "run", boom)
    will, _ = _cc._nix_dry_run_will_build(Path("/repo"), None)
    assert will is None


def test_preflight_state_a_quiet_when_all_cached(monkeypatch):
    monkeypatch.setattr(_cc, "_nix_dry_run_will_build", lambda *a: (False, []))
    msgs = []
    monkeypatch.setattr(_cc.console, "print", lambda m, *a, **k: msgs.append(str(m)))
    warned = []
    _cc._preflight_builder_needs(
        Path("/repo"),
        None,
        ok=lambda m: None,
        warn=lambda m, n="": warned.append(m),
        fail=lambda m, n="": None,
    )
    assert warned == []  # no warning in the common case
    assert any("binary cache" in m for m in msgs)


def test_preflight_state_c_fails_and_skips_build_without_builder(monkeypatch):
    monkeypatch.setattr(
        _cc, "_nix_dry_run_will_build", lambda *a: (True, ["yolo-jail-conf.json.drv"])
    )
    monkeypatch.setattr(_cc, "_has_linux_builder", lambda: False)
    failed = []
    result = _cc._preflight_builder_needs(
        Path("/repo"),
        None,
        ok=lambda m: None,
        warn=lambda m, n="": None,
        fail=lambda m, n="": failed.append((m, n)),
    )
    # ONE actionable FAIL (not a WARN+FAIL pair) and the caller is told to
    # skip the doomed build.
    assert result is False
    assert failed and "Linux builder" in failed[0][0]
    assert "linux-builder" in failed[0][1].lower()  # nix-darwin remedy, not colima


def test_preflight_state_b_pass_with_builder(monkeypatch):
    monkeypatch.setattr(_cc, "_nix_dry_run_will_build", lambda *a: (True, ["x.drv"]))
    monkeypatch.setattr(_cc, "_has_linux_builder", lambda: True)
    passed = []
    result = _cc._preflight_builder_needs(
        Path("/repo"),
        None,
        ok=lambda m: passed.append(m),
        warn=lambda m, n="": None,
        fail=lambda m, n="": None,
    )
    assert result is True
    assert passed and "built from source" in passed[0]


def test_preflight_state_a_returns_true(monkeypatch):
    monkeypatch.setattr(_cc, "_nix_dry_run_will_build", lambda *a: (False, []))
    monkeypatch.setattr(_cc.console, "print", lambda *a, **k: None)
    assert (
        _cc._preflight_builder_needs(
            Path("/repo"),
            None,
            ok=lambda m: None,
            warn=lambda m, n="": None,
            fail=lambda m, n="": None,
        )
        is True
    )

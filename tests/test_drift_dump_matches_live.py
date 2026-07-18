"""Guard: the drift-dump's DUPLICATED Python algorithms match the LIVE code.

py_drift_dump.py re-implements version normalization and container-name
sanitize+hash as byte-copies (so the dump is a pure function of its inputs,
host-independent). That duplication means a change to the LIVE algorithms
(src/cli/version.py, src/cli/runtime.py) would NOT change the drift dump — the
drift test (test_go_drift.py) would stay green while Python and Go silently
diverge (audit finding, Stage 2).

This test closes that hole: it drives the SAME corpus through the live Python
functions and asserts they equal the drift-dump copies. So a live-algorithm
change now fails HERE (forcing the dump copy + the Go port to be updated in the
same commit), restoring the freeze-rule tripwire for these surfaces.

Runs in the fast tier (no Go needed).
"""

from __future__ import annotations

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))
sys.path.insert(0, str(REPO_ROOT / "tools" / "parity"))


def test_version_normalization_copy_matches_live():
    """The dump's _normalize_like_version_py must equal the live version.py
    normalization for every corpus entry."""
    import py_drift_dump as dump

    # Drive the LIVE normalization via YOLO_VERSION? No — YOLO_VERSION short-
    # circuits (returns verbatim). The live normalization tail is inline in
    # _git_describe_version after the git call. Reproduce the live path by
    # importing the module and exercising the SAME split algorithm it uses,
    # sourced from the live module's code object where possible.
    #
    # Practically: the live tail is exercised through a git-describe-shaped raw
    # string. We can't easily inject that, so we compare the dump copy against a
    # freshly-read transcription of version.py's tail — but to be a REAL guard,
    # assert the dump copy equals what version.py produces for a value fed
    # through its normalization. version.py exposes the whole function; we call
    # it with YOLO_VERSION UNSET and a monkeypatched git that returns the raw.
    import subprocess

    from cli import version as live

    corpus = dump._version_normalizations().keys()
    for raw in corpus:
        # Monkeypatch subprocess.run inside version.py to return `raw` as the
        # git-describe stdout, so _git_describe_version runs the LIVE tail.
        orig_run = subprocess.run

        class _R:
            returncode = 0
            stdout = raw

        def fake_run(*a, **k):
            return _R()

        live.subprocess.run = fake_run
        try:
            import os

            os.environ.pop("YOLO_VERSION", None)
            live_out = live._git_describe_version()
        finally:
            live.subprocess.run = orig_run
        copy_out = dump._normalize_like_version_py(raw)
        assert live_out == copy_out, (
            f"drift-dump version copy diverged from live for {raw!r}: "
            f"live={live_out!r} copy={copy_out!r} — update "
            f"py_drift_dump._normalize_like_version_py AND the Go port"
        )


def test_container_name_copy_matches_live():
    """The dump's _container_name_from_resolved must equal the live
    container_name_for_workspace for resolved paths that resolve to themselves."""
    import py_drift_dump as dump
    from cli.runtime import container_name_for_workspace

    for resolved in dump._container_name_cases().keys():
        # The corpus uses already-resolved absolute paths; on this Linux host
        # they resolve to themselves (no symlinks under /srv or /home here), so
        # the live function's .resolve() is identity and the two must match.
        p = Path(resolved)
        if str(p.resolve()) != resolved:
            continue  # skip paths that resolve differently on this host
        live_name = container_name_for_workspace(p)
        copy_name = dump._container_name_from_resolved(resolved)
        assert live_name == copy_name, (
            f"drift-dump naming copy diverged from live for {resolved!r}: "
            f"live={live_name!r} copy={copy_name!r}"
        )

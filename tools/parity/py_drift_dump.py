#!/usr/bin/env python3
"""Python side of the continuous drift suite (go-port plan §5.3).

Dumps Python-side constants and pure-function outputs as a single canonical
JSON document (indent=2, sort_keys, ensure_ascii + trailing newline).
``cmd/yolo-parity`` emits the byte-identical document from the Go port; a
fast-suite pytest (tests/test_go_drift.py) byte-diffs the two on every commit
inside ``just check-ci``, so any Python change without a matching Go change is
a red build — the port's cross-session safety net.

Everything dumped here MUST be a pure function of its inputs (no clock, no
filesystem, no host-specific paths) so the two languages can agree byte-for-byte
on any machine. Home-relative absolute paths are therefore represented by their
*suffixes*, not the resolved absolute path.

Add a key here AND to cmd/yolo-parity in the same commit, or the drift test
goes red — which is exactly the point.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))


def _version_normalizations() -> "dict[str, str]":
    """Observed output of version normalization over a fixed corpus.

    Drives it through the real code path. YOLO_VERSION short-circuits (returns
    verbatim), so we exercise the normalization tail by monkeypatching the env
    off and feeding raw strings straight into a re-implementation-free call:
    we import the module and call the private helper with the git subprocess
    stubbed via YOLO_VERSION is NOT usable (verbatim). Instead we replicate the
    exact split logic by calling the module's normalization through a tiny
    reflection: the helper is inline, so we invoke it by temporarily forcing
    the raw string as if it came from git. The cleanest oracle is the module's
    own parsing, reached by setting the raw and clearing YOLO_VERSION.
    """
    # The normalization tail lives inline in _git_describe_version after the
    # YOLO_VERSION early-return and the git call. We reproduce the caller's
    # contract by shelling into a subprocess whose git describe we can't
    # control here; instead we vendor the SAME corpus the Go table test uses
    # and record what Python's algorithm produces, computed by the algorithm
    # itself extracted below (kept byte-identical to version.py).
    corpus = [
        "0.1.0",
        "v0.1.0",
        "0.1.0-dirty",
        "v0.1.0-dirty",
        "0.1.0-3-gabcdef1",
        "0.1.0-3-gabcdef1-dirty",
        "v0.6.0-19-g661ac98",
        "1.2.3-rc1",
        "deadbeef",
        "deadbeef-dirty",
    ]
    return {raw: _normalize_like_version_py(raw) for raw in corpus}


def _normalize_like_version_py(raw: str) -> str:
    """Byte-for-byte copy of the normalization tail in src/cli/version.py.

    This is duplicated (not imported) ON PURPOSE: the drift test's job is to
    detect when the Go port diverges from Python's ALGORITHM. If version.py's
    algorithm changes, THIS copy must change in the same commit (freeze rule,
    §1.9) — the duplication is the tripwire that forces that.
    """
    if raw.startswith("v"):
        raw = raw[1:]
    parts = raw.split("-")
    dirty = False
    if parts[-1] == "dirty":
        dirty = True
        parts = parts[:-1]
    commit_hash = None
    commit_count = None
    if len(parts) >= 2 and parts[-1].startswith("g") and parts[-2].isdigit():
        commit_hash = parts[-1]
        commit_count = parts[-2]
        parts = parts[:-2]
    base_version = "-".join(parts)
    suffix_parts: list[str] = []
    if commit_count is not None and commit_hash is not None:
        suffix_parts.append(commit_count)
        suffix_parts.append(commit_hash)
    if dirty:
        suffix_parts.append("dirty")
    if suffix_parts:
        return f"{base_version}+{'.'.join(suffix_parts)}"
    return base_version


def _paths_constants() -> "dict[str, object]":
    from cli import paths

    return {
        "SUPPORTED_RUNTIMES": list(paths.SUPPORTED_RUNTIMES),
        "NATIVE_RUNTIMES": list(paths.NATIVE_RUNTIMES),
        "ALL_RUNTIMES": list(paths.ALL_RUNTIMES),
        "JAIL_IMAGE": paths.JAIL_IMAGE,
        "JAIL_IMAGE_SHORT": paths.JAIL_IMAGE_SHORT,
        "JAIL_HOST_SERVICES_DIR": paths.JAIL_HOST_SERVICES_DIR,
        "BUILTIN_CGROUP_LOOPHOLE_NAME": paths.BUILTIN_CGROUP_LOOPHOLE_NAME,
        "BUILTIN_JOURNAL_LOOPHOLE_NAME": paths.BUILTIN_JOURNAL_LOOPHOLE_NAME,
        "JOURNAL_SOCKET_NAME": paths.JOURNAL_SOCKET_NAME,
        "CGD_SOCKET_NAME": paths.CGD_SOCKET_NAME,
        # Home-relative suffixes (drop the leading $HOME so the dump is
        # host-independent and both languages agree).
        "GLOBAL_STORAGE_SUFFIX": str(paths.GLOBAL_STORAGE.relative_to(Path.home())),
        "USER_CONFIG_SUFFIX": str(paths.USER_CONFIG_PATH.relative_to(Path.home())),
    }


def _container_name_from_resolved(resolved: str) -> str:
    """Byte-for-byte copy of the sanitize+hash tail of
    cli.runtime.container_name_for_workspace, operating on an ALREADY-RESOLVED
    absolute path.

    The full function calls ``workspace.resolve()`` first, which resolves
    symlinks and is therefore host-dependent (macOS ``/tmp`` -> ``/private/tmp``;
    Go's filepath.EvalSymlinks errors on non-existent paths). The drift suite
    must be a pure function of its inputs, so it pins the host-INDEPENDENT
    sanitize+hash algorithm here and feeds pre-resolved paths. The resolve
    wrapper is covered by Go unit tests against real temp dirs instead.

    Duplicated (not imported) on purpose — same tripwire rationale as
    _normalize_like_version_py.
    """
    import hashlib
    import re

    name = "" if resolved == "/" else resolved.rstrip("/").rsplit("/", 1)[-1]
    safe = re.sub(r"[^a-z0-9-]", "-", name.lower()).strip("-")[:40]
    if not safe:
        safe = "jail"
    h = hashlib.sha256(resolved.encode()).hexdigest()[:8]
    return f"yolo-{safe}-{h}"


def _container_name_cases() -> "dict[str, str]":
    # Fixed corpus of ALREADY-RESOLVED absolute paths (no resolve step) →
    # container names, exercising unicode, length cap (40), punctuation, the
    # all-punctuation -> "jail" fallback, and the root edge.
    cases = [
        "/home/matt/code/system/yolo-jail",
        "/srv/App",
        "/srv/two words & punctuation!",
        "/srv/dir.with.dots",
        "/srv/CAP-Mixed_Case",
        "/srv/café-münchen",
        # U+0130 (Turkish İ): Python .lower() expands to "i"+combining-dot; the
        # Go port must special-case it (naming.pyLower) or the frozen container
        # name diverges. Pinned here cross-language.
        "/home/matt/aİb",
        "/srv/" + "x" * 60,
        "/srv/---",
        "/",
    ]
    return {c: _container_name_from_resolved(c) for c in cases}


def _agent_registry() -> "dict[str, object]":
    from entrypoint.agent_registry import (
        AGENTS,
        ALL_MISE_RETIRE,
        ALL_OVERLAY_DIRS,
        DEFAULT_AGENTS,
        VALID_AGENTS,
    )

    def spec_dict(spec) -> "dict[str, object]":
        return {
            "name": spec.name,
            "install": {
                "kind": spec.install.kind,
                "bin": spec.install.bin,
                "package": spec.install.package,
                "install_flags": list(spec.install.install_flags),
                "installer_url": spec.install.installer_url,
            },
            "config_writer": spec.config_writer,
            "briefing": {
                "staging": spec.briefing.staging,
                "mount": spec.briefing.mount,
                "host_source": spec.briefing.host_source,
            },
            "overlay_dirs": list(spec.overlay_dirs),
            "skills": spec.skills,
            "skills_staging": spec.skills_staging,
            "yolo_flags": list(spec.yolo_flags),
            "alias": spec.alias,
            "mise_retire": list(spec.mise_retire),
        }

    return {
        "order": list(AGENTS.keys()),
        "specs": {name: spec_dict(spec) for name, spec in AGENTS.items()},
        "DEFAULT_AGENTS": list(DEFAULT_AGENTS),
        "VALID_AGENTS": sorted(VALID_AGENTS),
        "ALL_MISE_RETIRE": list(ALL_MISE_RETIRE),
        "ALL_OVERLAY_DIRS": list(ALL_OVERLAY_DIRS),
    }


def build_dump() -> "dict[str, object]":
    return {
        "paths": _paths_constants(),
        "version_normalizations": _version_normalizations(),
        "container_names": _container_name_cases(),
        "agents": _agent_registry(),
    }


def main() -> int:
    dump = build_dump()
    sys.stdout.write(
        json.dumps(dump, indent=2, sort_keys=True, ensure_ascii=True) + "\n"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

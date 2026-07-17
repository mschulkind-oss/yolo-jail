"""Native aarch64-darwin materialization of ``packages:`` for the macos-user backend.

The macos-user backend runs the agent as a native macOS user under Seatbelt —
no VM, no Linux image.  Its ``packages:`` must therefore resolve to *darwin*
builds.  The flake exposes ``packages.aarch64-darwin.yoloDarwinPackages`` (a
``buildEnv`` profile whose single ``/bin`` is EXACTLY the darwin build of
``packages:``, driven by the same ``YOLO_EXTRA_PACKAGES`` env contract the
image path uses) and ``darwinUnavailablePackages`` (the names filtered out for
having no darwin build — warn-and-skip).

Why a buildEnv, not a devShell: a devShell's ``print-dev-env`` dumps the WHOLE
stdenv build environment — the clang cc-wrapper, GNU coreutils/sed/grep/awk,
make, cctools ld, … — all under ``/nix/store``.  Scraping its PATH would put
that entire GNU toolchain on the sandbox agent's PATH ahead of the macOS BSD
userland (and strip the ``NIX_*`` env the cc-wrapper needs).  A ``buildEnv``
contains ONLY the declared packages, so ``<out>/bin`` is exactly what the user
asked for.

This module's job on the host (which has nix; the sandbox does not) is:
  1. read the skipped-package list (``nix eval``);
  2. realize the buildEnv profile (``nix build --print-out-paths``);
  3. hand ``<out>/bin`` (and, if present, ``<out>/lib/pkgconfig``) to the caller.

That single store bin dir is threaded into the sandbox agent's PATH via
``macos_user.sandbox_path(prefix=…)`` — the store is world-readable and the
build ran on the host user, so nothing privileged crosses into the sandbox.

Pinning: none here.  Because these are flake OUTPUTS, ``nix`` resolves
``nixpkgs.legacyPackages.aarch64-darwin`` against this repo's own ``flake.lock``
— the SAME locked rev the aarch64-linux image uses, just the darwin system.
``locked_nixpkgs_rev`` exists only for diagnostics / dry-run display.

Everything except ``materialize``'s actual nix calls is a pure function, so the
module is unit-testable on Linux by mocking ``subprocess.run``.
"""

from __future__ import annotations

import json
import os
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List

DARWIN_SYSTEM = "aarch64-darwin"
PROFILE_ATTR = "yoloDarwinPackages"  # packages.<system>.yoloDarwinPackages (buildEnv)
UNAVAILABLE_ATTR = "darwinUnavailablePackages"  # <attr>.<system> -> [str]


class DarwinPackagesError(RuntimeError):
    """A native darwin package build/eval failed unrecoverably.

    The caller (run_macos_user) turns this into an actionable message pointing
    at the Apple Container fallback, and aborts the run — a missing tool is
    better than a silently-incomplete environment.
    """


@dataclass
class DarwinPackages:
    """Result of materializing ``packages:`` natively on darwin."""

    path_prefix: List[str] = field(default_factory=list)  # /nix/store/*/bin dirs
    env: Dict[str, str] = field(default_factory=dict)  # whitelisted non-PATH vars
    skipped: List[str] = field(default_factory=list)  # names with no darwin build


def _nix_flags() -> List[str]:
    """Experimental-features flags so the CLI works regardless of the host's
    nix.conf (mirrors how the image build invokes nix)."""
    return ["--extra-experimental-features", "nix-command flakes"]


def build_env(packages) -> Dict[str, str]:
    """Env for the nix invocations: the parent env plus the packages contract.

    Mirrors ``image._build_image_store_path`` — the flake reads
    ``YOLO_EXTRA_PACKAGES`` via ``builtins.getEnv`` (hence ``--impure``).  Pure.
    """
    env = os.environ.copy()
    if packages:
        env["YOLO_EXTRA_PACKAGES"] = json.dumps(packages)
    else:
        env.pop("YOLO_EXTRA_PACKAGES", None)
    return env


def build_profile_argv(system: str = DARWIN_SYSTEM) -> List[str]:
    """argv to realize the darwin buildEnv profile + print its store out path.

    ``--print-out-paths`` emits the realized ``/nix/store/<hash>-...`` path on
    stdout; the profile's ``bin`` is ``<out>/bin`` — exactly the declared
    packages, no stdenv toolchain.  ``--no-link`` avoids a result symlink.  Pure.
    """
    return [
        "nix",
        *_nix_flags(),
        "build",
        "--impure",
        "--no-link",
        "--print-out-paths",
        "--print-build-logs",  # stream real build progress (not silent)
        f".#packages.{system}.{PROFILE_ATTR}",
    ]


def unavailable_eval_argv(system: str = DARWIN_SYSTEM) -> List[str]:
    """argv to read the no-darwin-build skip list as JSON. Pure."""
    return [
        "nix",
        *_nix_flags(),
        "eval",
        "--impure",
        "--json",
        f".#{UNAVAILABLE_ATTR}.{system}",
    ]


def profile_paths(out_path: str) -> "tuple[List[str], Dict[str, str]]":
    """From the buildEnv store out path, derive (PATH prefix, non-PATH env). Pure.

    The profile is a single store dir; its ``bin`` is the ONLY PATH entry we
    contribute (so the sandbox agent gets exactly the declared packages, never
    the stdenv toolchain).  If ``lib/pkgconfig`` exists, expose PKG_CONFIG_PATH
    so pkg-config-based builds inside the sandbox can find the packages.
    """
    out = out_path.strip()
    if not out:
        return [], {}
    path_prefix = [f"{out}/bin"]
    env: Dict[str, str] = {}
    pc = Path(out) / "lib" / "pkgconfig"
    # The dir exists only for library packages; check without failing if absent.
    try:
        if pc.is_dir():
            env["PKG_CONFIG_PATH"] = str(pc)
    except OSError:
        pass
    return path_prefix, env


def locked_nixpkgs_rev(flake_lock: Path) -> str:
    """The pinned nixpkgs rev from flake.lock (diagnostics/dry-run only). Pure."""
    data = json.loads(Path(flake_lock).read_text())
    return data["nodes"]["nixpkgs"]["locked"]["rev"]


def _skipped_names(repo_root: Path, env: Dict[str, str], system: str) -> List[str]:
    """Best-effort read of the no-darwin-build skip list. Non-fatal on failure."""
    try:
        r = subprocess.run(
            unavailable_eval_argv(system),
            cwd=repo_root,
            env=env,
            capture_output=True,
            text=True,
            timeout=120,
        )
    except (OSError, subprocess.SubprocessError):
        return []
    if r.returncode != 0:
        return []
    try:
        val = json.loads(r.stdout)
        return [str(x) for x in val] if isinstance(val, list) else []
    except json.JSONDecodeError:
        return []


def materialize(
    repo_root: Path, packages, *, system: str = DARWIN_SYSTEM
) -> DarwinPackages:
    """Realize the darwin buildEnv profile and return its store bin dir + env.

    IMPURE (runs nix; macOS-only in practice).  Raises ``DarwinPackagesError``
    when nix is missing or the build fails — the caller aborts with an
    actionable message rather than launching a half-provisioned sandbox.

    FUTURE (review #12): the realized profile has no GC root, so a
    ``nix-collect-garbage`` between materialize and a later reattach could reap
    it while the agent's baked PATH still points at it.  Low severity (needs an
    external GC trigger mid-session); a per-workspace indirect GC root under
    GLOBAL_STORAGE (``nix build --out-link <root>``) is the fix when needed.
    """
    env = build_env(packages)
    skipped = _skipped_names(repo_root, env, system)
    # Stream stderr (nix's `--print-build-logs` progress) straight to the
    # terminal so a from-source darwin build is VISIBLE — not a silent hang.
    # stdout (the store out-path) is captured; a stderr tail is kept for the
    # error message.  (A prior version used capture_output=True and showed
    # nothing, which read as "no build happened" on real hardware.)
    stderr_tail: List[str] = []
    try:
        proc = subprocess.Popen(
            build_profile_argv(system),
            cwd=repo_root,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
    except FileNotFoundError as e:
        raise DarwinPackagesError("nix command not found on PATH") from e
    except (OSError, subprocess.SubprocessError) as e:
        raise DarwinPackagesError(f"nix build failed to run: {e}") from e

    # Drain stderr live in a thread (so a long build streams) while the main
    # thread collects stdout; join both before inspecting the result.
    import sys as _sys
    import threading

    def _pump_stderr() -> None:
        assert proc.stderr is not None
        for line in iter(proc.stderr.readline, ""):
            _sys.stderr.write(line)
            _sys.stderr.flush()
            clean = line.rstrip()
            if clean:
                stderr_tail.append(clean)
                if len(stderr_tail) > 30:
                    stderr_tail.pop(0)

    t = threading.Thread(target=_pump_stderr, daemon=True)
    t.start()
    stdout, _ = proc.communicate()
    t.join(timeout=5)

    if proc.returncode != 0:
        raise DarwinPackagesError(
            "\n".join(stderr_tail).strip() or "nix build of darwin packages failed"
        )
    # --print-out-paths may emit multiple lines; the profile is the last one.
    out_lines = [ln for ln in (stdout or "").splitlines() if ln.strip()]
    if not out_lines:
        raise DarwinPackagesError("nix build produced no store path")
    path_prefix, extra = profile_paths(out_lines[-1])
    return DarwinPackages(path_prefix=path_prefix, env=extra, skipped=skipped)

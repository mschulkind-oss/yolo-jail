"""Native aarch64-darwin materialization of ``packages:`` for the macos-user backend.

The macos-user backend runs the agent as a native macOS user under Seatbelt —
no VM, no Linux image.  Its ``packages:`` must therefore resolve to *darwin*
builds.  The flake exposes ``devShells.aarch64-darwin.yoloDarwinPackages`` (a
shell whose closure is the darwin build of ``packages:``, driven by the same
``YOLO_EXTRA_PACKAGES`` env contract the image path uses) and
``darwinUnavailablePackages`` (the names filtered out for having no darwin
build — warn-and-skip).

This module's job on the host (which has nix; the sandbox does not) is:
  1. read the skipped-package list (``nix eval``);
  2. realize the devShell closure and capture its env (``nix print-dev-env``);
  3. extract the store ``bin`` dirs from that env's PATH and a STRICT whitelist
     of non-PATH build vars.

Those store bin dirs are threaded into the sandbox agent's PATH via
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
from typing import Dict, List, Tuple

DARWIN_SYSTEM = "aarch64-darwin"
DEVSHELL_ATTR = "yoloDarwinPackages"  # devShells.<system>.yoloDarwinPackages
UNAVAILABLE_ATTR = "darwinUnavailablePackages"  # <attr>.<system> -> [str]

# STRICT whitelist of non-PATH vars forwarded from ``nix print-dev-env`` into
# the sandbox.  print-dev-env dumps the ENTIRE stdenv build environment (a
# multi-thousand-line blob), so a blind passthrough would inject nix
# BUILD-ENV pollution — ``out``, ``TMPDIR``, ``SOURCE_DATE_EPOCH``,
# ``NIX_CFLAGS_COMPILE``, ``stdenv``, ``shellHook``, ``system``, ``name`` — into
# the agent's runtime env, which is meaningless-to-harmful at run time.  (This
# is NOT about re-leaking the host's ambient env; ``env -i`` in launch_argv
# already scrubs that — a distinct environment.)  Only forward vars that point
# into the store and genuinely help run the materialized tools.  Extend
# deliberately, never widen to a passthrough.
ENV_WHITELIST: Tuple[str, ...] = ("PKG_CONFIG_PATH",)


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


def print_dev_env_argv(system: str = DARWIN_SYSTEM) -> List[str]:
    """argv to dump the darwin devShell's env as JSON. Pure."""
    return [
        "nix",
        *_nix_flags(),
        "print-dev-env",
        "--impure",
        "--json",
        f".#devShells.{system}.{DEVSHELL_ATTR}",
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


def parse_dev_env(json_text: str) -> Dict[str, str]:
    """Extract exported scalar vars from ``print-dev-env --json`` output. Pure.

    The JSON has a ``variables`` map of ``name -> {type, value}``; we keep only
    ``type == "exported"`` string vars (the ones that would be in the shell's
    environment), ignoring bash functions and arrays.
    """
    data = json.loads(json_text)
    out: Dict[str, str] = {}
    for name, spec in (data.get("variables") or {}).items():
        if isinstance(spec, dict) and spec.get("type") == "exported":
            val = spec.get("value")
            if isinstance(val, str):
                out[name] = val
    return out


def split_env(dev_env: Dict[str, str]) -> Tuple[List[str], Dict[str, str]]:
    """Partition a dev-env dict into (store PATH dirs, whitelisted non-PATH). Pure.

    Only ``/nix/store/*`` PATH entries are kept — host dirs are dropped (the
    sandbox composes its own base PATH; we contribute only the package store
    dirs).  Non-PATH vars are filtered to ``ENV_WHITELIST``.
    """
    path_prefix = [
        p
        for p in dev_env.get("PATH", "").split(":")
        if p.startswith("/nix/store/") and p
    ]
    extra = {k: dev_env[k] for k in ENV_WHITELIST if k in dev_env}
    return path_prefix, extra


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
    """Realize the darwin devShell closure and return its store PATH + env.

    IMPURE (runs nix; macOS-only in practice).  Raises ``DarwinPackagesError``
    when nix is missing or the build fails — the caller aborts with an
    actionable message rather than launching a half-provisioned sandbox.
    """
    env = build_env(packages)
    skipped = _skipped_names(repo_root, env, system)
    try:
        proc = subprocess.run(
            print_dev_env_argv(system),
            cwd=repo_root,
            env=env,
            capture_output=True,
            text=True,
            timeout=1800,  # a cold from-source darwin build can be slow
        )
    except FileNotFoundError as e:
        raise DarwinPackagesError("nix command not found on PATH") from e
    except (OSError, subprocess.SubprocessError) as e:
        raise DarwinPackagesError(f"nix print-dev-env failed to run: {e}") from e
    if proc.returncode != 0:
        raise DarwinPackagesError(
            (proc.stderr or "").strip() or "nix print-dev-env failed"
        )
    try:
        dev_env = parse_dev_env(proc.stdout)
    except json.JSONDecodeError as e:
        raise DarwinPackagesError("could not parse nix print-dev-env JSON") from e
    path_prefix, extra = split_env(dev_env)
    return DarwinPackages(path_prefix=path_prefix, env=extra, skipped=skipped)

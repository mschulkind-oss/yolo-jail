"""Version-string discovery for the yolo CLI.

Resolution order matches a normal setuptools-scm + git-describe stack,
plus a YOLO_VERSION env-var escape hatch that the host CLI sets before
spawning the jail container so the inside-container banner and the
host banner agree.
"""

import os
import subprocess
from pathlib import Path

import typer


def _version_callback(value: bool):
    if value:
        v = _get_yolo_version()
        typer.echo(f"yolo-jail {v}")
        raise typer.Exit()


def _git_describe_version() -> "str | None":
    """Derive a version string from ``git describe --tags --dirty --always``.

    Returns a cleaned version such as ``0.1.0``, ``0.1.0+3.gabcdef1``, or
    ``0.1.0+3.gabcdef1.dirty``.  Returns *None* when git is unavailable or
    the command fails (e.g. not a git checkout).
    """
    raw = os.environ.get("YOLO_VERSION")
    if raw:
        return raw

    # Late import — _resolve_repo_root lives in cli/__init__.py and pulls in
    # the whole CLI on first import.  Doing it lazily breaks a circular
    # dependency (this module is imported during cli package init).
    try:
        from . import _resolve_repo_root

        repo_root = _resolve_repo_root()
    except Exception:
        repo_root = Path(__file__).resolve().parent.parent.parent

    try:
        result = subprocess.run(
            ["git", "describe", "--tags", "--dirty", "--always"],
            capture_output=True,
            text=True,
            timeout=5,
            cwd=repo_root,
        )
        if result.returncode == 0:
            raw = result.stdout.strip()
    except Exception:
        pass

    # Fall back to setuptools-scm baked version (in installed wheels)
    if raw is None:
        try:
            from src._version import version as scm_version

            raw = scm_version
        except Exception:
            pass

    # Fall back to package metadata
    if raw is None:
        try:
            from importlib.metadata import version as pkg_version

            raw = pkg_version("yolo-jail")
        except Exception:
            return None

    if raw is None:
        return None

    if raw.startswith("v"):
        raw = raw[1:]

    # git format: 0.1.0-3-gabcdef1-dirty  ->  0.1.0+3.gabcdef1.dirty
    # Exactly on tag: 0.1.0              ->  0.1.0
    # Dirty on tag:   0.1.0-dirty        ->  0.1.0.dirty
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


def _get_yolo_version() -> str:
    """Return the yolo-jail version string."""
    v = _git_describe_version()
    if v is None:
        from importlib.metadata import version as pkg_version

        try:
            v = pkg_version("yolo-jail")
        except Exception:
            v = "unknown"
    return v


def _container_baked_yolo_version(runtime: str, cname: str) -> "str | None":
    """Return the ``YOLO_VERSION`` baked into ``cname``'s env, or None.

    Runs ``<runtime> inspect`` to read the container's ``Config.Env``
    and greps out ``YOLO_VERSION=…``.  Short timeout + catch-all
    failure: a missing version is never a hard error, just falls back
    to the host CLI's version in the banner.
    """
    try:
        result = subprocess.run(
            [
                runtime,
                "inspect",
                "--format",
                "{{range .Config.Env}}{{println .}}{{end}}",
                cname,
            ],
            capture_output=True,
            text=True,
            timeout=3,
        )
    except (subprocess.TimeoutExpired, OSError, FileNotFoundError):
        return None
    if result.returncode != 0:
        return None
    for line in result.stdout.splitlines():
        if line.startswith("YOLO_VERSION="):
            return line[len("YOLO_VERSION=") :].strip() or None
    return None

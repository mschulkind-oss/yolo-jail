"""Typer subcommand group: ``yolo loopholes {list,status,enable,disable}``.

Inspect and toggle host-side loopholes (the controlled permeability
points between jail and host).  Bundled loopholes ship with the wheel;
user-installed loopholes live in ~/.local/share/yolo-jail/loopholes/;
config-inline loopholes come from yolo-jail.jsonc's ``loopholes:`` block.
"""

import os
from pathlib import Path
from typing import Any, Dict

import typer

from src import loopholes as _loopholes

from .config import _load_jsonc_file
from .paths import USER_CONFIG_PATH


loopholes_app = typer.Typer(
    help=(
        "Inspect and toggle host-side loopholes — controlled permeability "
        "points between the jail and the host (e.g. the Claude OAuth broker, "
        "journal bridge, host-process view)."
    )
)


def _loopholes_with_config(include_disabled: bool = False):
    """Discover loopholes including host_services synthesized from the
    merged user+workspace config (so `yolo loopholes list` sees them too).
    """
    try:
        user_cfg = _load_jsonc_file(USER_CONFIG_PATH, "user config") or {}
    except Exception:
        user_cfg = {}
    try:
        ws_cfg = (
            _load_jsonc_file(Path.cwd() / "yolo-jail.jsonc", "workspace config") or {}
        )
    except Exception:
        ws_cfg = {}
    merged_loopholes: Dict[str, Any] = {}
    for src in (user_cfg.get("loopholes") or {}, ws_cfg.get("loopholes") or {}):
        if isinstance(src, dict):
            merged_loopholes.update(src)
    return _loopholes.discover_loopholes(
        include_disabled=include_disabled,
        loopholes_config=merged_loopholes,
    )


@loopholes_app.command("list")
def loopholes_list():
    """List installed loopholes and their enabled/active state."""
    all_loopholes = _loopholes_with_config(include_disabled=True)
    if not all_loopholes:
        typer.echo("No loopholes installed.")
        typer.echo(f"  • bundled: {_loopholes.bundled_loopholes_dir()}")
        typer.echo(f"  • user: {_loopholes.user_loopholes_dir()}")
        typer.echo("  • workspace: yolo-jail.jsonc loopholes: block")
        return
    for loophole in all_loopholes:
        # State label: active / inactive(reason) / disabled.
        if not loophole.enabled:
            label = "disabled"
        elif loophole.inactive_reason:
            label = f"inactive ({loophole.inactive_reason})"
        else:
            label = "active"
        if loophole.transport == "tls-intercept" and loophole.intercepts:
            extra = (
                "intercepts=[" + ", ".join(i.host for i in loophole.intercepts) + "]"
            )
        else:
            extra = f"transport={loophole.transport}"
        tags = f"{loophole.source}/{loophole.transport}/{loophole.lifecycle}"
        typer.echo(f"  {label:<36}  {loophole.name}  ({tags})  {extra}")
        if loophole.description:
            typer.echo(f"      {loophole.description}")


@loopholes_app.command("status")
def loopholes_status():
    """Run each loophole's doctor_cmd and report."""
    # doctor_cmd entries are host-side console scripts (e.g.
    # yolo-claude-oauth-broker-host --self-check) — they aren't
    # installed inside the jail.  Running them from the jail just
    # surfaces confusing ENOENT output.  Tell the operator where to
    # run the checks instead.
    if os.environ.get("YOLO_VERSION") is not None:
        typer.echo(
            "Inside jail — doctor checks are host-side.  "
            "From the host: yolo loopholes status"
        )
        return
    loopholes_list_ = _loopholes_with_config(include_disabled=True)
    if not loopholes_list_:
        typer.echo("No loopholes installed.")
        return
    results = _loopholes.run_doctor_checks(loopholes_list_)
    for r in results:
        if not r.loophole.enabled:
            prefix = "disabled"
        elif not r.loophole.requirements_met:
            prefix = "inactive"
        elif r.returncode == 0:
            prefix = "ok"
        elif r.returncode is None:
            prefix = "no-check"
        else:
            prefix = "fail"
        typer.echo(f"  [{prefix}] {r.loophole.name}  rc={r.returncode}")
        if r.output:
            for line in r.output.splitlines():
                typer.echo(f"      {line}")


@loopholes_app.command("enable")
def loopholes_enable(name: str):
    """Enable a user-installed loophole by name.

    Bundled loopholes (shipped with the wheel) are read-only; to disable
    one, set ``loopholes.<name>.enabled: false`` in yolo-jail.jsonc.
    Config-inline loopholes are toggled the same way.
    """
    path = _loopholes.user_loopholes_dir() / name
    if not (path / "manifest.jsonc").is_file():
        typer.echo(
            f"No user-installed loophole at {path}.\n"
            "For bundled or workspace-inline loopholes, edit the workspace "
            "yolo-jail.jsonc (loopholes.<name>.enabled).",
            err=True,
        )
        raise typer.Exit(1)
    _loopholes.set_enabled(path, True)
    typer.echo(f"enabled {name}")


@loopholes_app.command("disable")
def loopholes_disable(name: str):
    """Disable a user-installed loophole (leaves files in place)."""
    path = _loopholes.user_loopholes_dir() / name
    if not (path / "manifest.jsonc").is_file():
        typer.echo(
            f"No user-installed loophole at {path}.\n"
            "For bundled or workspace-inline loopholes, edit the workspace "
            "yolo-jail.jsonc (loopholes.<name>.enabled).",
            err=True,
        )
        raise typer.Exit(1)
    _loopholes.set_enabled(path, False)
    typer.echo(f"disabled {name}")

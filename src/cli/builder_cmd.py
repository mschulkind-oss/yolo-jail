"""Typer subcommand group: ``yolo builder {setup,start,stop,status}``.

Manages the on-demand macOS Linux builder VM (see ``builder.py`` and
``docs/handoff-macos-ondemand-builder.md``).  The frictionless replacement
for "run and babysit ``nix run nixpkgs#darwin.linux-builder`` in a terminal":

  * ``setup``  — one-time: wire the Nix daemon to offload aarch64-linux builds
    to the VM (nix.conf builders line, ssh_config, key, launchd service).
  * ``start``  — bring the VM up now (yolo also does this automatically before
    a build that needs it).
  * ``stop``   — bring it down now (a launchd idle-timer also does this).
  * ``status`` — read-only snapshot: set-up? reachable? which conf file?

Registration is via ``app.add_typer(builder_app, name="builder")`` in
cli/__init__.py, mirroring the loopholes/broker groups.
"""

from __future__ import annotations

import typer

from . import builder
from .console import console
from .paths import IS_MACOS


builder_app = typer.Typer(
    help=(
        "Manage the on-demand macOS Linux builder VM.  yolo starts it "
        "automatically before a build that needs it and stops it when idle; "
        "these commands set it up and inspect/override its lifecycle."
    )
)


def _require_macos() -> None:
    if not IS_MACOS:
        console.print(
            "[yellow]The Linux builder is a macOS-only concept.[/yellow]  "
            "On Linux the image builds natively — no builder VM needed."
        )
        raise typer.Exit(0)


@builder_app.command("status")
def builder_status_cmd():
    """Report builder set-up state + reachability (read-only, safe)."""
    _require_macos()
    st = builder.builder_status()

    def mark(ok: bool) -> str:
        return "[green]yes[/green]" if ok else "[red]no[/red]"

    console.print("[bold]macOS Linux builder[/bold]")
    console.print(f"  set up:       {mark(st['done'])}")
    console.print(f"    nix.conf:   {mark(st['nix_builder'])}  ({st['conf_path']})")
    console.print(f"    ssh config: {mark(st['ssh_config'])}")
    console.print(f"    ssh key:    {mark(st['key'])}")
    console.print(
        f"  reachable:    {mark(st['reachable'])}  (port {builder.BUILDER_PORT})"
    )
    console.print()
    if not st["done"]:
        console.print(
            "[yellow]Not set up.[/yellow]  Run [cyan]yolo builder setup[/cyan] "
            "once to wire the Nix daemon to a Linux builder VM."
        )
        raise typer.Exit(1)
    if st["reachable"]:
        console.print("[green]Builder set up and running.[/green]")
        raise typer.Exit(0)
    console.print(
        "[dim]Builder set up but not running — that's normal when idle; "
        "yolo starts it automatically before a build.[/dim]"
    )
    raise typer.Exit(0)


@builder_app.command("start")
def builder_start_cmd():
    """Start the builder VM now (yolo does this automatically before a build)."""
    _require_macos()
    if builder.builder_reachable():
        console.print("[green]Builder already running.[/green]")
        raise typer.Exit(0)
    ok, err = builder.ensure_builder(
        on_progress=lambda m: console.print(f"[dim]{m}[/dim]")
    )
    if ok:
        console.print("[green]Builder is up.[/green]")
        raise typer.Exit(0)
    if err == "not set up":
        console.print(
            "[yellow]Builder not set up.[/yellow]  Run "
            "[cyan]yolo builder setup[/cyan] first."
        )
        raise typer.Exit(1)
    console.print(f"[red]Could not start builder:[/red] {err}")
    raise typer.Exit(1)


@builder_app.command("stop")
def builder_stop_cmd():
    """Stop the builder VM now, reclaiming its RAM (a launchd idle-timer
    also does this automatically)."""
    _require_macos()
    if not builder.builder_reachable():
        console.print("[dim]Builder not running.[/dim]")
        raise typer.Exit(0)
    ok, err = builder.stop_builder()
    if ok:
        console.print("[green]Builder stopped.[/green]")
        raise typer.Exit(0)
    console.print(f"[red]Could not stop builder:[/red] {err}")
    raise typer.Exit(1)


@builder_app.command("setup")
def builder_setup_cmd(
    max_jobs: int = typer.Option(
        4, "--max-jobs", help="Parallel build jobs to allow on the builder."
    ),
    show: bool = typer.Option(
        False, "--show", help="Print the privileged script and exit; don't run it."
    ),
    yes: bool = typer.Option(
        False, "--yes", "-y", help="Skip the confirmation prompt."
    ),
):
    """One-time wiring so the Nix daemon offloads aarch64-linux builds to the VM.

    macOS-only.  Does the work FOR you: explains what will change, shows the
    exact root script, then runs every privileged step in ONE ``sudo`` (a
    single password prompt).  This is an interactive per-run prompt, not a
    sudo-policy change — no NOPASSWD rules, nothing hidden.  ``trusted-users``
    is MERGED (existing entries preserved), never clobbered.
    """
    _require_macos()
    st = builder.builder_setup_state()
    if st["done"]:
        console.print("[green]Builder already set up.[/green]")
        console.print(
            "[dim]Run [cyan]yolo builder status[/cyan] to inspect, or "
            "[cyan]yolo builder start[/cyan] to bring it up.[/dim]"
        )
        raise typer.Exit(0)

    import getpass

    me = getpass.getuser()
    conf = builder._builder_conf_path()
    script = builder.setup_root_script(
        max_jobs, me, builder._current_trusted_users(), conf
    )

    console.print("[bold]Set up the on-demand macOS Linux builder[/bold]\n")
    console.print(
        "macOS can't build the Linux image locally, so Nix offloads to a small "
        "Linux VM.  This wires the Nix daemon to that VM so builds 'just work'; "
        "afterward yolo starts/stops the VM on demand (no terminal to babysit, "
        "no RAM held while idle).\n"
    )
    console.print("[bold]It will, in one sudo:[/bold]")
    console.print(
        f"  • add a [cyan]builders[/cyan] line to [cyan]{conf}[/cyan] (offload aarch64-linux)"
    )
    if builder.trusted_users_line(builder._current_trusted_users(), me) is not None:
        console.print(
            f"  • add [cyan]{me}[/cyan] to [cyan]trusted-users[/cyan] (merged — existing entries kept)"
        )
    else:
        console.print(
            "  • [dim](you're already a trusted user — no trusted-users change)[/dim]"
        )
    console.print(
        f"  • write the ssh host alias [cyan]{builder.SSH_CONFIG_PATH}[/cyan]"
    )
    console.print("  • restart the Nix daemon to apply\n")

    console.print("[dim]The exact root script:[/dim]")
    console.print(f"[dim]{script}[/dim]")

    if show:
        raise typer.Exit(0)

    console.print(
        "[dim]The builder VM itself isn't started here — yolo boots it on "
        "demand ([cyan]nix run nixpkgs#darwin.linux-builder[/cyan]) the first "
        "time a build needs it, which also installs its ssh key (one more sudo "
        "that first time only).[/dim]\n"
    )

    if not yes:
        proceed = typer.confirm("Run the privileged setup now (one sudo prompt)?")
        if not proceed:
            console.print(
                "[dim]Aborted. Re-run when ready, or `--show` to just print it.[/dim]"
            )
            raise typer.Exit(1)

    ok, err = builder.run_setup(max_jobs, me)
    if not ok:
        console.print(f"[red]Setup failed:[/red] {err}")
        raise typer.Exit(1)
    console.print("[green]Builder wired up.[/green]  yolo will start it on demand.")
    raise typer.Exit(0)

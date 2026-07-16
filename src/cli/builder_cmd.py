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
from .storage import _detect_nix_daemon_label


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
    console.print(f"  reachable:    {mark(st['reachable'])}  (port {builder.BUILDER_PORT})")
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
    ok, err = builder.ensure_builder(on_progress=lambda m: console.print(f"[dim]{m}[/dim]"))
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
):
    """One-time wiring so the Nix daemon offloads aarch64-linux builds to the VM.

    macOS-only, and it MUST be run on a real Mac — it writes ``/etc/nix`` and
    ``/etc/ssh`` and installs the builder's launchd service (needs sudo, and
    the VM is created by nixpkgs' installer).  Rather than silently mutating
    system files from here, this prints the exact, reviewable commands to run
    (single privileged step); trusted-users is checked but never changed for
    you (that policy call is yours).
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

    label = _detect_nix_daemon_label() or "org.nixos.nix-daemon"
    conf = builder._builder_conf_path()

    console.print("[bold]Set up the on-demand macOS Linux builder[/bold]\n")
    console.print(
        "Run these once (they need sudo — the builder installer writes the "
        "VM ssh key and the daemon config):\n"
    )
    console.print("[cyan]# 1. Create the builder VM + install its ssh key (nixpkgs installer):[/cyan]")
    console.print("  nix run nixpkgs#darwin.linux-builder -- --help  # first run installs credentials\n")
    console.print(f"[cyan]# 2. Tell the Nix daemon to offload aarch64-linux builds ({conf}):[/cyan]")
    console.print(
        f"  printf '%s' {builder.nix_builders_line(max_jobs)!r} | sudo tee -a {conf}\n"
    )
    console.print(f"[cyan]# 3. Register the ssh host alias ({builder.SSH_CONFIG_PATH}):[/cyan]")
    console.print(
        f"  printf '%s' {builder.ssh_config_block()!r} | sudo tee {builder.SSH_CONFIG_PATH}\n"
    )
    console.print("[cyan]# 4. Restart the Nix daemon to apply:[/cyan]")
    console.print(f"  sudo launchctl kickstart -k system/{label}\n")
    console.print(
        "[dim]After this, yolo starts/stops the builder for you on demand. "
        "See docs/handoff-macos-ondemand-builder.md for the launchd idle-stop "
        "service (still to be finalized on a Mac).[/dim]"
    )
    # Not "done" yet — the user must run the privileged steps.  Exit non-zero
    # so scripts can tell setup is incomplete.
    raise typer.Exit(1)

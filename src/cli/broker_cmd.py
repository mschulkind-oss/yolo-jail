"""Typer subcommand group: ``yolo broker {status,stop,restart,logs}``.

The Claude OAuth broker is a host-wide singleton (one daemon for every
running jail).  These commands manage that singleton — inspect health,
stop it, cycle it after a wheel upgrade, tail its log.

Importing this module side-effect-attaches the subcommand group to the
top-level ``app``.  cli/__init__.py just imports it (no symbols
needed); registration happens via the @broker_app.command decorators.
"""

import subprocess

import typer

from .console import console
from .loopholes_runtime import (
    _broker_is_alive,
    _broker_kill,
    _broker_spawn,
    _broker_status,
)
from .paths import GLOBAL_STORAGE


broker_app = typer.Typer(
    help=(
        "Manage the singleton Claude OAuth broker daemon.  One broker per "
        "host serves every running jail — cycle it here after a wheel "
        "upgrade, inspect its liveness, tail its log."
    )
)


@broker_app.command("status")
def broker_status_cmd():
    """Report whether the broker is alive + socket path + last ping."""
    status = _broker_status()
    console.print("[bold]Claude OAuth broker (singleton)[/bold]")
    pid = status["pid"]
    if pid is None:
        console.print("  [dim]not running[/dim] (no PID file)")
    else:
        mark = "[green]live[/green]" if status["pid_live"] else "[red]dead[/red]"
        console.print(f"  pid:          {pid}  {mark}")
    sock_mark = (
        "[green]present[/green]" if status["socket_exists"] else "[red]missing[/red]"
    )
    console.print(f"  socket:       {status['socket']}  {sock_mark}")
    ping_mark = "[green]ok[/green]" if status["ping_ok"] else "[red]no response[/red]"
    console.print(f"  ping:         {ping_mark}")
    console.print(f"  pid file:     {status['pid_file']}")
    console.print()
    if status["pid_live"] and status["ping_ok"]:
        console.print("[green]Broker healthy.[/green]")
        raise typer.Exit(0)
    console.print(
        "[yellow]Broker not fully healthy.[/yellow]  "
        "Run [cyan]yolo broker restart[/cyan] to cycle."
    )
    raise typer.Exit(1)


@broker_app.command("stop")
def broker_stop_cmd():
    """Kill the running broker singleton (if any).  Next jail access
    lazily respawns."""
    stopped = _broker_kill()
    if stopped:
        console.print("[green]Stopped broker.[/green]")
    else:
        console.print("[dim]No broker was running.[/dim]")


@broker_app.command("restart")
def broker_restart_cmd():
    """Stop the running broker (if any) then spawn a fresh one — the
    canonical way to pick up a new wheel's broker code without
    restarting every jail.  ``just deploy`` calls this at the end so
    upgrades are immediate."""
    _broker_kill()
    sock = _broker_spawn()
    if _broker_is_alive():
        console.print(f"[green]Broker restarted.[/green]  socket={sock}")
        raise typer.Exit(0)
    console.print(
        "[red]Broker failed to become live after spawn.[/red]  "
        f"Check {GLOBAL_STORAGE / 'logs' / 'host-service-claude-oauth-broker.log'}"
    )
    raise typer.Exit(1)


@broker_app.command("logs")
def broker_logs_cmd(
    lines: int = typer.Option(50, "-n", "--lines", help="Tail N lines"),
    follow: bool = typer.Option(False, "-f", "--follow", help="tail -f style"),
):
    """Tail the broker log.  One log shared across every jail."""
    log_path = GLOBAL_STORAGE / "logs" / "host-service-claude-oauth-broker.log"
    if not log_path.is_file():
        console.print(f"[dim]No log file yet at {log_path}[/dim]")
        raise typer.Exit(0)
    cmd = ["tail", f"-n{lines}"]
    if follow:
        cmd.append("-f")
    cmd.append(str(log_path))
    try:
        subprocess.run(cmd)
    except KeyboardInterrupt:
        pass

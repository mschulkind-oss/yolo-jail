// Package brokercmd is the Go port of the `yolo broker {status,stop,restart,
// logs}` command group (src/cli/broker_cmd.py). The Claude OAuth broker is a
// host-wide singleton — one daemon for every running jail — and this group
// manages it: inspect health, stop it, cycle it after a wheel upgrade, tail its
// log.
//
// The lifecycle engine (BrokerStatus/IsAlive/Kill/Spawn/Ping) lives in
// internal/brokerlifecycle behind an injectable Deps seam; this package is the
// thin command body. Output is rich-console → INFO-parity (same information,
// Go-native color) per the approved output-contract OQ; the EXIT CODES and the
// socket/pid/log PATH strings are byte-exact vs Python.
//
// Wiring is the orchestrator's job (a one-line runBroker dispatcher in
// cmd/yolo/native.go). This package only exposes clean importable funcs
// (Status/Stop/Restart/Logs) + RealDeps.
package brokercmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlifecycle"
)

// Deps are the injectable seams for the command bodies. Life is the lifecycle
// engine's Deps; the command layer wraps it. Out/Err are the console writers;
// Color enables ANSI markup rendering (info-parity, not rich byte-parity).
// RunTail runs the `tail` argv attached to the terminal (logs -f blocks); a
// test substitutes a no-op. LogIsFile reports whether the broker log exists as a
// regular file (mirrors Path.is_file()).
type Deps struct {
	Life      brokerlifecycle.Deps
	Out, Err  io.Writer
	Color     bool
	LogPath   string
	LogIsFile func(path string) bool
	RunTail   func(argv []string) error
}

// RealDeps returns Deps backed by the real lifecycle engine, stdout/stderr, and
// a `tail` that inherits the terminal.
func RealDeps() Deps {
	life := brokerlifecycle.RealDeps()
	return Deps{
		Life:    life,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Color:   true,
		LogPath: life.LogPath,
		LogIsFile: func(p string) bool {
			info, err := os.Stat(p)
			return err == nil && info.Mode().IsRegular()
		},
		RunTail: func(argv []string) error {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		},
	}
}

// Status ports broker_status_cmd: print the health snapshot, then exit 0 when
// the broker is healthy (pid_live AND ping_ok), else print the cycle hint and
// exit 1. The snapshot line CONTENT is info-parity; the exit code is exact.
func Status(deps Deps) int {
	st := brokerlifecycle.BrokerStatus(deps.Life)
	out := printer{w: deps.Out, color: deps.Color}

	out.print("[bold]Claude OAuth broker (singleton)[/bold]")
	if !st.PIDPresent {
		out.print("  [dim]not running[/dim] (no PID file)")
	} else {
		mark := "[red]dead[/red]"
		if st.PIDLive {
			mark = "[green]live[/green]"
		}
		out.printf("  pid:          %d  %s", st.PID, mark)
	}
	sockMark := "[red]missing[/red]"
	if st.SocketExists {
		sockMark = "[green]present[/green]"
	}
	out.printf("  socket:       %s  %s", st.Socket, sockMark)
	pingMark := "[red]no response[/red]"
	if st.PingOK {
		pingMark = "[green]ok[/green]"
	}
	out.printf("  ping:         %s", pingMark)
	out.printf("  pid file:     %s", st.PIDFile)
	out.print("")

	if st.PIDLive && st.PingOK {
		out.print("[green]Broker healthy.[/green]")
		return 0
	}
	out.print("[yellow]Broker not fully healthy.[/yellow]  " +
		"Run [cyan]yolo broker restart[/cyan] to cycle.")
	return 1
}

// Stop ports broker_stop_cmd: kill the running singleton (if any). Next jail
// access lazily respawns. Always exits 0 (no typer.Exit in the Python body).
func Stop(deps Deps) int {
	stopped := brokerlifecycle.BrokerKill(deps.Life, syscall.SIGTERM, brokerlifecycle.BrokerKillTimeout)
	out := printer{w: deps.Out, color: deps.Color}
	if stopped {
		out.print("[green]Stopped broker.[/green]")
	} else {
		out.print("[dim]No broker was running.[/dim]")
	}
	return 0
}

// Restart ports broker_restart_cmd: kill the running broker (if any) then spawn
// a fresh one — the canonical way to pick up a new wheel's broker code without
// restarting every jail. Exit 0 with `socket=<path>` when the broker becomes
// live; exit 1 with the log-path hint otherwise.
func Restart(deps Deps) int {
	brokerlifecycle.BrokerKill(deps.Life, syscall.SIGTERM, brokerlifecycle.BrokerKillTimeout)
	sock := brokerlifecycle.BrokerSpawn(deps.Life)
	out := printer{w: deps.Out, color: deps.Color}
	if brokerlifecycle.BrokerIsAlive(deps.Life) {
		out.printf("[green]Broker restarted.[/green]  socket=%s", sock)
		return 0
	}
	out.printf("[red]Broker failed to become live after spawn.[/red]  "+
		"Check %s", deps.LogPath)
	return 1
}

// Logs ports broker_logs_cmd: tail the shared broker log. When the log file
// doesn't exist yet, print the dim "no log" line and exit 0. Otherwise build the
// tail argv byte-exact vs Python — ["tail", "-n<lines>", maybe "-f", <path>] —
// and run it. KeyboardInterrupt (Ctrl-C on `-f`) is swallowed (exit 0).
func Logs(deps Deps, lines int, follow bool) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.LogIsFile(deps.LogPath) {
		out.printf("[dim]No log file yet at %s[/dim]", deps.LogPath)
		return 0
	}
	argv := []string{"tail", "-n" + strconv.Itoa(lines)}
	if follow {
		argv = append(argv, "-f")
	}
	argv = append(argv, deps.LogPath)
	if err := deps.RunTail(argv); err != nil {
		// Python catches KeyboardInterrupt and passes; any other tail failure
		// there surfaces via subprocess.run's own stderr and a non-zero rc that
		// Python ignores (the body returns None → exit 0). Match: swallow.
		if isInterrupt(err) {
			return 0
		}
		return 0
	}
	return 0
}

// isInterrupt reports whether err is a SIGINT death of the child (tail -f
// Ctrl-C), the analog of Python's KeyboardInterrupt swallow.
func isInterrupt(err error) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			return ws.Signaled() && ws.Signal() == syscall.SIGINT
		}
	}
	return false
}

// BuildTailArgv is the pure argv builder, exposed for parity testing against
// Python. Byte-exact: ["tail", "-n<lines>", ("-f")?, <path>].
func BuildTailArgv(lines int, follow bool, logPath string) []string {
	argv := []string{"tail", "-n" + strconv.Itoa(lines)}
	if follow {
		argv = append(argv, "-f")
	}
	return append(argv, logPath)
}

// printer renders one console line. In color mode the closed markup tag set is
// rendered to ANSI; otherwise tags are stripped (info-parity — same text).
type printer struct {
	w     io.Writer
	color bool
}

func (p printer) print(msg string) {
	fmt.Fprintln(p.w, renderMarkup(msg, p.color))
}

func (p printer) printf(format string, args ...any) {
	fmt.Fprintln(p.w, renderMarkup(fmt.Sprintf(format, args...), p.color))
}

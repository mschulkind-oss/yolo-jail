// The `yolo broker {status,stop,restart,logs}` command group. The Claude OAuth
// broker is a host-wide singleton — one daemon for every running jail — and this
// group manages it: inspect health, stop it, cycle it after a wheel upgrade,
// tail its log.
// The lifecycle engine (BrokerStatus/IsAlive/Kill/Spawn/Ping) lives alongside
// these command bodies in this package, behind an injectable Deps seam; the
// command layer is the thin body over it. Output is rich-console → INFO-parity
// (same information, Go-native color) per the approved output-contract OQ; the
// EXIT CODES and the socket/pid/log PATH strings are byte-exact vs Python.
// Wiring is the orchestrator's job (a one-line runBroker dispatcher in
// cmd/yolo/native.go). This file exposes clean importable funcs
// (PrintStatus/Stop/Restart/Logs) + CLIRealDeps.
package broker

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// CLIDeps are the injectable seams for the command bodies. Life is the lifecycle
// engine's Deps; the command layer wraps it. Out/Err are the console writers;
// Color enables ANSI markup rendering (info-parity, not rich byte-parity).
// RunTail runs the `tail` argv attached to the terminal (logs -f blocks); a
// test substitutes a no-op. LogIsFile reports whether the broker log exists as a
// regular file).
//
// Color requests ANSI markup, but it only reaches the writer when IsTTYStdout()
// is also true — a pipe/redirect stays clean. IsTTYStdout is an injectable seam:
// CLIRealDeps wires the os.Stdout char-device probe (mirroring builder/macosuser
// real-Deps); nil is treated as "not a TTY", so zero-value CLIDeps strips.
type CLIDeps struct {
	Life        Deps
	Out, Err    io.Writer
	Color       bool
	IsTTYStdout func() bool
	LogPath     string
	LogIsFile   func(path string) bool
	RunTail     func(argv []string) error
}

// CLIRealDeps returns CLIDeps backed by the real lifecycle engine, stdout/stderr,
// and a `tail` that inherits the terminal.
func CLIRealDeps() CLIDeps {
	life := RealDeps()
	return CLIDeps{
		Life:        life,
		Out:         os.Stdout,
		Err:         os.Stderr,
		Color:       true,
		IsTTYStdout: isTTYStdoutReal,
		LogPath:     life.LogPath,
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

// PrintStatus ports broker_status_cmd: print the health snapshot, then exit 0
// when the broker is healthy (pid_live AND ping_ok), else print the cycle hint
// and exit 1. The snapshot line CONTENT is info-parity; the exit code is exact.
func PrintStatus(deps CLIDeps) int {
	st := BrokerStatus(deps.Life)
	out := newPrinter(deps)

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
func Stop(deps CLIDeps) int {
	stopped := BrokerKill(deps.Life, syscall.SIGTERM, BrokerKillTimeout)
	out := newPrinter(deps)
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
func Restart(deps CLIDeps) int {
	BrokerKill(deps.Life, syscall.SIGTERM, BrokerKillTimeout)
	sock := BrokerSpawn(deps.Life)
	out := newPrinter(deps)
	if BrokerIsAlive(deps.Life) {
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
func Logs(deps CLIDeps, lines int, follow bool) int {
	out := newPrinter(deps)
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

// printer wraps the shared richtext renderer: color mode emits ANSI, otherwise
// known style tags are stripped (literal brackets like [y/N] are preserved in
// both modes — info-parity, same text). Lowercase methods keep call sites terse.
type printer struct{ rt richtext.Printer }

func (p printer) print(msg string)               { p.rt.Print(msg) }
func (p printer) printf(format string, a ...any) { p.rt.Printf(format, a...) }

// newPrinter builds a printer for deps.Out, resolving the color gate: ANSI is
// emitted only when deps.Color is set AND stdout is a real terminal, so a
// pipe/redirect stays clean.
func newPrinter(deps CLIDeps) printer {
	color := deps.Color && deps.IsTTYStdout != nil && deps.IsTTYStdout()
	return printer{rt: richtext.Printer{W: deps.Out, Color: color}}
}

// isTTYStdoutReal reports whether os.Stdout is a real terminal (the shared
// internal/tty ioctl probe), mirroring builder/macosuser real-Deps, so color
// reaches only a terminal.
func isTTYStdoutReal() bool {
	return tty.IsTerminalFile(os.Stdout)
}

// The `yolo host-daemon {status,stop,restart,logs}` command bodies, and the
// `yolo broker` alias over the Claude singleton. A host-wide daemon is a
// singleton — one process for every running jail on the machine — and this group
// manages it: inspect health, stop it, cycle it after a binary upgrade, tail its
// log.
//
// THE BODIES TAKE A SINGLETON RATHER THAN BEING THE BROKER'S. They used to wire
// CLIRealDeps() to the broker's own RealDeps(), with the name and the argv
// hardcoded — which was true of the one loophole that declared `scope: "host"`
// when they were written, and became a wrong instruction the moment there were
// three (OQ-HD2, docs/design/host-daemon-ownership.md). Every sentence they print
// names the daemon it is about, for the same reason reportFailedSpawn does: a
// generic verb that loses the name reintroduces the defect somewhere new.
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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// CLIDeps are the injectable seams for the command bodies. Singleton names WHICH
// host-wide daemon they are about; Life is the lifecycle engine's Deps for that
// daemon; the command layer wraps it. Out/Err are the console writers;
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
	// Singleton is the host-scoped daemon these bodies act on. Every message they
	// print names Singleton.Name, and Restart refuses by name when Argv is empty.
	Singleton   Singleton
	Life        Deps
	Out, Err    io.Writer
	Color       bool
	IsTTYStdout func() bool
	LogPath     string
	LogIsFile   func(path string) bool
	RunTail     func(argv []string) error
	// Format is the output format for the reporting verbs: "" / outfmt.Text (the
	// human report, unchanged) or outfmt.JSON. Only `status` reports state; the
	// CLI front door refuses the flag on the three verbs that ACT (stop, restart,
	// logs) rather than accepting it and ignoring it.
	Format string
}

// CLIRealDeps returns CLIDeps for the CLAUDE singleton — the `yolo broker` alias's
// target, and nothing else's. New callers want CLIDepsFor.
func CLIRealDeps() CLIDeps { return CLIDepsFor(BrokerSingleton()) }

// CLIDepsFor returns CLIDeps for one host-scoped daemon, backed by the real
// lifecycle engine, stdout/stderr, and a `tail` that inherits the terminal.
//
// Every path comes from s.Name through SingletonDeps, so this function knows no
// loophole by name — the half of the generalization the CLI was missing.
func CLIDepsFor(s Singleton) CLIDeps {
	life := SingletonDeps(s.Name, s.Argv)
	return CLIDeps{
		Singleton:   s,
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

// subject is the daemon these bodies are talking about, for the messages. It
// degrades an unnamed Singleton — reachable only from a hand-built CLIDeps, since
// both constructors fill it — to the generic phrase rather than to a blank, the
// same degradation Deps.Name takes in reportFailedSpawn. A sentence with a hole
// where the name should be is worse than one that admits it does not know.
func (d CLIDeps) subject() string {
	if d.Singleton.Name != "" {
		return d.Singleton.Name
	}
	return "the host-wide daemon"
}

// PrintStatus prints one daemon's health snapshot, then exits 0 when it is
// healthy (pid_live AND socket accepting), else prints the cycle hint and exits 1.
// The snapshot line CONTENT is info-parity; the exit code is exact.
//
// THE HINT NAMES THIS DAEMON, through cycleCommand. It said `yolo broker restart`
// unconditionally, which for two of the three host-scoped daemons cycles a
// different process and leaves the broken one running.
func PrintStatus(deps CLIDeps) int {
	st := BrokerStatus(deps.Life)
	// The document is the Status struct itself (see its json tags), and the exit
	// code below is unchanged: 0 healthy, 1 not. A consumer gets the same verdict
	// from st.Healthy without having to read the process's exit status.
	if outfmt.IsJSON(deps.Format) {
		enc, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			// Loud, never a silent empty stdout — the one outcome a machine
			// consumer cannot diagnose.
			fmt.Fprintf(deps.Err, "status for %s: encoding the report failed: %v\n",
				deps.subject(), err)
			return 1
		}
		fmt.Fprintln(deps.Out, string(enc))
		if st.Healthy {
			return 0
		}
		return 1
	}
	out := newPrinter(deps)

	// THE HEADER IS THE NAME. A management report whose heading says "the broker"
	// while the numbers below it belong to aws-auth is the whole defect this verb
	// exists to close, one layer up from the wrong remedy.
	out.printf("[bold]Host-wide daemon: %s[/bold]", deps.subject())
	if deps.Singleton.Description != "" {
		out.printf("  [dim]%s[/dim]", deps.Singleton.Description)
	}
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
	// "accepting", not "ping": the singleton sits behind yolo's front now, so the
	// host side can no longer speak its protocol without forging a jail identity
	// (broker.SingletonReachable). The column says what was measured.
	reachMark := "[red]not accepting[/red]"
	if st.Reachable {
		reachMark = "[green]accepting[/green]"
	}
	out.printf("  socket accept: %s", reachMark)
	out.printf("  pid file:     %s", st.PIDFile)
	out.print("")

	if st.Healthy {
		out.printf("[green]%s is healthy.[/green]", deps.subject())
		return 0
	}
	out.printf("[yellow]%s is not fully healthy.[/yellow]  Run [cyan]%s[/cyan] to cycle.",
		deps.subject(), CycleCommand(deps.Singleton.Name))
	return 1
}

// Stop kills this daemon's running singleton (if any). The next launch that wants
// it ensures it again. Always exits 0 — "nothing was running" is the requested
// state, not a failure.
//
// It needs no declaration: BrokerKill works from the PID file (or pgrep) and the
// rendezvous paths, all of which are functions of the NAME. That is what lets a
// user stop a daemon whose pack they have since deselected — the one act
// host-daemon-ownership.md §6 mode 5 says nothing in the system offers.
func Stop(deps CLIDeps) int {
	stopped := BrokerKill(deps.Life, syscall.SIGTERM, BrokerKillTimeout)
	out := newPrinter(deps)
	if stopped {
		out.printf("[green]Stopped %s.[/green]", deps.subject())
	} else {
		out.printf("[dim]No host-wide daemon was running for %s.[/dim]", deps.subject())
	}
	return 0
}

// Restart kills this daemon (if any) then spawns a fresh one — the canonical way
// to pick up a new binary's daemon code without restarting every jail, and the
// remedy the alive-but-incompatible warning names. Exit 0 with `socket=<path>`
// when it becomes live; exit 1 with the log-path hint otherwise.
//
// IT REFUSES BEFORE THE KILL when it has no argv, and the order is the whole
// point: a daemon nothing declares can be stopped but not started, so killing
// first and discovering that second would leave the user worse off than they
// began, with the process gone and no way to bring it back. The refusal names the
// daemon, says why, and points at the verb that does work on it.
func Restart(deps CLIDeps) int {
	out := newPrinter(deps)
	if len(deps.Life.Argv) == 0 {
		reason := deps.Singleton.NoSpawn
		if reason == "" {
			reason = "yolo has no spawn command for it"
		}
		out.printf("[red]Cannot restart %s: %s.[/red]", deps.subject(), reason)
		out.printf("  [dim]yolo host-daemon stop %s[/dim] can still stop it; "+
			"[dim]yolo host-daemon status %s[/dim] reports it.",
			deps.Singleton.Name, deps.Singleton.Name)
		return 1
	}
	BrokerKill(deps.Life, syscall.SIGTERM, BrokerKillTimeout)
	sock := BrokerSpawn(deps.Life)
	if BrokerIsAlive(deps.Life) {
		out.printf("[green]Restarted %s.[/green]  socket=%s", deps.subject(), sock)
		return 0
	}
	out.printf("[red]%s failed to become live after spawn.[/red]  Check %s",
		deps.subject(), deps.LogPath)
	return 1
}

// Logs tails this daemon's shared host-wide log. When the log file
// doesn't exist yet, print the dim "no log" line and exit 0. Otherwise build the
// tail argv byte-exact vs Python — ["tail", "-n<lines>", maybe "-f", <path>] —
// and run it. KeyboardInterrupt (Ctrl-C on `-f`) is swallowed (exit 0).
func Logs(deps CLIDeps, lines int, follow bool) int {
	out := newPrinter(deps)
	if !deps.LogIsFile(deps.LogPath) {
		out.printf("[dim]No log file yet for %s at %s[/dim]", deps.subject(), deps.LogPath)
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

// newPrinter builds a printer for deps.Out, resolving the color gate through
// tty.Color: ANSI is emitted only when deps.Color is set AND stdout is a real
// terminal AND NO_COLOR is unset or empty, so a pipe/redirect stays clean.
func newPrinter(deps CLIDeps) printer {
	color := tty.Color(nil, deps.Color, deps.IsTTYStdout != nil && deps.IsTTYStdout())
	return printer{rt: richtext.Printer{W: deps.Out, Color: color}}
}

// isTTYStdoutReal reports whether os.Stdout is a real terminal (the shared
// internal/tty ioctl probe), mirroring builder/macosuser real-Deps, so color
// reaches only a terminal. A var so a test can stand a terminal in for the
// pipe `go test` gives it, and so reach SingletonDeps' own color decision.
var isTTYStdoutReal = func() bool {
	return tty.IsTerminalFile(os.Stdout)
}

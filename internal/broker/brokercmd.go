// The `yolo host-daemon {status,stop,restart,logs}` command bodies, and the `yolo broker` alias
// over the Claude broker.
//
// WHAT THE VERB ACTS ON IS WHAT THE JAILS ARE RUNNING (docs/design/host-daemon-ownership.md
// §5.2). Since HD-R1 every host daemon is a launch's own, so a name has as many running copies as
// there are jails using it, each keyed by its jail, plus any machine-wide one an older yolo left
// (hostdaemons.go). What each verb does with them:
//
//   - status reports every copy, and exits non-zero when one does not accept;
//   - stop ends every copy of a name, waiting out a request in flight (the daemon's own upstream
//     deadline) before it kills, and names what the jails it served must do next; with
//     --host-wide it ends only the machine-wide copy an older yolo left (StopHostWide), the
//     upgrade's optional last step, once the jails that copy served have ended or been launched
//     again with their own (HD-D18; a launch beside the copy proceeds, by the owner's coexistence
//     decision of 2026-10-16);
//   - restart restarts nothing: a jail's daemon restarts with the jail (§6, mode 2), so it says
//     how to relaunch, and that stop is there for the copies running now;
//   - logs tails the one log every copy appends to.
//
// Every message names the daemon it is about and the next step (the happy-path principle,
// docs/reference/happy-path-principle.md).
package broker

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// StopWait is how long stop waits for a jail's copy to exit after its SIGTERM before it kills it:
// a credential daemon finishes the request in flight first, bounded by its thirty-second upstream
// deadline (the manifest's `detach`), so a kill sooner could cut a refresh that already redeemed
// a single-use token upstream. A copy with nothing in flight exits at once.
const StopWait = 35 * time.Second

// CLIDeps are the seams for the command bodies. Out/Err are the console writers; Color requests
// ANSI markup, which reaches the writer only when IsTTYStdout is also true.
type CLIDeps struct {
	Out, Err    io.Writer
	Color       bool
	IsTTYStdout func() bool
	// Format is "" / outfmt.Text or outfmt.JSON. Only status reports; the CLI front door refuses
	// the flag on the verbs that act.
	Format string
	// Running lists the running copies of a host daemon, "" for every one (RunningInstances).
	Running func(name string) []Instance
	// Stragglers lists the processes still serving a jail's copy of name whose socket is gone
	// (jailStragglers).
	Stragglers func(name string) []int
	// Terminate ends pids: SIGTERM, a wait of up to wait, then SIGKILL; it returns the ones it had
	// to kill (terminate).
	Terminate func(pids []int, wait time.Duration) (killed []int)
	// MarkStopped tells the supervisor of a jail's copy, the jail's own yolo process, that the
	// death of pids serving socket is a stop asked for, so it does not restart it
	// (hostservice.WriteStopMarker; docs/design/host-daemon-ownership.md HD-D28).
	MarkStopped func(socket string, pids []int) error
	// StopHostWide ends the machine-wide copy an older yolo left for name, and reports whether
	// one was running (BrokerKill over LegacyDeps).
	StopHostWide func(name string) bool
	// RemoveStale removes a jail's socket that nothing serves, and the daemon's private
	// sibling beside it (removeStaleSocket).
	RemoveStale func(socket string)
	// LogPath is the log of a name (HostServiceLogPath); LogIsFile and RunTail serve logs.
	LogPath   func(name string) string
	LogIsFile func(path string) bool
	RunTail   func(argv []string) error
}

// CLIRealDeps returns CLIDeps backed by this machine.
func CLIRealDeps() CLIDeps {
	return CLIDeps{
		Out:         os.Stdout,
		Err:         os.Stderr,
		Color:       true,
		IsTTYStdout: isTTYStdoutReal,
		Running:     RunningInstances,
		Stragglers:  jailStragglers,
		Terminate:   terminate,
		MarkStopped: hostservice.WriteStopMarker,
		StopHostWide: func(name string) bool {
			return BrokerKill(LegacyDeps(name), syscall.SIGTERM, BrokerKillTimeout)
		},
		RemoveStale: removeStaleSocket,
		LogPath:     HostServiceLogPath,
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

// PrintStatus reports every running copy of the host daemon called name, or of every host daemon
// when name is "", and exits 1 when one of them does not accept, else 0. Nothing running is an
// answer, not a failure: no copy runs while no jail uses the daemon.
func PrintStatus(deps CLIDeps, name string) int {
	insts := deps.Running(name)
	rc := 0
	for _, i := range insts {
		if !i.Healthy() {
			rc = 1
		}
	}
	if outfmt.IsJSON(deps.Format) {
		if insts == nil {
			insts = []Instance{}
		}
		enc, err := json.MarshalIndent(insts, "", "  ")
		if err != nil {
			fmt.Fprintf(deps.Err, "host-daemon status: encoding the report failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(deps.Out, string(enc))
		return rc
	}
	out := newPrinter(deps)
	if len(insts) == 0 {
		if name == "" {
			out.print("[dim]No host daemon is running on this machine.[/dim]")
			out.print("  [dim]Each jail starts its own host daemons and ends them with it, so none " +
				"run while no jail does; `yolo loopholes list` names the ones a launch starts.[/dim]")
		} else {
			out.printf("[dim]No %s is running on this machine.[/dim]", name)
			out.print("  [dim]Each jail that uses it starts a copy of its own and ends it with the " +
				"jail; a launch that selects its pack starts one.[/dim]")
		}
		return 0
	}
	last := ""
	for _, i := range insts {
		if i.Name != last {
			if last != "" {
				out.print("")
			}
			out.printf("[bold]Host daemon: %s[/bold]", i.Name)
			last = i.Name
		}
		mark := "[green]accepting[/green]"
		if !i.Accepting {
			mark = "[red]not accepting[/red]"
		}
		if i.HostWide {
			// ONLY WHAT IS KNOWN (HD-D31): the machine-wide model is retired, so no jail this yolo
			// launched uses the copy; whether a jail an older yolo launched still does is not
			// something status can see, so it is said as the condition for the stop.
			out.printf("  host-wide, left by an older yolo: pid %s, socket %s, %s",
				pidList(i.PIDs), socketValue(i.Socket), mark)
			out.print("    [dim]Jails this yolo launches do not use it; only a jail an older yolo " +
				"launched can. Once no such jail is running, this ends it:[/dim]")
			out.print("      [cyan]" + richtext.Escape(StopHostWideCommand(i.Name)) + "[/cyan]")
			continue
		}
		out.printf("  jail %s: pid %s, socket %s, %s", i.Jail, pidList(i.PIDs), socketValue(i.Socket), mark)
		if !i.Accepting {
			out.printf("    [yellow]Its daemon died, or its jail ended without stopping it. If that "+
				"jail is still running, its %s requests fail: leave the jail and launch it again, "+
				"which starts a fresh copy (`yolo %s logs %s` shows why this one stopped). If it "+
				"is not, `yolo %s stop %s` clears the socket it left.[/yellow]",
				i.Name, HostDaemonVerb, i.Name, HostDaemonVerb, i.Name)
		}
	}
	return rc
}

// socketValue renders a status line's socket path as its value: cyan, so the label/value split
// is visible (docs/plans/cli-visual-polish.md, Group C), and escaped, since the path is not text
// yolo wrote. Stripped, it is the path.
func socketValue(socket string) string {
	return "[cyan]" + richtext.Escape(socket) + "[/cyan]"
}

// pidList renders pids for a status line, "unknown" when none could be found.
func pidList(pids []int) string {
	if len(pids) == 0 {
		return "unknown"
	}
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ",")
}

// Stop ends every running copy of the host daemon called name: each jail's own, any copy still
// finishing a request after its jail ended, and a machine-wide one an older yolo left. Always
// exits 0 — "nothing was running" is the requested state.
//
// A JAIL'S COPY IS WAITED FOR, then killed: SIGTERM, up to StopWait for a request in flight to
// finish, then SIGKILL, which is how a human ends a copy stuck holding the refresh lock
// (docs/design/host-daemon-ownership.md §1.3: "killing a stuck lock holder is a thing a human may
// have to do").
//
// AND IT STAYS STOPPED: each jail's copy is marked first (MarkStopped), so the jail's supervisor
// does not restart it; a jail gets a fresh copy when it is launched again.
func Stop(deps CLIDeps, name string) int {
	out := newPrinter(deps)
	pidSet := map[int]bool{}
	var jails, cleared []string
	var marks []Instance
	hostWide := false
	for _, i := range deps.Running(name) {
		if i.HostWide {
			hostWide = true
			continue
		}
		if len(i.PIDs) == 0 && !i.Accepting {
			// NOTHING SERVES IT: no process names the socket and nothing accepts on it, so it is
			// what a daemon that died, or a jail that ended without its teardown, left behind.
			// Removing it is what clears the status line, and it can cut no one off.
			deps.RemoveStale(i.Socket)
			cleared = append(cleared, i.Jail)
			continue
		}
		jails = append(jails, i.Jail)
		for _, p := range i.PIDs {
			pidSet[p] = true
		}
		if len(i.PIDs) > 0 {
			marks = append(marks, i)
		}
	}
	// STOP MEANS STOP (docs/design/host-daemon-ownership.md HD-D28): the jail's own yolo process
	// supervises its copy and restarts a death it did not cause, so each copy is marked as stopped
	// on purpose BEFORE any signal, and its supervisor, reading the mark, leaves it down.
	var unmarked []string
	for _, i := range marks {
		if err := deps.MarkStopped(i.Socket, i.PIDs); err != nil {
			unmarked = append(unmarked, fmt.Sprintf("jail %s (%s)", i.Jail, richtext.Escape(err.Error())))
		}
	}
	for _, p := range deps.Stragglers(name) {
		pidSet[p] = true
	}
	pids := make([]int, 0, len(pidSet))
	for p := range pidSet {
		pids = append(pids, p)
	}
	sort.Ints(pids)
	stopped := false
	if len(pids) > 0 {
		stopped = true
		killed := deps.Terminate(pids, StopWait)
		what := "jail"
		if len(jails) != 1 {
			what = "jails"
		}
		if len(jails) > 0 {
			out.printf("[green]Stopped %s for %s %s (pid %s).[/green]", name, what,
				strings.Join(jails, ", "), pidList(pids))
		} else {
			out.printf("[green]Stopped %s (pid %s), still finishing after its jail ended.[/green]",
				name, pidList(pids))
		}
		if len(killed) > 0 {
			out.printf("  [yellow]pid %s did not exit within %s of its SIGTERM and was killed.[/yellow]",
				pidList(killed), StopWait)
		}
		if len(jails) > 0 {
			out.printf("  Each jail it served fails its %s requests until it is launched again: "+
				"leave that jail and launch it again, which starts a fresh copy.", name)
		}
		for _, u := range unmarked {
			out.printf("  [yellow]Warning: could not mark the copy for %s as stopped, so that "+
				"jail's yolo may restart it: `yolo stop` in that jail's workspace ends the jail and "+
				"its copy together.[/yellow]", u)
		}
	}
	if len(cleared) > 0 {
		stopped = true
		which := "jail"
		if len(cleared) != 1 {
			which = "jails"
		}
		out.printf("[green]Removed %s's socket for %s %s, which nothing was serving.[/green]", name,
			which, strings.Join(cleared, ", "))
		out.print("  Its daemon had died, or its jail ended without stopping it. If that jail is " +
			"still running, leave it and launch it again, which starts a fresh copy.")
	}
	if len(jails) > 0 && len(pids) == 0 {
		// A socket and no process found behind it: nothing to signal, and not "nothing is
		// running" either, since status lists it.
		stopped = true
		out.printf("[yellow]Found %s's socket for %s but no process serving it, so nothing "+
			"was stopped.[/yellow]", name, strings.Join(jails, ", "))
		out.print("  A jail that ended without stopping its daemon leaves this socket, and the " +
			"next launch of that workspace replaces it. If a process does serve it, this machine " +
			"has no `pgrep` to find it by: install procps, then run this again.")
	}
	if hostWide && deps.StopHostWide(name) {
		stopped = true
		out.printf("[green]Stopped the host-wide %s an older yolo left.[/green]", name)
		out.print("  The jails that yolo launched lose it: relaunch them with this yolo, which gives " +
			"each its own.")
	}
	if !stopped {
		out.printf("[dim]No %s is running on this machine.[/dim]", name)
	}
	return 0
}

// HostWideFlag is stop's flag for acting on an older yolo's host-wide copy alone.
const HostWideFlag = "--host-wide"

// StopHostWideCommand is the upgrade's optional last step for name, as a launch, status, `yolo
// check` and the changelog spell it: stop the host-wide copy an older yolo left, and nothing else,
// once the jails it serves have ended.
func StopHostWideCommand(name string) string {
	return "yolo " + HostDaemonVerb + " stop " + HostWideFlag + " " + name
}

// StopHostWide ends the machine-wide copy of name an older yolo left, and NOTHING ELSE: no jail's
// own copy and no straggler. It is the upgrade's optional last step, taken once the jails that
// copy served have ended or been launched again (HD-D18); a jail relaunched by then runs its own
// copy, and Stop, which ends every copy, would cut it off.
// Always exits 0, like Stop.
func StopHostWide(deps CLIDeps, name string) int {
	out := newPrinter(deps)
	hostWide := false
	for _, i := range deps.Running(name) {
		if i.HostWide {
			hostWide = true
		}
	}
	if hostWide && deps.StopHostWide(name) {
		out.printf("[green]Stopped the host-wide %s an older yolo left.[/green]", name)
		out.print("  Every jail's own copy is still running. A jail that older yolo launched and " +
			"that is still running loses it: leave that jail and launch it again with this yolo, " +
			"which gives it its own.")
		return 0
	}
	out.printf("[dim]No host-wide %s an older yolo left is running on this machine; every "+
		"jail's own copy is untouched.[/dim]", name)
	out.printf("  [dim]`yolo %s status %s` lists the copies running now.[/dim]", HostDaemonVerb, name)
	return 0
}

// Restart restarts nothing, and says why and what does: since HD-R1 a host daemon is its jail's
// own and restarts with the jail (docs/design/host-daemon-ownership.md §6, mode 2). There is no
// shared daemon to cycle, and a copy started by hand beside a jail would be no launch's, which is
// the process HD-R1 exists to end. Exits 1, a refusal.
func Restart(deps CLIDeps, name string) int {
	out := newPrinter(deps)
	n := 0
	for _, i := range deps.Running(name) {
		if !i.HostWide {
			n++
		}
	}
	out.printf("[yellow]Nothing to restart: each jail starts its own %s and ends it with the jail, "+
		"so it restarts with its jail.[/yellow]", name)
	out.printf("  To restart the copy a jail uses, leave that jail and launch it again (`yolo stop` "+
		"in its workspace ends it at once). %d copies run now; `yolo %s stop %s` ends them all, and "+
		"`yolo %s status %s` lists them.", n, HostDaemonVerb, name, HostDaemonVerb, name)
	return 1
}

// Logs tails the log every copy of name appends to. A log that does not exist yet prints a dim
// line and exits 0.
func Logs(deps CLIDeps, name string, lines int, follow bool) int {
	out := newPrinter(deps)
	path := deps.LogPath(name)
	if !deps.LogIsFile(path) {
		out.printf("[dim]No log file yet for %s at %s; a launch that starts it writes one.[/dim]", name, path)
		return 0
	}
	if err := deps.RunTail(BuildTailArgv(lines, follow, path)); err != nil {
		// A Ctrl-C on `-f` and any tail failure alike exit 0: tail has already said why.
		_ = isInterrupt(err)
	}
	return 0
}

// removeStaleSocket removes a jail's daemon socket that nothing serves, and the `.host` sibling
// a daemon may bind beside it (the OpenAI credential service's private socket). Missing files are
// not an error: the point is that they are gone.
func removeStaleSocket(socket string) {
	for _, p := range []string{socket, socket + ".host"} {
		_ = os.Remove(p)
	}
}

// terminate SIGTERMs pids, waits up to wait for them to exit, SIGKILLs the rest, and returns
// those it had to kill.
func terminate(pids []int, wait time.Duration) []int {
	for _, p := range pids {
		_ = syscall.Kill(p, syscall.SIGTERM)
	}
	deadline := time.Now().Add(wait)
	alive := append([]int(nil), pids...)
	for len(alive) > 0 && time.Now().Before(deadline) {
		var still []int
		for _, p := range alive {
			if execx.IsAlive(p) {
				still = append(still, p)
			}
		}
		alive = still
		if len(alive) > 0 {
			time.Sleep(SocketPollInterval)
		}
	}
	for _, p := range alive {
		_ = syscall.Kill(p, syscall.SIGKILL)
	}
	return alive
}

// isInterrupt reports whether err is a SIGINT death of the child (tail -f Ctrl-C).
func isInterrupt(err error) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			return ws.Signaled() && ws.Signal() == syscall.SIGINT
		}
	}
	return false
}

// BuildTailArgv is the pure argv builder: ["tail", "-n<lines>", ("-f")?, <path>].
func BuildTailArgv(lines int, follow bool, logPath string) []string {
	argv := []string{"tail", "-n" + strconv.Itoa(lines)}
	if follow {
		argv = append(argv, "-f")
	}
	return append(argv, logPath)
}

// printer wraps the shared richtext renderer.
type printer struct{ rt richtext.Printer }

func (p printer) print(msg string)               { p.rt.Print(msg) }
func (p printer) printf(format string, a ...any) { p.rt.Printf(format, a...) }

// newPrinter builds a printer for deps.Out, resolving the color gate through tty.Color: ANSI only
// when deps.Color is set AND stdout is a terminal AND NO_COLOR is unset or empty.
func newPrinter(deps CLIDeps) printer {
	color := tty.Color(nil, deps.Color, deps.IsTTYStdout != nil && deps.IsTTYStdout())
	return printer{rt: richtext.Printer{W: deps.Out, Color: color}}
}

// isTTYStdoutReal reports whether os.Stdout is a real terminal. A var so a test can stand a
// terminal in for the pipe `go test` gives it.
var isTTYStdoutReal = func() bool {
	return tty.IsTerminalFile(os.Stdout)
}

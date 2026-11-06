package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostrun"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// hostrun.go is `yolo host-run`: one line a human runs in a
// host terminal, whose stdout, stderr and exit status reach the jail through the workspace's
// mailbox, and the two jail forms that read it. The work is in internal/hostrun; this file is
// the verb, the in-jail ready line and steps 2 to 6 of the host form.
//
// Architecture and invariants: docs/reference/host-run-mailbox.md
//
// MAIN ROUTES IT BEFORE THE FRONT DOOR (cli.go), as it does `gh` (HX-D15): the host form's
// terminal carries another program's output, and the banner, the update notice and the global
// flags would each put yolo's words into it or take one of its flags.

const hostRunUsage = `Usage: yolo host-run [-C DIR] [--name LABEL] -- COMMAND [ARG...]
       yolo host-run wait [-C DIR] [--name LABEL] [--timeout DURATION]
       yolo host-run show [-C DIR] [RUN-ID | --name LABEL]

On the host: run COMMAND in DIR (-C, else this terminal's directory), as you,
with this terminal as its input, and deliver its stdout, stderr and exit status
to the yolo workspace at or above DIR, for the agent in its jail. The output
also shows here as it runs. Running the line is the approval: yolo prints what
it will run, the program's path and where the result goes, then runs it.

Start the line with bare ` + "`yolo host-run`" + `: nothing before it, and no ;, &&, |, >
or $( anywhere, since your shell acts on those before yolo sees them. The
command's output is a pipe, not a terminal: no color, no pager, and no
full-screen program. Its input is this terminal, so a password prompt works.
A refusal runs nothing, exits 64 and says why. Only the refusal of a gh
command that prints your GitHub credential is also sent to the agent; every
other refusal shows here only, so tell the agent yourself.

In a jail: the first form runs nothing and prints the line, with -C filled
from YOLO_HOST_DIR, for a human to run on the host. ` + "`wait`" + ` waits for the next
run (or the newest one carrying --name LABEL), prints it and exits 0, whatever
the command's own exit; 69 means the host refused or stopped, 75 that nothing
arrived within --timeout (default 100m): run it again. ` + "`show`" + ` prints the newest
run, RUN-ID, or the newest carrying --name, without waiting.

Flags:
  -C DIR              The command's directory; the workspace is found from it.
  --name LABEL        A label for the run (a-z, 0-9, '.', '_', '-'), so a
                      wait can pick out its own: use a fresh one each time.
  --timeout DURATION  wait only: how long to wait (default 100m).
  --help, -h          Show this help (before any ` + "`--`" + `).

Examples:
  yolo host-run -- brew upgrade                        # from a terminal inside the workspace
  yolo host-run -C /home/you/code/widgets --name merge-42 -- gh pr merge 42
  yolo host-run wait --name merge-42                   # in the jail, as a background task
  yolo host-run show                                   # in the jail: the newest result`

// runHostRunVerb is the registry handler, which receives the verb itself first. Main calls
// hostRunMain directly with the arguments after it.
func runHostRunVerb(args []string) int { return hostRunMain(args[1:]) }

// hostRunMain picks the form: `wait` and `show` are verbs, anything else the host form.
func hostRunMain(args []string) int {
	if answerHelp("host-run", args, os.Stdout) {
		return 0
	}
	if len(args) > 0 {
		switch args[0] {
		case "wait":
			return hostRunWait(args[1:])
		case "show":
			return hostRunShow(args[1:])
		}
	}
	return hostRunHost(args)
}

func hostRunSay(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "yolo host-run: "+format+"\n", a...)
}

// hostRunHost is the host form's steps 1 to 6, then hostrun.RunHost.
func hostRunHost(args []string) int {
	if config.InJail() {
		return hostRunReadyLine(args)
	}
	// Step 2: all three streams are terminals, so no redirect hides the live output.
	if !tty.IsTerminalFile(os.Stdin) || !tty.IsTerminalFile(os.Stdout) || !tty.IsTerminalFile(os.Stderr) {
		hostRunSay("stdin, stdout and stderr must each be a terminal, so nothing ran. Run the line as written, with no redirect or pipe.")
		return hostrun.ExitRefused
	}
	// Steps 3 and 4.
	l, err := hostrun.ParseHostArgs(args)
	if err != nil {
		hostRunSay("%v", err)
		return hostrun.ExitRefused
	}
	if err := hostrun.CheckLine(l); err != nil {
		hostRunSay("%v", err)
		return hostrun.ExitRefused
	}
	// Step 5: the start directory.
	jailSpelling := ""
	if l.Dir == "/workspace" || strings.HasPrefix(l.Dir, "/workspace/") {
		jailSpelling = " That is the jail's spelling; in the jail, `echo $YOLO_HOST_DIR` prints this host's."
	}
	start := l.Dir
	if start == "" {
		if start, err = os.Getwd(); err != nil {
			hostRunSay("cannot read this terminal's directory: %v; nothing ran. Add `-C <workspace>`.", err)
			return hostrun.ExitRefused
		}
	}
	start, err = filepath.Abs(start)
	if err == nil {
		start, err = filepath.EvalSymlinks(start)
	}
	if err == nil {
		if fi, serr := os.Stat(start); serr != nil || !fi.IsDir() {
			err = errors.New("not a directory")
		}
	}
	if err != nil {
		hostRunSay("no directory %s on this host, so nothing ran.%s", termsafe.Visible(shquote.QuoteDisplay(l.Dir)), jailSpelling)
		return hostrun.ExitRefused
	}
	// Step 6: the workspace, by the marker walk from the start directory (HX-D36, HX-D37).
	ws, ok := resolveWorkspaceRootFrom(start)
	if !ok {
		hostRunSay("no yolo workspace at or above %s, so nothing ran. Add `-C <workspace>` (in the jail, `echo $YOLO_HOST_DIR` prints it), or run the command without `yolo host-run` if no agent needs its output.%s",
			termsafe.Visible(start), jailSpelling)
		return hostrun.ExitRefused
	}
	repoRes, _ := reporoot.Resolve(os.Getenv)
	return hostrun.RunHost(hostrun.HostEnv{Version: version.Binary(repoRes.Root)}, l, start, ws)
}

// hostRunReadyLine is the host form typed in a jail: it runs nothing, creates nothing, and
// prints the line with -C filled from YOLO_HOST_DIR, plus the matching wait (HX-D42).
func hostRunReadyLine(args []string) int {
	l, err := hostrun.ParseHostArgs(args)
	if err != nil {
		hostRunSay("%v", err)
		return hostrun.ExitRefused
	}
	hostDir := os.Getenv("YOLO_HOST_DIR")
	if l.Dir == "" {
		l.Dir = hostDir
	}
	if err := hostrun.CheckLine(l); err != nil {
		hostRunSay("%v", err)
		return hostrun.ExitRefused
	}
	wait := "yolo host-run wait"
	if l.Label != "" {
		wait = "yolo wait " + l.Label
	}
	where := "into a terminal on the host"
	fmt.Fprintf(os.Stderr, "yolo host-run: this runs on the host, so nothing ran here. Ask the human to paste this ONE line\n"+
		"%s; it runs there, as them, and sends stdout, stderr and the exit status\n"+
		"back to this workspace:\n\n  %s\n\nThen start, as a background task:  %s\n", where, hostrun.FormatLine(l), wait)
	if l.Dir == "" {
		fmt.Fprintln(os.Stderr, "YOLO_HOST_DIR is unset here, so the line has no -C: the human must run it from a terminal inside the workspace.")
	}
	return hostrun.ExitRefused
}

// hostRunJailArgs is what wait and show parse.
type hostRunJailArgs struct {
	dir, label, id string
	timeout        time.Duration
}

func parseHostRunJailArgs(verb string, args []string) (hostRunJailArgs, bool) {
	var a hostRunJailArgs
	fail := func(format string, v ...any) (hostRunJailArgs, bool) {
		hostRunSay(format+"; see `yolo host-run --help`.", v...)
		return a, false
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, val, glued := strings.Cut(arg, "=")
		switch name {
		case "-C", "--name", "--timeout":
			if !glued {
				if i+1 >= len(args) {
					return fail("%s needs a value", name)
				}
				i++
				val = args[i]
			}
			switch name {
			case "-C":
				a.dir = val
			case "--name":
				if err := hostrun.CheckLabel(val); err != nil {
					return fail("%v", err)
				}
				a.label = val
			case "--timeout":
				if verb != "wait" {
					return fail("--timeout is for wait only")
				}
				d, err := time.ParseDuration(val)
				if err != nil || d <= 0 {
					return fail("--timeout %s is not a duration such as 30m", shquote.QuoteDisplay(val))
				}
				a.timeout = d
			}
		default:
			if verb == "show" && a.id == "" && !strings.HasPrefix(arg, "-") {
				a.id = arg
				continue
			}
			return fail("yolo host-run %s does not take %s", verb, shquote.QuoteDisplay(arg))
		}
	}
	if a.id != "" && a.label != "" {
		return fail("give a run id or --name, not both")
	}
	return a, true
}

// hostRunJailOpts finds the workspace for wait and show by the same walk as the host form.
func hostRunJailOpts(a hostRunJailArgs) (hostrun.JailOpts, bool) {
	start := a.dir
	if start == "" {
		start, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}
	if r, err := filepath.EvalSymlinks(start); err == nil {
		start = r
	}
	ws, ok := resolveWorkspaceRootFrom(start)
	if !ok {
		hostRunSay("no yolo workspace at or above %s; run it from the workspace or add `-C <workspace>`.", termsafe.Visible(start))
		return hostrun.JailOpts{}, false
	}
	return hostrun.JailOpts{Workspace: ws, Dir: a.dir, Label: a.label, ID: a.id, Timeout: a.timeout}, true
}

func hostRunWait(args []string) int {
	a, ok := parseHostRunJailArgs("wait", args)
	if !ok {
		return hostrun.ExitUsage
	}
	o, ok := hostRunJailOpts(a)
	if !ok {
		return hostrun.ExitUsage
	}
	return hostrun.Wait(o)
}

func hostRunShow(args []string) int {
	a, ok := parseHostRunJailArgs("show", args)
	if !ok {
		return hostrun.ExitUsage
	}
	o, ok := hostRunJailOpts(a)
	if !ok {
		return hostrun.ExitUsage
	}
	return hostrun.Show(o)
}

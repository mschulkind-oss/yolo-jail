package run

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// herdragent.go tells herdr (https://herdr.dev) which agent a jailed pane is running. It is
// Option 2 of docs/research/herdr-integration.md §4.3, plus a self-report.
//
// WHY IT IS NEEDED. herdr finds an agent by the processes in the foreground group of its
// pane, and for a jailed agent those are this `yolo` and the runtime client, never `claude`.
// Without help the pane reads as a plain shell and herdr never applies the agent's screen
// rules, so its sidebar cannot say working, blocked or done for any jailed agent.
//
// WHAT IT DOES, three things, all from the host:
//   - THE HINT: HERDR_AGENT=<agent> on each session's runtime client (runtimeClientEnv),
//     herdr's own documented fix for a sandbox that hides the agent (HR-D3). Never on the
//     launcher, never passed with -e. It reaches no process on macos-user, whose launcher
//     starts no runtime client.
//   - THE REPORT: `herdr pane report-agent --state unknown`, identity only, so the pane reads
//     as the agent from the session's start, macos-user included. Screen rules decide the state.
//   - THE LABEL: `herdr pane report-metadata --title "🔒 JAIL <project>"`, guarded by --agent
//     and by --applies-to-source, so herdr shows it only while the pane's agent matches and
//     this launch's report still holds the pane (HR-D8). Cleared at the session's end.
//
// WHEN. Each arm registers only once a signal arm covers it, just before its session starts
// (the fresh launch's armLaunchSignals, an attach's attachSignalArm), and releases when the
// session returns, before that arm is disarmed; each arm's teardown releases too, and Run's
// defer covers every other return. herdr keeps a report from a source it does not know
// until it is released or replaced, or the pane closes, and the `--agent` guard alone does not
// retire the label: the report is what makes the pane's agent match it. A registration made
// above the dispatch outlived a Ctrl-C at the config prompt or during the image build, where
// no arm runs, and left a host shell reading as the jailed agent under "🔒 JAIL". macos-user
// has no signal arm; it registers after its config prompt, just before its sandbox starts.
//
// WHICH LAUNCHES. Only one whose argv[0] is a program a SELECTED pack installs
// (Pack.InstallBins, the one authority for that namespace), so core names no agent. A bare
// `yolo` and `yolo -- bash` register nothing: yolo cannot see what is started inside them.
// An attach matches the packs the running jail booted from.
//
// WHICH HERDR. The one named `herdr` on PATH, as the tmux and kitty arms find theirs. The
// ambient HERDR_BIN_PATH is never run: that is the narrower variant of §4.3.
//
// NOTHING OF HERDR'S CROSSES INTO THE JAIL (HR-D2): no socket, no HERDR_* variable.
//
// It is HOST-SIDE and cannot be verified in a nested jail, which runs in no herdr pane.

// herdrSource is the --source every report carries. It is the spelling the hand-written
// `herdr-yolo-agent` wrapper used, so a machine still running that wrapper converges on one
// registration instead of holding two.
const herdrSource = "yolo-jail"

// herdrOptOutEnv turns off the hint, the report and the label. The kitty indicator's
// stand-down in a herdr pane (terminal.go) is not this switch's: that arm would retitle the
// kitty tab holding all of herdr whether or not yolo talks to herdr.
const herdrOptOutEnv = "YOLO_NO_HERDR"

// herdrBin is the program every call runs, resolved on PATH.
const herdrBin = "herdr"

// herdrTimeout bounds each herdr call. The report sits on the launch path and the release
// on the exit path, and a wedged herdr server must cost neither more than this, plus realExec's
// execDrainGrace. That holds for a herdr that leaves a child holding its output too: realExec
// stops reading at the deadline, though its kill reaches herdr alone.
const herdrTimeout = 2 * time.Second

// herdrPane is one launch's registration with the herdr pane it runs in. Run makes it before
// any arm is installed, so an arm's goroutine reads a pointer written before it started, and
// everything the two goroutines share is under mu. The registration holds mu while it talks to
// herdr, so a teardown that starts meanwhile waits for it and releases what it reported.
type herdrPane struct {
	mu          sync.Mutex
	pane, agent string
	reported    bool // herdr took the report: the release owes it a clear and a release
	closed      bool // released, or a teardown ran first: nothing more is said to herdr
}

// registerHerdrAgent sets the agent hint for the runtime client, reports the launching agent
// to the herdr pane this launch runs in and labels the pane. A caller registers only under its
// arm (herdragent.go's WHEN); releaseHerdrAgent undoes it, once.
//
// NEVER FATAL. herdr is an observer of the launch, so a failed report prints one line and
// the launch proceeds with the hint alone.
func (o *Options) registerHerdrAgent(packs []*packload.Pack, argv []string) {
	h := o.herdr
	if h == nil || o.DryRun || len(argv) == 0 || o.Exec == nil {
		return
	}
	if o.Getenv("HERDR_ENV") != "1" || o.Getenv(herdrOptOutEnv) == "1" {
		return
	}
	pane := o.Getenv("HERDR_PANE_ID")
	if pane == "" {
		return
	}
	agent := filepath.Base(argv[0])
	if !selectedPacksInstall(packs, agent) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.pane != "" {
		return
	}
	h.pane, h.agent = pane, agent
	o.runtimeClientEnv = append(o.runtimeClientEnv, "HERDR_AGENT="+agent)

	res := o.herdrCall(h, "report-agent", "--state", "unknown")
	if !res.Ran || res.RC != 0 || res.Timeout {
		o.pr(o.Stderr).print("[yellow]herdr: could not register this pane as " + richtext.Escape(agent) +
			" (" + richtext.Escape(herdrFailure(res)) + "); herdr may still find it from the jail's runtime. " +
			herdrOptOutEnv + "=1 turns this off.[/yellow]")
		return
	}
	h.reported = true
	// The label is display only, so its failure is not worth a line of its own.
	o.herdrCall(h, "report-metadata", "--applies-to-source", herdrSource, "--title", "🔒 JAIL "+o.herdrProject())
	o.pr(o.Stderr).print("[dim]herdr: pane " + richtext.Escape(pane) + " registered as " + richtext.Escape(agent) +
		" (jailed, source " + herdrSource + ")[/dim]")
}

// releaseHerdrAgent clears the label and releases the agent this launch reported, at most once
// for the launch, and closes the registration, so one that has not happened yet never will.
// Safe from a signal arm's goroutine; a no-op where nothing was reported.
func (o *Options) releaseHerdrAgent() {
	h := o.herdr
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	if !h.reported {
		return
	}
	o.herdrCall(h, "report-metadata", "--clear-title")
	o.herdrCall(h, "release-agent")
}

// herdrCall runs one `herdr pane <verb>` for the registered pane, bounded by herdrTimeout.
func (o *Options) herdrCall(h *herdrPane, verb string, extra ...string) ExecResult {
	argv := []string{herdrBin, "pane", verb, h.pane, "--source", herdrSource, "--agent", h.agent}
	return o.Exec(append(argv, extra...), "", nil, herdrTimeout)
}

// herdrFailure words why herdr refused or never answered: the first line of what it said, or,
// when it said nothing, what the launcher saw instead.
func herdrFailure(res ExecResult) string {
	switch {
	case !res.Ran:
		return "herdr could not be run from PATH"
	case res.Timeout:
		return "herdr did not answer within " + herdrTimeout.String()
	}
	for _, line := range strings.Split(res.Stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "herdr exited " + strconv.Itoa(res.RC)
}

// herdrProject is the label's project name, spelled as the tmux and kitty arms spell theirs:
// $SM_PROJECT, else the workspace's base name.
func (o *Options) herdrProject() string {
	if p := o.Getenv("SM_PROJECT"); p != "" {
		return p
	}
	return filepath.Base(o.Workspace)
}

// selectedPacksInstall reports whether any of packs installs bin.
func selectedPacksInstall(packs []*packload.Pack, bin string) bool {
	for _, p := range packs {
		if slices.Contains(p.InstallBins(), bin) {
			return true
		}
	}
	return false
}

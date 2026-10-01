package run

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
//     as the agent from the launch on, macos-user included. Screen rules decide the state.
//   - THE LABEL: `herdr pane report-metadata --title "🔒 JAIL <project>"`, guarded by
//     --agent so herdr shows it only while the pane's agent matches (HR-D8). Cleared at exit.
//
// WHICH LAUNCHES. Only one whose argv[0] is a program a SELECTED pack installs
// (Pack.InstallBins, the one authority for that namespace), so core names no agent. A bare
// `yolo` and `yolo -- bash` register nothing: yolo cannot see what is started inside them.
//
// WHICH HERDR. The one named `herdr` on PATH, as the tmux and kitty arms find theirs. The
// ambient HERDR_BIN_PATH is not run: that is the narrower variant of §4.3, which leaves
// OQ-HR1 only the question of reading HERDR_ENV and HERDR_PANE_ID.
//
// NOTHING OF HERDR'S CROSSES INTO THE JAIL (HR-D2): no socket, no HERDR_* variable.
//
// It is HOST-SIDE and cannot be verified in a nested jail, which runs in no herdr pane.

// herdrSource is the --source every report carries. It is the spelling the hand-written
// `herdr-yolo-agent` wrapper used, so a machine still running that wrapper converges on one
// registration instead of holding two.
const herdrSource = "yolo-jail"

// herdrOptOutEnv turns all of it off, as YOLO_NO_TMUX does the tmux indicator.
const herdrOptOutEnv = "YOLO_NO_HERDR"

// herdrBin is the program every call runs, resolved on PATH.
const herdrBin = "herdr"

// herdrTimeout bounds each herdr call. The report sits on the launch path and the release
// on the exit path, and a wedged herdr server must cost neither more than this.
const herdrTimeout = 2 * time.Second

// registerHerdrAgent sets the agent hint for the runtime client, reports the launching agent
// to the herdr pane this launch runs in and labels the pane, and returns the release, or nil
// when nothing was registered. The release is idempotent: both exit arms call it.
//
// NEVER FATAL. herdr is an observer of the launch, so a failed report prints one line and
// the launch proceeds with the hint alone.
func (o *Options) registerHerdrAgent(packs []*packload.Pack, argv []string) func() {
	if o.DryRun || len(argv) == 0 || o.Exec == nil {
		return nil
	}
	if o.Getenv("HERDR_ENV") != "1" || o.Getenv(herdrOptOutEnv) == "1" {
		return nil
	}
	pane := o.Getenv("HERDR_PANE_ID")
	if pane == "" {
		return nil
	}
	agent := filepath.Base(argv[0])
	if !selectedPacksInstall(packs, agent) {
		return nil
	}
	o.runtimeClientEnv = append(o.runtimeClientEnv, "HERDR_AGENT="+agent)

	idArgs := []string{"--source", herdrSource, "--agent", agent}
	call := func(verb string, extra ...string) ExecResult {
		argv := append([]string{herdrBin, "pane", verb, pane}, idArgs...)
		return o.Exec(append(argv, extra...), "", nil, herdrTimeout)
	}

	res := call("report-agent", "--state", "unknown")
	if !res.Ran || res.RC != 0 || res.Timeout {
		why := strings.TrimSpace(res.Stderr)
		if why == "" {
			why = "no output"
		}
		o.pr(o.Stderr).print("[yellow]herdr: could not register this pane as " + agent +
			" (" + why + "); herdr may still find it from the jail's runtime. " + herdrOptOutEnv +
			"=1 turns this off.[/yellow]")
		return nil
	}
	// The label is display only, so its failure is not worth a line of its own.
	call("report-metadata", "--title", "🔒 JAIL "+o.herdrProject())
	o.pr(o.Stderr).print("[dim]herdr: pane " + pane + " registered as " + agent +
		" (jailed, source " + herdrSource + ")[/dim]")

	var once sync.Once
	return func() {
		once.Do(func() {
			call("report-metadata", "--clear-title")
			call("release-agent")
		})
	}
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

// chainHerdrRelease puts release in front of the terminal restore, which is the one hook
// the signal arm runs (it os.Exit()s, so no defer fires there); Run defers release for
// every other exit. release is once-guarded, so the two never report twice.
func (o *Options) chainHerdrRelease(release func()) {
	restore := o.RestoreTerminal
	o.RestoreTerminal = func() {
		release()
		if restore != nil {
			restore()
		}
	}
}

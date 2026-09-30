package run

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// herdragent.go tells herdr (https://herdr.dev) which agent a jailed pane is running.
//
// WHY IT IS NEEDED. herdr finds an agent by the process in the foreground of its pane, and
// for a jailed agent that process is this `yolo` (then the runtime), never `claude`. Without
// a report the pane reads as a plain shell and herdr never applies the agent's screen rules,
// so its sidebar cannot say working, blocked or done for any jailed agent. herdr's own
// hook integrations cannot close the gap either: `herdr integration install claude` writes
// the HOST's ~/.claude, which the jail never reads.
//
// WHAT IT REPORTS. Identity only, with --state unknown, and herdr's screen rules decide the
// state from there. Semantic state and resume need the agent's session, which exists only
// inside the jail; that is a loophole's job, and this file does not pretend to do it.
//
// WHICH LAUNCHES. Only one whose argv[0] is a program a SELECTED pack installs
// (Pack.InstallBins, the one authority for that namespace), so core names no agent. A bare
// `yolo` and `yolo -- bash` register nothing: yolo cannot see what is started inside them.
//
// It is HOST-SIDE and cannot be verified in a nested jail, which runs in no herdr pane.

// herdrSource is the --source every report carries. It is the spelling the hand-written
// `herdr-yolo-agent` wrapper used, so a machine still running that wrapper converges on one
// registration instead of holding two.
const herdrSource = "yolo-jail"

// herdrOptOutEnv turns the registration off, as YOLO_NO_TMUX does the tmux indicator.
const herdrOptOutEnv = "YOLO_NO_HERDR"

// herdrTimeout bounds each herdr call. The report sits on the launch path and the release
// on the exit path, and a wedged herdr server must cost neither more than this.
const herdrTimeout = 2 * time.Second

// registerHerdrAgent reports the launching agent to the herdr pane this launch runs in, and
// returns the release, or nil when nothing was registered. The release is idempotent: both
// exit arms call it.
//
// NEVER FATAL. herdr is an observer of the launch, so a failed report prints one line and
// the launch proceeds unregistered.
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
	bin := o.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	idArgs := []string{"--source", herdrSource, "--agent", agent}

	report := append([]string{bin, "pane", "report-agent", pane}, idArgs...)
	res := o.Exec(append(report, "--state", "unknown"), "", nil, herdrTimeout)
	if !res.Ran || res.RC != 0 || res.Timeout {
		why := strings.TrimSpace(res.Stderr)
		if why == "" {
			why = "no output"
		}
		o.pr(o.Stderr).print("[yellow]herdr: could not register this pane as " + agent +
			" (" + why + "); its sidebar will show a shell. " + herdrOptOutEnv +
			"=1 turns the registration off.[/yellow]")
		return nil
	}
	o.pr(o.Stderr).print("[dim]herdr: pane " + pane + " registered as " + agent +
		" (jailed, source " + herdrSource + ")[/dim]")

	var once sync.Once
	return func() {
		once.Do(func() {
			o.Exec(append([]string{bin, "pane", "release-agent", pane}, idArgs...), "", nil, herdrTimeout)
		})
	}
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

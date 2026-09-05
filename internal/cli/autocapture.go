package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// autocapture.go is run.Options.AutoCapture's implementation: the launch-time act that
// FILLS the install-capture store, so that materialize has something to hit.
//
// # The ruling this exists to satisfy
//
// docs/design/program-delivery.md OQ-PD18, 2026-09-04: *"(d), DEFAULT ON."* Auto-capture
// on first launch, host-side, in the throwaway jail, with no knob to turn it ON. Before
// this, `yolo capture` was the store's only writer and no launch path called it — the
// maintainer who commissioned the subsystem had never run it, so on every machine the
// store was empty and `_try_materialize` had never once hit.
//
// # A caller and a decision, not a second capture path
//
// captureHost (capturehost.go) is already the whole act: resolve the declaration through
// the origin gate, stage inside the store, run the ordinary run pipeline against that
// scratch workspace, admit the proto-entry, write the receipt beside it. This file adds
// exactly two things it does not have — *should we?* and *say what it costs* — and calls
// it. Everything captureHost refuses, this refuses; everything it locks, this locks.
//
// # What that inheritance buys, spelled out because it is the safety property
//
//   - THE LOCK. captureHost takes a per-program flock (tryFlockAt, captureLockPath) and
//     REFUSES rather than waiting when it cannot. Two workspaces launching at once
//     therefore cannot both capture the same program: the loser skips and its launch
//     installs lazily, which is precisely the pre-capture status quo. Taking the lock
//     again out here would be self-contention rather than extra safety: flock is per
//     open file description, so a second open of the same path in the same process
//     fails its own non-blocking acquire against the fd this one holds — the trigger
//     would take the lock and then watch captureHost refuse it, every time, and nothing
//     would ever be captured.
//   - A FAILED INSTALL ADMITS NOTHING, which matters far more now that installers run
//     unasked. There are three gates and a capture must pass all of them: the launcher
//     itself exits 1 under YOLO_INSTALL_ONLY when $REAL_BIN is not executable
//     afterwards, so an installer that lands nothing fails its jail; capture.Run
//     propagates a non-zero installer as an error, so the driver writes no manifest; and
//     captureHost refuses an empty delta by name. The live case is copilot, whose
//     installer takes PREFIX="${PREFIX:-/usr/local}" on its root branch and exits 1 under
//     the jail's uid 0 + --read-only (program-delivery.md §3.5: *"self-updates once
//     native" is necessary and never sufficient*). It is `via: "npm"` today and so is not
//     a candidate at all — but the day it is flipped, an auto-capture of it stores
//     nothing and warns, rather than filing an entry that materializes a program that
//     is not there.
//
// # Failure is never fatal, and the retry is deliberate
//
// Network down, installer serves HTML, disk full, EXDEV on admit: warn once, name the
// program, continue the launch. The same discipline materialize's silent miss follows
// (internal/entrypoint/shims.go), one notch louder because nobody asked for this one.
//
// A failure is NOT remembered, so the next launch tries again. That is the honest default
// — the overwhelmingly common cause is a transient network, and a negative memo would be
// per-machine state with no expiry rule anyone has ruled on — but it does mean a program
// whose installer fails *every* time re-pays its attempt once per launch. If that is ever
// observed on a shipped pack, the fix is a stamp beside the store, not a special case
// here.

// NoAutoCaptureEnv is the escape hatch out of the trigger.
//
// ANY NON-EMPTY VALUE, the YOLO_ALLOW_STALE_IMAGE / YOLO_NO_HOST_LOOPBACK convention, and
// loud in the same way: it reports what it suppressed, naming the programs, and only when
// there was something to suppress. Default-on is the ruling; un-turn-off-able was not —
// a first launch on a metered connection is a real reason to say no, and a user who
// cannot say no reaches for `--help` and finds nothing.
const NoAutoCaptureEnv = "YOLO_NO_AUTO_CAPTURE"

// autoCapture records every program in bins that has no store entry for platform.
//
// bins is the selected packs' `via: "installer"` program set and platform is the JAIL's
// (run.containerJailPlatform) — both decided by the pipeline, because only it knows the
// pack set and which backend is about to run. Reading either here would be the
// host's-platform bug the seam exists to make unrepresentable.
//
// It returns nothing. There is no outcome a launch could act on: a capture that worked
// changes nothing about the launch, and one that failed has already said so.
func autoCapture(bins []string, platform string, out, errw io.Writer, color bool) {
	store := &capture.Store{Dir: paths.CapturesDir()}
	var missing []string
	for _, bin := range bins {
		// A MISS IS THE TRIGGER, and it is asked through the same resolver the
		// launcher's materialize asks through (resolveCaptureFor) rather than through a
		// cheaper "is there any entry" test. The two must agree exactly: an entry this
		// says exists but that one would not select is an entry the jail re-downloads
		// while the store looks full, and the reverse would re-capture forever. Deriving
		// the question from the reader is what keeps them from drifting — the same
		// argument OQ-PD17 makes for deriving the REAP from it.
		if _, _, err := resolveCaptureFor(store, bin, platform); err == nil {
			continue
		}
		missing = append(missing, bin)
	}
	if len(missing) == 0 {
		return
	}
	if os.Getenv(NoAutoCaptureEnv) != "" {
		fmt.Fprintf(errw, "Warning: %s is set — NOT capturing %s for %s.\n"+
			"  Each will be downloaded by the vendor installer in this workspace, and in\n"+
			"  every other workspace on this machine. Unset it to fill the store instead.\n"+
			"  docs/design/program-delivery.md §6.3\n",
			NoAutoCaptureEnv, humanList(missing), platform)
		return
	}

	pr := richtext.Printer{W: out, Color: color}
	// THE COST IS STATED WHERE IT IS PAID. The first launch on a fresh machine grows by
	// one installer download per uncaptured program (~205 MiB for claude, measured
	// 2026-09-03), and a launch that silently took minutes longer than the last one is
	// the thing a user files a bug about.
	pr.Printf("[bold]auto-capture[/bold]  %d %s never recorded on this machine: [cyan]%s[/cyan]",
		len(missing), plural(len(missing), "program", "programs"), humanList(missing))
	pr.Printf("[dim]  Each is installed once now, in a jail of its own, so this and every "+
		"other workspace\n  materialize it instead of downloading it. This launch pays one "+
		"installer download\n  per program. Set %s=1 to skip.[/dim]", NoAutoCaptureEnv)

	for i, bin := range missing {
		pr.Printf("[dim]  [%d/%d][/dim] %s", i+1, len(missing), bin)
		if rc := captureHost([]string{bin}, out, errw, color); rc != 0 {
			// ONE WARNING, NAMING THE PROGRAM, and then on with the launch. captureHost
			// has already printed what went wrong; what it cannot say is that nothing
			// downstream depends on it, which is the sentence a user needs in order not
			// to stop and investigate a jail that is about to work fine.
			fmt.Fprintf(errw, "Warning: could not capture %s (see above) — nothing was "+
				"stored, and this launch continues.\n"+
				"  %s will install the ordinary way, one download per workspace. "+
				"The next launch retries.\n", bin, bin)
		}
	}
}

// humanList renders a bin list for one line of prose: "claude", "claude and agy",
// "claude, agy and probetool".
func humanList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	head := names[:len(names)-1]
	s := head[0]
	for _, n := range head[1:] {
		s += ", " + n
	}
	return s + " and " + names[len(names)-1]
}

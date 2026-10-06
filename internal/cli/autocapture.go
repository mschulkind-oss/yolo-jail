package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
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
//     captureHost refuses an empty delta by name. The case that showed it is copilot,
//     whose installer takes PREFIX="${PREFIX:-/usr/local}" on its root branch and exits 1
//     under the jail's uid 0 + --read-only (program-delivery.md §3.5: *"self-updates once
//     native" is necessary and never sufficient*). Its recipe now sets PREFIX
//     (`installer_env`, OQ-NI1), so it is captured like claude; an installer that lands
//     nothing still stores nothing and warns, rather than filing an entry that
//     materializes a program that is not there.
//
// # Failure is never fatal, and it is remembered
//
// Network down, installer serves HTML, disk full, EXDEV on admit: warn once, name the
// program, continue the launch. The same discipline materialize's silent miss follows
// (internal/entrypoint/shims.go), one notch louder because nobody asked for this one.
//
// A FAILURE IS REMEMBERED, per program and platform on this machine, by a memo beside the
// store (capture.AutoFailure), and the launches that follow BACK OFF (OQ-PD26). It used to be
// forgotten, on the argument that the common cause is a transient network — and then a program
// whose capture fails EVERY time re-pays its installer on every launch. That was observed:
// every Apple Container launch ran claude's, codex's and agy's installers and stored nothing,
// 61 s of every fresh launch (MEASURED, docs/research/macos-backend-performance.md §8). The
// back-off keeps the transient case cheap: the first retry is a day away, each further failure
// doubles the wait up to a week, and a different yolo (which may be the fix) retries at once.
// The launch that fails says when it will try again and the two next steps — `yolo capture
// <bin>` to retry now, NoAutoCaptureEnv to stop — and the launches inside the back-off say
// nothing, because nothing new happened.
//
// LOSING THE LOCK IS NOT A FAILURE. Another workspace capturing the same program is the
// pre-capture status quo for this launch, and remembering it would hold the next launch off for
// a day behind a capture that may be storing the program right now.

// NoAutoCaptureEnv is the escape hatch out of the trigger.
//
// ANY NON-EMPTY VALUE, the YOLO_ALLOW_STALE_IMAGE / YOLO_NO_HOST_LOOPBACK convention, and
// loud in the same way: it reports what it suppressed, naming the programs, and only when
// there was something to suppress. Default-on is the ruling; un-turn-off-able was not —
// a first launch on a metered connection is a real reason to say no, and a user who
// cannot say no reaches for `--help` and finds nothing.
const NoAutoCaptureEnv = "YOLO_NO_AUTO_CAPTURE"

// autoCaptureNow and autoCaptureVersion are the back-off's two inputs, vars so a test can step a
// machine through launches days apart and through a yolo update.
var (
	autoCaptureNow     = time.Now
	autoCaptureVersion = version.Baked
)

// The back-off after a failed auto-capture (OQ-PD26): the first retry waits a day, each further
// consecutive failure under the same yolo doubles the wait, and no wait exceeds a week.
const (
	autoCaptureFirstBackoff = 24 * time.Hour
	autoCaptureMaxBackoff   = 7 * 24 * time.Hour
)

// autoCaptureRetryAt is when the launches after failure f may try again.
func autoCaptureRetryAt(f capture.AutoFailure) time.Time {
	wait := autoCaptureFirstBackoff
	for i := 1; i < f.Failures && wait < autoCaptureMaxBackoff; i++ {
		wait *= 2
	}
	if wait > autoCaptureMaxBackoff {
		wait = autoCaptureMaxBackoff
	}
	return f.Last.Add(wait)
}

// autoCaptureBackedOff reports whether a failure this yolo remembered for (bin, platform) still
// holds the trigger off. A memo from another yolo holds nothing: the update may be the fix.
func autoCaptureBackedOff(store *capture.Store, bin, platform string) bool {
	f, ok := store.AutoFailure(bin, platform)
	return ok && f.Version == autoCaptureVersion() && autoCaptureNow().Before(autoCaptureRetryAt(f))
}

// autoCapture records every program in bins that has no store entry for platform, unless a
// failure this yolo remembered for it is still backing off (autoCaptureBackedOff).
//
// bins is the selected packs' `via: "installer"` program set and platform is the JAIL's
// (run.containerJailPlatform, or run.macosUserJailPlatform for the macos-user sandbox) — both
// decided by the pipeline, because only it knows the pack set and which backend is about to
// run. Reading either here would be the host's-platform bug the seam exists to make
// unrepresentable. The platform also picks the capture act (autoCaptureActFor).
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
	// A PROGRAM WHOSE CAPTURE FAILED IS HELD OFF, silently: the launch that failed said what
	// happened and when the next try is, and nothing has changed since.
	var due []string
	for _, bin := range missing {
		if !autoCaptureBackedOff(store, bin, platform) {
			due = append(due, bin)
		}
	}
	missing = due
	if len(missing) == 0 {
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

	act := autoCaptureActFor(platform)
	for i, bin := range missing {
		pr.Printf("[dim]  [%d/%d][/dim] %s", i+1, len(missing), bin)
		// A capture that stores the program clears its memo inside captureHost, as `yolo capture
		// <bin>` and the host floor's capture do, so a success needs nothing more here.
		if rc := captureHostWith([]string{bin}, out, errw, color, act); rc != 0 {
			autoCaptureFailed(store, bin, platform, errw)
		}
	}
}

// autoCaptureActFor is the capture act that records an entry for platform. A DARWIN platform is a
// macos-user launch's (run.macosUserJailPlatform), and the one act that records a darwin entry is
// the macos-user capture act, so it is named for this act alone (captureAct.runtime), as the Mac's
// host floor names it (HP-D2). Left to the runtime a capture resolves — YOLO_RUNTIME, then the
// USER config, the scratch workspace carrying none — a launch whose workspace config chose
// macos-user over a user config's podman would record a linux entry for every program, miss it at
// the next launch, and capture again: a store that never hits while looking full. Every other
// platform is a container jail's, and keeps the runtime a capture resolves, as before.
func autoCaptureActFor(platform string) captureAct {
	if strings.HasPrefix(platform, "darwin/") {
		return captureAct{runtime: "macos-user"}
	}
	return captureAct{}
}

// autoCaptureFailed is what a launch does about one capture that did not store its program:
// remember it, unless another process holds the program's capture, and say so ONCE.
//
// ONE WARNING, NAMING THE PROGRAM, and then on with the launch. captureHost has already printed
// what went wrong; what it cannot say is that nothing downstream depends on it, which is the
// sentence a user needs in order not to stop and investigate a jail that is about to work fine
// — and, now that the failure is remembered, when the next try is and how to have it sooner.
func autoCaptureFailed(store *capture.Store, bin, platform string, errw io.Writer) {
	// ANOTHER CAPTURE OF IT: the lock is held right now, or the holder has already stored it.
	// Neither is this program failing, so nothing is remembered.
	if _, _, err := resolveCaptureFor(store, bin, platform); err == nil || captureBusy(bin) {
		fmt.Fprintf(errw, "Warning: could not capture %s (see above) — another capture of it is "+
			"running, and this launch continues.\n"+
			"  %s installs the ordinary way this time; a later launch uses what that capture "+
			"stores.\n", bin, bin)
		return
	}
	now := autoCaptureNow()
	f := capture.AutoFailure{Bin: bin, Platform: platform, Version: autoCaptureVersion(), Failures: 1, Last: now}
	if prev, ok := store.AutoFailure(bin, platform); ok && prev.Version == f.Version {
		f.Failures = prev.Failures + 1
	}
	fmt.Fprintf(errw, "Warning: could not capture %s (see above) — nothing was stored, and this "+
		"launch continues.\n"+
		"  %s will install the ordinary way, one download per workspace.\n", bin, bin)
	if err := store.RecordAutoFailure(f); err != nil {
		// Unremembered, the next launch tries again, which is the old behaviour: say so, and
		// what stopped the memo.
		fmt.Fprintf(errw, "  The next launch tries again: this failure could not be remembered "+
			"(%v).\n  To stop auto-capture: %s=1\n", err, NoAutoCaptureEnv)
		return
	}
	fmt.Fprintf(errw, "  Launches will not try to capture it again until %s, or until yolo is "+
		"updated.\n  To retry now: yolo capture %s    To stop auto-capture: %s=1\n",
		autoCaptureRetryAt(f).Local().Format("2006-01-02 15:04 MST"), bin, NoAutoCaptureEnv)
}

// captureBusy reports whether another process holds bin's capture lock right now. Asked after
// captureHost has returned and released its own hold, so a held lock is someone else's.
func captureBusy(bin string) bool {
	l := tryFlockAt(captureLockPath(bin))
	if l == nil {
		return true
	}
	l.Close()
	return false
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

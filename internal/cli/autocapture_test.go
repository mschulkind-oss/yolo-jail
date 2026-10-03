package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// autocapture_test.go drives the trigger's DECISION half — the part internal/cli owns —
// with the run pipeline substituted, exactly as capturehost_test.go does.
//
// The other half (does a launch call this at all?) is pinned from Run's own entry point
// in internal/cli/run/autocapture_test.go, and the WIRING between the two is pinned by
// TestALaunchWiresTheAutoCaptureTrigger below. All three are needed: the seam can be
// unwired, the pipeline can stop calling it, and the decision can stop capturing, and
// each is invisible to the other two's tests.

// capturedPlatform is the platform fakeCaptureJail stamps into the manifest it writes, so
// the receipt captureHost derives from it — and therefore the platform resolveCaptureFor
// must be asked about for a hit.
const capturedPlatform = "linux/arm64"

// probetoolEntries is a delta that LANDED THE PROGRAM: the launcher's $REAL_BIN
// ($HOME/.local/bin/<bin>) is among the captured paths, which is what an installer that
// worked leaves behind.
func probetoolEntries() []capture.ManifestEntry {
	return []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 64},
	}
}

// TestAutoCaptureRecordsAMissAndSkipsAHit is the decision itself, both ways round.
//
// Both halves in one test on purpose: "captures when it should" and "does not capture
// when it should not" are the same claim about the same predicate, and a second launch
// against the store the first one filled is the only way to assert the second half
// against real state rather than against a stub.
func TestAutoCaptureRecordsAMissAndSkipsAHit(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	jail := fakeCaptureJail(t, &seen, probetoolEntries())
	captures := 0
	withFakeCaptureJail(t, func(o run.Options) int { captures++; return jail(o) })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)

	if captures != 1 {
		t.Fatalf("a miss ran %d capture jails, want 1\nstdout:\n%s\nstderr:\n%s",
			captures, out.String(), errw.String())
	}
	// The entry is in the store AND is the one the launcher's materialize would select:
	// asked through resolveCaptureFor, the same reader, so "captured" and "will be found"
	// are one answer.
	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	if _, _, err := resolveCaptureFor(store, "probetool", capturedPlatform); err != nil {
		t.Fatalf("after auto-capture the store still has no entry the launcher would "+
			"select: %v", err)
	}
	// THE COST IS STATED WHERE IT IS PAID, and the escape hatch is named with it.
	for _, want := range []string{"probetool", "installer download", NoAutoCaptureEnv} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the auto-capture notice does not mention %q:\n%s", want, out.String())
		}
	}

	// SECOND LAUNCH, same machine: a hit, and no second download.
	out.Reset()
	errw.Reset()
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 1 {
		t.Errorf("a launch with the program already captured ran %d capture jails; "+
			"the store hit must skip it entirely", captures)
	}
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("a launch with nothing to capture must be silent, got:\n%s%s",
			out.String(), errw.String())
	}
}

// TestAutoCaptureIsPlatformKeyed: an entry recorded for one platform is not an entry for
// another. This is the failure OQ-PD18's trap describes from the other side — a store
// that looks full and never hits — reproduced here by asking for the wrong one.
func TestAutoCaptureIsPlatformKeyed(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	jail := fakeCaptureJail(t, &seen, probetoolEntries())
	captures := 0
	withFakeCaptureJail(t, func(o run.Options) int { captures++; return jail(o) })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 1 {
		t.Fatalf("setup: %d captures, want 1 (%s)", captures, errw.String())
	}
	// A host that asked with runtime.GOOS on a Mac would ask exactly this.
	autoCapture([]string{"probetool"}, "darwin/arm64", &out, &errw, false)
	if captures != 2 {
		t.Errorf("a linux/arm64 entry satisfied a darwin/arm64 lookup — the store is "+
			"platform-keyed and a cross-platform hit would materialize the wrong bytes "+
			"(%d captures)", captures)
	}
}

// TestAutoCaptureStoresNothingWhenTheInstallerLandsNothing is the copilot shape, and the
// reason this slice can be default-on at all.
//
// program-delivery.md §3.5: *"self-updates once native" is necessary and never
// sufficient; the sufficient question is whether the installer's default prefix, under
// the UID and filesystem the jail actually runs with, equals $REAL_BIN.* copilot's
// installer takes PREFIX="${PREFIX:-/usr/local}" on its root branch and exits 1 under the
// jail's uid 0 + --read-only. A trigger that runs installers unasked must not turn that
// into a stored entry, because an admitted entry SATISFIES EVERY LATER RESOLVE — the
// store would answer "yes, I have copilot" forever, and every materialize would put
// nothing in the home.
//
// The fixture is the empty-delta shape (the jail exits 0 having written no paths), which
// is captureHost's own named refusal. The exit-non-zero shape is the sibling below.
func TestAutoCaptureStoresNothingWhenTheInstallerLandsNothing(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, nil))

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)

	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	keys, err := store.EntryKeys()
	if err != nil {
		t.Fatalf("EntryKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("an installer that landed nothing produced %d store entries (%v); such "+
			"an entry satisfies every later resolve and materializes an absent program",
			len(keys), keys)
	}
	if !strings.Contains(errw.String(), "probetool") {
		t.Errorf("the failed capture did not name the program:\n%s", errw.String())
	}
}

// TestAutoCaptureNeverFailsTheLaunch: a capture jail that exits non-zero — the shape a
// network outage, a served web page, or an installer that refuses its prefix produces —
// warns once, names the program, and returns.
//
// autoCapture returns nothing precisely so that no caller can accidentally make this
// fatal; what is asserted here is that it RETURNS at all (no panic, no os.Exit) and that
// the warning is legible.
func TestAutoCaptureNeverFailsTheLaunch(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	withFakeCaptureJail(t, func(run.Options) int { return 1 })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)

	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	if keys, _ := store.EntryKeys(); len(keys) != 0 {
		t.Errorf("a capture jail that exited non-zero still admitted %v", keys)
	}
	warn := errw.String()
	if !strings.Contains(warn, "probetool") || !strings.Contains(warn, "continues") {
		t.Errorf("a failed capture must warn once, name the program, and say the launch "+
			"goes on; got:\n%s", warn)
	}
}

// TestAutoCaptureSkipsWhileAnotherWorkspaceIsCapturing is slice 7's lock bullet, asserted
// where the trigger meets it: two workspaces launching at once must not both capture, and
// the loser SKIPS rather than waits — its launch installs lazily, the pre-capture status
// quo.
//
// The lock is captureHost's (TestCaptureRefusesWhileAnotherCaptureHoldsTheLock pins the
// refusal itself); what this adds is the trigger's disposition toward it. Taking the lock
// out here as well would be self-contention rather than extra safety, so the only thing
// left to check is that a contended capture is a warning and not a stalled launch.
func TestAutoCaptureSkipsWhileAnotherWorkspaceIsCapturing(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	held := tryFlockAt(captureLockPath("probetool"))
	if held == nil {
		t.Skip("flock is a no-op on this filesystem")
	}
	defer held.Close()
	// Same guard the sibling test carries: flock is per open file description, so this
	// only models a second PROCESS where the OS makes a second fd conflict.
	if probe := tryFlockAt(captureLockPath("probetool")); probe != nil {
		probe.Close()
		t.Skip("this filesystem does not make a second flock on the same file conflict")
	}
	withFakeCaptureJail(t, func(run.Options) int {
		t.Error("no capture jail may launch while another workspace holds the lock")
		return 0
	})

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)

	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	if keys, _ := store.EntryKeys(); len(keys) != 0 {
		t.Errorf("the losing launch admitted %v", keys)
	}
	if !strings.Contains(errw.String(), "probetool") {
		t.Errorf("the skipped capture did not name the program:\n%s", errw.String())
	}
}

// TestAutoCaptureHonorsTheEscapeHatch: YOLO_NO_AUTO_CAPTURE suppresses the trigger,
// loudly, naming what it suppressed — the YOLO_NO_HOST_LOOPBACK convention.
//
// Default-on is the ruling; un-turn-off-able was not.
func TestAutoCaptureHonorsTheEscapeHatch(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	captures := 0
	withFakeCaptureJail(t, func(run.Options) int { captures++; return 0 })
	t.Setenv(NoAutoCaptureEnv, "1")

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)

	if captures != 0 {
		t.Fatalf("%s=1 still ran %d capture jails", NoAutoCaptureEnv, captures)
	}
	for _, want := range []string{NoAutoCaptureEnv, "probetool"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the opt-out notice does not mention %q:\n%s", want, errw.String())
		}
	}
}

// TestAutoCaptureSaysNothingWhenTheHatchSuppressesNothing: the other half of "loud".
// A machine whose store is already full must not print an opt-out notice about work that
// was never going to happen — the same rule decideHostLoopback follows, where the opt-out
// reports what it suppressed rather than that it exists.
func TestAutoCaptureSaysNothingWhenTheHatchSuppressesNothing(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, probetoolEntries()))

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if errw.Len() != 0 {
		t.Fatalf("setup capture warned: %s", errw.String())
	}

	t.Setenv(NoAutoCaptureEnv, "1")
	out.Reset()
	errw.Reset()
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("the opt-out spoke about a store that had nothing to capture:\n%s%s",
			out.String(), errw.String())
	}
}

// TestALaunchWiresTheAutoCaptureTrigger pins the WIRING — the one line in runRun that
// hands the pipeline its AutoCapture closure.
//
// IT INVOKES THE CLOSURE, for the reason TestCaptureWiresTheMacosUserBackend gives: a
// first version of that test asserted only non-nil-ness and stayed green when the wiring
// was reverted to a refusal, because a refusal closure is non-nil too. Here the closure
// is driven against the fixture home and the fake jail, so what is asserted is that the
// thing `yolo run` wires actually reaches captureHost.
//
// Delete `opts.AutoCapture = …` from runRun and this goes red; the pipeline-side test
// cannot see that deletion, because it injects its own seam.
func TestALaunchWiresTheAutoCaptureTrigger(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)

	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })

	if rc := runRun([]string{"run", "--", "true"}); rc != 0 {
		t.Fatalf("runRun = %d, want 0 with the pipeline stubbed", rc)
	}
	if seen.AutoCapture == nil {
		t.Fatal("`yolo run` did not wire Options.AutoCapture: OQ-PD18's trigger is dead, " +
			"and the pipeline-side test cannot see it because it injects its own seam")
	}

	var jailSeen run.Options
	captures := 0
	jail := fakeCaptureJail(t, &jailSeen, probetoolEntries())
	withFakeCaptureJail(t, func(o run.Options) int { captures++; return jail(o) })

	// The real closure writes to os.Stdout/os.Stderr; what matters is where it lands.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	seen.AutoCapture([]string{"probetool"}, capturedPlatform)
	os.Stdout, os.Stderr = stdout, stderr

	if captures != 1 {
		t.Errorf("the wired closure ran %d capture jails, want 1 — it is wired to "+
			"something that is not the capture act", captures)
	}
}

// withAutoCaptureClock and withAutoCaptureVersion pin the two inputs the back-off reads, so a
// test can step a machine through launches days apart and through a yolo update.
func withAutoCaptureClock(t *testing.T, now *time.Time) {
	t.Helper()
	prev := autoCaptureNow
	autoCaptureNow = func() time.Time { return *now }
	t.Cleanup(func() { autoCaptureNow = prev })
}

func withAutoCaptureVersion(t *testing.T, v *string) {
	t.Helper()
	prev := autoCaptureVersion
	autoCaptureVersion = func() string { return *v }
	t.Cleanup(func() { autoCaptureVersion = prev })
}

// TestAFailedAutoCaptureIsNotRetriedOnEveryLaunch is the retry storm, in the shape it was
// measured: on Apple Container every launch found claude, codex and agy never recorded, ran each
// installer, stored nothing because each "left nothing in the capture surfaces", and did it again
// on the next launch (docs/research/macos-backend-performance.md §8). The fixture is that empty
// delta.
//
// A failure is remembered for this program on this machine. The launch that failed says so ONCE,
// with when it will try again and the two next steps; the launches inside the back-off neither
// capture nor speak; the first launch after it tries again, and a second failure waits longer.
//
// Red before the memo: every call ran a capture jail.
func TestAFailedAutoCaptureIsNotRetriedOnEveryLaunch(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	withAutoCaptureClock(t, &now)
	ver := "0.11.2"
	withAutoCaptureVersion(t, &ver)
	var seen run.Options
	jail := fakeCaptureJail(t, &seen, nil) // exits 0 having landed nothing
	captures := 0
	withFakeCaptureJail(t, func(o run.Options) int { captures++; return jail(o) })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 1 {
		t.Fatalf("the first launch ran %d capture jails, want 1\n%s", captures, errw.String())
	}
	warn := errw.String()
	// When it tries again — a day after the failure — in the reader's own zone.
	retry := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
	for _, want := range []string{
		"could not capture probetool",
		retry,
		"yolo capture probetool", // the retry a human can run now
		NoAutoCaptureEnv + "=1",  // and the way to stop it altogether
		"this launch continues",
	} {
		if !strings.Contains(warn, want) {
			t.Errorf("the failure warning does not say %q:\n%s", want, warn)
		}
	}

	// An hour later, and again just before the day is up: no capture, and nothing said.
	for _, step := range []time.Duration{time.Hour, 22*time.Hour + 59*time.Minute} {
		now = now.Add(step)
		out.Reset()
		errw.Reset()
		autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
		if captures != 1 {
			t.Fatalf("a launch %s after the failure ran the installer again (%d captures): "+
				"a failure must not be retried on every launch", now.Sub(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)), captures)
		}
		if out.Len() != 0 || errw.Len() != 0 {
			t.Errorf("a launch inside the back-off spoke; the failure was said once:\n%s%s",
				out.String(), errw.String())
		}
	}

	// Past the day: tried again.
	now = time.Date(2026, 10, 4, 12, 0, 1, 0, time.UTC)
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 2 {
		t.Fatalf("the first launch after the back-off did not try again (%d captures)", captures)
	}
	// It failed again, so the next wait is longer than the first: a day later is still inside it.
	now = now.Add(25 * time.Hour)
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 2 {
		t.Errorf("a second failure was retried after one day (%d captures); the back-off "+
			"doubles", captures)
	}
}

// TestAnInstallerThatExitsNonZeroIsBackedOffToo: the other failure shape — network down, a
// served web page, a refused prefix — is remembered the same way.
func TestAnInstallerThatExitsNonZeroIsBackedOffToo(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	withAutoCaptureClock(t, &now)
	captures := 0
	withFakeCaptureJail(t, func(run.Options) int { captures++; return 1 })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	now = now.Add(time.Hour)
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 1 {
		t.Errorf("a capture jail that exited non-zero was retried an hour later (%d captures)", captures)
	}
}

// TestANewYoloRetriesABackedOffCapture: a failure is remembered against the yolo that failed. A
// different yolo may be the fix — the Apple Container one was — so its first launch tries at
// once rather than waiting out a back-off its predecessor earned.
func TestANewYoloRetriesABackedOffCapture(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	withAutoCaptureClock(t, &now)
	ver := "0.11.2"
	withAutoCaptureVersion(t, &ver)
	captures := 0
	withFakeCaptureJail(t, func(run.Options) int { captures++; return 1 })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	ver = "0.11.3"
	now = now.Add(time.Minute)
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 2 {
		t.Errorf("an updated yolo waited out its predecessor's back-off (%d captures, want 2)", captures)
	}
}

// TestASuccessfulAutoCaptureClearsTheMemo: once the program is recorded the memo has nothing left
// to say, and a later failure (after `yolo prune`, say) starts from the first wait again.
func TestASuccessfulAutoCaptureClearsTheMemo(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	withAutoCaptureClock(t, &now)
	var seen run.Options
	fail := true
	jail := fakeCaptureJail(t, &seen, probetoolEntries())
	withFakeCaptureJail(t, func(o run.Options) int {
		if fail {
			return 1
		}
		return jail(o)
	})

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	if _, ok := store.AutoFailure("probetool", capturedPlatform); !ok {
		t.Fatalf("the failure was not remembered, so this test asserts nothing\n%s", errw.String())
	}
	fail = false
	now = now.Add(48 * time.Hour)
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if _, _, err := resolveCaptureFor(store, "probetool", capturedPlatform); err != nil {
		t.Fatalf("the retry did not store the program: %v\n%s", err, errw.String())
	}
	if f, ok := store.AutoFailure("probetool", capturedPlatform); ok {
		t.Errorf("the memo outlived the capture that succeeded: %+v", f)
	}
}

// TestAContendedAutoCaptureIsNotRememberedAsAFailure: losing the lock to another workspace's
// capture of the same program is not a failure of the program. Remembered, it would hold off the
// next launch for a day behind a capture that may well be storing the program right now.
func TestAContendedAutoCaptureIsNotRememberedAsAFailure(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	held := tryFlockAt(captureLockPath("probetool"))
	if held == nil {
		t.Skip("flock is a no-op on this filesystem")
	}
	if probe := tryFlockAt(captureLockPath("probetool")); probe != nil {
		probe.Close()
		held.Close()
		t.Skip("this filesystem does not make a second flock on the same file conflict")
	}
	var seen run.Options
	captures := 0
	jail := fakeCaptureJail(t, &seen, probetoolEntries())
	withFakeCaptureJail(t, func(o run.Options) int { captures++; return jail(o) })

	var out, errw bytes.Buffer
	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	held.Close()
	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	if f, ok := store.AutoFailure("probetool", capturedPlatform); ok {
		t.Errorf("losing the lock was remembered as a failure: %+v\n%s", f, errw.String())
	}
	if !strings.Contains(errw.String(), "another capture") {
		t.Errorf("the contended launch does not say another capture is running:\n%s", errw.String())
	}

	autoCapture([]string{"probetool"}, capturedPlatform, &out, &errw, false)
	if captures != 1 {
		t.Errorf("the next launch, with the lock free, ran %d capture jails, want 1", captures)
	}
}

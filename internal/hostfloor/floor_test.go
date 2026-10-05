package hostfloor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestAnNpmProgramInstallsOnTheFloorsOwnNodeAndStartsWithNoPATH is §8's first "done looks like"
// row, at this package's reach: after one install, the entry starts from a launcher that names
// the floor's Node by absolute path, so a caller with no PATH at all (a Waybar widget, cron)
// starts the same program. The install itself ran on the floor's Node: the fake npm is only in
// the verified tarball, never on any PATH the test process has.
func TestAnNpmProgramInstallsOnTheFloorsOwnNodeAndStartsWithNoPATH(t *testing.T) {
	w := newWorld(t)
	w.publish("opencode-ai", "1.2.3", "bin=opencode")
	p := npmProgram("opencode", "opencode", "opencode-ai")

	if st := w.floor.Status(p); st.Disposition != Missing {
		t.Fatalf("before any install: %s (%s), want missing", st.Disposition, st.Reason)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if outcome != Installed || st.Disposition != Provisioned {
		t.Fatalf("after Ensure: outcome %s, disposition %s", outcome, st.Disposition)
	}
	rec := st.Record
	if rec.Version != "1.2.3" || rec.Node != w.version || rec.Declared != "opencode-ai@latest" {
		t.Errorf("record = %+v", rec)
	}
	node := filepath.Join(w.floor.Dir, "node", "v"+w.version, "bin", "node")
	if len(rec.Exec) != 2 || rec.Exec[0] != node || rec.Exec[1] != rec.Entry {
		t.Fatalf("Exec = %q, want [%s %s]: an npm program starts on the floor's Node by path", rec.Exec, node, rec.Entry)
	}
	// Started with an EMPTY environment: nothing on PATH, no HOME — the launcher needs neither.
	cmd := exec.Command(st.Launcher, "--flag", "arg two")
	cmd.Env = []string{}
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %s with no environment: %v\n%s", st.Launcher, err, got)
	}
	if want := "node:" + rec.Entry + " --flag arg two\n"; string(got) != want {
		t.Errorf("launcher ran %q, want %q", got, want)
	}
	out := w.out.String()
	for _, want := range []string{"installing opencode into yolo's floor", "fetching Node v" + w.version,
		"verified node-v" + w.version, "installed opencode 1.2.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("progress lines lack %q:\n%s", want, out)
		}
	}
	// bin/ holds exactly the one floor name: no node, no npm (§3).
	entries, _ := os.ReadDir(w.floor.BinDir())
	if len(entries) != 1 || entries[0].Name() != "opencode" {
		t.Errorf("bin/ holds %v, want only opencode", entries)
	}
	if fi, err := os.Stat(w.floor.Dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the prefix's mode is %v (%v), want 0700", fi.Mode().Perm(), err)
	}
}

// TestTheInstallerEnvironmentIsSetNotInherited pins the environment npm ran with: the ambient
// variables that redirect an npm install or change what Node loads are dropped, and PATH is the
// floor's Node then the baseline — never the caller's.
func TestTheInstallerEnvironmentIsSetNotInherited(t *testing.T) {
	w := newWorld(t)
	env := w.floor.npmEnv("/floor/node/bin", "/floor/programs/x/npm")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, gone := range []string{"/somewhere/else", "/also/else", "evil.js", "PATH=/nowhere"} {
		if strings.Contains(joined, gone) {
			t.Errorf("installer env carries %q:%s", gone, joined)
		}
	}
	for _, want := range []string{"\nNPM_CONFIG_PREFIX=/floor/programs/x/npm\n", "\nPATH=/floor/node/bin:",
		"\nFAKE_NPM_REGISTRY=", "\nHOME=" + w.floor.Home + "\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("installer env lacks %q:%s", want, joined)
		}
	}
}

// TestATarballThatDoesNotMatchItsDigestIsRefused: the shipped release is checked against the
// digest compiled into yolo, and a mismatch installs nothing — no Node, no program, no launcher.
func TestATarballThatDoesNotMatchItsDigestIsRefused(t *testing.T) {
	w := newWorld(t)
	w.floor.Node.Pinned = map[string]string{w.plat: strings.Repeat("0", 64)}
	w.publish("opencode-ai", "1.2.3", "bin=opencode")
	_, _, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai"))
	if err == nil || !strings.Contains(err.Error(), "refusing to install it") {
		t.Fatalf("Ensure = %v, want the digest refusal", err)
	}
	if w.floor.NodeReady(w.version) {
		t.Error("a Node release that failed verification was left in the floor")
	}
	if _, err := os.Stat(w.floor.Launcher("opencode")); err == nil {
		t.Error("a launcher exists for a program whose interpreter was refused")
	}
	if left := w.floor.Leftovers(); len(left) != 0 {
		t.Errorf("the refused install left %v", left)
	}
}

// TestANodeFloorAboveTheShippedReleaseUsesThatReleasesPublishedChecksums is OQ-HP4's "raised to
// the highest selected node_floor": the floor's Node becomes the floor's release, verified against
// that release's SHASUMS256.txt, since yolo cannot have shipped its digest.
func TestANodeFloorAboveTheShippedReleaseUsesThatReleasesPublishedChecksums(t *testing.T) {
	w := newWorld(t)
	p := npmProgram("pi", "pi", "@earendil-works/pi-coding-agent")
	p.Install.NodeFloor = "99.1"
	w.floor.NodeFloor = HighestNodeFloor([]Program{p, npmProgram("x", "x", "x")})
	if got := w.floor.NodeVersion(); got != "99.1.0" {
		t.Fatalf("NodeVersion = %s, want 99.1.0", got)
	}
	w.publish("@earendil-works/pi-coding-agent", "0.9.0", "bin=pi")
	st, _, err := w.floor.Ensure(context.Background(), p)
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if st.Record.Node != "99.1.0" || !strings.Contains(w.out.String(), "SHASUMS256.txt") {
		t.Errorf("node %s; output:\n%s", st.Record.Node, w.out.String())
	}
}

// TestANativeBuildShippedThroughNpmIsStartedAsItself: opencode-ai's bin is an ELF, and wrapping
// it in an interpreter would break it (packdecl.SatisfiesNodeFloor's note).
func TestANativeBuildShippedThroughNpmIsStartedAsItself(t *testing.T) {
	w := newWorld(t)
	w.publish("opencode-ai", "1.2.3", "bin=opencode", "native")
	st, _, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Record.Exec) != 1 || st.Record.Exec[0] != st.Record.Entry {
		t.Errorf("Exec = %q, want the entry alone", st.Record.Exec)
	}
}

// TestTwoFirstLaunchesTogetherProduceOneInstallAndOneReceipt is §8's concurrency row: the second
// waits on the program's lock, says whose, and after the wait finds the entry installed.
func TestTwoFirstLaunchesTogetherProduceOneInstallAndOneReceipt(t *testing.T) {
	w := newWorld(t)
	w.publish("opencode-ai", "1.2.3", "bin=opencode", "sleep=1")
	p := npmProgram("opencode", "opencode", "opencode-ai")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = w.floor.Ensure(context.Background(), p)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("Ensure: %v\n%s", err, w.out.String())
		}
	}
	if n := len(w.npmCalls("install")); n != 1 {
		t.Errorf("npm install ran %d times, want 1", n)
	}
	b, _ := os.ReadFile(w.floor.receiptsPath())
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("receipts.jsonl has %d lines, want 1:\n%s", n, b)
	}
	if !strings.Contains(w.out.String(), "waiting for pid") {
		t.Errorf("the second launch did not say whom it waited for:\n%s", w.out.String())
	}
}

// TestAFailedFirstInstallLeavesNoEntryAndNamesTheInstallersLastLines.
func TestAFailedFirstInstallLeavesNoEntryAndNamesTheInstallersLastLines(t *testing.T) {
	w := newWorld(t)
	w.publish("opencode-ai", "1.2.3", "bin=opencode", "fail")
	_, _, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai"))
	if err == nil || !strings.Contains(err.Error(), "npm ERR! 404") {
		t.Fatalf("Ensure = %v, want the installer's own last line", err)
	}
	if _, err := os.Stat(w.floor.Launcher("opencode")); err == nil {
		t.Error("a failed first install left a launcher")
	}
	dirs, _ := os.ReadDir(w.floor.programsDir("opencode"))
	if len(dirs) != 0 {
		t.Errorf("a failed first install left %d install directories", len(dirs))
	}
}

// TestAKilledInstallTimesOutAndLeavesNothing: the 600 s bound, shortened, kills the installer and
// removes its directory.
func TestAKilledInstallTimesOutAndLeavesNothing(t *testing.T) {
	w := newWorld(t)
	w.floor.InstallTimeout = 300 * time.Millisecond
	w.publish("opencode-ai", "1.2.3", "bin=opencode", "sleep=5")
	start := time.Now()
	_, _, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai"))
	if err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("Ensure = %v, want the timeout", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("the timeout took %s: the installer's process group was not killed", time.Since(start))
	}
	dirs, _ := os.ReadDir(w.floor.programsDir("opencode"))
	if len(dirs) != 0 {
		t.Errorf("a timed-out install left %d install directories", len(dirs))
	}
}

// TestTheEvergreenRefreshIsThrottledAndFollowsAgentUpdates is the jail launcher's cadence at the
// host: no poll inside the interval, none when `agent_updates` freezes the pack, and an update
// once the interval has passed.
func TestTheEvergreenRefreshIsThrottledAndFollowsAgentUpdates(t *testing.T) {
	w := newWorld(t)
	clk := &clock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	w.floor.Now = clk.now
	w.publish("opencode-ai", "1.0.0", "bin=opencode")
	p := npmProgram("opencode", "opencode", "opencode-ai")
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	w.publish("opencode-ai", "2.0.0", "bin=opencode")

	clk.t = clk.t.Add(30 * time.Minute)
	if _, outcome, _ := w.floor.Ensure(context.Background(), p); outcome != Current || len(w.npmCalls("view")) != 0 {
		t.Fatalf("inside the interval: outcome %s, %d polls", outcome, len(w.npmCalls("view")))
	}
	clk.t = clk.t.Add(2 * time.Hour)
	frozen := *w.floor
	frozen.UpdatesAllowed = func(pack string) bool { return pack != "opencode" }
	if _, outcome, _ := frozen.Ensure(context.Background(), p); outcome != Current || len(w.npmCalls("view")) != 0 {
		t.Fatalf("with agent_updates freezing the pack: outcome %s, %d polls", outcome, len(w.npmCalls("view")))
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Updated || st.Record.Version != "2.0.0" {
		t.Fatalf("past the interval: outcome %s, version %v, err %v\n%s", outcome, st.Record, err, w.out.String())
	}
	// The install it replaced is kept; a third version drops the first.
	w.publish("opencode-ai", "3.0.0", "bin=opencode")
	clk.t = clk.t.Add(2 * time.Hour)
	if _, outcome, _ := w.floor.Ensure(context.Background(), p); outcome != Updated {
		t.Fatalf("third version: %s", outcome)
	}
	dirs, _ := os.ReadDir(w.floor.programsDir("opencode"))
	if len(dirs) != 2 {
		t.Errorf("%d install directories after three versions, want the current and the previous", len(dirs))
	}
}

// TestAFailedUpdateKeepsTheInstalledVersionAndWaitsOutTheInterval.
func TestAFailedUpdateKeepsTheInstalledVersionAndWaitsOutTheInterval(t *testing.T) {
	w := newWorld(t)
	clk := &clock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	w.floor.Now = clk.now
	w.publish("opencode-ai", "1.0.0", "bin=opencode")
	p := npmProgram("opencode", "opencode", "opencode-ai")
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	w.publish("opencode-ai", "2.0.0", "bin=opencode", "fail")
	clk.t = clk.t.Add(2 * time.Hour)
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Kept || st.Record.Version != "1.0.0" {
		t.Fatalf("failed update: outcome %s, record %+v, err %v", outcome, st.Record, err)
	}
	polls := len(w.npmCalls("view"))
	clk.t = clk.t.Add(10 * time.Minute)
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil || len(w.npmCalls("view")) != polls {
		t.Errorf("a failed update was retried inside the interval (%d polls, was %d)", len(w.npmCalls("view")), polls)
	}
}

// TestAPinnedPackageIsNeverPolledAndAMovedPinReinstalls.
func TestAPinnedPackageIsNeverPolledAndAMovedPinReinstalls(t *testing.T) {
	w := newWorld(t)
	clk := &clock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	w.floor.Now = clk.now
	w.publish("oh-my-pi", "9.9.9", "bin=omp")
	p := npmProgram("oh-omp", "omp", "oh-my-pi@0.15.3")
	st, _, err := w.floor.Ensure(context.Background(), p)
	if err != nil || st.Record.Version != "0.15.3" {
		t.Fatalf("pinned install: %+v %v", st.Record, err)
	}
	clk.t = clk.t.Add(5 * time.Hour)
	if _, outcome, _ := w.floor.Ensure(context.Background(), p); outcome != Current || len(w.npmCalls("view")) != 0 {
		t.Fatalf("a pinned package was polled (%s, %d views)", outcome, len(w.npmCalls("view")))
	}
	p.Install.Package = "oh-my-pi@0.16.0"
	if st := w.floor.Status(p); st.Disposition != Provisioned || st.Pending == "" {
		t.Fatalf("a moved pin: %s pending %q", st.Disposition, st.Pending)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Record.Version != "0.16.0" {
		t.Fatalf("a moved pin reinstalls: %s %+v %v", outcome, st.Record, err)
	}
}

// TestAnInstallerProgramIsTheMachinesCaptureMaterialized is OQ-HP3 on Linux: the floor's claude
// is the capture store's entry, relocated into the prefix, and it starts from its launcher with
// no environment. The store is never written.
func TestAnInstallerProgramIsTheMachinesCaptureMaterialized(t *testing.T) {
	w := newLinuxWorld(t)
	cs := newCaptureStore(t)
	entry := cs.add("claude", "2.1.267", true)
	w.floor.ResolveCapture = cs.resolve
	w.floor.Capture = func(string) error { t.Fatal("a capture ran although the store had one"); return nil }
	st, outcome, err := w.floor.Ensure(context.Background(), installerProgram("claude", "claude"))
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if outcome != Installed || st.Record.Capture != entry.Key || st.Record.Version != "2.1.267" {
		t.Fatalf("outcome %s record %+v", outcome, st.Record)
	}
	cmd := exec.Command(st.Launcher, "--version")
	cmd.Env = []string{}
	got, err := cmd.CombinedOutput()
	if err != nil || string(got) != "claude-2.1.267 --version\n" {
		t.Fatalf("running the floor's claude: %q %v", got, err)
	}
	link, _ := os.Readlink(st.Record.Entry)
	if !strings.HasPrefix(link, w.floor.Dir) {
		t.Errorf("~/.local/bin/claude links to %s, not into the floor: the capture was not relocated", link)
	}
}

// TestACaptureRecordedForTheJailHomeOnlyIsRecapturedOnce: an entry whose manifest lacks the full
// scan cannot move out of /home/agent, so the floor runs one capture, which records the scan.
func TestACaptureRecordedForTheJailHomeOnlyIsRecapturedOnce(t *testing.T) {
	w := newLinuxWorld(t)
	cs := newCaptureStore(t)
	cs.add("claude", "2.1.200", false)
	w.floor.ResolveCapture = cs.resolve
	captures := 0
	w.floor.Capture = func(bin string) error {
		captures++
		cs.add(bin, "2.1.267", true)
		return nil
	}
	st, _, err := w.floor.Ensure(context.Background(), installerProgram("claude", "claude"))
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if captures != 1 || st.Record.Version != "2.1.267" {
		t.Errorf("captures %d, version %s", captures, st.Record.Version)
	}

	// The same jail-home-only entry on a machine that cannot capture: no floor entry here, since
	// the one way to move it is a capture this machine cannot run.
	other := newLinuxWorld(t)
	jailOnly := newCaptureStore(t)
	jailOnly.add("claude", "2.1.200", false)
	other.floor.ResolveCapture = jailOnly.resolve
	other.floor.Capture = func(string) error { t.Fatal("a capture ran on a machine that cannot"); return nil }
	other.floor.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
	if st := other.floor.Status(installerProgram("claude", "claude")); st.Disposition != NoEntry ||
		!strings.Contains(st.Reason, "recorded for a jail's home only") {
		t.Errorf("a jail-home-only capture with no way to recapture: %s (%s)", st.Disposition, st.Reason)
	}
}

// TestAnInstallerProgramThisMachineCanNeitherMaterializeNorCaptureHasNoFloorEntry: no capture in
// the store and no container runtime to make one means the floor cannot provision it HERE — no
// floor entry, with the reason, so a launch looks on PATH (OQ-HE11) instead of failing an install.
// A provisioned copy stays provisioned whatever the store says afterwards.
func TestAnInstallerProgramThisMachineCanNeitherMaterializeNorCaptureHasNoFloorEntry(t *testing.T) {
	w := newLinuxWorld(t)
	cs := newCaptureStore(t)
	w.floor.ResolveCapture = cs.resolve
	w.floor.Capture = func(string) error { t.Fatal("a capture ran on a machine that cannot"); return nil }
	w.floor.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
	claude := installerProgram("claude", "claude")
	st := w.floor.Status(claude)
	if st.Disposition != NoEntry || !strings.Contains(st.Reason, "no capture of claude") ||
		!strings.Contains(st.Reason, "podman") {
		t.Fatalf("Status = %s (%s)", st.Disposition, st.Reason)
	}
	if _, _, err := w.floor.Ensure(context.Background(), claude); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Ensure = %v, want ErrNoEntry", err)
	}
	// With nothing able to capture at all (no Capture), the same answer.
	w.floor.Capture, w.floor.CaptureUnavailable = nil, nil
	if st := w.floor.Status(claude); st.Disposition != NoEntry {
		t.Errorf("with no capture act: %s", st.Disposition)
	}
	// Once provisioned, the store emptying does not take it away.
	cs.add("claude", "2.1.267", true)
	if _, _, err := w.floor.Ensure(context.Background(), claude); err != nil {
		t.Fatal(err)
	}
	delete(cs.byBin, "claude")
	if st := w.floor.Status(claude); st.Disposition != Provisioned {
		t.Errorf("a provisioned entry became %s when the store lost its entry", st.Disposition)
	}
}

// AN INSTALLER PROGRAM THE FLOOR COULD CAPTURE BUT FOR A RUNTIME is no floor entry whose reason
// names the missing runtime and the step that ends it, in both of the capture arms: no capture in
// the store, and one recorded for a jail's home only. The step is true: once a runtime is there,
// the next install runs the one capture itself and installs it, with nothing run by hand. And a
// floor with no capture act names no runtime to install, since installing one would change nothing.
func TestAnInstallerProgramTheFloorCannotCaptureForWantOfARuntimeNamesTheStep(t *testing.T) {
	const unavailable = "no container runtime (podman) is on PATH"
	for _, c := range []struct {
		name, reason, does string
		seed               func(cs *captureStore)
	}{
		{"no capture", "there is no capture of claude on this machine, and ", "captures it",
			func(*captureStore) {}},
		{"a capture for a jail's home only",
			"the capture of claude on this machine was recorded for a jail's home only, and ", "recaptures it",
			func(cs *captureStore) { cs.add("claude", "2.1.200", false) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newLinuxWorld(t)
			cs := newCaptureStore(t)
			c.seed(cs)
			w.floor.ResolveCapture = cs.resolve
			captures := 0
			w.floor.Capture = func(bin string) error {
				captures++
				cs.add(bin, "2.1.267", true)
				return nil
			}
			runtimeMissing := true
			w.floor.CaptureUnavailable = func() string {
				if runtimeMissing {
					return unavailable
				}
				return ""
			}
			claude := installerProgram("claude", "claude")
			st, _, err := w.floor.Ensure(context.Background(), claude)
			if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry {
				t.Fatalf("Ensure = %s (%s) %v, want no floor entry", st.Disposition, st.Reason, err)
			}
			want := c.reason + unavailable + " — install one (`yolo check` names how on this machine) " +
				"and the next `yolo host` launch " + c.does
			if st.Reason != want {
				t.Errorf("the reason is\n  %s\nwant\n  %s", st.Reason, want)
			}
			if captures != 0 {
				t.Fatalf("a capture ran %d times on a machine with no runtime", captures)
			}

			// The step, taken: a runtime, and the next install captures it once and installs that.
			runtimeMissing = false
			st, outcome, err := w.floor.Ensure(context.Background(), claude)
			if err != nil || outcome != Installed || st.Record == nil || st.Record.Version != "2.1.267" {
				t.Fatalf("with a runtime: Ensure = %+v %s %v, want the capture installed\n%s", st, outcome, err,
					w.out.String())
			}
			if captures != 1 {
				t.Errorf("with a runtime: %d captures, want the one the step promised", captures)
			}

			// No capture act at all: no floor entry, and no runtime to install.
			other := newLinuxWorld(t)
			none := newCaptureStore(t)
			c.seed(none)
			other.floor.ResolveCapture = none.resolve
			other.floor.CaptureUnavailable = func() string { return unavailable }
			st = other.floor.Status(claude)
			if st.Disposition != NoEntry || !strings.Contains(st.Reason, "cannot run `yolo capture`") ||
				strings.Contains(st.Reason, "install one") {
				t.Errorf("with no capture act: %s (%s), want no floor entry naming no runtime step",
					st.Disposition, st.Reason)
			}
		})
	}
}

// TestAnInstallerProgramOnAMacIsItsCaptureMaterialized is HP-D2 at this package's reach: on a Mac
// the floor's claude is the store's capture — the macos-user capture act's, taken under the neutral
// staging home /Users/Shared/yolo-captures/claude/home — relocated into the prefix, and it starts
// from its launcher with no environment. The floor's platform is the Mac's; the materialize itself
// runs wherever the test does, which only the platform gate in capture.Materialize reads.
func TestAnInstallerProgramOnAMacIsItsCaptureMaterialized(t *testing.T) {
	w := newLinuxWorld(t)
	w.floor.GOOS, w.floor.GOARCH = "darwin", "arm64"
	cs := newCaptureStore(t)
	cs.home = "/Users/Shared/yolo-captures/claude/home"
	entry := cs.add("claude", "2.1.267", true)
	w.floor.ResolveCapture = cs.resolve
	w.floor.Capture = func(string) error { t.Fatal("a capture ran although the store had one"); return nil }
	w.floor.CaptureActUnavailable = func(string, string) string { return "" }
	claude := installerProgram("claude", "claude")
	if st := w.floor.Status(claude); st.Disposition != Missing {
		t.Fatalf("Status = %s (%s), want missing: a Mac holds an installer agent's capture", st.Disposition, st.Reason)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), claude)
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if outcome != Installed || st.Record.Capture != entry.Key || st.Record.Version != "2.1.267" {
		t.Fatalf("outcome %s record %+v", outcome, st.Record)
	}
	cmd := exec.Command(st.Launcher, "--version")
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "claude-2.1.267 --version\n" {
		t.Fatalf("running the floor's claude: %q %v", got, err)
	}
	if link, _ := os.Readlink(st.Record.Entry); !strings.HasPrefix(link, w.floor.Dir) {
		t.Errorf("~/.local/bin/claude links to %s, not into the floor: the capture was not relocated", link)
	}
}

// AN INSTALLER'S CAPTURE IS NOT A FORK'S BUILD: on a machine that cannot boot a jail (CaptureUnavailable)
// but can capture an installer another way (CaptureActUnavailable "" — the Landlock host capture,
// HP-D18), the installer program is missing and its install captures it, while a fork's program,
// whose build only a jail runs, stays no floor entry naming the runtime: a pack may not build on the
// host (forked-programs-as-packs.md §12).
func TestAMachineThatCapturesWithoutAJailStillCannotBuildAFork(t *testing.T) {
	const noRuntime = "no container runtime (podman) is on PATH"
	w := newLinuxWorld(t)
	cs := newCaptureStore(t)
	w.floor.ResolveCapture = cs.resolve
	captures := 0
	w.floor.Capture = func(bin string) error {
		captures++
		cs.add(bin, "2.1.267", true)
		return nil
	}
	w.floor.CaptureUnavailable = func() string { return noRuntime }
	w.floor.CaptureActUnavailable = func(string, string) string { return "" }
	claude := installerProgram("claude", "claude")
	if st := w.floor.Status(claude); st.Disposition != Missing {
		t.Fatalf("installer: Status = %s (%s), want missing — the capture runs without a jail", st.Disposition, st.Reason)
	}
	if st, outcome, err := w.floor.Ensure(context.Background(), claude); err != nil || outcome != Installed ||
		captures != 1 {
		t.Fatalf("installer: Ensure = %+v %s %v with %d captures, want one capture installed\n%s", st, outcome, err,
			captures, w.out.String())
	}

	pin := forkCommitOne
	fw, bs := forkWorld(t, &pin)
	fw.floor.CaptureUnavailable = func() string { return noRuntime }
	fw.floor.CaptureActUnavailable = func(string, string) string { return "" }
	st, _, err := fw.floor.Ensure(context.Background(), forkProgram())
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry || !strings.Contains(st.Reason, noRuntime) {
		t.Fatalf("fork: Ensure = %s (%s), %v; want no floor entry naming the missing runtime", st.Disposition,
			st.Reason, err)
	}
	if len(bs.builds) != 0 {
		t.Errorf("builds = %v: a fork was built on a machine that boots no jail", bs.builds)
	}
}

// THE CAPTURE ACT'S OWN REASON IS THE WHOLE CLAUSE: CaptureActUnavailable names its step, so the floor
// appends no runtime step to it, in both of the capture arms; and with no act predicate the runtime
// predicate answers with its step, as before.
func TestTheCaptureActsReasonCarriesItsOwnStep(t *testing.T) {
	const own = "the sandbox account _yolojail does not exist — run `yolo macos-setup`, and the next launch "
	for _, c := range []struct {
		name, reason, does string
		seed               func(cs *captureStore)
	}{
		{"no capture", "there is no capture of claude on this machine, and ", "captures it", func(*captureStore) {}},
		{"a capture for a jail's home only", "the capture of claude on this machine was recorded for a jail's home " +
			"only, and ", "recaptures it", func(cs *captureStore) { cs.add("claude", "2.1.200", false) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newLinuxWorld(t)
			w.floor.GOOS = "darwin"
			cs := newCaptureStore(t)
			c.seed(cs)
			w.floor.ResolveCapture = cs.resolve
			w.floor.Capture = func(string) error { t.Fatal("a capture ran"); return nil }
			w.floor.CaptureUnavailable = func() string { return "a runtime reason no installer capture reads" }
			var asked []string
			w.floor.CaptureActUnavailable = func(bin, does string) string {
				asked = append(asked, bin+"/"+does)
				return own + does
			}
			st := w.floor.Status(installerProgram("claude", "claude"))
			if st.Disposition != NoEntry || st.Reason != c.reason+own+c.does {
				t.Fatalf("Status = %s\n  %s\nwant no floor entry\n  %s", st.Disposition, st.Reason, c.reason+own+c.does)
			}
			if strings.Join(asked, "|") != "claude/"+c.does {
				t.Errorf("CaptureActUnavailable was asked %q, want once for claude", asked)
			}
		})
	}
}

// TestNoFloorEntryDispositions: the ways the floor cannot hold a selected pack's program,
// each with its reason, and none of them touching the disk.
func TestNoFloorEntryDispositions(t *testing.T) {
	w := newWorld(t)
	cases := []struct {
		name string
		mut  func(f *Floor, p *Program)
		want string
	}{
		{"configured out", func(f *Floor, p *Program) { f.Include = func(string) bool { return false } }, "host_floor"},
		{"unpublished", func(f *Floor, p *Program) { p.Install.Platforms = []string{"plan9"} }, "publishes no build"},
		// An installer agent on a Mac with nothing captured and the macos-user capture act unable to
		// run: its own refusal, whose step is the sandbox account's setup (HP-D2).
		{"installer on macOS before macos-setup", func(f *Floor, p *Program) {
			f.GOOS, f.GOARCH = "darwin", "arm64"
			f.ResolveCapture = newCaptureStore(t).resolve
			f.Capture = func(string) error { t.Fatal("a capture ran on a Mac with no sandbox account"); return nil }
			f.CaptureActUnavailable = func(bin, does string) string {
				return "the sandbox account _yolojail does not exist — run the one-time setup, `yolo macos-setup`, " +
					"and the next `yolo host` launch " + does
			}
			*p = installerProgram("claude", "claude")
		}, "`yolo macos-setup`, and the next `yolo host` launch captures it"},
		// A Mac's floor given no capture act has none to run: no capture jail stands in for it.
		{"installer on macOS with no capture act", func(f *Floor, p *Program) {
			f.GOOS, f.GOARCH = "darwin", "arm64"
			f.ResolveCapture = newCaptureStore(t).resolve
			f.CaptureUnavailable = func() string { return "" }
			*p = installerProgram("claude", "claude")
		}, "macos-user capture act"},
		{"installer on a platform no capture runs on", func(f *Floor, p *Program) {
			f.GOOS = "freebsd"
			*p = installerProgram("claude", "claude")
		}, "this machine is freebsd/"},
		{"no Node for this arch", func(f *Floor, p *Program) { f.GOARCH = "riscv64" }, "Node publishes no official build"},
		// A FORK's program (the base's, after the selection's fork rewrite) on a floor that reads
		// no pin: no build to ask for, naming the fork. built_test.go has the fork's other cases.
		{"source-built fork with no pin", func(f *Floor, p *Program) {
			f.GOOS = "linux"
			*p = Program{Pack: "pi", Install: packdecl.Install{Kind: packdecl.InstallKindSource, Bin: "pi",
				Source: "git+https://example.invalid/pi-fork?ref=main", Build: "make install",
				Produces: []string{".local/bin/pi"}, ForkedBy: "pi-matt"}}
		}, "built from source by fork pack pi-matt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := *w.floor
			p := npmProgram("opencode", "opencode", "opencode-ai")
			c.mut(&f, &p)
			st := f.Status(p)
			if st.Disposition != NoEntry || !strings.Contains(st.Reason, c.want) {
				t.Fatalf("Status = %s (%s), want no floor entry naming %q", st.Disposition, st.Reason, c.want)
			}
			if _, _, err := f.Ensure(context.Background(), p); !errors.Is(err, ErrNoEntry) {
				t.Errorf("Ensure = %v, want ErrNoEntry", err)
			}
			if _, err := os.Stat(f.Dir); err == nil {
				t.Errorf("asking about a program with no floor entry created %s", f.Dir)
			}
		})
	}
}

// TestReconcileRemovesADeselectedProgramAndNothingElse is §4's deselection rule.
func TestReconcileRemovesADeselectedProgramAndNothingElse(t *testing.T) {
	w := newWorld(t)
	w.publish("opencode-ai", "1.0.0", "bin=opencode")
	w.publish("@github/copilot", "1.0.0", "bin=copilot")
	keep := npmProgram("opencode", "opencode", "opencode-ai")
	drop := npmProgram("copilot", "copilot", "@github/copilot")
	for _, p := range []Program{keep, drop} {
		if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.floor.Reconcile([]Program{keep}, false); len(got) != 1 || got[0].Bin != "copilot" {
		t.Fatalf("dry run = %+v", got)
	}
	if _, err := os.Stat(w.floor.Launcher("copilot")); err != nil {
		t.Fatal("the dry run removed something")
	}
	w.floor.Reconcile([]Program{keep}, true)
	for _, gone := range []string{w.floor.Launcher("copilot"), w.floor.recordPath("copilot"), w.floor.programsDir("copilot")} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s survived the deselection", gone)
		}
	}
	if st := w.floor.Status(keep); st.Disposition != Provisioned {
		t.Errorf("the kept program is %s", st.Disposition)
	}
	if !w.floor.NodeReady(w.version) {
		t.Error("the Node a kept program runs on was removed")
	}
}

// TestSweepReclaimsOnlyInterruptedInstalls is `yolo prune`'s reach: a directory with no marker and
// no lock holder; never a complete install, never one being written.
func TestSweepReclaimsOnlyInterruptedInstalls(t *testing.T) {
	w := newWorld(t)
	w.publish("opencode-ai", "1.0.0", "bin=opencode")
	if _, _, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai")); err != nil {
		t.Fatal(err)
	}
	torn := filepath.Join(w.floor.programsDir("opencode"), "torn")
	must(t, os.MkdirAll(torn, 0o700))
	must(t, os.WriteFile(filepath.Join(torn, "half"), []byte("x"), 0o600))
	busy := filepath.Join(w.floor.programsDir("busy"), "inflight")
	must(t, os.MkdirAll(busy, 0o700))
	lk, err := acquire(w.floor.lockPath("busy"), false, nil)
	must(t, err)
	defer lk.release()

	bytes, n := w.floor.Sweep(true)
	if n != 1 || bytes != 1 {
		t.Errorf("Sweep = %d bytes, %d dirs; want the one torn install", bytes, n)
	}
	if _, err := os.Stat(torn); err == nil {
		t.Error("the torn install survived")
	}
	if _, err := os.Stat(busy); err != nil {
		t.Error("an install whose lock is held was swept")
	}
	if st := w.floor.Status(npmProgram("opencode", "opencode", "opencode-ai")); st.Disposition != Provisioned {
		t.Errorf("a complete install was swept: %s", st.Disposition)
	}
}

// TestExtractRefusesALinkThatLeavesTheTree.
func TestExtractRefusesALinkThatLeavesTheTree(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: "node-v1/bin/evil", Typeflag: tar.TypeSymlink, Linkname: "../../../etc/passwd"}))
	must(t, tw.Close())
	must(t, gz.Close())
	dir := resolvedTemp(t)
	file := filepath.Join(dir, "x.tar.gz")
	must(t, os.WriteFile(file, buf.Bytes(), 0o600))
	if err := extractTarGz(file, filepath.Join(dir, "out"), 1); err == nil || !strings.Contains(err.Error(), "leaves the archive") {
		t.Fatalf("extract = %v", err)
	}
}

// TestOtherCopiesNamesAHandInstalledCopyAndSkipsTheFloor.
func TestOtherCopiesNamesAHandInstalledCopyAndSkipsTheFloor(t *testing.T) {
	w := newWorld(t)
	local := filepath.Join(w.floor.Home, ".local", "bin")
	must(t, os.MkdirAll(local, 0o755))
	must(t, os.WriteFile(filepath.Join(local, "claude"), []byte("#!/bin/sh\n"), 0o755))
	must(t, os.MkdirAll(w.floor.BinDir(), 0o700))
	must(t, os.WriteFile(w.floor.Launcher("claude"), []byte("#!/bin/sh\n"), 0o700))
	got := w.floor.OtherCopies("claude", w.floor.BinDir()+":"+local, w.floor.Home, nil)
	if len(got) != 1 || got[0] != filepath.Join(local, "claude") {
		t.Errorf("OtherCopies = %v", got)
	}
}

// TestOtherCopiesNamesAHomebrewCopyOffThePath: Homebrew's claude (`brew install --cask
// claude-code`) is named though the PATH yolo was handed does not reach it, after the PATH's copy
// and in hint order — the Mac fact that made the test above machine-dependent, set here in the
// world's own stand-ins for /opt/homebrew/bin and /home/linuxbrew/.linuxbrew/bin.
func TestOtherCopiesNamesAHomebrewCopyOffThePath(t *testing.T) {
	w := newWorld(t)
	local := filepath.Join(w.root, "on-path")
	var want []string
	for _, d := range []string{local, machineDir(w.root, "/opt/homebrew/bin"),
		machineDir(w.root, "/home/linuxbrew/.linuxbrew/bin")} {
		must(t, os.MkdirAll(d, 0o755))
		must(t, os.WriteFile(filepath.Join(d, "claude"), []byte("#!/bin/sh\n"), 0o755))
		want = append(want, filepath.Join(d, "claude"))
	}
	got := w.floor.OtherCopies("claude", local, w.floor.Home, nil)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("OtherCopies = %v, want %v", got, want)
	}
}

// TestOtherCopiesWithNoHintsLooksAtTheCompiledList: the floor `yolo check` builds sets no Hints, so
// it looks at HintLocations — the world's stand-ins are a test's alone.
func TestOtherCopiesWithNoHintsLooksAtTheCompiledList(t *testing.T) {
	home := resolvedTemp(t)
	got, want := (&Floor{}).hints(home), HintLocations(home)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("hints = %v, want HintLocations %v", got, want)
	}
}

// TestACaptureThatHoldsNoRunnableProgramIsNoFloorEntry: codex's installer leaves ~/.local/bin/codex
// a link into ~/.codex, which no capture records. Its capture can never run outside the jail that
// made it, so the floor has no entry for it — whether the store already holds that capture, or the
// install makes it — and nothing is materialized.
func TestACaptureThatHoldsNoRunnableProgramIsNoFloorEntry(t *testing.T) {
	w := newLinuxWorld(t)
	codex := installerProgram("codex", "codex")

	cs := newCaptureStore(t)
	w.floor.ResolveCapture = cs.resolve
	cs.addLinkedOut("codex")
	st := w.floor.Status(codex)
	if st.Disposition != NoEntry || !strings.Contains(st.Reason, "which the capture did not record") {
		t.Fatalf("with the store's capture: %s (%s)", st.Disposition, st.Reason)
	}

	fresh := newCaptureStore(t)
	w.floor.ResolveCapture = fresh.resolve
	w.floor.Capture = func(bin string) error { fresh.addLinkedOut(bin); return nil }
	st, _, err := w.floor.Ensure(context.Background(), codex)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry || !strings.Contains(st.Reason, "cannot run outside a jail") {
		t.Fatalf("when the install makes the capture: err %v, %s (%s)", err, st.Disposition, st.Reason)
	}
	if dirs, _ := os.ReadDir(w.floor.programsDir("codex")); len(dirs) != 0 {
		t.Errorf("an unusable capture left %d install directories", len(dirs))
	}
}

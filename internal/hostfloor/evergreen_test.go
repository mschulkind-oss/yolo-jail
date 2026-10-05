package hostfloor

// evergreen_test.go pins HP-D16, the installer recipe's evergreen refresh: under the same hourly
// stamp and `agent_updates` gate as an npm program's poll, a floor whose newest capture of an
// installer agent is a day old runs the capture act once, saying so first, and installs what it
// stored only when that release is NEWER than the installed one. Nothing is ever downgraded, and the
// vendor's own update verb never runs. Every capture here is a fixture; the clock is the test's.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// evergreenWorld is a Linux floor holding claude 2.1.267 from the store, installed at the clock's
// start, with a capture act that files capturing() — the next release, by default — and counts
// itself.
type evergreenWorld struct {
	*world
	clk       *clock
	cs        *captureStore
	captures  int
	capturing func(bin string) error
}

func newEvergreenWorld(t *testing.T) *evergreenWorld {
	t.Helper()
	e := &evergreenWorld{world: newLinuxWorld(t), clk: &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		cs: newCaptureStore(t)}
	e.floor.Now = e.clk.now
	e.floor.ResolveCapture = e.cs.resolve
	e.capturing = func(bin string) error { e.cs.add(bin, "2.1.300", true); return nil }
	e.floor.Capture = func(bin string) error { e.captures++; return e.capturing(bin) }
	e.cs.add("claude", "2.1.267", true)
	if st, outcome, err := e.floor.Ensure(context.Background(), installerProgram("claude", "claude")); err != nil ||
		outcome != Installed || st.Record.Version != "2.1.267" {
		t.Fatalf("installing claude: %s %+v %v\n%s", outcome, st.Record, err, e.out.String())
	}
	return e
}

func (e *evergreenWorld) ensure() (Status, Outcome, error) {
	return e.floor.Ensure(context.Background(), installerProgram("claude", "claude"))
}

// TestAnInstallerAgentWhoseCaptureIsADayOldIsRecapturedAndUpdated: past the interval and the
// capture-refresh age, one capture runs, announced first, and the newer release it stored is
// installed. The launcher runs the new copy.
func TestAnInstallerAgentWhoseCaptureIsADayOldIsRecapturedAndUpdated(t *testing.T) {
	e := newEvergreenWorld(t)
	notice := "claude 2.1.267 in yolo's floor is from a capture made 25 hours ago; running `yolo capture claude` " +
		"to look for a newer release"
	e.capturing = func(bin string) error {
		if !strings.Contains(e.out.String(), notice) {
			t.Errorf("the capture started before the line saying so:\n%s", e.out.String())
		}
		e.cs.add(bin, "2.1.300", true)
		return nil
	}
	e.clk.t = e.clk.t.Add(25 * time.Hour)
	st, outcome, err := e.ensure()
	if err != nil || outcome != Updated || st.Record.Version != "2.1.300" || e.captures != 1 {
		t.Fatalf("Ensure = %s %+v %v after %d captures\n%s", outcome, st.Record, err, e.captures, e.out.String())
	}
	if !strings.Contains(e.out.String(), "updating claude 2.1.267 → 2.1.300 in yolo's floor") {
		t.Errorf("no update line:\n%s", e.out.String())
	}
	cmd := exec.Command(st.Launcher)
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "claude-2.1.300\n" {
		t.Errorf("the floor's claude ran %q %v, want 2.1.300", got, err)
	}
}

// TestNoRecaptureRunsInsideTheAgeOrTheIntervalOrUnderAHold: the capture costs a jail, so it waits
// out the capture-refresh age even once the hourly stamp allows a poll; it waits out the hourly
// stamp too; and `agent_updates` holding the pack holds it.
func TestNoRecaptureRunsInsideTheAgeOrTheIntervalOrUnderAHold(t *testing.T) {
	e := newEvergreenWorld(t)
	e.clk.t = e.clk.t.Add(2 * time.Hour)
	if _, outcome, err := e.ensure(); err != nil || outcome != Current || e.captures != 0 {
		t.Fatalf("inside the age: %s %v after %d captures", outcome, err, e.captures)
	}
	e.clk.t = e.clk.t.Add(23 * time.Hour)
	held := *e.floor
	held.UpdatesAllowed = func(pack string) bool { return pack != "claude" }
	if _, outcome, err := held.Ensure(context.Background(), installerProgram("claude", "claude")); err != nil ||
		outcome != Current || e.captures != 0 {
		t.Fatalf("under agent_updates false: %s %v after %d captures", outcome, err, e.captures)
	}
	// The stamp a poll writes holds the next one off for the interval, whatever the age.
	rec, _ := e.floor.readRecord("claude")
	rec.Checked = e.clk.t.Add(-10 * time.Minute)
	must(t, e.floor.writeRecord(rec))
	if _, outcome, _ := e.ensure(); outcome != Current || e.captures != 0 {
		t.Fatalf("inside the hourly interval: %s after %d captures", outcome, e.captures)
	}
}

// TestAMachineThatCannotCaptureKeepsItsCopyAndSaysNothing: with no container runtime, no capture is
// tried, the installed copy stays current and still runs, and nothing is printed for it.
func TestAMachineThatCannotCaptureKeepsItsCopyAndSaysNothing(t *testing.T) {
	e := newEvergreenWorld(t)
	e.floor.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
	before := e.out.String()
	e.clk.t = e.clk.t.Add(25 * time.Hour)
	st, outcome, err := e.ensure()
	if err != nil || outcome != Current || e.captures != 0 || st.Record.Version != "2.1.267" {
		t.Fatalf("Ensure = %s %+v %v after %d captures", outcome, st.Record, err, e.captures)
	}
	if after := e.out.String(); after != before {
		t.Errorf("a refresh that could not capture printed:\n%s", strings.TrimPrefix(after, before))
	}
	cmd := exec.Command(st.Launcher)
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "claude-2.1.267\n" {
		t.Errorf("the floor's claude ran %q %v", got, err)
	}
}

// TestAFailedRecaptureKeepsTheInstalledCopyAndWaitsOutTheInterval: the stamp is written before the
// capture, so a capture that fails (a contended or broken one) keeps 2.1.267, and the next one runs
// only once the interval has passed.
func TestAFailedRecaptureKeepsTheInstalledCopyAndWaitsOutTheInterval(t *testing.T) {
	e := newEvergreenWorld(t)
	e.capturing = func(string) error { return errors.New("another capture of claude is running") }
	e.clk.t = e.clk.t.Add(25 * time.Hour)
	st, outcome, err := e.ensure()
	if err != nil || outcome != Kept || st.Record.Version != "2.1.267" || e.captures != 1 {
		t.Fatalf("Ensure = %s %+v %v after %d captures\n%s", outcome, st.Record, err, e.captures, e.out.String())
	}
	if !strings.Contains(e.out.String(), "could not capture a newer release") {
		t.Errorf("the failure was not said:\n%s", e.out.String())
	}
	if rec, _ := e.floor.readRecord("claude"); !rec.Checked.Equal(e.clk.t) {
		t.Errorf("the record was checked at %s, want stamped now (%s)", rec.Checked, e.clk.t)
	}
	e.clk.t = e.clk.t.Add(10 * time.Minute)
	if _, _, _ = e.ensure(); e.captures != 1 {
		t.Errorf("a failed capture was retried inside the interval (%d captures)", e.captures)
	}
	e.clk.t = e.clk.t.Add(time.Hour)
	if _, _, _ = e.ensure(); e.captures != 2 {
		t.Errorf("past the interval: %d captures, want the retry", e.captures)
	}
}

// TestARecaptureOfTheSameReleaseInstallsNothing: a capture that stores the same release — the same
// bytes, or other bytes under the same version — leaves the installed copy where it is.
func TestARecaptureOfTheSameReleaseInstallsNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		shape func(tree string, m *capture.Manifest)
	}{
		{"the same bytes", nil},
		{"other bytes", func(tree string, m *capture.Manifest) {
			must(t, os.WriteFile(filepath.Join(tree, ".local", "share", "claude", "versions", "2.1.267"), []byte("#!/bin/sh\necho repacked\n"), 0o755))
			for i := range m.Entries {
				if m.Entries[i].Path == ".local/share/claude/versions/2.1.267" {
					m.Entries[i].Size = int64(len("#!/bin/sh\necho repacked\n"))
				}
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEvergreenWorld(t)
			installed, _ := e.floor.readRecord("claude")
			e.capturing = func(bin string) error { e.cs.addShaped(bin, "2.1.267", true, c.shape); return nil }
			e.clk.t = e.clk.t.Add(25 * time.Hour)
			st, outcome, err := e.ensure()
			if err != nil || outcome != Current || e.captures != 1 || st.Record.Dir != installed.Dir {
				t.Fatalf("Ensure = %s %+v %v after %d captures, want the installed copy kept\n%s", outcome, st.Record,
					err, e.captures, e.out.String())
			}
		})
	}
}

// TestAnOlderReleaseTheStoreSelectsIsNeverInstalled: the store selects its newest CAPTURE, which can
// be an older RELEASE (a vendor's stable channel behind the latest one), and the refresh never moves
// the floor back to it — neither when a jail's capture put it there nor when its own capture did.
func TestAnOlderReleaseTheStoreSelectsIsNeverInstalled(t *testing.T) {
	e := newEvergreenWorld(t)
	e.cs.add("claude", "2.1.200", true)
	e.clk.t = e.clk.t.Add(2 * time.Hour)
	st, outcome, err := e.ensure()
	if err != nil || outcome != Current || st.Record.Version != "2.1.267" {
		t.Fatalf("with an older capture selected: %s %+v %v\n%s", outcome, st.Record, err, e.out.String())
	}
	e.capturing = func(bin string) error { e.cs.add(bin, "2.1.199", true); return nil }
	e.clk.t = e.clk.t.Add(25 * time.Hour)
	st, outcome, err = e.ensure()
	if err != nil || outcome != Current || st.Record.Version != "2.1.267" || e.captures != 1 {
		t.Fatalf("when its own capture stored an older release: %s %+v %v after %d captures\n%s", outcome, st.Record,
			err, e.captures, e.out.String())
	}
	if strings.Contains(e.out.String(), "updating claude") {
		t.Errorf("the refresh offered a downgrade:\n%s", e.out.String())
	}
}

// TestTheCapturesAgeIsTheStoresNewestReceipt: the age is measured from when the machine last
// recorded a capture of the program — the receipt log beside the store's entry, which a jail's
// auto-capture or a human's `yolo capture` writes too — and not from when the floor installed it.
func TestTheCapturesAgeIsTheStoresNewestReceipt(t *testing.T) {
	e := newEvergreenWorld(t)
	installed := e.clk.t
	entry := e.cs.byBin["claude"]
	receipts := capture.ReceiptsPath(entry.Root)
	must(t, os.WriteFile(receipts, []byte("{}\n"), 0o644))
	recorded := installed.Add(24 * time.Hour)
	must(t, os.Chtimes(receipts, recorded, recorded))
	e.clk.t = installed.Add(26 * time.Hour)
	if _, outcome, err := e.ensure(); err != nil || outcome != Current || e.captures != 0 {
		t.Fatalf("a capture recorded 2 hours ago: %s %v after %d captures", outcome, err, e.captures)
	}
	must(t, os.Chtimes(receipts, installed.Add(-time.Hour), installed.Add(-time.Hour)))
	e.clk.t = e.clk.t.Add(2 * time.Hour)
	if _, outcome, err := e.ensure(); err != nil || outcome != Updated || e.captures != 1 {
		t.Fatalf("a capture recorded 29 hours ago: %s %v after %d captures\n%s", outcome, err, e.captures, e.out.String())
	}
}

// TestAnInstallerAgentWithNoVersionsDirectoryFollowsTheStore: a program whose captures carry no
// versions directory (a lone binary in ~/.local/bin, as agy's installer leaves it) has nothing to
// compare, so a newer capture in the store updates it, as every jail's materialize would.
func TestAnInstallerAgentWithNoVersionsDirectoryFollowsTheStore(t *testing.T) {
	w := newLinuxWorld(t)
	clk := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	w.floor.Now = clk.now
	cs := newCaptureStore(t)
	w.floor.ResolveCapture = cs.resolve
	lone := func(body string) {
		cs.addShaped("agy", "unused", true, func(tree string, m *capture.Manifest) {
			must(t, os.RemoveAll(filepath.Join(tree, ".local", "share")))
			must(t, os.Remove(filepath.Join(tree, ".local", "bin", "agy")))
			must(t, os.WriteFile(filepath.Join(tree, ".local", "bin", "agy"), []byte(body), 0o755))
			m.Entries = []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin/agy", Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
			}
			m.AbsoluteRefs = []capture.AbsoluteRef{}
		})
	}
	lone("#!/bin/sh\necho agy one\n")
	agy := installerProgram("agy", "agy")
	st, _, err := w.floor.Ensure(context.Background(), agy)
	if err != nil || !strings.HasPrefix(st.Record.Version, "capture ") {
		t.Fatalf("installing agy: %+v %v\n%s", st.Record, err, w.out.String())
	}
	lone("#!/bin/sh\necho agy two\n")
	clk.t = clk.t.Add(2 * time.Hour)
	st, outcome, err := w.floor.Ensure(context.Background(), agy)
	if err != nil || outcome != Updated || st.Record.Capture != cs.byBin["agy"].Key {
		t.Fatalf("with a newer capture: %s %+v %v\n%s", outcome, st.Record, err, w.out.String())
	}
}

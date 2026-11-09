package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// `yolo stop` acts per setup (named-setups-plan.md slice 6b; named-setups.md §6: "`yolo stop`
// stops both, naming each; `yolo stop --setup seatbelt-self` stops one"). The fixture is a
// workspace with podman-self selected and state under .yolo/setups for podman-self and
// seatbelt-self, each with a jail: podman-self's on a stubbed podman, seatbelt-self's on the
// macos-user stop (stubbed), since its setup runs on Seatbelt.
type stopSetupsFixture struct {
	ws, podman, seatbelt string
	// podmanCalls is every runtime command the stop ran, by jail name.
	podmanCalls map[string][][]string
	// macosStops is every jail name the macos-user stop was handed.
	macosStops []string
	// known is the jails stopSetupJailKnown and stopMacosUserJailKnown answer have something left.
	known map[string]bool
}

func newStopSetupsFixture(t *testing.T) *stopSetupsFixture {
	t.Helper()
	cname, _ := stopWorkspace(t, "podman-self")
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f := &stopSetupsFixture{ws: ws, podman: cname, seatbelt: runtime.JailName(ws, "seatbelt-self"),
		podmanCalls: map[string][][]string{}}
	f.known = map[string]bool{f.seatbelt: true}
	for _, name := range []string{"podman-self", "seatbelt-self"} {
		if err := os.MkdirAll(paths.SetupStateDir(ws, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	savedLegacy, savedSetup, savedMacKnown, savedMac := stopLegacyJailKnown, stopSetupJailKnown, stopMacosUserJailKnown, stopMacosUser
	t.Cleanup(func() {
		stopLegacyJailKnown, stopSetupJailKnown, stopMacosUserJailKnown, stopMacosUser = savedLegacy, savedSetup, savedMacKnown, savedMac
	})
	stopLegacyJailKnown = func(string) bool { return false }
	stopSetupJailKnown = func(cname string) bool { return f.known[cname] }
	stopMacosUserJailKnown = func(cname string) bool { return f.known[cname] }
	stopMacosUser = func(_, _ io.Writer, _, cname string) int {
		f.macosStops = append(f.macosStops, cname)
		return 0
	}
	present := map[string]bool{f.podman: true}
	useStopExec(t, func(argv []string, _ time.Duration) (string, bool, int) {
		target := argv[len(argv)-1]
		f.podmanCalls[target] = append(f.podmanCalls[target], argv)
		switch {
		case argv[1] == "inspect" && present[target]:
			return "true\n", true, 0
		case argv[1] == "inspect":
			return "Error: no such container", true, 125
		case argv[1] == "container" && argv[2] == "exists" && present[target]:
			return "", true, 0
		case argv[1] == "container" && argv[2] == "exists":
			return "", true, 1
		case argv[1] == "stop" || argv[1] == "rm":
			present[target] = false
			return "", true, 0
		}
		t.Errorf("unexpected argv %v", argv)
		return "", false, 1
	})
	return f
}

// podmanStops is the jail names a `podman stop` was run for.
func (f *stopSetupsFixture) podmanStops() []string {
	var out []string
	for name, calls := range f.podmanCalls {
		for _, argv := range calls {
			if len(argv) > 1 && argv[1] == "stop" {
				out = append(out, name)
			}
		}
	}
	return out
}

func TestAStopStopsEverySetupsJailNamingEach(t *testing.T) {
	f := newStopSetupsFixture(t)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if got := f.podmanStops(); !slices.Equal(got, []string{f.podman}) {
		t.Errorf("podman stops = %v, want one, of %s; calls %v", got, f.podman, f.podmanCalls)
	}
	if !slices.Equal(f.macosStops, []string{f.seatbelt}) {
		t.Errorf("macos-user stops = %v, want one, of %s", f.macosStops, f.seatbelt)
	}
	for _, want := range []string{"stopped podman-self\n", "stopped seatbelt-self\n"} {
		if strings.Count(stdout, want) != 1 {
			t.Errorf("the stop must say %q once:\n%s", strings.TrimSpace(want), stdout)
		}
	}
	if strings.Index(stdout, "stopped podman-self") > strings.Index(stdout, "stopped seatbelt-self") {
		t.Errorf("the selected setup's jail must be stopped first:\n%s", stdout)
	}
}

// A setup with state here but nothing of its jail left is neither stopped nor named, and a stop
// with nothing running names no setup as stopped.
func TestAStopLeavesASetupWithNothingLeftAlone(t *testing.T) {
	f := newStopSetupsFixture(t)
	f.known = map[string]bool{}
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if len(f.macosStops) != 0 || strings.Contains(stdout, "seatbelt-self") {
		t.Errorf("a setup with nothing left was stopped (%v) or named:\n%s", f.macosStops, stdout)
	}
	// Run again: podman-self's jail is gone now, so nothing is named stopped.
	stdout, _ = captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 || strings.Contains(stdout, "stopped ") || !strings.Contains(stdout, "No jail running") {
		t.Errorf("a stop with nothing running: rc=%d\n%s", rc, stdout)
	}
}

func TestAStopWithSetupStopsThatSetupsJailAlone(t *testing.T) {
	f := newStopSetupsFixture(t)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--setup", "seatbelt-self"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if !slices.Equal(f.macosStops, []string{f.seatbelt}) {
		t.Errorf("macos-user stops = %v, want one, of %s", f.macosStops, f.seatbelt)
	}
	if len(f.podmanCalls) != 0 {
		t.Errorf("--setup seatbelt-self asked podman about %v", f.podmanCalls)
	}
	if !strings.Contains(stdout, "stopped seatbelt-self\n") || strings.Contains(stdout, "podman-self") {
		t.Errorf("the stop must name seatbelt-self alone:\n%s", stdout)
	}

	// The glued spelling selects the same; the selected setup is stopped by name too.
	f.macosStops = nil
	stdout, _ = captureBoth(t, func() { rc = runStop([]string{"stop", "--setup=podman-self"}) })
	if rc != 0 || len(f.macosStops) != 0 || !slices.Equal(f.podmanStops(), []string{f.podman}) ||
		!strings.Contains(stdout, "stopped podman-self\n") {
		t.Errorf("--setup=podman-self: rc=%d macos %v podman %v\n%s", rc, f.macosStops, f.podmanStops(), stdout)
	}
}

// --setup is checked as a launch checks it: an undefined name is refused naming the list, a flag
// with no value is refused, and a host setup runs no jail, so the stop says how it ends instead.
func TestAStopWithSetupRefusesWhatNamesNoJail(t *testing.T) {
	f := newStopSetupsFixture(t)
	for _, tc := range []struct {
		args []string
		rc   int
		want string
	}{
		{[]string{"stop", "--setup", "nope"}, 2, "yolo setup list"},
		{[]string{"stop", "--setup"}, 2, "--setup needs a value"},
		{[]string{"stop", "--setup", "host-self"}, 0, "ends when the program it runs exits"},
	} {
		var rc int
		stdout, stderr := captureBoth(t, func() { rc = runStop(tc.args) })
		if rc != tc.rc || !strings.Contains(stdout+stderr, tc.want) {
			t.Errorf("%v: rc=%d, want %d naming %q\nstdout:\n%s\nstderr:\n%s", tc.args, rc, tc.rc, tc.want, stdout, stderr)
		}
	}
	if len(f.podmanCalls) != 0 || len(f.macosStops) != 0 {
		t.Errorf("a refused stop stopped something: podman %v macos %v", f.podmanCalls, f.macosStops)
	}
}

// A LEGACY macos-user JAIL IS STOPPED ON macos-user WHATEVER IS SELECTED (review M4): its keeper
// keeps its start record under its macos-user key, not under the bare name, so a stop that read
// only the bare name asked the selected container runtime, found nothing, and left the jail that
// the launch refuses on running. The record is real; the stop reads it through
// run.LaunchedRuntime, so deleting its notch keys fails this.
func TestAStopReachesALegacyMacosUserJailWhenAContainerSetupIsSelected(t *testing.T) {
	f := newStopSetupsFixture(t)
	legacy := runtime.FromWorkspace(f.ws)
	record := filepath.Join(paths.GlobalStorage(), "owners", legacy+".macos-user.keeper.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"pid":4242,"runtime":"macos-user"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopLegacyJailKnown = func(name string) bool { return name == legacy }
	f.known[legacy] = true
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if !slices.Contains(f.macosStops, legacy) {
		t.Errorf("the legacy macos-user jail %s was not handed to the macos-user stop (got %v)\nstdout:\n%s",
			legacy, f.macosStops, stdout)
	}
	if calls := f.podmanCalls[legacy]; len(calls) != 0 {
		t.Errorf("the stop asked podman about the legacy macos-user jail: %v", calls)
	}
}

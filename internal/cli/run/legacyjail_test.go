package run

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// legacyContainerRuns makes o's podman answer that a container runs under the legacy jail name of
// ws, and nothing else; it returns the argv of every probe the launch made.
func legacyContainerRuns(o *Options, ws string) *[][]string {
	var probes [][]string
	legacy := yoloruntime.FromWorkspace(ws)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		probes = append(probes, argv)
		if slices.Equal(argv, []string{"podman", "ps", "-a", "-q", "--filter", "name=^/" + legacy + "$"}) {
			return ExecResult{Ran: true, Stdout: "0123abcd\n"}
		}
		return ExecResult{Ran: false}
	}
	return &probes
}

// A JAIL AN OLDER YOLO STARTED REFUSES THE LAUNCH BEFORE ANY MOVE (SUP-D10, slice 6a-iii): its
// name is the project key alone, which this launch's setup-qualified jail name is not, so nothing
// else in the launch would find it; the launch would start a second jail beside it, on the home
// the first still binds. The refusal names how to stop it, and the legacy home stays where the
// running jail has it. Deleting Run's call of refuseLegacyJail fails this.
func TestALegacyJailRunningRefusesTheLaunchBeforeAnyMove(t *testing.T) {
	packHome(t)
	ws, src := legacyHomeWorkspace(t)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	probes := legacyContainerRuns(o, ws)

	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1 (refused)\nstderr:\n%s", rc, stderr.String())
	}
	legacy := yoloruntime.FromWorkspace(ws)
	errText := stderr.String()
	if !strings.Contains(errText, "Refusing to launch") || !strings.Contains(errText, legacy) {
		t.Errorf("the refusal must say it refuses and name the running jail %s:\n%s", legacy, errText)
	}
	if !strings.Contains(errText, "`yolo stop`") || !strings.Contains(errText, "`podman stop "+legacy+"`") {
		t.Errorf("the refusal must name how to stop it:\n%s", errText)
	}
	assertHoldsTranscript(t, src)
	if _, err := os.Lstat(paths.SetupsDir(ws)); !os.IsNotExist(err) {
		t.Errorf("a refused launch created %s: %v", paths.SetupsDir(ws), err)
	}
	if len(*probes) == 0 {
		t.Error("the launch never asked podman about the legacy jail")
	}
}

// With nothing under the legacy name, the launch is not refused on its account.
func TestNoLegacyJailRefusesNothing(t *testing.T) {
	packHome(t)
	ws, _ := legacyHomeWorkspace(t)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	_ = Run(*o)
	if strings.Contains(stderr.String(), "an earlier yolo started for this workspace") {
		t.Errorf("a launch with no legacy jail was refused on its account:\n%s", stderr.String())
	}
}

// TWO SETUPS OF ONE PROJECT ARE TWO JAILS (SUP-D2): their jail names, keeper keys and launch locks
// differ from each other and from the legacy name, including the two Seatbelt setups, which share
// a backend.
func TestTwoSetupsHaveDistinctJailNamesKeysAndLocks(t *testing.T) {
	ws := t.TempDir()
	seen := map[string]string{}
	note := func(what, v string) {
		t.Helper()
		if prev, ok := seen[v]; ok {
			t.Errorf("%s and %s are both %s", prev, what, v)
		}
		seen[v] = what
	}
	note("legacy", yoloruntime.FromWorkspace(ws))
	for _, name := range []string{"podman-self", "apple-container-self", "seatbelt-self", "seatbelt-yolo-account"} {
		cname := JailNameFor(ws, setup.MustBuiltIn(name))
		if cname != yoloruntime.JailName(ws, name) {
			t.Errorf("%s: JailNameFor = %s, want runtime.JailName's %s", name, cname, yoloruntime.JailName(ws, name))
		}
		note(name+" name", cname)
		note(name+" launch lock", launchLockPath(cname))
		for _, notch := range macosUserKeeperNotches {
			note(name+" "+notch+" key", keeperKey(cname, notch))
		}
		note(name+" services hash", paths.JailShortHash(cname))
	}
	if got := JailNameFor(ws, setup.Setup{}); got != yoloruntime.FromWorkspace(ws) {
		t.Errorf("a setup with no name names %s, want the legacy name", got)
	}
}

// The launch's jail name is its resolved setup's (Options.jailName), and before the setup is
// resolved, the one its selection would make.
func TestOptionsJailNameIsTheSelectedSetups(t *testing.T) {
	ws := t.TempDir()
	o := &Options{Workspace: ws}
	selectRuntimeSetup(o, "podman")
	if got, want := o.jailName(), yoloruntime.JailName(ws, "podman-self"); got != want {
		t.Errorf("before resolveSetup: jailName = %s, want %s", got, want)
	}
	if got := o.setupName(); got != "podman-self" {
		t.Errorf("setupName = %q, want podman-self", got)
	}
	o.Setup = setup.MustBuiltIn("seatbelt-self")
	if got, want := o.jailName(), yoloruntime.JailName(ws, "seatbelt-self"); got != want {
		t.Errorf("after resolveSetup: jailName = %s, want %s", got, want)
	}
	if got := o.legacyJailName(); got != yoloruntime.FromWorkspace(ws) {
		t.Errorf("legacyJailName = %s, want the project key", got)
	}
}

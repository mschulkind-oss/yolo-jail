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

// legacyContainerLeftover makes o's podman hold a STOPPED container under the legacy jail name of
// ws: `ps -a` lists it until `podman rm` (whose status is rmRC) removes it, and `ps` (running only)
// never does. It returns the argv of every command the launch ran.
func legacyContainerLeftover(o *Options, ws string, rmRC int) *[][]string {
	var calls [][]string
	legacy := yoloruntime.FromWorkspace(ws)
	present := true
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		calls = append(calls, argv)
		switch {
		case slices.Equal(argv, []string{"podman", "ps", "-a", "-q", "--filter", "name=^/" + legacy + "$"}):
			if present {
				return ExecResult{Ran: true, Stdout: "0123abcd\n"}
			}
			return ExecResult{Ran: true}
		case slices.Equal(argv, []string{"podman", "ps", "-q", "--filter", "name=^/" + legacy + "$"}):
			return ExecResult{Ran: true}
		case slices.Equal(argv, []string{"podman", "rm", legacy}):
			if rmRC == 0 {
				present = false
			}
			return ExecResult{Ran: true, RC: rmRC}
		}
		return ExecResult{Ran: false}
	}
	return &calls
}

// A STOPPED CONTAINER AN OLDER YOLO LEFT IS REMOVED, NOT A REFUSAL (review M2): it holds the
// legacy name and nothing else, and no yolo command would clear it (`podman stop` does nothing to
// an exited container), so refusing would wedge the workspace. The launch removes it the way it
// removes a stale container of its own name, and says so. Deleting the leftover branch of Run's
// refuseLegacyJail fails this.
func TestAStoppedLegacyContainerIsRemovedNotRefused(t *testing.T) {
	packHome(t)
	ws, _ := legacyHomeWorkspace(t)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	calls := legacyContainerLeftover(o, ws, 0)
	_ = Run(*o)
	legacy := yoloruntime.FromWorkspace(ws)
	if strings.Contains(stderr.String(), "Refusing to launch: a jail an earlier yolo") ||
		strings.Contains(stderr.String(), "Refusing to launch: a stopped jail") {
		t.Errorf("a stopped legacy container refused the launch:\n%s", stderr.String())
	}
	if !slices.ContainsFunc(*calls, func(a []string) bool { return slices.Equal(a, []string{"podman", "rm", legacy}) }) {
		t.Errorf("the launch did not remove the stopped legacy container; calls %v", *calls)
	}
	if !strings.Contains(stderr.String(), "Removed the stopped jail an earlier yolo left for this workspace ("+legacy+")") {
		t.Errorf("the launch did not say it removed the stopped legacy container:\n%s", stderr.String())
	}
}

// A stopped legacy container the runtime will not remove refuses the launch naming the runtime's
// own removal, not a stop (which does nothing to it).
func TestAStoppedLegacyContainerThatWillNotGoNamesItsRemoval(t *testing.T) {
	packHome(t)
	ws, _ := legacyHomeWorkspace(t)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	legacyContainerLeftover(o, ws, 1)
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1 (refused)\nstderr:\n%s", rc, stderr.String())
	}
	legacy := yoloruntime.FromWorkspace(ws)
	if !strings.Contains(stderr.String(), "`podman rm "+legacy+"`") {
		t.Errorf("the refusal must name `podman rm %s`:\n%s", legacy, stderr.String())
	}
	if strings.Contains(stderr.String(), "podman stop") {
		t.Errorf("the refusal named a stop, which does nothing to a stopped container:\n%s", stderr.String())
	}
}

// A LEGACY JAIL A KEEPER ANSWERS FOR, WHICH IS ALSO A CONTAINER, NAMES THE RUNTIME'S STOP TOO (the
// Mac run of 2026-11-09, W-SU12's Apple Container half). jailRuns answers through the keeper
// before it asks any runtime, so the refusal named `yolo stop` alone; it now asks the runtimes for
// the name to choose its advice. Red when refuseLegacyJail's runtime probe is deleted.
func TestAKeptLegacyContainerJailNamesTheRuntimesStop(t *testing.T) {
	packHome(t)
	ws, _ := legacyHomeWorkspace(t)
	legacy := yoloruntime.FromWorkspace(ws)
	live, err := holdLivenessLock(legacy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseLock(live) })
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
			o.LookPath = func(name string) (string, bool) { return "/bin/" + name, name == "podman" || name == "container" }
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				switch {
				case rt == "podman" && slices.Equal(argv, []string{"podman", "ps", "-q", "--filter", "name=^/" + legacy + "$"}):
					return ExecResult{Ran: true, Stdout: "0123abcd\n"}
				case rt == "container" && slices.Equal(argv, []string{"container", "ls"}):
					return ExecResult{Ran: true, Stdout: "ID IMAGE\n" + legacy + " img\n"}
				case argv[0] == "podman" || argv[0] == "container":
					return ExecResult{Ran: true}
				}
				return ExecResult{Ran: false}
			}
			if rc := Run(*o); rc != 1 {
				t.Fatalf("Run() = %d, want 1 (refused)\nstderr:\n%s", rc, stderr.String())
			}
			if want := "`" + rt + " stop " + legacy + "`"; !strings.Contains(stderr.String(), "`yolo stop`") ||
				!strings.Contains(stderr.String(), want) {
				t.Errorf("the refusal must name `yolo stop` and %s:\n%s", want, stderr.String())
			}
		})
	}
}

// A LEGACY JAIL A KEEPER ANSWERS FOR, WHICH NO RUNTIME RUNS (a Seatbelt session), names `yolo stop`
// alone: no runtime's stop would reach it.
func TestAKeptLegacyJailNoRuntimeRunsNamesOnlyYoloStop(t *testing.T) {
	packHome(t)
	ws, _ := legacyHomeWorkspace(t)
	legacy := yoloruntime.FromWorkspace(ws)
	live, err := holdLivenessLock(legacy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseLock(live) })
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.LookPath = func(name string) (string, bool) { return "/bin/" + name, name == "podman" || name == "container" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if argv[0] == "podman" || argv[0] == "container" {
			return ExecResult{Ran: true}
		}
		return ExecResult{Ran: false}
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1 (refused)\nstderr:\n%s", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Stop it, then launch again: `yolo stop` from this workspace.") {
		t.Errorf("the refusal must name `yolo stop` alone:\n%s", stderr.String())
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

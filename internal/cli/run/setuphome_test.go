package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// TestTheClaudeViewIsRegisteredInTheLaunchsHome: the Claude credential view is registered in the
// home this launch binds, a setup's by its Home, so two setups of one project hold two views and
// the broker writes the one each jail reads. Red if claudeCredentialViewLocation drops Home.
func TestTheClaudeViewIsRegisteredInTheLaunchsHome(t *testing.T) {
	ws := t.TempDir()
	for _, rt := range []string{"podman", "macos-user", "container"} {
		o := &Options{Workspace: ws, homeState: paths.SetupHomeState(ws, "x-setup")}
		loc := o.claudeCredentialViewLocation(rt)
		if loc.Home != "setups/x-setup/home" {
			t.Errorf("%s: the view names home %q, want the setup's", rt, loc.Home)
		}
		if !strings.HasPrefix(loc.Path(), paths.SetupHomeState(ws, "x-setup")+"/") {
			t.Errorf("%s: the view is at %s, outside the launch's home", rt, loc.Path())
		}
		o.homeState = paths.LegacyHomeState(ws)
		if loc := o.claudeCredentialViewLocation(rt); loc.Home != "" {
			t.Errorf("%s: the legacy home's view names Home %q, want none", rt, loc.Home)
		}
	}
}

// launchHome is the home a launch of o binds when the workspace holds no legacy home: the
// selected setup's own (named-setups-plan.md Slice 6a-ii). o must name its setup (SetupName), as
// dispatchOptions and selectMacosUserMode make it.
func launchHome(t *testing.T, o *Options) string {
	t.Helper()
	if o.SetupName == "" {
		t.Fatal("launchHome: the options name no setup, so the home a launch binds is not known here")
	}
	return paths.SetupHomeState(o.Workspace, o.SetupName)
}

// runtimeHome is launchHome for a launch dispatchOptions made for backend rt in ws: the home of
// the built-in setup that runs rt (testSetupForRuntime).
func runtimeHome(ws, rt string) string {
	return paths.SetupHomeState(ws, testSetupForRuntime(rt))
}

// macosUserModeHome is the home a macos-user launch selectMacosUserMode made for mode binds in ws.
func macosUserModeHome(ws string, mode macosuser.Mode) string {
	if mode == macosuser.ModeSelf {
		return paths.SetupHomeState(ws, "seatbelt-self")
	}
	return paths.SetupHomeState(ws, "seatbelt-yolo-account")
}

// seedLegacyHome plants a legacy home holding one marker file and returns the marker's name.
func seedLegacyHome(t *testing.T, ws string) string {
	t.Helper()
	legacy := paths.LegacyHomeState(ws)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "carried-over"
	if err := os.WriteFile(filepath.Join(legacy, marker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

// TestLaunchMovesTheLegacyHomeAndBindsTheSetupsHome: a launch with no jail of the project running
// moves `.yolo/home` to the selected setup's home, says so in its one informational line, and hands
// the backend that home (named-setups-plan.md Slice 6a-ii). Red if Run stops calling selectHome, or
// the macos-user arm stops carrying o.homeDir() as the overlay's sidecar.
func TestLaunchMovesTheLegacyHomeAndBindsTheSetupsHome(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	marker := seedLegacyHome(t, ws)
	record := []byte(`{"version":1,"entries":{}}`)
	if err := os.WriteFile(filepath.Join(paths.WorkspaceStateDir(ws), packFilesMountpointManifestName), record, 0o644); err != nil {
		t.Fatal(err)
	}
	// The legacy home's capture store, its baseline rendered into that home.
	baseline := []byte(`{"theme":"system"}`)
	if err := os.MkdirAll(filepath.Join(paths.WorkspaceStateDir(ws), "prism"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.WorkspaceStateDir(ws), "prism", "claude-settings.last_render"), baseline, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	var got macosUserCall
	o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int { got = c; return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	want := paths.SetupHomeState(ws, testSetupForRuntime("macos-user"))
	if _, err := os.Lstat(paths.LegacyHomeState(ws)); !os.IsNotExist(err) {
		t.Errorf("the legacy home is still in place (err %v)\nstderr:\n%s", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(want, marker)); err != nil {
		t.Errorf("the legacy home's contents did not move to %s: %v", want, err)
	}
	if got.overlay.Sidecar != want {
		t.Errorf("the macos-user arm was handed home %q, want the setup's %q", got.overlay.Sidecar, want)
	}
	// The session is told the home it was bound (SUP-D35), so its render keys its store to it.
	if v, _ := got.packEnv.Get(paths.HomeStateEnv); v != "setups/"+testSetupForRuntime("macos-user")+"/home" {
		t.Errorf("the session's %s = %v, want the setup's home", paths.HomeStateEnv, v)
	}
	// The mountpoint record beside the home goes with it (preparePackFiles reads it beside its home).
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(want), packFilesMountpointManifestName)); err != nil || !bytes.Equal(b, record) {
		t.Errorf("the mountpoint record did not move beside the setup's home: %q, %v", b, err)
	}
	// And so does its capture store: the baseline belongs to the home it was rendered into (SUP-D35).
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(want), "prism", "claude-settings.last_render")); err != nil || !bytes.Equal(b, baseline) {
		t.Errorf("the capture store did not move beside the setup's home: %q, %v", b, err)
	}
	if n := strings.Count(stderr.String(), "Moved this project's home"); n != 1 {
		t.Errorf("the move's informational line was printed %d times, want once:\n%s", n, stderr.String())
	}
}

// TestALaunchWhileAJailRunsKeepsTheLegacyHome (SUP-D9/SUP-D21): with a jail of the project running,
// the legacy home stays where it is and this launch binds it, so two jails never split one home; and
// the launch creates no setup home beside it (SUP-D21's entry condition, 6a-ii review m8), so a
// printed manual move never lands inside one. A jail runs in each of three ways the probe must see
// (6a-ii review M1): a keeper, a keeperless macos-user session record (JL-D42), and a container of
// the workspace's name whose keeper died. Red if selectHome stops asking o.projectJails, or
// projectJails drops any of the three.
func TestALaunchWhileAJailRunsKeepsTheLegacyHome(t *testing.T) {
	for _, tc := range []struct {
		name string
		// running makes a jail of ws run, and returns its release.
		running func(t *testing.T, ws string, o *Options) func()
	}{
		{"a keeper", func(t *testing.T, ws string, _ *Options) func() {
			held, err := holdLivenessLock(keeperKey(yoloruntime.FromWorkspace(ws), keeperNotchMacosUser))
			if err != nil {
				t.Fatal(err)
			}
			return func() { releaseLock(held) }
		}},
		{"a keeperless session", func(t *testing.T, ws string, _ *Options) func() {
			rec, err := openKeyedSessionRecord(keeperKey(yoloruntime.FromWorkspace(ws), keeperNotchMacosUser),
				4242, time.Unix(0, 0), false)
			if err != nil {
				t.Fatal(err)
			}
			return func() { closeKeyedSessionRecord(rec) }
		}},
		{"a container whose keeper died", func(t *testing.T, ws string, o *Options) func() {
			answerContainerOf(o, yoloruntime.FromWorkspace(ws), "podman", "c0ffee\n", 0)
			return func() {}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `[]`)
			ws := t.TempDir()
			marker := seedLegacyHome(t, ws)

			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
			defer tc.running(t, ws, o)()
			var got macosUserCall
			o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int { got = c; return 0 })
			Run(*o)
			if _, err := os.Stat(filepath.Join(paths.LegacyHomeState(ws), marker)); err != nil {
				t.Errorf("the legacy home moved while a jail of the project ran: %v\nstderr:\n%s", err, stderr.String())
			}
			if got.overlay.Sidecar != paths.LegacyHomeState(ws) {
				t.Errorf("the launch was handed home %q, want the legacy home it is still attributed to", got.overlay.Sidecar)
			}
			if v, _ := got.packEnv.Get(paths.HomeStateEnv); v != "home" {
				t.Errorf("the session's %s = %v, want the legacy home it was bound", paths.HomeStateEnv, v)
			}
			if _, err := os.Lstat(launchHome(t, o)); !os.IsNotExist(err) {
				t.Errorf("the launch created the setup's home %s beside the unmoved legacy home (err %v)", launchHome(t, o), err)
			}
			if !strings.Contains(stderr.String(), "a jail of this project is running") {
				t.Errorf("the deferral does not say a jail runs:\n%s", stderr.String())
			}
		})
	}
}

// answerContainerOf makes o's runtime rt be on PATH beside the launch's own, and answer a probe for
// a container named cname with stdout and rc (probeExistingContainer's `ps -a -q --filter`, or
// Apple Container's `ls --all`); every other command still does not run.
func answerContainerOf(o *Options, cname, rt, stdout string, rc int) {
	look, exec := o.LookPath, o.Exec
	o.LookPath = func(name string) (string, bool) {
		if name == rt {
			return "/usr/bin/" + name, true
		}
		return look(name)
	}
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		probe := len(argv) > 2 && argv[0] == rt &&
			((argv[1] == "ps" && argv[len(argv)-1] == "name=^/"+cname+"$") || (argv[1] == "ls" && argv[2] == "--all"))
		if probe {
			return ExecResult{Ran: true, Stdout: stdout, RC: rc}
		}
		return exec(argv, dir, env, timeout)
	}
}

// TestProjectJailsAsksEveryWayAJailRuns pins projectJails' three probes and its tri-state: a keeper,
// a session record kept or not, and a container in each runtime on PATH; "could not ask" is
// unknown, and a runtime not on PATH is not asked (6a-ii review M1).
func TestProjectJailsAsksEveryWayAJailRuns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, cname string, o *Options) func()
		want  projectJails
	}{
		{"nothing runs", func(*testing.T, string, *Options) func() { return func() {} }, projectJailsStopped},
		{"a kept session's record", func(t *testing.T, cname string, _ *Options) func() {
			rec, err := openKeyedSessionRecord(keeperKey(cname, keeperNotchGuest), 4242, time.Unix(0, 0), true)
			if err != nil {
				t.Fatal(err)
			}
			return func() { closeKeyedSessionRecord(rec) }
		}, projectJailsRunning},
		{"an unheld record is no session", func(t *testing.T, cname string, _ *Options) func() {
			rec, err := openKeyedSessionRecord(keeperKey(cname, keeperNotchMacosUser), 4242, time.Unix(0, 0), false)
			if err != nil {
				t.Fatal(err)
			}
			_ = rec.Close() // the record stays, its lock goes: a session that ended without removing it
			return func() {}
		}, projectJailsStopped},
		{"a podman container", func(_ *testing.T, cname string, o *Options) func() {
			answerContainerOf(o, cname, "podman", "c0ffee\n", 0)
			return func() {}
		}, projectJailsRunning},
		{"an Apple Container container", func(_ *testing.T, cname string, o *Options) func() {
			answerContainerOf(o, cname, "container", "ID IMAGE OS ARCH STATE\n"+cname+" img linux arm64 stopped\n", 0)
			return func() {}
		}, projectJailsRunning},
		{"podman answers none", func(_ *testing.T, cname string, o *Options) func() {
			answerContainerOf(o, cname, "podman", "", 0)
			return func() {}
		}, projectJailsStopped},
		{"podman cannot be asked", func(_ *testing.T, cname string, o *Options) func() {
			answerContainerOf(o, cname, "podman", "", 125)
			return func() {}
		}, projectJailsUnknown},
		{"podman on PATH does not run", func(_ *testing.T, _ string, o *Options) func() {
			o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
			return func() {}
		}, projectJailsUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			o := &Options{Workspace: ws}
			o.LookPath = func(string) (string, bool) { return "", false }
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{} }
			defer tc.setup(t, yoloruntime.FromWorkspace(ws), o)()
			if got := o.projectJails(); got != tc.want {
				t.Errorf("projectJails() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBothKeeperPlansCarryTheLaunchsHome (6a-ii review m1): each plan builder hands its keeper the
// home this launch bound, which its teardown capture and forgetGoneCredentials read, through the
// plan file the keeper reads back. Red if either `Home: o.homeDir()` is deleted: the keeper would
// fall back to the legacy home and capture nothing from the setup's.
func TestBothKeeperPlansCarryTheLaunchsHome(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ws := t.TempDir()
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	want := paths.SetupHomeState(ws, "lab")
	o.homeState = want
	podmanPlan, err := o.keeperPlanFor(jsonx.NewOrderedMap(), "podman", "yolo-home", stagedPacks{}, nil, nil, nil, "", "",
		[]string{"podman", "run"}, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	o.macosUserKey = &macosUserKeying{}
	macPlan, err := o.macosUserKeeperPlanFor(jsonx.NewOrderedMap(), "macos-user", "yolo-home", stagedPacks{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, plan := range map[string]*keeperPlan{"podman": podmanPlan, "macos-user": macPlan} {
		path, err := writeKeeperPlan(plan)
		if err != nil {
			t.Fatal(err)
		}
		back, err := readKeeperPlan(path)
		if err != nil {
			t.Fatal(err)
		}
		if back.Home != want {
			t.Errorf("%s: the keeper plan carries home %q, want the launch's %q", name, back.Home, want)
		}
	}
}

// TestSelectHomeDecidesUnderTheLaunchLock (6a-ii review m2): while a legacy home is in place, the
// launch decides whether to move it under the workspace launch lock and keeps holding it, so no
// other launch can move the home between this one's decision and its jail answering the probe.
// Once the home has moved, selectHome takes no lock. Red if selectHome stops taking it.
func TestSelectHomeDecidesUnderTheLaunchLock(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		home := packHome(t)
		writeUserPacks(t, home, `[]`)
		ws := t.TempDir()
		if legacy {
			seedLegacyHome(t, ws)
		}
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.Setup = setup.MustBuiltIn(o.SetupName) // what Run's resolveSetup leaves
		o.selectHome()
		held := o.launchLock != nil && !o.launchLock.isClosed()
		if held && o.launchLock.path != launchLockPath(yoloruntime.FromWorkspace(ws)) {
			t.Errorf("legacy=%v: selectHome holds lock %s, not the workspace's launch lock", legacy, o.launchLock.path)
		}
		if held != legacy {
			t.Errorf("legacy home in place=%v: selectHome holds the launch lock=%v\nstderr:\n%s", legacy, held, stderr.String())
		}
		o.releaseLaunchLock()
	}
}

// TestTheContainerArgvTellsTheSessionItsHome (SUP-D35): the container's environment names the
// home the launch bound, relative to .yolo, which the jail's boot keys its capture store to. Red if
// commonEnvBlock stops emitting it.
func TestTheContainerArgvTellsTheSessionItsHome(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	for _, tc := range []struct{ bound, want string }{
		{paths.SetupHomeState(ws, "lab"), "setups/lab/home"},
		{paths.LegacyHomeState(ws), "home"},
	} {
		o := goldenOptions(ws, home)
		o.homeState = tc.bound
		sec := jsonx.NewOrderedMap()
		sec.Set("blocked_tools", []any{})
		argv := o.assembleRunCmd(&assembleInput{
			cfg: newConfig("security", sec), rt: "podman", cname: "yolo-ws-abcd1234",
			packs: claudePackFixture(t), agentsPath: ws, wsState: tc.bound, miseStore: "/mise-store",
			yoloVersion: "9.9.9-test", mountTargets: map[string]struct{}{},
		})
		if !slices.Contains(argv, paths.HomeStateEnv+"="+tc.want) {
			t.Errorf("bound %s: the argv does not carry %s=%s:\n%v", tc.bound, paths.HomeStateEnv, tc.want, argv)
		}
	}
}

// TestMacosUserLaunchCarriesTheSetupsHome: with no legacy home, the macos-user arm is handed the
// selected setup's home, for each Seatbelt setup. Red if run.go stops setting the overlay's Sidecar.
func TestMacosUserLaunchCarriesTheSetupsHome(t *testing.T) {
	for name, mode := range map[string]macosuser.Mode{"self": macosuser.ModeSelf, "account": macosuser.ModeAccount} {
		t.Run(name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `[]`)
			ws := t.TempDir()
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
			selectMacosUserMode(o, mode)
			var got macosUserCall
			o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int { got = c; return 0 })
			if rc := Run(*o); rc != 0 {
				t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
			}
			if want := paths.SetupHomeState(ws, o.SetupName); got.overlay.Sidecar != want {
				t.Errorf("handed home %q, want %q", got.overlay.Sidecar, want)
			}
			if _, err := os.Lstat(paths.LegacyHomeState(ws)); !os.IsNotExist(err) {
				t.Errorf("the launch made a legacy home (err %v)", err)
			}
		})
	}
}

// TestALinkAtTheSetupsHomeRefusesTheLaunch (SUP-D30): the setup home is bound by pathname after
// the no-link check, so a link a jail left at `.yolo/setups` refuses the launch before the backend.
func TestALinkAtTheSetupsHomeRefusesTheLaunch(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(paths.WorkspaceStateDir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.SetupsDir(ws)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	reached := false
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { reached = true; return 0 })
	if rc := Run(*o); rc == 0 || reached {
		t.Fatalf("Run() = %d, reached = %v: a linked setups dir did not refuse the launch\nstderr:\n%s", rc, reached, stderr.String())
	}
	if !strings.Contains(stderr.String(), paths.SetupsDir(ws)) {
		t.Errorf("the refusal does not name the link:\n%s", stderr.String())
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("wrote %d entries through the link", len(entries))
	}
}

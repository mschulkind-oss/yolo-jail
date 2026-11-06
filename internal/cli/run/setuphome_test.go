package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
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
	// The mountpoint record beside the home goes with it (preparePackFiles reads it beside its home).
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(want), packFilesMountpointManifestName)); err != nil || !bytes.Equal(b, record) {
		t.Errorf("the mountpoint record did not move beside the setup's home: %q, %v", b, err)
	}
	if n := strings.Count(stderr.String(), "Moved this project's home"); n != 1 {
		t.Errorf("the move's informational line was printed %d times, want once:\n%s", n, stderr.String())
	}
}

// TestALaunchWhileAJailRunsKeepsTheLegacyHome (SUP-D9/SUP-D21): with a jail of the project running,
// the legacy home stays where it is and this launch binds it, so two jails never split one home.
func TestALaunchWhileAJailRunsKeepsTheLegacyHome(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	marker := seedLegacyHome(t, ws)
	held, err := holdLivenessLock(keeperKey(yoloruntime.FromWorkspace(ws), keeperNotchMacosUser))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(held)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	var got macosUserCall
	o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int { got = c; return 0 })
	Run(*o)
	if _, err := os.Stat(filepath.Join(paths.LegacyHomeState(ws), marker)); err != nil {
		t.Errorf("the legacy home moved while a jail of the project ran: %v\nstderr:\n%s", err, stderr.String())
	}
	if got.overlay.Sidecar != paths.LegacyHomeState(ws) {
		t.Errorf("the launch was handed home %q, want the legacy home it is still attributed to", got.overlay.Sidecar)
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

package run

import (
	"bytes"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// G15 (docs/plans/setup-support-gaps.md): the PLATFORM axis of the inert report read the
// manifest's `default_enabled` and never the user's `loopholes.<name>.enabled`. So a user who
// switched ON a Linux-only loophole (audio, journal, host-processes, …) on a Mac got a clean
// launch and no line, and one who switched OFF a default-on loophole still heard it was
// unsupported. Both halves drive a REAL launch through Run(), so deleting the config from the
// report's selection fails them — a direct call to the producer would not.

// isolatedPackModules clears the process-wide pack-module record before and after, so a record
// staged by an earlier test cannot stand in for this launch's local pack.
func isolatedPackModules(t *testing.T) {
	t.Helper()
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
}

// inertReportLaunch runs one real macos-user launch, or its PLAN RENDER when dryRun is set.
// The two arms reach the inert report through DIFFERENT call sites (the dry-run branch of the
// macos-user arm, and the spawn-disclosure path of a real launch), so each test runs both, or
// deleting the config from one of them would leave the gate green.
func inertReportLaunch(t *testing.T, ws string, dryRun bool) (string, int) {
	t.Helper()
	if !dryRun {
		got := macosUserLaunch(t, ws)
		return got.out, got.rc
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool) int {
		return 0
	}
	rc := Run(*o)
	return stdout.String() + stderr.String(), rc
}

var inertReportArms = []struct {
	name   string
	dryRun bool
}{{"launch", false}, {"dry-run", true}}

func foreignPlatform() string {
	if goruntime.GOOS == "darwin" {
		return "linux"
	}
	return "darwin"
}

// ENABLED BY THE USER, OFF BY DEFAULT: the launch must name it and say why.
func TestLaunchReportsAUserEnabledPlatformInertLoophole(t *testing.T) {
	for _, arm := range inertReportArms {
		t.Run(arm.name, func(t *testing.T) {
			isolatedPackModules(t)
			home := packHome(t)
			ws := t.TempDir()
			writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": false, "transport": "none",
		"platforms": ["`+foreignPlatform()+`"]}`)
			writeUserConfigJSON(t, home, `{"packs": [], "loopholes": {"acme-proxy": {"enabled": true}}}`)

			out, rc := inertReportLaunch(t, ws, arm.dryRun)
			if rc != 0 {
				t.Fatalf("Run() = %d, want 0\n%s", rc, out)
			}
			for _, want := range []string{"local: loophole acme-proxy is ", "unsupported on " + goruntime.GOOS} {
				if !strings.Contains(out, want) {
					t.Errorf("the user ENABLED a loophole this machine cannot run, and the %s did "+
						"not say %q. The report read the manifest default (false) instead of the "+
						"merged config (true) — G15.\noutput:\n%s", arm.name, want, out)
				}
			}
		})
	}
}

// DISABLED BY THE USER, ON BY DEFAULT: the user's switch preempts the platform line, the same
// disabled-skip PlatformInertNotes states — it must be the USER's switch, not the author's.
func TestLaunchStaysQuietForAUserDisabledPlatformInertLoophole(t *testing.T) {
	for _, arm := range inertReportArms {
		t.Run(arm.name, func(t *testing.T) {
			isolatedPackModules(t)
			home := packHome(t)
			ws := t.TempDir()
			writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "default_enabled": true, "transport": "none",
		"platforms": ["`+foreignPlatform()+`"]}`)
			writeUserConfigJSON(t, home, `{"packs": [], "loopholes": {"acme-proxy": {"enabled": false}}}`)

			out, rc := inertReportLaunch(t, ws, arm.dryRun)
			if rc != 0 {
				t.Fatalf("Run() = %d, want 0\n%s", rc, out)
			}
			// The line must still be ABSENT for the right reason: a render that printed
			// nothing at all would pass this half, which the ENABLED test rules out.
			if strings.Contains(out, "loophole acme-proxy is ") {
				t.Errorf("the user DISABLED this loophole, and the %s still reported it inert for "+
					"the platform — the report read the manifest default (true) instead of the "+
					"merged config (false).\noutput:\n%s", arm.name, out)
			}
		})
	}
}

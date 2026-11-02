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

// G15 (docs/plans/setup-support-gaps.md): the PLATFORM axis of the inert report. A pack's
// loophole runs whenever its pack is selected, so a Linux-only loophole (audio, journal,
// host-processes, …) selected on a Mac is named with why, and a config `enabled` key, which
// decides nothing for a pack's loophole, silences nothing. Both halves drive a REAL launch
// through Run(), so a report that stopped asking the producer fails them.

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
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
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

// SELECTED, INERT HERE: the launch must name it and say why.
func TestLaunchReportsASelectedPlatformInertLoophole(t *testing.T) {
	for _, arm := range inertReportArms {
		t.Run(arm.name, func(t *testing.T) {
			isolatedPackModules(t)
			home := packHome(t)
			ws := t.TempDir()
			writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "transport": "none",
		"platforms": ["`+foreignPlatform()+`"]}`)
			writeUserConfigJSON(t, home, `{"packs": []}`)

			out, rc := inertReportLaunch(t, ws, arm.dryRun)
			if rc != 0 {
				t.Fatalf("Run() = %d, want 0\n%s", rc, out)
			}
			for _, want := range []string{"local: loophole acme-proxy is ", "unsupported on " + goruntime.GOOS} {
				if !strings.Contains(out, want) {
					t.Errorf("a selected loophole this machine cannot run drew no %q in the %s "+
						"— G15.\noutput:\n%s", want, arm.name, out)
				}
			}
		})
	}
}

// A USER `enabled: false` SWITCHES NOTHING for a pack's loophole, so it does not silence the
// platform line either: the line is about the selected pack, which the key does not unselect.
func TestAUserSwitchDoesNotSilenceAPlatformInertPackLoophole(t *testing.T) {
	for _, arm := range inertReportArms {
		t.Run(arm.name, func(t *testing.T) {
			isolatedPackModules(t)
			home := packHome(t)
			ws := t.TempDir()
			writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "transport": "none",
		"platforms": ["`+foreignPlatform()+`"]}`)
			writeUserConfigJSON(t, home, `{"packs": [], "loopholes": {"acme-proxy": {"enabled": false}}}`)

			out, rc := inertReportLaunch(t, ws, arm.dryRun)
			if rc != 0 {
				t.Fatalf("Run() = %d, want 0\n%s", rc, out)
			}
			if !strings.Contains(out, "loophole acme-proxy is ") {
				t.Errorf("a config `enabled: false`, which switches nothing for a pack's "+
					"loophole, silenced the %s's platform line.\noutput:\n%s", arm.name, out)
			}
		})
	}
}

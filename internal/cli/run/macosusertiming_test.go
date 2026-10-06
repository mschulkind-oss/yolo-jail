package run

// macosusertiming_test.go pins the macos-user arm's timing surface (docs/reference/perf-logging.md):
// a launch that reaches the dispatch reports, after its teardown, with the backend's span, its
// keeper's start and the keeper's teardown it streamed inside the table, and the keeper records its
// own spans in the same host perf log; a recording-only launch prints the quiet line; a
// refusal before the dispatch prints neither; and the table names no jail half, since the
// bootstrap keeps no jail perf log. Each fails if the arm's deferred report is deleted.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// timedMacosUserLaunch drives Run to the macos-user dispatch over a config whose one host service
// is a config-declared loophole, so the teardown has a front to stop, with a stub backend returning
// rc. env adds to the stub's environment.
func timedMacosUserLaunch(t *testing.T, timing bool, env map[string]string, rc int) (int, string) {
	t.Helper()
	got, out, _ := timedMacosUserLaunchIn(t, timing, env, rc)
	return got, out
}

// timedMacosUserLaunchIn is timedMacosUserLaunch, also naming the workspace it launched.
func timedMacosUserLaunchIn(t *testing.T, timing bool, env map[string]string, rc int) (int, string, string) {
	t.Helper()
	home := packHome(t)
	writeUserConfigJSON(t, home, `{
	  "packs": [],
	  "loopholes": {
	    "acme-proxy": {"enabled": true, "description": "acme proxy",
	      "command": `+testHostDaemonCmdJSON("acme-timing-daemon")+`}
	  }
	}`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Timing = timing
	getenv := o.Getenv
	o.Getenv = func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return getenv(k)
	}
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay,
		macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return rc
	}
	return Run(*o), stdout.String() + stderr.String(), ws
}

func TestAMacosUserLaunchReportsItsTimingAfterItsTeardown(t *testing.T) {
	rc, out, ws := timedMacosUserLaunchIn(t, true, nil, 7)
	if rc != 7 {
		t.Fatalf("Run() = %d, want the backend's 7\n%s", rc, out)
	}
	header := strings.Index(out, "--- Host-side timing (rc 7) ---")
	if header < 0 {
		t.Fatalf("no timing report for a --timing macos-user launch:\n%s", out)
	}
	table := out[header:]
	// THE HOST SERVICES ARE THE WORKSPACE'S KEEPER'S (docs/design/jail-lifetime-last-session-wins.md
	// §9.9): this launch's report has the keeper's start and, as the last session, the teardown it
	// streamed; the keeper's own spans are in the same host perf log, recorded by the keeper.
	for _, row := range []string{"launch.refresh_jail_briefings", "launch.build_home_overlay", "launch.build_ctx_tree",
		"launch.start_keeper", "launch.macos_user", "session.keeper_teardown"} {
		if !strings.Contains(table, row) {
			t.Errorf("the report has no %s row:\n%s", row, table)
		}
	}
	if strings.Index(table, "launch.macos_user") > strings.Index(table, "session.keeper_teardown") {
		t.Errorf("the teardown's span precedes the backend's:\n%s", table)
	}
	file, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(ws), HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	for _, span := range []string{"launch.start_loopholes", "launch.start_doorways", "launch.start_services",
		"shutdown.stop_front.acme-proxy", "shutdown.stop_loopholes"} {
		if !strings.Contains(string(file), span) {
			t.Errorf("the keeper recorded no %s span in the host perf log:\n%s", span, file)
		}
	}
	if strings.Contains(table, "jail half:") {
		t.Errorf("a macos-user report names a jail half, and the bootstrap keeps no jail perf log:\n%s", table)
	}
}

// A RECORDING-ONLY LAUNCH (YOLO_TIMING exported) prints the one quiet line and no table.
func TestAMacosUserLaunchThatOnlyRecordsPrintsTheQuietLine(t *testing.T) {
	rc, out := timedMacosUserLaunch(t, false, map[string]string{paths.TimingEnv: "1"}, 0)
	if rc != 0 {
		t.Fatalf("Run() = %d\n%s", rc, out)
	}
	if !strings.Contains(out, "timings recorded in") || strings.Contains(out, "Host-side timing") {
		t.Errorf("a recording-only macos-user launch must print the quiet line alone:\n%s", out)
	}
}

// A REFUSAL BEFORE THE DISPATCH prints the refusal and no report: the credential pre-flight, which
// runs after the arm is installed.
func TestAMacosUserRefusalBeforeTheDispatchPrintsNoReport(t *testing.T) {
	o, stderr, seen := overrideNativeLaunch(t,
		awsAuthUserConfig(inEnvSources(map[string]string{"WIDGET_TOKEN": "frozen"})),
		shellWith(nil))
	writeWidgetLocalPack(t, seen.home)
	o.Timing = true
	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d (reached %v), want the pre-flight's refusal\n%s", rc, seen.reached, stderr.String())
	}
	if strings.Contains(stderr.String(), "Host-side timing") {
		t.Errorf("a launch refused before the dispatch printed a timing report:\n%s", stderr.String())
	}
}

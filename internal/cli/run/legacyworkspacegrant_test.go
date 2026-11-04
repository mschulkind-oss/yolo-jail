package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestAnApprovedLegacyWorkspaceLaunchesWithNoPrompt is LG-D16 at the launch
// (docs/reference/loophole-grants.md): a legacy workspace `loopholes.host-processes` block that
// switches the loophole on with its `visible` names is the request and, once the config-change
// gate approved the file, its admission, with no `yolo allow` line in the user config. So after
// one approved launch the next launch runs with no loophole-grant prompt, as v0.15.1's one y did.
func TestAnApprovedLegacyWorkspaceLaunchesWithNoPrompt(t *testing.T) {
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"packs": ["host-processes"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"loopholes": {"host-processes": {"enabled": true, "settings": {"visible": ["postgres"]}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	launch := func(accept bool) brokerLaunch {
		t.Helper()
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.AcceptConfigChanges = accept
		var l brokerLaunch
		o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
			l.reached = true
			return 0
		}
		l.rc = Run(*o)
		l.out = stdout.String() + stderr.String()
		return l
	}
	// Unapproved, the gate asks once, naming the workspace file as the request: no ceiling line.
	unapproved := launch(false)
	if unapproved.reached || !strings.Contains(unapproved.out, "host-processes: see postgres (requested by yolo-jail.jsonc)") ||
		strings.Contains(unapproved.out, "yolo allow host-processes") {
		t.Fatalf("the unapproved launch did not ask for the legacy switch's names as the workspace's request: rc %d\n%s",
			unapproved.rc, unapproved.out)
	}
	first := launch(true)
	if first.rc != 0 || !first.reached {
		t.Fatalf("the approving launch did not run: rc %d\n%s", first.rc, first.out)
	}
	// v0.15.1 recorded only the approved config, never a loophole-grants record: the approval of
	// the file is what admits its switch, so the next launch prompts nothing without one.
	records, err := filepath.Glob(filepath.Join(home, ".local", "share", "yolo-jail", "approvals", "*.loophole-grants.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("approval records = %v (%v), want one loophole-grants record", records, err)
	}
	if err := os.Remove(records[0]); err != nil {
		t.Fatal(err)
	}
	again := launch(false)
	if again.rc != 0 || !again.reached || strings.Contains(again.out, "Loophole grants for this project") {
		t.Fatalf("the approved legacy workspace prompted again: rc %d reached %v\n%s", again.rc, again.reached, again.out)
	}
}

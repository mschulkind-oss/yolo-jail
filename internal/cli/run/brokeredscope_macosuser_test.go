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

// The macos-user arm's call site hands the gate the MERGED config, which is where the
// github-broker's enable lives (it ships off, and a user turns it on at user scope). Handed
// anything less, the gate finds no broker in play, asks nothing about the remotes, and the
// broker starts with an empty scope: every in-scope read answers 64, silently, on the one
// backend no integration test launches. So this drives Run itself, not the gate.
func TestTheMacosUserArmAsksAboutTheScopeOfAnEnabledBroker(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{
  "packs": ["github"],
  "loopholes": {"github-broker": {"enabled": true}}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc == 0 || reached {
		t.Fatalf("a macos-user launch with no terminal ran past an unapproved repository scope: rc %d, "+
			"reached=%v\nstdout:\n%s\nstderr:\n%s", rc, reached, stdout.String(), stderr.String())
	}
	out := stdout.String() + stderr.String()
	for _, want := range []string{"github-broker repository scope, read from", `+ o/r  remote "origin"  added`,
		"--accept-config-changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not show %q:\n%s", want, out)
		}
	}
}

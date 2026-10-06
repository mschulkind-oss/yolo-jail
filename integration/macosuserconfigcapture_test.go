package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E3 ON A MAC: a session's edit to a capture-mode surface reaches its overlay sidecar when the
// session ends, folded from the HOST side, so `yolo config diff` answers about the session that
// just ended rather than the one before. E3 is docs/plans/setup-support-gaps.md's name for that
// fold, which a container's teardown runs.
//
// What the Linux gate cannot reach and this does: the host user writing a sidecar in the
// sandbox-created <ws>/.yolo/prism (through the workspace ACL's grant), and reading the surface the
// sandbox account wrote through the account home's link into <ws>/.yolo/home/claude. ONE launch:
// the next boot would fold the edit anyway, so a second launch would hide the very defect this pins.
func TestMacosUserCapturesConfigEditsWhenTheSessionEnds(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUser(t, ws, `
node -e '
const fs = require("fs");
const f = process.env.HOME + "/.claude/settings.json";
const j = JSON.parse(fs.readFileSync(f, "utf8"));
j.yoloItE3Marker = "captured-at-exit";
fs.writeFileSync(f, JSON.stringify(j, null, 2));
' && echo "=== EDITED ==="
`)
	if r.rc != 0 || !strings.Contains(r.stdout, "=== EDITED ===") {
		t.Fatalf("the macos-user launch did not make its edit (rc %d), so nothing below is a "+
			"statement about the capture.\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	overlay := filepath.Join(ws, ".yolo", "prism", "claude-settings.overlay.json")
	got, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("no overlay sidecar after the session ended (%v): the host-side capture did "+
			"not run, or could not write <ws>/.yolo/prism.\nstderr:\n%s", err, r.stderr)
	}
	if !strings.Contains(string(got), "captured-at-exit") {
		t.Fatalf("the session's edit to ~/.claude/settings.json is not in %s once it ended, so "+
			"`yolo config diff` reports the previous session.\noverlay:\n%s\nstderr:\n%s",
			overlay, got, r.stderr)
	}
}

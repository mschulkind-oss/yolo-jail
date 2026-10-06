package cli

// packlintpatched_test.go pins `yolo pack lint` on a PATCHED EXTENSION
// (docs/design/patched-extensions.md §8.2, PPX-D10): a pack whose only content is a series and the
// declaration lints clean — its series directory is content the pack ships, not content nothing
// reads — and the one lint names the list entry to add while no entry names the tree.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePatchedExtensionPack(t *testing.T, listed bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "matt")
	if err := os.MkdirAll(filepath.Join(dir, "patches", "pi-subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A member a launch reads: lint reads the series as a launch does (PF-D63).
	if err := os.WriteFile(filepath.Join(dir, "patches", "pi-subagents", "0001-x.patch"), []byte(lintMember(lintBase)), 0o644); err != nil {
		t.Fatal(err)
	}
	list := ""
	if listed {
		list = `, {"kind": "config-list", "surface": "pi/settings", "path": "/packages",
			"add": ["~/.pi/agent/yolo-patched/pi-subagents"]}`
	}
	manifest := `{"name": "matt", "contributes": [
		{"kind": "files", "into": ".pi/agent/yolo-patched/pi-subagents",
		 "source": "git+https://github.com/upstream/pi-subagents?ref=main",
		 "patches": "patches/pi-subagents"}` + list + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackLintOnAPatchedExtension(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", writePatchedExtensionPack(t, false)}, &out, &errw, false); rc != 0 {
		t.Fatalf("a patched extension's pack does not lint clean: rc %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), `"~/.pi/agent/yolo-patched/pi-subagents"`) ||
		!strings.Contains(out.String(), "no agent loads it") {
		t.Errorf("lint does not name the list entry to add:\n%s", out.String())
	}
	out.Reset()
	if rc := packMain([]string{"lint", writePatchedExtensionPack(t, true)}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc %d\n%s%s", rc, out.String(), errw.String())
	}
	if strings.Contains(out.String(), "no agent loads it") {
		t.Errorf("a listed tree is still linted:\n%s", out.String())
	}
}

package cli

// packlsnamespace_test.go pins docs/design/pack-naming.md §8's two inspection lines: `yolo pack ls`
// shows each entry's namespace beside its handle, and `yolo pack explain` lists each skill under
// the name agents see it by.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackLsAndExplainShowTheDeliveredNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfg := filepath.Join(home, ".config", "yolo-jail")
	pack := filepath.Join(cfg, "packs", "tools")
	writeFile(t, filepath.Join(pack, "pack.json"),
		`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
	writeFile(t, filepath.Join(pack, "skills", "Review_It", "SKILL.md"), "---\nname: Review_It\n---\n")
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("packs = [\"local/tools\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if rc := packMain([]string{"ls"}, &out, &errw, false); rc != 0 {
		t.Fatalf("ls rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "local/tools") || !strings.Contains(out.String(), "namespace: tools") {
		t.Errorf("pack ls does not show the handle and its namespace:\n%s", out.String())
	}

	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"explain", "local/tools"}, &out, &errw, false); rc != 0 {
		t.Fatalf("explain rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "delivers 1 skill(s)") || !strings.Contains(out.String(), "tools-review-it") {
		t.Errorf("pack explain does not list the skill under its delivered name:\n%s", out.String())
	}
}

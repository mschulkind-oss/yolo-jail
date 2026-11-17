package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Design §5.1/GSS-D12: a legacy `only` filter WITHHOLDS
// the pack's sourced skills (and fetches nothing). Withholding is not a failure.
func TestHostApplySourcedOnlyWithholdingIsNotAFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	first, reader := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = \"first\"\n", "git+file:///nonexistent/never-fetched//skills/first?ref=main"))
	writeFile(t, filepath.Join(first, "skills/local/SKILL.md"), "---\nname: local\n---\nlocal\n")
	writeFile(t, filepath.Join(reader, "pack.toml"), "[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n")
	config := fmt.Sprintf(`{"packs":[{"name":"first","source":%q,"only":["pack.toml","skills/**"]},{"name":"reader","source":%q}]}`, "file://"+first, "file://"+reader)
	writeFile(t, filepath.Join(home, ".config/yolo-jail/config.jsonc"), config)
	rc, report := applyWith(t, true, strings.NewReader(""))
	t.Logf("rc=%d\n%s", rc, report)
	if _, err := os.Lstat(filepath.Join(home, ".local/share/yolo-jail")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(home, ".local/share/yolo-jail"))
		t.Logf("data dir entries: %v", entries)
	}
	if rc != 0 {
		t.Fatalf("host apply --assert exits %d for a pack whose sourced rows are merely withheld by `only`", rc)
	}
}

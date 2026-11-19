package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackAddScopedTOMLDryRunIsPure(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs = []\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	dir := filepath.Join(filepath.Dir(user.Path), "packs/corpus/skills/core-tools/skill-a")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("chosen\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(user.Path)
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runPackCapture(t, project.Root, []string{"add", "local/corpus", "--skills-dir", "skills/core-tools", "--only", "skill-a", "--dry-run"})
	if rc != 0 || errw != "" {
		t.Fatalf("rc=%d out=%s err=%s", rc, out, errw)
	}
	after, err := os.ReadFile(user.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("dry-run wrote: %q %v", after, err)
	}
	for _, want := range []string{"skill-a", "skills/core-tools", "Dry run", "inspected: local directory"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
}

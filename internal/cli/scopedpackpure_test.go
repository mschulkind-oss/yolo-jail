package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestDirectScopedPureInspectDoesNotReadRawPoisonOrFetch(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, `packs=[{pack="local/corpus",skills={dir="raw",only=["chosen"]}}]`+"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	if err := os.MkdirAll(filepath.Join(root, "raw/chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "raw/chosen/SKILL.md"), []byte("chosen\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte("broken TOML [\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Explain is the configured-handle surface; author-directory lint/footprint
	// keep their existing whole-directory/shipped meanings.
	for _, args := range [][]string{{"explain", "local/corpus"}} {
		rc, out, errw := runPackCapture(t, project.Root, args)
		if rc != 0 || errw != "" || strings.Contains(out, "broken TOML") || !strings.Contains(out, "candidates: [chosen]") {
			t.Fatalf("pure scoped %v: rc=%d out=%s err=%s", args, rc, out, errw)
		}
	}
	if _, err := os.Lstat(filepath.Join(paths.PacksDir(), "mirrors")); !os.IsNotExist(err) {
		t.Fatalf("pure inspect created mirror: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(user.Path), "packs.lock.json")); !os.IsNotExist(err) {
		t.Fatalf("pure inspect wrote pin: %v", err)
	}
}

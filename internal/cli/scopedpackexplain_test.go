package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackExplainDirectScopedAllHandleTuplesWithoutMaterialization(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, `packs=[{pack="local/corpus",skills={dir="one"}},{pack="local/corpus",skills={dir="two"}}]`+"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, name := range []string{"one", "two"} {
		dir := filepath.Join(root, name, name+"-chosen")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/corpus"})
	if rc != 0 || errw != "" {
		t.Fatalf("scoped explain: %d %s %s", rc, out, errw)
	}
	for _, want := range []string{`dir="one"`, `dir="two"`, `selected: [one-chosen]`, `selected: [two-chosen]`} {
		if !strings.Contains(out, want) {
			t.Fatalf("tuple dropped %q: %s", want, out)
		}
	}
	if strings.Contains(out, "delivers registration") {
		t.Fatalf("pure declaration claimed materialized delivery: %s", out)
	}
	if _, err := os.Lstat(paths.PacksDir()); !os.IsNotExist(err) {
		t.Fatalf("pure explain materialized store: %v", err)
	}
}

func TestPackExplainNeverFetchedScopedSelectionIsUnverified(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, `packs=[{git="https://example.invalid/fixture/corpus",ref="main",skills={only=["chosen"]}}]`+"\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "fixture/corpus"})
	if rc != 0 || errw != "" || !strings.Contains(out, "selection not yet verified; resolved at next launch") || strings.Contains(out, "delivers registration") {
		t.Fatalf("pure missing scope: %d %s %s", rc, out, errw)
	}
	if _, err := os.Lstat(paths.PacksDir()); !os.IsNotExist(err) {
		t.Fatalf("pure explain created mirror/tree: %v", err)
	}
}

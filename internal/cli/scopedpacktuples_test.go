package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func scopedCLIWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPackExplainScopedThreeLegalRootsPreservesAllTuples(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs=[{pack='local/corpus',subdir='one',skills={dir='raw'}},{pack='local/corpus',subdir='two',skills={dir='raw'}},{pack='local/corpus',subdir='whole'}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, name := range []string{"one", "two"} {
		scopedCLIWrite(t, filepath.Join(root, name, "raw", name+"-chosen/SKILL.md"), name+"\n")
	}
	scopedCLIWrite(t, filepath.Join(root, "whole/skills/ordinary/SKILL.md"), "whole\n")
	before := scopedCLISnapshot(t, root, filepath.Dir(user.Path), paths.PacksDir(), paths.EmbeddedPacksDir())
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/corpus"})
	if after := scopedCLISnapshot(t, root, filepath.Dir(user.Path), paths.PacksDir(), paths.EmbeddedPacksDir()); !reflect.DeepEqual(before, after) {
		t.Fatal("mixed Explain wrote source/config/store/carrier/cache")
	}
	if rc != 0 || errw != "" {
		t.Fatalf("legal multi-tuple explain: %d %s %s", rc, out, errw)
	}
	previous := -1
	for _, want := range []string{"packs[0] local/corpus", "packs[1] local/corpus", "packs[2] local/corpus"} {
		at := strings.Index(out, want)
		if at <= previous {
			t.Fatalf("lost/order tuple %q: %s", want, out)
		}
		previous = at
	}
	for _, want := range []string{`subdir="one"`, `subdir="two"`, `subdir="whole"`, `mode=raw`, `mode=whole`, `config.toml:`, `selected: [one-chosen]`, `selected: [two-chosen]`, `stages 1 file(s)`, `delivers 1 skill(s)`, `corpus-ordinary`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing tuple/body %q: %s", want, out)
		}
	}
}

func TestPackExplainSingleUnscopedKeepsExactBaselineBody(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs=['local/corpus']\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(root, "skills/one/SKILL.md"), "one\n")
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/corpus"})
	want := fmt.Sprintf("local/corpus → file://%s\nstages 1 file(s)\n  skills/one/SKILL.md\ndelivers 1 skill(s) (namespace corpus)\n  corpus-one\n", filepath.ToSlash(root))
	if rc != 0 || errw != "" || out != want {
		t.Fatalf("single unscoped baseline changed: rc=%d got=%q want=%q err=%s", rc, out, want, errw)
	}
}

func TestAuthorLintAndFootprintIgnoreScopedUserSelection(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs=[]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	author := filepath.Join(project.Root, "local/corpus")
	scopedCLIWrite(t, filepath.Join(author, "skills/marked/SKILL.md"), "marked\n")
	scopedCLIWrite(t, filepath.Join(author, "skills/folder/data.txt"), "folder\n")
	source := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(source, "raw/chosen/SKILL.md"), "chosen\n")
	for _, args := range [][]string{{"lint", "local/corpus"}, {"footprint", "local/corpus"}, {"footprint", "cgroup-delegate"}, {"footprint"}} {
		if err := os.WriteFile(user.Path, []byte("packs=[]\n"), 0644); err != nil {
			t.Fatal(err)
		}
		beforeRC, beforeOut, beforeErr := runPackCapture(t, project.Root, args)
		if err := os.WriteFile(user.Path, []byte("packs=[{pack='local/corpus',skills={dir='raw',only=['chosen']}}]\n"), 0644); err != nil {
			t.Fatal(err)
		}
		rc, out, errw := runPackCapture(t, project.Root, args)
		if rc != beforeRC || out != beforeOut || errw != beforeErr {
			t.Fatalf("author surface routed scoped config %v: before=(%d,%q,%q) after=(%d,%q,%q)", args, beforeRC, beforeOut, beforeErr, rc, out, errw)
		}
		if strings.Contains(out, "chosen") || strings.Contains(out, "scoped pack") {
			t.Fatalf("author surface leaked selected scope: %s", out)
		}
	}
}

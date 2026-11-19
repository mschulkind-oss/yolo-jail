package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackScopedAddEditWhyRemoveRoundTripPreservesComments(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "# top\npacks=[]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, name := range []string{"a", "b", "c"} {
		scopedCLIWrite(t, filepath.Join(root, "raw", name, "SKILL.md"), name+"\n")
	}
	rc, out, errw := runPackCapture(t, project.Root, []string{"add", "local/corpus", "--skills-dir", "raw", "--only", "a", "--only", "b", "--exclude", "c", "--namespace", "team"})
	if rc != 0 || errw != "" {
		t.Fatalf("add: %d %s %s", rc, out, errw)
	}
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("load: %v %v", err, problems)
	}
	var chosen config.PackEntry
	for _, e := range entries {
		if e.Name == "local/corpus" {
			chosen = e
		}
	}
	resolved, err := config.ResolvePack(chosen, config.ResolvePackSpec{ReadOnlyStore: true})
	if err != nil || resolved.Pack.Projection.Namespace != "team" || strings.Join(resolved.Pack.Projection.Selected, ",") != "a,b" {
		t.Fatalf("entry/projection round-trip: %+v %v", resolved, err)
	}
	rc, out, errw = dispatchConfigCapture(t, []string{"why", "packs[0].skills.only[0]"})
	if rc != 0 || !strings.Contains(out, "config.toml:") || !strings.Contains(out, `"a"`) {
		t.Fatalf("why origin: %d %s %s", rc, out, errw)
	}
	rc, out, errw = runConfigEditCapture(t, project.Root, []string{"set", "packs[0].skills.only[0]", `"b"`, "--user"})
	// Duplicate selector would be invalid: verify it refuses transactionally.
	if rc == 0 {
		t.Fatalf("duplicate indexed selector accepted: %s %s", out, errw)
	}
	rc, out, errw = runConfigEditCapture(t, project.Root, []string{"set", "packs[0].skills.only", `["a"]`, "--user"})
	if rc != 0 || errw != "" {
		t.Fatalf("selector edit: %d %s %s", rc, out, errw)
	}
	before, err := os.ReadFile(user.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"remove", "local/corpus", "--skills-dir", "wrong"}, {"add", "local/corpus", "--skills-dir", "raw", "--only", "a"}} {
		rc, _, _ = runPackCapture(t, project.Root, args)
		if rc == 0 {
			t.Fatalf("wrong/duplicate tuple accepted: %v", args)
		}
		after, _ := os.ReadFile(user.Path)
		if !bytes.Equal(before, after) {
			t.Fatalf("refused edit wrote: %v", args)
		}
	}
	rc, out, errw = runPackCapture(t, project.Root, []string{"remove", "local/corpus", "--skills-dir", "raw"})
	if rc != 0 || errw != "" {
		t.Fatalf("remove: %d %s %s", rc, out, errw)
	}
	after, _ := os.ReadFile(user.Path)
	if !bytes.Contains(after, []byte("# top\n")) || bytes.Contains(after, []byte("local/corpus")) {
		t.Fatalf("remove disturbed comment/item: %s", after)
	}
}

func TestPackScopedRemoveAmbiguityAndOneTupleOnly(t *testing.T) {
	project := testconfig.Workspace(t, "")
	original := "# top\npacks=[\n # one stays as commentary\n {pack='local/corpus',skills={dir='one'}},\n # two untouched\n {pack='local/corpus',skills={dir='two'}},\n]\n"
	user := testconfig.User(t, original)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, name := range []string{"one", "two"} {
		scopedCLIWrite(t, filepath.Join(root, name, "chosen/SKILL.md"), name+"\n")
	}
	rc, _, errw := runPackCapture(t, project.Root, []string{"remove", "local/corpus"})
	if rc == 0 || !strings.Contains(errw, "ambiguous") || !strings.Contains(errw, "packs[0]") || !strings.Contains(errw, "packs[1]") || !strings.Contains(errw, "yolo config unset") {
		t.Fatalf("ambiguity dropped entries: %d %s", rc, errw)
	}
	if after, _ := os.ReadFile(user.Path); string(after) != original {
		t.Fatal("ambiguous removal wrote")
	}
	rc, out, errw := runPackCapture(t, project.Root, []string{"remove", "local/corpus", "--skills-dir", "one"})
	if rc != 0 || errw != "" {
		t.Fatalf("exact removal: %d %s %s", rc, out, errw)
	}
	after, _ := os.ReadFile(user.Path)
	for _, keep := range []string{"# top\n", "# one stays as commentary", "# two untouched", "{pack='local/corpus',skills={dir='two'}}"} {
		if !strings.Contains(string(after), keep) {
			t.Fatalf("neighbor changed %q: %s", keep, after)
		}
	}
	if strings.Contains(string(after), "dir='one'") {
		t.Fatalf("wrong tuple removed: %s", after)
	}
}

func TestPackScopedRemoveTargetsFragmentAndIndexedEditsRefuseEmptySelection(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "# main\ninclude_if_found=['imports.toml']\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	fragment := filepath.Join(filepath.Dir(user.Path), "imports.toml")
	scopedCLIWrite(t, fragment, "# imported\npacks=[{pack='local/corpus',skills={only=['chosen']}}]\n")
	scopedCLIWrite(t, filepath.Join(filepath.Dir(user.Path), "packs/corpus/skills/chosen/SKILL.md"), "chosen\n")
	t.Chdir(project.Root)
	before, _ := os.ReadFile(fragment)
	mainBefore, _ := os.ReadFile(user.Path)
	for _, args := range [][]string{{"unset", "packs[0].skills.only[0]", "--user"}, {"unset", "packs[0].skills.only", "--user"}} {
		rc, out, errw := runConfigEditCapture(t, project.Root, args)
		if rc == 0 {
			t.Fatalf("empty selection accepted: %v %s %s", args, out, errw)
		}
		after, _ := os.ReadFile(fragment)
		if !bytes.Equal(before, after) {
			t.Fatal("refused empty edit wrote fragment")
		}
	}
	rc, out, errw := dispatchConfigCapture(t, []string{"why", "packs[0].skills.only"})
	if rc != 0 || !strings.Contains(out, "imports.toml:") {
		t.Fatalf("fragment provenance lost: %d %s %s", rc, out, errw)
	}
	rc, out, errw = runPackCapture(t, project.Root, []string{"remove", "local/corpus"})
	if rc != 0 || errw != "" {
		t.Fatalf("fragment removal: %d %s %s", rc, out, errw)
	}
	after, _ := os.ReadFile(fragment)
	if bytes.Contains(after, []byte("local/corpus")) || !bytes.Contains(after, []byte("# imported")) {
		t.Fatalf("fragment remove: %s", after)
	}
	mainAfter, _ := os.ReadFile(user.Path)
	if !bytes.Equal(mainBefore, mainAfter) {
		t.Fatal("fragment removal changed main file")
	}
}

func TestPackScopedGitFlagsAndInvalidOptionsArePure(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs=[]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	args := []string{"add", "--git", "https://example.invalid/fixture/corpus", "--ref", "main", "--subdir", "one", "--only", "chosen", "--bare", "--dry-run"}
	rc, out, errw := runPackCapture(t, project.Root, args)
	if rc != 0 || errw != "" || !strings.Contains(out, "selection not yet verified; resolved at next launch") || !strings.Contains(out, "bare = true") {
		t.Fatalf("direct flags: %d %s %s", rc, out, errw)
	}
	for _, bad := range [][]string{{"add", "local/corpus", "--git", "https://example.invalid/fixture/corpus"}, {"add", "local/corpus", "--ref", "main"}, {"add", "local/corpus", "--subdir", "../escape"}, {"add", "local/corpus", "--only", "bad*"}, {"add", "--git", "https://example.invalid/fixture/corpus", "--bare", "--namespace", "team"}} {
		rc, _, _ = runPackCapture(t, project.Root, bad)
		if rc == 0 {
			t.Fatalf("invalid flags accepted: %v", bad)
		}
		after, _ := os.ReadFile(user.Path)
		if string(after) != "packs=[]\n" {
			t.Fatalf("invalid/dry-run wrote: %s", after)
		}
	}
}

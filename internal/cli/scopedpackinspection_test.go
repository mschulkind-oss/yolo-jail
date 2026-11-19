package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackExplainScopedShippedReadsEmbeddedFSWithoutMaterialization(t *testing.T) {
	original := packload.EmbeddedFS()
	packload.ReleaseEmbedded()
	t.Cleanup(func() { packload.ReleaseEmbedded(); packload.SetEmbeddedFS(original) })
	packload.SetEmbeddedFS(fstest.MapFS{"corpus/pack.toml": {Data: []byte("name='corpus'\n")}, "corpus/skills/chosen/SKILL.md": {Data: []byte("chosen\n")}, "corpus/LICENSE": {Data: []byte("license\n")}})
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs=[{pack='corpus',skills={only=['chosen']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	entries, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("shipped fixture producer: %+v %v %v names=%v", entries, problems, err, packload.EmbeddedNames())
	}
	before := scopedCLISnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), paths.EmbeddedPacksDir())
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "corpus"})
	if rc != 0 || errw != "" || !strings.Contains(out, "inspected: shipped") || !strings.Contains(out, "license:") || !strings.Contains(out, "LICENSE") || !strings.Contains(out, "selected: [chosen]") {
		t.Fatalf("shipped inspection: %d %s %s", rc, out, errw)
	}
	if packload.EmbeddedLoaded() {
		t.Fatal("pure shipped explain materialized embedded tree")
	}
	scopedCLIWrite(t, user.Path, "packs=['corpus']\n")
	rc, out, errw = runPackCapture(t, project.Root, []string{"explain", "corpus"})
	if rc != 0 || errw != "" || !strings.Contains(out, "stages 3 file(s)") || !strings.Contains(out, "delivers 1 skill(s)") {
		t.Fatalf("shipped whole body: %d %s %s", rc, out, errw)
	}
	if packload.EmbeddedLoaded() {
		t.Fatal("pure whole shipped explain extracted embedded bytes")
	}
	scopedCLIWrite(t, user.Path, "packs=[{pack='corpus',skills={only=['chosen']}}]\n")
	after := scopedCLISnapshot(t, filepath.Dir(user.Path), paths.PacksDir(), paths.EmbeddedPacksDir())
	if len(before) != len(after) {
		t.Fatal("pure embedded inspector wrote")
	}
	for path, value := range before {
		if after[path] != value {
			t.Fatalf("pure embedded inspector changed %s", path)
		}
	}
}

func TestPackExplainScopedMarkerlessCountsAndLicenseAreInspectedFacts(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "packs=[{pack='local/corpus',skills={only=['folder','marked']}}]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	scopedCLIWrite(t, filepath.Join(root, "skills/folder/data.txt"), "folder bytes\n")
	scopedCLIWrite(t, filepath.Join(root, "skills/marked/SKILL.md"), "marked\n")
	scopedCLIWrite(t, filepath.Join(root, "LICENSE"), "top license\n")
	rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/corpus"})
	if rc != 0 || errw != "" {
		t.Fatalf("markerless inspect: %d %s %s", rc, out, errw)
	}
	for _, want := range []string{"kind=directory marker=absent", "kind=directory marker=present", "ordinary-directory governor:", "license:", "conventional-skill-folders=1", "file://", "origin:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing fact %q: %s", want, out)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "skills/folder/SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("marker synthesized: %v", err)
	}
}

package packload_test

import (
	"os"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestEmbeddedProjectSkillDirsReadsMetadataWithoutMaterializing(t *testing.T) {
	old := packload.EmbeddedFS()
	packload.SetEmbeddedFS(fstest.MapFS{
		"alpha/pack.toml": &fstest.MapFile{Data: []byte(`[[skills]]
agent = "pi"
into = ".pi/agent/skills"
project_dirs = [".pi/skills", ".agents/skills"]
`)},
		"beta/pack.toml": &fstest.MapFile{Data: []byte(`[[skills]]
agent = "claude"
into = ".claude/skills"
project_dirs = [".claude/skills", ".agents/skills"]
`)},
	})
	t.Cleanup(func() { packload.SetEmbeddedFS(old) })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	got, err := packload.EmbeddedProjectSkillDirs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".pi/skills", ".agents/skills", ".claude/skills"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EmbeddedProjectSkillDirs() = %v, want %v", got, want)
	}
	if _, err := os.Stat(paths.EmbeddedPacksDir()); !os.IsNotExist(err) {
		t.Fatalf("metadata read materialized an embedded pack cache: %v", err)
	}
}

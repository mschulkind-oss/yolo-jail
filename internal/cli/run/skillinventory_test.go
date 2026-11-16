package run

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	runtimeidentity "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestReadSkillInventorySourcesReadsLiveTreeWithoutStagingOrCache(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	cname := runtimeidentity.FromWorkspace(workspace)
	tree := filepath.Join(paths.PackTreeRoot(cname), "generation-test")
	packDir := filepath.Join(tree, "copilot-fixture")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.toml"), []byte(`[[skills]]
agent = "copilot"
into = ".copilot/skills"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDirForUse(packDir, "copilot-fixture")
	if len(problems) != 0 {
		t.Fatalf("fixture pack manifest: %v", problems)
	}
	if err := packload.WritePackTreeRecordForWorkspace(tree, []*packload.Pack{pack}, workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LivePackTreeRecord(cname)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LivePackTreeRecord(cname), []byte("generation-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := inventoryState(t, paths.GlobalStorage())
	beforeAgents := inventoryState(t, paths.AgentsDir())
	beforeEmbedded := inventoryState(t, paths.EmbeddedPacksDir())

	got, err := ReadSkillInventorySources(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 1 || got.Targets[0].Agent != "copilot" || got.Targets[0].Dest != ".copilot/skills" {
		t.Fatalf("targets = %+v", got.Targets)
	}
	if !containsString(got.WorkspaceDirs, ".agents/skills") || len(got.PackSources) != 0 {
		t.Fatalf("inventory sources = %+v", got)
	}
	if after := inventoryState(t, paths.GlobalStorage()); !reflect.DeepEqual(after, before) {
		t.Fatalf("inventory read wrote host state:\nbefore=%v\nafter=%v", before, after)
	}
	if after := inventoryState(t, paths.AgentsDir()); !reflect.DeepEqual(after, beforeAgents) {
		t.Fatalf("inventory read changed skill staging:\nbefore=%v\nafter=%v", beforeAgents, after)
	}
	if after := inventoryState(t, paths.EmbeddedPacksDir()); !reflect.DeepEqual(after, beforeEmbedded) {
		t.Fatalf("inventory read changed embedded pack cache:\nbefore=%v\nafter=%v", beforeEmbedded, after)
	}
}

func inventoryState(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return out
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[rel] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

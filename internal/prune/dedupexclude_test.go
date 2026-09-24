package prune

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A jail's own embedded-pack cache lives in its `.local` overlay, which dedup walks. Two
// workspaces of one build hold byte-identical trees, and linking them would share one inode
// across jails — so they are never dedup candidates, while an identical file elsewhere in
// the same surface still is.
func TestDedupNeverLinksTheInJailEmbeddedPackTrees(t *testing.T) {
	rel, err := filepath.Rel("/", paths.EmbeddedPacksDirUnder("/"))
	if err != nil {
		t.Fatal(err)
	}
	var workspaces []string
	for _, name := range []string{"a", "b"} {
		ws := filepath.Join(t.TempDir(), name)
		// podman binds <ws>/.yolo/home/local at ~/.local, so the tree's host path is under
		// the "local" subtree, one ".local" shorter.
		local := filepath.Join(ws, ".yolo", "home", "local")
		tree := filepath.Join(local, filepath.Clean(rel[len(".local/"):]), "0123456789abcdef0123456789abcdef", "claude")
		for dir, file := range map[string]string{tree: "pack.json", filepath.Join(local, "bin"): "tool"} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, file), []byte("identical bytes\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		workspaces = append(workspaces, ws)
	}
	got := WalkDedupableWorkspaces(workspaces)
	var tools int
	for _, e := range got {
		if filepath.Base(e.Path) == "pack.json" {
			t.Errorf("dedup candidate inside an embedded-pack tree: %s", e.Path)
		}
		if filepath.Base(e.Path) == "tool" {
			tools++
		}
	}
	if tools != 2 {
		t.Errorf("found %d of the 2 ordinary identical files; the exclusion is too wide: %v", tools, got)
	}
}

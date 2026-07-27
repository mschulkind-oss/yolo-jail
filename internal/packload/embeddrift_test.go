package packload

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/packs"
)

// The embed directive and the packs/ tree must not drift.
//
// packs/embed.go lists each pack dir EXPLICITLY (so editor droppings never get baked
// into a release binary), which means a new pack dir is invisible until someone extends
// that list. This fails the build the moment they disagree, so the sync is test-enforced
// rather than convention-enforced — the same trade bundled_loopholes makes.
//
// It also guards the flake.nix goSrc fileset indirectly: if packs/ were dropped from
// that fileset the image build would lose the tree while `go build` stayed green, and a
// jail would come up with no packs at all. That failure is silent, so it is worth having
// this test name the file to check.
func TestEmbedMatchesTree(t *testing.T) {
	repoRoot := findRepoRoot(t)
	entries, err := os.ReadDir(filepath.Join(repoRoot, "packs"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, e := range entries {
		if e.IsDir() {
			onDisk = append(onDisk, e.Name())
		}
	}

	embedded, err := fs.ReadDir(packs.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var inEmbed []string
	for _, e := range embedded {
		if e.IsDir() {
			inEmbed = append(inEmbed, e.Name())
		}
	}

	sort.Strings(onDisk)
	sort.Strings(inEmbed)
	if len(onDisk) != len(inEmbed) {
		t.Fatalf("packs/ tree and the go:embed list differ:\n tree:  %v\n embed: %v\n"+
			"extend the //go:embed directive in packs/embed.go (and check the goSrc "+
			"fileset in flake.nix)", onDisk, inEmbed)
	}
	for i := range onDisk {
		if onDisk[i] != inEmbed[i] {
			t.Errorf("pack %q on disk vs %q embedded", onDisk[i], inEmbed[i])
		}
	}
}

// findRepoRoot walks up to the dir holding go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

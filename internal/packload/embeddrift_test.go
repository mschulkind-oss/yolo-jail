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

// An embedded pack's manifest `name` must equal its DIRECTORY name.
//
// Not style: the directory name is what becomes the effective name. `packs: ["claude"]`
// resolves a bare entry against the embedded DIR list (config.embeddedPackName), the
// staged tree is <root>/_official/<dir>, and both MaterializeEmbedded and the entrypoint
// name the pack from that dir — so a pack.json `name` that disagrees is a declaration
// that does nothing at all, and the two spellings would appear in different messages
// about the same pack. Every shipped pack agrees today; this is what keeps it that way.
//
// The general rule is in packload.Pack's Name field comment: the manifest never supplies
// the effective name on any production path, for a configured pack or an embedded one.
func TestEmbeddedPackManifestNamesMatchTheirDirs(t *testing.T) {
	packs := Embedded()
	if len(packs) == 0 {
		t.Fatal("no embedded packs materialized — this test would pass vacuously")
	}
	for _, p := range packs {
		dir := filepath.Base(p.Root)
		if p.Decl.Name != "" && p.Decl.Name != dir {
			t.Errorf("packs/%s/pack.json declares name %q — the directory name is what "+
				"yolo uses, so the manifest's is inert; rename the dir or the field",
				dir, p.Decl.Name)
		}
		if p.Name != dir {
			t.Errorf("embedded pack in %s loaded as %q, want the dir name", dir, p.Name)
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

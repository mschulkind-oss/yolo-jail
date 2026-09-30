package run

// forkpacktree_test.go pins the fork rewrite at the HOST-side reader of a running jail's pack tree
// (loadPackTree, which an attach reads), so the host sees the programs that jail was given
// (docs/design/forked-programs-as-packs.md FP-D5).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestARunningJailsPackTreeIsReadWithItsForksApplied(t *testing.T) {
	root := t.TempDir()
	write := func(dir, manifest string) *packload.Pack {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		p, probs := packload.LoadDir(filepath.Join(root, dir), dir)
		if len(probs) > 0 {
			t.Fatal(probs)
		}
		return p
	}
	base := write("pi", `{"contributes":[{"kind":"program","bin":"pi","via":"npm","package":"pi"}]}`)
	fork := write("pi-matt", `{"contributes":[{"kind":"program","bin":"pi","via":"source","fork_of":"pi",
		"source":"git+https://example.invalid/pi-fork?ref=main","build":"make install","produces":[".local/bin/pi"]}]}`)
	if err := packload.WritePackTreeRecord(root, []*packload.Pack{fork, base}); err != nil {
		t.Fatal(err)
	}
	packs, err := loadPackTree(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range packs {
		for _, in := range p.Decl.InstallContributions() {
			if in.Bin == "pi" && (in.Kind != packdecl.InstallKindSource || in.ForkedBy != "pi-matt") {
				t.Errorf("pack %s delivers pi as %q (forked by %q), want pi-matt's build", p.Name, in.Kind, in.ForkedBy)
			}
		}
	}
}

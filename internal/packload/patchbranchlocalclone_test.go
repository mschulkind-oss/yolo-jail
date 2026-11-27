package packload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// patchBranchFork is a patched fork whose patch branch is a URL, with a check record that resolved it through
// copy: the record's Checkout is clone, as a registered local clone would be.
func patchBranchFork(t *testing.T, copyName string) Fork {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := Fork{Pack: "pi-fork", Bin: "pi", Source: "git+https://example.invalid/upstream/pi?ref=main",
		PatchBranch: "git+ssh://git@example.invalid/me/dotfiles?ref=patch/pi"}
	rec := packsrc.CheckRecord{Schema: packsrc.CheckRecordSchema, Owner: f.Key(),
		PatchBranch: &packsrc.PatchBranchResolved{Source: f.PatchBranch, Upstream: packsrc.CheckRepo(f.Source),
			Ref: "refs/heads/patch/pi", Commit: "caf4f9c3caf4f9c3caf4f9c3caf4f9c3caf4f9c3", Base: "b",
			Checkout: "/home/me/.dotfiles", Branch: "patch/pi", Copy: copyName}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	p := (&packsrc.Store{Dir: paths.PacksDir()}).CheckRecordPath(f.Key())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

var patchBranchFailure = &packsrc.PatchFailure{Kind: "conflict", Member: "patch/pi@caf4f9c3", Paths: []string{"a.ts"},
	Target: packsrc.ListEntry{Commit: "abcdef0123456789", Tag: "v1.4.0"}}

// A patch branch read from a LOCAL CLONE is repaired in that clone, never by cloning the remote, whose branch is
// not the one that failed.
func TestThePatchBranchRecipeRepairsTheLocalCloneItWasReadFrom(t *testing.T) {
	f := patchBranchFork(t, "local")
	block := patchBranchFailure.Block(f.Key(), f.RebaseRecipe(), packsrc.PatchBypass{Key: f.Key()})
	for _, want := range []string{"git -C /home/me/.dotfiles checkout patch/pi", "git -C /home/me/.dotfiles merge abcdef0123456789",
		"all in /home/me/.dotfiles"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if lead := strings.Join(f.RebaseRecipe().Repair(patchBranchFailure.Target, packsrc.RecipeLead, ""), "\n"); !strings.Contains(lead, "the next launch reads that clone") {
		t.Errorf("lead does not say the next launch reads the clone:\n%s", lead)
	}
	for _, bad := range []string{"git clone", "git push"} {
		if strings.Contains(block, bad) {
			t.Errorf("block holds %q, which repairs the remote's branch:\n%s", bad, block)
		}
	}
}

// A branch read from the repository itself (a remote's copy) keeps the clone-from-origin recipe.
func TestThePatchBranchRecipeClonesTheRepositoryWhenNoLocalCloneAnswered(t *testing.T) {
	f := patchBranchFork(t, "origin")
	block := patchBranchFailure.Block(f.Key(), f.RebaseRecipe(), packsrc.PatchBypass{Key: f.Key()})
	for _, want := range []string{"git clone ssh://git@example.invalid/me/dotfiles pi-fork-pi-patch-branch", "git push origin patch/pi"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if lead := strings.Join(f.RebaseRecipe().Repair(patchBranchFailure.Target, packsrc.RecipeLead, ""), "\n"); !strings.Contains(lead, "the next check fetches it") {
		t.Errorf("lead changed:\n%s", lead)
	}
	if strings.Contains(block, "git -C") {
		t.Errorf("block repairs a local clone nothing read:\n%s", block)
	}
}

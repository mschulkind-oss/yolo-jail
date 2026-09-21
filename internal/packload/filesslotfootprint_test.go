package packload

// filesslotfootprint_test.go pins what the single-pack views SAY about an addressed `files`
// contribution, and what Collisions may NOT conclude from it.
//
// Both were wrong until 2026-09-21, from one line: the claim's target was the raw `into`, which an
// addressed contribution does not have (into-xor-agents). So the target was BLANK — the reporting
// gap audienceTarget closed for `briefing`/`skills`, reopened for the third kind — and because
// `files` is CombineExclusive, two packs addressing one slot GROUPED on that empty string and were
// reported as a sole-ownership collision: rc=1 from `pack lint`/`pack footprint`, fatal at
// `yolo check`, for the exact arrangement per-pack namespacing exists to make legal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// addressedFilesPack is a content pack addressing `pi`'s files slot with one tree.
func addressedFilesPack(name, from string) *Pack {
	return pk(name, &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, From: from, Agents: []string{"pi"}},
	}})
}

// TestAddressedFilesClaimNamesItsAddressAndItsSubdirectory: the target carries both halves,
// because both are things only this line can tell the author — WHO the tree is for (the address)
// and WHERE it lands relative to the slot (a subdirectory named for this pack).
func TestAddressedFilesClaimNamesItsAddressAndItsSubdirectory(t *testing.T) {
	claims := FootprintOf(addressedFilesPack("matt", "pi-extensions")).Claims
	if len(claims) != 1 {
		t.Fatalf("want exactly one claim, got %+v", claims)
	}
	c := claims[0]
	if c.Target == "" {
		t.Fatal("an addressed `files` claim printed a BLANK target — the single-pack views are " +
			"where an author checks what their manifest does")
	}
	for _, want := range []string{"pi", "matt"} {
		if !strings.Contains(c.Target, want) {
			t.Errorf("target %q must name %q", c.Target, want)
		}
	}
	if !strings.Contains(c.Detail, "addressed to pi") {
		t.Errorf("detail %q must say the contribution is addressed", c.Detail)
	}
}

// TestTwoPacksAddressingOneFilesSlotDoNotCollide: invariant 2 ("namespacing is the contributing
// pack's name; collisions are impossible") stated where it can actually fail. A collision here
// refuses `yolo check` over an arrangement that works.
func TestTwoPacksAddressingOneFilesSlotDoNotCollide(t *testing.T) {
	owner := pk("pi", &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Agent: "pi", Into: ".pi/agent/extensions"},
	}})
	set := []*Pack{owner, addressedFilesPack("matt", "pi-extensions"), addressedFilesPack("acme", "ext")}
	if cols := Collisions(set); len(cols) != 0 {
		t.Errorf("two packs addressing one slot must not collide, got %+v", cols)
	}
}

// AND A PLAIN `files` CLAIM STILL COLLIDES. The fix above is a narrowing of one shape, not a
// weakening of sole ownership: two packs claiming one PATH is the violation that reaches podman as
// "duplicate mount destination" if nothing catches it first.
func TestTwoPacksClaimingOneFilesPathStillCollide(t *testing.T) {
	a := pk("a", &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, From: "tree", Into: ".x/data"},
	}})
	b := pk("b", &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, From: "tree", Into: ".x/data"},
	}})
	cols := Collisions([]*Pack{a, b})
	if len(cols) != 1 || cols[0].Target != ".x/data" {
		t.Errorf("two packs claiming .x/data must still collide, got %+v", cols)
	}
}

// TestAResolvedAddressedFilesClaimReportsTheRealPath: after ResolveDestinations the synthesized
// contribution is an ordinary declaring one, so its claim reports `<slot>/<pack>` — the path that
// is actually written — and does NOT claim the slot root the owner declared. Claiming the root was
// the post-resolution half of the same defect: the owner and every contributor grouped on one
// target.
func TestAResolvedAddressedFilesClaimReportsTheRealPath(t *testing.T) {
	owner := pk("pi", &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Agent: "pi", Into: ".pi/agent/extensions"},
	}})
	content := addressedFilesPack("matt", "pi-extensions")
	// carriesFor reads the pack's TREE: a `from` naming nothing routes nothing, so the fixture
	// has to exist on disk for the inference to fire at all.
	content.Root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(content.Root, "pi-extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content.Root, "pi-extensions", "compact-tools.ts"),
		[]byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, _ := ResolveDestinations([]*Pack{owner, content})
	var targets []string
	for _, p := range resolved {
		for _, c := range FootprintOf(p).Claims {
			if c.Kind == packdecl.KindFiles {
				targets = append(targets, c.Target)
			}
		}
	}
	want := ".pi/agent/extensions/matt"
	found := false
	for _, tg := range targets {
		if tg == want {
			found = true
		}
	}
	if !found {
		t.Errorf("a resolved addressed claim must target %q, got %v", want, targets)
	}
	if cols := Collisions(resolved); len(cols) != 0 {
		t.Errorf("the owner's slot root and a contributor's subdirectory are two targets, "+
			"got collisions %+v", cols)
	}
}

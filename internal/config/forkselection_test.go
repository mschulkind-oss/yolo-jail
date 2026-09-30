package config

// forkselection_test.go pins the fork rewrite's CALL SITE in the one selection function
// (docs/design/forked-programs-as-packs.md FP-D5): every verb that reads a selection sees a fork's
// base carrying the fork's delivery, and a fork the rewrite refuses is the selection's refusal.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// forkEntryPack is a configured pack whose one contribution forks base's bin program.
func forkEntryPack(name, base, bin string) *packload.Pack {
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: bin, Via: packdecl.ViaSource, ForkOf: base,
		Source: "git+https://example.invalid/" + bin + "-fork?ref=main", Build: "make install",
		Produces: []string{".local/bin/" + bin},
	}}}}
}

// installKindOf is the Install.Kind the named pack's program of bin has in packs.
func installKindOf(packs []*packload.Pack, pack, bin string) string {
	for _, p := range packs {
		if p.Name != pack {
			continue
		}
		for _, in := range p.Decl.InstallContributions() {
			if in.Bin == bin {
				return in.Kind
			}
		}
	}
	return ""
}

// A fork of a pack yolo ships: the closure joins the base with its cause line, and the selection
// hands back the base carrying the fork's delivery — while the process's shared embedded pi keeps
// its own.
func TestSelectPacksAppliesAForkToAShippedBase(t *testing.T) {
	fork := forkEntryPack("pi-matt", "pi", "pi")
	resolve := func(e PackEntry) (*packload.Pack, error) {
		if e.Name == "pi-matt" {
			return fork, nil
		}
		return embeddedResolve(e)
	}
	sel, err := SelectPacks([]PackEntry{{Name: "pi-matt", Source: "file:///nowhere/pi-matt"}},
		PackSelectSpec{Resolve: resolve, FailFast: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sel.Causes, "+ pi (forked by pi-matt)") {
		t.Errorf("causes = %v, want the fork's join of pi", sel.Causes)
	}
	if got := installKindOf(sel.Packs(), "pi", "pi"); got != packdecl.InstallKindSource {
		t.Errorf("the selected pi's program is %q, want the fork's %q", got, packdecl.InstallKindSource)
	}
	shipped, err := embeddedPackNamed("pi")
	if err != nil {
		t.Fatal(err)
	}
	if got := installKindOf([]*packload.Pack{shipped}, "pi", "pi"); got == packdecl.InstallKindSource {
		t.Error("the shared embedded pi carries the fork's delivery after one selection")
	}
}

// A fork of a CONFIGURED base: the rewrite lands in Configured, keeps the entries' order, and the
// conventional local pack stays last if it is the base.
func TestSelectPacksAppliesAForkToAConfiguredBase(t *testing.T) {
	base := &packload.Pack{Name: "mybase", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "tool", Via: "npm", Package: "tool",
	}}}}
	fork := forkEntryPack("tool-fork", "mybase", "tool")
	byName := map[string]*packload.Pack{"mybase": base, "tool-fork": fork}
	resolve := func(e PackEntry) (*packload.Pack, error) { return byName[e.Name], nil }
	sel, err := SelectPacks([]PackEntry{
		{Name: "tool-fork", Source: "file:///x/tool-fork"},
		{Name: "mybase", Source: "file:///x/mybase", Implicit: true},
	}, PackSelectSpec{Resolve: resolve, FailFast: true})
	if err != nil {
		t.Fatal(err)
	}
	packs := sel.Packs()
	if got := selectionNames(packs); !slices.Equal(got, []string{"tool-fork", "mybase"}) {
		t.Fatalf("Packs() = %v, want the implicit base still last", got)
	}
	if packs[1] == base {
		t.Fatal("the selection handed back the configured base itself, not the rewritten copy")
	}
	if got := installKindOf(packs, "mybase", "tool"); got != packdecl.InstallKindSource {
		t.Errorf("the configured base's program is %q, want the fork's", got)
	}
}

// A fork the rewrite refuses is the selection's refusal: ClosureErr, and FailFast's error.
func TestSelectPacksRefusesAForkWithNoBase(t *testing.T) {
	fork := forkEntryPack("orphan-fork", "nosuch", "tool")
	resolve := func(PackEntry) (*packload.Pack, error) { return fork, nil }
	entries := []PackEntry{{Name: "orphan-fork", Source: "file:///x/orphan-fork"}}
	sel, err := SelectPacks(entries, PackSelectSpec{Resolve: resolve})
	if err != nil || sel.ClosureErr == nil || !strings.Contains(sel.ClosureErr.Error(), "nosuch") {
		t.Fatalf("err = %v, ClosureErr = %v; want a refusal naming the missing base", err, sel.ClosureErr)
	}
	if sel.Complete() {
		t.Error("a refused fork left the selection complete")
	}
	if _, err := SelectPacks(entries, PackSelectSpec{Resolve: resolve, FailFast: true}); err == nil {
		t.Error("FailFast returned no error for a refused fork")
	}
}

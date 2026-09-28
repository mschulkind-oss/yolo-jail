package config

// packprecedence_test.go pins THE ONE PACK ORDER (docs/plans/notch-convergence.md OQ-NC4, ruled
// A): the entries in the order the user config lists them, then the packs the selection closure
// joined, then the conventional local pack last. "Later wins" means later in this order at every
// notch. The notches' own call sites are pinned beside them (internal/cli/run, internal/cli); this
// is the function they share.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// precedenceFixture is a local pack "zeta" that needs hello-daemon, so the closure adds a pack,
// and the conventional local pack's entry.
func precedenceFixture(t *testing.T) (zeta, local PackEntry) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "zeta")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"zeta","needs":[{"pack":"hello-daemon"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	localRoot := filepath.Join(t.TempDir(), "local")
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, "pack.json"), []byte(`{"name":"local"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return PackEntry{Source: "file://" + root, Name: "zeta"},
		PackEntry{Source: "file://" + localRoot, Name: LocalPackName, Implicit: true}
}

func resolveForOrder(t *testing.T) func(PackEntry) (*packload.Pack, error) {
	return func(e PackEntry) (*packload.Pack, error) {
		res, err := ResolvePack(e, ResolvePackSpec{})
		if err != nil {
			return nil, err
		}
		return res.Pack, nil
	}
}

// CONFIG ORDER, THEN THE CLOSURE, THEN THE LOCAL PACK. The local pack is last even though the
// closure's additions are computed after it resolves, and a configured embedded pack keeps its
// place in the list rather than moving ahead of a configured local one (the launcher's old
// embedded-first order).
func TestPackPrecedenceIsConfigOrderThenClosureThenLocal(t *testing.T) {
	zeta, local := precedenceFixture(t)
	want := []string{"zeta", "guardrails", "hello-daemon", "local"}
	for _, entries := range [][]PackEntry{
		{zeta, EmbeddedPackEntry("guardrails"), local},
		// The order is the function's, not the caller's: an implicit entry handed over early
		// still composes last.
		{local, zeta, EmbeddedPackEntry("guardrails")},
	} {
		sel, err := SelectPacks(entries, PackSelectSpec{Resolve: resolveForOrder(t)})
		if err != nil {
			t.Fatal(err)
		}
		if got := selectionNames(sel.Packs()); !slices.Equal(got, want) {
			t.Errorf("Packs() over %v = %v, want %v", selectionEntryNames(entries), got, want)
		}
		// Configured keeps its meaning: the entries' packs in the order they arrived.
		if got := selectionNames(sel.Configured); len(got) != 3 {
			t.Errorf("Configured = %v, want the three entries' packs", got)
		}
	}
}

// An EXPLICIT config line that takes the local pack's name is a config line: it keeps its place.
func TestAnExplicitPackNamedLocalKeepsItsConfigPlace(t *testing.T) {
	zeta, local := precedenceFixture(t)
	local.Implicit = false
	sel, err := SelectPacks([]PackEntry{local, zeta}, PackSelectSpec{Resolve: resolveForOrder(t)})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"local", "zeta", "hello-daemon"}
	if got := selectionNames(sel.Packs()); !slices.Equal(got, want) {
		t.Errorf("Packs() = %v, want %v", got, want)
	}
}

func selectionEntryNames(entries []PackEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

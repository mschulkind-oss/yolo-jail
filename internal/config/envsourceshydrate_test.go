package config

// envsourceshydrate_test.go pins HydrateEnvSources and SplitHydratedEnvSources: the one map a jail
// launch keeps its env_sources hydration in, removals carried as nil values, so a recomposition
// from that map keeps what the first pass removed (run's composePackChannel).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestHydrateEnvSourcesCarriesRemovalsAsNil(t *testing.T) {
	dir := t.TempDir()
	dotenv := filepath.Join(dir, "later.env")
	if err := os.WriteFile(dotenv, []byte("BACK=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := jsonx.NewOrderedMap()
	first.Set("KEEP", "yes")
	first.Set("GONE", nil)
	first.Set("BACK", nil)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("env_sources", []any{first, dotenv})

	m := HydrateEnvSources(dir, cfg, nil)
	if v, ok := m.Get("GONE"); !ok || v != nil {
		t.Errorf("a removal must be carried as a nil value: %v %v", v, ok)
	}
	if v, _ := m.Get("BACK"); v != "from-file" {
		t.Errorf("a dotenv file after the null cancels it, as ResolveEnvSourcesFull rules: %v", v)
	}
	assignments, removals := SplitHydratedEnvSources(m)
	if len(removals) != 1 || removals[0] != "GONE" {
		t.Errorf("removals = %v, want [GONE]", removals)
	}
	if got := assignments.Keys(); len(got) != 2 || got[0] != "KEEP" || got[1] != "BACK" {
		t.Errorf("assignments = %v, want [KEEP BACK] in hydration order", got)
	}
	// The same two answers ResolveEnvSourcesFull gives, from one pass.
	merged, full := ResolveEnvSourcesFull(dir, cfg, nil)
	if merged.Len() != assignments.Len() || len(full) != len(removals) {
		t.Errorf("the split must give ResolveEnvSourcesFull's answers: %v %v vs %v %v",
			merged.Keys(), full, assignments.Keys(), removals)
	}
	if a, r := SplitHydratedEnvSources(nil); a.Len() != 0 || r != nil {
		t.Errorf("a nil map splits to nothing: %v %v", a.Keys(), r)
	}
}

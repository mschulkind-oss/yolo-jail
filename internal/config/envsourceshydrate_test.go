package config

// envsourceshydrate_test.go pins the removals ResolveEnv carries: the hydration a jail launch keeps
// (run's packChannel.userEnv) holds them beside the values, so a recomposition from it keeps what
// the first pass removed (run's composePackChannel), and a dotenv file listed after an inline null
// cancels that null.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestResolveEnvCarriesRemovalsAFileLaterCancels(t *testing.T) {
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

	h := ResolveEnv(dir, cfg, nil)
	if removals := h.Removals(); len(removals) != 1 || removals[0] != "GONE" {
		t.Errorf("removals = %v, want [GONE]", removals)
	}
	if v, _ := h.Values().Get("BACK"); v != "from-file" {
		t.Errorf("a dotenv file after the null cancels it: %v", v)
	}
	if got := h.Values().Keys(); len(got) != 2 || got[0] != "KEEP" || got[1] != "BACK" {
		t.Errorf("values = %v, want [KEEP BACK] in hydration order", got)
	}
	if _, ok := h.Values().Get("GONE"); ok {
		t.Error("a removed name holds no value")
	}
	var none *HydratedEnv
	if none.Values().Len() != 0 || none.Removals() != nil {
		t.Errorf("a nil hydration reads as empty: %v %v", none.Values().Keys(), none.Removals())
	}
}

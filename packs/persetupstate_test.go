package packs_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

func shippedPerSetupState(t *testing.T, pack string) []packdecl.PerSetupState {
	t.Helper()
	data, err := packs.FS.ReadFile(pack + "/pack.toml")
	if err != nil {
		t.Fatal(err)
	}
	m, problems := packdecl.DecodeTOML(data)
	if len(problems) != 0 {
		t.Fatalf("packs/%s/pack.toml: %v", pack, problems)
	}
	return m.PerSetupState()
}

// TestShippedPacksCarveOutTheirInstalls pins what each shipped pack keeps in a setup's own home
// instead of sharing it (docs/design/unified-workspace-home.md §6.3, UH-D7): installs a setup
// builds for its own platform, and databases whose locking across two kernels is unmeasured. The
// paths are relative to the state directory, so omp's `agent.db` is `agent/agent.db` under `.oh-omp`.
func TestShippedPacksCarveOutTheirInstalls(t *testing.T) {
	want := map[string][]packdecl.PerSetupState{
		"pi": {{Dir: ".pi", Paths: []string{"agent/npm", "agent/git", "agent/bin", "agent/tools"}}},
		"copilot": {{Dir: ".copilot", Paths: []string{
			"session-store.db", "session-store.db-wal", "session-store.db-shm"}}},
		"omp": {{Dir: ".oh-omp", Paths: []string{
			"agent/agent.db", "agent/history.db", "agent/models.db", "agent/tool-results.db", "agent/concept-graph.db"}}},
	}
	for pack, w := range want {
		got := shippedPerSetupState(t, pack)
		for _, g := range got {
			sort.Strings(g.Paths)
		}
		for _, x := range w {
			sort.Strings(x.Paths)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("packs/%s per_setup_state = %+v, want %+v", pack, got, w)
		}
	}
	// Their payloads are already generated or installed-program surfaces (UHP-D9): nothing to carve.
	for _, pack := range []string{"codex-auth", "claude", "agy"} {
		if got := shippedPerSetupState(t, pack); len(got) != 0 {
			t.Errorf("packs/%s declares a carve-out %+v; this slice declares none", pack, got)
		}
	}
}

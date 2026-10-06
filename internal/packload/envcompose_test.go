package packload

// envcompose_test.go pins THE ONE ORDERED ENV COMPOSITION (envcompose.go): which of yolo's own
// sources wins one name for one process, OQ-NC12's option A, and where an env_sources null
// ranks. Every vehicle serializes this answer; the vehicles' own pins are the env-winner parity
// tests in internal/cli and internal/cli/run, which compare each one against EnvFor.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// winnerPack is one pack that sets every name the table below asks about from each of the three
// sources. It installs fxa, ships the provider fxp claiming FXP_KEY and a profile over it, sets
// K4 and K5 statically, gates K1 on the profile, and its derive sets K1, FXP_KEY, K2 and K6 and
// tombstones K7.
func winnerPack(t *testing.T) *Pack {
	t.Helper()
	root := t.TempDir()
	derive := `yolo.env("fxa", function(ctx)
  return {K1 = "shape", FXP_KEY = "shape-key", K2 = "shape", K6 = "shape", K7 = ctx.tombstone}
end)
`
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(derive), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Pack{Name: "fx", Root: root, Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"fxa","via":"npm","package":"@acme/fxa"},
	  {"kind":"provider","name":"fxp","api_key_env_name":"FXP_KEY"},
	  {"kind":"profile","name":"fxp","provider":"fxp"},
	  {"kind":"env","vars":{"K4":"fold-static","K5":"fold-static","K7":"fold-static","K8":"fold-static"}},
	  {"kind":"env","profile":"fxp","vars":{"K1":"gated"}}]}`)}
}

// winnerScope is the gate's answer for fxa on fxp over winnerPack, with env_sources assigning
// FXP_KEY (claimed), K2 and K4 (unclaimed) and K7, and removing K5, K6 and K9.
func winnerScope(t *testing.T) *CredentialScope {
	t.Helper()
	providers := jsonx.NewOrderedMap()
	fxp := jsonx.NewOrderedMap()
	fxp.Set("api_key_env_name", "FXP_KEY")
	providers.Set("fxp", fxp)
	sources := jsonx.NewOrderedMap()
	sources.Set("FXP_KEY", "es-claimed")
	sources.Set("K2", "es-unclaimed")
	sources.Set("K4", "es-unclaimed")
	sources.Set("K7", "es-unclaimed")
	scope, err := ScopeCredentials(ScopeInput{
		Packs: []*Pack{winnerPack(t)}, Providers: providers,
		Profiles:          map[string]string{"fxa": "fxp"},
		Resolved:          map[string]ResolvedProfile{"fxp": {Provider: "fxp"}},
		EnvSources:        sources,
		EnvSourceRemovals: []string{"K5", "K6", "K9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

// One winner per name, by source: the shape var over env_sources over the pack env fold, a null
// ranking with env_sources (it takes out the fold's value and never a shape var), a shape
// tombstone beating both below. Each entry names its source and its pack.
func TestTheCompositionRanksShapeOverEnvSourcesOverTheFold(t *testing.T) {
	comp := winnerScope(t).EnvFor("fxa")
	for _, tc := range []struct {
		key, value, origin, pack string
		unset                    bool
		why                      string
	}{
		{"K1", "shape", FromProfileEnv, "fx", false, "a shape var beats the gated fold"},
		{"FXP_KEY", "shape-key", FromProfileEnv, "fx", false, "a shape var beats a claimed env_sources value"},
		{"K2", "shape", FromProfileEnv, "fx", false, "a shape var beats an unclaimed env_sources value"},
		{"K4", "es-unclaimed", FromEnvSources, "", false, "env_sources beats the static fold"},
		{"K5", "", FromEnvSources, "", true, "a null removes the fold's value"},
		{"K6", "shape", FromProfileEnv, "fx", false, "a null never removes a shape var"},
		{"K7", "", FromProfileEnv, "fx", true, "a shape tombstone beats env_sources and the fold"},
		{"K8", "fold-static", FromPackEnv, "fx", false, "the fold stands where nothing else sets the name"},
		{"K9", "", FromEnvSources, "", true, "a null with nothing below is still a removal"},
	} {
		e, ok := comp.Lookup(tc.key)
		if !ok {
			t.Errorf("%s: no entry (%s)", tc.key, tc.why)
			continue
		}
		if e.Value != tc.value || e.Unset != tc.unset || e.Origin != tc.origin || e.Pack != tc.pack {
			t.Errorf("%s = %+v, want value %q unset %v origin %q pack %q (%s)", tc.key, e,
				tc.value, tc.unset, tc.origin, tc.pack, tc.why)
		}
	}
	seen := map[string]bool{}
	for _, e := range comp.Entries() {
		if seen[e.Key] {
			t.Errorf("%s has two entries: a vehicle would layer two values and let its grammar pick", e.Key)
		}
		seen[e.Key] = true
	}
}

// The shared composition is what every process starts with: the fold's shared half and the
// unclaimed env_sources and every removal, no gate and no shape. A program no profile selects
// receives exactly it.
func TestTheSharedCompositionCarriesNoAgentsValues(t *testing.T) {
	s := winnerScope(t)
	shared := s.SharedEnv()
	for key, want := range map[string]string{"K2": "es-unclaimed", "K4": "es-unclaimed", "K7": "es-unclaimed", "K8": "fold-static"} {
		if got, _ := shared.Value(key); got != want {
			t.Errorf("shared %s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"K1", "FXP_KEY", "K6"} {
		if v, ok := shared.Value(key); ok {
			t.Errorf("shared %s = %q: a gated value, a claimed credential and a shape var are one agent's", key, v)
		}
	}
	if e, ok := shared.Lookup("K5"); !ok || !e.Unset {
		t.Errorf("a removal reaches every process, the shared one included: %+v", e)
	}
	other := s.EnvFor("bystander")
	if len(other.Entries()) != len(shared.Entries()) {
		t.Fatalf("a program no profile selects must receive the shared composition: %v vs %v",
			other.Entries(), shared.Entries())
	}
	for i, e := range shared.Entries() {
		if other.Entries()[i] != e {
			t.Errorf("entry %d: %+v vs shared %+v", i, other.Entries()[i], e)
		}
	}
}

// The order is the order of each name's winning write, so the fold's names come first, then
// env_sources', then the shape's, a name moving to the layer that won it.
func TestTheCompositionKeepsTheOrderOfEachWinningWrite(t *testing.T) {
	var c EnvComposition
	c.put(EnvEntry{Key: "A", Value: "1", Origin: FromPackEnv})
	c.put(EnvEntry{Key: "B", Value: "1", Origin: FromPackEnv})
	c.put(EnvEntry{Key: "C", Value: "1", Origin: FromPackEnv})
	c.put(EnvEntry{Key: "A", Value: "2", Origin: FromEnvSources})
	c.put(EnvEntry{Key: "", Value: "x"})
	var got []string
	for _, e := range c.Entries() {
		got = append(got, e.Key+"="+e.Value)
	}
	if want := []string{"B=1", "C=1", "A=2"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("entries = %v, want %v (an empty key is no variable)", got, want)
	}
	if v, ok := c.Value("C"); !ok || v != "1" {
		t.Errorf("Lookup after a move: C = %q %v", v, ok)
	}
}

// DeliveredTo is the agent's composition's answer, so the region pre-flight and every other
// per-agent reader rank the sources as the vehicles do: the shape var over a claimed value, and
// a null taking the fold's value out. Delivered is the launch-wide reading of the same
// compositions, naming the source that wins.
func TestDeliveredToReadsTheComposition(t *testing.T) {
	s := winnerScope(t)
	for key, want := range map[string]string{"K1": "shape", "FXP_KEY": "shape-key", "K4": "es-unclaimed", "K6": "shape"} {
		if got, _ := s.DeliveredTo("fxa", key); got != want {
			t.Errorf("DeliveredTo(fxa, %s) = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"K5", "K7"} {
		if v, ok := s.DeliveredTo("fxa", key); ok {
			t.Errorf("DeliveredTo(fxa, %s) = %q: a removal delivers nothing", key, v)
		}
	}
	for key, want := range map[string]string{"FXP_KEY": FromProfileEnv, "K4": FromEnvSources, "K8": FromPackEnv} {
		if e, ok := s.Delivered(key); !ok || e.Origin != want {
			t.Errorf("Delivered(%s) = %+v %v, want origin %q", key, e, ok, want)
		}
	}
	if e, ok := s.Delivered("K5"); ok {
		t.Errorf("Delivered(K5) = %+v: a null removed the only value", e)
	}
	var nilScope *CredentialScope
	if _, ok := nilScope.Delivered("K4"); ok || len(nilScope.EnvFor("fxa").Entries()) != 0 {
		t.Error("a nil scope composes nothing")
	}
}

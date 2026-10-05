package cli

// envwinnerparity_test.go pins ONE WINNER PER NAME AT THE HOST EXEC: what `yolo host -- fxa`
// hands fxa is packload's one ordered composition (CredentialScope.EnvFor, envcompose.go)
// applied over the invoking shell, name by name, and the composition is OQ-NC12's option A —
// the shape var over env_sources over the pack env fold, an env_sources null ranking with
// env_sources. The jail vehicles' pin is internal/cli/run's envwinnerparity_test.go, over the
// same fixture.
//
// MEASURED before the composition (2026-10-04, at 6dede33a4): the host already ranked the three
// sources this way, and K6 — a shape var a null names — was removed, so a null split a derive's
// address from its credential (claude on zai kept zai's token and lost zai's base URL).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// hostWinnerKeys are the names the fixture sets from more than one source; K3 is the shell's
// against the shape var's.
var hostWinnerKeys = []string{"K1", "FXP_KEY", "K2", "K3", "K4", "K5", "K6", "K7", "K8"}

// wantHostWinners is option A for fxa on its profile, over a shell exporting K3, K5 and K6:
// "" for a name the exec'd process does not hold.
var wantHostWinners = map[string]string{
	"K1":      "shape",        // the shape var beats the gated fold
	"FXP_KEY": "shape-key",    // the shape var beats a claimed env_sources value
	"K2":      "shape",        // the shape var beats an unclaimed env_sources value
	"K3":      "shape",        // yolo's value beats the shell's until OQ-NC13 says otherwise
	"K4":      "es-unclaimed", // env_sources beats the static fold
	"K5":      "",             // a null removes the fold's value and the shell's
	"K6":      "shape",        // a null never removes a shape var
	"K7":      "",             // a shape tombstone beats env_sources and the fold
	"K8":      "fold-static",  // the fold where nothing else sets the name
}

// writeWinnerFixture writes the fixture pack under home, selects it with fxa on fxp, and exports
// K3, K5 and K6 in the invoking shell.
func writeWinnerFixture(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, "packs", "fx")
	writeFile(t, filepath.Join(root, "pack.json"), `{"name":"fx","contributes":[`+
		`{"kind":"program","bin":"fxa","via":"npm","package":"@acme/fxa"},`+
		`{"kind":"provider","name":"fxp","api_key_env_name":"FXP_KEY"},`+
		`{"kind":"profile","name":"fxp","provider":"fxp"},`+
		`{"kind":"env","vars":{"K4":"fold-static","K5":"fold-static","K7":"fold-static","K8":"fold-static"}},`+
		`{"kind":"env","profile":"fxp","vars":{"K1":"gated"}}]}`)
	writeFile(t, filepath.Join(root, "derive.lua"), `yolo.env("fxa", function(ctx)
  return {K1 = "shape", FXP_KEY = "shape-key", K2 = "shape", K3 = "shape", K6 = "shape", K7 = ctx.tombstone}
end)
`)
	userCfg(t, home, `{
	  "packs": [{"source": "file://`+root+`", "name": "fx"}],
	  "profile": {"fxa": "fxp"},
	  "env_sources": [{"FXP_KEY": "es-claimed", "K2": "es-unclaimed", "K4": "es-unclaimed",
	    "K5": null, "K6": null, "K7": "es-unclaimed"}]
	}`)
	for _, k := range hostWinnerKeys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for _, k := range []string{"K3", "K5", "K6"} {
		t.Setenv(k, "user-shell")
	}
}

func TestTheHostExecDeliversTheCompositionsWinner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeWinnerFixture(t, home)

	c := composeHostLaunch("fxa", "", nil, func(string) {})
	if c.err != nil {
		t.Fatal(c.err)
	}
	got := map[string]string{}
	for _, kv := range c.environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			got[k] = v
		}
	}
	comp := c.scope.EnvFor("fxa")
	for _, k := range hostWinnerKeys {
		// packload's answer over the shell: the composition's entry when it has one, a removal
		// leaving the name out, else what the shell holds.
		composed := os.Getenv(k)
		if e, ok := comp.Lookup(k); ok {
			composed = e.Value
		}
		if got[k] != composed {
			t.Errorf("%s: the exec hands %q, but packload's composition over the shell says %q — "+
				"the host layered the sources in an order of its own", k, got[k], composed)
		}
		if composed != wantHostWinners[k] {
			t.Errorf("composition: %s = %q, want %q (OQ-NC12 option A)", k, composed, wantHostWinners[k])
		}
	}
	// One var per name, so agentenv.Apply never orders two assignments for one name.
	seen := map[string]bool{}
	for _, v := range c.vars {
		if seen[v.Key] {
			t.Errorf("%s is composed twice", v.Key)
		}
		seen[v.Key] = true
	}
}

// OQ-NC13's one input: with hostHonorsIncomingValue's switch on, a value the shell already holds
// passes through and yolo's of the same name is dropped, while a removal, the user's own, still
// applies. Off, as it ships, yolo's value is composed whatever the shell holds.
func TestHostComposedVarsHonorsTheShellOnlyWhenAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeWinnerFixture(t, home)
	c := composeHostLaunch("fxa", "", nil, func(string) {})
	if c.err != nil {
		t.Fatal(c.err)
	}
	comp := c.scope.EnvFor("fxa")
	shell := func(name string) (string, bool) {
		if name == "K3" || name == "K5" || name == "K7" {
			return "user-shell", true
		}
		return "", false
	}
	index := func(vars []agentenv.Var) map[string]agentenv.Var {
		out := map[string]agentenv.Var{}
		for _, v := range vars {
			out[v.Key] = v
		}
		return out
	}
	off, _, _ := hostComposedVars(comp, false, shell)
	if v := index(off)["K3"]; v.Value != "shape" {
		t.Errorf("off: K3 = %+v, want yolo's shape value over the shell's", v)
	}
	on, origins, packs := hostComposedVars(comp, true, shell)
	if len(on) != len(origins) || len(on) != len(packs) {
		t.Fatalf("vars, origins and packs must align: %d %d %d", len(on), len(origins), len(packs))
	}
	if v, ok := index(on)["K3"]; ok {
		t.Errorf("on: K3 = %+v, want it left to the shell", v)
	}
	if v := index(on)["K5"]; !v.Unset {
		t.Errorf("on: K5 = %+v, want the user's own removal kept", v)
	}
	if v := index(on)["K1"]; v.Value != "shape" {
		t.Errorf("on: K1 = %+v, a name the shell does not hold keeps yolo's value", v)
	}
	// A SHAPE TOMBSTONE IS THE DERIVE'S, not the user's: switched on, it leaves a value the shell
	// holds alone, as the jail's per-agent file writes one only over a value yolo set (CN-D21).
	// Off, it removes the shell's value like every composed entry replaces one.
	if v := index(off)["K7"]; !v.Unset {
		t.Errorf("off: K7 = %+v, want the derive's tombstone over the shell's value", v)
	}
	if v, ok := index(on)["K7"]; ok {
		t.Errorf("on: K7 = %+v, want the shell's value left alone: a tombstone is yolo's, not the user's", v)
	}
}

// THE WIRE TABLES ARE THE LAUNCH'S. They are written after the composition, so neither an
// env_sources value nor an env_sources null of a table's name replaces or removes the table this
// launch composed, as no jail vehicle lets one (internal/cli/run's
// TestTheJailVehiclesKeepTheWireTablesOverEnvSources), and a launch started inside another
// agent's launch still replaces the table it inherited (FT-D2). Before the one composition the
// host applied the removals after the tables, so a null of YOLO_USE_PROFILES handed the agent
// the table its parent launch exported, or none.
func TestTheHostExecKeepsTheWireTablesOverEnvSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeWinnerFixture(t, home)
	userCfg(t, home, `{
	  "packs": [{"source": "file://`+filepath.Join(home, "packs", "fx")+`", "name": "fx"}],
	  "profile": {"fxa": "fxp"},
	  "env_sources": [{"YOLO_PROVIDERS": "es-value", "YOLO_USE_PROFILES": null, "YOLO_PROFILES": null}]
	}`)
	// What a parent launch exported.
	t.Setenv("YOLO_USE_PROFILES", `{"claude": "zai"}`)
	t.Setenv("YOLO_PROFILES", `{"zai": {}}`)

	c := composeHostLaunch("fxa", "", nil, func(string) {})
	if c.err != nil {
		t.Fatal(c.err)
	}
	got := map[string]string{}
	for _, kv := range c.environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			got[k] = v
		}
	}
	wire := c.wireTables()
	for _, k := range entrypoint.WireTables() {
		if got[k] != wire[k] {
			t.Errorf("%s = %q, want the table this launch composed, %q", k, got[k], wire[k])
		}
	}
	// One var per name, so `yolo host env` prints no removal its own next line undoes.
	seen := map[string]bool{}
	for _, v := range c.vars {
		if seen[v.Key] {
			t.Errorf("%s is composed twice", v.Key)
		}
		seen[v.Key] = true
	}
}

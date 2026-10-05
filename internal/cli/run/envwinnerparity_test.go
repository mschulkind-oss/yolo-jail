package run

// envwinnerparity_test.go pins ONE WINNER PER NAME AT EVERY JAIL VEHICLE: the container's shared
// file and per-agent file (podman and Apple Container), the macos-user session env, that session
// with the agent's own file sourced after it, a macos-user login shell starting the agent, and
// the per-agent and launch-wide readers (DeliveredTo, deliverySource). Each is compared, name by
// name, against packload's one ordered composition (CredentialScope.EnvFor and SharedEnv,
// envcompose.go), and against the order itself: OQ-NC12's option A, the shape var over
// env_sources over the pack env fold, with an env_sources null ranking with env_sources.
//
// MEASURED before the composition (2026-10-04, at 6dede33a4), over this fixture: the per-agent
// file answered "gated" for K1 and "es-claimed" for FXP_KEY, a jail shell "fold-static" for K4,
// the macos-user session "es-unclaimed" for K2, and every jail vehicle kept K5, which a null
// removes. The host's own pin is internal/cli's envwinnerparity_test.go, over the same fixture.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// winnerKeys are the names the fixture sets from more than one source.
var winnerKeys = []string{"K1", "FXP_KEY", "K2", "K4", "K5", "K6", "K7", "K8"}

// unsetMark is what a sourced shell prints for a name it does not hold.
const unsetMark = "<unset>"

// wantAgentWinners is option A for fxa on its profile: each name's winner, unsetMark for a removal.
var wantAgentWinners = map[string]string{
	"K1":      "shape",        // the shape var beats the gated fold
	"FXP_KEY": "shape-key",    // the shape var beats a claimed env_sources value
	"K2":      "shape",        // the shape var beats an unclaimed env_sources value
	"K4":      "es-unclaimed", // env_sources beats the static fold
	"K5":      unsetMark,      // a null removes the fold's value
	"K6":      "shape",        // a null never removes a shape var
	"K7":      unsetMark,      // a shape tombstone beats env_sources and the fold
	"K8":      "fold-static",  // the fold where nothing else sets the name
}

// wantSharedWinners is option A for every process: no gate, no claimed value, no shape var.
var wantSharedWinners = map[string]string{
	"K1": unsetMark, "FXP_KEY": unsetMark, "K2": "es-unclaimed", "K4": "es-unclaimed",
	"K5": unsetMark, "K6": unsetMark, "K7": "es-unclaimed", "K8": "fold-static",
}

// winnerFixturePack installs fxa and sets every winnerKeys name from more than one source: the
// provider fxp claims FXP_KEY; K4, K5, K7 and K8 are static; K1 is gated on the fxp profile; the
// derive sets K1, FXP_KEY, K2 and K6 and tombstones K7.
func winnerFixturePack(t *testing.T) *packload.Pack {
	t.Helper()
	p := inlinePack(t, "fx", `{"name":"fx","contributes":[`+
		`{"kind":"program","bin":"fxa","via":"npm","package":"@acme/fxa"},`+
		`{"kind":"provider","name":"fxp","api_key_env_name":"FXP_KEY"},`+
		`{"kind":"profile","name":"fxp","provider":"fxp"},`+
		`{"kind":"env","vars":{"K4":"fold-static","K5":"fold-static","K7":"fold-static","K8":"fold-static"}},`+
		`{"kind":"env","profile":"fxp","vars":{"K1":"gated"}}]}`)
	derive := `yolo.env("fxa", function(ctx)
  return {K1 = "shape", FXP_KEY = "shape-key", K2 = "shape", K6 = "shape", K7 = ctx.tombstone}
end)
`
	if err := os.WriteFile(filepath.Join(p.Root, "derive.lua"), []byte(derive), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// winnerFixtureConfig is a launch config whose inline env_sources assigns FXP_KEY (claimed), K2,
// K4 and K7, and removes K5 and K6.
func winnerFixtureConfig() *jsonx.OrderedMap {
	cfg := bareConfig()
	es := jsonx.NewOrderedMap()
	es.Set("FXP_KEY", "es-claimed")
	es.Set("K2", "es-unclaimed")
	es.Set("K4", "es-unclaimed")
	es.Set("K5", nil)
	es.Set("K6", nil)
	es.Set("K7", "es-unclaimed")
	cfg.Set("env_sources", []any{es})
	return cfg
}

// winnerChannel composes the fixture the way Run does: composePackChannel hydrating the config's
// env_sources itself, removals included.
func winnerChannel(t *testing.T) (*Options, *jsonx.OrderedMap, []*packload.Pack, *packChannel) {
	t.Helper()
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"fxa": "fxp"}
	cfg := winnerFixtureConfig()
	packs := []*packload.Pack{winnerFixturePack(t)}
	return o, cfg, packs, channelFor(t, o, cfg, packs, nil)
}

// sourcedWinners sources files in bash over the environment pre and reports winnerKeys.
func sourcedWinners(t *testing.T, pre map[string]string, files ...string) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	var script strings.Builder
	for _, f := range files {
		script.WriteString(". '" + f + "'\n")
	}
	script.WriteString(`for k in ` + strings.Join(winnerKeys, " ") + `; do printf '%s=%s\n' "$k" "${!k-` + unsetMark + `}"; done`)
	cmd := exec.Command("bash", "-c", script.String())
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	for k, v := range pre {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing %v: %v\n%s", files, err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

// envMap is a launch env as the strings it carries, for sourcing over.
func envMap(env *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// winnersOf is comp's answer for winnerKeys in sourcedWinners' terms.
func winnersOf(comp packload.EnvComposition) map[string]string {
	out := map[string]string{}
	for _, k := range winnerKeys {
		e, ok := comp.Lookup(k)
		switch {
		case !ok || e.Unset:
			out[k] = unsetMark
		default:
			out[k] = e.Value
		}
	}
	return out
}

// requireWinners fails for each name whose value in got is not the composition's, or not
// option A's.
func requireWinners(t *testing.T, vehicle string, got, composed, want map[string]string) {
	t.Helper()
	for _, k := range winnerKeys {
		if got[k] != composed[k] {
			t.Errorf("%s: %s = %q, but packload's composition says %q — the vehicle layered "+
				"the sources in an order of its own", vehicle, k, got[k], composed[k])
		}
		if composed[k] != want[k] {
			t.Errorf("composition: %s = %q, want %q (OQ-NC12 option A)", k, composed[k], want[k])
		}
	}
}

// THE CONTAINER VEHICLE, both backends: the shared file sourced (the boot, .bashrc), then the
// agent's own file (its launcher), carries the agent's composition, and the shared file alone,
// a bare jail shell, carries the shared one. A value the user exports between the two is kept,
// as OQ-CN8 rules for the per-agent file.
func TestTheContainerFilesDeliverTheCompositionsWinner(t *testing.T) {
	_, _, _, channel := winnerChannel(t)
	agent := winnersOf(channel.scope.EnvFor("fxa"))
	shared := winnersOf(channel.scope.SharedEnv())
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			ws := t.TempDir()
			deliverChannel(ws, rt, channel)
			sharedFile := filepath.Join(ws, "yolo-user-env.sh")
			agentFile := filepath.Join(ws, agentEnvStateDir, "fxa.sh")
			if rt == "container" {
				sharedFile = filepath.Join(ws, ".config", "yolo-user-env.sh")
				agentFile = filepath.Join(ws, entrypoint.AgentEnvDirRel, "fxa.sh")
			}
			requireWinners(t, rt+" agent (shared file, then fxa's)", sourcedWinners(t, nil, sharedFile, agentFile),
				agent, wantAgentWinners)
			requireWinners(t, rt+" bare shell (shared file)", sourcedWinners(t, nil, sharedFile), shared, wantSharedWinners)
			// ONE LINE PER NAME in the shared file: K4's env_sources default and no fold line beside it.
			b, err := os.ReadFile(sharedFile)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(b), "export K4="); n != 1 {
				t.Errorf("the shared file writes K4 %d times, want once:\n%s", n, b)
			}
			if strings.Contains(string(b), "K5=") {
				t.Errorf("the shared file writes K5, which a null removes:\n%s", b)
			}
			if got := sourcedWinners(t, map[string]string{"K2": "mine"}, agentFile); got["K2"] != "mine" {
				t.Errorf("%s: the user's own K2 must win in the agent file (OQ-CN8), got %q", rt, got["K2"])
			}
		})
	}
}

// THE macos-user VEHICLE: the session env of `yolo -- fxa` carries fxa's composition, alone and
// with fxa's own file sourced after it (its launcher), and a login shell's session env, which is
// the shared composition, carries fxa's once fxa's file is sourced over it.
func TestTheMacosUserSessionDeliversTheCompositionsWinner(t *testing.T) {
	_, _, _, channel := winnerChannel(t)
	agent := winnersOf(channel.scope.EnvFor("fxa"))
	shared := winnersOf(channel.scope.SharedEnv())
	ws := t.TempDir()
	writeMacosUserAgentEnvFiles(ws, channel)
	agentFile := filepath.Join(ws, macosUserAgentEnvDir, "fxa.sh")

	session := envMap(channel.launchEnv("fxa"))
	requireWinners(t, "macos-user session (yolo -- fxa)", sourcedWinners(t, session), agent, wantAgentWinners)
	requireWinners(t, "macos-user session, then fxa's file", sourcedWinners(t, session, agentFile), agent, wantAgentWinners)
	login := envMap(channel.launchEnv("zsh"))
	requireWinners(t, "macos-user login shell", sourcedWinners(t, login), shared, wantSharedWinners)
	requireWinners(t, "macos-user login shell, then fxa's file", sourcedWinners(t, login, agentFile), agent, wantAgentWinners)
}

// THE READERS: DeliveredTo answers the agent's composition, and deliverySource, the env-override
// pre-flight's launch-wide lookup, answers the source that wins in some process — the shared
// composition first — rather than the first source that holds the name.
func TestTheDeliveryReadersAnswerTheCompositionsWinner(t *testing.T) {
	o, _, _, channel := winnerChannel(t)
	agent := winnersOf(channel.scope.EnvFor("fxa"))
	delivered := map[string]string{}
	for _, k := range winnerKeys {
		v, ok := channel.scope.DeliveredTo("fxa", k)
		if !ok {
			v = unsetMark
		}
		delivered[k] = v
	}
	requireWinners(t, "DeliveredTo(fxa)", delivered, agent, wantAgentWinners)

	for k, want := range map[string]struct{ value, origin string }{
		"K1":      {"shape", packload.FromProfileEnv},
		"FXP_KEY": {"shape-key", packload.FromProfileEnv},
		"K2":      {"es-unclaimed", packload.FromEnvSources}, // the shared composition's winner
		"K4":      {"es-unclaimed", packload.FromEnvSources},
		"K6":      {"shape", packload.FromProfileEnv},
		"K7":      {"es-unclaimed", packload.FromEnvSources}, // fxa removes it; every other process holds it
		"K8":      {"fold-static", packload.FromPackEnv},
	} {
		v, origin, ok := channel.deliverySource(o, nil, k)
		e, eok := channel.scope.Delivered(k)
		if !ok || v != want.value || origin != want.origin {
			t.Errorf("deliverySource(%s) = %q, %q, %v; want %q from %q", k, v, origin, ok, want.value, want.origin)
		}
		if !eok || e.Value != v || e.Origin != origin {
			t.Errorf("deliverySource(%s) disagrees with the composition's Delivered: %+v", k, e)
		}
	}
	if v, origin, ok := channel.deliverySource(o, nil, "K5"); ok {
		t.Errorf("K5 is removed in every process, yet deliverySource answers %q from %q", v, origin)
	}
}

// THE REMOVALS SURVIVE A RECOMPOSITION. An attach composes the channel again from the map the
// first composition hydrated (rekeyChannelForAttach, the pack-skew view), so that map carries the
// removals, and the second composition removes what the first did.
func TestARecomposedChannelKeepsTheEnvSourcesRemovals(t *testing.T) {
	o, cfg, packs, channel := winnerChannel(t)
	again := channelFor(t, o, cfg, packs, channel.userEnv)
	for _, k := range []string{"K5", "K6"} {
		if v, ok := channel.userEnv.Get(k); !ok || v != nil {
			t.Errorf("the channel's hydration must carry %s's removal as a nil value: %v %v", k, v, ok)
		}
	}
	if e, ok := again.scope.SharedEnv().Lookup("K5"); !ok || !e.Unset {
		t.Errorf("the recomposed channel lost K5's removal: %+v %v", e, ok)
	}
	requireWinners(t, "recomposed fxa", winnersOf(again.scope.EnvFor("fxa")), winnersOf(channel.scope.EnvFor("fxa")),
		wantAgentWinners)
}

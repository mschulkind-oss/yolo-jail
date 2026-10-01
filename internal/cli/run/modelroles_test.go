package run

// modelroles_test.go pins the role environment (docs/research/extension-model-defaults.md
// OQ-XM4) at the container vehicle: each agent's own env file, written by deliverChannel from the
// real composePackChannel over the shipped packs, carries YOLO_MODEL_<ROLE> for ITS provider, and
// removes a role another agent's file sets that its own provider does not name, so a child one
// agent starts never inherits the other's tier across providers. The composition half is
// packload's modelroles_test.go.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestEachAgentsEnvFileCarriesItsOwnProvidersTiers(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "opencode"),
		officialPack(t, "zai"), officialPack(t, "cerebras")}
	o.UseProfiles = map[string]string{"pi": "zai", "opencode": "cerebras"}
	// zai ships no tier alias, so the user names one; cerebras ships `default`.
	provs, err := jsonx.Decode([]byte(`{"zai": {"models": {"fast": "glm-5.3-flash"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := bareConfig()
	cfg.Set("providers", provs)
	keys := jsonx.NewOrderedMap()
	keys.Set("ZAI_API_KEY", "tok-zai")
	keys.Set("CEREBRAS_API_KEY", "tok-cerebras")
	_, agents := deliveredFiles(t, channelFor(t, o, cfg, packs, keys))

	pi, opencode := agents["pi"], agents["opencode"]
	if pi == "" || opencode == "" {
		t.Fatalf("both profiled agents get a file of their own; got %v", agents)
	}
	for _, c := range []struct{ agent, body, want string }{
		// Each provider's own tier, qualified, and def-form, since no other file sets that name
		// to a value: a value the user typed for the command wins (OQ-CN8).
		{"pi", pi, "export YOLO_MODEL_FAST=${YOLO_MODEL_FAST:-'zai/glm-5.3-flash'}\n"},
		{"opencode", opencode, "export YOLO_MODEL_DEFAULT=${YOLO_MODEL_DEFAULT:-'cerebras/qwen-3.8-27b'}\n"},
		// Each removes the other's tier, and only the value yolo set: a child of pi started
		// with opencode's default in its environment loses it, a value the user typed stays.
		{"pi", pi, `case "${YOLO_MODEL_DEFAULT-}" in 'cerebras/qwen-3.8-27b') unset YOLO_MODEL_DEFAULT ;; esac`},
		{"opencode", opencode, `case "${YOLO_MODEL_FAST-}" in 'zai/glm-5.3-flash') unset YOLO_MODEL_FAST ;; esac`},
	} {
		if !strings.Contains(c.body, c.want) {
			t.Errorf("%s's env file lacks %q:\n%s", c.agent, c.want, c.body)
		}
	}
	// No provider names `balanced` or `frontier`, so neither file says anything about them.
	for agent, body := range agents {
		for _, none := range []string{"YOLO_MODEL_BALANCED", "YOLO_MODEL_FRONTIER"} {
			if strings.Contains(body, none) {
				t.Errorf("%s's file names %s, which no provider of this launch declares:\n%s", agent, none, body)
			}
		}
	}
}

package run

// secretenvcallsites_test.go pins the launch's three secret-env call sites in composePackChannelWith
// by what they do, so deleting a call fails a test (secret-env-scoping-plan.md; review major 5):
// MCPClaims, UnselectedClaims and PublicValues.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func hydratedWith(public, private map[string]string) *config.HydratedEnv {
	h := config.NewHydratedEnv()
	for k, v := range public {
		h.SetPublic(k, v, config.EnvPlace{Key: "env.set"})
	}
	for k, v := range private {
		h.SetPrivate(k, v, config.EnvPlace{Private: true, Key: "env.secrets.set"})
	}
	return h
}

// An MCP server's requires_env claims a private name for the agents rendering it: without the
// claim the launch would withhold it from every process.
func TestTheLaunchPassesTheMCPClaimsToTheGate(t *testing.T) {
	o := goldenOptions(t.TempDir(), packHome(t))
	cfg := bareConfig()
	server := jsonx.NewOrderedMap()
	server.Set("command", "tavily-mcp")
	server.Set("requires_env", []any{"TAVILY_API_KEY"})
	servers := jsonx.NewOrderedMap()
	servers.Set("tavily", server)
	cfg.Set("mcp_servers", servers)
	channel, err := o.composePackChannel(cfg, setPacks(t), hydratedWith(nil, map[string]string{"TAVILY_API_KEY": "t-probe"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := channel.scope.EnvSourcesFor("claude").Get("TAVILY_API_KEY"); !ok {
		t.Error("claude renders the MCP table, so the server's claim delivers the private key to it")
	}
	if _, ok := channel.scope.EnvSourcesFor("").Get("TAVILY_API_KEY"); ok {
		t.Error("a bare shell never receives a private value")
	}
}

// A shipped provider the launch does not select words the public name's line (row 1): the launch
// hands the gate what those packs claim.
func TestTheLaunchPassesTheUnselectedClaimsToTheGate(t *testing.T) {
	o := goldenOptions(t.TempDir(), packHome(t))
	packs := []*packload.Pack{officialPack(t, "claude")}
	channel, err := o.composePackChannel(bareConfig(), packs, hydratedWith(map[string]string{"ZAI_API_KEY": "z-probe"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(channel.scope.DisclosureWith(packload.DisclosureNotes{}), "\n")
	if !strings.Contains(lines, "the zai provider, which this launch does not select, claims that name") {
		t.Errorf("row 1's unselected-provider line is missing:\n%s", lines)
	}
}

// The session inputs read the public set alone: a private MISE_DISABLE_TOOLS is not merged into
// the boot environment every process inherits.
func TestTheSessionInputsReadThePublicSetAlone(t *testing.T) {
	value := func(h *config.HydratedEnv) string {
		o := goldenOptions(t.TempDir(), packHome(t))
		channel, err := o.composePackChannel(bareConfig(), []*packload.Pack{officialPack(t, "claude")}, h)
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range channel.sessionInputs {
			if in.name == "MISE_DISABLE_TOOLS" {
				return in.value
			}
		}
		t.Fatal("no MISE_DISABLE_TOOLS session input")
		return ""
	}
	if got := value(hydratedWith(map[string]string{"MISE_DISABLE_TOOLS": "pubtool"}, nil)); !strings.Contains(got, "pubtool") {
		t.Fatalf("a public value is merged, got %q", got)
	}
	if got := value(hydratedWith(nil, map[string]string{"MISE_DISABLE_TOOLS": "privtool"})); strings.Contains(got, "privtool") {
		t.Errorf("a private value reached the session inputs: %q", got)
	}
}

// A launch from inside a jail that lacks a provider's key says the private set is not carried
// into a nested jail, and names the next step; a host launch's facts stand as they are (SE-D16).
func TestANestedLaunchNamesWhyAKeyIsMissing(t *testing.T) {
	facts := []string{`  • pack zai requires provider "zai", whose credential variable ZAI_API_KEY is not set in this launch's environment`}
	t.Setenv("YOLO_VERSION", "")
	if got := nestedPrivateKeyNotes(facts); len(got) != 1 {
		t.Errorf("a host launch is not nested: %q", got)
	}
	t.Setenv("YOLO_VERSION", "9.9.9")
	got := strings.Join(nestedPrivateKeyNotes(facts), "\n")
	if !strings.Contains(got, "ZAI_API_KEY") || !strings.Contains(got, "not carried into a nested jail") ||
		!strings.Contains(got, "run this agent from the jail the host launched") {
		t.Errorf("the nested note:\n%s", got)
	}
}

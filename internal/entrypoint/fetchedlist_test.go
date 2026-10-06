package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The FETCHED LIST as the agents' own derives meet it (docs/design/model-lists-and-pickers.md
// OQ-MM6): where no pack or config gives the Bedrock provider a list, the launch writes the one it
// fetched onto the composed entry under its own key, packload.FetchedModelsKey. copilot, which has
// no Bedrock catalog, starts on it; every agent with a catalog of its own never reads it (MM-D32).

// copilotEnvOnBedrock composes the shipped packs for copilot on profile with the bedrock entry's
// list taken away (no pack list, as MM-D32 ships) and fetched set as its fetched list, and runs
// copilot's env producer with the profile's options extra merged in.
func copilotEnvOnBedrock(t *testing.T, profile string, fetched []packload.FetchedModel,
	extra map[string]string) map[string]string {
	t.Helper()
	packs := testPacksForAgent(t, "copilot", "bedrock", "wire-bridge")
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := table.Get("bedrock")
	entry, _ := v.(*jsonx.OrderedMap)
	entry.Delete("models")
	entry.Delete("model_options")
	packload.SetFetchedModels(table, "bedrock", fetched)
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	r := resolved[profile]
	if r.Options == nil {
		r.Options = map[string]string{}
	}
	for k, val := range extra {
		r.Options[k] = val
	}
	resolved[profile] = r
	vars, err := packload.AgentEnv(packs, table, map[string]string{"copilot": profile}, "copilot", profile,
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	return got
}

// TestCopilotStartsOnTheFetchedListWhereNoPackSuppliesOne: with no list copilot composes nothing
// (its BYOK refuses to start without a model), with a fetched one it starts on the list's first
// Anthropic model, the maker the bridge carries untranslated, and a profile's own `model` wins
// over both. It fails if copilot's derive stops reading the fetched list.
func TestCopilotStartsOnTheFetchedListWhereNoPackSuppliesOne(t *testing.T) {
	for _, profile := range []string{"bedrock-bridge", "bedrock"} {
		if got := copilotEnvOnBedrock(t, profile, nil, nil); got["COPILOT_MODEL"] != "" {
			t.Errorf("copilot on %s with no list: %v, want nothing composed", profile, got)
		}
		fetched := []packload.FetchedModel{{ID: "amazon.test-v1", Vendor: "amazon"},
			{ID: "us.anthropic.claude-test-v1", Vendor: "anthropic"}, {ID: "openai.gpt-test-1:0", Vendor: "openai"}}
		got := copilotEnvOnBedrock(t, profile, fetched, nil)
		if got["COPILOT_MODEL"] != "us.anthropic.claude-test-v1" || got["COPILOT_PROVIDER_BASE_URL"] == "" {
			t.Errorf("copilot on %s with a fetched list: %v, want its Claude model through the bridge", profile, got)
		}
		got = copilotEnvOnBedrock(t, profile, fetched, map[string]string{"model": "my.own-model-v1"})
		if got["COPILOT_MODEL"] != "my.own-model-v1" {
			t.Errorf("copilot on %s with the profile's own model: %v", profile, got)
		}
	}
}

// TestOnlyCopilotsDeriveReadsTheFetchedList: an agent with a Bedrock catalog of its own keeps it
// (MM-D32), so no other shipped derive names the fetched list's key. A derive that read it would
// narrow its agent's menu to a list nobody chose.
func TestOnlyCopilotsDeriveReadsTheFetchedList(t *testing.T) {
	root := filepath.Join("..", "..", "packs")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	readers := []string{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(root, e.Name(), "derive.lua"))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), packload.FetchedModelsKey) {
			readers = append(readers, e.Name())
		}
	}
	if strings.Join(readers, ",") != "copilot" {
		t.Errorf("derives reading %s: %v, want copilot's alone", packload.FetchedModelsKey, readers)
	}
}

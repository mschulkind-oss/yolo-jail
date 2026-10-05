package entrypoint

// bedrock_model_list_test.go pins the Bedrock model list's two rulings: OQ-BR9's one helper
// (docs/design/bedrock-plumbing.md, ruled 2026-09-29), through which each agent that binds Bedrock
// expands whatever list it is handed, filtered to what that agent's own client can call; and
// MM-D32 (docs/design/model-lists-and-pickers.md, ruled 2026-10-05, amending ML-D9), which
// withdrew the list packs/bedrock shipped: yolo ships none, every agent starts on its own Bedrock
// default, and copilot, which has no Bedrock catalog, on one cheap open-weight model (MM-D34). A
// pack or the user may still supply a list, and the consumers' renders of one are pinned beside
// each binding; this file pins that none ships, what each agent then starts on, and the helper's
// one text.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrockBindingDerives are the derives that carry the helper: the agent packs whose own
// Bedrock client a `-p bedrock` launch configures.
var bedrockBindingDerives = []string{"claude", "codex", "opencode", "pi"}

// THE HELPER IS ONE TEXT IN FOUR FILES. A derive cannot load another file, so callableModels
// and callableModel are copied into each consumer's derive.lua; a copy edited alone would give
// two agents two answers to "which models of this provider can I call", which is the drift a
// single declaration exists to end.
func TestBedrockModelListHelperIsIdenticalInEveryDerive(t *testing.T) {
	helper := regexp.MustCompile(`(?s)-- THE MODELS OF A MULTI-MAKER PROVIDER THIS AGENT CAN CALL\..*?\nlocal function callableModel\(p, list, profile, pick\)\n.*?\nend\n`)
	var first, firstPack string
	for _, pack := range bedrockBindingDerives {
		p, err := embeddedPack(pack)
		if err != nil {
			t.Fatal(err)
		}
		matches := helper.FindAllString(packload.DeriveScript(p), -1)
		if len(matches) != 1 {
			t.Fatalf("packs/%s/derive.lua carries %d copies of the Bedrock model-list helper, want 1", pack, len(matches))
		}
		if first == "" {
			first, firstPack = matches[0], pack
			continue
		}
		if matches[0] != first {
			t.Errorf("packs/%s/derive.lua's Bedrock model-list helper differs from packs/%s/derive.lua's", pack, firstPack)
		}
	}
	// copilot carries callableModels alone (its providers.json rows, MM-D31), with the same
	// comment, so its copy is held to the same text.
	alone := regexp.MustCompile(`(?s)-- THE MODELS OF A MULTI-MAKER PROVIDER THIS AGENT CAN CALL\..*?\nlocal function callableModels\(p, makers\)\n.*?\nend\n`)
	want := alone.FindString(first)
	p, err := embeddedPack("copilot")
	if err != nil {
		t.Fatal(err)
	}
	got := alone.FindAllString(packload.DeriveScript(p), -1)
	if len(got) != 1 || got[0] != want {
		t.Errorf("packs/copilot/derive.lua's callableModels differs from packs/%s/derive.lua's (%d copies)", firstPack, len(got))
	}
}

// shippedBedrockDeclaration is the `bedrock` provider contribution packs/bedrock ships.
func shippedBedrockDeclaration(t *testing.T) packdecl.ProviderContribution {
	t.Helper()
	p, err := embeddedPack("bedrock")
	if err != nil {
		t.Fatal(err)
	}
	for _, prov := range p.Decl.Providers() {
		if prov.Name == "bedrock" {
			return prov
		}
	}
	t.Fatal("packs/bedrock declares no bedrock provider")
	return packdecl.ProviderContribution{}
}

// YOLO SHIPS NO BEDROCK MODEL LIST (MM-D32, the maintainer 2026-10-05: *"this is another opinion I
// don't want to have. we should just leave it unfiltered, whatever defaults you get, and then
// allow packs to override that if needed"*). The provider declares no models and no model facts,
// and no shipped pack adds to its list with a `models` contribution, which would be the same
// opinion shipped from another pack.
func TestTheShippedBedrockProviderDeclaresNoModelList(t *testing.T) {
	decl := shippedBedrockDeclaration(t)
	if len(decl.Models) != 0 || len(decl.ModelOptions) != 0 {
		t.Errorf("packs/bedrock ships a model list (models %v, model_options %v); MM-D32 ships none",
			decl.Models, decl.ModelOptions)
	}
	all, err := embeddedPackSet()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		for _, mc := range p.Decl.ModelsContributions() {
			if mc.Provider == "bedrock" {
				t.Errorf("packs/%s shapes the bedrock list (%+v); MM-D32 ships no Bedrock list from any pack", p.Name, mc)
			}
		}
	}
}

// useJSON is a CLI-keyed profile selection as YOLO_USE_PROFILES carries it.
func useJSON(t *testing.T, use map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(use)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// bedrockModelID matches a Bedrock model id anywhere in a rendered value: an optional geographic
// or global inference prefix, then a maker and a model, as `global.anthropic.claude-opus-5-5`,
// `us.openai.gpt-6.1-sol` or `openai.gpt-oss-120b-1:0` spell one.
var bedrockModelID = regexp.MustCompile(`(?:^|[^A-Za-z0-9.])((?:(?:global|us|eu|apac|jp|au|us-gov)\.)?` +
	`(?:anthropic|openai|amazon|meta|mistral|qwen|deepseek|moonshot|moonshotai|google|nvidia|minimax|zai|cohere|writer)` +
	`\.[a-z0-9][a-z0-9._:-]*)`)

// EVERY AGENT ON BEDROCK STARTS ON ITS OWN DEFAULT, BUT COPILOT (MM-D32, MM-D34). Every agent pack
// that reaches Bedrock is selected together, with the wire bridge, and each is put on the shipped
// `bedrock` profile and then on `bedrock-bridge`. Every file the boot render writes into the jail
// home, and every variable each agent's env derive composes, is scanned for a Bedrock model id, so
// a pick under any key is found, not only under the keys this test knows. The one id allowed is
// copilot's starting model, and only as copilot's COPILOT_MODEL: copilot has no Bedrock catalog to
// default from, and its BYOK refuses to start without a model.
func TestNoAgentOnBedrockStartsOnAModelYoloPickedButCopilot(t *testing.T) {
	bins := map[string]string{"claude": "claude", "codex": "codex", "opencode": "opencode", "pi": "pi",
		"oh-omp": "omp", "copilot": "copilot"}
	packs := testPacksForAgent(t, "claude", "codex", "opencode", "pi", "omp", "copilot", "bedrock", "wire-bridge")
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	none := func(string) (string, bool) { return "", false }
	for _, profile := range []string{"bedrock", "bedrock-bridge"} {
		t.Run(profile, func(t *testing.T) {
			use := map[string]string{}
			for bin := range bins {
				use[bin] = profile
			}
			for bin := range bins {
				var files []packload.AgentFile
				vars, err := packload.AgentEnv(packs, table, use, bin, profile, none,
					packload.WithResolvedProfiles(resolved), packload.WithAgentFiles(&files))
				if err != nil {
					t.Fatalf("%s's env derive: %v", bin, err)
				}
				if len(files) != 0 {
					t.Errorf("%s composed agent files %+v with no list supplied", bin, files)
				}
				for _, v := range vars {
					for _, m := range bedrockModelID.FindAllStringSubmatch(v.Value, -1) {
						if bin == "copilot" && v.Key == "COPILOT_MODEL" && m[1] == copilotBedrockStartModel {
							continue
						}
						t.Errorf("%s's environment names the Bedrock model %s under %s; yolo picks none (MM-D32)", bin, m[1], v.Key)
					}
				}
				if bin == "copilot" {
					var model string
					for _, v := range vars {
						if v.Key == "COPILOT_MODEL" {
							model = v.Value
						}
					}
					if model != copilotBedrockStartModel {
						t.Errorf("copilot on %s starts on %q, want its one cheap open-weight default %s (MM-D34)",
							profile, model, copilotBedrockStartModel)
					}
				}
			}

			e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &strings.Builder{}, Vars: map[string]string{
				"YOLO_PROVIDERS":    mustCompactJSON(t, table),
				"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
				"YOLO_USE_PROFILES": useJSON(t, use),
			}}
			root := t.TempDir()
			for _, pack := range bins {
				withCtxRoot(t, root, pack)
			}
			ConfigurePackSurfaces(e, packs)
			if fails := e.GenFailures(); len(fails) != 0 {
				t.Fatalf("boot render failed: %v\n%s", fails, e.Stderr)
			}
			rendered := 0
			_ = filepath.WalkDir(e.Home, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				rendered++
				for _, m := range bedrockModelID.FindAllStringSubmatch(string(raw), -1) {
					rel, _ := filepath.Rel(e.Home, path)
					t.Errorf("~/%s names the Bedrock model %s; yolo picks none (MM-D32)", rel, m[1])
				}
				return nil
			})
			if rendered == 0 {
				t.Fatal("the boot render wrote nothing, so this scanned nothing")
			}
		})
	}
}

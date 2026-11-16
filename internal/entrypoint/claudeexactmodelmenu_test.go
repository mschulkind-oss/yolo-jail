package entrypoint

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const claudeExactMenuModels = `{"kind":"models","provider":"bedrock","add":[
 {"id":"global.anthropic.claude-fable-5-2","vendor":"anthropic","alias":"fable","name":"Claude Fable 5.2"},
 {"id":"global.anthropic.claude-haiku-5-5","vendor":"anthropic","alias":"haiku","name":"Claude Haiku 5.5"},
 {"id":"global.anthropic.claude-opus-5-5","vendor":"anthropic","alias":"opus","name":"Claude Opus 5.5"},
 {"id":"global.anthropic.claude-sonnet-5-5","vendor":"anthropic","alias":"sonnet","name":"Claude Sonnet 5.5"},
 {"id":"global.openai.gpt-6.1-sol","vendor":"openai"},
 {"id":"global.openai.gpt-6-astra","vendor":"openai"},
 {"id":"global.moonshot.kimi-k3","vendor":"moonshot"},
 {"id":"global.zai.glm-5.3","vendor":"zai"},
 {"id":"global.deepseek.v4.1","vendor":"deepseek"},
 {"id":"global.qwen.qwen3","vendor":"qwen"}]}`

var claudeExactMenuIDs = []any{
	"global.anthropic.claude-fable-5-2",
	"global.anthropic.claude-haiku-5-5",
	"global.anthropic.claude-opus-5-5",
	"global.anthropic.claude-sonnet-5-5",
}

type modelMenuRender struct {
	files map[string][]byte
	env   map[string]string
	vars  []packload.AgentFile
}

// renderModelMenuCaller runs the boot surface loop and host AgentEnv over the same composed
// provider table, so deleting either production projection callsite makes these assertions fail.
func renderModelMenuCaller(t *testing.T, agent, scope string, enforce bool) modelMenuRender {
	t.Helper()
	needs := []string{"bedrock"}
	if agent == "copilot" {
		needs = append(needs, "wire-bridge")
	}
	packs := testPacksForAgent(t, agent, needs...)
	contributes := claudeExactMenuModels
	if scope != "none" {
		only := `,"only":[
		 "global.anthropic.claude-fable-5-2","global.anthropic.claude-haiku-5-5",
		 "global.anthropic.claude-opus-5-5","global.anthropic.claude-sonnet-5-5"]`
		if scope == "claude" {
			contributes += `,{"kind":"models","provider":"bedrock"` + only + `,"agents":["claude"]}`
		} else {
			contributes += `,{"kind":"models","provider":"bedrock"` + only + `}`
		}
	}
	company := companyModelsPack(t, contributes)
	packs = append(packs, company)
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	enforceModels := enforce
	via := ""
	if agent == "copilot" {
		via = "wire-bridge"
	}
	resolved, err := resolveShippedProfiles(packs, map[string]packload.UserProfile{
		"menu": {Provider: "bedrock", Via: via, EnforceModels: &enforceModels},
	}, providers)
	if err != nil {
		t.Fatal(err)
	}
	use := map[string]string{agent: "menu"}
	useJSON := useJSON(t, use)
	errw := &bytes.Buffer{}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_USE_PROFILES": useJSON,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	withCtxRoot(t, t.TempDir(), agent)
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("%s boot surface render failed: %v\n%s", agent, fails, errw.String())
	}
	var files []packload.AgentFile
	vars, err := packload.AgentEnv(packs, providers, use, agent, "menu",
		func(string) (string, bool) { return "", false },
		packload.WithResolvedProfiles(resolved), packload.WithAgentFiles(&files))
	if err != nil {
		t.Fatal(err)
	}
	render := modelMenuRender{files: map[string][]byte{}, env: map[string]string{}, vars: files}
	for _, v := range vars {
		render.env[v.Key] = v.Value
	}
	switch agent {
	case "claude":
		path := filepath.Join(e.Home, ".claude", "settings.json")
		render.files["claude/settings"] = readRenderedBytes(t, path)
	case "pi":
		render.files["pi/settings"] = readRenderedBytes(t, filepath.Join(e.Home, ".pi", "agent", "settings.json"))
		render.files["pi/models"] = readRenderedBytes(t, piModelsPath(e.Home))
	case "copilot":
		for _, file := range files {
			if file.Name == "providers.json" {
				render.files["copilot/providers"] = append([]byte(nil), file.Content...)
			}
		}
	}
	return render
}

func readRenderedBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading rendered surface %s: %v", path, err)
	}
	return data
}

func TestClaudeExactModelMenuProjectsScopedOnlyAtProductionCallers(t *testing.T) {
	for _, enforced := range []bool{false, true} {
		t.Run(map[bool]string{false: "enforcement_off", true: "enforcement_on"}[enforced], func(t *testing.T) {
			got := renderModelMenuCaller(t, "claude", "claude", enforced)
			var settings map[string]any
			if err := json.Unmarshal(got.files["claude/settings"], &settings); err != nil {
				t.Fatalf("Claude settings are not JSON: %v", err)
			}
			picker, _ := settings["modelPicker"].(map[string]any)
			if picker["replaceBuiltInOptions"] != true {
				t.Errorf("replaceBuiltInOptions = %v, want true", picker["replaceBuiltInOptions"])
			}
			options, _ := picker["options"].([]any)
			var ids []any
			for _, option := range options {
				row, _ := option.(map[string]any)
				ids = append(ids, row["model"])
			}
			if !reflect.DeepEqual(ids, claudeExactMenuIDs) {
				t.Errorf("Claude picker ids = %v, want exact provider order %v", ids, claudeExactMenuIDs)
			}
			for tier, id := range map[string]string{
				"FABLE": "global.anthropic.claude-fable-5-2", "HAIKU": "global.anthropic.claude-haiku-5-5",
				"OPUS": "global.anthropic.claude-opus-5-5", "SONNET": "global.anthropic.claude-sonnet-5-5",
			} {
				if got.env["ANTHROPIC_DEFAULT_"+tier+"_MODEL"] != id {
					t.Errorf("Claude %s tier pin = %q, want %q", tier, got.env["ANTHROPIC_DEFAULT_"+tier+"_MODEL"], id)
				}
			}
			if enforced {
				if !reflect.DeepEqual(settings["availableModels"], claudeExactMenuIDs) || settings["enforceAvailableModels"] != true {
					t.Errorf("enforced model settings = available %v / enforce %v, want exact four-row list and true",
						settings["availableModels"], settings["enforceAvailableModels"])
				}
			} else {
				for _, key := range []string{"availableModels", "enforceAvailableModels"} {
					if _, present := settings[key]; present {
						t.Errorf("enforcement-off settings unexpectedly contain %s: %v", key, settings[key])
					}
				}
			}
			if !enforced {
				unscoped := renderModelMenuCaller(t, "claude", "all", false)
				if !bytes.Equal(got.files["claude/settings"], unscoped.files["claude/settings"]) {
					t.Errorf("Claude scoped-only render differs from the equivalent unscoped only:\\nscoped=%s\\nunscoped=%s",
						got.files["claude/settings"], unscoped.files["claude/settings"])
				}
			}
		})
	}
}

func TestScopedClaudeOnlyLeavesPiAndCopilotRendersByteIdentical(t *testing.T) {
	for _, agent := range []string{"pi", "copilot"} {
		t.Run(agent, func(t *testing.T) {
			before := renderModelMenuCaller(t, agent, "none", false)
			after := renderModelMenuCaller(t, agent, "claude", false)
			if !reflect.DeepEqual(before.files, after.files) {
				t.Errorf("%s surfaces changed under Claude-scoped only:\nbefore=%v\nafter=%v", agent, before.files, after.files)
			}
			if agent == "copilot" && len(before.files["copilot/providers"]) == 0 {
				t.Fatalf("Copilot providers.json was not composed, so its equality assertion would be vacuous: files=%+v env=%v",
					before.vars, before.env)
			}
			if agent == "pi" {
				var settings map[string]any
				if err := json.Unmarshal(before.files["pi/settings"], &settings); err != nil {
					t.Fatalf("Pi settings are not JSON: %v", err)
				}
				wantPiIDs := []any{
					"amazon-bedrock/global.anthropic.claude-fable-5-2",
					"amazon-bedrock/global.anthropic.claude-haiku-5-5",
					"amazon-bedrock/global.anthropic.claude-opus-5-5",
					"amazon-bedrock/global.anthropic.claude-sonnet-5-5",
					"amazon-bedrock/global.openai.gpt-6.1-sol", "amazon-bedrock/global.openai.gpt-6-astra",
					"amazon-bedrock/global.moonshot.kimi-k3", "amazon-bedrock/global.zai.glm-5.3",
					"amazon-bedrock/global.deepseek.v4.1", "amazon-bedrock/global.qwen.qwen3",
				}
				if !reflect.DeepEqual(settings["enabledModels"], wantPiIDs) {
					t.Errorf("Pi enabledModels = %v, want unchanged unscoped provider list %v", settings["enabledModels"], wantPiIDs)
				}
			}
			if !reflect.DeepEqual(before.env, after.env) || !reflect.DeepEqual(before.vars, after.vars) {
				t.Errorf("%s host AgentEnv changed under Claude-scoped only: before=%v/%v after=%v/%v",
					agent, before.env, before.vars, after.env, after.vars)
			}
		})
	}
}

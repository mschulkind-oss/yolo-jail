package entrypoint

// codex_model_list_test.go pins the ONE openai-codex model list
// (docs/design/model-lists-and-pickers.md ML-D1): packs/openai-auth/pack.json declares it on
// the provider, and every consumer renders it — claude's picker and allowlist, claude's
// launch env, pi's selection and pi-subagents' scope, the data file pi's extension
// registers, codex's model, opencode's selection and the rows and whitelist on its own `openai`
// provider. The maintainer's report (2026-09-27) was that two hand-kept
// lists had drifted apart; the test that keeps them together is this one.
//
// Every consumer is driven through its PRODUCTION call site — the boot render
// (ConfigurePackByName, ConfigurePackSurfaces), the host env composition
// (packload.AgentEnv), and the shipped extension under node — over a provider table
// composed from the real needs closure. The SECOND pass composes a user override that adds
// one id and removes another, and asserts every consumer follows it: a consumer that went
// back to a literal list passes the first pass and fails the second.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// codexModel is one row of the expanded list, the Go statement of codexModelList's rule.
type codexModel struct {
	ID, Base, Name, Description string
	ContextWindow               float64 // 0: undeclared
}

// expandCodexModels is codexModelList (packs/{claude,pi,codex}/derive.lua) in Go, written
// from the rule rather than from the Lua: one row per distinct id, whose facts are the alias
// spelled as the id when there is one, with every other alias naming that id filling only
// the facts still missing, in sorted alias order; rows ordered by `order` (declared before
// undeclared, then by id); and a `<id>[1m]` row after each base that declares
// long_context_window.
func expandCodexModels(models map[string]string, opts map[string]map[string]string) []codexModel {
	type row struct {
		id, name, desc string
		order, cw, lcw float64
		hasOrder       bool
	}
	num := func(s string) (float64, bool) {
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	aliases := make([]string, 0, len(models))
	for a := range models {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	byID := map[string]*row{}
	var ids []string
	absorb := func(id, alias string) {
		r, ok := byID[id]
		if !ok {
			r = &row{id: id}
			byID[id] = r
			ids = append(ids, id)
		}
		f := opts[alias]
		if r.name == "" {
			r.name = f["name"]
		}
		if r.desc == "" {
			r.desc = f["description"]
		}
		if !r.hasOrder {
			r.order, r.hasOrder = num(f["order"])
		}
		if r.cw == 0 {
			r.cw, _ = num(f["context_window"])
		}
		if r.lcw == 0 {
			r.lcw, _ = num(f["long_context_window"])
		}
	}
	for _, a := range aliases {
		if id := models[a]; id != "" && id == a {
			absorb(id, a)
		}
	}
	for _, a := range aliases {
		if id := models[a]; id != "" && id != a {
			absorb(id, a)
		}
	}
	rows := make([]row, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, *byID[id])
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.hasOrder != b.hasOrder {
			return a.hasOrder
		}
		if a.hasOrder && a.order != b.order {
			return a.order < b.order
		}
		return a.id < b.id
	})
	var out []codexModel
	for _, r := range rows {
		out = append(out, codexModel{ID: r.id, Name: r.name, Description: r.desc, ContextWindow: r.cw})
		if r.lcw != 0 {
			m := codexModel{ID: r.id + "[1m]", Base: r.id, ContextWindow: r.lcw}
			if r.name != "" {
				m.Name = r.name + " (1M context)"
			}
			if r.desc != "" {
				m.Description = r.desc + " · 1M context"
			}
			out = append(out, m)
		}
	}
	return out
}

// composedCodexModels reads the openai-codex entry's models and model_options back out of a
// composed table, so the expected list follows whatever the table holds.
func composedCodexModels(t *testing.T, providers *jsonx.OrderedMap) (map[string]string, map[string]map[string]string) {
	t.Helper()
	raw, ok := providers.Get("openai-codex")
	entry, isMap := raw.(*jsonx.OrderedMap)
	if !ok || !isMap {
		t.Fatalf("the composed table has no openai-codex entry: %v", providers.Keys())
	}
	models := map[string]string{}
	if mv, ok := entry.Get("models"); ok {
		m, _ := mv.(*jsonx.OrderedMap)
		for _, a := range m.Keys() {
			v, _ := m.Get(a)
			if s, ok := v.(string); ok {
				models[a] = s
			}
		}
	}
	opts := map[string]map[string]string{}
	if ov, ok := entry.Get("model_options"); ok {
		o, _ := ov.(*jsonx.OrderedMap)
		for _, a := range o.Keys() {
			v, _ := o.Get(a)
			facts, _ := v.(*jsonx.OrderedMap)
			if facts == nil {
				continue
			}
			opts[a] = map[string]string{}
			for _, k := range facts.Keys() {
				fv, _ := facts.Get(k)
				if s, ok := fv.(string); ok {
					opts[a][k] = s
				}
			}
		}
	}
	return models, opts
}

func codexIDs(list []codexModel, prefix string) []any {
	out := make([]any, 0, len(list))
	for _, m := range list {
		out = append(out, prefix+m.ID)
	}
	return out
}

// shippedCodexDeclaration is the openai-codex provider contribution packs/openai-auth ships.
func shippedCodexDeclaration(t *testing.T) packdecl.ProviderContribution {
	t.Helper()
	p, err := embeddedPack("openai-auth")
	if err != nil {
		t.Fatal(err)
	}
	for _, prov := range p.Decl.Providers() {
		if prov.Name == "openai-codex" {
			return prov
		}
	}
	t.Fatal("packs/openai-auth declares no openai-codex provider")
	return packdecl.ProviderContribution{}
}

// codexConsumers is what every consumer rendered for one composed table.
type codexConsumers struct {
	claudeAvailable []any
	claudeOptions   []any
	claudeEnv       map[string]string
	piSettings      map[string]any
	piModelsFile    map[string]any
	codexModel      any
	opencodeConfig  map[string]any
}

// piCodexModelsRel is the pi/codex-models surface's path, relative to HOME, read off the
// shipped pi manifest — never spelled here, so the extension's path is pinned to the
// manifest's rather than to a third copy.
func piCodexModelsRel(t *testing.T) string {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	surfaces, _ := pi.SurfacesFor(false)
	for _, s := range surfaces {
		if s.Agent == "pi" && s.Name == "codex-models" {
			rel, ok := strings.CutPrefix(s.Path, "~/")
			if !ok {
				t.Fatalf("pi/codex-models path %q is not home-relative", s.Path)
			}
			return rel
		}
	}
	t.Fatal("packs/pi declares no pi/codex-models surface")
	return ""
}

func renderCodexConsumers(t *testing.T, user *jsonx.OrderedMap) (codexConsumers, *jsonx.OrderedMap) {
	t.Helper()
	packs := testPacksForAgent(t, "pi", "claude", "codex", "opencode")
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	providersJSON := mustCompactJSON(t, providers)
	wire := mustCompactJSON(t, packload.ProfilesWireTable(resolved))
	var got codexConsumers

	// claude settings, through the boot render.
	e, _ := newClaudePrismEnv(t, map[string]string{
		"YOLO_PROVIDERS":    providersJSON,
		"YOLO_USE_PROFILES": `{"claude":"codex"}`,
		"YOLO_PROFILES":     wire,
	})
	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatal(err)
	}
	settings := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))
	got.claudeAvailable, _ = settings["availableModels"].([]any)
	if picker, ok := settings["modelPicker"].(map[string]any); ok {
		got.claudeOptions, _ = picker["options"].([]any)
	}

	// claude env, through the host composition a launch runs.
	vars, err := packload.AgentEnv(packs, providers, map[string]string{"claude": "codex"},
		"claude", "codex", func(string) (string, bool) { return "", false },
		packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	got.claudeEnv = map[string]string{}
	for _, v := range vars {
		got.claudeEnv[v.Key] = v.Value
	}

	// pi settings and the extension's data file, and opencode's config, through the boot render.
	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(wire)
	r.render(t, `{"pi":"codex","opencode":"codex"}`)
	got.piSettings = r.piSettings(t)
	got.piModelsFile = r.surface(t, strings.Split(piCodexModelsRel(t), "/")...)
	got.opencodeConfig = r.ocConfig(t)

	// codex, through the boot render.
	codexCfg := renderCodexConfig(t, providersJSON, `{"codex":"codex"}`, wire)
	got.codexModel = codexCfg["model"]
	return got, providers
}

// requireConsumersRender asserts every consumer against one expected list.
func requireConsumersRender(t *testing.T, got codexConsumers, want []codexModel) {
	t.Helper()
	requireConsumersRenderTiers(t, got, want, nil)
}

// opencodeSmallModel is the tiers key for opencode's small model, which is no variable: the id an
// alias moves opencode's `small_model` to.
const opencodeSmallModel = "opencode small_model"

// requireConsumersRenderTiers is requireConsumersRender for a table that names a tier alias:
// tiers holds each claude tier variable the alias moves off the default entry, and its id, and
// under opencodeSmallModel the id opencode's small model moves to.
func requireConsumersRenderTiers(t *testing.T, got codexConsumers, want []codexModel, tiers map[string]string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatal("the expected list is empty, so every assertion below would measure nothing")
	}
	first := want[0]

	if !reflect.DeepEqual(got.claudeAvailable, codexIDs(want, "")) {
		t.Errorf("claude availableModels = %v, want %v", got.claudeAvailable, codexIDs(want, ""))
	}
	var wantOptions []any
	for _, m := range want {
		o := map[string]any{"model": m.ID, "label": m.ID}
		if m.Name != "" {
			o["label"] = m.Name
		}
		if m.Description != "" {
			o["description"] = m.Description
		}
		wantOptions = append(wantOptions, o)
	}
	if !reflect.DeepEqual(got.claudeOptions, wantOptions) {
		t.Errorf("claude modelPicker.options = %v, want %v", got.claudeOptions, wantOptions)
	}

	// EVERY TIER IS PINNED TO THE DEFAULT ENTRY (MM-D2): the declared list names no tier alias,
	// and claude's background, hook and classifier requests pick by tier, so an unpinned one
	// reached the Claude subscription's own model through a bridge that serves none.
	wantEnv := map[string]string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   first.ID,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": first.ID,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  first.ID,
		"ANTHROPIC_DEFAULT_FABLE_MODEL":  first.ID,
	}
	if first.Name != "" {
		wantEnv["ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"] = first.Name
	}
	if first.Description != "" {
		wantEnv["ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION"] = first.Description + " (default)"
	}
	for k, v := range tiers {
		if k != opencodeSmallModel {
			wantEnv[k] = v
		}
	}
	for k, v := range wantEnv {
		if got.claudeEnv[k] != v {
			t.Errorf("claude env %s = %q, want %q", k, got.claudeEnv[k], v)
		}
	}
	// NO START PIN WITHOUT THE OPT-IN (MM-D3). claude returns to ANTHROPIC_MODEL at every
	// launch, over a `/model` choice it saved, so pinning it overrode a valid choice every
	// time; the allowlist already replaces an off-list saved model with Default, which the
	// tier pins above make the list's default.
	for _, k := range []string{"ANTHROPIC_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		if v, set := got.claudeEnv[k]; set {
			t.Errorf("claude env %s = %q, want it unset — the profile does not opt in with pin_model", k, v)
		}
	}

	if got.piSettings["defaultProvider"] != "openai-codex" || got.piSettings["defaultModel"] != first.ID {
		t.Errorf("pi selection = %v/%v, want openai-codex/%s",
			got.piSettings["defaultProvider"], got.piSettings["defaultModel"], first.ID)
	}
	sub, _ := got.piSettings["subagents"].(map[string]any)
	if sub["defaultModel"] != "openai-codex/"+first.ID {
		t.Errorf("pi subagents.defaultModel = %v, want openai-codex/%s", sub["defaultModel"], first.ID)
	}
	scope, _ := sub["modelScope"].(map[string]any)
	if !reflect.DeepEqual(scope["allow"], codexIDs(want, "openai-codex/")) {
		t.Errorf("pi subagents.modelScope.allow = %v, want the exact declared ids %v",
			scope["allow"], codexIDs(want, "openai-codex/"))
	}
	// pi writes NO scope for openai-codex (ML-D2): its "all" view is the registered list,
	// which the data file below and the extension pin, so a scope could only restate it.
	if scoped, present := got.piSettings["enabledModels"]; present {
		t.Errorf("pi enabledModels = %v, want it absent — pi's \"all\" view for openai-codex "+
			"is the declared list, and a second copy of it is the drift this test exists for", scoped)
	}

	var wantFile []any
	for _, m := range want {
		o := map[string]any{"id": m.ID}
		if m.Base != "" {
			o["base"] = m.Base
		}
		if m.Name != "" {
			o["name"] = m.Name
		}
		if m.ContextWindow != 0 {
			o["contextWindow"] = m.ContextWindow
		}
		wantFile = append(wantFile, o)
	}
	if !reflect.DeepEqual(got.piModelsFile["models"], wantFile) {
		t.Errorf("pi codex-models file models = %v, want %v", got.piModelsFile["models"], wantFile)
	}

	if got.codexModel != first.ID {
		t.Errorf("codex config model = %v, want %s", got.codexModel, first.ID)
	}

	// opencode: its own `openai` provider (opencode's built-in ChatGPT client) carries the list as
	// rows, the menu is exactly the list (the whitelist, the switch being on), and the session
	// starts on the list's default, the one codexDefault picks for every consumer.
	oc := got.opencodeConfig
	small := first.ID
	if id, moved := tiers[opencodeSmallModel]; moved {
		small = id
	}
	if oc["model"] != "openai/"+first.ID || oc["small_model"] != "openai/"+small {
		t.Errorf("opencode model = %v, small_model = %v, want openai/%s and openai/%s",
			oc["model"], oc["small_model"], first.ID, small)
	}
	rows, _ := oc["provider"].(map[string]any)
	row, _ := rows["openai"].(map[string]any)
	models, _ := row["models"].(map[string]any)
	var wantIDs []string
	for _, m := range want {
		wantIDs = append(wantIDs, m.ID)
		entry, _ := models[m.ID].(map[string]any)
		if entry == nil {
			t.Errorf("opencode's openai row has no %s: %v", m.ID, models)
			continue
		}
		if m.Name != "" && entry["name"] != m.Name {
			t.Errorf("opencode's %s is named %v, want %q", m.ID, entry["name"], m.Name)
		}
		if m.Base != "" && entry["id"] != m.Base {
			t.Errorf("opencode's %s sends model id %v, want its base %s", m.ID, entry["id"], m.Base)
		}
	}
	if len(models) != len(want) {
		t.Errorf("opencode's openai row has %d models, want the list's %d: %v", len(models), len(want), models)
	}
	sort.Strings(wantIDs)
	if got := strs(row["whitelist"]); !reflect.DeepEqual(got, wantIDs) {
		t.Errorf("opencode's openai whitelist = %v, want the list's ids %v", got, wantIDs)
	}
}

func TestEveryCodexModelConsumerReadsTheOneDeclaration(t *testing.T) {
	// 1-2. The declaration, and the rule over it. The literal pins the RULE: the order, the
	// variant placement and the [1m] spelling, stated once here rather than per consumer.
	decl := shippedCodexDeclaration(t)
	shipped := expandCodexModels(decl.Models, decl.ModelOptions)
	wantIDs := []any{"gpt-6.1-sol", "gpt-6.1-sol[1m]", "gpt-6-astra", "gpt-6-astra[1m]", "gpt-6-luna", "gpt-6-luna[1m]"}
	if got := codexIDs(shipped, ""); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("the shipped declaration expands to %v, want %v", got, wantIDs)
	}

	// 3-4. Every consumer, over the table the real needs closure composes.
	got, composed := renderCodexConsumers(t, nil)
	models, opts := composedCodexModels(t, composed)
	if composedList := expandCodexModels(models, opts); !reflect.DeepEqual(composedList, shipped) {
		t.Fatalf("the composed table expands to %v, want the declaration's %v", composedList, shipped)
	}
	requireConsumersRender(t, got, shipped)

	// 5. A USER OVERRIDE, which is what fails a consumer that went back to a literal: one id
	// added with no facts (so it sorts last and has no 1M variant), and two removed. One of
	// the two is the declared DEFAULT, because the consumers that read only the first id
	// (claude's env, codex's model) would otherwise pass against a literal default.
	user := jsonx.NewOrderedMap()
	codexOverride := jsonx.NewOrderedMap()
	overrideModels := jsonx.NewOrderedMap()
	overrideModels.Set("gpt-6-nova", "gpt-6-nova")
	overrideModels.Set("gpt-6.1-sol", nil)
	overrideModels.Set("gpt-6-luna", nil)
	codexOverride.Set("models", overrideModels)
	user.Set("openai-codex", codexOverride)
	gotOverride, composedOverride := renderCodexConsumers(t, user)
	models, opts = composedCodexModels(t, composedOverride)
	overridden := expandCodexModels(models, opts)
	wantOverrideIDs := []any{"gpt-6-astra", "gpt-6-astra[1m]", "gpt-6-nova"}
	if ids := codexIDs(overridden, ""); !reflect.DeepEqual(ids, wantOverrideIDs) {
		t.Fatalf("the overridden table expands to %v, want %v", ids, wantOverrideIDs)
	}
	requireConsumersRender(t, gotOverride, overridden)

	// 6. The shipped extension, under node, reading the file the boot render just wrote from
	// the path the manifest declares. Its registered ids must be claude's allowlist.
	for _, pass := range []struct {
		name string
		got  codexConsumers
	}{{"shipped", got}, {"override", gotOverride}} {
		ids := runExtensionRegisteredIDs(t, pass.got.piModelsFile)
		if !reflect.DeepEqual(ids, pass.got.claudeAvailable) {
			t.Errorf("%s: the pi extension registers %v, and claude's availableModels is %v",
				pass.name, ids, pass.got.claudeAvailable)
		}
	}
}

// runExtensionRegisteredIDs writes the rendered data file into a fresh HOME, at the
// manifest's path, and runs the shipped extension there, returning the model ids it passed
// to registerProvider.
func runExtensionRegisteredIDs(t *testing.T, file map[string]any) []any {
	t.Helper()
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, filepath.FromSlash(piCodexModelsRel(t)))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.mjs"), []byte(`
import extension from "./extension.mjs";
let registration;
await extension({ registerProvider(name, config) { registration = { name, config }; }, on() {} });
if (!registration || registration.name !== "openai-codex") throw new Error("openai-codex was not registered");
console.log(JSON.stringify((registration.config.models ?? []).map((m) => m.id)));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(requireNode(t, "the shipped pi extension"), "harness.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running the shipped pi extension: %v\n%s", err, out)
	}
	var ids []any
	if err := json.Unmarshal(out, &ids); err != nil {
		t.Fatalf("decoding the extension's registered ids %q: %v", out, err)
	}
	return ids
}

// AN ALIAS FOR A DECLARED ID ADDS NOTHING. `default` and `fast` are yolo's conventional alias
// names (copilot's derive and pi's non-codex arm look `default` up, and config-ref's example
// spells both), so a user who adds one for a declared id must leave every consumer where the
// declaration put it. The helper walks aliases in sorted order and both names sort before
// every gpt-6 id, so under a first-alias-wins rule the id took the NEW alias's facts, which
// are none: Sol fell to last with no name and no 1M variant, and every agent's default moved
// to Astra. The alias spelled as the id is the one that carries the facts; another alias for
// it fills only a fact that one lacks, which the added id below pins.
func TestAnAliasForADeclaredCodexIDChangesNoConsumer(t *testing.T) {
	decl := shippedCodexDeclaration(t)
	shipped := expandCodexModels(decl.Models, decl.ModelOptions)

	user := jsonx.NewOrderedMap()
	codexOverride := jsonx.NewOrderedMap()
	overrideModels := jsonx.NewOrderedMap()
	overrideModels.Set("default", "gpt-6.1-sol")
	overrideModels.Set("fast", "gpt-6-luna")
	// An added id with no facts of its own under its id-named alias, and a second alias that
	// sorts before it and names it: the second alias's name fills the gap.
	overrideModels.Set("gpt-6-nova", "gpt-6-nova")
	named := jsonx.NewOrderedMap()
	named.Set("id", "gpt-6-nova")
	named.Set("name", "GPT-6 Nova")
	overrideModels.Set("a-nova", named)
	codexOverride.Set("models", overrideModels)
	user.Set("openai-codex", codexOverride)

	want := append(append([]codexModel(nil), shipped...), codexModel{ID: "gpt-6-nova", Name: "GPT-6 Nova"})
	got, composed := renderCodexConsumers(t, user)
	models, opts := composedCodexModels(t, composed)
	if list := expandCodexModels(models, opts); !reflect.DeepEqual(list, want) {
		t.Fatalf("the Go statement of the rule expands the aliased table to %v, want %v", list, want)
	}
	// `fast` is also the haiku tier's conventional alias (MM-D17), so claude's haiku tier follows
	// it, and so does opencode's small model, by the same alias rule its other providers follow:
	// the one thing that alias is meant to move. The list, its order, its names and every
	// agent's default stay where the declaration put them.
	requireConsumersRenderTiers(t, got, want, map[string]string{"ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-6-luna",
		opencodeSmallModel: "gpt-6-luna"})
	if ids := runExtensionRegisteredIDs(t, got.piModelsFile); !reflect.DeepEqual(ids, codexIDs(want, "")) {
		t.Errorf("the pi extension registers %v, want %v", ids, codexIDs(want, ""))
	}
}

// THE HELPER IS ONE TEXT IN FOUR FILES. A derive cannot load another file, so
// codexModelList and codexDefault are copied into each consumer's derive.lua; a copy edited
// alone is exactly the drift this list exists to end, so every copy must be byte-identical.
func TestCodexModelListHelperIsIdenticalInEveryDerive(t *testing.T) {
	helper := regexp.MustCompile(`(?s)-- THE openai-codex MODEL LIST\..*?\nlocal function codexDefault\(list, profile\)\n.*?\nend\n`)
	var first, firstPack string
	for _, pack := range []string{"claude", "pi", "codex", "opencode"} {
		p, err := embeddedPack(pack)
		if err != nil {
			t.Fatal(err)
		}
		script := packload.DeriveScript(p)
		matches := helper.FindAllString(script, -1)
		if len(matches) != 1 {
			t.Fatalf("packs/%s/derive.lua carries %d copies of the codex model-list helper, want 1", pack, len(matches))
		}
		if first == "" {
			first, firstPack = matches[0], pack
			continue
		}
		if matches[0] != first {
			t.Errorf("packs/%s/derive.lua's codex model-list helper differs from packs/%s/derive.lua's", pack, firstPack)
		}
	}
}

// THE HOST NOTCH GETS THE DECLARED LIST (OQ-HC1, docs/design/host-computed-layer.md, which
// supersedes docs/design/model-lists-and-pickers.md ML-D8). `yolo host apply` runs pi's derive
// over the provider table it composes at user scope, so pi/codex-models holds the openai-codex
// declaration's expansion at the host too — rendered under `assert`, and under `own` through
// `stateful` (OQ-HC2), where it used to be refused. The extension the host notch delivers then
// registers that list, 1M variants included, in place of pi-ai's built-in catalog, as in a jail.
// This used to pin the opposite (ML-D8: `{}` at the host, the extension registering nothing).
func TestTheHostNotchGivesPiTheDeclaredCodexList(t *testing.T) {
	rel := piCodexModelsRel(t)
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			in := hostTestInputs(t, testPacksForAgent(t, "pi"), nil, nil, nil)
			if r := hostRenderWith(t, home, ownership, in, "pi", "pi/codex-models"); r.Action != "rendered" {
				t.Fatalf("pi/codex-models at the host under %s: %q, want rendered", ownership, r.Action)
			}
			p := shippedPiPack(t)
			if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			var file map[string]any
			if err := json.Unmarshal(raw, &file); err != nil {
				t.Fatalf("the host-rendered %s is not JSON: %v\n%s", rel, err, raw)
			}
			if models, _ := file["models"].([]any); len(models) == 0 {
				t.Fatalf("the host notch rendered no openai-codex list:\n%s", raw)
			}
			// The extension the host notch delivered, run from where it landed with that home.
			harness := filepath.Join(t.TempDir(), "harness.mjs")
			ext := filepath.Join(home, ".pi", "agent", "extensions", "yolo-openai-auth.js")
			if err := os.WriteFile(harness, []byte(`
const { default: extension } = await import(process.env.EXT);
let registration;
await extension({ registerProvider(name, config) { registration = { name, config }; }, on() {} });
if (!registration || registration.name !== "openai-codex") throw new Error("openai-codex was not registered");
console.log(JSON.stringify((registration.config.models || []).map((m) => m.id)));
`), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(requireNode(t, "the host-delivered pi extension"), harness)
			cmd.Env = append(os.Environ(), "HOME="+home, "EXT="+ext)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running the host-delivered extension: %v\n%s", err, out)
			}
			var ids []string
			if err := json.Unmarshal(out, &ids); err != nil {
				t.Fatalf("harness output: %v\n%s", err, out)
			}
			long := false
			for _, id := range ids {
				if strings.HasSuffix(id, "[1m]") {
					long = true
				}
				if strings.HasPrefix(id, "gpt-5") {
					t.Errorf("the host-delivered extension registered pi-ai's GPT-5.x id %q", id)
				}
			}
			if !long {
				t.Errorf("the host-delivered extension registered no 1M variant: %v", ids)
			}
		})
	}
}

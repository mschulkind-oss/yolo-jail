package entrypoint

// hostcomputedlayer_test.go pins OQ-HC1–HC3 (docs/reference/host-agent-environment.md, ruled
// 2026-09-28): `yolo host apply` runs the SAME derives a jail runs, over inputs composed at user
// scope, and lands their output per key (HC-D10). Every test here renders through
// RenderHostPack, the one host entry, with a HostInputs built the way internal/cli's
// composeHostInputs builds one, so deleting the derive call in RenderHostPack — or its handing
// of the inputs to the Env — fails them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostTestInputs composes the host's derive inputs for packs as the CLI does: the provider table
// the packs compose with nothing served, the profiles resolved over it with every via cleared,
// the given selection, and the given server tables (nil for none).
func hostTestInputs(t *testing.T, packs []*packload.Pack, use map[string]string,
	mcp, lsp map[string]any) *HostInputs {
	t.Helper()
	providers, _, err := packload.ComposeProvidersAt(nil, packs, nil, packload.NothingServed())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	inert, _ := packload.ViaServedAt(resolved, packs, packload.NothingServed())
	plain := func(v any) string {
		if v == nil {
			return "{}"
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	useTable := map[string]any{}
	for k, v := range use {
		useTable[k] = v
	}
	if providers == nil {
		providers = jsonx.NewOrderedMap()
	}
	return &HostInputs{Packs: packs, Vars: map[string]string{
		ProvidersWireEnv:   mustCompactJSON(t, providers),
		ProfilesWireEnv:    mustCompactJSON(t, packload.ProfilesWireTable(inert)),
		UseProfilesWireEnv: plain(useTable),
		MCPServersWireEnv:  plain(mcp),
		LSPServersWireEnv:  plain(lsp),
	}}
}

// hostRenderWith renders the named shipped pack (with the packs its `needs` close over) into
// home under ownership, writing, and returns the one surface's result.
func hostRenderWith(t *testing.T, home string, ownership render.HostOwnership, in *HostInputs,
	pack, surface string) HostRenderResult {
	t.Helper()
	var p *packload.Pack
	for _, cand := range in.Packs {
		if cand.Name == pack {
			p = cand
		}
	}
	if p == nil {
		t.Fatalf("fixture: pack %q is not in the input set", pack)
	}
	results, err := RenderHostPack(p, home, ownership, false,
		packoverlay.Collect(in.Packs, false, nil), in)
	if err != nil {
		t.Fatalf("RenderHostPack(%s): %v", pack, err)
	}
	return resultFor(t, results, surface)
}

// hostMechanisms is the two ways an owned host writes a composing surface: `stateful`, which the
// shipped packs' settings surfaces declare (and their `computed` ones are coerced to, OQ-HC2),
// and the rmw arm, which runs for a surface its pack declares `rmw`. Until the `assert`
// retirement (OQ-CO14) these were the two CONTRACTS' mechanisms, the retired `assert` forcing
// every surface through rmw; now the pack's declaration picks, so the rmw case re-declares the
// shipped pack's surfaces `rmw` to reach that arm (declaredRMW).
var hostMechanisms = []struct {
	name string
	rmw  bool
}{{"stateful", false}, {"rmw", true}}

// declaredRMW returns packs with the named pack's config surfaces re-declared `rmw`
// (asRetiredAssert's copy, used for exactly that and not for an asserted home), or packs as
// they are when rmw is false.
func declaredRMW(t *testing.T, packs []*packload.Pack, pack string, rmw bool) []*packload.Pack {
	t.Helper()
	if !rmw {
		return packs
	}
	out := make([]*packload.Pack, len(packs))
	found := false
	for i, p := range packs {
		if p.Name == pack {
			p, found = asRetiredAssert(t, p), true
		}
		out[i] = p
	}
	if !found {
		t.Fatalf("fixture: pack %q is not in the set", pack)
	}
	return out
}

// tavily is a user's own MCP server, the design's running example (§6.7 item 1).
func tavily() map[string]any {
	return map[string]any{"tavily": map[string]any{"command": "npx", "args": []any{"-y", "tavily-mcp"}}}
}

// jsonAt decodes home/rel and walks the dotted path, failing when any step is missing.
func jsonAt(t *testing.T, home, rel string, path ...string) any {
	t.Helper()
	var cur any = decodeJSONFile(t, filepath.Join(home, rel))
	for _, k := range path {
		m, isMap := cur.(map[string]any)
		if !isMap {
			t.Fatalf("%s: %q is not reached (at %v)", rel, strings.Join(path, "."), cur)
		}
		v, ok := m[k]
		if !ok {
			raw, _ := os.ReadFile(filepath.Join(home, rel))
			t.Fatalf("%s has no %s:\n%s", rel, strings.Join(path, "."), raw)
		}
		cur = v
	}
	return cur
}

// EVERY DERIVED SURFACE CLASS gets its computed layer at the host: the provider catalogs, the
// openai-codex list, the MCP tables and the LSP table. Before OQ-HC1 each of these rendered its
// declared layers only — `{}` or an empty table (§2.2's inventory).
func TestTheHostRendersEachDerivedSurfaceClass(t *testing.T) {
	gopls := map[string]any{"gopls": map[string]any{"command": "gopls", "args": []any{"serve"},
		"fileExtensions": map[string]any{".go": "go"}}}
	for _, tc := range []struct {
		name, pack, surface, rel string
		extra                    []string
		mcp, lsp                 map[string]any
		rmw                      bool // the pack's surfaces re-declared `rmw` (declaredRMW)
		check                    func(t *testing.T, home string)
	}{
		{name: "pi/models carries the provider table", pack: "pi", surface: "pi/models",
			rel: ".pi/agent/models.json", extra: []string{"cerebras"},
			check: func(t *testing.T, home string) {
				row := jsonAt(t, home, ".pi/agent/models.json", "providers", "cerebras").(map[string]any)
				if row["baseUrl"] != "https://api.cerebras.ai/v1" {
					t.Errorf("pi/models' cerebras row does not point at the provider: %v", row)
				}
			}},
		{name: "pi/codex-models carries the declared openai-codex list", pack: "pi",
			surface: "pi/codex-models", rel: ".pi/agent/yolo-openai-codex-models.json",
			check: func(t *testing.T, home string) {
				models := jsonAt(t, home, ".pi/agent/yolo-openai-codex-models.json", "models").([]any)
				var long bool
				for _, m := range models {
					if id, _ := m.(map[string]any)["id"].(string); strings.HasSuffix(id, "[1m]") {
						long = true
					}
				}
				if len(models) == 0 || !long {
					t.Errorf("the host list is not the declared one with its 1M variants: %v", models)
				}
			}},
		{name: "pi/mcp carries your mcp_servers", pack: "pi", surface: "pi/mcp",
			rel: ".pi/agent/mcp.json", mcp: tavily(),
			check: func(t *testing.T, home string) {
				jsonAt(t, home, ".pi/agent/mcp.json", "mcpServers", "tavily", "command")
			}},
		{name: "copilot/mcp carries your mcp_servers", pack: "copilot", surface: "copilot/mcp",
			rel: ".copilot/mcp-config.json", mcp: tavily(),
			check: func(t *testing.T, home string) {
				jsonAt(t, home, ".copilot/mcp-config.json", "mcpServers", "tavily", "command")
			}},
		{name: "copilot/lsp carries your lsp_servers", pack: "copilot", surface: "copilot/lsp",
			rel: ".copilot/lsp-config.json", lsp: gopls,
			check: func(t *testing.T, home string) {
				if got := jsonAt(t, home, ".copilot/lsp-config.json", "lspServers", "gopls",
					"command"); got != "gopls" {
					t.Errorf("copilot/lsp gopls command = %v", got)
				}
			}},
		{name: "agy/mcp carries your mcp_servers", pack: "agy", surface: "agy/mcp",
			rel: ".gemini/antigravity-cli/mcp_config.json", mcp: tavily(),
			check: func(t *testing.T, home string) {
				jsonAt(t, home, ".gemini/antigravity-cli/mcp_config.json", "mcpServers", "tavily")
			}},
		// oh-omp/models is yaml: declared `rmw` it renders through rmw's yaml arm (yamltrivia.go),
		// as every surface did under the retired `assert`, and as shipped (`computed`) through
		// `stateful` (OQ-HC2). Until 2026-10-04 the rmw case was refused, "no RMW encoder for
		// codec yaml".
		{name: "oh-omp/models carries the provider table through rmw", pack: "omp",
			surface: "oh-omp/models", rel: ".oh-omp/agent/models.yml", extra: []string{"cerebras"},
			rmw: true,
			check: func(t *testing.T, home string) {
				raw, err := os.ReadFile(filepath.Join(home, ".oh-omp/agent/models.yml"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(raw), "cerebras:") ||
					!strings.Contains(string(raw), "https://api.cerebras.ai/v1") {
					t.Errorf("oh-omp/models has no cerebras row:\n%s", raw)
				}
			}},
		{name: "oh-omp/models carries the provider table through stateful", pack: "omp",
			surface: "oh-omp/models", rel: ".oh-omp/agent/models.yml", extra: []string{"cerebras"},
			check: func(t *testing.T, home string) {
				raw, err := os.ReadFile(filepath.Join(home, ".oh-omp/agent/models.yml"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(raw), "cerebras") ||
					!strings.Contains(string(raw), "https://api.cerebras.ai/v1") {
					t.Errorf("oh-omp/models has no cerebras row:\n%s", raw)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			packs := declaredRMW(t, testPacksForAgent(t, tc.pack, tc.extra...), tc.pack, tc.rmw)
			in := hostTestInputs(t, packs, nil, tc.mcp, tc.lsp)
			r := hostRenderWith(t, home, render.OwnershipOwn, in, tc.pack, tc.surface)
			if r.Action != "rendered" {
				t.Fatalf("%s at the host: %q, want rendered", tc.surface, r.Action)
			}
			tc.check(t, home)
		})
	}
}

// NO DERIVE REMOVES A KEY OF YOURS (HC-D10, §6.7 item 4's second half). claude/settings'
// derive returns an `env` it does not declare in full and a `mcpServers` TOMBSTONE; at the host
// the first asserts only its leaf and the second is dropped, so a variable of yours in `env` and
// a `mcpServers` key in your settings.json both survive, through either mechanism. Handed to the
// rmw arm's table write instead, `env` was cleared (MEASURED in the design's scratch test);
// applied, the tombstone wrote `mcpServers: null`.
func TestAHostComputedLayerKeepsYourKeysOutsideItsTables(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			settings := filepath.Join(home, ".claude", "settings.json")
			writeTestFile(t, settings, `{"env": {"MY_VAR": "x"}, "mcpServers": {"mine": {"command": "me"}}}`)
			in := hostTestInputs(t, declaredRMW(t, testPacksForAgent(t, "claude"), "claude", m.rmw), nil, nil,
				map[string]any{"gopls": map[string]any{"command": "gopls"}})
			if r := hostRenderWith(t, home, render.OwnershipOwn, in, "claude", "claude/settings"); r.Action == "" ||
				strings.HasPrefix(r.Action, "refused") {
				t.Fatalf("claude/settings: %q", r.Action)
			}
			if got := jsonAt(t, home, ".claude/settings.json", "env", "MY_VAR"); got != "x" {
				t.Errorf("your env.MY_VAR was changed to %v", got)
			}
			if got := jsonAt(t, home, ".claude/settings.json", "env", "ENABLE_LSP_TOOL"); got != "1" {
				t.Errorf("the derive's one env leaf did not land: %v", got)
			}
			if got := jsonAt(t, home, ".claude/settings.json", "mcpServers", "mine", "command"); got != "me" {
				t.Errorf("the derive's mcpServers tombstone reached your file: %v", got)
			}
		})
	}
}

// THE JAIL-PATH REFUSAL (HC-D14): a surface whose computed layer names a path only a jail has
// is refused by name and never written. The inputs here bypass the CLI's own per-entry filter
// (HC-D6) on purpose, which is the case the check is the backstop for: a derive — or an input a
// caller composed wrong — spelling a jail path.
func TestTheHostRefusesAComputedLayerThatNamesAJailPath(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	in := hostTestInputs(t, testPacksForAgent(t, "pi"), nil,
		map[string]any{"wrapped": map[string]any{"command": "/home/agent/.local/bin/mcp-wrappers/node"}}, nil)
	r := hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/mcp")
	if !strings.HasPrefix(r.Action, "refused: ") || !strings.Contains(r.Action, "/home/agent") ||
		!strings.Contains(r.Action, "mcpServers.wrapped.command") {
		t.Fatalf("pi/mcp with a jail path: %q, want a refusal naming the key and the path", r.Action)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "mcp.json")); !os.IsNotExist(err) {
		t.Errorf("a refused surface's file was written (err=%v)", err)
	}
}

// PRESETS NEVER EXPAND AT THE HOST (HC-D6), even when a caller hands the list in: their commands
// are a wrapper only a jail's boot writes. A preset let through would also trip the jail-path
// check, so this pins the input rule on its own: the surface renders, without the preset.
func TestTheHostNeverExpandsAnMCPPreset(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	in := hostTestInputs(t, testPacksForAgent(t, "pi"), nil, tavily(), nil)
	in.Vars[mcpPresetsWireEnv] = `["chrome-devtools","sequential-thinking"]`
	if r := hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/mcp"); r.Action != "rendered" {
		t.Fatalf("pi/mcp: %q", r.Action)
	}
	servers := jsonAt(t, home, ".pi/agent/mcp.json", "mcpServers").(map[string]any)
	if _, has := servers["chrome-devtools"]; has || len(servers) != 1 {
		t.Errorf("the host expanded a preset: %v", servers)
	}
}

// JailPathsIn's token rule: a root counts only as a whole path token, and a root the rendered
// home lies under is not a jail path there (HC-D15).
func TestJailPathsInMatchesWholePathTokensOnly(t *testing.T) {
	for _, tc := range []struct {
		value string
		hit   bool
	}{
		{"/workspace", true},
		{"/workspace/bin/x", true},
		{"--dir=/workspace/x", true},
		{"/ctx/host-pi/settings.json", true},
		{"/opt/yolo-jail/bin/yolo", true},
		{"/run/yolo/caller-tokens/T", true},
		{"/run/yolo-services/x.endpoint", true},
		{"/workspaces/other", false},
		{"/srv/workspace", false},
		{"https://example.com/workspace", false},
		{"/home/agentx/bin", false},
		{"gopls", false},
	} {
		got := JailPathsIn(map[string]any{"k": tc.value}, "/home/me")
		if (len(got) > 0) != tc.hit {
			t.Errorf("JailPathsIn(%q) = %v, want hit=%v", tc.value, got, tc.hit)
		}
	}
	if got := JailPathsIn(map[string]any{"k": "/home/agent/.local/bin/x"}, "/home/agent"); len(got) != 0 {
		t.Errorf("a path under the rendered home itself was called a jail path: %v", got)
	}
	if got := JailPathsIn(jsonx.NewOrderedMap(), "/home/me"); got != nil {
		t.Errorf("an empty document names nothing: %v", got)
	}
}

// UNDER `own` A COMPUTED SURFACE RENDERS THROUGH `stateful` AND ADOPTS THE FILE (OQ-HC2). It
// was refused there, which would have left copilot/mcp and its siblings no host path once `assert`
// retired (OQ-CO14).
// The first owned render keeps a key of yours outside the in-full table and writes the table.
// (pi/mcp was this test's surface until it became `stateful` in its own right, on pi's mcp.json.)
func TestAnOwnedHostRenderAdoptsAComputedSurface(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	config := filepath.Join(home, ".copilot", "mcp-config.json")
	writeTestFile(t, config, `{"settings": {"idleTimeout": 30}}`)
	in := hostTestInputs(t, testPacksForAgent(t, "copilot"), nil, tavily(), nil)
	r := hostRenderWith(t, home, render.OwnershipOwn, in, "copilot", "copilot/mcp")
	if strings.HasPrefix(r.Action, "refused") || r.Action == "" {
		t.Fatalf("copilot/mcp under own: %q, want it rendered through stateful", r.Action)
	}
	if got := jsonAt(t, home, ".copilot/mcp-config.json", "settings", "idleTimeout"); got != float64(30) {
		t.Errorf("the first owned render did not adopt your key: %v", got)
	}
	jsonAt(t, home, ".copilot/mcp-config.json", "mcpServers", "tavily")
	if r.Archived == "" {
		t.Errorf("an adopting owned render keeps the one-time archive (OQ-CO7); none was named")
	}
}

// AN EXISTING YAML CATALOG IS READ, through either mechanism, and a provider you added by hand is
// named before it goes. Until 2026-10-04 the rmw arm (the retired `assert`'s only one) refused the
// yaml surface outright and `own` refused it over an existing file ("no RMW decoder for codec
// yaml", hostStatefulRefusal's probe), so neither contract could ever write omp's catalog into a
// home that had one.
func TestAHostRenderReadsAnExistingYAMLCatalog(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			models := filepath.Join(home, ".oh-omp", "agent", "models.yml")
			writeTestFile(t, models, "# my catalog\nproviders:\n  mine:\n"+
				"    baseUrl: http://127.0.0.1:9/v1\n    api: openai-completions\n")
			packs := declaredRMW(t, testPacksForAgent(t, "omp", "cerebras"), "omp", m.rmw)
			in := hostTestInputs(t, packs, nil, nil, nil)
			preview := func() HostRenderResult {
				results, err := RenderHostPack(packs[0], home, render.OwnershipOwn, true,
					packoverlay.Collect(packs, false, nil), in)
				if err != nil {
					t.Fatal(err)
				}
				return resultFor(t, results, "oh-omp/models")
			}
			r := preview()
			if strings.HasPrefix(r.Action, "refused") {
				t.Fatalf("oh-omp/models over an existing file: %q", r.Action)
			}
			if !strings.Contains(strings.Join(r.EntryLosses, " "), "providers.mine") || !r.FirstApply {
				t.Errorf("the hand-added provider is not named as a first-apply loss: %+v", r)
			}
			if r := hostRenderWith(t, home, render.OwnershipOwn, in, "omp", "oh-omp/models"); r.Action != "rendered" {
				t.Fatalf("oh-omp/models --assert: %q", r.Action)
			}
			raw := string(mustRead(t, models))
			if !strings.Contains(raw, "cerebras:") {
				t.Errorf("the catalog was not written:\n%s", raw)
			}
			if again := preview(); again.WouldChange {
				t.Errorf("an apply over the file it just wrote is not in sync: %+v\n%s", again, raw)
			}
		})
	}
}

// THE profile SELECTION, with the jail's edge-triggered rule (OQ-HC3, OQ-PSW2), under `own`,
// through both mechanisms an owned host runs — pi/settings as shipped (`stateful`) and
// re-declared `rmw`: written when the chosen profile arrives, your own later pick standing
// through a re-apply, and on deselection only what yolo wrote cleared.
//
// THE rmw CASE IS THE SELECTION RECORD'S DIRECTORY. At an owned host the record lives in the
// capture store, which only a `stateful` render used to create, so an rmw surface's record
// write failed and every re-apply reverted the user's pick (writeSelectionRecord's MkdirAll).
func TestHostApplyWritesTheUseProfilesSelectionOnTheEdge(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) { hostSelectionOnTheEdge(t, m.rmw) })
	}
}

func hostSelectionOnTheEdge(t *testing.T, rmw bool) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	// A value predating the selection, which the first activation outranks (HC-D17).
	writeTestFile(t, settings, `{"theme": "dark", "defaultModel": "before-yolo"}`)
	packs := declaredRMW(t, testPacksForAgent(t, "pi"), "pi", rmw)
	selected := hostTestInputs(t, packs, map[string]string{"pi": "codex"}, nil, nil)
	apply := func(in *HostInputs) map[string]any {
		t.Helper()
		if r := hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/settings"); strings.HasPrefix(r.Action, "refused") {
			t.Fatalf("pi/settings: %q", r.Action)
		}
		return decodeJSONFile(t, settings)
	}

	doc := apply(selected)
	if doc["defaultProvider"] != "openai-codex" {
		t.Fatalf("activation did not write the selection: %v", doc)
	}
	yoloModel, _ := doc["defaultModel"].(string)
	if yoloModel == "" || yoloModel == "before-yolo" {
		t.Fatalf("activation did not write the profile's model over the earlier one: %v", doc)
	}
	if doc["theme"] != "dark" {
		t.Errorf("a key of yours outside the selection changed: %v", doc)
	}

	// Your own /model pick, then a re-apply with the same selection: it stands.
	doc["defaultModel"] = "my-pick"
	raw, _ := json.Marshal(doc)
	writeTestFile(t, settings, string(raw))
	if doc = apply(selected); doc["defaultModel"] != "my-pick" {
		t.Errorf("a re-apply with an unchanged selection reverted your /model pick: %v", doc)
	}

	// Deselect: the provider yolo wrote is cleared, your pick is kept.
	doc = apply(hostTestInputs(t, packs, nil, nil, nil))
	if _, has := doc["defaultProvider"]; has {
		t.Errorf("deselection left the defaultProvider yolo wrote: %v", doc)
	}
	if doc["defaultModel"] != "my-pick" {
		t.Errorf("deselection cleared your own pick: %v", doc)
	}
}

// A COMPUTED LEAF THAT REPLACES A VALUE OF YOURS IS NAMED, with the input of yours it comes
// from, under `own` — and once your own later pick stands, it is not. MEASURED before
// 2026-10-04: the profile's first activation replaced `defaultModel: before-yolo` and the
// report's Overwrites was [] under the retired `assert` (rmw) and under `own` (stateful).
//
// Through both mechanisms an owned host runs — pi/settings as shipped (`stateful`) and
// re-declared `rmw` — because each builds the report its own way: the rmw arm adds the computed
// leaves to the declaration-based list (withComputedOverwrites), and `stateful` re-measures
// against its composed write (hostStatefulOverwrites). Run under `own` alone with the shipped
// declaration, the rmw arm's half had no test.
func TestTheOverwriteReportNamesAComputedLeaf(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) { overwriteReportNamesAComputedLeaf(t, m.rmw) })
	}
}

func overwriteReportNamesAComputedLeaf(t *testing.T, rmw bool) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	writeTestFile(t, settings, `{"theme": "dark", "defaultModel": "before-yolo"}`)
	packs := declaredRMW(t, testPacksForAgent(t, "pi"), "pi", rmw)
	in := hostTestInputs(t, packs, map[string]string{"pi": "codex"}, nil, nil)
	preview := func() HostRenderResult {
		t.Helper()
		results, err := RenderHostPack(packs[0], home, render.OwnershipOwn, true,
			packoverlay.Collect(packs, false, nil), in)
		if err != nil {
			t.Fatal(err)
		}
		return resultFor(t, results, "pi/settings")
	}
	if got := preview().Overwrites; !hasLine(got, "defaultModel (selected by your profile)") {
		t.Fatalf("the profile's first activation replaces your defaultModel and the "+
			"report does not say so, or not from what: %q", got)
	}
	hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/settings")
	doc := decodeJSONFile(t, settings)
	doc["defaultModel"] = "my-pick"
	raw, _ := json.Marshal(doc)
	writeTestFile(t, settings, string(raw))
	if got := strings.Join(preview().Overwrites, "; "); strings.Contains(got, "defaultModel") {
		t.Errorf("your own later pick stands, and the report says it is replaced: %q", got)
	}
}

// A SELECTION KEY AND A RECOMPUTED LEAF ARE LABELLED APART, under `own`, because what
// happens to a pick of yours afterwards differs. pi's top-level defaultModel comes from the
// profile's selection namespace, written on the activation edge, so your own later pick stands
// (HC-D17); pi-subagents' subagents.defaultModel is a leaf the derive computes from the same
// profile and writes on every apply. Both read "(computed from your profile)" until 2026-10-05,
// and the CLI attached "a pick of your own after that stands" to both — MEASURED (the reviewer's
// probe): your subagent model was replaced on the next apply, under a line saying it would stand.
//
// Through both mechanisms an owned host runs, as shipped (`stateful`) and re-declared `rmw`, for
// the reason TestTheOverwriteReportNamesAComputedLeaf states: each labels the leaves its own way.
func TestTheOverwriteReportTellsASelectionKeyFromARecomputedLeaf(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) { overwriteReportTellsASelectionKeyFromARecomputedLeaf(t, m.rmw) })
	}
}

func overwriteReportTellsASelectionKeyFromARecomputedLeaf(t *testing.T, rmw bool) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	writeTestFile(t, settings, `{"theme": "dark", "defaultModel": "before-yolo"}`)
	packs := declaredRMW(t, testPacksForAgent(t, "pi"), "pi", rmw)
	in := hostTestInputs(t, packs, map[string]string{"pi": "codex"}, nil, nil)
	preview := func() []string {
		t.Helper()
		results, err := RenderHostPack(packs[0], home, render.OwnershipOwn, true,
			packoverlay.Collect(packs, false, nil), in)
		if err != nil {
			t.Fatal(err)
		}
		return resultFor(t, results, "pi/settings").Overwrites
	}
	if got := preview(); !hasLine(got, "defaultModel (selected by your profile)") {
		t.Fatalf("the activation's defaultModel is not labelled as the profile's "+
			"selection: %q", got)
	}
	hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/settings")
	doc := decodeJSONFile(t, settings)
	sub, _ := doc["subagents"].(map[string]any)
	if sub == nil || sub["defaultModel"] == nil {
		t.Fatalf("fixture: the apply wrote no subagents.defaultModel: %v", doc)
	}
	doc["defaultModel"] = "my-pick"
	sub["defaultModel"] = "my-subagent-pick"
	raw, _ := json.Marshal(doc)
	writeTestFile(t, settings, string(raw))

	got := preview()
	if !hasLine(got, "subagents.defaultModel (computed from your profile)") {
		t.Errorf("the recomputed subagents.defaultModel is not reported as computed "+
			"from your profile: %q", got)
	}
	for _, line := range got {
		if strings.HasPrefix(line, "defaultModel ") ||
			strings.Contains(line, "subagents.defaultModel (selected") {
			t.Errorf("a selection label on the wrong key, or your own later pick "+
				"reported as replaced: %q", got)
		}
	}
	// What the labels promise is what the write does.
	hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/settings")
	doc = decodeJSONFile(t, settings)
	if doc["defaultModel"] != "my-pick" {
		t.Errorf("your own later defaultModel pick did not stand: %v", doc)
	}
	if sub, _ := doc["subagents"].(map[string]any); sub["defaultModel"] == "my-subagent-pick" {
		t.Errorf("fixture: the derive no longer re-writes subagents.defaultModel, so this "+
			"test no longer tells the two labels apart: %v", doc)
	}
}

// hasLine reports whether lines holds want exactly.
func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// AND A LEAF THE DERIVE RE-ASSERTS EVERY APPLY: claude/settings' env.ENABLE_LSP_TOOL, from
// your lsp_servers, over a different value in your file, through either mechanism.
func TestTheOverwriteReportNamesAComputedEnvLeaf(t *testing.T) {
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			writeTestFile(t, filepath.Join(home, ".claude", "settings.json"),
				`{"env": {"ENABLE_LSP_TOOL": "0", "MY_VAR": "x"}}`)
			packs := declaredRMW(t, testPacksForAgent(t, "claude"), "claude", m.rmw)
			in := hostTestInputs(t, packs, nil, nil, map[string]any{"gopls": map[string]any{"command": "gopls"}})
			results, err := RenderHostPack(packs[0], home, render.OwnershipOwn, true,
				packoverlay.Collect(packs, false, nil), in)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(resultFor(t, results, "claude/settings").Overwrites, "; ")
			if !strings.Contains(got, "env.ENABLE_LSP_TOOL (computed from your lsp_servers)") {
				t.Errorf("the computed env leaf that replaces your value is not named by its input: %q", got)
			}
			if strings.Contains(got, "MY_VAR") {
				t.Errorf("a key no layer asserts is reported: %q", got)
			}
		})
	}
}

// A KEY A CONFIG-OVERLAY AND A COMPUTED LEAF BOTH WRITE IS REPORTED ONCE, AS THE COMPUTED LEAF,
// through either mechanism. Computed outranks a config-overlay (§5), so the leaf is what replaces
// your value; reported under the overlay too, the line's remedy sent you to a pack edit that could
// not keep it. Through rmw the overlay line is dropped where the computed one is added
// (withComputedOverwrites); through stateful the re-measure skips it (hostStatefulOverwrites).
func TestAComputedLeafOutranksAnOverlayInTheOverwriteReport(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"managed": map[string]any{
		"env": map[string]any{"ENABLE_LSP_TOOL": "from-the-pack"}}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPack := &packload.Pack{Name: "lspov", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "claude/settings", Raw: raw},
		},
	}}
	for _, m := range hostMechanisms {
		t.Run(m.name, func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			writeTestFile(t, filepath.Join(home, ".claude", "settings.json"),
				`{"env": {"ENABLE_LSP_TOOL": "0"}}`)
			packs := append(declaredRMW(t, testPacksForAgent(t, "claude"), "claude", m.rmw), overlayPack)
			in := hostTestInputs(t, packs, nil, nil,
				map[string]any{"gopls": map[string]any{"command": "gopls"}})
			results, err := RenderHostPack(packs[0], home, render.OwnershipOwn, true,
				packoverlay.Collect(packs, false, nil), in)
			if err != nil {
				t.Fatal(err)
			}
			r := resultFor(t, results, "claude/settings")
			if len(r.Overlays) == 0 {
				t.Fatalf("fixture: the overlay pack does not reach claude/settings: %+v", r)
			}
			var lines []string
			for _, o := range r.Overwrites {
				if strings.Contains(o, "ENABLE_LSP_TOOL") {
					lines = append(lines, o)
				}
			}
			if len(lines) != 1 || lines[0] != "env.ENABLE_LSP_TOOL (computed from your lsp_servers)" {
				t.Errorf("want env.ENABLE_LSP_TOOL reported once, as the computed leaf; got %q", lines)
			}
		})
	}
}

// A CATALOG MADE yolo's FOR THE FIRST TIME IN A HOME ASKS FIRST (HC-D8). pi/models' `providers`
// is declared in full, so once the host composes a provider table a provider you added by hand
// goes — and in a home already applied into, the first-apply prompt would not fire. The table
// the record does not yet attribute to yolo is what marks the render FirstApply.
func TestAProviderCatalogNewlyYolosInAHomeIsAFirstApply(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	models := filepath.Join(home, ".pi", "agent", "models.json")
	writeTestFile(t, models, `{"providers": {"mine": {"baseUrl": "http://127.0.0.1:9/v1", "api": "openai-completions"}}}`)
	packs := testPacksForAgent(t, "pi", "cerebras")
	pi := packs[0]
	// A record from an apply that owned no table here: the key is the user's (`host`).
	if err := os.MkdirAll(render.Host(home, nil, render.OwnershipOwn).ProvenanceDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, render.Host(home, nil, render.OwnershipOwn).ProvenancePath("pi", "models"),
		"providers\thost\n")
	results, err := RenderHostPack(pi, home, render.OwnershipOwn, true,
		packoverlay.Collect(packs, false, nil), hostTestInputs(t, packs, nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	r := resultFor(t, results, "pi/models")
	if len(r.EntryLosses) == 0 || !strings.Contains(strings.Join(r.EntryLosses, " "), "providers.mine") {
		t.Fatalf("fixture premise: the hand-added provider is not a loss: %+v", r)
	}
	if !r.FirstApply {
		t.Errorf("the first render that makes `providers` yolo's here must read as a first apply, " +
			"so the confirmation fires before your provider is dropped (HC-D8)")
	}
}

// writeTestFile writes content at path, creating its parent.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THE MCP INPUT IS FILTERED PER SURFACE AGENT, as a jail filters it (HC-D6): a server whose
// `provides` an agent's own login performs is withheld from that agent (claude's subscription
// performs web_search), and a server whose requires_env the agent's composed environment lacks
// is skipped for it and named. With the selected packs or the per-agent lookup left out, both
// agents would get the same table — the defect the per-agent rule exists to prevent.
func TestTheHostMCPInputIsFilteredPerSurfaceAgent(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	packs := testPacksForAgent(t, "claude", "pi")
	in := hostTestInputs(t, packs, nil, map[string]any{
		"search": map[string]any{"command": "search-mcp", "provides": "web_search"},
		"keyed":  map[string]any{"command": "keyed-mcp", "requires_env": []any{"KEYED_TOKEN"}},
	}, nil)
	in.AgentLookup = func(agent string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			if agent == "pi" && key == "KEYED_TOKEN" {
				return "t", true
			}
			return "", false
		}
	}
	overlays := packoverlay.Collect(packs, false, nil)
	render1 := func(pack string) []HostRenderResult {
		for _, p := range packs {
			if p.Name == pack {
				results, err := RenderHostPack(p, home, render.OwnershipOwn, false, overlays, in)
				if err != nil {
					t.Fatal(err)
				}
				return results
			}
		}
		t.Fatalf("no pack %s", pack)
		return nil
	}
	claude := resultFor(t, render1("claude"), "claude/config")
	render1("pi")
	claudeServers := jsonAt(t, home, ".claude.json", "mcpServers").(map[string]any)
	piServers := jsonAt(t, home, ".pi/agent/mcp.json", "mcpServers").(map[string]any)
	if _, has := claudeServers["search"]; has {
		t.Errorf("a web_search server reached claude, whose own login performs it: %v", claudeServers)
	}
	if _, has := piServers["search"]; !has {
		t.Errorf("the web_search server did not reach pi, whose login does not: %v", piServers)
	}
	if _, has := piServers["keyed"]; !has {
		t.Errorf("pi's environment holds KEYED_TOKEN, and its server was skipped: %v", piServers)
	}
	if _, has := claudeServers["keyed"]; has {
		t.Errorf("claude's environment lacks KEYED_TOKEN, and its server was written: %v", claudeServers)
	}
	if !strings.Contains(strings.Join(claude.InputSkips, " "), `"keyed"`) {
		t.Errorf("the skip for claude is not named: %v", claude.InputSkips)
	}
}

// A KEY THE SURFACE'S MANAGED LAYER ALSO ASSERTS IS LEFT OUT OF THE COMPUTED REPORT: managed
// outranks computed (§5), so the file gets managed's value, and managedOverwrites has already
// named the key. Listed here too, it was reported twice, once under a computed label for a layer
// that did not win. A key holding a dot is one key, looked up whole.
func TestComputedOverwritePathsLeavesOutAManagedKey(t *testing.T) {
	existing := jsonx.NewOrderedMap()
	env := jsonx.NewOrderedMap()
	env.Set("ENABLE_LSP_TOOL", "0")
	env.Set("OTHER", "mine")
	existing.Set("env", env)
	existing.Set("model", "mine")
	existing.Set("archimedes.sessionName", "mine")
	leaves := map[string]any{
		"env":                    map[string]any{"ENABLE_LSP_TOOL": "1", "OTHER": "computed"},
		"model":                  "computed",
		"archimedes.sessionName": "computed",
	}
	managed := map[string]any{
		"env":                    map[string]any{"ENABLE_LSP_TOOL": "the pack's"},
		"archimedes.sessionName": "the pack's",
	}
	var got []string
	for _, p := range computedOverwritePaths(existing, leaves, managed) {
		got = append(got, strings.Join(p, "/"))
	}
	if want := "env/OTHER,model"; strings.Join(got, ",") != want {
		t.Errorf("computedOverwritePaths = %v, want %s (the managed keys left out)", got, want)
	}
}

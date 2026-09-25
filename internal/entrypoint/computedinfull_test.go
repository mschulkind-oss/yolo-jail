package entrypoint

// computedinfull_test.go pins CO13 (docs/design/config-ownership-and-promotion.md#co13--how-a-derive-says-it-fills-a-computed-table-in-full--decided)
// end to end, through the SHIPPED packs and the production call sites — the boot loop
// (ConfigurePackByName → renderDeclaredSurface) and the host notch (RenderHostPack →
// hostTableKeys). The engine-level halves are agentcfg's; these are the ones that fail if a
// call site stops passing the declaration along.
//
// The defect both notches shared: "an object-valued key a derive produces is a table yolo
// regenerates in full" was the rule, and claude/settings' `env` is the table it is false for
// by construction. yolo asserts ENABLE_LSP_TOOL there and can never own the rest of a user's
// environment, so treating the table as regenerated took every OTHER variable with it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// THE JAIL HALF: a first migration (no last_render) of claude/settings, with an LSP
// configured so the derive asserts env.ENABLE_LSP_TOOL, over a file that already holds one of
// the agent's own variables. Before CO13 the adoption drop took `env` wholesale and
// AWS_PROFILE was gone from the rendered file.
func TestConfigureClaudePrismFirstMigrationKeepsTheAgentsOwnEnv(t *testing.T) {
	e, _ := newClaudePrismEnv(t, map[string]string{
		"YOLO_LSP_SERVERS": `{"python":{}}`,
	})
	if err := os.MkdirAll(e.ClaudeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(e.ClaudeDir(), "settings.json")
	if err := os.WriteFile(settings,
		[]byte(`{"env":{"ENABLE_LSP_TOOL":"1","AWS_PROFILE":"mine"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("ConfigurePackByName: %v", err)
	}

	got := decodeJSONFile(t, settings)
	env, _ := got["env"].(map[string]any)
	if env["AWS_PROFILE"] != "mine" {
		t.Errorf("env = %v, want AWS_PROFILE kept — claude's derive asserts ONE leaf of env "+
			"and does not declare the table in full (ctx.in_full), so an adopting render may "+
			"claim only that leaf as yolo's own", got["env"])
	}
	if env["ENABLE_LSP_TOOL"] != "1" {
		t.Errorf("env = %v, want yolo's own asserted leaf still written", got["env"])
	}
	// And it is ADOPTED, not merely left over: the capture overlay holds it, so a later boot
	// that stops asserting ENABLE_LSP_TOOL still renders the user's variable.
	overlay := decodeJSONFile(t, prismOverlayPath(e, "claude", "settings"))
	if oenv, _ := overlay["env"].(map[string]any); oenv["AWS_PROFILE"] != "mine" {
		t.Errorf("overlay = %v, want env.AWS_PROFILE adopted into the capture", overlay)
	}
	if oenv, _ := overlay["env"].(map[string]any); oenv["ENABLE_LSP_TOOL"] != nil {
		t.Errorf("overlay = %v, want yolo's asserted leaf narrowed OUT of the capture", overlay)
	}
}

// THE HOST HALF, the twin at `yolo host apply`. hostTableKeys probes the derive with SENTINEL
// live tables, so claude/settings always produced a non-empty `env` there and reported it as a
// wholesale table — and the rmw writer (regenerateManagedTables) then cleared the user's real
// ~/.claude/settings.json env block and rewrote it from yolo's declared layers, which declare
// no env at all.
func TestHostApplyKeepsTheUsersClaudeEnv(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"env":{"AWS_PROFILE":"mine"},"verbose":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	results := hostRenderClaude(t, home, false)

	r := resultFor(t, results, "claude/settings")
	if r.Action != "rendered" {
		t.Fatalf("claude/settings action = %q, want rendered", r.Action)
	}
	for _, loss := range r.EntryLosses {
		t.Errorf("claude/settings reported an entry loss %q — env is not a table yolo owns", loss)
	}
	got := readRenderedJSON(t, home, filepath.Join(".claude", "settings.json"))
	if env, _ := got["env"].(map[string]any); env["AWS_PROFILE"] != "mine" {
		t.Errorf("host ~/.claude/settings.json env = %v, want the user's variable intact", got["env"])
	}
}

// The probe itself, stated as the declaration it now reads: claude/settings' `env` is not a
// wholesale table at the host, and every MCP/LSP table the shipped packs regenerate still is
// (TestHostTableKeysUniformAcrossShippedPacks pins those by name).
func TestHostTableKeysDoNotClaimClaudeEnv(t *testing.T) {
	p, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	surfaces, _ := p.SurfacesFor(false)
	seen := false
	for _, s := range surfaces {
		if s.Agent != "claude" || s.Name != "settings" {
			continue
		}
		seen = true
		if keys := hostTableKeys(p, s); contains(keys, "env") {
			t.Errorf("hostTableKeys(claude/settings) = %v — env is asserted leaf by leaf, "+
				"never regenerated in full", keys)
		}
	}
	if !seen {
		t.Fatal("claude/settings was never visited — did the pack drop it?")
	}
}

// THE HOST `own` ARM keeps taking a DECLARED table whole. Its computed slot carries only the
// table layer hostTableKeys kept (hostTableLayer), so the declaration it hands adoption is
// that layer's key set (hostTableInFull) — and a stale server under codex's mcp_servers is
// dropped on the first owned render, exactly as the jail's adoption drops one, rather than
// frozen into the capture as the user's. The overlay pack makes the table NON-empty: an empty
// one claims nothing whatever is declared, so without it this fixture could not tell the
// declaration from its absence.
func TestHostOwnAdoptionStillRegeneratesADeclaredTable(t *testing.T) {
	home, path := codexHostHome(t, "model = \"gpt-5\"\n\n"+
		"[mcp_servers.tavily]\ncommand = \"/bin/tavily\"\n\n"+
		"[mcp_servers.stale]\ncommand = \"/gone\"\n")
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"managed": map[string]any{
		"mcp_servers": map[string]any{"tavily": map[string]any{"command": "/bin/tavily"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	contributor := &packload.Pack{Name: "my-mcp", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "codex/config", Raw: raw},
		},
	}}
	overlays := packoverlay.Collect([]*packload.Pack{codex, contributor}, false, nil)
	results, err := RenderHostPack(codex, home, render.OwnershipOwn, false, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	if r := resultFor(t, results, "codex/config"); r.Action != "rendered" {
		t.Fatalf("codex/config action = %q, want rendered", r.Action)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := tomlx.Decode(data)
	if err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, data)
	}
	servers, _ := decoded["mcp_servers"].(map[string]any)
	if _, resurrected := servers["stale"]; resurrected {
		t.Errorf("mcp_servers = %v — adoption froze a stale entry under a table yolo declares "+
			"it regenerates in full", servers)
	}
	if _, ok := servers["tavily"]; !ok {
		t.Errorf("mcp_servers = %v, want the declared server", servers)
	}
	if decoded["model"] != "gpt-5" {
		t.Errorf("model = %v, want the user's own key adopted", decoded["model"])
	}
}

// codexMCPContributor is a pack contributing the given servers to codex/config's
// `mcp_servers` through a config-overlay, which is what makes the host's table layer for that
// declared table non-empty.
func codexMCPContributor(t *testing.T, servers map[string]any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"managed": map[string]any{"mcp_servers": servers}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "my-mcp", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "codex/config", Raw: raw},
		},
	}}
}

// THE HOST `own` DRY RUN agrees with the write under a declared table. The change predicate
// (hostStatefulWouldChange) runs the writer's own composition, so it must hand adoption the
// SAME in-full declaration the writer does — or, on a first owned apply over a file that is
// already the canonical render of an earlier config, the observe posture folds the stale
// server back in as the user's, reports `unchanged`, and the --assert then deletes it.
//
// Two phases, because the disagreement needs a file whose bytes are exactly what a
// leaf-level adoption would reproduce: phase one renders a config naming tavily AND stale
// into one home and keeps the bytes; phase two puts those bytes in a FRESH home (no capture
// sidecars, so a first migration) under a config naming tavily alone.
func TestHostOwnObserveAgreesWithAssertUnderADeclaredTable(t *testing.T) {
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatal(err)
	}
	tavily := map[string]any{"command": "/bin/tavily"}

	homeA, pathA := codexHostHome(t, "model = \"gpt-5\"\n")
	both := packoverlay.Collect([]*packload.Pack{codex, codexMCPContributor(t, map[string]any{
		"tavily": tavily, "stale": map[string]any{"command": "/gone"},
	})}, false, nil)
	if _, err := RenderHostPack(codex, homeA, render.OwnershipOwn, false, both); err != nil {
		t.Fatalf("phase one RenderHostPack: %v", err)
	}
	canonical, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, derr := tomlx.Decode(canonical); derr != nil {
		t.Fatalf("decode phase one: %v\n%s", derr, canonical)
	} else if servers, _ := decoded["mcp_servers"].(map[string]any); servers["stale"] == nil {
		t.Fatalf("phase one wrote no stale server, so phase two tests nothing:\n%s", canonical)
	}

	homeB, pathB := codexHostHome(t, string(canonical))
	only := packoverlay.Collect([]*packload.Pack{codex,
		codexMCPContributor(t, map[string]any{"tavily": tavily})}, false, nil)

	observed, err := RenderHostPack(codex, homeB, render.OwnershipOwn, true, only)
	if err != nil {
		t.Fatalf("observe RenderHostPack: %v", err)
	}
	obs := resultFor(t, observed, "codex/config")
	if after, _ := os.ReadFile(pathB); string(after) != string(canonical) {
		t.Fatalf("the observe posture wrote the file:\n%s", after)
	}

	if _, err := RenderHostPack(codex, homeB, render.OwnershipOwn, false, only); err != nil {
		t.Fatalf("assert RenderHostPack: %v", err)
	}
	written, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatal(err)
	}
	changed := string(written) != string(canonical)
	if !changed {
		t.Fatalf("the --assert left the file unchanged, so the predicate had nothing to "+
			"disagree with — the stale server should have been dropped:\n%s", written)
	}
	if obs.WouldChange != changed {
		t.Errorf("observe said WouldChange=%v, but the --assert changed the file (%v):\n"+
			"before:\n%s\nafter:\n%s\nthe dry run must hand adoption the writer's in-full "+
			"declaration (hostTableInFull), or it hides a server deletion", obs.WouldChange,
			changed, canonical, written)
	}
}

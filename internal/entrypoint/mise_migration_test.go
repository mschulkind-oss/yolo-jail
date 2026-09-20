package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mise_migration_test.go pins the mise global-config PRISM port
// (docs/reference/config-migration-to-prism.md §4.1). The bespoke in-place editor
// (GenerateMiseConfig) is gone; ConfigureMisePrism composes the surface through
// the engine. The §4.1 guarantee — a stale yolo-written default runtime line no
// longer shadows the baked /bin/<tool> — was delivered by the prism's
// first-migration seed rather than by a special-case scrub.
//
// ⚠ THE NO-PIN HALF OF THAT GUARANTEE IS WITHDRAWN, by the 2026-09-20 adoption ruling
// (docs/design/config-ownership-and-promotion.md §6.3.1): the adoption drop narrows to
// the leaves yolo actually asserted, and on a jail with no YOLO_MISE_TOOLS pin the
// computed [tools] table is present-and-EMPTY — it asserts none. So a line in that
// table is no longer taken to be yolo's own stale output, because nothing this boot
// regenerated can claim it. The scrub still runs for a jail that DOES pin something
// (TestMisePrismInjectedPinLands: the unpinned sibling is dropped), which is where
// yolo has a regenerated table to claim the file's contents against.
//
// What that buys is the loss the same ruling names: `mise use -g neovim` on an
// unpinned jail used to be deleted on the first prism boot, and no longer is.

// newMiseEnv builds a test Env whose Home and Workspace are temp dirs, so the
// prism sidecars land under a throwaway workspace (never the live /workspace).
func newMiseEnv(t *testing.T, vars map[string]string) *Env {
	t.Helper()
	if vars == nil {
		vars = map[string]string{}
	}
	return &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars}
}

// writeMiseConfig seeds a persistent global mise config.toml and returns its path.
func writeMiseConfig(t *testing.T, home, content string) string {
	t.Helper()
	miseDir := filepath.Join(home, ".config", "mise")
	if err := os.MkdirAll(miseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(miseDir, "config.toml")
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestMisePrismFirstMigrationKeepsRuntimesUnderAnEmptyComputedTable is the §4.1 case
// AFTER the 2026-09-20 ruling, and it is the same fixture with the opposite verdict —
// kept as one test so the reversal is legible rather than looking like a deleted
// guarantee.
//
// An existing jail's config.toml carries the old yolo-written runtime lines
// (node/python/go) and yolo pins NOTHING this boot, so the computed [tools] table is
// present-and-empty. The adoption drop used to take the whole table on the strength of
// that presence; it no longer does, because an empty table regenerated no leaf and so
// can claim none of the file's as its own previous output. The lines look stale and
// are indistinguishable — from here — from `mise use -g node@22` typed by hand, which
// is the loss B1 exists to refuse.
//
// ⚠ This is the test to come to if the §4.1 scrub is ever wanted back for an unpinned
// jail: it needs a record of what yolo WROTE, not the shape of what it computes, and
// the first migration is defined by that record being absent.
func TestMisePrismFirstMigrationKeepsRuntimesUnderAnEmptyComputedTable(t *testing.T) {
	e := newMiseEnv(t, nil) // no YOLO_MISE_TOOLS pin -> computed [tools] is {}
	cfg := writeMiseConfig(t, e.Home,
		"[tools]\nnode = \"22\"\npython = \"3.13\"\ngo = \"latest\"\n")

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, cfg))
	for _, kept := range []string{"node =", "python =", "go ="} {
		if !strings.Contains(s, kept) {
			t.Errorf("runtime %q was dropped, but yolo asserted no [tools] leaf this boot "+
				"— an empty computed table may not claim the file's own lines:\n%s", kept, s)
		}
	}
}

// TestMisePrismInjectedPinLands: a runtime pinned via YOLO_MISE_TOOLS is an
// intentional override and rides the COMPUTED layer, so it lands in the render
// at its pinned version even though the pre-existing config carried a different
// (stale) value; an unpinned baked runtime in the same file is still dropped.
func TestMisePrismInjectedPinLands(t *testing.T) {
	e := newMiseEnv(t, map[string]string{"YOLO_MISE_TOOLS": `{"node": "20"}`})
	cfg := writeMiseConfig(t, e.Home, "[tools]\nnode = \"22\"\npython = \"3.13\"\n")

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, cfg))
	if !strings.Contains(s, `node = "20"`) {
		t.Errorf("injected node pin must land at its version via the computed layer:\n%s", s)
	}
	if strings.Contains(s, "python =") {
		t.Errorf("unpinned python should be dropped:\n%s", s)
	}
}

// TestMisePrismInjectedVersionWithDollar is the audit §C regression, preserved
// across the port: an injected mise version containing `$` must be written
// VERBATIM. The prism never runs a regex substitution over the value (the old
// ReplaceAllString hazard is structurally gone), and the TOML codec emits the
// string literally.
func TestMisePrismInjectedVersionWithDollar(t *testing.T) {
	e := newMiseEnv(t, map[string]string{
		"YOLO_MISE_TOOLS": `{"node": "1.2.3-$1-${name}-$"}`,
	})
	cfg := writeMiseConfig(t, e.Home, "[tools]\nnode = \"20\"\n")

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, cfg))
	if !strings.Contains(s, `node = "1.2.3-$1-${name}-$"`) {
		t.Errorf("dollar-version corrupted:\n%s", s)
	}
}

// TestMisePrismUserGlobalToolSurvivesBothBoots pins what the §3.2 accepted cost
// became, AND the steady-state edit-preservation guarantee that always held.
//
// The cost used to be: a hand-added global tool (`mise use -g neovim`, in no yolo
// layer) is dropped on the FIRST prism boot, because with no last_render baseline it
// is indistinguishable from stale generator output. The 2026-09-20 ruling withdraws
// it for this shape — yolo pins nothing here, so the computed [tools] table asserts no
// leaf, and a drop on the strength of an empty table is a deletion with nothing behind
// it. The tool now survives boot 1 by ADOPTION, and boot 2 by capture; the second half
// is unchanged and is still worth pinning beside the first, because the two paths
// reach the same file by different mechanisms and have disagreed before.
func TestMisePrismUserGlobalToolSurvivesBothBoots(t *testing.T) {
	e := newMiseEnv(t, nil)
	cfg := writeMiseConfig(t, e.Home, "[tools]\nneovim = \"nightly\"\n")

	// Boot 1 (first migration): the un-layered user tool is ADOPTED, not dropped.
	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	if s := string(mustRead(t, cfg)); !strings.Contains(s, `neovim = "nightly"`) {
		t.Errorf("first-migration boot dropped the un-layered user tool, but the computed "+
			"[tools] table is empty and asserts nothing:\n%s", s)
	}

	// The user edits it after the migration boot (a genuine in-jail edit).
	if err := os.WriteFile(cfg, []byte("[tools]\nneovim = \"stable\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2 (steady state): the edit is captured into the overlay and survives.
	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	if s := string(mustRead(t, cfg)); !strings.Contains(s, `neovim = "stable"`) {
		t.Errorf("steady-state boot should preserve the edited user tool via the overlay:\n%s", s)
	}
}

// TestMisePrismRetiresWorkspaceTool covers the one bespoke side effect the prism
// does NOT own: stripping a retired agent's token from the WORKSPACE mise.toml
// (never a prism-owned file — migration doc §5.3). The token used here is
// claude's npm spec, which is always in agents.AllMiseRetire (the union is
// unconditional over the registry); an unrelated pin is kept. It was `gemini`
// before that agent was removed — the retire MECHANISM is unchanged, only the
// example token.
func TestMisePrismRetiresWorkspaceTool(t *testing.T) {
	e := newMiseEnv(t, nil)
	writeMiseConfig(t, e.Home, "[tools]\n") // global config exists but is empty

	// Point the retire surgery at a fixture workspace mise.toml.
	prev := workspaceMisePath
	t.Cleanup(func() { workspaceMisePath = prev })
	ws := filepath.Join(t.TempDir(), "mise.toml")
	if err := os.WriteFile(ws, []byte("[tools]\n\"npm:@anthropic-ai/claude-code\" = \"latest\"\nnode = \"24\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceMisePath = ws

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, ws))
	if strings.Contains(got, "claude-code\" =") {
		t.Errorf("retired claude npm token must be stripped from the workspace mise.toml:\n%s", got)
	}
	if !strings.Contains(got, `node = "24"`) {
		t.Errorf("unrelated workspace pin node must be preserved:\n%s", got)
	}
}

// TestMisePrismEmptyRenderStillEmitsToolsTable guards the empty-document trap:
// with no yolo-owned tools the render must STILL emit a [tools] table, so the
// last_render sidecar is non-empty and the stateful engine trusts it (an
// empty-decoding last_render would re-seed every boot and never capture in-jail
// edits — see the ConfigureMisePrism comment). This is the invariant behind
// TestMisePrismUserGlobalToolDroppedThenPreserved's boot-2 preservation.
func TestMisePrismEmptyRenderStillEmitsToolsTable(t *testing.T) {
	e := newMiseEnv(t, nil) // no YOLO_MISE_TOOLS pin
	cfg := writeMiseConfig(t, e.Home, "[tools]\n")

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	if s := string(mustRead(t, cfg)); !strings.Contains(s, "[tools]") {
		t.Errorf("render must emit a [tools] table even with no tools (keeps last_render trusted):\n%q", s)
	}
	// The last_render sidecar must be non-empty (the trust signal).
	if lr := strings.TrimSpace(string(mustRead(t, prismLastRenderPath(e, "mise", "config")))); lr == "" {
		t.Error("last_render sidecar is empty; the stateful engine would re-seed every boot")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mise_migration_test.go pins the mise global-config PRISM port
// (docs/design/config-migration-to-prism.md §4.1). The bespoke in-place editor
// (GenerateMiseConfig) is gone; ConfigureMisePrism composes the surface through
// the engine. The §4.1 guarantee — a stale yolo-written default runtime line no
// longer shadows the baked /bin/<tool> — is now delivered by the prism's
// first-migration seed (an empty-overlay render that DISCARDS the on-disk file,
// staterender.go §3.2), not by a special-case scrub.

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

// TestMisePrismFirstMigrationDropsStaleRuntimes is the §4.1 fix, delivered by the
// prism: an existing jail's config.toml carrying the old yolo-written default
// runtime lines (node/python/go) has them dropped on the first prism boot,
// because the first-migration render composes from the (empty) yolo layers and
// discards the on-disk file — the stale lines are in no layer, so they vanish.
func TestMisePrismFirstMigrationDropsStaleRuntimes(t *testing.T) {
	e := newMiseEnv(t, nil) // no YOLO_MISE_TOOLS pin
	cfg := writeMiseConfig(t, e.Home,
		"[tools]\nnode = \"22\"\npython = \"3.13\"\ngo = \"latest\"\n")

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, cfg))
	for _, stale := range []string{"node =", "python =", "go ="} {
		if strings.Contains(s, stale) {
			t.Errorf("stale baked runtime %q not dropped by the first-migration render:\n%s", stale, s)
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

// TestMisePrismUserGlobalToolDroppedThenPreserved pins the §3.2 accepted cost
// AND the steady-state edit-preservation guarantee. On the FIRST prism boot a
// hand-added global tool (`mise use -g neovim`, in no yolo layer) is dropped —
// with no last_render baseline it is indistinguishable from stale generator
// output. On the SECOND boot, after the user re-adds it, it is captured into the
// overlay and preserved.
func TestMisePrismUserGlobalToolDroppedThenPreserved(t *testing.T) {
	e := newMiseEnv(t, nil)
	cfg := writeMiseConfig(t, e.Home, "[tools]\nneovim = \"nightly\"\n")

	// Boot 1 (first migration): the un-layered user tool is dropped.
	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	if s := string(mustRead(t, cfg)); strings.Contains(s, "neovim") {
		t.Errorf("first-migration boot should drop the un-layered user tool (accepted §3.2 cost):\n%s", s)
	}

	// User re-adds neovim after the migration boot (a genuine in-jail edit).
	if err := os.WriteFile(cfg, []byte("[tools]\nneovim = \"nightly\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2 (steady state): the edit is captured into the overlay and survives.
	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	if s := string(mustRead(t, cfg)); !strings.Contains(s, `neovim = "nightly"`) {
		t.Errorf("steady-state boot should preserve the re-added user tool via the overlay:\n%s", s)
	}
}

// TestMisePrismRetiresWorkspaceTool covers the one bespoke side effect the prism
// does NOT own: stripping a retired agent's token from the WORKSPACE mise.toml
// (never a prism-owned file — migration doc §5.3). `gemini` is always in
// agents.AllMiseRetire (the union is unconditional); an unrelated pin is kept.
func TestMisePrismRetiresWorkspaceTool(t *testing.T) {
	e := newMiseEnv(t, nil)
	writeMiseConfig(t, e.Home, "[tools]\n") // global config exists but is empty

	// Point the retire surgery at a fixture workspace mise.toml.
	prev := workspaceMisePath
	t.Cleanup(func() { workspaceMisePath = prev })
	ws := filepath.Join(t.TempDir(), "mise.toml")
	if err := os.WriteFile(ws, []byte("[tools]\ngemini = \"latest\"\nnode = \"24\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceMisePath = ws

	if err := ConfigureMisePrism(e); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, ws))
	if strings.Contains(got, "gemini =") {
		t.Errorf("retired token gemini must be stripped from the workspace mise.toml:\n%s", got)
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

package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// This file replaces loadagents_test.go, whose subject no longer exists.
//
// That file tested LoadAgents — which read YOLO_AGENTS and had, at various points, a
// default-to-claude fallback. Both are gone: nothing selects agents by name any more, so
// there is no list to parse and no fallback to guard against.
//
// The PROPERTY those tests were protecting does survive, and it is the one worth keeping:
// a jail with nothing to provision must come up clean rather than erroring or writing a
// half-formed file. A user with no packs configured gets exactly that jail, so it is a
// supported state, not an edge case.

// TestGeneratorsTolerateNoPacks: with no packs mounted, every boot generator must succeed
// and produce a usable shell.
//
// The in-jail half matters more than the host half, and for a reason worth stating: the
// host CLI prints what it is doing, so a mistake there is visible. The entrypoint runs at
// boot inside the container with nobody reading its output — a generator that errored on an
// empty pack set would halt the boot (A12) with a message the user has to go digging in the
// startup log to find.
func TestGeneratorsTolerateNoPacks(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_WORKSPACE": filepath.Join(home, "workspace"),
		// No YOLO_PACK_ROOT: nothing mounted, nothing to render.
	})

	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers with no packs: %v", err)
	}
	// No pack declared an install, so no launcher was written. Read the LAUNCHER dir:
	// the shim dir holds blocked-tool shims in a real boot, and those are a different
	// mechanism — counting them here would make this assert something it does not mean.
	entries, err := os.ReadDir(e.LauncherDir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, ent := range entries {
		t.Errorf("no packs but a launcher was written: %s", ent.Name())
	}

	if err := GenerateBashrc(e); err != nil {
		t.Fatalf("GenerateBashrc with no packs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err != nil {
		t.Errorf("no packs but .bashrc was not written: %v", err)
	}
	if got := packAliases(e); got != "" {
		t.Errorf("packAliases = %q, want empty", got)
	}
}

// TestConfigurePackSurfacesWithNoPacksWritesNothing: the render loop over an empty pack
// list must be a clean no-op, not a failure recorded through genStep — which would abort
// the boot for a user whose only mistake was configuring no packs.
func TestConfigurePackSurfacesWithNoPacksWritesNothing(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_WORKSPACE": filepath.Join(home, "workspace"),
	})
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatalf("LoadJailPacks with nothing mounted: %v", err)
	}
	if len(packs) != 0 {
		t.Fatalf("want no packs, got %d", len(packs))
	}
	ConfigurePackSurfaces(e, packs)
	RunPackHooks(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Errorf("an empty pack set must not fail the boot: %v", fails)
	}
}

// TestPackAliasesDerivesFromLaunchFlags pins the alias derivation against the real copilot
// pack. The alias used to be its own AgentSpec.Alias string duplicating the same flags —
// two places to change, so a pack updating one would get a shell alias that silently
// disagreed with the launcher.
func TestPackAliasesDerivesFromLaunchFlags(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	p, err := embeddedPack("copilot")
	if err != nil {
		t.Fatal(err)
	}
	// Mount the copilot pack where LoadJailPacks looks.
	dest := filepath.Join(root, "copilot")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.Root, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "pack.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root})
	got := packAliases(e)
	want := "alias copilot='copilot --yolo --no-auto-update'"
	if got != want {
		t.Errorf("packAliases = %q, want %q", got, want)
	}
}

// TestPackAliasesIncludesAutonomousPostureFlags verifies that packs declaring launch flags
// inside the autonomous posture (like claude's --dangerously-skip-permissions) have their
// shell alias properly generated in .bashrc.
func TestPackAliasesIncludesAutonomousPostureFlags(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	p, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "claude")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.Root, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "pack.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root})
	got := packAliases(e)
	want := "alias claude='claude --dangerously-skip-permissions'"
	if got != want {
		t.Errorf("packAliases = %q, want %q", got, want)
	}
}

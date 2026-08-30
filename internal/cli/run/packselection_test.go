package run

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOnlySelectedEmbeddedPacksAreStaged: the MOUNT is the filter.
//
// The entrypoint renders every pack it finds under YOLO_PACK_ROOT, so an unselected pack
// left in the staged tree gets its surfaces rendered and its hooks run in-jail. A real jail
// showed exactly that: with `packs: ["claude","codex"]`, all six official packs were staged
// and the boot HALTED with eleven read-only-filesystem errors, because the unselected packs'
// config dirs are (correctly) not writable. Loud, but still wrong — "the jail refuses to
// start because of a pack you did not ask for" is the same bug with a better error.
func TestOnlySelectedEmbeddedPacksAreStaged(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-select")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "claude" {
		var names []string
		for _, p := range loaded {
			names = append(names, p.Name)
		}
		t.Fatalf("loaded = %v, want [claude]", names)
	}
	// And nothing else is on disk to be mounted.
	entries, err := os.ReadDir(filepath.Dir(loaded[0].Root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("staged tree holds %v; an unselected pack there would be rendered in-jail",
			names)
	}
}

// TestNoPacksStagesNothing: an empty config must produce a jail with no agent, which is what
// makes the launch warning ("No packs are configured, so this jail has no coding agent")
// true. Six official packs activating unconditionally while that warning printed was a
// contradiction a user could only find by looking in ~/.yolo/bin/block.
func TestNoPacksStagesNothing(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, briefings, err := o.stagePacks("yolo-test-none")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("want no packs, got %d", len(loaded))
	}
	if len(briefings) != 0 {
		t.Errorf("want no briefings, got %d", len(briefings))
	}
}

// TestDroppingAPackUnstagesIt: the staging root persists across launches, so a pack removed
// from config must stop being mounted. A leftover tree would keep rendering as if it were
// still selected — the deactivation would appear to do nothing.
func TestDroppingAPackUnstagesIt(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "codex"]`)

	o := &Options{Workspace: t.TempDir()}
	if _, loaded, _, err := o.stagePacks("yolo-test-drop"); err != nil {
		t.Fatalf("stagePacks: %v", err)
	} else if len(loaded) != 2 {
		t.Fatalf("first pass: want 2 packs, got %d", len(loaded))
	}

	writeUserPacks(t, home, `["claude"]`)
	_, loaded, _, err := o.stagePacks("yolo-test-drop")
	if err != nil {
		t.Fatalf("stagePacks after drop: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "claude" {
		t.Fatalf("after dropping codex: want [claude], got %d packs", len(loaded))
	}
	entries, err := os.ReadDir(filepath.Dir(loaded[0].Root))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "codex" {
			t.Error("codex tree survived being dropped from config; it would still render")
		}
	}
}

// packHome points HOME at a temp dir so the test drives its own user config, and returns it.
//
// These are UNIT tests: nothing here launches a container, so the image cache is not in
// play and a plain temp HOME is safe (the integration suite's packHome re-links the real
// store for exactly that reason, which does not apply here).
func packHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// writeUserPacks writes a user config whose only key is `packs`.
func writeUserPacks(t *testing.T, home, packsJSON string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "{\n  \"packs\": " + packsJSON + "\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

package config

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// writeWSConfig writes a yolo-jail.jsonc into a workspace dir.
func writeWSConfig(t *testing.T, ws, content string) {
	t.Helper()
	mustWrite(t, ws+"/"+WorkspaceConfigName, content)
}

// The baseline round-trips and an unedited workspace reports NO drift.
func TestWorkspaceDriftInSync(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packs":["claude"],"resources":{"pids_limit":4096}}`)

	wsCfg, err := LoadWorkspaceConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceBootBaseline(ws, wsCfg); err != nil {
		t.Fatal(err)
	}

	diff, hasDrift, ok, err := WorkspaceConfigDrift(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("baseline was written, so ok must be true")
	}
	if hasDrift {
		t.Errorf("an unedited workspace must not drift; got diff:\n%s", strings.Join(diff, "\n"))
	}
}

// Editing the workspace config after the baseline is frozen shows drift, and the
// diff names the changed value.
func TestWorkspaceDriftDetectsEdit(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packs":["claude"],"resources":{"pids_limit":4096}}`)
	wsCfg, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	if err := WriteWorkspaceBootBaseline(ws, wsCfg); err != nil {
		t.Fatal(err)
	}

	// Human edits the config (adds a pack, bumps a limit) after boot.
	writeWSConfig(t, ws, `{"packs":["claude","codex"],"resources":{"pids_limit":8192}}`)

	diff, hasDrift, ok, err := WorkspaceConfigDrift(ws)
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if !hasDrift {
		t.Fatal("an edited workspace config must drift")
	}
	joined := strings.Join(diff, "\n")
	// The new value must appear on a + line and the old on a - line.
	if !strings.Contains(joined, "codex") {
		t.Errorf("diff should show the added pack:\n%s", joined)
	}
	if !strings.Contains(joined, "8192") || !strings.Contains(joined, "4096") {
		t.Errorf("diff should show both the new and old limit:\n%s", joined)
	}
}

// No baseline → ok=false (cannot determine), distinct from hasDrift=false (in sync).
// A pre-feature jail must not read as "no drift".
func TestWorkspaceDriftNoBaseline(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packs":["claude"]}`)
	diff, hasDrift, ok, err := WorkspaceConfigDrift(ws)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("with no baseline, ok must be false (cannot determine)")
	}
	if hasDrift || diff != nil {
		t.Errorf("no baseline must not fabricate drift: hasDrift=%v diff=%v", hasDrift, diff)
	}
}

// Drift compares the CANONICAL form, so reformatting/reordering the source file is
// not drift — only a real value change is. This is what keeps the command from
// nagging about cosmetic edits.
func TestWorkspaceDriftIgnoresCosmeticReorder(t *testing.T) {
	ws := t.TempDir()
	writeWSConfig(t, ws, `{"packs":["claude"],"resources":{"pids_limit":4096}}`)
	wsCfg, _ := LoadWorkspaceConfig(ws, false, func(string) {})
	if err := WriteWorkspaceBootBaseline(ws, wsCfg); err != nil {
		t.Fatal(err)
	}

	// Same values, keys reordered + whitespace + a comment (jsonc). Object-key order
	// is not semantic, so the canonical form is identical → no drift.
	writeWSConfig(t, ws, "{\n  // reordered, reformatted\n  \"resources\":  { \"pids_limit\": 4096 },\n  \"packs\": [\"claude\"]\n}\n")

	_, hasDrift, ok, err := WorkspaceConfigDrift(ws)
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if hasDrift {
		t.Error("reordering keys / reformatting must NOT count as drift (canonical compare)")
	}
}

// The baseline is workspace-ONLY: WriteWorkspaceBootBaseline serializes exactly what
// it is handed, and the path is distinct from the merged snapshot, so the two never
// clobber each other.
func TestBootBaselineIsDistinctFromSnapshot(t *testing.T) {
	ws := t.TempDir()
	if WorkspaceConfigBootPath(ws) == ConfigSnapshotPath(ws) {
		t.Fatal("boot baseline and merged snapshot must be different files")
	}
	m := jsonx.NewOrderedMap()
	m.Set("packs", []any{"claude"})
	if err := WriteWorkspaceBootBaseline(ws, m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(WorkspaceConfigBootPath(ws)); err != nil {
		t.Errorf("baseline not written: %v", err)
	}
	// The merged snapshot must NOT have been written by the baseline call.
	if _, err := os.Stat(ConfigSnapshotPath(ws)); !os.IsNotExist(err) {
		t.Errorf("writing the boot baseline must not touch config-snapshot.json (err=%v)", err)
	}
}

package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// reconcileSurface is an RMW surface with one reconciled dynamic table, the shape
// ~/.claude.json has.
func reconcileSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "example", Name: "config", Path: "~/.example.json", Codec: "json",
		Mode:     manifest.ModeRMW,
		Computed: []manifest.Computed{{From: manifest.SourceMCPServers, To: "servers", Reconcile: true}},
	}
}

func readServers(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	m, _ := d["servers"].(map[string]any)
	return m
}

func tables(names ...string) map[string]map[string]any {
	t := map[string]any{}
	for _, n := range names {
		t[n] = map[string]any{"command": n}
	}
	return map[string]map[string]any{manifest.SourceMCPServers: t}
}

// TestReconcileRemovesADroppedEntry is the property plain RMW could NOT express, and the
// reason the mechanism exists: with no record of what yolo asserted last boot, "the agent
// added this" and "yolo added it and config has since dropped it" look identical on disk.
// The sidecar is that record.
func TestReconcileRemovesADroppedEntry(t *testing.T) {
	e := modeEnv(t)
	s := reconcileSurface()
	path := expandHomePath(e, s.Path)

	if err := renderSurfaceRMWSurface(e, s, tables("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	if got := readServers(t, path); len(got) != 2 {
		t.Fatalf("first render: want 2 servers, got %v", got)
	}
	// beta dropped from config.
	if err := renderSurfaceRMWSurface(e, s, tables("alpha")); err != nil {
		t.Fatal(err)
	}
	got := readServers(t, path)
	if _, present := got["beta"]; present {
		t.Errorf("a server dropped from config must disappear, got %v", got)
	}
	if _, present := got["alpha"]; !present {
		t.Errorf("alpha should remain: %v", got)
	}
}

// TestReconcilePreservesAnAgentAddedEntry is the flip side, and the one that makes the
// removal above safe. Verified live in a nested jail too, but pinned here because it is the
// property whose loss is silent data destruction: an agent's own MCP server vanishing on the
// next boot.
func TestReconcilePreservesAnAgentAddedEntry(t *testing.T) {
	e := modeEnv(t)
	s := reconcileSurface()
	path := expandHomePath(e, s.Path)

	if err := renderSurfaceRMWSurface(e, s, tables("alpha")); err != nil {
		t.Fatal(err)
	}
	// The agent adds one itself.
	var doc map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["servers"].(map[string]any)["agent-added"] = map[string]any{"command": "mine"}
	out, _ := json.Marshal(doc)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := renderSurfaceRMWSurface(e, s, tables("alpha")); err != nil {
		t.Fatal(err)
	}
	if _, present := readServers(t, path)["agent-added"]; !present {
		t.Errorf("an entry the agent added must survive: %v", readServers(t, path))
	}
}

// TestReconcileWithNoSidecarOnlyAdds pins the SAFE DIRECTION for a missing record. Without
// the sidecar, removing anything is a guess — and guessing wrong deletes an entry the agent
// owns. A stale entry is a wrong config the user can see and fix; deleted state they cannot.
func TestReconcileWithNoSidecarOnlyAdds(t *testing.T) {
	e := modeEnv(t)
	s := reconcileSurface()
	path := expandHomePath(e, s.Path)

	if err := renderSurfaceRMWSurface(e, s, tables("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	// Lose the record (a wiped .yolo dir, a fresh clone).
	if err := os.Remove(rmwManagedPath(e, s, "servers")); err != nil {
		t.Fatal(err)
	}
	if err := renderSurfaceRMWSurface(e, s, tables("alpha")); err != nil {
		t.Fatal(err)
	}
	got := readServers(t, path)
	if _, present := got["beta"]; !present {
		t.Errorf("with no record, beta must be KEPT rather than guessed away: %v", got)
	}
}

// TestReconcileSidecarStaysOutOfResetsWay: `yolo config reset` removes exactly
// `<agent>-<name>.overlay.json` and `.last_render`. The reconcile record must not match
// either, because resetting it would make yolo forget what it asserted and orphan every
// entry it had added — a leak dressed as a reset.
func TestReconcileSidecarStaysOutOfResetsWay(t *testing.T) {
	e := modeEnv(t)
	s := reconcileSurface()
	got := filepath.Base(rmwManagedPath(e, s, "servers"))
	for _, reserved := range []string{
		filepath.Base(prismOverlayPath(e, s.Agent, s.Name)),
		filepath.Base(prismLastRenderPath(e, s.Agent, s.Name)),
	} {
		if got == reserved {
			t.Errorf("reconcile record %q collides with a reset target", got)
		}
	}
}

// TestReconcileSidecarLivesInAWritableDir is the regression for a failure a real jail found
// and no unit test would have: the record was first written beside the surface file, which
// for a home-root surface (~/.claude.json) is the :ro home itself. The boot halted with
// `read-only file system`.
func TestReconcileSidecarLivesInAWritableDir(t *testing.T) {
	e := modeEnv(t)
	s := reconcileSurface()
	dir := filepath.Dir(rmwManagedPath(e, s, "servers"))
	if dir == e.Home {
		t.Error("the reconcile record must not live in the home root — it is mounted :ro")
	}
	if dir != prismSidecarDir(e) {
		t.Errorf("expected the prism sidecar dir (writable), got %s", dir)
	}
}

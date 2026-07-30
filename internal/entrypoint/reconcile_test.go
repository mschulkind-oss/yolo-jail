package entrypoint

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// managedTableSurface is an RMW surface with one dynamic managed table (the MCP
// block), the shape ~/.claude.json has. yolo OWNS the table and regenerates it
// wholesale each boot (OQ12 (d)) from the derived computed layer — no sidecar, no
// preserve-hand-edits. The dynamic layer arrives as the `computed` arg (a
// {servers: {...}} map), not from a Surface.Computed field.
func managedTableSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "example", Name: "config", Path: "~/.example.json", Codec: "json",
		Mode: manifest.ModeRMW,
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

// computedServers is the derived dynamic layer the RMW render receives: the
// `servers` managed table with one entry per name.
func computedServers(names ...string) map[string]any {
	t := map[string]any{}
	for _, n := range names {
		t[n] = map[string]any{"command": n}
	}
	return map[string]any{"servers": t}
}

// A server dropped from config disappears on the next render — the same
// "regenerate, don't reconcile" behavior every other agent's MCP block has.
func TestManagedTableDropsAServerRemovedFromConfig(t *testing.T) {
	e := modeEnv(t)
	s := managedTableSurface()
	path := expandHomePath(e, s.Path)

	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	if got := readServers(t, path); len(got) != 2 {
		t.Fatalf("first render: want 2 servers, got %v", got)
	}
	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha")); err != nil {
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

// The behavior CHANGE from the old reconcile mechanism (OQ12 (d)): a server the
// agent added through its own UI is NOT preserved — yolo owns the mcpServers block
// and regenerates it from config. This is intentional: config is the source of
// truth, and a user-scope server added via the agent belongs in yolo's config
// (where it reaches every agent), not hand-poked into one agent's file.
func TestManagedTableOverwritesAnAgentAddedEntry(t *testing.T) {
	e := modeEnv(t)
	s := managedTableSurface()
	path := expandHomePath(e, s.Path)

	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha")); err != nil {
		t.Fatal(err)
	}
	// The agent adds one itself, at the top-level managed block.
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

	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha")); err != nil {
		t.Fatal(err)
	}
	if _, present := readServers(t, path)["agent-added"]; present {
		t.Errorf("a hand-added server must be overwritten (yolo owns the block): %v", readServers(t, path))
	}
}

// Dropping a hand-added (or config-removed) entry is ANNOUNCED, not silent — the
// visible-drop requirement of OQ12 (d). The notice names the entry and points at
// mcp_servers.
func TestManagedTableAnnouncesADrop(t *testing.T) {
	e := modeEnv(t)
	var errbuf bytes.Buffer
	e.Stderr = &errbuf
	s := managedTableSurface()
	path := expandHomePath(e, s.Path)

	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	errbuf.Reset() // ignore the first render's output; beta exists now
	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha")); err != nil {
		t.Fatal(err)
	}
	out := errbuf.String()
	if !strings.Contains(out, "beta") {
		t.Errorf("dropping beta must be announced, got: %q", out)
	}
	if !strings.Contains(out, "mcp_servers") {
		t.Errorf("the drop notice must point at mcp_servers, got: %q", out)
	}
	_ = path
}

// No drop, no noise: a render that removes nothing must be quiet.
func TestManagedTableQuietWhenNothingDropped(t *testing.T) {
	e := modeEnv(t)
	var errbuf bytes.Buffer
	e.Stderr = &errbuf
	s := managedTableSurface()

	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha")); err != nil {
		t.Fatal(err)
	}
	errbuf.Reset()
	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha")); err != nil {
		t.Fatal(err)
	}
	if errbuf.Len() != 0 {
		t.Errorf("a render that drops nothing must be silent, got: %q", errbuf.String())
	}
}

// No sidecar is created any more — the whole stateful mechanism is gone.
func TestManagedTableWritesNoSidecar(t *testing.T) {
	e := modeEnv(t)
	s := managedTableSurface()
	if err := renderSurfaceRMWSurface(e, s, computedServers("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(prismSidecarDir(e))
	if err != nil {
		if os.IsNotExist(err) {
			return // no sidecar dir at all — fine
		}
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.Contains(ent.Name(), "managed") {
			t.Errorf("no managed-name sidecar should be written any more, found %s", ent.Name())
		}
	}
}

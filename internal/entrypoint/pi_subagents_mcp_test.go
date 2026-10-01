package entrypoint

// pi_subagents_mcp_test.go pins the maintainer's ruling of 2026-09-28 on pi-subagents' MCP file
// (docs/design/agent-directory-map.md, AM-R2), end to end through the boot's own loop
// (ConfigurePackSurfaces) over the SHIPPED pi pack, so deleting either the pack's declaration or
// the render's call site fails a test here: while pi-subagents is in pi's `packages`, the
// configured MCP servers are also rendered into `~/.config/mcp/mcp.json`, the file pi-subagents
// reads; without it, nothing is written there, and a file the user keeps there is merged into,
// never replaced.
//
// AM-R1's half, the `~/.pi/agent/mcp.json` a v0.10.0 boot wrote, moved to pi_mcp_test.go when
// that file became the pi/mcp surface's own render.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const (
	subagentsMCPRel = ".config/mcp/mcp.json"
	probeMCPServers = `{"probe-mcp":{"command":"/bin/probe-mcp","args":["--stdio"]}}`
)

// probeMCPTable is probeMCPServers decoded, the value every render below should carry.
func probeMCPTable() map[string]any {
	return map[string]any{"probe-mcp": map[string]any{
		"command": "/bin/probe-mcp", "args": []any{"--stdio"},
	}}
}

// piMCPEnv is a boot Env over a fresh home. The /ctx root is pointed at an empty directory:
// pi/settings reads a host layer, and in a jail /ctx/host-pi holds the real host's
// settings.json, whose `packages` would select pi-subagents for every test here.
func piMCPEnv(t *testing.T) (*Env, string) {
	t.Helper()
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	return &Env{
		Home:      home,
		Workspace: t.TempDir(),
		Vars:      map[string]string{"YOLO_MCP_SERVERS": probeMCPServers},
	}, home
}

func shippedPi(t *testing.T) *packload.Pack {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatalf("embedded pi: %v", err)
	}
	return pi
}

// subagentsPack is the maintainer's personal pack in miniature: it adds a FORK of
// pi-subagents to pi's packages, which is the spelling this jail's settings.json carries.
func subagentsPack(t *testing.T) *packload.Pack {
	return listContributorPack(t, "personal", "pi/settings", "/packages",
		"git:github.com/mschulkind/pi-subagents")
}

func plant(t *testing.T, home, rel, content string) string {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ── AM-R2: the subagents file, while pi-subagents is selected ──────────────────────────────

func TestPiSubagentsSelectedRendersTheConfiguredServersIntoTheSharedMCPFile(t *testing.T) {
	e, home := piMCPEnv(t)
	bootJail(t, e, shippedPi(t), subagentsPack(t))
	got := readRenderedJSON(t, home, subagentsMCPRel)
	if want := map[string]any{"mcpServers": probeMCPTable()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("~/.config/mcp/mcp.json = %#v, want %#v — pi-subagents reads this file "+
			"(getConfigPaths)", got, want)
	}
}

// The npm spelling, a pinned version and a package-filter OBJECT all select it; a package
// whose name merely starts with pi-subagents does not.
func TestPiSubagentsSelectionMatchesEverySpellingOfThePackage(t *testing.T) {
	for _, tc := range []struct {
		entry any
		want  bool
	}{
		{"npm:pi-subagents", true},
		{"npm:pi-subagents@0.35.1", true},
		{"git:github.com/nicobailon/pi-subagents@v0.35.1", true},
		{"https://github.com/nicobailon/pi-subagents.git", true},
		{map[string]any{"source": "npm:pi-subagents", "extensions": []any{}}, true},
		{"npm:pi-subagents-extra", false},
		{"npm:pi-mcp-adapter", false},
	} {
		e, home := piMCPEnv(t)
		bootJail(t, e, shippedPi(t), listContributorPack(t, "personal", "pi/settings",
			"/packages", tc.entry))
		_, err := os.Stat(filepath.Join(home, subagentsMCPRel))
		if got := err == nil; got != tc.want {
			t.Errorf("packages [%v]: ~/.config/mcp/mcp.json written = %v, want %v", tc.entry, got, tc.want)
		}
	}
}

func TestPiSubagentsNotSelectedWritesNothing(t *testing.T) {
	e, home := piMCPEnv(t)
	bootJail(t, e, shippedPi(t))
	if _, err := os.Stat(filepath.Join(home, subagentsMCPRel)); !os.IsNotExist(err) {
		t.Fatalf("~/.config/mcp/mcp.json exists without pi-subagents in pi's packages "+
			"(stat err %v): the surface must render only while the extension is selected", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "mcp")); !os.IsNotExist(err) {
		t.Errorf("an unselected subagents surface still created its directory (stat err %v)", err)
	}
}

// ~/.config/mcp/mcp.json is a cross-tool location (pi-mcp-adapter reads it too, and its setup
// panel's "Add globally" writes it), so a file already there is MERGED: its servers and its
// other keys survive beside yolo's, across boots, and an edit made after the render survives
// the next boot.
func TestPiSubagentsMergesIntoAFileTheUserKeepsThere(t *testing.T) {
	e, home := piMCPEnv(t)
	plant(t, home, subagentsMCPRel,
		`{"mcpServers":{"mine":{"url":"https://example.invalid/mcp"}},"settings":{"toolPrefix":"short"}}`)
	bootJail(t, e, shippedPi(t), subagentsPack(t))

	want := map[string]any{
		"mcpServers": map[string]any{
			"mine":      map[string]any{"url": "https://example.invalid/mcp"},
			"probe-mcp": probeMCPTable()["probe-mcp"],
		},
		"settings": map[string]any{"toolPrefix": "short"},
	}
	if got := readRenderedJSON(t, home, subagentsMCPRel); !reflect.DeepEqual(got, want) {
		t.Fatalf("first boot over the user's file:\n got %#v\nwant %#v", got, want)
	}

	// An edit after the render, the way pi-mcp-adapter's "Add globally" makes one.
	m := readRenderedJSON(t, home, subagentsMCPRel)
	m["mcpServers"].(map[string]any)["added"] = map[string]any{"command": "added"}
	data, _ := json.Marshal(m)
	plant(t, home, subagentsMCPRel, string(data))
	e2 := &Env{Home: home, Workspace: e.Workspace, Vars: e.Vars}
	bootJail(t, e2, shippedPi(t), subagentsPack(t))
	want["mcpServers"].(map[string]any)["added"] = map[string]any{"command": "added"}
	if got := readRenderedJSON(t, home, subagentsMCPRel); !reflect.DeepEqual(got, want) {
		t.Fatalf("second boot after an in-jail edit:\n got %#v\nwant %#v", got, want)
	}
}

// Deselecting the extension leaves no stale copy of yolo's servers behind for pi-mcp-adapter
// to keep loading: the file is removed when it is still exactly yolo's render with no edit of
// anyone else's in it, and kept, untouched, when it carries one.
func TestPiSubagentsDeselectedRemovesOnlyYolosOwnCopy(t *testing.T) {
	t.Run("unedited", func(t *testing.T) {
		e, home := piMCPEnv(t)
		bootJail(t, e, shippedPi(t), subagentsPack(t))
		readRenderedJSON(t, home, subagentsMCPRel) // rendered while selected
		bootJail(t, &Env{Home: home, Workspace: e.Workspace, Vars: e.Vars}, shippedPi(t))
		if _, err := os.Stat(filepath.Join(home, subagentsMCPRel)); !os.IsNotExist(err) {
			t.Fatalf("yolo's own unedited render survived the deselect (stat err %v)", err)
		}
	})
	t.Run("user file merged into", func(t *testing.T) {
		e, home := piMCPEnv(t)
		plant(t, home, subagentsMCPRel, `{"mcpServers":{"mine":{"command":"mine"}}}`)
		bootJail(t, e, shippedPi(t), subagentsPack(t))
		before, _ := os.ReadFile(filepath.Join(home, subagentsMCPRel))
		bootJail(t, &Env{Home: home, Workspace: e.Workspace, Vars: e.Vars}, shippedPi(t))
		after, err := os.ReadFile(filepath.Join(home, subagentsMCPRel))
		if err != nil {
			t.Fatalf("a file holding the user's own server was deleted at deselect: %v", err)
		}
		if string(after) != string(before) {
			t.Fatalf("the deselect rewrote the file:\n got %s\nwant %s", after, before)
		}
	})
}

// ── the host notch ─────────────────────────────────────────────────────────────────────────

// pi/subagents-mcp is declared `notAtHost`, so even now that `yolo host apply` runs the derives
// (docs/reference/host-agent-environment.md OQ-HC1) this surface is never rendered there, and the
// user's real ~/.config/mcp/mcp.json must come through an apply exactly as it went in, under
// either contract, with pi-subagents in the host's own pi settings.
func TestPiSubagentsHostApplyLeavesTheUsersSharedFileAlone(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			home := t.TempDir()
			plant(t, home, ".pi/agent/settings.json", `{"packages":["npm:pi-subagents"]}`)
			const user = `{"mcpServers":{"mine":{"command":"mine"}},"settings":{"toolPrefix":"short"}}`
			path := plant(t, home, subagentsMCPRel, user)
			results := applyHostPacks(t, home, ownership, false, shippedPi(t))
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("host apply removed the user's ~/.config/mcp/mcp.json: %v", err)
			}
			if string(got) != user {
				t.Fatalf("host apply rewrote the user's ~/.config/mcp/mcp.json:\n got %s\nwant %s",
					got, user)
			}
			r, ok := listResultFor(results, "pi/subagents-mcp")
			if !ok {
				t.Fatalf("host apply reported nothing for pi/subagents-mcp: %+v", results)
			}
			t.Logf("host apply under %s: %s", ownership, r.Action)
		})
	}
}

package entrypoint

// pi_mcp_test.go pins where pi's MCP servers go, end to end through the boot's own loop
// (ConfigurePackSurfaces) and the host's (RenderHostPack) over the SHIPPED pi pack, so deleting
// the pack's declaration, its derive or either call site fails a test here.
//
// pi has had its own MCP client since 0.99.0. It reads ~/.pi/agent/mcp.json always, and a
// project's .pi/mcp.json only while the project is trusted (pi 0.99.2,
// dist/extensions/mcp/config.js, loadMcpConfig). yolo used to write its table to
// mcp-adapter.json, pi-mcp-adapter's file, which pi's own client never reads, so pi started
// none of yolo's servers unless the user had installed the adapter. The pi/mcp surface now
// renders mcp.json:
//
//   - `stateful`, not `computed`: pi writes the file too (`pi mcp add`, /mcp's enable and
//     exposure changes), so an edit is captured rather than discarded at the next boot;
//   - not declared in full (CO13): a file already there is adopted, its servers the user's;
//   - yolo's old copy in mcp-adapter.json is retired while it holds exactly this render, and
//     kept otherwise (retireIfMatchesRender, the AM-R1 rule);
//   - at the host the table is asserted per key, so a server you added stays.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const (
	piMCPRel     = ".pi/agent/mcp.json"
	piAdapterRel = ".pi/agent/mcp-adapter.json"
)

// rebootPi boots the shipped pi pack again over the same home and workspace, with servers as
// the configured MCP table.
func rebootPi(t *testing.T, e *Env, servers string) {
	t.Helper()
	bootJail(t, &Env{Home: e.Home, Workspace: e.Workspace,
		Vars: map[string]string{"YOLO_MCP_SERVERS": servers}}, shippedPi(t))
}

// A declared server lands in ~/.pi/agent/mcp.json, the file pi's own client reads, and nothing
// is written for pi-mcp-adapter.
func TestPiMcpRendersDeclaredServersIntoPisOwnFile(t *testing.T) {
	e, home := piMCPEnv(t)
	bootJail(t, e, shippedPi(t))

	got := readRenderedJSON(t, home, piMCPRel)
	if want := map[string]any{"mcpServers": probeMCPTable()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("~/.pi/agent/mcp.json = %#v, want %#v — pi's own MCP client reads only "+
			"this file (and a trusted project's .pi/mcp.json)", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, piAdapterRel)); !os.IsNotExist(err) {
		t.Fatalf("the boot wrote pi-mcp-adapter's mcp-adapter.json (stat err %v): pi's own "+
			"client serves the table from mcp.json, and the adapter would start every "+
			"server a second time", err)
	}
}

// yolo's own copy in mcp-adapter.json — the file 0.11.0 moved the render to — goes once the new
// render holds the same table. Formatting does not decide it: the planted copy orders and
// indents its keys differently from today's encoder. With no server configured, both renders
// are `{"mcpServers": {}}`, and that copy goes too.
func TestPiMcpRetiresYolosAdapterCopy(t *testing.T) {
	for name, tc := range map[string]struct{ servers, adapter string }{
		"servers": {probeMCPServers,
			"{\n    \"mcpServers\": {\n        \"probe-mcp\": {\n            \"args\": [\"--stdio\"],\n" +
				"            \"command\": \"/bin/probe-mcp\"\n        }\n    }\n}\n"},
		"no servers": {`{}`, `{"mcpServers": {}}`},
	} {
		t.Run(name, func(t *testing.T) {
			e, home := piMCPEnv(t)
			e.Vars["YOLO_MCP_SERVERS"] = tc.servers
			adapter := plant(t, home, piAdapterRel, tc.adapter)
			bootJail(t, e, shippedPi(t))
			if _, err := os.Stat(adapter); !os.IsNotExist(err) {
				t.Fatalf("mcp-adapter.json holds exactly the pi/mcp render (what 0.11 wrote "+
					"there) and survived the boot (stat err %v): the pi pack's mcp surface must "+
					"declare it under retireIfMatchesRender, and the boot must run that retire", err)
			}
			readRenderedJSON(t, home, piMCPRel)
		})
	}
}

// Anything but yolo's exact render stays in mcp-adapter.json, byte for byte: a changed server,
// an added one, a key yolo never writes (`settings`, pi-mcp-adapter's own), or a file that is
// not JSON. That file is the adapter user's.
func TestPiMcpKeepsAnAdapterFileThatIsNotYolosRender(t *testing.T) {
	for name, content := range map[string]string{
		"extra key":      `{"mcpServers":{"probe-mcp":{"command":"/bin/probe-mcp","args":["--stdio"]}},"settings":{"directTools":true}}`,
		"changed server": `{"mcpServers":{"probe-mcp":{"command":"/bin/probe-mcp","args":["--http"]}}}`,
		"extra server":   `{"mcpServers":{"probe-mcp":{"command":"/bin/probe-mcp","args":["--stdio"]},"mine":{"command":"mine"}}}`,
		"not json":       `{"mcpServers": `,
	} {
		t.Run(name, func(t *testing.T) {
			e, home := piMCPEnv(t)
			path := plant(t, home, piAdapterRel, content)
			bootJail(t, e, shippedPi(t))
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the boot deleted an mcp-adapter.json that is not yolo's render: %v", err)
			}
			if string(got) != content {
				t.Fatalf("the boot rewrote mcp-adapter.json:\n got %s\nwant %s", got, content)
			}
		})
	}
}

// YOUR OWN ENTRIES STAY. A server and a top-level setting already in mcp.json are adopted as
// yours on the first boot; a server added later and a server disabled later — the edits
// `pi mcp add` and /mcp make — survive the next boot; and a server yolo stops configuring
// leaves, while yours stay.
func TestPiMcpKeepsTheUsersOwnEntries(t *testing.T) {
	e, home := piMCPEnv(t)
	plant(t, home, piMCPRel, `{"mcpServers":{"mine":{"command":"mine"}},"autoEnableCodemode":false}`)
	bootJail(t, e, shippedPi(t))
	want := map[string]any{
		"autoEnableCodemode": false,
		"mcpServers": map[string]any{
			"mine":      map[string]any{"command": "mine"},
			"probe-mcp": probeMCPTable()["probe-mcp"],
		},
	}
	if got := readRenderedJSON(t, home, piMCPRel); !reflect.DeepEqual(got, want) {
		t.Fatalf("first boot over your mcp.json:\n got %#v\nwant %#v", got, want)
	}

	// The two edits pi itself makes to this file.
	m := readRenderedJSON(t, home, piMCPRel)
	servers := m["mcpServers"].(map[string]any)
	servers["added"] = map[string]any{"command": "added"}
	servers["mine"].(map[string]any)["enabled"] = false
	data, _ := json.Marshal(m)
	plant(t, home, piMCPRel, string(data))
	rebootPi(t, e, probeMCPServers)
	wantServers := want["mcpServers"].(map[string]any)
	wantServers["added"] = map[string]any{"command": "added"}
	wantServers["mine"] = map[string]any{"command": "mine", "enabled": false}
	if got := readRenderedJSON(t, home, piMCPRel); !reflect.DeepEqual(got, want) {
		t.Fatalf("second boot after `pi mcp add` and /mcp disable:\n got %#v\nwant %#v", got, want)
	}

	rebootPi(t, e, `{}`)
	delete(wantServers, "probe-mcp")
	if got := readRenderedJSON(t, home, piMCPRel); !reflect.DeepEqual(got, want) {
		t.Fatalf("a boot with probe-mcp dropped from the config:\n got %#v\nwant %#v", got, want)
	}
}

// /mcp disable on one of yolo's own servers is an edit too, and the next boot keeps it: the
// server stays declared, and stays off.
func TestPiMcpKeepsADisabledYoloServerDisabled(t *testing.T) {
	e, home := piMCPEnv(t)
	bootJail(t, e, shippedPi(t))
	m := readRenderedJSON(t, home, piMCPRel)
	m["mcpServers"].(map[string]any)["probe-mcp"].(map[string]any)["enabled"] = false
	data, _ := json.Marshal(m)
	plant(t, home, piMCPRel, string(data))
	rebootPi(t, e, probeMCPServers)
	got := readRenderedJSON(t, home, piMCPRel)["mcpServers"].(map[string]any)["probe-mcp"]
	want := map[string]any{"command": "/bin/probe-mcp", "args": []any{"--stdio"}, "enabled": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("probe-mcp after /mcp disable and a boot = %#v, want %#v", got, want)
	}
}

// The copy yolo 0.10.0 wrote to mcp.json holds exactly what pi/mcp renders, so it is adopted as
// yolo's own and nothing in it becomes yours: the server leaves when the config drops it. AM-R1
// retired that copy by deleting it; the surface now renders over it instead.
func TestPiMcpTakesTheV0100CopyAsYolos(t *testing.T) {
	e, home := piMCPEnv(t)
	plant(t, home, piMCPRel, `{"mcpServers":{"probe-mcp":{"args":["--stdio"],"command":"/bin/probe-mcp"}}}`)
	bootJail(t, e, shippedPi(t))
	rebootPi(t, e, `{}`)
	got := readRenderedJSON(t, home, piMCPRel)
	if want := map[string]any{"mcpServers": map[string]any{}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the 0.10.0 copy's server outlived its removal from the config, so the first "+
			"boot adopted it as the user's: %#v", got)
	}
}

// THE TRUST BEHAVIOR, which needs no code of yolo's beyond writing the right file. pi trusts its
// global mcp.json unconditionally and gates only a project's .pi/mcp.json (pi 0.99.2,
// loadMcpConfig's projectTrusted). So yolo's servers go to the global file and none to the
// project, and a repository's own .pi/mcp.json follows the pi pack's project-trust posture:
// `always` in a jail, which is the confinement boundary (docs/design/workspace-mcp-sources.md
// §1), and `ask` at the host.
func TestPiMcpTrust(t *testing.T) {
	t.Run("jail", func(t *testing.T) {
		e, home := piMCPEnv(t)
		const repo = `{"mcpServers":{"repo-server":{"command":"repo-mcp"}}}`
		project := plant(t, e.Workspace, ".pi/mcp.json", repo)
		bootJail(t, e, shippedPi(t))
		if got, err := os.ReadFile(project); err != nil || string(got) != repo {
			t.Fatalf("the boot touched the repository's own .pi/mcp.json (err %v):\n%s", err, got)
		}
		if _, has := readRenderedJSON(t, home, piMCPRel)["mcpServers"].(map[string]any)["probe-mcp"]; !has {
			t.Fatalf("yolo's server is not in the global ~/.pi/agent/mcp.json, the one file pi " +
				"loads whatever the project's trust")
		}
		if got := readRenderedJSON(t, home, ".pi/agent/settings.json")["defaultProjectTrust"]; got != "always" {
			t.Fatalf("pi's defaultProjectTrust in a jail = %v, want always, so the repository's "+
				"own .pi/mcp.json loads without a prompt", got)
		}
	})
	t.Run("host", func(t *testing.T) {
		t.Setenv("YOLO_CTX_ROOT", t.TempDir())
		home := t.TempDir()
		in := hostTestInputs(t, testPacksForAgent(t, "pi"), nil, tavily(), nil)
		if r := hostRenderWith(t, home, render.OwnershipAssert, in, "pi", "pi/mcp"); r.Action != "rendered" {
			t.Fatalf("pi/mcp at the host: %q", r.Action)
		}
		jsonAt(t, home, piMCPRel, "mcpServers", "tavily", "command")
		if got := jsonAt(t, home, ".pi/agent/settings.json", "defaultProjectTrust"); got != "ask" {
			t.Fatalf("pi's defaultProjectTrust at the host = %v, want ask: a project's own "+
				".pi/mcp.json is asked about there, not trusted", got)
		}
	})
}

// AT THE HOST YOUR OWN ENTRIES STAY TOO, under both contracts: pi/mcp is not declared in full, so
// `assert` writes yolo's servers beside yours rather than replacing the table, and `own`'s first
// render adopts the file (with its one-time archive). host apply deletes nothing, so a real
// mcp-adapter.json is left exactly as it was.
func TestPiMcpHostApplyKeepsYourOwnEntries(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			t.Setenv("YOLO_CTX_ROOT", t.TempDir())
			home := t.TempDir()
			plant(t, home, piMCPRel, `{"mcpServers":{"mine":{"command":"mine"}},"autoEnableCodemode":false}`)
			const adapter = `{"mcpServers":{"tavily":{"command":"npx","args":["-y","tavily-mcp"]}}}`
			adapterPath := plant(t, home, piAdapterRel, adapter)
			in := hostTestInputs(t, testPacksForAgent(t, "pi"), nil, tavily(), nil)
			r := hostRenderWith(t, home, ownership, in, "pi", "pi/mcp")
			if r.Action != "rendered" {
				t.Fatalf("pi/mcp at the host: %q", r.Action)
			}
			if len(r.EntryLosses) != 0 {
				t.Errorf("host apply names a loss in your mcp.json: %v", r.EntryLosses)
			}
			if got := jsonAt(t, home, piMCPRel, "mcpServers", "mine", "command"); got != "mine" {
				t.Errorf("your own server in mcp.json was changed: %v", got)
			}
			if got := jsonAt(t, home, piMCPRel, "autoEnableCodemode"); got != false {
				t.Errorf("your autoEnableCodemode was changed: %v", got)
			}
			jsonAt(t, home, piMCPRel, "mcpServers", "tavily", "command")
			if ownership == render.OwnershipOwn && r.Archived == "" {
				t.Errorf("the first owned render adopted your mcp.json without its one-time archive")
			}
			if got, err := os.ReadFile(adapterPath); err != nil || string(got) != adapter {
				t.Errorf("host apply changed your mcp-adapter.json (err %v):\n%s", err, got)
			}
		})
	}
}

// THE OWNED HOST'S CAPTURE STATE OUTLIVES THE MOVE. The sidecars are keyed by surface, not by
// file, so under `own` the first apply onto mcp.json reads the baseline the mcp-adapter.json
// render left. Diffed against a mcp.json of yours, that baseline says you deleted yolo's server;
// the server must land anyway, and yours must stay. (The sidecars are planted as an 0.11 owned
// apply of pi/mcp leaves them: the adapter file's render as the baseline, its adopted
// `settings` key as the overlay.)
func TestPiMcpOwnedHostApplyOverTheAdapterRenderSidecars(t *testing.T) {
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	tgt := render.Host(home, nil, render.OwnershipOwn)
	writeTestFile(t, tgt.LastRenderPath("pi", "mcp"),
		`{"mcpServers":{"tavily":{"command":"npx","args":["-y","tavily-mcp"]}},"settings":{"idleTimeout":30}}`+"\n")
	writeTestFile(t, tgt.OverlayPath("pi", "mcp"), `{"settings":{"idleTimeout":30}}`+"\n")
	plant(t, home, piMCPRel, `{"mcpServers":{"mine":{"command":"mine"}}}`)
	in := hostTestInputs(t, testPacksForAgent(t, "pi"), nil, tavily(), nil)
	if r := hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/mcp"); r.Action != "rendered" {
		t.Fatalf("pi/mcp under own: %q", r.Action)
	}
	for i := 0; i < 2; i++ { // and again, over the overlay the first apply captured
		if got := jsonAt(t, home, piMCPRel, "mcpServers", "tavily", "command"); got != "npx" {
			t.Fatalf("apply %d: yolo's server did not land over the old baseline: %v", i+1, got)
		}
		if got := jsonAt(t, home, piMCPRel, "mcpServers", "mine", "command"); got != "mine" {
			t.Fatalf("apply %d: your own server did not survive: %v", i+1, got)
		}
		hostRenderWith(t, home, render.OwnershipOwn, in, "pi", "pi/mcp")
	}
}

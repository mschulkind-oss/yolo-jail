package entrypoint

// requiresenvdropnotice_test.go pins what the jail boot's drop notice (noteDroppedManagedEntries)
// says about an MCP server the `requires_env` gate removed (mcpServersWith): one that IS declared
// under `mcp_servers`, whose required variable is unset for the agent this boot renders.
//
// The defect (roadmap item, docs/reference/mcp-configuration.md "The rules the one loader
// enforces"): a launch with the variable set wrote the server into ~/.claude.json, and the next
// launch without it dropped the copy and called it "not in config", telling the user to declare
// a server they had already declared. Following that notice changed nothing.
//
// Each test boots one home twice through the boot loop (ConfigurePackSurfaces) over the shipped
// claude pack, so deleting the gate's record (Env.recordMCPGated) or the notice's read of it
// fails them.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// gatedServer needs ACME_TOKEN, the variable the second boot lacks.
func gatedServer() map[string]any {
	return map[string]any{"command": "/bin/true", "requires_env": []any{"ACME_TOKEN"}}
}

// bootTwiceDroppingVar boots one home with the variable set and then with it unset, writing
// extra into ~/.claude.json's mcpServers between the boots, and returns the second boot's stderr.
func bootTwiceDroppingVar(t *testing.T, servers, extra map[string]any) string {
	t.Helper()
	packs := capabilityPacks(t, "claude")
	home, ws := t.TempDir(), t.TempDir()
	boot := func(set bool) (*Env, string) {
		vars := capabilityJailVars(t, packs, servers, nil)
		if set {
			vars["ACME_TOKEN"] = "acme-test"
		}
		var stderr bytes.Buffer
		e := &Env{Home: home, Workspace: ws, Vars: vars, Stderr: &stderr}
		ConfigurePackSurfaces(e, packs)
		if fails := e.GenFailures(); len(fails) != 0 {
			t.Fatalf("boot render failed: %v\n%s", fails, stderr.String())
		}
		return e, stderr.String()
	}
	e, _ := boot(true)
	if _, ok := renderedMCPServers(t, e, "claude")["acme"]; !ok {
		t.Fatalf("the first boot, with ACME_TOKEN set, must deliver acme; this test is about " +
			"the copy it leaves behind")
	}
	if len(extra) > 0 {
		doc := decodeJSONFile(t, e.ClaudeJSONPath())
		table, _ := doc["mcpServers"].(map[string]any)
		for k, v := range extra {
			table[k] = v
		}
		doc["mcpServers"] = table
		writeHostFile(t, e.ClaudeJSONPath(), dumpJSON(t, doc))
	}
	e, notice := boot(false)
	if _, kept := renderedMCPServers(t, e, "claude")["acme"]; kept {
		t.Fatalf("acme was delivered with ACME_TOKEN unset")
	}
	return notice
}

// dropLine is the drop notice's line naming name ("dropping from …"), "" when none does: the
// gate's own "skipped" notice names the server too, and is not the line under test.
func dropLine(notice, name string) string {
	for _, l := range strings.Split(notice, "\n") {
		if strings.Contains(l, "dropping from") && strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

func TestTheBootDropNoticeSaysARequiresEnvGatedServerIsInConfig(t *testing.T) {
	notice := bootTwiceDroppingVar(t, map[string]any{"acme": gatedServer()}, nil)
	line := dropLine(notice, "acme")
	if line == "" {
		t.Fatalf("the boot must still announce that acme left ~/.claude.json; got %q", notice)
	}
	for _, wrong := range []string{"not in config", "to keep it, declare it"} {
		if strings.Contains(line, wrong) {
			t.Errorf("the notice says %q of a server that IS declared under mcp_servers; got %q",
				wrong, line)
		}
	}
	for _, want := range []string{
		"claude/config:",
		"mcpServers",
		"`mcp_servers`",                       // where it is declared
		"ACME_TOKEN",                          // the variable it lacks
		"`requires_env`",                      // the gate that dropped it
		"`env_sources`",                       // where to set the variable
		dropRemedyUserConfig + " on the host", // the file that lists them
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the notice for a requires_env-gated server must name %q; got %q", want, line)
		}
	}
}

// One table can lose a gated server and a hand-added entry at once: each keeps its own remedy.
func TestTheBootDropNoticeKeepsTheDeclareRemedyBesideAGatedServer(t *testing.T) {
	notice := bootTwiceDroppingVar(t, map[string]any{"acme": gatedServer()},
		map[string]any{"handmade": map[string]any{"command": "/bin/handmade"}})
	hand := dropLine(notice, "handmade")
	if !strings.Contains(hand, "dropping from mcpServers (not in config): handmade") {
		t.Errorf("the hand-added entry lost its declare-it remedy; got %q", hand)
	}
	if strings.Contains(hand, "acme") {
		t.Errorf("the gated server is listed among the entries not in config; got %q", hand)
	}
	if gated := dropLine(notice, "acme"); gated == "" || gated == hand {
		t.Errorf("the gated server must get a line of its own; got %q", notice)
	}
}

// An agent with an env file of its own renders its own table (tablesForAgent), so the gate's
// removals are read from that table: here ACME_TOKEN reached claude only through its file, and
// the second boot's file no longer sets it.
func TestTheBootDropNoticeReadsTheAgentsOwnGatedTable(t *testing.T) {
	packs := capabilityPacks(t, "claude")
	home, ws := t.TempDir(), t.TempDir()
	envFile := filepath.Join(home, AgentEnvDirRel, "claude.sh")
	servers := map[string]any{"acme": gatedServer()}
	boot := func(file string) (*Env, string) {
		writeHostFile(t, envFile, file)
		var stderr bytes.Buffer
		e := &Env{Home: home, Workspace: ws, Vars: capabilityJailVars(t, packs, servers, nil),
			Stderr: &stderr}
		ConfigurePackSurfaces(e, packs)
		if fails := e.GenFailures(); len(fails) != 0 {
			t.Fatalf("boot render failed: %v\n%s", fails, stderr.String())
		}
		return e, stderr.String()
	}
	e, _ := boot("export ACME_TOKEN='acme-test'\n")
	if _, ok := renderedMCPServers(t, e, "claude")["acme"]; !ok {
		t.Fatalf("claude's own env file sets ACME_TOKEN, so the first boot must deliver acme")
	}
	_, notice := boot("export OTHER='x'\n")
	line := dropLine(notice, "acme")
	if line == "" || strings.Contains(line, "not in config") || !strings.Contains(line, "ACME_TOKEN") {
		t.Errorf("claude's own table lost acme to the gate; the notice must say so and name "+
			"ACME_TOKEN; got %q", notice)
	}
}

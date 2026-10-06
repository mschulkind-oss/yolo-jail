package cli

// hostapplygatedloss_test.go pins what `yolo host apply` says about an MCP server the
// `requires_env` gate removed for an agent (hostAgentTables.skipped) whose copy is already in
// that agent's file: one the user's config DOES declare under `mcp_servers`, whose required
// variable this agent's composed environment lacks.
//
// The defect: the loss list read the derived layer, which never held the gated server, so the
// copy was labeled "(dropped — not in your config)", the dropped-entry group offered the
// declare-it remedy, and a first apply's confirmation said "anything above that is not in your
// config is dropped. To KEEP them: declare it…", beside the separate "not written — required env
// not set" line. Declaring it again changes nothing. The jail boot's drop notice had the same
// defect (requiresenvdropnotice_test.go in internal/entrypoint).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gatedCopyHome selects the shipped claude pack under `own`, declares `acme` with
// `requires_env: ["ACME_TOKEN"]`, leaves ACME_TOKEN unset, and puts a copy of acme in
// ~/.claude.json, as an earlier apply with the variable set would have.
func gatedCopyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	selectPacksWith(t, home, `"claude"`, `,"host_management":"own",`+
		`"mcp_servers":{"acme":{"command":"echo","requires_env":["ACME_TOKEN"]}}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("ACME_TOKEN", "")
	os.Unsetenv("ACME_TOKEN")
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"acme":{"command":"echo"}}}`)
	return home
}

func TestAHostApplyNamesARequiresEnvGatedLossForWhatItIs(t *testing.T) {
	home := gatedCopyHome(t)
	survey, report := surveyApply(t)
	if !strings.Contains(report, `"acme" not written — required env not set`) {
		t.Fatalf("fixture premise: the gate did not skip acme for claude:\n%s", report)
	}
	if !strings.Contains(report, "mcpServers.acme (dropped") {
		t.Fatalf("fixture premise: the copy of acme in ~/.claude.json is not reported lost:\n%s",
			report)
	}
	if strings.Contains(report, "mcpServers.acme (dropped — not in your config)") {
		t.Errorf("acme IS declared under `mcp_servers`; the loss calls it not in config:\n%s", report)
	}
	if !strings.Contains(report, "mcpServers.acme (dropped — in your config, required env not set: ACME_TOKEN)") {
		t.Errorf("the loss must say acme is declared and name the variable it lacks:\n%s", report)
	}
	for _, name := range survey.DroppedEntryNames() {
		if name == "acme" {
			t.Errorf("acme is counted among the entries the declare-it remedy keeps: %v",
				survey.DroppedEntryNames())
		}
	}
	if groups := groupsWithKey(hostApplyRemedyGroups(survey, home, false), mcpEntryRemedyKey(home)); len(groups) != 0 {
		t.Errorf("a declared server's loss got the declare-it group: %+v", groups)
	}
}

// A declined first apply lists the gated copy (it IS lost) but does not offer the declare-it
// remedy for it: it names the variable's home, `env_sources`, instead.
func TestADeclinedApplyGivesAGatedLossItsOwnRemedy(t *testing.T) {
	gatedCopyHome(t)
	rc, declined := applyWith(t, true, strings.NewReader("n\n"))
	if rc == 0 {
		t.Fatalf("a declined first apply must not proceed\n%s", declined)
	}
	if !strings.Contains(declined, "mcpServers.acme (dropped — in your config, required env not set") {
		t.Fatalf("the prompt must still list the gated copy, which the write drops:\n%s", declined)
	}
	for _, wrong := range []string{"not in your config is dropped", "declare it in"} {
		if strings.Contains(declined, wrong) {
			t.Errorf("the prompt offers %q for a server that is declared:\n%s", wrong, declined)
		}
	}
	if !strings.Contains(declined, "`env_sources`") {
		t.Errorf("the prompt must name `env_sources`, where the variable is set:\n%s", declined)
	}
}

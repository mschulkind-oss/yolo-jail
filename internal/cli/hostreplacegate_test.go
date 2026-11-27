package cli

// hostreplacegate_test.go pins `repo_instructions` replace at the host through the launch gate
// (docs/reference/workspace-instruction-files.md IF-D13): `yolo host -- <agent>` auto-applies a changed
// render before it starts the agent, so the replacement text must reach the real home's briefing
// through the gate alone, follow the replacement file's content, and leave when the mode goes back.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setUserRepoInstructions rewrites the fixture's user config with the given table body ("" removes it).
func setUserRepoInstructions(t *testing.T, home, table string) {
	t.Helper()
	cfg := `{"packs":["claude"],"host_management":"own","host_apply_on_launch":true`
	if table != "" {
		cfg += `,"repo_instructions":` + table
	}
	cfg += `}`
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
}

func hostClaudeBriefing(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("no host briefing written: %v", err)
	}
	return string(data)
}

func launchThroughTheGate(t *testing.T) string {
	t.Helper()
	var out, errw bytes.Buffer
	rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil)
	report := out.String() + errw.String()
	if rc != 127 {
		t.Fatalf("launch rc = %d, want 127 (past the gate)\n%s", rc, report)
	}
	return report
}

func TestHostLaunchGateDeliversTheReplacementAndFollowsIt(t *testing.T) {
	home := gateFixture(t, true)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	rules := filepath.Join(home, "rules.md")
	writeFile(t, rules, "The password is hamburger.\n")

	// keep -> replace: the launch alone writes the section.
	setUserRepoInstructions(t, home, `{"mode":"replace","replace_with":"`+rules+`"}`)
	launchThroughTheGate(t)
	if got := hostClaudeBriefing(t, home); !strings.Contains(got, "The password is hamburger.") {
		t.Fatalf("the launch gate did not deliver the replacement:\n%s", got)
	}

	// the file's CONTENT changes, the config does not: the launch re-applies.
	writeFile(t, rules, "The password is pickles.\n")
	launchThroughTheGate(t)
	got := hostClaudeBriefing(t, home)
	if !strings.Contains(got, "The password is pickles.") || strings.Contains(got, "hamburger") {
		t.Fatalf("a changed replacement file did not re-apply:\n%s", got)
	}

	// replace -> ignore, then replace -> keep: the section leaves.
	setUserRepoInstructions(t, home, `{"mode":"ignore"}`)
	launchThroughTheGate(t)
	if got := hostClaudeBriefing(t, home); strings.Contains(got, "pickles") {
		t.Fatalf("ignore left the replacement in the briefing:\n%s", got)
	}
	setUserRepoInstructions(t, home, `{"mode":"replace","replace_with":"`+rules+`"}`)
	launchThroughTheGate(t)
	setUserRepoInstructions(t, home, "")
	launchThroughTheGate(t)
	if got := hostClaudeBriefing(t, home); strings.Contains(got, "pickles") {
		t.Fatalf("keep left the replacement in the briefing:\n%s", got)
	}
}

// A dropped pack's leftovers are a question the launch cannot ask, and used to stop the WHOLE
// render: on the maintainer's machine 80 paths of one dropped pack made every launch skip the
// briefing, so a replacement was never written. The retire is the one question that does not
// block the rest of the render: declining it keeps everything, and the launch says it is waiting.
func TestHostLaunchGateRendersTheReplacementPastAWaitingRetire(t *testing.T) {
	home, packDir := dropFixture(t, dropPackJSON)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude",{"source":"file://`+packDir+`","name":"dropme"}],"host_management":"own","host_apply_on_launch":true}`)
	t.Setenv("YOLO_VERSION", "")
	setGateTTY(t, false)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	skill, _ := deliveredPaths(home)
	mustExist(t, skill, "the first apply delivered it")

	rules := filepath.Join(home, "rules.md")
	writeFile(t, rules, "The password is hamburger.\n")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_management":"own","host_apply_on_launch":true,`+
			`"repo_instructions":{"mode":"replace","replace_with":"`+rules+`"}}`)

	report := launchThroughTheGate(t)
	if got := hostClaudeBriefing(t, home); !strings.Contains(got, "The password is hamburger.") {
		t.Fatalf("a waiting retire stopped the render:\n%s\n--- briefing:\n%s", report, got)
	}
	mustExist(t, skill, "the launch must not retire what it cannot ask about")
	if !strings.Contains(report, "left behind are still in your home") || !strings.Contains(report, "yolo host apply --assert") {
		t.Errorf("the launch must say the retire is waiting and name the command:\n%s", report)
	}
	if strings.Contains(report, "did not render your host configuration") {
		t.Errorf("the launch still refused to render:\n%s", report)
	}
}

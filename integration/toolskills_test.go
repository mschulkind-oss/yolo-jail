package integration

import (
	"regexp"
	"strings"
	"testing"
)

// toolskills_test.go is slice 6 of docs/design/mise-tool-skills.md: the tool-skills path end to end,
// slices 1 to 5 together, with no agent and no network.
//
// THE SHAPE. The allowlist is the USER scope's `install.mise` keys (`jq`, at version "system" so
// mise installs nothing). A fake `mise` answers `skills ls --json` with the shape of
// internal/toolskills/testdata/ls-nonempty.json: it is a script in a scratch directory put in front
// of PATH for ONE command, the in-jail step itself (`yolo internal tool-skills`, the command the
// provisioning stage runs), because the stage runs before any test command can shape its PATH.
// The first launch publishes the snapshot and says it arrives with the next session; the second
// launch's host composition delivers it to the agent's skills directory and names the one skill a
// built-in already provides.
//

const toolSkillsFakeTool = "jq"

func TestToolSkillsReachTheAgentSkillsDirectoryOnTheNextSession(t *testing.T) {
	requireJail(t)
	packHome(t, `{"packs": ["claude"], "install": {"mise": {"`+toolSkillsFakeTool+`": "system"}}}`)
	dir := writeProject(t, `{}`)
	nonce := "TOOLSKILL-" + strings.ToUpper(randomHex(t, 4))

	first := strings.Join([]string{
		`set -u`,
		`data="${MISE_DATA_DIR:-/mise}"`,
		`for n in fake-skill configuring-the-jail; do`,
		`  mkdir -p "$data/installs/` + toolSkillsFakeTool + `/9.9/skills/$n"`,
		`  printf '` + nonce + `-%s\n' "$n" > "$data/installs/` + toolSkillsFakeTool + `/9.9/skills/$n/SKILL.md"`,
		`done`,
		`d=$(mktemp -d)`,
		`cat > "$d/mise" <<EOF`,
		`#!/bin/sh`,
		`cat <<'JSON'`,
		`[{"name":"fake-skill","tool":"` + toolSkillsFakeTool + `","version":"9.9","path":"$data/installs/` + toolSkillsFakeTool + `/9.9/skills/fake-skill"},`,
		` {"name":"configuring-the-jail","tool":"` + toolSkillsFakeTool + `","version":"9.9","path":"$data/installs/` + toolSkillsFakeTool + `/9.9/skills/configuring-the-jail"}]`,
		`JSON`,
		`EOF`,
		`chmod +x "$d/mise"`,
		`echo "=== ALLOWLIST ==="`,
		`echo "${YOLO_MISE_SKILL_TOOLS:-}"`,
		`echo "=== STEP ==="`,
		`PATH="$d:$PATH" yolo internal tool-skills /workspace 2>&1`,
		`echo "=== END ==="`,
	}, "\n")
	// withHostSemantics: a launch from inside a jail gets an EMPTY allowlist (design §7, MS-D1), and the
	// suite itself runs in one, so each launch here takes itself for a host launch.
	r := runYolo(t, dir, first, withoutFixtureProgramInstall(), withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("first launch: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== ALLOWLIST ===", "=== STEP ===")); got != `["`+toolSkillsFakeTool+`"]` {
		t.Fatalf("the jail was handed YOLO_MISE_SKILL_TOOLS=%q, want the user scope's install.mise keys", got)
	}
	// The in-jail disclosure (design 6.2, "First snapshot"): two skills are ready, and the line names
	// the next step.
	step := section(r.stdout, "=== STEP ===", "=== END ===")
	if want := "yolo: mise skills: 2 skills from " + toolSkillsFakeTool +
		" are ready; they reach the agent with the next session: relaunch or attach."; !strings.Contains(step, want) {
		t.Fatalf("the step's output = %q, want it to contain %q", step, want)
	}

	second := strings.Join([]string{
		`echo "=== DIGEST ==="`,
		`echo "${YOLO_TOOL_SKILLS_DELIVERED:-}"`,
		`echo "=== SKILL ==="`,
		`cat "$HOME/.claude/skills/fake-skill/SKILL.md"`,
		`echo "=== BUILTIN ==="`,
		`grep -c '` + nonce + `' "$HOME/.claude/skills/configuring-the-jail/SKILL.md" || true`,
		`echo "=== END ==="`,
	}, "\n")
	r = runYolo(t, dir, second, withoutFixtureProgramInstall(), withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("second launch: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== SKILL ===", "=== BUILTIN ===")); got != nonce+"-fake-skill" {
		t.Errorf("the tool's skill in the agent's skills directory = %q, want %q", got, nonce+"-fake-skill")
	}
	if got := strings.TrimSpace(section(r.stdout, "=== BUILTIN ===", "=== END ===")); got != "0" {
		t.Errorf("the tool's skill overwrote the built-in of the same name (marker count %q)", got)
	}
	// YOLO_TOOL_SKILLS_DELIVERED is the digest the host delivered (MS-D7).
	if got := strings.TrimSpace(section(r.stdout, "=== DIGEST ===", "=== SKILL ===")); !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Errorf("YOLO_TOOL_SKILLS_DELIVERED = %q, want the snapshot's 64-hex digest", got)
	}
	// The host's disclosure (design 6.2, "Name already provided"): named, with its next step.
	want := `yolo: mise skill "configuring-the-jail" (` + toolSkillsFakeTool + `) not delivered: `
	if !strings.Contains(r.stderr, want) || !strings.Contains(r.stderr, "Rename that skill to use the tool's.") {
		t.Errorf("the second launch's stderr lacks the shadow line %q:\n%s", want, r.stderr)
	}
	if strings.Contains(r.stderr, `"fake-skill"`) {
		t.Errorf("a delivered skill got a disclosure line:\n%s", r.stderr)
	}
}

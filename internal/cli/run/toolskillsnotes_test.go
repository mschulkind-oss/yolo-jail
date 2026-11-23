package run

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type notedSkill struct{ tool, name string }

// writeToolNotesSnapshot is writeToolLayerSnapshot for several skills.
func writeToolNotesSnapshot(t *testing.T, ws, token string, skills ...notedSkill) {
	t.Helper()
	dir := filepath.Join(ws, ".yolo", "tool-skills")
	dd := filepath.Join(dir, toolLayerDigest)
	var entries []map[string]string
	for _, s := range skills {
		if err := os.MkdirAll(filepath.Join(dd, "skills", s.name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dd, "skills", s.name, "SKILL.md"), []byte(s.tool+"/"+s.name), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, map[string]string{"tool": s.tool, "version": "1", "name": s.name})
	}
	idx, _ := json.Marshal(map[string]any{"version": 1, "token": token, "digest": toolLayerDigest, "skills": entries})
	if err := os.WriteFile(filepath.Join(dd, "index.json"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current"), []byte(toolLayerDigest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// refreshToolNotes runs the REAL composition call site (refreshJailBriefings: the launch and the
// attach both run it) with a user allowlist and returns what it said on stderr.
func refreshToolNotes(t *testing.T, userScope string, skills ...notedSkill) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	home := ctxLaunchHome(t, "")
	t.Setenv("HOME", home)
	writeUserScope(t, userScope)
	workspace := realWorkspace(t)
	token, err := config.ToolSkillsToken(workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeToolNotesSnapshot(t, workspace, token, skills...)
	o := goldenOptions(workspace, home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	cname := "yolo-tool-notes"
	if _, err := o.refreshJailBriefings(cname, jsonx.NewOrderedMap(), "podman",
		stagedPacks{packs: claudePackFixture(t)}, ioprio.Normal, nil); err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	if got := stagedSkillBodies(t, filepath.Join(paths.AgentsDir(), cname), "ok-skill"); len(skills) > 0 && len(got) == 0 {
		t.Fatalf("the unshadowed skill was not delivered, so the fixture proves nothing about the lines")
	}
	return stderr.String()
}

// Design 6.2, row "Name already provided": a tool skill a built-in took the name of is named, with
// its winner and its next step. Run through refreshJailBriefings, so deleting the print call in
// prepare.go fails this test.
func TestToolSkillsShadowedByABuiltInIsNamedWithItsNextStep(t *testing.T) {
	got := refreshToolNotes(t, `{"install": {"mise": {"packslip:x": "1"}}}`,
		notedSkill{"packslip:x", "ok-skill"}, notedSkill{"packslip:x", "configuring-the-jail"})
	want := `yolo: mise skill "configuring-the-jail" (packslip:x) not delivered: `
	if !strings.Contains(got, want) || !strings.Contains(got, "already provides a skill by that name and wins. Rename that skill to use the tool's.") {
		t.Fatalf("stderr = %q\nwant a shadow line beginning %q ending in its next step", got, want)
	}
	if strings.Contains(got, `"ok-skill"`) {
		t.Errorf("a delivered skill got a line: %q", got)
	}
}

// Design 6.2, row "Two tools, one name": the loser is named, its winner is named, and the line
// names the verb that reorders install.mise.
func TestToolSkillsTwoToolsOneNameNamesTheWinnerAndTheReorderVerb(t *testing.T) {
	got := refreshToolNotes(t, `{"install": {"mise": {"packslip:a": "1", "packslip:b": "1"}}}`,
		notedSkill{"packslip:a", "ok-skill"}, notedSkill{"packslip:a", "dup"}, notedSkill{"packslip:b", "dup"})
	for _, want := range []string{`yolo: mise skill "dup" from packslip:b not delivered: packslip:a comes first in your install.mise and wins.`,
		"Reorder install.mise (`yolo config unset install.mise.<tool> --user`"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr = %q\nmissing %q", got, want)
		}
	}
}

// A skill the host refuses (an unusable name in a tampered index) is named with a next step, and
// the others still arrive.
func TestToolSkillsInvalidSkillIsNamedWithItsNextStep(t *testing.T) {
	got := refreshToolNotes(t, `{"install": {"mise": {"packslip:x": "1"}}}`,
		notedSkill{"packslip:x", "ok-skill"}, notedSkill{"packslip:x", ".hidden"})
	want := `yolo: mise skill ".hidden" (packslip:x) not delivered: not a usable skill name. Fix that skill in the tool, or remove the tool from your install.mise, then relaunch yolo.`
	if !strings.Contains(got, want) {
		t.Fatalf("stderr = %q\nwant %q", got, want)
	}
}

// A clean delivery says nothing (design 6.2, first row).
func TestToolSkillsCleanDeliveryIsSilent(t *testing.T) {
	if got := refreshToolNotes(t, `{"install": {"mise": {"packslip:x": "1"}}}`, notedSkill{"packslip:x", "ok-skill"}); got != "" {
		t.Fatalf("a clean delivery printed %q", got)
	}
}

// The no-layer case the host owns: the user named tools but the snapshot token cannot be stored, so
// no allowlist would reach the jail. It says so and names the next step instead of delivering
// nothing silently.
func TestToolSkillsBadTokenStoreSaysToolSkillsAreOff(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := ctxLaunchHome(t, "")
	t.Setenv("HOME", home)
	writeUserScope(t, `{"install": {"mise": {"packslip:x": "1"}}}`)
	workspace := realWorkspace(t)
	// A regular file where the token folder belongs: the token cannot be minted.
	if err := os.MkdirAll(paths.GlobalStorage(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.GlobalStorage(), "tool-skills-tokens"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(workspace, home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	if _, err := o.refreshJailBriefings("yolo-tool-notes-token", jsonx.NewOrderedMap(), "podman",
		stagedPacks{packs: claudePackFixture(t)}, ioprio.Normal, nil); err != nil {
		t.Fatal(err)
	}
	got := stderr.String()
	if !strings.Contains(got, "yolo: mise skills: tool skills are off for this launch: the workspace's snapshot token could not be stored") ||
		!strings.Contains(got, "then relaunch yolo.") {
		t.Fatalf("stderr = %q", got)
	}
}

// YOLO_TOOL_SKILLS_DELIVERED (MS-D7) is the digest of the snapshot this entry's composition read,
// on the per-entry channel the jail's session reads (userEnvFileBody) and on the macos-user launch
// env; empty before the jail has written one, and for a snapshot a forged token guards.
func TestToolSkillsDeliveredDigestRidesTheChannel(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeUserScope(t, `{"install": {"mise": {"packslip:x": "1"}}}`)
	workspace := realWorkspace(t)
	o := goldenOptions(workspace, home)
	line := func() string {
		c := channelFor(t, o, bareConfig(), zaiSelected(t), nil)
		body := string(userEnvFileBody(nil, c))
		for _, l := range strings.Split(body, "\n") {
			if strings.Contains(l, "YOLO_TOOL_SKILLS_DELIVERED") {
				return l
			}
		}
		t.Fatalf("no YOLO_TOOL_SKILLS_DELIVERED in the channel:\n%s", body)
		return ""
	}
	if got := line(); strings.Contains(got, toolLayerDigest) {
		t.Fatalf("no snapshot yet but %q", got)
	}
	token, err := config.ToolSkillsToken(workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeToolNotesSnapshot(t, workspace, strings.Repeat("0", 64), notedSkill{"packslip:x", "ok-skill"})
	if got := line(); strings.Contains(got, toolLayerDigest) {
		t.Fatalf("a forged snapshot's digest was delivered: %q", got)
	}
	writeToolNotesSnapshot(t, workspace, token, notedSkill{"packslip:x", "ok-skill"})
	if got := line(); !strings.Contains(got, toolLayerDigest) {
		t.Fatalf("the snapshot's digest is not on the channel: %q", got)
	}
	env := channelFor(t, o, bareConfig(), zaiSelected(t), nil).launchEnv("claude")
	if got := envAt(env, "YOLO_TOOL_SKILLS_DELIVERED"); got != toolLayerDigest {
		t.Errorf("launch env YOLO_TOOL_SKILLS_DELIVERED = %q, want %q", got, toolLayerDigest)
	}
}

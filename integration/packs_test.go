package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackDeliversSkillAndBriefing is C3 end to end in a real container: a local
// (file://) pack's skills/ tree reaches the agent's :ro-mounted skills dir, and its
// AGENTS.md prose reaches the briefing WITH a provenance header naming the pack.
//
// The provenance header is the part worth an integration test rather than a unit
// test: pack prose is instructions the agent will follow, so if attribution were
// lost anywhere in the staging→mount→compose chain, a surprising rule would be
// untraceable and nobody would notice until it mattered.
//
// `packs` is USER-scope only by construction, so the fixture writes a user config
// under a temp HOME rather than a workspace config — which is itself worth
// exercising, since it is the only channel that works.
func TestPackDeliversSkillAndBriefing(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	skill := filepath.Join(pack, "skills", "pack-demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"),
		[]byte("---\nname: pack-demo\ndescription: from a pack\n---\n# Pack Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "AGENTS.md"),
		[]byte("PACKRULE always prefer rg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{"agents": ["claude"]}`)
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": ["file://`+pack+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	r := runYolo(t, dir,
		`ls /home/agent/.claude/skills/pack-demo/SKILL.md && `+
			`rg -c PACKRULE /home/agent/.claude/CLAUDE.md && `+
			`rg -c 'from pack:' /home/agent/.claude/CLAUDE.md`)
	if r.rc != 0 {
		t.Fatalf("pack delivery failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "SKILL.md") {
		t.Errorf("pack skill not mounted:\n%s", r.stdout)
	}
}

// A pack whose only/exclude filters match nothing must WARN rather than silently
// deliver an empty tree: that is nearly always a filter typo, and the user would
// otherwise just see a pack that does nothing.
func TestPackWithNoMatchingFilesWarns(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "AGENTS.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{"agents": ["claude"]}`)
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": [{"source": "file://`+pack+`", "only": ["nothing-matches/*"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	r := runYolo(t, dir, "true")
	if !strings.Contains(r.combined(), "staged 0 files") {
		t.Errorf("expected a 0-files warning for a pack whose filters match nothing:\n%s", r.combined())
	}
}

package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agents/builtinskills"
)

// TestPrepareSkillsStaging verifies the built-in skill lands, host skills are
// mirrored per-agent, stale entries are cleared, and — critically — the
// skills_dir INODE is preserved across re-staging (never rmtree+mkdir'd).
func TestPrepareSkillsStaging(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A host claude skills dir with one skill + a nested file.
	hostClaudeSkills := filepath.Join(home, ".claude", "skills")
	must(t, os.MkdirAll(filepath.Join(hostClaudeSkills, "my-skill"), 0o755))
	must(t, os.WriteFile(filepath.Join(hostClaudeSkills, "my-skill", "SKILL.md"), []byte("host skill"), 0o644))

	staging, err := PrepareSkills("test-cname", home, []string{"claude"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// claude's skills-staging dir exists; find it via the spec.
	claudeStaging := ""
	for _, entry := range mustReadDir(t, staging) {
		if entry.IsDir() && entry.Name() != "" && entry.Name() != "jail-startup" {
			// skills-claude/ (or whatever SkillsStaging() yields)
			if _, err := os.Stat(filepath.Join(staging, entry.Name(), "jail-startup", "SKILL.md")); err == nil {
				claudeStaging = filepath.Join(staging, entry.Name())
			}
		}
	}
	if claudeStaging == "" {
		t.Fatal("claude skills-staging dir with jail-startup not found")
	}

	// Built-in skill matches the embedded source (copy-fidelity smoke test;
	// the real content/frontmatter contract lives in TestSkillFrontmatter).
	want, _ := builtinskills.FS.ReadFile("jail-startup/SKILL.md")
	if data, _ := os.ReadFile(filepath.Join(claudeStaging, "jail-startup", "SKILL.md")); string(data) != string(want) {
		t.Error("jail-startup SKILL.md content mismatch vs embedded source")
	}
	// Every ungated built-in skill lands.
	for _, name := range []string{"jail-startup", "configuring-the-jail", "diagnosing-the-jail"} {
		if _, err := os.Stat(filepath.Join(claudeStaging, name, "SKILL.md")); err != nil {
			t.Errorf("built-in skill %q missing from staging: %v", name, err)
		}
	}
	// The source-tree-gated skill is absent when includeDev is false.
	if _, err := os.Stat(filepath.Join(claudeStaging, "developing-yolo-jail", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("developing-yolo-jail should be gated out when includeDev is false")
	}
	// Host skill mirrored.
	if data, _ := os.ReadFile(filepath.Join(claudeStaging, "my-skill", "SKILL.md")); string(data) != "host skill" {
		t.Errorf("host skill not mirrored: %q", data)
	}

	// Inode preservation: re-stage and confirm the skills_dir inode is unchanged.
	ino1 := inodeOf(t, claudeStaging)
	// Drop a stale entry to prove clearing happens inside.
	must(t, os.WriteFile(filepath.Join(claudeStaging, "STALE"), []byte("x"), 0o644))
	if _, err := PrepareSkills("test-cname", home, []string{"claude"}, false); err != nil {
		t.Fatal(err)
	}
	if inodeOf(t, claudeStaging) != ino1 {
		t.Error("skills_dir inode changed across re-stage — bind-mount would detach")
	}
	if _, err := os.Stat(filepath.Join(claudeStaging, "STALE")); !os.IsNotExist(err) {
		t.Error("stale entry should have been cleared")
	}
	// Built-in + host skill still present after re-stage.
	if _, err := os.Stat(filepath.Join(claudeStaging, "jail-startup", "SKILL.md")); err != nil {
		t.Error("jail-startup missing after re-stage")
	}
}

// TestPrepareSkillsIncludeDev confirms the source-tree-gated skill is staged
// only when includeDev is true.
func TestPrepareSkillsIncludeDev(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	staging, err := PrepareSkills("dev-cname", home, []string{"claude"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(staging, "skills-claude", "developing-yolo-jail", "SKILL.md")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("developing-yolo-jail should be staged when includeDev is true: %v", err)
	}
}

// TestPrepareSkillsFollowsSymlinks confirms a symlinked host skill dir is
// dereferenced (copytree symlinks=False).
func TestPrepareSkillsFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Real skill content outside the skills dir, symlinked in.
	realSkill := filepath.Join(home, "real-skill")
	must(t, os.MkdirAll(realSkill, 0o755))
	must(t, os.WriteFile(filepath.Join(realSkill, "SKILL.md"), []byte("via symlink"), 0o644))
	hostSkills := filepath.Join(home, ".claude", "skills")
	must(t, os.MkdirAll(hostSkills, 0o755))
	must(t, os.Symlink(realSkill, filepath.Join(hostSkills, "linked-skill")))

	staging, err := PrepareSkills("c2", home, []string{"claude"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Find the staging dir + confirm the dereferenced copy is a real file.
	var found bool
	_ = filepath.Walk(staging, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() && filepath.Base(p) == "SKILL.md" {
			if data, _ := os.ReadFile(p); string(data) == "via symlink" {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Error("symlinked host skill should be dereferenced into staging")
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	must(t, err)
	return entries
}

// A8: pi and codex had Skills:"" and were `continue`d by PrepareSkills, so they
// received NO skills at all — including yolo's own BUILT-IN suite — while their
// generated briefing told them to "read configuring-the-jail". This pins that
// every skills-bearing agent gets the built-in suite staged, and pins the two
// paths, which come from the shipped agent implementations (pi:
// join(getAgentDir(),"skills"); codex: $CODEX_HOME/skills) rather than from docs.
//
// opencode is deliberately absent: it has no user-level skills dir at all, so
// Skills:"" is correct for it and it stays briefing-only.
func TestEveryAgentWithSkillsGetsBuiltinSuite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, tc := range []struct{ agent, want string }{
		{"claude", ".claude/skills"},
		{"copilot", ".copilot/skills"},
		{"pi", ".pi/agent/skills"},
		{"codex", ".codex/skills"},
		{"agy", ".gemini/antigravity-cli/skills"},
	} {
		spec, ok := Get(tc.agent)
		if !ok {
			t.Fatalf("agent %q not in registry", tc.agent)
		}
		if spec.Skills != tc.want {
			t.Errorf("%s Skills = %q, want %q", tc.agent, spec.Skills, tc.want)
		}
		staging, err := PrepareSkills("c-"+tc.agent, home, []string{tc.agent}, false)
		if err != nil {
			t.Fatalf("%s: %v", tc.agent, err)
		}
		// jail-startup is in the built-in suite, so its presence proves the agent
		// is no longer skipped.
		probe := filepath.Join(staging, spec.SkillsStaging(), "jail-startup", "SKILL.md")
		if _, err := os.Stat(probe); err != nil {
			t.Errorf("%s: built-in suite not staged (%v)", tc.agent, err)
		}
	}

	// opencode stays skills-less by design.
	if spec, _ := Get("opencode"); spec.Skills != "" {
		t.Errorf("opencode Skills = %q, want \"\" (no user-level skills dir)", spec.Skills)
	}
}

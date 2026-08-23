package jailcontent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// TestPrepareSkillsStaging verifies the built-in skill lands, a pack's skills are
// layered in, stale entries are cleared, and — critically — the skills_dir INODE
// is preserved across re-staging (never rmtree+mkdir'd).
//
// The user's own skill arrives through the PACK layer, which is where it comes from since S3:
// the layer that read the host's ~/.<agent>/skills directly was reading a directory
// `apply --host` generates, so the user's tree reaches a jail as the local pack instead.
func TestPrepareSkillsStaging(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A pack (the local pack's shape: a bare skills/ tree) with one skill.
	localPack := filepath.Join(home, ".config", "yolo-jail", "local", "skills")
	must(t, os.MkdirAll(filepath.Join(localPack, "my-skill"), 0o755))
	must(t, os.WriteFile(filepath.Join(localPack, "my-skill", "SKILL.md"), []byte("host skill"), 0o644))
	withPackSkillDirs(t, localPack)

	withSkillTargets(t, ".claude/skills")
	staging, err := PrepareSkills("test-cname", home, []string{"claude"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// claude's skills-staging dir exists; find it via the spec.
	claudeStaging := ""
	for _, entry := range mustReadDir(t, staging) {
		if entry.IsDir() && entry.Name() != "" && entry.Name() != "configuring-the-jail" {
			// skills-claude/ (or whatever SkillsStaging() yields)
			if _, err := os.Stat(filepath.Join(staging, entry.Name(), "configuring-the-jail", "SKILL.md")); err == nil {
				claudeStaging = filepath.Join(staging, entry.Name())
			}
		}
	}
	if claudeStaging == "" {
		t.Fatal("claude skills-staging dir with configuring-the-jail not found")
	}

	// Built-in skill matches the embedded source (copy-fidelity smoke test;
	// the real content/frontmatter contract lives in TestSkillFrontmatter).
	want, _ := builtinskills.FS.ReadFile("configuring-the-jail/SKILL.md")
	if data, _ := os.ReadFile(filepath.Join(claudeStaging, "configuring-the-jail", "SKILL.md")); string(data) != string(want) {
		t.Error("configuring-the-jail SKILL.md content mismatch vs embedded source")
	}
	// Every ungated built-in skill lands.
	for _, name := range []string{"configuring-the-jail", "diagnosing-the-jail"} {
		if _, err := os.Stat(filepath.Join(claudeStaging, name, "SKILL.md")); err != nil {
			t.Errorf("built-in skill %q missing from staging: %v", name, err)
		}
	}
	// The source-tree-gated skill is absent when includeDev is false.
	if _, err := os.Stat(filepath.Join(claudeStaging, "developing-yolo-jail", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("developing-yolo-jail should be gated out when includeDev is false")
	}
	// The pack's skill is layered in.
	if data, _ := os.ReadFile(filepath.Join(claudeStaging, "my-skill", "SKILL.md")); string(data) != "host skill" {
		t.Errorf("pack skill not staged: %q", data)
	}

	// Inode preservation: re-stage and confirm the skills_dir inode is unchanged.
	ino1 := inodeOf(t, claudeStaging)
	// Drop a stale entry to prove clearing happens inside.
	must(t, os.WriteFile(filepath.Join(claudeStaging, "STALE"), []byte("x"), 0o644))
	withSkillTargets(t, ".claude/skills")
	if _, err := PrepareSkills("test-cname", home, []string{"claude"}, false); err != nil {
		t.Fatal(err)
	}
	if inodeOf(t, claudeStaging) != ino1 {
		t.Error("skills_dir inode changed across re-stage — bind-mount would detach")
	}
	if _, err := os.Stat(filepath.Join(claudeStaging, "STALE")); !os.IsNotExist(err) {
		t.Error("stale entry should have been cleared")
	}
	// Built-in + pack skill still present after re-stage.
	if _, err := os.Stat(filepath.Join(claudeStaging, "configuring-the-jail", "SKILL.md")); err != nil {
		t.Error("configuring-the-jail missing after re-stage")
	}
}

// TestPrepareSkillsIncludeDev confirms the source-tree-gated skill is staged
// only when includeDev is true.
func TestPrepareSkillsIncludeDev(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	withSkillTargets(t, ".claude/skills")
	staging, err := PrepareSkills("dev-cname", home, []string{"claude"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(staging, "skills-claude", "developing-yolo-jail", "SKILL.md")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("developing-yolo-jail should be staged when includeDev is true: %v", err)
	}
}

// TestPrepareSkillsFollowsSymlinks confirms a symlinked skill dir is
// dereferenced (copytree symlinks=False) — the dotfile-manager shape, where a user's skills
// tree is a tree of links into the repo that deploys them.
func TestPrepareSkillsFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Real skill content outside the skills dir, symlinked in.
	realSkill := filepath.Join(home, "real-skill")
	must(t, os.MkdirAll(realSkill, 0o755))
	must(t, os.WriteFile(filepath.Join(realSkill, "SKILL.md"), []byte("via symlink"), 0o644))
	packSkills := filepath.Join(home, ".config", "yolo-jail", "local", "skills")
	must(t, os.MkdirAll(packSkills, 0o755))
	must(t, os.Symlink(realSkill, filepath.Join(packSkills, "linked-skill")))
	withPackSkillDirs(t, packSkills)

	withSkillTargets(t, ".claude/skills")
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

// S3: THE JAIL DOES NOT READ THE DESTINATION BACK IN.
//
// `apply --host` COMPOSES ~/.claude/skills wholesale, so a jail that layered the host's copy of
// that directory in as "the user's own tree" was reading yolo's own generated output — and since
// the local pack is an ordinary pack entry, its content then arrived TWICE by two routes,
// invisible only because a flat merge is last-writer-wins. This is the observable that
// distinguishes the two: a skill sitting ONLY in the destination reaches the staging dir if and
// only if the defect is present.
//
// It also pins the second half — nothing arrives twice — by asserting the staged skill's content
// comes from the PACK source rather than the destination's differing copy of the same name.
func TestJailDoesNotStageTheDestinationsOwnSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// The DESTINATION, holding composed output: one skill found nowhere else, and one whose name
	// the pack layer also ships with different content.
	dest := filepath.Join(home, ".claude", "skills")
	must(t, os.MkdirAll(filepath.Join(dest, "composed-only"), 0o755))
	must(t, os.WriteFile(filepath.Join(dest, "composed-only", "SKILL.md"),
		[]byte("FROM THE DESTINATION"), 0o644))
	must(t, os.MkdirAll(filepath.Join(dest, "shared-name"), 0o755))
	must(t, os.WriteFile(filepath.Join(dest, "shared-name", "SKILL.md"),
		[]byte("FROM THE DESTINATION"), 0o644))

	// The local pack, which is how the user's own skills legitimately reach a jail.
	localPack := filepath.Join(home, ".config", "yolo-jail", "local", "skills")
	must(t, os.MkdirAll(filepath.Join(localPack, "shared-name"), 0o755))
	must(t, os.WriteFile(filepath.Join(localPack, "shared-name", "SKILL.md"),
		[]byte("FROM THE LOCAL PACK"), 0o644))
	withPackSkillDirs(t, localPack)

	withSkillTargets(t, ".claude/skills")
	staging, err := PrepareSkills("s3-cname", home, []string{"claude"}, false)
	must(t, err)
	staged := filepath.Join(staging, SkillStagingName("claude"))

	if _, err := os.Stat(filepath.Join(staged, "composed-only")); !os.IsNotExist(err) {
		t.Errorf("a skill present ONLY in ~/.claude/skills was staged (stat err=%v) — the jail is "+
			"reading the destination `apply --host` generates back in as the user's own tree (S3)",
			err)
	}
	// And the pack's copy is what landed: content, not just presence, because the destination
	// holds the same NAME and a defect would simply overwrite the pack's copy last.
	data, rerr := os.ReadFile(filepath.Join(staged, "shared-name", "SKILL.md"))
	must(t, rerr)
	if string(data) != "FROM THE LOCAL PACK" {
		t.Errorf("the staged skill came from the destination, not the pack: %q", data)
	}
}

// Every PACK-DECLARED skills destination must receive the built-in suite.
//
// This used to iterate the AGENT REGISTRY and assert each agent's Skills field. It now
// iterates DECLARATIONS, which is the point of the transition: core builds a staging dir
// per pack target and knows nothing about which tool reads it. The destinations below are
// the ones the official packs declare, so a pack dropping its skills mount fails here.
//
// A8 is still pinned by the paths themselves: pi and codex appear, and before A8 they had
// no skills dir at all and silently received nothing — including yolo's own built-ins.
func TestBuiltinSuiteReachesEveryDeclaredSkillsTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, dest := range []string{
		".claude/skills",
		".copilot/skills",
		".pi/agent/skills",
		".codex/skills",
		".gemini/config/skills",
	} {
		withSkillTargets(t, dest)
		staging, err := PrepareSkills("c-target", home, nil, false)
		if err != nil {
			t.Fatalf("%s: %v", dest, err)
		}
		// configuring-the-jail is in the built-in suite, so its presence proves the target was
		// built rather than skipped.
		pack := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(dest, "."), "/skills"), "/", "-")
		probe := filepath.Join(staging, SkillStagingName(pack), "configuring-the-jail", "SKILL.md")
		if _, err := os.Stat(probe); err != nil {
			t.Errorf("%s: built-in suite not staged (%v)", dest, err)
		}
	}

	// With NO declared target, nothing is staged — a jail with no packs gets no skills
	// dirs rather than an invented one.
	SetPackSkillTargets(nil)
	staging, err := PrepareSkills("c-none", home, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("no declared targets but skills were staged: %v", entries)
	}
}

func withSkillTargets(t *testing.T, dests ...string) {
	t.Helper()
	var targets []SkillTarget
	for _, dest := range dests {
		pack := strings.TrimSuffix(strings.TrimPrefix(dest, "."), "/skills")
		pack = strings.ReplaceAll(pack, "/", "-")
		targets = append(targets, SkillTarget{Staging: SkillStagingName(pack), Dest: dest})
	}
	SetPackSkillTargets(targets)
	t.Cleanup(func() { SetPackSkillTargets(nil) })
}

// withPackSkillDirs points the pack layer at `dirs` for one test, replacing what the host's own
// ~/.<agent>/skills tree used to supply directly (S3: that layer read the DESTINATION, which
// `apply --host` now generates). The user's own skills reach a jail as the LOCAL PACK, which is an
// ordinary entry in this list — appended last by config.LoadPacks.
func withPackSkillDirs(t *testing.T, dirs ...string) {
	t.Helper()
	SetPackSkillDirs(dirs)
	t.Cleanup(func() { SetPackSkillDirs(nil) })
}

package run

// packskillsfrom_test.go is the JAIL-PATH gate for the `skills` kind's `from` field.
//
// The bug: stagePacks read <packRoot>/skills unconditionally, on both branches (the embedded
// staging loop and the configured one), so a pack declaring
// `{"kind":"skills","from":"my-skills","into":".claude/skills"}` had skills/ read instead —
// no warning, and `pack lint` said the manifest was fine.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
)

// writeSkillTree writes <dir>/<name>/SKILL.md so the dir counts as a skills source.
func writeSkillTree(t *testing.T, dir, name string) {
	t.Helper()
	sk := filepath.Join(dir, name)
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sk, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// localSkillsPack writes a pack whose skills live in `srcDir` and configures it as the only
// pack, returning the Options ready to stage. The pack is LOCAL (file://) so staging reads
// it from disk without a fetch.
func localSkillsPack(t *testing.T, srcDir, from string) *Options {
	t.Helper()
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "sf")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, packDir, `{"contributes":[{"kind":"skills","from":"`+from+
		`","into":".claude/skills"}]}`)
	if srcDir != "" {
		writeSkillTree(t, filepath.Join(packDir, srcDir), "sfskill")
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"sf"}]`)
	return &Options{Workspace: t.TempDir()}
}

// stagedSkillDirs runs stagePacks and returns what it handed to skills staging, plus the
// warnings it printed.
func stagedSkillDirs(t *testing.T, o *Options) ([]string, string) {
	t.Helper()
	var out bytes.Buffer
	o.Stdout = &out
	agents.SetPackSkillDirs(nil)
	if _, _, _, err := o.stagePacks("yolo-test-skillsfrom"); err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	return agents.PackSkillDirs(), out.String()
}

// A CUSTOM `from` is honored: the staged source dir is the declared one.
func TestJailSkillsHonorsCustomFrom(t *testing.T) {
	o := localSkillsPack(t, "my-skills", "my-skills")
	dirs, warnings := stagedSkillDirs(t, o)
	if len(dirs) != 1 {
		t.Fatalf("skill dirs = %v, want exactly one", dirs)
	}
	if filepath.Base(dirs[0]) != "my-skills" {
		t.Errorf("staged skills from %q, want the declared my-skills/ — a hardcoded "+
			"<packRoot>/skills is the bug being fixed", dirs[0])
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("unexpected warning for a source that IS there:\n%s", warnings)
	}
}

// The DEFAULT still works: `from: "skills"` reads skills/, which every shipped pack needs.
func TestJailSkillsDefaultFromStillWorks(t *testing.T) {
	o := localSkillsPack(t, "skills", "skills")
	dirs, warnings := stagedSkillDirs(t, o)
	if len(dirs) != 1 || filepath.Base(dirs[0]) != "skills" {
		t.Fatalf("skill dirs = %v, want the conventional skills/", dirs)
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("unexpected warning:\n%s", warnings)
	}
}

// A pack with NO manifest still merges its skills/ — the zero-ceremony case the jail path
// depends on, and the one a naive "iterate the contributions" fix would drop.
func TestJailSkillsZeroCeremonyPackStillMerges(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "bare")
	writeSkillTree(t, filepath.Join(packDir, "skills"), "bareskill")
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"bare"}]`)

	o := &Options{Workspace: t.TempDir()}
	dirs, warnings := stagedSkillDirs(t, o)
	if len(dirs) != 1 || filepath.Base(dirs[0]) != "skills" {
		t.Fatalf("skill dirs = %v, want the conventional skills/ for a manifest-less pack", dirs)
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("unexpected warning:\n%s", warnings)
	}
}

// A declared source that is not in the staged content delivers nothing, and says so. A
// declaration yolo accepts and silently no-ops would just relocate the original defect.
func TestJailSkillsWarnsOnMissingDeclaredFrom(t *testing.T) {
	// The pack ships skills/ but declares my-skills/: the old code read skills/ regardless.
	o := localSkillsPack(t, "skills", "my-skills")
	dirs, warnings := stagedSkillDirs(t, o)
	if len(dirs) != 0 {
		t.Errorf("skill dirs = %v, want none — a missing declared source must not fall back "+
			"to skills/", dirs)
	}
	if !strings.Contains(warnings, "my-skills") {
		t.Errorf("no warning naming the missing source:\n%s", warnings)
	}
}

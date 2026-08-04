package cli

// applyhostskillsfrom_test.go is the HOST-NOTCH gate for the `skills` kind's `from` field.
//
// The bug: applyHostSkills passed `Sources: []string{filepath.Join(p.Root, "skills")}` — the
// conventional dir, whatever the contribution declared. A pack whose skills live in
// `my-skills/` delivered nothing to a real home, silently, and the observe posture agreed
// with the write because both read the same wrong path.
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never
// read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostSkillsFixture writes a pack whose skills tree lives at `srcDir`, declaring `from`, and
// selects it in a user-scope config under a temp home. srcDir "" writes no skills at all.
func hostSkillsFixture(t *testing.T, srcDir, from string) string {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "sf")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"sf","description":"d","contributes":[`+
			`{"kind":"skills","from":"`+from+`","into":".claude/skills","tier":"flat"}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "sf prose\n")
	if srcDir != "" {
		writeFile(t, filepath.Join(packDir, srcDir, "sfskill", "SKILL.md"),
			"---\nname: sfskill\ndescription: d\n---\nbody\n")
	}
	// `packs` is USER scope only, so the user config is the only place a pack can be named.
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+packDir+`","name":"sf"}]}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// A CUSTOM `from` is honored at the host notch: the skill from my-skills/ lands in the real
// (temp) home. This is the assertion the shipped code failed.
func TestApplyHostSkillsHonorsCustomFrom(t *testing.T) {
	home := hostSkillsFixture(t, "my-skills", "my-skills")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	dest := filepath.Join(home, ".claude", "skills", "sfskill", "SKILL.md")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the skill declared from my-skills/ did not reach %s: %v\nreport:\n%s",
			dest, err, report)
	}
}

// The DEFAULT still delivers: `from: "skills"` reads skills/. Asserted alongside the custom
// case because the fix must honor the declaration WITHOUT breaking the convention every
// shipped pack uses.
func TestApplyHostSkillsDefaultFromStillDelivers(t *testing.T) {
	home := hostSkillsFixture(t, "skills", "skills")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	dest := filepath.Join(home, ".claude", "skills", "sfskill", "SKILL.md")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the skill declared from skills/ did not reach %s: %v\nreport:\n%s",
			dest, err, report)
	}
}

// A declared source that is not in the pack is REFUSED BY NAME, not silently skipped — and
// specifically not silently read from skills/ instead, which is what the old code did.
func TestApplyHostSkillsRefusesMissingDeclaredFrom(t *testing.T) {
	// Ships skills/ but declares my-skills/: the old code delivered skills/ contents.
	home := hostSkillsFixture(t, "skills", "my-skills")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Errorf("a declared skills source that is missing must not be a silent success "+
			"(rc=0)\n%s", report)
	}
	if !strings.Contains(report, "my-skills") {
		t.Errorf("report does not name the missing source:\n%s", report)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "sfskill")); err == nil {
		t.Error("the conventional skills/ was delivered despite `from: my-skills` — the fix " +
			"must not fall back to the dir the declaration replaced")
	}
}

// OBSERVE agrees with ASSERT. Both postures resolve the source the same way, so a dry run
// cannot promise a delivery the write does not make (or the reverse).
func TestApplyHostSkillsObserveNamesCustomFrom(t *testing.T) {
	home := hostSkillsFixture(t, "my-skills", "my-skills")

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	if !strings.Contains(report, "sfskill") {
		t.Errorf("observe did not name the skill it would deliver from my-skills/:\n%s", report)
	}
	// And it wrote nothing.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "sfskill")); err == nil {
		t.Error("observe posture wrote to the home")
	}
}

// A WRAPPED PLUGIN under a custom source is delivered as a plugin, verbatim. A plugin is
// carried BY a skills contribution (it has no kind of its own), so `from` has to reach plugin
// discovery too — otherwise a pack that moves its skills loses its plugin's manifest,
// components and namespacing while its loose skills still arrive.
func TestApplyHostSkillsPluginUnderCustomFrom(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "wrapper")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"wrapper","description":"d","contributes":[`+
			`{"kind":"skills","from":"my-skills","into":".claude/skills","tier":"namespaced"}]}`)
	writeFile(t, filepath.Join(packDir, "my-skills", "acme-tools", ".claude-plugin", "plugin.json"),
		`{"name":"acme-tools","description":"third-party","skills":["./"]}`)
	writeFile(t, filepath.Join(packDir, "my-skills", "acme-tools", "skills", "deep", "SKILL.md"),
		"---\nname: deep\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+packDir+`","name":"wrapper"}]}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply --host --assert rc=%d\n%s", rc, report)
	}
	// The plugin arrives under its OWN name, with its manifest — the verbatim shape.
	man := filepath.Join(home, ".claude", "skills", "acme-tools", ".claude-plugin", "plugin.json")
	if _, err := os.Stat(man); err != nil {
		t.Fatalf("the plugin under my-skills/ was not delivered (%s missing): %v\nreport:\n%s",
			man, err, report)
	}
}

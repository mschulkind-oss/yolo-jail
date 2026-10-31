package jailcontent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shippedPrefixHome is a fake HOME whose user config sets shipped_skill_prefix to prefix ("" writes
// no key), so the jail staging reads it as a launch does (config.ShippedSkillPrefix).
func shippedPrefixHome(t *testing.T, prefix string) string {
	t.Helper()
	useRealShippedSkillPrefix(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	body := ""
	if prefix != "" {
		body = "shipped_skill_prefix = \"" + prefix + "\"\n"
	}
	must(t, os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(body), 0o644))
	return home
}

// stagedClaudeSkills runs the jail's skills staging with an applied fact sheet (so the generated
// jail-mechanics skill is written too) and returns the claude destination's staged tree.
func stagedClaudeSkills(t *testing.T, packSkills ...string) string {
	t.Helper()
	withPackSkillDirs(t, packSkills...)
	withSkillTargets(t, ".claude/skills")
	facts := &FactSheet{HostWorkspace: "/home/test/project", WorkspacePath: "/workspace",
		ConfigPath: "/workspace/yolo-jail.toml", Briefing: BriefingInput{Mechanism: "podman"}}
	staging, _, err := PrepareSkillsWithFactSheet("test-cname", nil, facts)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(staging, packSkillTargets[0].Staging)
}

// PN-D13: with shipped_skill_prefix set, every built-in skill is delivered as `<prefix>-<skill>`,
// its SKILL.md names it so, and its references to the other built-in skills follow; unset, the
// names are exactly what they were.
func TestBuiltinSkillsAreDeliveredUnderTheShippedSkillPrefix(t *testing.T) {
	for _, tc := range []struct{ prefix, want string }{{"", ""}, {"yolo", "yolo-"}} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			shippedPrefixHome(t, tc.prefix)
			dir := stagedClaudeSkills(t)
			for _, bare := range builtinSkillBareNames {
				name := tc.want + bare
				body, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
				if err != nil {
					t.Fatalf("built-in %s is not delivered as %s: %v", bare, name, err)
				}
				if !strings.Contains(string(body), "\nname: "+name+"\n") {
					t.Errorf("%s/SKILL.md does not name itself %s:\n%s", name, name, body)
				}
				if tc.prefix != "" {
					if _, err := os.Stat(filepath.Join(dir, bare)); !os.IsNotExist(err) {
						t.Errorf("the bare %s is delivered too (err=%v)", bare, err)
					}
				}
			}
			configuring, _ := os.ReadFile(filepath.Join(dir, tc.want+"configuring-the-jail", "SKILL.md"))
			if !strings.Contains(string(configuring), "**"+tc.want+"jail-mechanics**") {
				t.Errorf("configuring-the-jail does not refer to the mechanics skill as %sjail-mechanics:\n%s",
					tc.want, configuring)
			}
		})
	}
}

// A pack's skill overrides a built-in one by either name, as on main: a bare pack skill called
// configuring-the-jail still keeps yolo's from being delivered when the prefix renames yolo's.
func TestAPackSkillOverridesAPrefixedBuiltinByEitherName(t *testing.T) {
	for _, packName := range []string{"configuring-the-jail", "yolo-configuring-the-jail"} {
		t.Run(packName, func(t *testing.T) {
			home := shippedPrefixHome(t, "yolo")
			skills := filepath.Join(home, "pack", "skills")
			must(t, os.MkdirAll(filepath.Join(skills, packName), 0o755))
			must(t, os.WriteFile(filepath.Join(skills, packName, "SKILL.md"),
				[]byte("---\nname: "+packName+"\ndescription: mine\n---\nPACK\n"), 0o644))
			dir := stagedClaudeSkills(t, skills)
			if body, _ := os.ReadFile(filepath.Join(dir, packName, "SKILL.md")); !strings.Contains(string(body), "PACK") {
				t.Errorf("the pack's %s is not delivered: %q", packName, body)
			}
			for _, name := range []string{"configuring-the-jail", "yolo-configuring-the-jail"} {
				if name == packName {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Errorf("yolo's built-in is delivered as %s beside the pack's override %s (err=%v)",
						name, packName, err)
				}
			}
		})
	}
}

// The briefing names each built-in skill as it is delivered.
func TestTheBriefingNamesBuiltinSkillsUnderTheShippedSkillPrefix(t *testing.T) {
	for _, tc := range []struct{ prefix, want string }{{"", ""}, {"yolo", "yolo-"}} {
		body := BriefingContent(BriefingInput{ShippedSkillPrefix: tc.prefix})
		for _, bare := range builtinSkillBareNames {
			if !strings.Contains(body, "**"+tc.want+bare+"**") {
				t.Errorf("prefix %q: the briefing does not name **%s%s**:\n%s", tc.prefix, tc.want, bare, body)
			}
		}
		if tc.prefix != "" && strings.Contains(body, "**configuring-the-jail**") {
			t.Errorf("prefix %q: the briefing still names the bare skill", tc.prefix)
		}
	}
}

// The mechanics refresh after a live apply rewrites the skill where it was delivered.
func TestTheMechanicsRefreshWritesThePrefixedSkill(t *testing.T) {
	shippedPrefixHome(t, "yolo")
	dir := stagedClaudeSkills(t)
	facts := FactSheet{HostWorkspace: "/home/test/other", WorkspacePath: "/workspace",
		ConfigPath: "/workspace/yolo-jail.toml", Briefing: BriefingInput{Mechanism: "podman"}}
	if err := RefreshMechanicsFiles([]string{dir}, facts); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "yolo-jail-mechanics", "SKILL.md"))
	if err != nil || !strings.Contains(string(body), "\nname: yolo-jail-mechanics\n") {
		t.Errorf("the refreshed mechanics skill is not yolo-jail-mechanics (err=%v):\n%s", err, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "jail-mechanics")); !os.IsNotExist(err) {
		t.Errorf("the refresh wrote a bare jail-mechanics (err=%v)", err)
	}
}

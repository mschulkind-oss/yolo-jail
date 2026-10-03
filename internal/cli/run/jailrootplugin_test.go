package run

// jailrootplugin_test.go pins how a jail delivers a Claude Code MOD in Claude's own shape: a pack
// whose root IS the plugin (.claude-plugin/plugin.json) and whose only component is a
// hooks/hooks.json naming a hooks module, with no skills/ folder at all
// (docs/research/claude-code-mods-management.md, G10).
//
// The host composition writes such a plugin before it looks at sources
// (hostskills.writeLayer), while the jail attached a root plugin only to the pack's skills
// sources, so a pack with no skills/ folder reached `yolo host apply` whole and no jail at all.
// The two notches delivering one pack differently is what the 2026-09-28 parity ruling removed
// (docs/plans/notch-convergence.md#OQ-NC11).
//
// Driven through stagePacks and refreshJailBriefings, the launch's own staging path, for the
// reason skillcollision_test.go gives: a test of jailSkillSources alone stays green with the
// call site deleted.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// rootModFiles is a mod as Claude Code's own tutorial lays it out: a manifest declaring no
// component, and a hooks/hooks.json carrying a command hook and a `modules` entry naming the
// JavaScript hooks module beside it. Delivered, it passes `claude plugin validate` (2.1.288, with
// warnings for the missing version and author and for yolo's marker), which lists the module's
// `hooks: session.start` and `calls: $.ui.status`.
var rootModFiles = map[string]string{
	".claude-plugin/plugin.json": `{"name":"first-mod","description":"a mod in Claude Code's own shape"}`,
	"hooks/hooks.json": `{"hooks":{"PostToolUse":[{"matcher":"Write","hooks":[` +
		`{"type":"command","command":"echo written"}]}]},"modules":["./register.js"]}`,
	"hooks/register.js": "export function register(on) {\n  on(\"session.start\", ($) => {\n" +
		"    $.ui.status(\"first-mod is loaded\");\n  });\n}\n",
}

// writeRootMod writes the mod at <base>/<dir> with the given pack.json ("" writes none) and the
// manifest name `name`, and returns the `packs` entry naming it.
func writeRootMod(t *testing.T, base, dir, name, packJSON string) string {
	t.Helper()
	root := filepath.Join(base, dir)
	for rel, body := range rootModFiles {
		if rel == ".claude-plugin/plugin.json" {
			body = strings.Replace(body, `"first-mod"`, `"`+name+`"`, 1)
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if packJSON != "" {
		writePack(t, root, packJSON)
	}
	return `{"source":"file://` + root + `","name":"` + dir + `"}`
}

// assertModDeliveredWhole checks every file of the mod sits under dest/<name>, byte for byte,
// except the manifest, which carries yolo's ownership marker beside the mod's own fields.
func assertModDeliveredWhole(t *testing.T, dest, name string) {
	t.Helper()
	for rel, body := range rootModFiles {
		got, err := os.ReadFile(filepath.Join(dest, name, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("the mod's %s did not reach the jail's skills folder: %v", rel, err)
			continue
		}
		if rel == ".claude-plugin/plugin.json" {
			var m map[string]any
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("the delivered manifest does not parse: %v", err)
			}
			if m["name"] != name || m["x-yolo-managed-by"] != "yolo-jail" {
				t.Errorf("the delivered manifest lost its name or lacks yolo's marker: %v", m)
			}
			continue
		}
		if string(got) != body {
			t.Errorf("the mod's %s arrived changed:\n%s", rel, got)
		}
	}
}

// A NAMESPACED pack whose root is a mod, with no skills/ folder, reaches the jail whole: the mod
// lands at ~/.claude/skills/first-mod/, its hooks module included, exactly as the host writes it.
// Both shapes the research measured: the pack declaring the destination itself, and the pack
// declaring only its tier, which reaches the jail's destinations by the jail's own fan-out
// (packskillsdelivery_test.go) like any other pack's skills.
func TestAJailDeliversARootModWithNoSkillsFolder(t *testing.T) {
	for _, tc := range []struct{ name, packJSON string }{
		{"declaring its destination", `{"name":"first-mod","skills_tier":"namespaced",` +
			`"contributes":[{"kind":"skills","into":".claude/skills"}]}`},
		{"declaring only its tier", `{"name":"first-mod","skills_tier":"namespaced"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `["claude",`+writeRootMod(t, t.TempDir(), "first-mod", "first-mod", tc.packJSON)+`]`)
			staging, _ := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-g10-ns")
			assertModDeliveredWhole(t, filepath.Join(staging, jailcontent.SkillStagingName("claude")), "first-mod")
		})
	}
}

// THE FLAT DEFAULT STILL HOLDS: a pack with no pack.json is flat, and a flat skills folder can
// carry a plugin's skills and nothing else (hostskills.deliverPluginFlat). So the mod does not
// arrive — and the launch now SAYS so, naming the hooks that cannot, where it used to stage
// nothing and say nothing.
func TestAJailNamesAFlatRootModThatCannotArrive(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude",`+writeRootMod(t, t.TempDir(), "first-mod", "first-mod", "")+`]`)
	staging, said := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-g10-flat")
	dest := filepath.Join(staging, jailcontent.SkillStagingName("claude"))
	if _, err := os.Stat(filepath.Join(dest, "first-mod")); !os.IsNotExist(err) {
		t.Errorf("a flat pack's mod was copied whole into the jail's skills folder (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "hooks")); !os.IsNotExist(err) {
		t.Errorf("a flat pack's mod spilled its hooks/ into the jail's skills folder (%v)", err)
	}
	for _, want := range []string{"Skills:", "first-mod:hooks", "cannot arrive", "~/.claude/skills"} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch did not say %q about the mod a flat pack cannot deliver:\n%s", want, said)
		}
	}
}

// THE RESERVED CHILD STILL HOLDS on the new path: a mod that calls itself `synced`, the name
// packs/claude reserves for Claude Code's sync root, is withheld from ~/.claude/skills and said.
func TestAJailWithholdsARootModUnderAReservedName(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude",`+writeRootMod(t, t.TempDir(), "sync-mod", "synced",
		`{"name":"sync-mod","skills_tier":"namespaced"}`)+`]`)
	staging, said := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-g10-reserved")
	dest := filepath.Join(staging, jailcontent.SkillStagingName("claude"))
	if _, err := os.Lstat(filepath.Join(dest, "synced")); !os.IsNotExist(err) {
		t.Errorf("a mod named for the reserved child reached the jail's ~/.claude/skills (%v)", err)
	}
	for _, want := range []string{"synced", "pack sync-mod", "withheld"} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch did not say %q about the withheld mod:\n%s", want, said)
		}
	}
}

// THE AUDIENCE STILL HOLDS: a pack that addresses its skills to one agent sends its root mod to
// that agent's destination only, as it would had its skills/ folder existed for the mod to ride.
func TestAJailSendsARootModOnlyToTheAgentItsPackAddresses(t *testing.T) {
	home := packHome(t)
	base := t.TempDir()
	writeUserPacks(t, home, "["+agentSkillsPack(t, base, "alphacli")+`,"claude",`+
		writeRootMod(t, base, "first-mod", "first-mod", `{"name":"first-mod","skills_tier":"namespaced",`+
			`"contributes":[{"kind":"skills","agents":["claude"]}]}`)+`]`)
	staging, _ := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-g10-audience")
	assertModDeliveredWhole(t, filepath.Join(staging, jailcontent.SkillStagingName("claude")), "first-mod")
	if _, err := os.Stat(filepath.Join(staging, jailcontent.SkillStagingName("alphacli"), "first-mod")); !os.IsNotExist(err) {
		t.Errorf("a mod whose pack addresses its skills to claude reached alphacli's skills folder (%v)", err)
	}
}

// A MOD'S NAME IS ONE FOLDER: the staging joins the manifest's name onto a scratch skills dir in
// the HOST's temp dir, so "../../<x>" used to land in that temp dir itself, outside everything the
// staging owns, and "synced/<x>" inside the child packs/claude reserves. Both are refused, and the
// launch names the rename to make.
func TestAJailRefusesARootModWhoseNameIsAPath(t *testing.T) {
	for _, tc := range []struct{ label, name string }{
		{"leaving the staging", "../../zz-escaped-mod"},
		{"entering a reserved child", "synced/planted"},
	} {
		name := tc.name
		t.Run(tc.label, func(t *testing.T) {
			home := packHome(t)
			tmp := t.TempDir()
			mods := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			writeUserPacks(t, home, `["claude",`+writeRootMod(t, mods, "first-mod", name,
				`{"name":"first-mod","skills_tier":"namespaced"}`)+`]`)
			staging, said := stageJailSkills(t, goldenOptions(t.TempDir(), home), "yolo-test-g10-pathname")
			for _, p := range []string{
				filepath.Join(tmp, "zz-escaped-mod"),
				filepath.Join(staging, jailcontent.SkillStagingName("claude"), "synced", "planted"),
			} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Errorf("a mod named %q was written to %s (%v)", name, p, err)
				}
			}
			for _, want := range []string{"Skills:", "not a plain folder name", "set `name` in its .claude-plugin/plugin.json"} {
				if !strings.Contains(said, want) {
					t.Errorf("the launch did not say %q about a mod named %q:\n%s", want, name, said)
				}
			}
		})
	}
}

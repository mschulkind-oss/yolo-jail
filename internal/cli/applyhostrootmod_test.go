package cli

// applyhostrootmod_test.go pins how `yolo host apply` delivers a Claude Code MOD in Claude's own
// shape: a pack whose root IS the plugin (.claude-plugin/plugin.json) and whose only component is
// a hooks/hooks.json naming a hooks module, with no skills/ folder at all
// (docs/research/claude-code-mods-management.md, G10).
//
// The jail delivers such a pack to the destinations its skills are addressed to
// (packload.Pack.SkillsAudience, run.jailSkillSources; pinned by run's jailrootplugin_test.go). The
// host resolved a destination for it only when its pack.json named one with `into`: a pack saying
// only "skills_tier": "namespaced", or no pack.json at all, carries no skill directory, so the
// destination inference found nothing to route and the mod reached no home, with nothing said.
// One pack delivered by one notch and not the other is what the 2026-09-28 parity ruling removed
// (docs/plans/notch-convergence.md#OQ-NC11).
//
// Driven through applyHost, the command's own entry point, so deleting the resolution's call site
// fails these. Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME
// is never read or written.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostRootModFiles is the jail test's mod, byte for byte (run's rootModFiles): a manifest declaring
// no component, and a hooks/hooks.json carrying a command hook and a `modules` entry naming the
// JavaScript hooks module beside it.
var hostRootModFiles = map[string]string{
	".claude-plugin/plugin.json": `{"name":"first-mod","description":"a mod in Claude Code's own shape"}`,
	"hooks/hooks.json": `{"hooks":{"PostToolUse":[{"matcher":"Write","hooks":[` +
		`{"type":"command","command":"echo written"}]}]},"modules":["./register.js"]}`,
	"hooks/register.js": "export function register(on) {\n  on(\"session.start\", ($) => {\n" +
		"    $.ui.status(\"first-mod is loaded\");\n  });\n}\n",
}

// writeHostRootMod writes the mod at <base>/<dir> with the given pack.json ("" writes none) and
// returns the `packs` entry naming it.
func writeHostRootMod(t *testing.T, base, dir, packJSON string) string {
	t.Helper()
	root := filepath.Join(base, dir)
	for rel, body := range hostRootModFiles {
		writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	if packJSON != "" {
		writeFile(t, filepath.Join(root, "pack.json"), packJSON)
	}
	return `{"source":"file://` + root + `","name":"` + dir + `"}`
}

// hostRootModHome selects `list` (a raw `packs` fragment) under a fresh temp home.
func hostRootModHome(t *testing.T, list string) string {
	t.Helper()
	home := t.TempDir()
	selectPacks(t, home, list)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// assertHostModDeliveredWhole checks every file of the mod sits under dest/first-mod, byte for
// byte, except the manifest, which carries yolo's ownership marker beside the mod's own fields.
func assertHostModDeliveredWhole(t *testing.T, dest, report string) {
	t.Helper()
	for rel, body := range hostRootModFiles {
		got, err := os.ReadFile(filepath.Join(dest, "first-mod", filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("the mod's %s did not reach %s: %v\nreport:\n%s", rel, dest, err, report)
			continue
		}
		if rel == ".claude-plugin/plugin.json" {
			var m map[string]any
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("the delivered manifest does not parse: %v", err)
			}
			if m["name"] != "first-mod" || m["x-yolo-managed-by"] != "yolo-jail" {
				t.Errorf("the delivered manifest lost its name or lacks yolo's marker: %v", m)
			}
			continue
		}
		if string(got) != body {
			t.Errorf("the mod's %s arrived changed:\n%s", rel, got)
		}
	}
}

// A NAMESPACED pack whose root is a mod, with no skills/ folder, reaches the home whole: the mod
// lands at ~/.claude/skills/first-mod/, its hooks module included. Both shapes the jail test
// measures: the pack declaring the destination itself, which always worked here, and the pack
// declaring only its tier, which reached no home.
func TestApplyHostDeliversARootModWithNoSkillsFolder(t *testing.T) {
	for _, tc := range []struct{ name, packJSON string }{
		{"declaring its destination", `{"name":"first-mod","skills_tier":"namespaced",` +
			`"contributes":[{"kind":"skills","into":".claude/skills"}]}`},
		{"declaring only its tier", `{"name":"first-mod","skills_tier":"namespaced"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := hostRootModHome(t, `"claude",`+writeHostRootMod(t, t.TempDir(), "first-mod", tc.packJSON))
			rc, report := applyWith(t, true, strings.NewReader("y\n"))
			if rc != 0 {
				t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
			}
			assertHostModDeliveredWhole(t, filepath.Join(home, ".claude", "skills"), report)
		})
	}
}

// A pack silent about where its skills go broadcasts them, so its root mod reaches every agent
// the set names, as in the jail: here claude's skills folder and pi's.
func TestApplyHostSendsATierOnlyRootModToEveryAgent(t *testing.T) {
	home := hostRootModHome(t, `"claude","pi",`+writeHostRootMod(t, t.TempDir(), "first-mod",
		`{"name":"first-mod","skills_tier":"namespaced"}`))
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	assertHostModDeliveredWhole(t, filepath.Join(home, ".claude", "skills"), report)
	assertHostModDeliveredWhole(t, filepath.Join(home, ".pi", "agent", "skills"), report)
}

// A DECLARATION IS HONORED EXACTLY: a pack that names its own skills destination delivers its mod
// there and is not widened into another agent's home, as its skills are not
// (TestApplyHostDeclaringPackIsUnaffectedByTheInference).
func TestApplyHostKeepsARootModToTheDestinationItsPackNames(t *testing.T) {
	home := hostRootModHome(t, `"claude","pi",`+writeHostRootMod(t, t.TempDir(), "first-mod",
		`{"name":"first-mod","skills_tier":"namespaced","contributes":[{"kind":"skills","into":".claude/skills"}]}`))
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	assertHostModDeliveredWhole(t, filepath.Join(home, ".claude", "skills"), report)
	if _, err := os.Lstat(filepath.Join(home, ".pi", "agent", "skills", "first-mod")); !os.IsNotExist(err) {
		t.Errorf("a mod whose pack names ~/.claude/skills was widened into pi's skills folder (%v)\n%s",
			err, report)
	}
}

// THE AUDIENCE STILL HOLDS: a pack that addresses its skills to one agent sends its root mod to
// that agent's skills folder only, as the jail does.
func TestApplyHostSendsARootModOnlyToTheAgentItsPackAddresses(t *testing.T) {
	home := hostRootModHome(t, `"claude","pi",`+writeHostRootMod(t, t.TempDir(), "first-mod",
		`{"name":"first-mod","skills_tier":"namespaced","contributes":[{"kind":"skills","agents":["claude"]}]}`))
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	assertHostModDeliveredWhole(t, filepath.Join(home, ".claude", "skills"), report)
	if _, err := os.Lstat(filepath.Join(home, ".pi", "agent", "skills", "first-mod")); !os.IsNotExist(err) {
		t.Errorf("a mod whose pack addresses its skills to claude reached pi's skills folder (%v)\n%s",
			err, report)
	}
}

// A root mod beside a skills folder addressed to claude reaches claude ONCE, riding that folder's
// delivery, and still reaches pi, where the pack's unnamed skills/ would have broadcast it. A
// second delivery of one plugin to one folder is refused as two packs wanting one name, so a
// destination the pack already reaches must not be inferred for its mod a second time. (The
// folder's own skill travels inside the mod's tree: every source of a pack whose root is a plugin
// sits inside that plugin, hostskills.collectSkills.)
func TestApplyHostDeliversARootModOnceBesideAnAddressedSource(t *testing.T) {
	base := t.TempDir()
	entry := writeHostRootMod(t, base, "modpack", `{"name":"modpack","skills_tier":"namespaced",`+
		`"contributes":[{"kind":"skills","from":"extra","agents":["claude"]}]}`)
	writeFile(t, filepath.Join(base, "modpack", "extra", "xskill", "SKILL.md"),
		"---\nname: xskill\ndescription: d\n---\nbody\n")
	home := hostRootModHome(t, `"claude","pi",`+entry)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "already delivers") {
		t.Errorf("the mod was delivered to one folder twice:\n%s", report)
	}
	assertHostModDeliveredWhole(t, filepath.Join(home, ".claude", "skills"), report)
	assertHostModDeliveredWhole(t, filepath.Join(home, ".pi", "agent", "skills"), report)
}

// A PLUGIN INSIDE A SKILLS SOURCE IS NOT A ROOT PLUGIN: it belongs to that source and reaches
// only the agents the source is addressed to, as in the jail (run.jailSkillSources). Only a plugin
// in none of the pack's sources is routed to the pack's whole skills audience, so a pack whose one
// source, addressed to claude, holds the mod must not have it inferred into pi's skills folder.
func TestApplyHostKeepsAPluginInAnAddressedSourceToThatSourcesAgent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wrapmod")
	for rel, body := range hostRootModFiles {
		writeFile(t, filepath.Join(root, "extra", "first-mod", filepath.FromSlash(rel)), body)
	}
	writeFile(t, filepath.Join(root, "pack.json"), `{"name":"wrapmod","skills_tier":"namespaced",`+
		`"contributes":[{"kind":"skills","from":"extra","agents":["claude"]}]}`)
	home := hostRootModHome(t, `"claude","pi",{"source":"file://`+root+`","name":"wrapmod"}`)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	assertHostModDeliveredWhole(t, filepath.Join(home, ".claude", "skills"), report)
	if _, err := os.Lstat(filepath.Join(home, ".pi", "agent", "skills", "first-mod")); !os.IsNotExist(err) {
		t.Errorf("a mod inside a skills source addressed to claude reached pi's skills folder (%v)\n%s",
			err, report)
	}
}

// THE FLAT DEFAULT STILL HOLDS: a pack with no pack.json is flat, and a flat skills folder can
// carry a plugin's skills and nothing else (hostskills.deliverPluginFlat). So the mod does not
// arrive, and the apply SAYS so, naming the hooks that cannot and the tier that would carry them,
// where it used to say nothing.
func TestApplyHostNamesAFlatRootModThatCannotArrive(t *testing.T) {
	home := hostRootModHome(t, `"claude",`+writeHostRootMod(t, t.TempDir(), "first-mod", ""))
	_, report := applyWith(t, true, strings.NewReader("y\n"))
	dest := filepath.Join(home, ".claude", "skills")
	if _, err := os.Lstat(filepath.Join(dest, "first-mod")); !os.IsNotExist(err) {
		t.Errorf("a flat pack's mod was copied whole into ~/.claude/skills (%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "hooks")); !os.IsNotExist(err) {
		t.Errorf("a flat pack's mod spilled its hooks/ into ~/.claude/skills (%v)", err)
	}
	for _, want := range []string{"first-mod:hooks", "cannot arrive", "set `skills_tier` to `namespaced`",
		"pack first-mod's pack.json"} {
		if !strings.Contains(report, want) {
			t.Errorf("the apply did not say %q about the mod a flat pack cannot deliver:\n%s", want, report)
		}
	}
}

// A root mod selected with no agent pack reaches nothing, and is refused by name like any other
// content pack that renders nothing (TestApplyHostZeroCeremonyWithNoAgentPackIsRefused).
func TestApplyHostRefusesARootModNoAgentPackReceives(t *testing.T) {
	hostRootModHome(t, writeHostRootMod(t, t.TempDir(), "first-mod",
		`{"name":"first-mod","skills_tier":"namespaced"}`))
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Errorf("a mod that reaches no home exited 0 — it must be refused by name:\n%s", report)
	}
	if !strings.Contains(report, "first-mod") || !strings.Contains(report, "no pack in `packs` names a") {
		t.Errorf("the refusal does not name the pack and the cause:\n%s", report)
	}
}

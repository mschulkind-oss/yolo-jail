package cli

// shippedskillprefixhost_test.go pins the host side of a pack skill meeting a built-in under
// shipped_skill_prefix (docs/design/pack-naming.md PN-D13): either name is a clash, and a render
// that fails after the prefix changed keeps the suite under the names it has.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
)

// A pack's skill named a built-in's bare or prefixed name is refused as a clash at the host,
// whatever the prefix, as it is with none. Fails if addHostBuiltinLayer drops the bare names.
func TestAHostPackSkillUnderEitherBuiltinNameIsAClash(t *testing.T) {
	for _, tc := range []struct{ prefix, packSkill string }{
		{"", "configuring-the-jail"},
		{"yolo", "configuring-the-jail"},
		{"yolo", "yolo-configuring-the-jail"},
		{"yolo", "jail-mechanics"},
		{"yolo", "yolo-jail-mechanics"},
	} {
		t.Run(tc.prefix+"/"+tc.packSkill, func(t *testing.T) {
			packSkills := t.TempDir()
			writeFile(t, filepath.Join(packSkills, tc.packSkill, "SKILL.md"), "---\nname: "+tc.packSkill+"\n---\n")
			root := t.TempDir()
			for _, bare := range jailcontent.BuiltinSkillBareNames() {
				name := jailcontent.BuiltinSkillName(tc.prefix, bare)
				writeFile(t, filepath.Join(root, "destination-0", name, "SKILL.md"), "---\nname: "+name+"\n---\n")
			}
			dests := []hostskills.Destination{{Dir: filepath.Join(t.TempDir(), ".claude", "skills"), Agent: "claude",
				Layers: []hostskills.Layer{{Pack: "acme/x", Sources: []string{packSkills}}}}}
			dests = addHostBuiltinLayer(dests, root, tc.prefix)
			cols := hostskills.Collisions(dests)
			if len(cols) != 1 || cols[0].Name != tc.packSkill {
				t.Fatalf("collisions = %+v, want one on %s", cols, tc.packSkill)
			}
		})
	}
}

// A built-in render that fails right after shipped_skill_prefix changed keeps the suite under the
// names it was delivered under, rather than retiring it for the new names it does not have yet.
// Fails if the fallback copies only the names the new prefix gives.
func TestAHostRenderFailureAfterAPrefixChangeKeepsTheDeliveredSuite(t *testing.T) {
	home := setupHostMechanicsTest(t)
	selectPacksWith(t, home, `"claude","codex","pi"`, "")
	if rc, report := applyWith(t, true, strings.NewReader("y\ny\ny\n")); rc != 0 {
		t.Fatalf("initial host apply rc=%d\n%s", rc, report)
	}
	claude := filepath.Join(home, ".claude", "skills")
	before := map[string][]byte{}
	for _, bare := range jailcontent.BuiltinSkillBareNames() {
		body, err := os.ReadFile(filepath.Join(claude, bare, "SKILL.md"))
		if err != nil {
			t.Fatalf("the initial apply did not deliver %s: %v", bare, err)
		}
		before[bare] = body
	}
	selectPacksWith(t, home, `"claude","codex","pi"`, `,"shipped_skill_prefix":"yolo"`)
	oldRenderer := renderHostBuiltinSkills
	renderHostBuiltinSkills = func(_ jailcontent.FactSheet) (map[string][]byte, error) {
		return nil, fmt.Errorf("injected render failure")
	}
	t.Cleanup(func() { renderHostBuiltinSkills = oldRenderer })
	rc, report := applyWith(t, true, strings.NewReader("y\ny\ny\n"))
	if rc != 0 {
		t.Fatalf("a render failure must not fail host apply (rc=%d)\n%s", rc, report)
	}
	for bare, want := range before {
		got, err := os.ReadFile(filepath.Join(claude, bare, "SKILL.md"))
		if err != nil || string(got) != string(want) {
			t.Errorf("the delivered %s was not kept (err=%v)\n%s", bare, err, report)
		}
	}
}

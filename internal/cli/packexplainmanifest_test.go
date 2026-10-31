package cli

// packexplainmanifest_test.go pins `yolo pack explain`'s delivered-names line for a pack with a
// manifest whose skills are in the conventional skills/ tree (docs/design/pack-naming.md §8): each
// skill under the name agents see, and whether the entry is bare or namespaced.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackExplainListsTheDeliveredSkillsOfAPackWithAManifest(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, body, entry, wantMode, wantSkill string
	}{
		{"pack.toml namespaced", "pack.toml", "name = \"mypack\"\n", "'src/mypack'", "(namespace src)", "src-review"},
		{"pack.toml bare", "pack.toml", "name = \"mypack\"\n", "{ pack = 'src/mypack', bare = true }", "(bare)", "review"},
		{"pack.json namespaced", "pack.json", `{"name":"mypack","contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`,
			"'src/mypack'", "(namespace src)", "src-review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("YOLO_VERSION", "")
			sources := t.TempDir()
			pack := filepath.Join(sources, "mypack")
			writeFile(t, filepath.Join(pack, tc.manifest), tc.body)
			writeFile(t, filepath.Join(pack, "skills", "review", "SKILL.md"), "---\nname: review\ndescription: d\n---\n")
			cfg := filepath.Join(home, ".config", "yolo-jail")
			writeFile(t, filepath.Join(cfg, "config.toml"), "packs = ["+tc.entry+"]\n\n[pack_sources]\n"+
				"src = { path = '"+filepath.ToSlash(sources)+"' }\n")

			var out, errw bytes.Buffer
			if rc := packMain([]string{"explain", "src/mypack"}, &out, &errw, false); rc != 0 {
				t.Fatalf("explain rc=%d: %s", rc, errw.String())
			}
			got := out.String()
			if !strings.Contains(got, "delivers 1 skill(s) "+tc.wantMode) {
				t.Errorf("explain does not say the pack delivers one skill %s:\n%s", tc.wantMode, got)
			}
			if !strings.Contains(got, "\n  "+tc.wantSkill+"\n") {
				t.Errorf("explain does not list the skill as %s:\n%s", tc.wantSkill, got)
			}
			if _, err := os.Stat(filepath.Join(pack, tc.manifest)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

package hostskills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// PN-D13: a shipped pack's skills are delivered under the user's shipped_skill_prefix, and bare
// without one; a third-party pack's namespace is its own either way. Read through the real user
// config in a fake HOME, so PackLayer's read of the key is what is pinned.
func TestAShippedPacksSkillsTakeTheShippedSkillPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"", []string{"acme-review", "review-it"}},
		{"yolo", []string{"acme-review", "yolo-review-it"}},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			useRealShippedSkillPrefix(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			body := ""
			if tc.prefix != "" {
				body = "shipped_skill_prefix = \"" + tc.prefix + "\"\n"
			}
			if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			layers := []Layer{
				exampleLayer(t, "claude", "", true, "review-it"),
				exampleLayer(t, "acme/tools", "acme", false, "review"),
			}
			if got := composedNames(t, layers); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("delivered %v, want %v", got, tc.want)
			}
			if tc.prefix != "" {
				dir := t.TempDir()
				if _, err := ComposeInto(Destination{Dir: "~/.claude/skills", Layers: layers}, dir); err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(filepath.Join(dir, "yolo-review-it", "SKILL.md"))
				if !strings.Contains(string(data), "name: yolo-review-it") {
					t.Errorf("the shipped skill's SKILL.md does not carry its delivered name:\n%s", data)
				}
			}
		})
	}
}

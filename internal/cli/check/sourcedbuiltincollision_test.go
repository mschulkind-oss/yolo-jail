package check

import (
	"bytes"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSectionPacksSourcedBuiltinCollisionIncludesUnfetchedPrefixAliases(t *testing.T) {
	for _, prefix := range []string{"", "yolo"} {
		for _, name := range jailcontent.BuiltinSkillClaimNames(prefix) {
			t.Run(prefix+"/"+name, func(t *testing.T) {
				owner := t.TempDir()
				if err := os.WriteFile(filepath.Join(owner, "pack.toml"), []byte(fmt.Sprintf("[[skills]]\nsource = %q\nas = %q\n[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n", "git+https://example.invalid/repository//skills/chosen?ref=main", name)), 0644); err != nil {
					t.Fatal(err)
				}
				packsFixture(t, fmt.Sprintf(`{"shipped_skill_prefix":%q,"packs":[{"name":"selected","source":%q}]}`, prefix, "file://"+owner))
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
				t.Setenv("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local/share"))
				var output bytes.Buffer
				(&Options{Getenv: hostEnv}).sectionPacks(&reporter{w: &output}, jsonx.NewOrderedMap())
				text := output.String()
				if !strings.Contains(text, "[FAIL]") || !strings.Contains(text, "yolo's built-in skill") || !strings.Contains(text, "selected") {
					t.Fatalf("builtin static claims absent:\n%s", text)
				}
				assertNoMirrors(t)
			})
		}
	}
}

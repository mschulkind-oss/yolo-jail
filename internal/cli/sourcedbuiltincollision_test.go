package cli

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostApplySourcedBuiltinNamesAndPrefixAliasesRefuseUnfetched(t *testing.T) {
	for _, prefix := range []string{"", "yolo"} {
		for _, name := range jailcontent.BuiltinSkillClaimNames(prefix) {
			t.Run(prefix+"/"+name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
				t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
				owner := t.TempDir()
				writeFile(t, filepath.Join(owner, "pack.toml"), fmt.Sprintf("[[skills]]\nsource = %q\nas = %q\n[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n", "git+https://example.invalid/repository//skills/chosen?ref=main", name))
				writeFile(t, filepath.Join(home, ".config/yolo-jail/config.jsonc"), fmt.Sprintf(`{"shipped_skill_prefix":%q,"packs":[{"name":"selected","source":%q}]}`, prefix, "file://"+owner))
				rc, report := applyWith(t, false, strings.NewReader(""))
				if rc == 0 || !strings.Contains(report, "selected") || !strings.Contains(report, "yolo's built-in skill") || !strings.Contains(report, "collision") {
					t.Fatalf("builtin collision not refused: rc=%d\n%s", rc, report)
				}
				if _, err := os.Lstat(filepath.Join(home, ".local/share/yolo-jail/packs/mirrors")); !os.IsNotExist(err) {
					t.Fatalf("preview fetched source: %v", err)
				}
			})
		}
	}
}

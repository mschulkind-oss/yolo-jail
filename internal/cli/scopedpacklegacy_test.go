package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestHostApplyScopedModernBesideLegacyInclusionKeepsWithholding(t *testing.T) {
	user := testconfig.User(t, `include_if_found = ["legacy.jsonc"]
packs = ["local/reader", {pack="local/modern", skills={only=["marked", "folder"]}}]
`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(user.Root, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(user.Root, ".local/share"))
	root := filepath.Dir(user.Path)
	legacy := filepath.Join(root, "packs", "legacy")
	scopedCLIWrite(t, filepath.Join(root, "packs/reader/pack.toml"), "[[skills]]\nagent = \"fixture\"\ninto = \".fixture/skills\"\n")
	scopedCLIWrite(t, filepath.Join(root, "packs/modern/skills/marked/SKILL.md"), "MODERN-MARKER\n")
	scopedCLIWrite(t, filepath.Join(root, "packs/modern/skills/folder/notes.md"), "MODERN-FOLDER\n")
	scopedCLIWrite(t, filepath.Join(legacy, "skills/folder/notes.md"), "LEGACY-FOLDER\n")
	scopedCLIWrite(t, filepath.Join(legacy, "pack.toml"), "[[skills]]\nsource = \"git+https://example.invalid/never/fetch//skills/remote?ref=main\"\nas = \"remote\"\n")
	scopedCLIWrite(t, filepath.Join(root, "legacy.jsonc"), fmt.Sprintf(`{"packs":[{"source":%q,"name":"old","only":["pack.toml","skills/**"]}]}`, "file://"+filepath.ToSlash(legacy)))
	spy := filepath.Join(t.TempDir(), "git")
	marker := spy + ".called"
	scopedCLIWrite(t, spy, "#!/bin/sh\nprintf called > \"$SCOPED_GIT_SPY\"\nexit 91\n")
	if err := os.Chmod(spy, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCOPED_GIT_SPY", marker)
	t.Setenv("PATH", filepath.Dir(spy)+string(os.PathListSeparator)+os.Getenv("PATH"))
	rc, report := applyWith(t, true, strings.NewReader(""))
	if rc != 0 {
		t.Fatalf("mixed modern/legacy host apply: %d %s", rc, report)
	}
	for name, want := range map[string]string{"modern-marked/SKILL.md": "MODERN-MARKER", "modern-folder/notes.md": "MODERN-FOLDER", "folder/notes.md": "LEGACY-FOLDER"} {
		data, err := os.ReadFile(filepath.Join(user.Root, ".fixture/skills", name))
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("mixed delivery %s: %q %v\n%s", name, data, err, report)
		}
	}
	for _, name := range []string{"modern-folder/SKILL.md", "folder/SKILL.md", "remote/SKILL.md"} {
		if _, err := os.Lstat(filepath.Join(user.Root, ".fixture/skills", name)); !os.IsNotExist(err) {
			t.Errorf("invented marker or unwithheld legacy source %s: %v", name, err)
		}
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("legacy inclusion contacted Git: %v", err)
	}
}

package cli

// applyhostpacknames_test.go drives `yolo host apply`'s pack-name refusal (docs/design/pack-naming.md
// §3.5) through the real apply, so it fails if the call to packload.NameCollisions is deleted:
// `local/acme` is its own namespace, acme, and `local/other` chooses the same word.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyHostRefusesTwoEntriesInOneNamespace(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	for _, name := range []string{"acme", "other"} {
		writeFile(t, filepath.Join(cfgDir, "packs", name, "pack.json"), `{"contributes":[]}`)
	}
	writeFile(t, filepath.Join(cfgDir, "config.toml"),
		"packs = [\"local/acme\", { pack = \"local/other\", namespace = \"acme\" }]\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, nil)
	if rc == 0 {
		t.Fatalf("two entries in the namespace acme must FAIL the apply:\n%s", report)
	}
	for _, want := range []string{"local/acme", "local/other", `namespace "acme"`, "Nothing was written"} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, report)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		t.Errorf("a refused apply wrote into the home:\n%s", report)
	}
}

package check

// packnames_test.go pins `yolo check`'s two pack-naming failures (docs/reference/pack-system.md
// #pack-name-clashes): two entries in one namespace, and two packs delivering one skill name. The launch and
// `yolo host apply` refuse both, so a check that passed them would pass a config that cannot start.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func writeNamingFixture(t *testing.T, packs string, skill bool) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	src := t.TempDir()
	for _, dir := range []string{filepath.Join(src, "x"), filepath.Join(cfgDir, "packs", "acme")} {
		manifest := `{"contributes":[]}`
		if skill {
			manifest = `{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`
			if err := os.MkdirAll(filepath.Join(dir, "skills", "review"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "skills", "review", "SKILL.md"), []byte("# review\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := "packs = " + packs + "\n\n[pack_sources]\nacme = { path = '" + filepath.ToSlash(src) + "', bare = true }\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runSectionPacks() (*reporter, string) {
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())
	return r, buf.String()
}

func TestSectionPacksFailsOnTwoBareEntriesShippingOneSkill(t *testing.T) {
	writeNamingFixture(t, `["acme/x", { pack = "local/acme", bare = true }]`, true)
	r, out := runSectionPacks()
	if r.failed == 0 || !strings.Contains(out, "pack skill name collision") ||
		!strings.Contains(out, `"review"`) || !strings.Contains(out, "bare = false") {
		t.Errorf("check passed two bare packs delivering review:\n%s", out)
	}
}

func TestSectionPacksFailsOnTwoEntriesInOneNamespace(t *testing.T) {
	// `local/acme` is its own namespace, acme, and `local/other` chooses the same word.
	writeNamingFixture(t, `["acme/x", { pack = "local/other", namespace = "acme" }, "local/acme"]`, false)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "packs", "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "packs", "other", "pack.json"),
		[]byte(`{"contributes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, out := runSectionPacks()
	if r.failed == 0 || !strings.Contains(out, "pack name collision") || !strings.Contains(out, "local/other") ||
		!strings.Contains(out, "local/acme") {
		t.Errorf("check passed two entries in the namespace acme:\n%s", out)
	}
}

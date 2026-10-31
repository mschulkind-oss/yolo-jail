package check

// packnamesconverted_test.go pins what `yolo check` says about a config main's migrator converted
// (pack-naming review finding M5): its `[pack_sources]` keys are folder names with no `bare`. Such a
// key loads, and is refused only where a namespace would be composed from it, naming the exact
// `yolo config set` line; and a converted source that now gets a default namespace is warned of,
// naming each renamed skill and the one command that keeps the old names.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConvertedFixture is a user config of one source, keyed key and written sourceValue (with
// %s for its path), selecting key/x, whose pack ships skill review; converted adds the migrator's
// backup beside it.
func writeConvertedFixture(t *testing.T, key, sourceValue string, converted bool) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	src := t.TempDir()
	dir := filepath.Join(src, "x")
	if err := os.MkdirAll(filepath.Join(dir, "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "review", "SKILL.md"), []byte("# review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"),
		[]byte(`{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	value := strings.ReplaceAll(sourceValue, "%s", filepath.ToSlash(src))
	body := "packs = [\"" + key + "/x\"]\n\n[pack_sources]\n" + key + " = " + value + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if converted {
		if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc.pre-migrate"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSectionPacksLoadsAFolderNameKeyAndRefusesOnlyAComposedNamespace(t *testing.T) {
	writeConvertedFixture(t, "Matt_Tools", "{ path = '%s', bare = true }", true)
	if r, out := runSectionPacks(); r.failed != 0 {
		t.Errorf("a bare source keyed by a folder name must load:\n%s", out)
	}

	writeConvertedFixture(t, "Matt_Tools", "'%s'", true)
	_, out := runSectionPacks()
	if !strings.Contains(out, "entry skipped") || !strings.Contains(out, "yolo config set pack_sources.Matt_Tools '{ path = ") ||
		!strings.Contains(out, "bare = true }' --user") || !strings.Contains(out, `"matt-tools"`) {
		t.Errorf("a folder-name key composing a namespace was not refused with the exact line:\n%s", out)
	}
}

func TestSectionPacksWarnsWhenAConvertedSourceRenamesSkills(t *testing.T) {
	writeConvertedFixture(t, "work", "'%s'", true)
	r, out := runSectionPacks()
	if r.failed != 0 {
		t.Fatalf("a converted source with a word key must load:\n%s", out)
	}
	for _, want := range []string{"review → work-review", "yolo config set pack_sources.work '{ path = ",
		"bare = true }' --user", "bare = false"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not say %q:\n%s", want, out)
		}
	}

	for _, tc := range []struct {
		name, value string
		converted   bool
	}{
		{"not converted", "'%s'", false},
		{"marked bare", "{ path = '%s', bare = true }", true},
		{"marked namespaced", "{ path = '%s', bare = false }", true},
	} {
		writeConvertedFixture(t, "work", tc.value, tc.converted)
		if _, out := runSectionPacks(); strings.Contains(out, "work-review") && strings.Contains(out, "→") {
			t.Errorf("%s: warned of a rename the user chose or never had:\n%s", tc.name, out)
		}
	}
}

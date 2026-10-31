package packload

// defaultnames_test.go pins the converted-config warning's contents (pack-naming second review,
// minor 1): beside each renamed skill it names the moved `files` landing and /ctx directory, and
// both commands, the one that keeps the old names and the one that keeps the new.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheConvertedConfigWarningNamesEveryMovedName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "x")
	for rel, body := range map[string]string{
		"pack.json": `{"contributes":[{"kind":"skills","from":"skills","into":".claude/skills"},` +
			`{"kind":"files","from":"files","into":".tool/x"},` +
			`{"kind":"reads-host","host":".acme/key","into":"key"}]}`,
		"skills/review/SKILL.md": "---\nname: review\n---\n",
		"files/a.txt":            "a\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, problems := LoadDirForUse(root, "work/x")
	if p == nil {
		t.Fatal(problems)
	}
	p.Namespace, p.Landing = "work", "work-x"
	p.KeepNamesStep, p.AcceptNamesStep = "`keep`", "`accept`"

	if got := DefaultNamespaceRenames([]*Pack{p}, false); got != nil {
		t.Fatalf("a config never converted is not warned of: %v", got)
	}
	got := DefaultNamespaceRenames([]*Pack{p}, true)
	if len(got) != 1 {
		t.Fatalf("want one warning: %v", got)
	}
	for _, want := range []string{"skill review → work-review", "files landing work/x → work-x",
		"/ctx/host-x/ → /ctx/host-work-x/", "Keep the old names with `keep`", "silence this with `accept`"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the warning does not say %q:\n%s", want, got[0])
		}
	}
}

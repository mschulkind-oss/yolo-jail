package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackLintSourcedSkillFields(t *testing.T) {
	source := "git+https://example.invalid/repo//nested/chosen?ref=main"
	for _, tc := range []struct {
		name, row, want string
		valid           bool
	}{
		{"toml", fmt.Sprintf("source = %q\nas = \"alias\"\nlicense = \"legal/LICENSE\"\n", source), "contents", true},
		{"missing-ref", "source = \"git+https://example.invalid/repo//skills/chosen\"", "ref", false},
		{"root", "source = \"git+https://example.invalid/repo?ref=main\"", "subdirectory", false},
		{"from", fmt.Sprintf("source = %q\nfrom = \"skills\"", source), "from", false},
		{"password", "source = \"git+https://user:secret@example.invalid/repo//skills/chosen?ref=main\"", "password", false},
		{"directory", "source = \"file:///some/directory\"", "from", false},
		{"bad-name", fmt.Sprintf("source = %q\nas = \"Bad_Name\"", source), "as", false},
		{"bad-base", "source = \"git+https://example.invalid/repo//skills/Bad_Name?ref=main\"", "as", false},
		{"alias-without-source", "as = \"alias\"", "source", false},
		{"license-without-source", "license = \"LICENSE\"", "source", false},
		{"license-escape", fmt.Sprintf("source = %q\nlicense = \"../LICENSE\"", source), "license", false},
		{"path-escape", "source = \"git+https://example.invalid/repo//skills/../chosen?ref=main\"", "subdirectory", false},
		{"destination", fmt.Sprintf("source = %q\nagent = \"fixture\"\ninto = \".fixture/skills\"", source), "source", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "pack.toml"), "[[skills]]\n"+tc.row+"\n")
			rc, report := lintReport(t, dir)
			if (rc == 0) != tc.valid || !strings.Contains(report, tc.want) {
				t.Fatalf("rc=%d valid=%v want=%q:\n%s", rc, tc.valid, tc.want, report)
			}
			if !tc.valid && !strings.Contains(report, "skills[0]") {
				t.Fatalf("missing row label:\n%s", report)
			}
		})
	}
	t.Run("legacy-json", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "pack.json"), fmt.Sprintf(`{"contributes":[{"kind":"skills","source":%q,"as":"alias"}]}`, source))
		if rc, report := lintReport(t, dir); rc != 0 || !strings.Contains(report, "not verified") {
			t.Fatalf("rc=%d:\n%s", rc, report)
		}
	})
	t.Run("legacy-toml-refused", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "pack.toml"), fmt.Sprintf("[[contributes]]\nkind = \"skills\"\nsource = %q\n", source))
		if rc, _ := lintReport(t, dir); rc == 0 {
			t.Fatal("legacy TOML unexpectedly accepted")
		}
	})
}

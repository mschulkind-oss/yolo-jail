package basehome

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestCleanupLinesAreOptionalAndNameOnlyDeclaredRootsWithBytes pins the reworded report
// (the maintainer's OQ-BH13 ruling, docs/design/base-home-legacy-state.md#10-decision-ledger):
// the bytes are unmounted and unread, so the `mv` is OPTIONAL — and it is offered only for a
// root that carries bytes AND that a shipped pack declares, the rule the deleted launch
// refusal used. An undeclared root may hold a local pack's own content, so it is reported by
// Summary and never offered.
func TestCleanupLinesAreOptionalAndNameOnlyDeclaredRootsWithBytes(t *testing.T) {
	home := legacyFixture(t)
	writeFile(t, home, ".not-a-shipped-pack/data.bin", strings.Repeat("u", 4096))
	decls := fixtureDeclsWithSweep()
	rep := Detect(home, decls)
	if rep.Summary() == "" {
		t.Fatal("the fixture produced no Summary, so the lines below prove nothing")
	}
	if !strings.Contains(strings.Join(rep.UnknownRoots, " "), ".not-a-shipped-pack") {
		t.Fatalf("the undeclared root is not an UnknownRoot (%v), so its negative below proves nothing",
			rep.UnknownRoots)
	}

	archive := filepath.Join(home, "archive")
	lines := rep.CleanupLines(home, archive)
	body := strings.Join(lines, "\n")
	for _, want := range []string{
		"Nothing mounts or reads these bytes",
		"optional cleanup",
		"  mkdir -p " + shquote.Quote(archive),
		"  mv " + shquote.Quote(filepath.Join(home, ".claude")) + " " + shquote.Quote(archive) + "/",
		"Do NOT move .claude-shared-credentials",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("CleanupLines is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, filepath.Join(home, ".not-a-shipped-pack")) {
		t.Errorf("an undeclared root was offered an mv; its bytes may be a local pack's own content:\n%s", body)
	}
	for _, shared := range []string{".claude-shared-credentials", ".gemini-shared-credentials"} {
		if strings.Contains(body, "mv "+filepath.Join(home, shared)) {
			t.Errorf("the machine-scope shared dir %s was offered an mv:\n%s", shared, body)
		}
	}
	for _, e := range lines {
		if strings.Contains(e, "-home-someone-code-secret-thing") {
			t.Errorf("a cleanup line names a workspace: %q", e)
		}
	}
}

// The cleanup lines are for pasting, so each path in them has to come back out of a shell as
// ONE word. The state dir is under the user's home, and a macOS home is often under a path with
// a space in it (/Users/Jane Doe): printed bare, `mv` moved the path's first word.
func TestCleanupLinesNameEachPathAsOneShellWord(t *testing.T) {
	home := filepath.Join(baseHomeFixture(t), "Jane Doe", "state", "home")
	mkdirAll(t, filepath.Dir(home), ".")
	if err := os.Rename(legacyFixture(t), home); err != nil {
		t.Fatal(err)
	}
	rep := Detect(home, fixtureDeclsWithSweep())
	if rep.Summary() == "" {
		t.Fatal("the fixture produced no Summary, so the lines below prove nothing")
	}
	archive := filepath.Join(filepath.Dir(home), "home archive")
	var offered [][]string
	for _, line := range rep.CleanupLines(home, archive) {
		if strings.HasPrefix(line, "  mkdir -p ") || strings.HasPrefix(line, "  mv ") {
			offered = append(offered, testsupport.ShellWords(t, strings.TrimSpace(line)))
		}
	}
	for _, want := range [][]string{
		{"mkdir", "-p", archive},
		{"mv", filepath.Join(home, ".claude"), archive + "/"},
	} {
		if !slices.ContainsFunc(offered, func(got []string) bool { return slices.Equal(got, want) }) {
			t.Errorf("the cleanup lines do not offer %q as a shell reads them; they offer %q", want, offered)
		}
	}
}

// TestCleanupLinesAreSilentWhenSummaryIs: nothing to report, nothing to say.
func TestCleanupLinesAreSilentWhenSummaryIs(t *testing.T) {
	home := baseHomeFixture(t)
	mkdirAll(t, home, ".copilot")
	rep := Detect(home, Decls{StateDirs: []string{".copilot"}})
	if lines := rep.CleanupLines(home, filepath.Join(home, "archive")); lines != nil {
		t.Errorf("CleanupLines = %q, want nil for a clean home", lines)
	}
}

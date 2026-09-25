package basehome

import (
	"path/filepath"
	"strings"
	"testing"
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
		"  mkdir -p " + archive,
		"  mv " + filepath.Join(home, ".claude") + " " + archive + "/",
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

// TestCleanupLinesAreSilentWhenSummaryIs: nothing to report, nothing to say.
func TestCleanupLinesAreSilentWhenSummaryIs(t *testing.T) {
	home := baseHomeFixture(t)
	mkdirAll(t, home, ".copilot")
	rep := Detect(home, Decls{StateDirs: []string{".copilot"}})
	if lines := rep.CleanupLines(home, filepath.Join(home, "archive")); lines != nil {
		t.Errorf("CleanupLines = %q, want nil for a clean home", lines)
	}
}

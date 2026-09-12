package cli

// hostapplydetail_test.go pins docs/design/report-tiers.md §4.5's split: what a `yolo host
// apply` prints by default, and what `--verbose` adds.
//
// TWO PROPERTIES, and the second is the one with a trap in it:
//
//   - THE DEFAULT VIEW DROPS NO LOSS AND NO BLOCKER (§4.4). Compression is allowed to take the
//     per-destination LINES and never the SET, so the assertions below are about names —
//     every dropped entry, every adopted skill, every missing binary — rather than about line
//     counts.
//   - THE GATE HONORS AN INHERITED YOLO_VERBOSE, not only a typed --verbose (OQ-RO2). The
//     accessor next door, explicitVerbose(), answers the opposite question by design
//     (perf-logging D12: an inherited setting RECORDS timings without PRINTING a table), and
//     copying that half here would leave `YOLO_VERBOSE=1 yolo host apply` silently compressed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// verboseReport points a test at the `--verbose` view.
//
// Many tests in this package pin a per-destination line — a dep probe, a composed-from line, a
// per-entry skills action, an inferred destination — that §4.5 moved behind the flag. The FACT
// each of them asserts is unchanged; only the view carrying it moved, so those tests ask for
// that view here rather than being rewritten to assert something weaker.
//
// The inherited spelling, deliberately: it is the one OQ-RO2 rules must work, and using it
// everywhere means the whole suite would notice a gate that only honored the typed flag.
func verboseReport(t *testing.T) {
	t.Helper()
	t.Setenv(paths.VerboseEnv, "1")
}

// detailFixture is one pack whose contributions produce a line in each class the split sorts:
// a settled config surface, a composed skill, and a briefing.
func detailFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"detailpack","description":"d","contributes":[`+
			`{"kind":"skills","from":"skills","into":".detail/skills"},`+
			`{"kind":"briefing","from":"AGENTS.md","into":".detail/AGENTS.md"}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Detail prose.\n")
	writeFile(t, filepath.Join(packDir, "skills", "demo", "SKILL.md"), "---\nname: demo\n---\n")
	selectPacks(t, home, `{"source":"file://`+packDir+`","name":"detailpack"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// THE DEFAULT VIEW IS THE COMPRESSED ONE, AND --verbose ADDS THE ITEMIZATION (OQ-RO1). Both
// halves in one test, over one fixture, because "compressed" is a claim about a difference:
// asserting the absence of a line proves nothing unless the same run with the flag has it.
func TestHostApplyDefaultViewCompressesAndVerboseItemizes(t *testing.T) {
	detailFixture(t)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("dry run rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	plain := out.String() + errw.String()

	// The verdict is the default view's whole job (P7), so it is there at both verbosities.
	if !strings.Contains(plain, "An --assert would complete") {
		t.Errorf("the default view must still state the result:\n%s", plain)
	}
	if strings.Contains(plain, "composed from") {
		t.Errorf("the composed-from line is tier-2 detail and must not print by default:\n%s", plain)
	}
	if strings.Contains(plain, "in sync, ") {
		t.Errorf("the destination roll-up counts what a loop visited (§3.4) and is detail:\n%s",
			plain)
	}

	// THE SAME RUN, ASKING FOR MORE. Nothing about the home changed, so every added line is
	// the flag's doing.
	verboseReport(t)
	out.Reset()
	errw.Reset()
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("verbose dry run rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	verbose := out.String() + errw.String()
	for _, want := range []string{"composed from", "in sync, ", "demo"} {
		if !strings.Contains(verbose, want) {
			t.Errorf("--verbose must add %q:\n%s", want, verbose)
		}
	}
	if len(verbose) <= len(plain) {
		t.Errorf("the verbose view (%d bytes) is not longer than the default (%d)",
			len(verbose), len(plain))
	}
}

// THE GATE READS THE ENVIRONMENT, WHICH IS BOTH SPELLINGS (OQ-RO2). A typed --verbose publishes
// YOLO_VERBOSE (applyVerboseFlag), and a user who exported it in their shell profile asked for
// the same thing — the distinction perf-logging D12 draws is about a timing TABLE at every
// quit, not about a report the user typed a command to get.
//
// Asserted through applyVerboseFlag rather than by setting the variable twice, so the typed
// half is pinned to the code that publishes it: if the flag ever stopped publishing, the
// inherited-only assertion above would still pass and this one would not.
func TestReportVerboseHonorsTypedAndInheritedSpellings(t *testing.T) {
	t.Setenv(paths.VerboseEnv, "")
	if reportVerbose() {
		t.Fatal("no flag and no variable, yet the report claims verbose")
	}

	// INHERITED — the spelling explicitVerbose() deliberately does not see.
	t.Setenv(paths.VerboseEnv, "1")
	if !reportVerbose() {
		t.Error("an inherited YOLO_VERBOSE must reach the report (OQ-RO2)")
	}
	if explicitVerbose() {
		t.Error("fixture bug: the typed accessor should still be false here — if it is not, " +
			"this test is no longer telling the two spellings apart")
	}

	// TYPED — through the real stripper, which is what publishes the variable.
	t.Setenv(paths.VerboseEnv, "")
	if rest := applyVerboseFlag([]string{"apply", "--verbose", "--at", "host"}); len(rest) != 3 {
		t.Fatalf("the flag was not stripped: %v", rest)
	}
	if os.Getenv(paths.VerboseEnv) == "" {
		t.Fatal("applyVerboseFlag stopped publishing the variable the report reads")
	}
	if !reportVerbose() {
		t.Error("a typed --verbose must reach the report")
	}
}

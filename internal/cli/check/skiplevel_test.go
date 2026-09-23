package check

// skiplevel_test.go pins the [SKIP] level: what it counts as, what it must never count as,
// and — the one that matters in a year — that no new site can quietly go back to reporting
// a skipped area as a pass.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// A skip is its own bucket. The whole of OQ-3 is that it must not land in the pass tally,
// so that is asserted directly rather than inferred from a rendered line.
func TestSkipIsNotAPass(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	r.skip("something host-side", "run `yolo check` on the host")

	if r.passed != 0 {
		t.Errorf("a skip must not be counted as a pass: passed=%d", r.passed)
	}
	if r.warned != 0 || r.failed != 0 {
		t.Errorf("a skip is neither a warning nor a failure: warned=%d failed=%d", r.warned, r.failed)
	}
	if r.skipped != 1 {
		t.Errorf("a skip must land in its own counter: skipped=%d", r.skipped)
	}
	if !strings.Contains(buf.String(), "[SKIP]") {
		t.Errorf("a skip must be PRINTED — silence is worse than the pass it replaces:\n%s", buf.String())
	}
}

// The summary shows the skip count only when something skipped, so a host run reads
// exactly as it did before this level existed.
func TestSummaryOmitsAZeroSkipCount(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	r.ok("fine")
	r.summaryFinal()
	if strings.Contains(buf.String(), "skipped") {
		t.Errorf("a run that skipped nothing must not mention skips:\n%s", buf.String())
	}

	buf.Reset()
	r = newReporter(&buf, false)
	r.ok("fine")
	r.skip("host-side thing", "check it there")
	r.summaryFinal()
	if !strings.Contains(buf.String(), "1 skipped") {
		t.Errorf("a run that skipped something must say so:\n%s", buf.String())
	}
}

// The JSON field is ALWAYS emitted, even as 0 — unlike the human summary's conditional
// line. A consumer distinguishing "nothing skipped" from "this yolo predates the field"
// cannot do it against an omitted key.
func TestJSONAlwaysCarriesTheSkipCount(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	r.ok("fine")

	raw, err := json.Marshal(r.report())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"skipped":0`) {
		t.Errorf("skipped must be present at zero, not omitted:\n%s", raw)
	}

	r.skip("host-side thing", "check it there")
	raw, err = json.Marshal(r.report())
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", got.Skipped)
	}
	var found bool
	for _, f := range got.Findings {
		if f.Status == "skip" {
			found = true
		}
	}
	if !found {
		t.Errorf(`no finding recorded with status "skip": %+v`, got.Findings)
	}
}

// hostFact is the OTHER direction OQ-3 ruled on: a fact that is TRUE but not about this
// reader. It must produce a skip rather than a failure, and it must say where to look
// instead — a bare "[SKIP] GPU" tells a reader less than the [FAIL] it replaced.
func TestHostFactIsASkipWithSomewhereToLook(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	r.hostFact("GPU (NVIDIA) checks", "Run `yolo check` on the host.")

	if r.failed != 0 {
		t.Errorf("a host fact must not fail an in-jail run: failed=%d", r.failed)
	}
	if r.skipped != 1 {
		t.Errorf("a host fact must count as a skip: skipped=%d", r.skipped)
	}
	out := buf.String()
	if !strings.Contains(out, "host fact") || !strings.Contains(out, "->") {
		t.Errorf("a host fact must say what it is and where to check it:\n%s", out)
	}
}

// THE DRIFT GUARD, and the reason this file exists a year from now.
//
// Nine sites across five section files called r.ok on an area they had declined to
// examine. Converting them is a one-time fix; keeping them converted is not, because the
// next in-jail guard someone writes will be copied from whatever sits next to it. So the
// SOURCE is the assertion: an r.ok whose message says it skipped something is the defect,
// spelled the only way that catches a site nobody thought to add a behavioural test for.
func TestNoSectionReportsASkippedAreaAsAPass(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	// The words that mean "I did not look". Deliberately narrow: this must catch the
	// copied-guard case, not every message with the word "host" in it.
	tells := []string{"skipped", "inside jail", "not reachable here", "managed by host"}

	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ok" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "r" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			msg, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			checked++
			lower := strings.ToLower(msg)
			for _, tell := range tells {
				if strings.Contains(lower, tell) {
					t.Errorf("%s:%d reports a skipped area as a [PASS]: %q\n"+
						"    Use r.skip (or r.hostFact for a fact about the host) instead. "+
						"A check that did not look must not be counted as a pass — "+
						"docs/reference/claude-oauth-interposition.md#oq-3. Nine sites did "+
						"this, and an all-green in-jail run included areas nobody checked.",
						name, fset.Position(lit.Pos()).Line, msg)
					return true
				}
			}
			return true
		})
	}
	// A census that walked nothing passes vacuously, which is the failure this repo
	// finds more often than wrong logic.
	if checked < 20 {
		t.Fatalf("only %d r.ok literals inspected — the walk is not reaching the sections", checked)
	}
}

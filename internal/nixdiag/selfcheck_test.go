package nixdiag

import (
	"reflect"
	"testing"
)

func TestSplitSelfCheckProblems(t *testing.T) {
	out := "some preamble\n" +
		"FAIL: broker socket missing\n" +
		"  run yolo broker restart\n" +
		"\n" +
		"  check the log\n" +
		"FAIL: relay dead\n" +
		"OK: everything else\n"
	got := SplitSelfCheckProblems(out)
	want := []Problem{
		{Title: "broker socket missing", Detail: "  run yolo broker restart\n  check the log"},
		{Title: "relay dead", Detail: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems =\n%#v\nwant\n%#v", got, want)
	}
	// No FAIL lines -> no problems.
	if got := SplitSelfCheckProblems("all good\nnothing wrong\n"); len(got) != 0 {
		t.Errorf("no-FAIL => %v, want empty", got)
	}
}

// TestSplitSelfCheckLinesGradesAllThree: the whole protocol, not just FAIL.
//
// The "OK: everything else" line above is the load-bearing case: it used to be
// swallowed into the PRECEDING failure's remediation detail, so a self-check's
// passing observations were both invisible on the rc=0 path and misattributed on
// the rc=1 one. Grading it separately is what lets a loophole report a live
// number (the broker's shared-creds remaining lifetime) through its own
// doctor_cmd instead of core re-implementing the grading.
func TestSplitSelfCheckLinesGradesAllThree(t *testing.T) {
	out := "some preamble\n" +
		"FAIL: broker socket missing\n" +
		"  run yolo broker restart\n" +
		"NOTE: ca.crt not yet generated\n" +
		"  run --init-ca\n" +
		"OK: shared creds valid for 5h0m, last write 10m ago\n" +
		"OK (broker present; state not yet primed)\n"
	got := SplitSelfCheckLines(out)
	want := []GradedLine{
		{Grade: GradeFail, Title: "broker socket missing", Detail: "  run yolo broker restart"},
		{Grade: GradeNote, Title: "ca.crt not yet generated", Detail: "  run --init-ca"},
		{Grade: GradeOK, Title: "shared creds valid for 5h0m, last write 10m ago",
			// The bare "OK (…)" summary carries no colon, so it is NOT a graded
			// line of its own — but it is also not this entry's detail, or a
			// clean broker would print its summary as remediation text.
			Detail: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines =\n%#v\nwant\n%#v", got, want)
	}
}

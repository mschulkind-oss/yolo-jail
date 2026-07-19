package checkdiag

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
		{Title: "relay dead", Detail: "OK: everything else"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems =\n%#v\nwant\n%#v", got, want)
	}
	// No FAIL lines -> no problems.
	if got := SplitSelfCheckProblems("all good\nnothing wrong\n"); len(got) != 0 {
		t.Errorf("no-FAIL => %v, want empty", got)
	}
}

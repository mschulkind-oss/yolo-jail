package checkdiag

import (
	"encoding/json"
	"reflect"
	"strings"
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

// TestSplitSelfCheckParity cross-checks against live _split_self_check_problems.
func TestSplitSelfCheckParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	input := "preamble\nFAIL: alpha\n  detail a1\n\n  detail a2\nFAIL: beta\nignored OK line\n"
	script := `
import sys, json; sys.path.insert(0, 'src')
from cli.check_cmd import _split_self_check_problems
print(json.dumps(_split_self_check_problems(sys.stdin.read())))
`
	cmd := py("-c", script)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python check_cmd import failed: %v", err)
	}
	var want [][]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := SplitSelfCheckProblems(input)
	if len(got) != len(want) {
		t.Fatalf("count go=%d py=%d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Title != w[0] || got[i].Detail != w[1] {
			t.Errorf("problem %d go=%q/%q py=%q/%q", i, got[i].Title, got[i].Detail, w[0], w[1])
		}
	}
}

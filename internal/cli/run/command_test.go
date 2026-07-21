package run

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildFinalInternalCmdBashGolden pins the non-profile final_internal_cmd
// bytes for target_cmd="bash" against a golden (testdata/final_cmd_bash.txt).
// This is a frozen host-state contract — the bash -c payload the container runs.
func TestBuildFinalInternalCmdBashGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "final_cmd_bash.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := buildFinalInternalCmd("bash", false)
	if got != string(want) {
		t.Errorf("final_internal_cmd mismatch\n got: %q\nwant: %q", got, string(want))
	}
}

// TestBuildFinalInternalCmdQuotingEscapesDisplay checks the display_cmd single-
// quote escaping (target_cmd's quotes → '\” in the "Executing:" printf).
func TestBuildFinalInternalCmdQuotingEscapesDisplay(t *testing.T) {
	got := buildFinalInternalCmd("echo 'hi'", false)
	if !contains(got, `Executing: echo '\''hi'\''`) {
		t.Errorf("display_cmd not escaped: %q", got)
	}
	// The raw target_cmd (unescaped) is what actually runs at the tail.
	if !hasSuffixStr(got, "echo 'hi'") {
		t.Errorf("target_cmd tail not raw: %q", got)
	}
}

func contains(s, sub string) bool { return indexOfStr(s, sub) >= 0 }
func hasSuffixStr(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}
func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

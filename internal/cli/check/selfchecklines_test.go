package check

import (
	"bytes"
	"strings"
	"testing"
)

// TestReportSelfCheckLinesGradesTheWholeProtocol: a loophole's self-check speaks
// three levels, and `yolo check` must render all three.
//
// This is the seam that let the broker's shared-creds freshness grading move out
// of core. Before it, only FAIL lines were rendered and only on a non-zero rc, so
// a loophole had no way to report a healthy-but-informative MEASUREMENT — which
// is why `check` grew its own copy of the grading, reaching into
// `claudeAiOauth.expiresAt` directly. The load-bearing assertion is the OK line:
// the remaining-lifetime number reaches the user through the doctor_cmd seam, as
// text, because DoctorResult itself carries only a return code.
func TestReportSelfCheckLinesGradesTheWholeProtocol(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	out := "FAIL: shared creds expired 2h0m ago (last write 3h0m ago)\n" +
		"  Refreshes are not landing.\n" +
		"NOTE: ca.crt not yet generated\n" +
		"OK: shared creds valid for 5h0m, last write 10m ago\n" +
		"OK (broker present; state not yet primed)\n"
	if n := reportSelfCheckLines(r, "claude-oauth-broker", out); n != 3 {
		t.Errorf("rendered %d lines, want 3", n)
	}
	got := buf.String()
	if r.failed != 1 || r.warned != 1 || r.passed != 1 {
		t.Errorf("failed=%d warned=%d passed=%d, want 1/1/1\n%s", r.failed, r.warned, r.passed, got)
	}
	for _, want := range []string{
		"[FAIL] loophole claude-oauth-broker: shared creds expired 2h0m ago (last write 3h0m ago)",
		"Refreshes are not landing.",
		"[WARN] loophole claude-oauth-broker: ca.crt not yet generated",
		"[PASS] loophole claude-oauth-broker: shared creds valid for 5h0m, last write 10m ago",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
	// The colon-less trailing summary is the caller's "self-check ok" header, not
	// a graded line — it must not be echoed as a fourth finding.
	if strings.Contains(got, "broker present") {
		t.Errorf("trailing OK summary leaked into the graded lines:\n%s", got)
	}
}

// TestReportSelfCheckLinesEmpty: a self-check that graded nothing renders
// nothing, so the rc!=0 branch can still fall back to its "no output" failure.
func TestReportSelfCheckLinesEmpty(t *testing.T) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	if n := reportSelfCheckLines(r, "x", "OK\n"); n != 0 {
		t.Errorf("rendered %d lines, want 0", n)
	}
	if buf.Len() != 0 {
		t.Errorf("want no output, got %q", buf.String())
	}
}

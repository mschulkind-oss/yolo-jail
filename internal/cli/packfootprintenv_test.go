package cli

// packfootprintenv_test.go pins `yolo pack footprint aws-auth`, whole, as printed.
//
// It printed AWS_CONTAINER_CREDENTIALS_FULL_URI twice — once bare, once with its `bedrock`
// gate — because the footprint's contribution loop claimed every env variable and the gated
// loop claimed the gated ones again (internal/packload/footprint.go). The unit test there
// pins the claims; this pins the command a reader actually runs, so a renderer change that
// reintroduced a second line would be caught where it would be seen.

import (
	"bytes"
	"testing"
)

// TestPackFootprintAWSAuthGolden is the golden for the shipped aws-auth pack's footprint:
// ONE env line carrying its gate, the loophole with its host argv, and the three
// `overridden_by` lines, the ~/.aws one worded as a warning (its entry is `certain: false`).
func TestPackFootprintAWSAuthGolden(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", "aws-auth"}, &out, &errw, false); rc != 0 {
		t.Fatalf("yolo pack footprint aws-auth: rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	const uri = "AWS_CONTAINER_CREDENTIALS_FULL_URI"
	const want = "aws-auth\n" +
		"  env            " + uri + "  =http://127.0.0.1:1461/credentials when profile \"bedrock\" is active\n" +
		"  loophole       aws-auth  RUNS yolo internal daemon aws-auth --socket '{socket}' " +
		"--state-file '{state}/credentials.json' --settings '{settings}' and yolo internal daemon " +
		"aws-auth --self-check --state-file '{state}/credentials.json' --settings '{settings}' " +
		"on your machine ⚠ RUNS CODE ON YOUR MACHINE\n" +
		"  overridden-by  " + uri + "  launch refused beside AWS_BEARER_TOKEN_BEDROCK " +
		"(when profile \"bedrock\" is active)\n" +
		"  overridden-by  " + uri + "  launch refused beside AWS_ACCESS_KEY_ID + " +
		"AWS_SECRET_ACCESS_KEY unless AWS_PROFILE is also delivered (when profile \"bedrock\" is active)\n" +
		"  overridden-by  " + uri + "  launch warned (may override) beside a host_files grant " +
		"at ~/.aws (when profile \"bedrock\" is active)\n" +
		"\n" +
		"1 claim(s) worth review: 1 RUNNING CODE ON YOUR MACHINE\n"
	if got := out.String(); got != want {
		t.Errorf("yolo pack footprint aws-auth:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// TestRunReportsTheFlakeSource pins the report AT ITS CALL SITE: delete the
// o.reportFlakeSource(repoRes) line in Run and this fails, which a test that
// called reportFlakeSource directly would not.
//
// It rides the skew fixture because the skew refusal is the first deterministic
// stop AFTER the report — Run prints the source line, then refuses, then returns,
// with no pack staging and no nix build in between.
//
// The line's whole job is to name what the cwd used to decide silently, so the
// assertion is on both halves: the resolved path AND the source that produced it.
func TestRunReportsTheFlakeSource(t *testing.T) {
	repoRoot := skewRepo(t)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := skewOptions(t, repoRoot, nil, &stdout, &stderr)

	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1 (the skew fixture refuses)\nstderr:\n%s", rc, stderr.String())
	}
	want := "Flake source: " + repoRoot + " (" + reporoot.FromEnv.Describe() + ")"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("launch did not report its flake source.\nwant substring: %s\nstderr:\n%s",
			want, stderr.String())
	}
}

// The report must precede the refusal, not trail it: read in the other order the
// refusal names a path the reader has no context for yet.
func TestFlakeSourceReportPrecedesTheSkewRefusal(t *testing.T) {
	repoRoot := skewRepo(t)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := skewOptions(t, repoRoot, nil, &stdout, &stderr)
	Run(*o)

	out := stderr.String()
	report := strings.Index(out, "Flake source:")
	refusal := strings.Index(out, "older than the source tree")
	if report < 0 || refusal < 0 {
		t.Fatalf("expected both the report and the refusal:\n%s", out)
	}
	if report > refusal {
		t.Errorf("the flake-source report printed AFTER the refusal:\n%s", out)
	}
}

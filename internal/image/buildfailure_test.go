package image

import (
	"strings"
	"testing"
)

// The report is the deliverable: a failed image build has to arrive as ITSELF,
// carrying nix's own words, rather than as a downstream symptom hours later.
// These tests pin the parts a reader depends on.

// TestBuildFailureReportCarriesNixOutput is the core of the fix. The old code
// captured buildTail and then threw it away, so the one thing that could have
// explained the failure — what nix actually said — never reached anybody.
func TestBuildFailureReportCarriesNixOutput(t *testing.T) {
	tail := []string{
		"error: builder for '/nix/store/aaa-libzbar.drv' failed with exit code 1",
		"       last 2 log lines:",
		"       > configure: error: no acceptable C compiler",
	}
	msg := buildFailureReport("nix build failed", "", tail, false)
	for _, want := range append([]string{BuildFailedMarker}, tail...) {
		if !strings.Contains(msg, want) {
			t.Errorf("report is missing %q:\n%s", want, msg)
		}
	}
}

// TestBuildFailureReportFatalIsActionable: the refusing variant must name the
// diagnosis, say WHY it refuses rather than falling back, and hand over the
// escape hatch — a refusal with no way past it is a worse tool than the silent
// fallback it replaced.
func TestBuildFailureReportFatalIsActionable(t *testing.T) {
	msg := buildFailureReport("Image build needs a Linux builder",
		"Part of the image isn't in the binary cache.\nSet up a builder.",
		[]string{"error: a 'x86_64-linux' is required to build"}, false)
	for _, want := range []string{
		BuildFailedMarker,
		"Cannot start jail: Image build needs a Linux builder.",
		"Set up a builder.",          // the classified remedy survives
		"error: a 'x86_64-linux' is", // ...and so does the raw log
		StaleImageEnv + "=1",         // the escape hatch is stated
		"DIFFERENT source tree",      // why falling back is not neutral
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("fatal report is missing %q:\n%s", want, msg)
		}
	}
}

// TestBuildFailureReportStaleVariantStatesTheStaleness: with the escape hatch
// set the run continues, but the report must say out loud that what follows was
// built from other source. "Loud but continuing" is only acceptable while the
// staleness is stated; the moment it is implied, this is the original bug again.
func TestBuildFailureReportStaleVariantStatesTheStaleness(t *testing.T) {
	msg := buildFailureReport("nix build failed", "", []string{"error: boom"}, true)
	for _, want := range []string{
		BuildFailedMarker,
		StaleImageEnv,
		"STALE",
		"error: boom",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("stale-continue report is missing %q:\n%s", want, msg)
		}
	}
	// It must NOT claim the jail cannot start — it is about to.
	if strings.Contains(msg, "Cannot start jail") {
		t.Errorf("stale-continue report claims the jail cannot start:\n%s", msg)
	}
}

// TestBuildFailureReportDoesNotEchoTheTailTwice: nixdiag's UNCLASSIFIED fallback
// returns the last 10 stderr lines AS the remedy. Printing the tail verbatim and
// then printing it again under "Diagnosis" doubles the noise in exactly the case
// where the reader has the least to go on.
func TestBuildFailureReportDoesNotEchoTheTailTwice(t *testing.T) {
	tail := []string{"error: one", "error: two"}
	remedy := strings.Join(tail, "\n") // what nixdiag's fallback hands back
	msg := buildFailureReport("nix build failed", remedy, tail, false)
	if n := strings.Count(msg, "error: two"); n != 1 {
		t.Errorf("tail line printed %d times, want 1:\n%s", n, msg)
	}
}

// TestBuildFailureReportWithNoOutputSaysSo: `nix` missing from PATH produces a
// one-line tail or none at all. An empty "what the build said" block would read
// as a rendering bug; name the possibility instead.
func TestBuildFailureReportWithNoOutputSaysSo(t *testing.T) {
	msg := buildFailureReport("nix build failed", "", nil, false)
	if !strings.Contains(msg, BuildFailedMarker) {
		t.Errorf("missing marker:\n%s", msg)
	}
	if !strings.Contains(msg, "no output") {
		t.Errorf("empty-output case is not explained:\n%s", msg)
	}
}

// TestRemedyEchoesTail pins the de-duplication predicate itself: only the exact
// last-10-lines join counts as an echo. A CLASSIFIED remedy that happens to
// quote a log line must still be printed.
func TestRemedyEchoesTail(t *testing.T) {
	tail := []string{"a", "b", "c"}
	if !remedyEchoesTail("a\nb\nc", tail) {
		t.Error("the exact tail join must count as an echo")
	}
	if remedyEchoesTail("Set up a Linux builder: see docs/guides/macos.md", tail) {
		t.Error("a classified remedy must not be swallowed as an echo")
	}
	if remedyEchoesTail("a\nb\nc", nil) {
		t.Error("with no tail there is nothing to echo")
	}
	// >10 lines: only the last 10 are what got printed as the tail.
	long := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}
	if !remedyEchoesTail(strings.Join(long[2:], "\n"), long) {
		t.Error("the last-10 join must count as an echo")
	}
}

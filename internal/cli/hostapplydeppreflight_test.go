package cli

// hostapplydeppreflight_test.go pins docs/design/report-tiers.md §9 step 5: the dependency
// probe is a PRE-FLIGHT over the whole run, not a loop body.
//
// WHY IT NEEDS A TEST AT ALL, given the step changes no output: that is precisely why. A probe
// hoisted out of the loop and a probe left in it produce byte-identical reports (verified by
// diffing a real `yolo apply --at host` across the refactor), so nothing in the report can tell
// them apart — and the next step hangs a fatal abort on the position. An abort from inside the
// loop leaves the packs already visited WRITTEN and the rest not, which is the one thing
// "nothing was written" must never mean.
//
// So the observation is TIMING, taken through the hostDepProbe seam: every probe sees the same
// output, and no line the render loop prints precedes any of them. Put resolveHostDeps back in
// the loop and the second pack's probe sees the first pack's lines, which is exactly what these
// two assertions measure.

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestHostApplyProbesEveryPackBeforeTheFirstReportLine(t *testing.T) {
	shippedPacksFixture(t)
	// THE MEASUREMENT NEEDS THE LINES. The observation here is TIMING — no report line may
	// precede any probe — and §4.5 moved the per-contribution dep lines behind `--verbose`, so
	// the compressed view has nothing for a probe to land between. The property under test is
	// the probe's POSITION, which no verbosity changes.
	verboseReport(t)

	var out, errw bytes.Buffer
	// Each probe records the report SO FAR. A pre-flight takes them all at one point in the
	// stream; a loop body takes each one further down it.
	var snapshots []string
	real := hostDepProbe
	t.Cleanup(func() { hostDepProbe = real })
	hostDepProbe = func(p *packload.Pack) *hostDeps {
		snapshots = append(snapshots, out.String())
		return real(p)
	}

	if rc := applyHostSurveyed(&out, &errw, false, false, nil, &hostApplySurvey{}); rc != 0 {
		t.Fatalf("observe apply rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()

	if len(snapshots) < 2 {
		t.Fatalf("fixture bug: %d pack(s) probed — with fewer than two, \"all at once\" is "+
			"the only thing it could be\n%s", len(snapshots), report)
	}
	// The dep lines are what the loop prints about the probe's answer, and they are the lines
	// a loop-body probe would interleave itself between.
	depLines := countLines(report, "present at") + countLines(report, "MISSING")
	if depLines < 2 {
		t.Fatalf("fixture bug: %d dep line(s) in the report, so there is nothing for a probe "+
			"to land between\n%s", depLines, report)
	}

	for i, snap := range snapshots {
		if snap != snapshots[0] {
			t.Fatalf("probe %d of %d saw %d bytes of report where the first saw %d — the "+
				"probes are not happening together, so an abort at one of them would find "+
				"some packs already written (§4.9 point 1)\n%s",
				i+1, len(snapshots), len(snap), len(snapshots[0]), report)
		}
	}
	// And the pre-flight is before the REPORT, not merely self-consistent: a run that probed
	// every pack at the end of the loop would also pass the check above.
	tail := report[len(snapshots[len(snapshots)-1]):]
	if got := countLines(tail, "present at") + countLines(tail, "MISSING"); got != depLines {
		t.Errorf("%d of %d dep lines were printed before the last probe — the probe is still "+
			"inside the loop, so aborting on one would leave a half-applied home\n%s",
			depLines-got, depLines, report)
	}
}

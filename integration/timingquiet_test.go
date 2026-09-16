package integration

// The quiet-contract ASSERTION, tested against real recorded output — no jail, because
// the subject is what the assertion accepts rather than what a launch does.
//
// It exists because that assertion was wrong for eight days in a way no Linux run could
// show. `assertRecordedQuietly` forbade the literal `shutdown.stop_loopholes` as a proxy
// for "any span row", while the reference rules that a quietly-recording launch KEEPS its
// live slow-span notices — so the test failed on any machine slow enough for one span to
// cross perf.SlowSpanThreshold, and passed everywhere else. Four nightly-macOS runs read
// as a yolo defect; the launch was doing exactly what it was ruled to do.
//
// Fixing a machine-speed-dependent test with a machine-speed-dependent test would be no
// fix, which is why the sample below is the VERBATIM stderr of the failing run
// (2026-09-16 nightly, shard 9, job 104928285262). It is deterministic, and it fails
// against the old canary — the property that makes this a regression test rather than a
// restatement.

import (
	"strings"
	"testing"
)

// quietNightlyStderr is the tail of a real quiet launch that CI rejected: the slow-span
// notice, then the discoverability line. Trimmed to the last two lines because those are
// the whole subject; the banner above them is not in dispute.
const quietNightlyStderr = `Jail: yolo-003-0771862c | podman
yolo: shutdown.stop_loopholes took 1.142s
yolo: timings recorded in /private/var/folders/fq/T/x/003/.yolo/host-perf.log (--timing prints them)
`

// A live slow-span notice is not a report, and a quiet launch is allowed to print one.
func TestQuietContractAcceptsALiveSlowSpanNotice(t *testing.T) {
	if problems := quietLaunchProblems(quietNightlyStderr); len(problems) > 0 {
		t.Errorf("the quiet contract rejected a launch that honored it, over %q.\n"+
			"A notice naming a span that crossed perf.SlowSpanThreshold is RULED IN for a "+
			"quietly-recording launch (docs/reference/perf-logging.md: it \"keeps the live "+
			"slow-span notices\"), so rejecting it makes the test a function of machine "+
			"speed.\nstderr:\n%s", problems, quietNightlyStderr)
	}
	// The sample has to still contain the thing that used to trip the canary, or this test
	// would pass by having been trimmed rather than by the fix.
	if !strings.Contains(quietNightlyStderr, "shutdown.stop_loopholes") {
		t.Fatal("the sample no longer carries the span name the old canary matched, so it " +
			"cannot show that naming a span is allowed")
	}
}

// The other direction, so the contract is not merely permissive: a launch that actually
// PRINTS the report is caught. One case per marker, each the real emitted text.
func TestQuietContractStillCatchesAPrintedReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
	}{
		{"the host table's header", "--- Host-side timing (rc 0) ---\n  Total: 1.000s\n"},
		{"the jail half", "=== YOLO Jail Profile ===\n"},
		{"the report footer, when a table renders without its header", "" +
			"    1.234s    +0.100s    0.050s  shutdown.stop_loopholes\n" +
			"  Total: 1.234s\n" +
			"  host file: /w/.yolo/host-perf.log\n" +
			"  jail half: /w/.yolo/home/yolo-perf.log\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(quietLaunchProblems(tc.stderr)) == 0 {
				t.Errorf("a printed report went unnoticed by the quiet contract:\n%s", tc.stderr)
			}
		})
	}
}

// The trap that decided which markers the contract uses. The entrypoint's boot profile
// prints the report's row shape AND its `Total:` line, by design — the reference calls the
// table "the same register as the entrypoint's boot log" — so neither can be a canary
// without failing every quiet launch that shows the jail's own boot output.
func TestQuietContractDoesNotMistakeTheBootProfileForTheReport(t *testing.T) {
	bootProfile := "" +
		"  boot catalog: 8 steps\n" +
		"    0.010s              -  entrypoint.start\n" +
		"    1.500s    +1.490s  1.480s  genstep.packs\n" +
		"  Total: 1.500s\n"

	if problems := quietLaunchProblems(bootProfile); len(problems) > 0 {
		t.Errorf("the jail's own boot profile was read as the host timing report, over %q — "+
			"a quiet launch that shows boot output would fail for printing nothing of its "+
			"own:\n%s", problems, bootProfile)
	}
}

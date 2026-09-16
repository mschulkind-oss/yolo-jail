package integration

// timing_test.go pins the timing surface END TO END: a real `--timing` launch
// must print the span report on stderr (launch spans, the child window, the
// shutdown chain) and leave <workspace>/.yolo/host-perf.log holding the same
// events, written as they happened. runContainer is out of unit reach, so
// these are the CALL-SITE pins for the spans inside it — delete one from
// run.go and the matching assertion here goes red (the callee pins live in
// internal/cli/run/timingspans_test.go; this file holds the launch-path ones).
//
// It also pins D12 (docs/reference/perf-logging.md), the split between RECORDING
// and REPORTING: every opt-in writes the file, but only an explicit --timing /
// --verbose typed on THIS invocation prints — the table, and the in-container
// `=== YOLO Jail Profile ===` block the YOLO_JAIL_TIMING=1 pair switches on. The
// persistent opt-ins (an exported YOLO_TIMING/YOLO_VERBOSE, `perf_logging: true`)
// record in silence, which is why the two tests below assert an ABSENCE.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// The flag spelling: report on stderr, spans in the file, jail-tagged.
func TestTimingReportSpansShutdown(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, tempProjectConfig)

	res := runCommand(t, dir, append(jailRunArgs(), "--timing", "--", "true"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}

	for _, want := range []string{
		"--- Host-side timing (rc 0) ---",
		"launch.run_with_proxy",
		"shutdown.stop_loopholes",
		"Total:",
		"host file:",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr missing %q;\ngot:\n%s", want, res.stderr)
		}
	}

	logBytes, err := os.ReadFile(filepath.Join(dir, ".yolo", "host-perf.log"))
	if err != nil {
		t.Fatalf("host-perf.log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "jail="+naming.FromWorkspace(dir)) {
		t.Errorf("file not jail-tagged;\ngot:\n%s", log)
	}
	for _, want := range []string{
		"start  launch.run_with_proxy",
		"mark   child.exited",
		"end    shutdown.stop_loopholes",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("host-perf.log missing %q;\ngot:\n%s", want, log)
		}
	}
}

// The env spelling RECORDS BUT DOES NOT REPORT (D12). YOLO_TIMING=1 is what a
// shell profile exports — "always on" — so it writes the file and prints no
// table, no in-container profile block, and exactly one dim line naming the file
// so the data stays discoverable.
//
// This assertion INVERTED on 2026-09-08: it used to demand the report, back when
// recording and reporting were one gate.
func TestTimingEnvVarRecordsWithoutReporting(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, tempProjectConfig)

	res := runCommand(t, dir, append(jailRunArgs(), "--", "true"),
		withEnv(paths.TimingEnv+"=1"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	assertRecordedQuietly(t, dir, res.stderr)
}

// quietLaunchProblems reports what a quietly-recording launch printed that it must
// not have. Empty means the stderr honors D12's quiet contract.
//
// ⚠ "QUIET" MEANS NO REPORT, NOT NO SPAN NAMES, and getting that wrong cost four
// nightly-macOS runs. This list carried `"shutdown.stop_loopholes"` with the comment
// "any span row at all" from 2026-09-08 (`5e26aaf6`) — one day before the reference
// stated that a quiet launch "keeps the live slow-span notices" (`baab8199`) and two
// before it printed the example `yolo: shutdown.window_a took 8.400s` as the expected
// output of a quiet quit (`312119af`). So the canary contradicted a ruling made after
// it, and nothing failed until a machine was slow enough to prove it:
//
//	yolo: shutdown.stop_loopholes took 1.142s
//
// measured on the 2026-09-16 nightly, one span crossing perf.SlowSpanThreshold (1 s)
// on a macOS runner where a Linux runner's shutdown finishes well under it. The launch
// was CORRECT — the notice is the culprit announced live, which is the whole reason the
// threshold exists — and the test was wrong, on a difference in machine speed rather
// than in behaviour.
//
// WHY THESE FOUR AND NOT THE OBVIOUS ONES. The report's row shape (`  %7.3fs  %9s …`)
// and its `  Total:` line both look like ideal canaries and are unusable: the
// ENTRYPOINT's boot profile prints the same two things (`internal/entrypoint/boot.go`),
// deliberately — the reference describes the table as being in "the same register as
// the entrypoint's boot log" — so either would fail a quiet launch for the jail's own
// output. What is left is report-only: its two headers, and the footer pair
// `emitTimingReportLocked` prints under every table it renders (`run.go`). The footer
// also catches rows printed without a header, which is what the deleted canary was
// really guarding.
func quietLaunchProblems(stderr string) []string {
	var problems []string
	for _, unwanted := range []string{
		"--- Host-side timing",      // the host table's header
		"=== YOLO Jail Profile ===", // the in-container half YOLO_JAIL_TIMING=1 switches on
		"host file: ",               // the report footer, printed with every table…
		"jail half: ",               // …and only there
	} {
		if strings.Contains(stderr, unwanted) {
			problems = append(problems, unwanted)
		}
	}
	return problems
}

// assertRecordedQuietly is D12's quiet contract, for whichever persistent opt-in
// a caller turned on: the run block is in the file, both printed halves are
// absent, and the one dim discoverability line names the file.
func assertRecordedQuietly(t *testing.T, dir, stderr string) {
	t.Helper()
	for _, unwanted := range quietLaunchProblems(stderr) {
		t.Errorf("a silently-recording launch printed %q;\nstderr:\n%s", unwanted, stderr)
	}
	if !strings.Contains(stderr, "timings recorded in") {
		t.Errorf("the quiet launch never named its log file, so the data is undiscoverable;"+
			"\nstderr:\n%s", stderr)
	}
	logBytes, err := os.ReadFile(filepath.Join(dir, ".yolo", "host-perf.log"))
	if err != nil {
		t.Fatalf("a recording launch wrote no host-perf.log: %v", err)
	}
	for _, want := range []string{"jail=" + naming.FromWorkspace(dir), "end    shutdown.stop_loopholes"} {
		if !strings.Contains(string(logBytes), want) {
			t.Errorf("host-perf.log missing %q;\ngot:\n%s", want, logBytes)
		}
	}
}

// The OTHER explicit spelling: the global --verbose / -v prints exactly as
// --timing does. It is the trap D12 had to solve — the flag publishes itself as
// YOLO_VERBOSE, the same variable the test above proves is quiet — so this pair
// of tests is where "typed flag" and "exported variable" are shown to differ on a
// real launch, which no unit test can do.
func TestVerboseFlagReportsWhereTheEnvVarDoesNot(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, tempProjectConfig)

	// The flag is GLOBAL: it goes before the subcommand, unlike --timing.
	args := append([]string{"--verbose"}, append(jailRunArgs(), "--", "true")...)
	res := runCommand(t, dir, args)
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	if !strings.Contains(res.stderr, "--- Host-side timing") {
		t.Errorf("--verbose printed no report;\nstderr:\n%s", res.stderr)
	}

	res = runCommand(t, dir, append(jailRunArgs(), "--", "true"),
		withEnv(paths.VerboseEnv+"=1"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	assertRecordedQuietly(t, dir, res.stderr)
}

// The off default: an ordinary launch prints no report and writes no file.
// The gate being default-off is a contract, not a courtesy — every assertion
// above is only meaningful because this one holds.
func TestTimingOffByDefault(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, tempProjectConfig)

	res := runCommand(t, dir, append(jailRunArgs(), "--", "true"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	if strings.Contains(res.stderr, "Host-side timing") {
		t.Errorf("ordinary launch printed a timing report:\n%s", res.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".yolo", "host-perf.log")); !os.IsNotExist(err) {
		t.Errorf("ordinary launch wrote host-perf.log (err=%v)", err)
	}
}

// THE PERSISTENT OPT-IN, end to end: `perf_logging: true` in the USER config
// records for a launch that passes no flag and sets no env var — and prints
// nothing. This is the call-site pin for fillDefaults' read of the key; the unit
// test proves the read, this proves the launch a human actually types honors it.
//
// The quiet half is the whole point of the key after D12: it is the maintainer's
// always-on setting, and a ~25-line table at every jail quit scrolled away
// whatever was on screen. The report assertion here INVERTED on 2026-09-08.
func TestPerfLoggingUserConfigRecordsQuietly(t *testing.T) {
	requireJail(t)
	// packHome writes the USER config (~/.config/yolo-jail/config.jsonc) into an
	// isolated HOME, which is the only scope this key is read from.
	packHome(t, `{"perf_logging": true}`)
	dir := writeProject(t, tempProjectConfig)

	res := runCommand(t, dir, append(jailRunArgs(), "--", "true"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	assertRecordedQuietly(t, dir, res.stderr)
}

// The workspace spelling is refused rather than silently ignored: the key is
// read before any workspace config is loaded, so a value there would look
// accepted and do nothing.
func TestPerfLoggingIsRefusedInWorkspaceScope(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"perf_logging": true}`)

	res := runYoloCLI(t, dir, "check", "--no-build")
	if !strings.Contains(res.combined(), "perf_logging") ||
		!strings.Contains(res.combined(), "user-scope only") {
		t.Errorf("a workspace perf_logging must be a check error naming the scope;\ngot:\n%s",
			res.combined())
	}
}

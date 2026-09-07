package integration

// timing_test.go pins the timing surface END TO END: a real `--timing` launch
// must print the span report on stderr (launch spans, the child window, the
// shutdown chain) and leave <workspace>/.yolo/host-perf.log holding the same
// events, written as they happened. runContainer is out of unit reach, so
// these are the CALL-SITE pins for the spans inside it — delete one from
// run.go and the matching assertion here goes red (the callee pins live in
// internal/cli/run/timingspans_test.go; this file holds the launch-path ones).

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

// The env spelling is equivalent: YOLO_TIMING=1 with no flag enables the same
// surface — the gate run.timingEnabled() promises, pinned where only a real
// launch can prove it.
func TestTimingEnvVarEquivalent(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, tempProjectConfig)

	res := runCommand(t, dir, append(jailRunArgs(), "--", "true"),
		withEnv(paths.TimingEnv+"=1"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	if !strings.Contains(res.stderr, "--- Host-side timing") {
		t.Errorf("YOLO_TIMING=1 printed no report;\nstderr:\n%s", res.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".yolo", "host-perf.log")); err != nil {
		t.Errorf("YOLO_TIMING=1 wrote no host-perf.log: %v", err)
	}
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
// turns the full logging on for a launch that passes no flag and sets no env
// var. This is the call-site pin for the fold in fillDefaults — the unit test
// proves the fold, this proves the launch a human actually types honors it.
func TestPerfLoggingUserConfigEnablesTiming(t *testing.T) {
	requireJail(t)
	// packHome writes the USER config (~/.config/yolo-jail/config.jsonc) into an
	// isolated HOME, which is the only scope this key is read from.
	packHome(t, `{"perf_logging": true}`)
	dir := writeProject(t, tempProjectConfig)

	res := runCommand(t, dir, append(jailRunArgs(), "--", "true"))
	if res.rc != 0 {
		t.Fatalf("rc=%d stderr:\n%s", res.rc, res.stderr)
	}
	if !strings.Contains(res.stderr, "--- Host-side timing") {
		t.Errorf(`"perf_logging": true printed no report;\nstderr:\n%s`, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".yolo", "host-perf.log")); err != nil {
		t.Errorf(`"perf_logging": true wrote no host-perf.log: %v`, err)
	}
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

package entrypoint

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// durablereportpass_test.go pins WHICH PASS walks the durable dir (docs/design/durable-scratch-space.md
// §5.4, DS-D11): the jail's own boot, never a session's pass. In a jail whose main process is a
// hold, that process boots once and its lines reach the launch terminal; every session after it,
// the first included, runs its own pass of the step table on `<runtime> exec`, and an attach used
// to walk the dir again and print the line on the attach's terminal.

// The real step, through the runner: the container's main-process boot and the macos-user
// bootstrap (which has no attach, so every run is a launch's boot) walk the dir and print the
// line; a session's pass of a hold-main jail runs nothing of the step, so it neither prints nor
// marks the perf log. MUTATION: drop the step's notSessionPass, or the runner's check of it, and
// the session case prints the line.
func TestOnlyTheJailsOwnBootWalksTheDurableDir(t *testing.T) {
	ws, dir := durableFixture(t)
	step := mustBootStep(t, "report_durable_dir")
	for _, c := range []struct {
		name        string
		target      bootTarget
		sessionPass bool
		want        bool
	}{
		{"the container's main-process boot", bootContainer, false, true},
		{"a session's pass in a hold-main jail (an attach, or the first session)", bootContainer, true, false},
		{"the macos-user bootstrap", bootDarwin, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stderr, logOnly bytes.Buffer
			e := NewEnv(map[string]string{durable.EnvVar: dir})
			e.Workspace = ws
			e.Stderr = &stderr
			e.LogOnly = &logOnly
			p := newPerfLog()
			runSteps(&bootRun{e: e, target: c.target, perf: p, sessionPass: c.sessionPass}, []bootStep{step})
			printed := strings.Contains(stderr.String(), "Durable dir: ")
			if printed != c.want {
				t.Errorf("printed the durable line = %v, want %v:\n%s", printed, c.want, stderr.String())
			}
			if !c.want && (stderr.Len() != 0 || logOnly.Len() != 0) {
				t.Errorf("a session's pass said something of the durable dir: stderr %q, log %q",
					stderr.String(), logOnly.String())
			}
			var marks []string
			for _, m := range p.entries {
				marks = append(marks, m.label)
			}
			if marked := slices.Contains(marks, "report_durable_dir"); marked != c.want {
				t.Errorf("marked report_durable_dir in the perf log = %v, want %v (marks %v)", marked, c.want, marks)
			}
		})
	}
}

// The runner's rule over a synthetic table: a session's pass skips exactly the steps that declare
// notSessionPass, on the container boot, and a boot pass runs them.
func TestASessionsPassSkipsTheStepsOnlyTheJailsBootRuns(t *testing.T) {
	var ran []string
	record := func(name string) func(*bootRun) { return func(*bootRun) { ran = append(ran, name) } }
	steps := []bootStep{
		{name: "every_pass", run: record("every_pass")},
		{name: "boot_only", run: record("boot_only"), notSessionPass: "a reason given here"},
	}
	newEnv := func() *Env { return &Env{Home: t.TempDir(), Vars: map[string]string{}, Stderr: &strings.Builder{}} }

	runSteps(&bootRun{e: newEnv(), target: bootContainer, sessionPass: true}, steps)
	if want := []string{"every_pass"}; !slices.Equal(ran, want) {
		t.Errorf("a session's pass ran %v, want %v", ran, want)
	}
	ran = nil
	runSteps(&bootRun{e: newEnv(), target: bootContainer}, steps)
	if want := []string{"every_pass", "boot_only"}; !slices.Equal(ran, want) {
		t.Errorf("the jail's boot ran %v, want %v", ran, want)
	}
}

// The durable report is the step a session's pass skips, and says why.
func TestTheDurableReportIsBootOnly(t *testing.T) {
	if why := mustBootStep(t, "report_durable_dir").notSessionPass; len(strings.Fields(why)) < 4 {
		t.Errorf("report_durable_dir runs on every session's pass (notSessionPass %q), against DS-D11", why)
	}
}

// MAIN'S CALL SITE, which no test can drive to its end (the session path execs): the bootRun it
// hands the table says whether this pass is a session's, and a session is one exactly when there
// is a gate, i.e. when the jail's main process is a hold and this invocation is not it. A jail
// with no hold keeps its one pass. Deleting the field, or passing a constant, fails here.
func TestMainTellsTheTableWhetherThisIsASessionsPass(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func Main(args []string) error {")
	if start < 0 {
		t.Fatal("Main not found")
	}
	body = body[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	const want = "runBootSteps(&bootRun{e: e, target: bootContainer, perf: p, sessionPass: gate != nil})"
	if !strings.Contains(body, want) {
		t.Errorf("Main does not run the table as %s, so an attach walks the durable dir again", want)
	}
}

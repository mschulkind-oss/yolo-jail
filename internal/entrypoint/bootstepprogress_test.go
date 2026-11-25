package entrypoint

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// A boot step that runs past a second says so: `Boot step <name>…` and then
// `Boot step <name>: done (N.Ns)`. Immediate stands in for "the step ran long", so no test waits.
func TestASlowBootStepHasAProgressLine(t *testing.T) {
	var out strings.Builder
	e := &Env{Home: t.TempDir(), Vars: map[string]string{}, Stderr: &out,
		progressCfg: progress.Config{Immediate: true}}
	steps := []bootStep{
		{name: "report_durable_dir", run: func(*bootRun) {}},
		{name: "has_own_line", ownProgress: true, run: func(*bootRun) {}},
	}
	runSteps(&bootRun{e: e, target: bootContainer}, steps)
	got := out.String()
	for _, want := range []string{"Boot step report durable dir…", "Boot step report durable dir: done ("} {
		if !strings.Contains(got, want) {
			t.Errorf("boot output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "has own line") {
		t.Errorf("a step that opens its own progress line got a second one from the runner:\n%s", got)
	}
}

// The runner's lines show a step from perf.SlowSpanThreshold on, a second before the two-second
// default the steps that wait on something keep, so every step the host would have named in a
// `took` line has already been said by the jail.
func TestTheBootStepLineAppearsAtTheSlowSpanThreshold(t *testing.T) {
	now := time.Unix(1000, 0)
	tick := make(chan time.Time)
	var out strings.Builder
	e := &Env{Stderr: &out, progressCfg: progress.Config{Now: func() time.Time { return now }, Tick: tick}}
	line := e.progressWithGrace("Boot step x", perf.SlowSpanThreshold)
	now = now.Add(perf.SlowSpanThreshold)
	line.Tick() // the clock, not a sleep, is what a second means here
	if !strings.Contains(out.String(), "Boot step x…") {
		t.Errorf("a step %s in shows no line: %q", perf.SlowSpanThreshold, out.String())
	}
	line.Done("")
	if !strings.Contains(out.String(), "Boot step x: done (") {
		t.Errorf("no result line: %q", out.String())
	}
}

// The table's own-line steps are the three that call Env.progress themselves; a fourth that does
// must declare it, or its wait prints twice.
func TestStepsWithTheirOwnProgressLineDeclareIt(t *testing.T) {
	want := map[string]bool{"report_egress": true, "probe_service_reachability": true,
		"start_jail_daemon_supervisor": true}
	for _, s := range bootSteps() {
		if s.ownProgress != want[s.name] {
			t.Errorf("step %s: ownProgress = %v, want %v", s.name, s.ownProgress, want[s.name])
		}
	}
}

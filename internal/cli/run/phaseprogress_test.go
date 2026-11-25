package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// phaseprogress_test.go pins the launch's phase progress lines and the rule that a phase with a
// shown line is not reported a second time by the slow-span notice ("yolo: <span> took N.NNNs").

// slowNoticeLog is a collector wired to the production slow-span sink, collecting its notices.
func slowNoticeLog(t *testing.T) (*perf.Log, *[]string) {
	t.Helper()
	var notices []string
	return perf.New(time.Now, slowSpanNoticeSink(func(s string) { notices = append(notices, s) })), &notices
}

func forgetSpans(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		spansWithProgress.Delete(n)
	}
	t.Cleanup(func() {
		for _, n := range names {
			spansWithProgress.Delete(n)
		}
	})
}

// A phase whose progress line was shown is reported by that line alone: the line prints
// `<label>: done (N.Ns)` and the slow-span notice for the same span stays silent.
func TestAShownPhaseLineWithholdsTheSlowSpanNotice(t *testing.T) {
	const span = "launch.test_phase"
	forgetSpans(t, span)
	log, notices := slowNoticeLog(t)
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr, Perf: log, Progress: &progress.Config{Immediate: true}}
	ok := o.withPhase(span, "Doing the thing", func() bool {
		slowSpanEnd(log, span) // the span ran past the threshold, as the record would say
		return true
	})
	if !ok {
		t.Fatal("withPhase reported failure for a step that succeeded")
	}
	if !strings.Contains(stderr.String(), "Doing the thing: done (") {
		t.Errorf("no result line:\n%s", stderr.String())
	}
	if len(*notices) != 0 {
		t.Errorf("the slow-span notice repeated a phase its progress line reported: %q", *notices)
	}
}

// A phase whose line stayed silent (it ended inside its grace) is still named by the notice:
// the notice is the only record on the terminal that it was slow.
func TestASilentPhaseLineLeavesTheSlowSpanNotice(t *testing.T) {
	const span = "launch.test_silent_phase"
	forgetSpans(t, span)
	log, notices := slowNoticeLog(t)
	var stderr bytes.Buffer
	// A grace nothing reaches: the line is never shown.
	o := &Options{Stderr: &stderr, Perf: log, Progress: &progress.Config{Grace: time.Hour}}
	o.withPhase(span, "Doing the thing", func() bool {
		slowSpanEnd(log, span)
		return true
	})
	if stderr.Len() != 0 {
		t.Errorf("a line that was never shown printed: %q", stderr.String())
	}
	if len(*notices) != 1 || !strings.HasPrefix((*notices)[0], span+" took ") {
		t.Errorf("notices %q, want the one slow-span line for %s", *notices, span)
	}
}

// The image half's lines and the fork pool's line each report the spans they cover, so a slow
// image build or fork check is not named again by a `took` line.
func TestTheImageAndForkLinesCoverTheirSpans(t *testing.T) {
	forgetSpans(t, append(append([]string{}, imageSpans...), forkSlotSpans...)...)
	log, notices := slowNoticeLog(t)
	o := &Options{Stderr: &bytes.Buffer{}, Perf: log, Progress: &progress.Config{Immediate: true}}
	for _, spans := range [][]string{imageSpans, forkSlotSpans} {
		cfg := o.progressConfigCovering(spans...)
		line := cfg.Start(o.Stderr, "A step")
		for _, s := range spans {
			slowSpanEnd(log, s)
		}
		line.Done("")
	}
	if len(*notices) != 0 {
		t.Errorf("notices for spans a progress line covers: %q", *notices)
	}
}

// The jail's own boot steps are said by the jail (entrypoint runSteps), so the host's copy of the
// perf block names none of them; the provision total, which sums steps no line covers, stays.
func TestBootStepSpansAreNotNamedByTheHost(t *testing.T) {
	log, notices := slowNoticeLog(t)
	slowSpanEnd(log, "container.boot.report_durable_dir")
	slowSpanEnd(log, "container.provision")
	if len(*notices) != 1 || !strings.HasPrefix((*notices)[0], "container.provision took ") {
		t.Errorf("notices %q, want only container.provision", *notices)
	}
}

// THE CALL SITES. Each phase below is reported by a progress line only while its call is in
// the pipeline; deleting one turns its silence back on, and nothing but this fails. A scan
// rather than a launch because the launch needs a container, and these are the spans the
// maintainer's slow launches measured.
func TestEverySlowPhaseIsWiredToAProgressLine(t *testing.T) {
	for file, wants := range map[string][]string{
		"run.go": {`o.withPhase("launch.stage_packs"`, `o.withPhase("launch.auto_capture"`,
			`o.withPhase("launch.reap_orphaned_jails"`, `o.withPhase("launch.refresh_jail_briefings"`},
		"packloopholes.go": {`o.phaseProgressConfig("launch.start_loopholes")`},
		"imageload.go":     {`o.progressConfigCovering(imageSpans...)`},
		"forkbuild.go":     {`o.progressConfigCovering(forkSlotSpans...)`},
		"patchedtrees.go":  {`o.progressConfigCovering(forkSlotSpans...)`},
		"packrefresh.go":   {`o.progressConfigCovering("launch.refresh_configured_packs")`},
	} {
		src, err := os.ReadFile(filepath.Join(".", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(src), want) {
				t.Errorf("%s no longer contains %s: that phase is silent again", file, want)
			}
		}
	}
}

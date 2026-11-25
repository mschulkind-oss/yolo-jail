package run

import (
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// launchprogress.go is how the launcher's long host-side steps say they are still
// running (internal/progress): the image build and delivery (through
// image.AutoLoadOptions.Progress), the source build of yolo's own binaries, the
// store-delivered package builds, and a wait on another launch's housekeeping.
//
// Every line goes to the launch stream, o.Stderr, which is teed to launch.log. On
// a terminal the line redraws in place and the tee keeps the redraws out of the
// log (teeLog.WriteTransient); anywhere else it is written as lines. Either way a
// step that ends within progress.DefaultGrace prints nothing, so a warm launch is
// unchanged. This is progress, never a density control: OQ-RO3's no-quiet-mode
// rule is untouched, and nothing here can hide a line anything else prints.

// progressConfig is the rendering for this launch's stream.
func (o *Options) progressConfig() progress.Config {
	if o.Progress != nil {
		return *o.Progress
	}
	live := o.IsTTYStderr != nil && o.IsTTYStderr() &&
		(o.Getenv == nil || o.Getenv("TERM") != "dumb")
	return progress.Config{
		Live:  live,
		Plain: tty.NoColor(o.Getenv),
		Width: func() int { return tty.Width(os.Stderr.Fd()) },
	}
}

// spansWithProgress is the set of span names whose time a SHOWN progress line already
// reported ("Doing X: done (N.Ns)"). The slow-span notice ("yolo: <span> took N.NNNs",
// slowSpanNoticeSink) withholds itself for an entry, so one piece of work is said
// once. THE RULE: a phase with a progress line that was shown is reported by that line; a
// phase whose line stayed silent (it ended inside its grace period) still gets the notice.
//
// An entry is never removed: the pool's keys run same-named spans side by side, and a launch
// is one process.
var spansWithProgress sync.Map

// forkSlotSpans are the spans the fork-build pool's one line ("Waiting for this launch's
// builds") reports: the slot, and the checks and builds its keys run side by side.
var forkSlotSpans = []string{"launch.fork_builds", "preparation.check", "preparation.build",
	"preparation.copy_install", "preparation.registration", "preparation.admitted_read"}

// imageSpans are the spans the image half's own lines (the nix build, the copier build, the
// copy lock wait, the layer copy) report.
var imageSpans = []string{"launch.auto_load_image", "image.nix_build", "image.copier_build",
	"image.copy_lock", "image.layer_copy"}

// ProgressCovering is cfg with a hook that records spans as reported the moment its line is
// first shown, which is before the span can end. Exported for the host notch's launch
// (internal/cli), whose phases share the slow-span notice's sink.
func ProgressCovering(cfg progress.Config, spans ...string) progress.Config {
	prev := cfg.OnShown
	cfg.OnShown = func() {
		if prev != nil {
			prev()
		}
		for _, s := range spans {
			spansWithProgress.Store(s, struct{}{})
		}
	}
	return cfg
}

// progressConfigCovering is the launch's rendering for a line that reports the time of spans.
func (o *Options) progressConfigCovering(spans ...string) progress.Config {
	return ProgressCovering(o.progressConfig(), spans...)
}

// phaseProgressConfig is progressConfigCovering for a phase that gets no line of its own
// elsewhere: shown from perf.SlowSpanThreshold on, so that every phase the slow-span notice
// would have named has a line instead, and the notice has nothing left to add.
func (o *Options) phaseProgressConfig(spans ...string) progress.Config {
	cfg := o.progressConfig()
	if cfg.Grace <= 0 && !cfg.Immediate {
		cfg.Grace = perf.SlowSpanThreshold
	}
	return ProgressCovering(cfg, spans...)
}

// spanHasProgress reports whether a shown progress line covered span.
func spanHasProgress(span string) bool {
	_, ok := spansWithProgress.Load(span)
	return ok
}

// coveredByBootLine is whether a span is one of the jail's boot steps, recorded on the host
// from the jail's perf block: the entrypoint says each step that ran a second or more itself
// (bootsteps.go, runSteps), on the terminal this launch holds.
func coveredByBootLine(span string) bool { return strings.HasPrefix(span, "container.boot.") }

// abandonLine is the result a step returns to end its line with no result line: the step failed and
// said why itself, and that message stays the last thing printed.
const abandonLine = "\x00abandon"

// withPhase runs step as the launch's phase span, under a progress line "<label>: done (N.Ns)"
// shown once the phase runs a second, with o.Stderr routed through the line as
// withStderrProgress does. step reports success.
func (o *Options) withPhase(span, label string, step func() bool) bool {
	sp := o.Perf.Span(span)
	defer sp.End()
	return o.withProgressLineCfg(o.phaseProgressConfig(span), label, func(*progress.Line) (bool, string) {
		if step() {
			return true, "done"
		}
		return false, abandonLine
	})
}

// withStderrProgress runs step under a progress line on the launch stream, with
// o.Stderr routed THROUGH the line for the step's duration: whatever the step
// prints there (nix's "Building …" summaries) then lands above the live line
// instead of across it. step reports success, which closes the line.
func (o *Options) withStderrProgress(label string, step func() bool) bool {
	return o.withProgressLine(label, func(*progress.Line) (bool, string) {
		if step() {
			return true, "done"
		}
		return false, "failed"
	})
}

func (o *Options) launchStderr() io.Writer {
	if o.progressStderr != nil {
		return o.progressStderr
	}
	return o.Stderr
}

// withProgressLine is withStderrProgress for a step that needs the line itself: to keep its
// detail current (Line.Set) and to print a line above it (Line.Println), as the podman
// readiness gate does (podmanready.go). step returns its success and the result the line
// closes with ("done after 3 attempts"). The line is nil only when o.Stderr is, and every
// method on a nil line is a no-op, so step may use it unconditionally.
func (o *Options) withProgressLine(label string, step func(line *progress.Line) (bool, string)) bool {
	return o.withProgressLineCfg(o.progressConfig(), label, step)
}

// withProgressLineCfg is withProgressLine under a rendering the caller chose.
func (o *Options) withProgressLineCfg(cfg progress.Config, label string, step func(line *progress.Line) (bool, string)) bool {
	line := cfg.Start(o.Stderr, label)
	if line == nil {
		ok, _ := step(nil)
		return ok
	}
	prev := o.Stderr
	prevProgressStderr := o.progressStderr
	if prevProgressStderr == nil {
		o.progressStderr = prev
	}
	o.Stderr = line
	ok, result := step(line)
	o.Stderr = prev
	o.progressStderr = prevProgressStderr
	if result == abandonLine {
		line.Abandon()
	} else {
		line.Done(result)
	}
	return ok
}

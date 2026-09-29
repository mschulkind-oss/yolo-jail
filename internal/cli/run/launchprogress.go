package run

import (
	"os"

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
		Width: func() int { return tty.Width(os.Stderr.Fd()) },
	}
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

// withProgressLine is withStderrProgress for a step that needs the line itself: to keep its
// detail current (Line.Set) and to print a line above it (Line.Println), as the podman
// readiness gate does (podmanready.go). step returns its success and the result the line
// closes with ("done after 3 attempts"). The line is nil only when o.Stderr is, and every
// method on a nil line is a no-op, so step may use it unconditionally.
func (o *Options) withProgressLine(label string, step func(line *progress.Line) (bool, string)) bool {
	line := o.progressConfig().Start(o.Stderr, label)
	if line == nil {
		ok, _ := step(nil)
		return ok
	}
	prev := o.Stderr
	o.Stderr = line
	ok, result := step(line)
	o.Stderr = prev
	line.Done(result)
	return ok
}

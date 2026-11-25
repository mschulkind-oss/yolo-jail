package cli

import (
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// hostphaseprogress.go is the host notch's launch saying that a phase is still running
// (docs/reference/report-tiers.md, "progress"): a phase that runs past perf.SlowSpanThreshold
// prints `<label>…` and then `<label>: done (N.Ns)`, through internal/progress, the same
// line the jail launch's phases print. It is progress and never a density control: it hides
// nothing, and a phase that ends inside the second prints nothing.
//
// A phase with a shown line is reported by it, so the slow-span notice ("yolo: host.<span> took
// N.NNNs") withholds itself for the span (run.ProgressCovering).

// hostProgressConfig is the rendering for the host launch's stream: live on a terminal,
// lines anywhere else, shown from perf.SlowSpanThreshold on, and recording spans as reported.
// A var only so a test can drive the clock and force the rendering.
var hostProgressConfig = func(spans ...string) progress.Config {
	cfg := progress.Config{
		Live:  tty.IsTerminal(os.Stderr.Fd()) && os.Getenv("TERM") != "dumb",
		Width: func() int { return tty.Width(os.Stderr.Fd()) },
		Grace: perf.SlowSpanThreshold,
	}
	return run.ProgressCovering(cfg, spans...)
}

// phase runs step as the launch's span name under a progress line labelled label. step gets the
// writer to print through, which is errw routed through the line, so whatever it prints lands
// above the live line instead of across it. It reports success, which closes the line.
func (t *hostLaunchTrace) phase(errw io.Writer, name, label string, step func(w io.Writer) bool) bool {
	sp := t.span(name)
	defer sp.End()
	line := hostProgressConfig(name).Start(errw, label)
	if line == nil {
		return step(errw)
	}
	ok := step(line)
	if ok {
		line.Done("done")
	} else {
		line.Abandon() // the step's own message is the stop, and stays the last line
	}
	return ok
}

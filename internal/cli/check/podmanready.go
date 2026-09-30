package check

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// podmanready.go is `yolo check`'s half of the podman READINESS GATE
// (docs/design/podman-reboot-readiness.md, a term coined there): the same gate a launch runs,
// with the same budget and the same progress line (PR-D6), asked ONCE per check. Every podman
// question check asks on Linux reads its answer — the Container Runtime section's liveness,
// the runtime resolution Merged Configuration and the later sections use, the image delivery
// section's store facts, and the Disk I/O priority section's storage root — so check keeps no
// probe of its own. The one it had
// discarded stderr, and disagreed with the launch about why podman was down.
//
// Out of scope, as on the launch path (PR-D7): macOS and Apple Container keep their one-shot
// probe.

// defaultPodmanAttempt is the attempt runner check uses when Options.PodmanReadiness leaves it
// nil. A package variable only so this package's TestMain can make it refuse: a unit test
// that reached the gate without a fake would otherwise run the machine's real podman.
var defaultPodmanAttempt runtime.AttemptRunner = runtime.RunPodmanAttempt

// usesReadinessGate is the gate's scope: podman on a Linux host.
func (o *Options) usesReadinessGate(rt string) bool {
	return rt == "podman" && !o.IsMacOS
}

// podmanGate runs the gate the first time it is asked and returns that one result every
// time. The progress line goes to Stderr: the report on Stdout is a formatted artifact
// (maybe JSON), and a wait is not a finding.
func (o *Options) podmanGate() runtime.ReadyResult {
	if o.podmanReady != nil {
		return *o.podmanReady
	}
	seams := o.PodmanReadiness
	if seams.Attempt == nil {
		seams.Attempt = defaultPodmanAttempt
	}
	cfg := progress.Config{
		Live:  o.IsTTYStderr != nil && o.IsTTYStderr() && (o.Getenv == nil || o.Getenv("TERM") != "dumb"),
		Width: func() int { return tty.Width(os.Stderr.Fd()) },
	}
	// A nil Stderr (a section driven directly by a test) draws nothing: Start returns a nil
	// line, on which every method is a no-op.
	line := cfg.Start(o.Stderr, runtime.PodmanReadyLabel)
	res := runtime.WaitForPodmanShowing(line, "podman", seams, nil)
	line.Done(res.DoneText())
	o.podmanReady = &res
	return res
}

// podmanStoreFacts is the image delivery section's `podman info` read, from the gate's
// answer: which namespace and which store a launch's copy would write.
func (o *Options) podmanStoreFacts() image.PodmanStoreFacts {
	res := o.podmanGate()
	if res.Outcome != runtime.PodmanReady {
		return image.PodmanStoreFacts{Unknown: "podman did not answer the readiness check"}
	}
	return image.ParsePodmanStoreFacts(res.Info)
}

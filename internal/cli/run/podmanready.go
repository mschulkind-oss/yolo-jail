package run

import (
	"os"
	"os/signal"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// podmanready.go puts internal/runtime's READINESS GATE (docs/design/podman-reboot-readiness.md,
// a term coined there) in front of a Linux podman launch: runtime selection, whether the
// runtime was named (validateExplicitRuntime) or found (resolveRuntime), waits up to
// runtime.PodmanReadyBudget for `podman info --format json` instead of refusing a podman that
// is still finishing its post-boot cleanup. Its answer is the launch's PODMAN FACTS (also
// coined there): the one `podman info` document every later reader on the launch path takes
// — the host-loopback decision, the image copy's store facts, and the `podman.facts` note —
// so no second `podman info` runs (PR-D5).
//
// macOS (Podman Machine) takes the gate's PATIENT ONE-SHOT instead (runtime.WaitForPodmanMachine,
// PR-D24, a term coined there): one attempt, never retried, because a stopped machine answers
// at once (PR-D7), and waited for up to the same budget, because a machine that has not
// answered is up or coming up. Its answer is not taken as the launch's Podman facts: every
// reader of them is Linux podman's. Apple Container keeps probeAppleContainer's one-shot probe.

// defaultPodmanAttempt is the attempt runner a launch uses when Options.PodmanReadiness leaves
// it nil. A package variable only so this package's TestMain can make it refuse: a unit test
// that reached the gate without a fake would otherwise run the machine's real podman.
var defaultPodmanAttempt runtime.AttemptRunner = runtime.RunPodmanAttempt

// podmanFacts is the gate's successful answer: `podman info --format json` verbatim, and the
// sliver of it the host-loopback decision reads.
type podmanFacts struct {
	json   string
	info   podmanInfo
	parsed bool
}

// usesReadinessGate is the gate's scope: podman on a Linux host, launched on the host or in
// a nested jail. Podman on macOS takes the patient one-shot (usesPatientOneShot); everything
// else keeps probeAppleContainer's one-shot probe.
func (o *Options) usesReadinessGate(rt string) bool {
	return rt == "podman" && !o.IsMacOS // parity: HonoredBy — Apple Container keeps probeAppleContainer's one-shot probe and Podman Machine the patient one-shot (PR-D7, PR-D24): a stopped VM answers at once, so neither retries
}

// usesPatientOneShot is the patient one-shot's scope: podman on macOS, where it is a client of
// the Podman machine's VM (runtime.WaitForPodmanMachine, PR-D24).
func (o *Options) usesPatientOneShot(rt string) bool {
	return rt == "podman" && o.IsMacOS // parity: HonoredBy — Linux podman takes the readiness gate, which retries an early exit; Apple Container keeps probeAppleContainer's one-shot probe
}

// readySeams is Options.PodmanReadiness with the package's attempt runner filled in.
func (o *Options) readySeams() runtime.ReadySeams {
	s := o.PodmanReadiness
	if s.Attempt == nil {
		s.Attempt = defaultPodmanAttempt
	}
	return s
}

// readyInterrupt is the channel a Ctrl-C closes while the gate waits. The attempt runs in its
// own process group, so the terminal's SIGINT reaches yolo alone; it is caught for the wait
// only, so the gate can stop waiting, say which podman it left running, and exit 130.
func (o *Options) readyInterrupt() (<-chan struct{}, func()) {
	if o.PodmanReadiness.Interrupt != nil {
		return o.PodmanReadiness.Interrupt, func() {}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	interrupt, stop := make(chan struct{}), make(chan struct{})
	go func() {
		select {
		case <-sig:
			close(interrupt)
		case <-stop:
		}
	}()
	return interrupt, func() {
		signal.Stop(sig)
		close(stop)
	}
}

// waitForPodman runs the gate for rt — on macOS, the patient one-shot — and reports whether
// podman answered. On success on Linux the answer is o.podmanFacts; on failure the
// runtimeDown is the refusal body and next step its caller prints under the
// runtime-selection headline. Either way the result stays on o.readiness for the
// machine-wide launch line (launchrecord.go), and every perf event is written before this
// returns (PR-D10).
func (o *Options) waitForPodman(rt string) (ok bool, down runtimeDown) {
	seams := o.readySeams()
	interrupt, stopInterrupt := o.readyInterrupt()
	defer stopInterrupt()
	seams.Interrupt = interrupt

	sp := o.Perf.Span("runtime.ready")
	note := func(detail string) { o.Perf.Note("runtime.ready.attempt", detail) }
	var res runtime.ReadyResult
	o.withProgressLine(runtime.PodmanReadyLabel, func(line *progress.Line) (bool, string) {
		if o.usesPatientOneShot(rt) {
			res = runtime.WaitForPodmanMachineShowing(line, rt, seams, note)
		} else {
			res = runtime.WaitForPodmanShowing(line, rt, seams, note)
		}
		return res.Outcome == runtime.PodmanReady, res.DoneText()
	})
	o.readiness = &res
	if res.Outcome == runtime.PodmanReady && !res.Machine {
		o.acceptPodmanFacts(res.Info)
	}
	sp.End()
	if res.Outcome == runtime.PodmanReady {
		return true, runtimeDown{}
	}
	down = runtimeDown{reason: res.Refusal(rt), hint: runtimeStartHint(rt, o.IsMacOS)}
	switch {
	case res.EndedOnScratchError():
		// yolo's own scratch file, on either host: the last attempt never ran podman, and the
		// refusal already names the step its error calls for, so no podman step follows it
		// (PR-D23).
		down.scratch, down.hint = true, ""
	case res.Machine && (res.StillRunning() || res.Outcome == runtime.PodmanInterrupted):
		// A machine that took the probe and did not answer is up or coming up: it is reported
		// as that, with the step that looks at it, never as one to start (PR-D24).
		down.unanswered = true
		down.hint = runtime.PodmanMachineBusyHint("launch again")
	}
	return false, down
}

// acceptPodmanFacts makes info — the gate's answer — the launch's Podman facts, and notes
// the facts that decide how a jail's podman client exits (`podman.facts`) from THIS answer:
// the launch asks podman once (PR-D5).
func (o *Options) acceptPodmanFacts(info string) {
	facts := &podmanFacts{json: info}
	facts.info, facts.parsed = parsePodmanInfo(info)
	o.podmanFacts = facts
	if facts.parsed {
		o.Perf.Note("podman.facts", podmanFactsNote(facts.info))
	}
}

// readinessInterrupted reports whether the launch's gate ended on a Ctrl-C: the launch then
// exits 130, the shell's code for an interrupted command.
func (o *Options) readinessInterrupted() bool {
	return o.readiness != nil && o.readiness.Outcome == runtime.PodmanInterrupted
}

// storeFactsFromGate is the image load's StoreFacts seam on the run path: the rootless answer
// and the store, parsed from the gate's answer rather than asked for again. A launch that has
// no gate answer (the gate is Linux podman's, and only that arm copies into a store) gets the
// unknown answer, which takes the bare copy and says so.
func (o *Options) storeFactsFromGate() image.PodmanStoreFacts {
	if o.podmanFacts == nil {
		return image.PodmanStoreFacts{Unknown: "the launch's podman readiness check did not answer"}
	}
	return image.ParsePodmanStoreFacts(o.podmanFacts.json)
}

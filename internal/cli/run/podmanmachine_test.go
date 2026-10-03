package run

// podmanmachine_test.go pins macOS's podman probe, the patient one-shot (PR-D24 of
// docs/design/podman-reboot-readiness.md, internal/runtime/podmanmachine.go), THROUGH runtime
// selection: every case runs down each of the three ways a launch reaches it (YOLO_RUNTIME,
// the config's `runtime` key, autodetection), so deleting the probe from validateExplicitRuntime
// or from resolveRuntime's candidate loop fails every case here.
//
// The fake is a Podman machine, not a probe result: it answers `podman info` after a set time
// through whichever seam the launch asks it by, Exec (whose timeout kills the probe) or the
// readiness gate's attempt runner (whose deadline it outlives, and is left running). So a test
// states what the MACHINE does, and the launch's bound decides what the launch sees.

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// stoppedMachineStderr is podman 5.8.4's answer for a machine that is not running: the client's
// dial is refused, and cmd/podman/root.go prints the banner for the ConnectError.
const stoppedMachineStderr = "Cannot connect to Podman. Please verify your connection to the Linux system " +
	"using `podman system connection list`, or try `podman machine init` and `podman machine start` " +
	"to manage a new Linux VM\nError: unable to connect to Podman socket: failed to connect: " +
	"dial tcp 127.0.0.1:50655: connect: connection refused\n"

// fakeMachine is a Podman machine on a fake clock. A running one answers `podman info` after
// answersAfter; a stopped one answers at once, exit 125, with stoppedMachineStderr.
type fakeMachine struct {
	answersAfter time.Duration
	stopped      bool
	// scratch, when set, is the errno yolo's own scratch file fails with: no attempt ever
	// runs podman (PR-D23).
	scratch syscall.Errno
	now     time.Time
	probes  []string // every `podman info` asked, "<seam>: <argv>"
}

// asMac makes o a macOS launch whose only runtime on PATH is podman, reaching runtime selection
// down path p, with m answering every `podman info` it asks.
func (m *fakeMachine) asMac(p gatePath, stdout, stderr *bytes.Buffer) *Options {
	o := &Options{IsMacOS: true}
	fillDefaults(o)
	o.Getenv = p.getenv
	o.LookPath = func(name string) (string, bool) { return "/opt/podman/bin/" + name, name == "podman" }
	o.Stdout, o.Stderr = stdout, stderr
	m.now = time.Unix(1_000_000, 0)
	o.Exec = func(argv []string, _ string, _ []string, timeout time.Duration) ExecResult {
		if len(argv) < 2 || argv[0] != "podman" || argv[1] != "info" {
			return ExecResult{Ran: false}
		}
		m.probes = append(m.probes, "exec: "+strings.Join(argv, " "))
		switch {
		case m.stopped:
			return ExecResult{Ran: true, RC: 125, Stderr: stoppedMachineStderr}
		case timeout < m.answersAfter:
			m.now = m.now.Add(timeout)
			return ExecResult{Ran: true, Timeout: true}
		}
		m.now = m.now.Add(m.answersAfter)
		return ExecResult{Ran: true, RC: 0, Stdout: podmanInfoFixture}
	}
	o.PodmanReadiness = yoloruntime.ReadySeams{
		Attempt: func(argv []string, deadline time.Time, _ <-chan struct{}) yoloruntime.Attempt {
			m.probes = append(m.probes, "attempt: "+strings.Join(argv, " "))
			switch left := deadline.Sub(m.now); {
			case m.scratch != 0:
				return yoloruntime.Attempt{StartErr: &yoloruntime.ProbeScratchError{
					Err: &os.PathError{Op: "open", Path: "/tmp/yolo-podman-ready-1.out", Err: m.scratch}}}
			case m.stopped:
				return yoloruntime.Attempt{Exited: true, RC: 125, Stderr: stoppedMachineStderr, Pid: 4242,
					Duration: 300 * time.Millisecond}
			case left < m.answersAfter:
				m.now = deadline
				return yoloruntime.Attempt{Pid: 4242, Duration: left}
			}
			m.now = m.now.Add(m.answersAfter)
			return yoloruntime.Attempt{Exited: true, RC: 0, Stdout: podmanInfoFixture, Pid: 4242,
				Duration: m.answersAfter}
		},
		Now: func() time.Time { return m.now },
		Sleep: func(d time.Duration, _ <-chan struct{}) bool {
			m.now = m.now.Add(d)
			return true
		},
		Interrupt: make(chan struct{}),
	}
	return o
}

// The Intel macOS nightly of 2026-10-03: a Podman machine that is running but busy answers
// `podman info` after the 10 s the launch used to kill it at. The launch waits for it and takes
// podman, on every path to runtime selection.
func TestAPodmanMachineThatAnswersAfterTheOldBoundIsWaitedFor(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			m := &fakeMachine{answersAfter: 15 * time.Second}
			o := m.asMac(p, &stdout, &stderr)
			rt, ok := o.resolveRuntime(p.cfg)
			if !ok || rt != "podman" {
				t.Fatalf("resolveRuntime = %q,%v; want podman: a machine that answers in 15 s is running\n"+
					"probes: %q\nstdout:\n%s\nstderr:\n%s", rt, ok, m.probes, stdout.String(), stderr.String())
			}
			if len(m.probes) != 1 {
				t.Errorf("asked podman %d times, want once: %q", len(m.probes), m.probes)
			}
		})
	}
}

// A machine that does not answer within the whole budget is reported as what was seen: podman
// did not answer, and the machine may be busy or still starting. It is never called "not
// started", and the next step is to look at the machine and wait, not to start it. The probe
// is asked once and left running.
func TestAPodmanMachineThatNeverAnswersIsNotCalledStopped(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			m := &fakeMachine{answersAfter: time.Hour}
			o := m.asMac(p, &stdout, &stderr)
			if rt, ok := o.resolveRuntime(p.cfg); ok || rt != "" {
				t.Fatalf("resolveRuntime = %q,%v; want a refusal", rt, ok)
			}
			got := stdout.String()
			for _, want := range []string{
				"podman info did not answer within 60s",
				"the Podman machine may be busy or still starting",
				"podman (pid 4242) is still running; yolo left it to finish.",
				"`podman machine list`",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("refusal lacks %q:\n%s", want, got)
				}
			}
			for _, wrong := range []string{"not started", "Start it:", "timed out"} {
				if strings.Contains(got, wrong) {
					t.Errorf("a machine that did not answer was reported with %q:\n%s", wrong, got)
				}
			}
			if len(m.probes) != 1 {
				t.Errorf("asked podman %d times, want once: %q", len(m.probes), m.probes)
			}
		})
	}
}

// A stopped machine answers at once, and that answer is final (PR-D7's reason stands): one
// probe, no wait, no retry, and the refusal says it is not started and how to start it, with
// podman's own reason.
func TestAStoppedPodmanMachineIsRefusedAtOnceWithTheStartHint(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			m := &fakeMachine{stopped: true}
			o := m.asMac(p, &stdout, &stderr)
			start := m.now
			if rt, ok := o.resolveRuntime(p.cfg); ok || rt != "" {
				t.Fatalf("resolveRuntime = %q,%v; want a refusal", rt, ok)
			}
			got := stdout.String()
			for _, want := range []string{"installed but not started", "`podman machine start`",
				"connect: connection refused"} {
				if !strings.Contains(got, want) {
					t.Errorf("refusal lacks %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "podman machine list") {
				t.Errorf("a stopped machine was sent to look at its state, not to start it:\n%s", got)
			}
			if len(m.probes) != 1 {
				t.Errorf("asked a stopped machine %d times, want once (no retry): %q", len(m.probes), m.probes)
			}
			if waited := m.now.Sub(start); waited > time.Second {
				t.Errorf("waited %s on a machine that answered at once", waited)
			}
		})
	}
}

// A patient one-shot that ends on yolo's own scratch file never ran podman (PR-D23), so the
// Podman machine is neither stopped nor busy: the refusal says yolo could not create its scratch
// file and names that error's fix, and no headline or step points at the machine, on every path
// to runtime selection.
func TestAPodmanMachineProbeThatNeverRanPodmanNamesYolosScratchFile(t *testing.T) {
	for _, p := range gatePaths() {
		t.Run(p.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			m := &fakeMachine{scratch: syscall.ENOSPC}
			o := m.asMac(p, &stdout, &stderr)
			if rt, ok := o.resolveRuntime(p.cfg); ok || rt != "" {
				t.Fatalf("resolveRuntime = %q,%v; want a refusal", rt, ok)
			}
			got := stdout.String()
			for _, want := range []string{"Cannot query", "so podman info never ran (1 attempt",
				"no space left on device", "Fix: free space in the temporary directory"} {
				if !strings.Contains(got, want) {
					t.Errorf("refusal lacks %q:\n%s", want, got)
				}
			}
			for _, wrong := range []string{"not started", "not answering", "podman machine", "did not answer"} {
				if strings.Contains(got, wrong) {
					t.Errorf("a probe that never ran podman was reported with %q:\n%s", wrong, got)
				}
			}
			if len(m.probes) != 1 {
				t.Errorf("asked %d times, want once (the patient one-shot retries nothing): %q", len(m.probes), m.probes)
			}
		})
	}
}

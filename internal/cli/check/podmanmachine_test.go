package check

// podmanmachine_test.go pins `yolo check`'s half of macOS's podman probe, the patient one-shot
// (PR-D24 of docs/design/podman-reboot-readiness.md, internal/runtime/podmanmachine.go),
// through the whole report (Check), so deleting the probe from the Container Runtime section or
// from the runtime resolution Merged Configuration reads fails here.
//
// The fake is a Podman machine, not a probe result: it answers `podman info` after a set time
// through whichever seam check asks it by, Exec (whose timeout kills the probe) or the readiness
// gate's attempt runner (whose deadline it outlives, and is left running).

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// fakeMachine is a Podman machine on a fake clock: a running one answers `podman info` after
// answersAfter.
type fakeMachine struct {
	answersAfter time.Duration
	// scratch, when set, is the errno yolo's own scratch file fails with: no attempt ever
	// runs podman (PR-D23).
	scratch syscall.Errno
	now     time.Time
	probes  []string // every `podman info` asked, "<seam>: <argv>"
}

// asMac makes opts a macOS check with podman (only) on PATH, and m answering it.
func (m *fakeMachine) asMac(t *testing.T, opts *Options) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	opts.IsMacOS = true
	opts.LookPath = func(name string) (string, bool) { return "/opt/podman/bin/" + name, name == "podman" }
	m.now = time.Unix(1_000_000, 0)
	opts.Exec = func(argv []string, _ string, _ []string, timeout time.Duration) ExecResult {
		switch key := strings.Join(argv, " "); {
		case key == "podman --version":
			return ExecResult{Ran: true, RC: 0, Stdout: "podman version 5.8.4"}
		case strings.HasPrefix(key, "podman info"):
			m.probes = append(m.probes, "exec: "+key)
			if timeout < m.answersAfter {
				m.now = m.now.Add(timeout)
				return ExecResult{Ran: true, Timeout: true}
			}
			m.now = m.now.Add(m.answersAfter)
			return ExecResult{Ran: true, RC: 0, Stdout: "{}"}
		}
		return ExecResult{Ran: false}
	}
	opts.PodmanReadiness = runtime.ReadySeams{
		Attempt: func(argv []string, deadline time.Time, _ <-chan struct{}) runtime.Attempt {
			m.probes = append(m.probes, "attempt: "+strings.Join(argv, " "))
			if m.scratch != 0 {
				return runtime.Attempt{StartErr: &runtime.ProbeScratchError{
					Err: &os.PathError{Op: "open", Path: "/tmp/yolo-podman-ready-1.out", Err: m.scratch}}}
			}
			if left := deadline.Sub(m.now); left < m.answersAfter {
				m.now = deadline
				return runtime.Attempt{Pid: 4242, Duration: left}
			}
			m.now = m.now.Add(m.answersAfter)
			return runtime.Attempt{Exited: true, RC: 0, Stdout: "{}", Pid: 4242, Duration: m.answersAfter}
		},
		Now: func() time.Time { return m.now },
		Sleep: func(d time.Duration, _ <-chan struct{}) bool {
			m.now = m.now.Add(d)
			return true
		},
		Interrupt: make(chan struct{}),
	}
}

// A running, busy machine that answers after the 10 s check used to kill its probe at is a
// working runtime: [PASS], no runtime finding, and podman asked once for the whole report.
func TestCheckWaitsForAPodmanMachineThatAnswersAfterTheOldBound(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	m := &fakeMachine{answersAfter: 15 * time.Second}
	m.asMac(t, &opts)
	Check(opts)
	got := stripANSI(out.String())
	section := sectionOf(t, got, "Container Runtime")
	if !strings.Contains(section, "[PASS] podman: podman version 5.8.4") {
		t.Errorf("a machine that answers in 15 s was not reported working:\n%s", section)
	}
	for _, wrong := range []string{"not started", "not connected", "not answering", "[FAIL]"} {
		if strings.Contains(section, wrong) {
			t.Errorf("the Container Runtime section says %q of a machine that answered:\n%s", wrong, section)
		}
	}
	if len(m.probes) != 1 {
		t.Errorf("asked podman %d times for one report, want once: %q", len(m.probes), m.probes)
	}
}

// A machine that never answers is ONE finding, saying what was seen and naming the step that
// looks at the machine; never "not started", never "START it".
func TestCheckReportsAPodmanMachineThatNeverAnswersAsNotAnswering(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	m := &fakeMachine{answersAfter: time.Hour}
	m.asMac(t, &opts)
	if rc := Check(opts); rc != 1 {
		t.Errorf("exit = %d, want 1: no runtime answered", rc)
	}
	got := stripANSI(out.String())
	section := sectionOf(t, got, "Container Runtime")
	for _, want := range []string{
		"[FAIL] Container runtime installed but not answering (podman: podman version 5.8.4)",
		"podman info did not answer within 60s",
		"the Podman machine may be busy or still starting",
		"`podman machine list`",
		"wait for it to settle and run `yolo check` again.",
		"`podman machine stop`, then `podman machine start`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the finding lacks %q:\n%s", want, section)
		}
	}
	for _, wrong := range []string{"not started", "START it", "liveness probe failed"} {
		if strings.Contains(got, wrong) {
			t.Errorf("a machine that did not answer was reported with %q:\n%s", wrong, got)
		}
	}
	if c := strings.Count(section, "[FAIL]"); c != 1 {
		t.Errorf("%d [FAIL] rows for one silent machine, want 1:\n%s", c, section)
	}
	if len(m.probes) != 1 {
		t.Errorf("asked podman %d times for one report, want once: %q", len(m.probes), m.probes)
	}
}

// On Linux too, a podman still running at the end of the gate's budget is not answering, not
// "not started": the finding says what was seen, with the gate's refusal and the pid it left.
func TestCheckReportsALinuxPodmanStillRunningAsNotAnswering(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 6.1.2", Ran: true, RC: 0},
	})
	podmanAnswers(&opts, ExecResult{Ran: true, Timeout: true})
	r := newReporter(&out, false)
	opts.sectionContainerRuntime(r)
	got := stripANSI(out.String())
	for _, want := range []string{
		"[FAIL] Container runtime installed but not answering (podman: podman version 6.1.2)",
		"podman info did not answer within 60s", "podman (pid 4242) is still running",
		"Run 'podman info' to diagnose",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the finding lacks %q:\n%s", want, got)
		}
	}
	for _, wrong := range []string{"not started", "START it", "podman machine"} {
		if strings.Contains(got, wrong) {
			t.Errorf("a podman still running was reported with %q:\n%s", wrong, got)
		}
	}
	if r.failed != 1 {
		t.Errorf("failed=%d, want one finding:\n%s", r.failed, got)
	}
}

// A patient one-shot that ends on yolo's own scratch file never ran podman (PR-D23): ONE finding,
// naming the scratch file and its fix, and nothing in the report calls the machine stopped or
// busy or sends the user to it.
func TestCheckNamesYolosScratchFileForAPodmanMachineProbeThatNeverRan(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	m := &fakeMachine{scratch: syscall.ENOSPC}
	m.asMac(t, &opts)
	if rc := Check(opts); rc != 1 {
		t.Errorf("exit = %d, want 1: no runtime answered", rc)
	}
	got := stripANSI(out.String())
	section := sectionOf(t, got, "Container Runtime")
	for _, want := range []string{"[FAIL] podman not checked: yolo could not create the scratch file",
		"so podman info never ran (1 attempt", "Fix: free space in the temporary directory"} {
		if !strings.Contains(got, want) {
			t.Errorf("the finding lacks %q:\n%s", want, section)
		}
	}
	for _, wrong := range []string{"not started", "not answering", "START it", "did not answer"} {
		if strings.Contains(got, wrong) {
			t.Errorf("a probe that never ran podman was reported with %q:\n%s", wrong, got)
		}
	}
	// The macOS Platform section's own `podman machine info` probe is not this finding's step.
	if strings.Contains(section, "podman machine") {
		t.Errorf("the finding sends the user to the Podman machine:\n%s", section)
	}
	if c := strings.Count(section, "[FAIL]"); c != 1 {
		t.Errorf("%d [FAIL] rows for one cause, want 1:\n%s", c, section)
	}
	if len(m.probes) != 1 {
		t.Errorf("asked %d times for one report, want once: %q", len(m.probes), m.probes)
	}
}

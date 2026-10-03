package check

// podmanready_test.go pins `yolo check`'s use of the podman readiness gate (podmanready.go):
// the same gate a launch runs (PR-D6 of docs/design/podman-reboot-readiness.md), asked once,
// and read by every section that needs podman's answer on Linux. Each test fails if check
// keeps a `podman info` of its own.

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// A whole check on a Linux podman that is busy for two attempts: one gate, three attempts,
// the Container Runtime section and Merged Configuration's runtime resolution both satisfied
// from it, and never an `info` through Exec. (The image delivery section's read of the same
// answer is pinned in section_imagedelivery_test.go: this fixture stops before that section.)
func TestCheckAsksTheReadinessGateOnceForEveryReader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out, errOut bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Stderr = &errOut
	opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	var execInfo []string
	opts.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "info" {
			execInfo = append(execInfo, strings.Join(argv, " "))
		}
		if strings.Join(argv, " ") == "podman --version" {
			return ExecResult{Ran: true, RC: 0, Stdout: "podman version 6.1.2"}
		}
		return ExecResult{Ran: false}
	}
	busy := runtime.Attempt{Exited: true, RC: 125, Stderr: "Error: database is locked"}
	gate := scriptedPodman(&opts, busy, busy, runtime.Attempt{Exited: true, RC: 0, Stdout: `{"host":{}}`})

	Check(opts)
	got := stripANSI(out.String())
	if !strings.Contains(got, "[PASS] podman: podman version 6.1.2") {
		t.Errorf("the runtime section did not pass on the gate's late answer:\n%s", got)
	}
	if gate.count() != 3 {
		t.Errorf("the gate ran %d attempts, want 3: check asks it once and every section reads that", gate.count())
	}
	if len(execInfo) != 0 {
		t.Errorf("check ran its own podman info: %v", execInfo)
	}
	if c := strings.Count(errOut.String(), "podman info: exit 125: Error: database is locked; retrying in"); c != 2 {
		t.Errorf("printed %d retry lines on stderr, want 2:\n%s", c, errOut.String())
	}
}

// check's runtime resolution — named or found — reaches the gate, and refuses a permanent
// error at once with the launch's own words and fix.
func TestChecksRuntimeResolutionReadsTheGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		getenv func(string) string
		cfg    *jsonx.OrderedMap
	}{
		{"YOLO_RUNTIME", func(k string) string {
			if k == "YOLO_RUNTIME" {
				return "podman"
			}
			return ""
		}, nil},
		{"config runtime", func(string) string { return "" }, jsonx.NewOrderedMap()},
		{"autodetected", func(string) string { return "" }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			opts.Getenv = tc.getenv
			if tc.cfg != nil {
				tc.cfg.Set("runtime", "podman")
			}
			opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
			gate := answeringPodman(&opts, `{}`)
			if rt, msg := opts.runtimeForCheck(tc.cfg); rt != "podman" || msg != "" {
				t.Fatalf("runtimeForCheck = %q, %q", rt, msg)
			}
			if gate.count() != 1 {
				t.Errorf("gate attempts = %d, want 1", gate.count())
			}

			var out2 bytes.Buffer
			opts2 := baseOptions(t, &out2)
			opts2.Getenv = tc.getenv
			opts2.LookPath = opts.LookPath
			refused := scriptedPodman(&opts2, runtime.Attempt{Exited: true, RC: 125,
				Stderr: `Error: invalid internal status, try resetting the pause process with "podman system migrate"`})
			if rt, _ := opts2.runtimeForCheck(tc.cfg); rt != "" {
				t.Fatalf("a permanent error resolved runtime %q", rt)
			}
			if refused.count() != 1 {
				t.Errorf("a permanent error was retried: %d attempts", refused.count())
			}
		})
	}
}

// The Container Runtime section's failure carries the gate's refusal: the fix first, then
// podman's own line and the fix the table names.
func TestTheRuntimeSectionCarriesTheGatesRefusal(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 6.1.2", Ran: true, RC: 0},
	})
	scriptedPodman(&opts, runtime.Attempt{Exited: true, RC: 125, Stderr: "cannot clone: Operation not permitted\nError: cannot re-exec process"})
	r := newReporter(&out, false)
	opts.sectionContainerRuntime(r)
	got := stripANSI(out.String())
	for _, want := range []string{"-> podman: Run 'podman info' to diagnose", "does not clear on its own",
		"cannot clone: Operation not permitted", "Fix: podman cannot create its user namespace"} {
		if !strings.Contains(got, want) {
			t.Errorf("section lacks %q:\n%s", want, got)
		}
	}
}

// macOS asks the gate's patient one-shot (PR-D24): once for the whole section, through the
// gate's runner with the whole budget as its deadline, and never through Exec, whose timeout
// kills the probe. podmanmachine_test.go pins what it reports.
func TestCheckOnMacOSAsksThePatientOneShotOnce(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.IsMacOS = true
	opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	execProbes := 0
	opts.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch key := strings.Join(argv, " "); {
		case key == "podman --version":
			return ExecResult{Stdout: "podman version 5.8.4", Ran: true, RC: 0}
		case strings.HasPrefix(key, "podman info"):
			execProbes++
		}
		return ExecResult{Ran: false}
	}
	now := time.Unix(1_000_000, 0)
	var deadlines []time.Duration
	opts.PodmanReadiness = runtime.ReadySeams{
		Attempt: func(_ []string, deadline time.Time, _ <-chan struct{}) runtime.Attempt {
			deadlines = append(deadlines, deadline.Sub(now))
			return runtime.Attempt{Exited: true, RC: 0, Stdout: `{}`, Pid: 1}
		},
		Now:       func() time.Time { return now },
		Interrupt: make(chan struct{}),
	}
	r := newReporter(&out, false)
	if rt := opts.sectionContainerRuntime(r); rt != "podman" {
		t.Fatalf("detected %q", rt)
	}
	if len(deadlines) != 1 || deadlines[0] != runtime.PodmanReadyBudget {
		t.Errorf("attempt deadlines = %v; want one, the whole %s budget", deadlines, runtime.PodmanReadyBudget)
	}
	if execProbes != 0 {
		t.Errorf("ran `podman info` through Exec %d times, whose timeout kills it", execProbes)
	}
}

// A gate that ends on yolo's own scratch file never ran podman, so check's one row for it names
// that error and the temporary directory's fix: no "Run 'podman info'", and no "start it".
func TestTheRuntimeSectionNamesYolosScratchFileNotPodman(t *testing.T) {
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 6.1.2", Ran: true, RC: 0},
	})
	scriptedPodman(&opts, runtime.Attempt{StartErr: &runtime.ProbeScratchError{
		Err: &os.PathError{Op: "open", Path: "/tmp/yolo-podman-ready-1.out", Err: syscall.ENOSPC}}})
	r := newReporter(&out, false)
	if rt := opts.sectionContainerRuntime(r); rt != "" {
		t.Fatalf("detected %q from a gate that never ran podman", rt)
	}
	got := stripANSI(out.String())
	for _, want := range []string{"[FAIL] podman not checked: yolo could not create the scratch file",
		"no space left on device", "Fix: free space in the temporary directory"} {
		if !strings.Contains(got, want) {
			t.Errorf("section lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"podman info' to diagnose", "START it", "not started"} {
		if strings.Contains(got, bad) {
			t.Errorf("the section points at podman (%q) for a podman that never ran:\n%s", bad, got)
		}
	}
	if n := strings.Count(got, "[FAIL]"); n != 1 {
		t.Errorf("one cause, %d [FAIL] rows:\n%s", n, got)
	}
}

package cli

// stopgone_test.go pins terminal-state verification, not the cause of a runtime wedge.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// stuckStopRuntime is a fake podman whose container starts running and is NOT removed by the stop
// the runtime reports it took (rc 0). present flips false only when a
// forced `rm` takes it, and removable decides whether that takes; the existence probe then answers
// like podman's, an inspect of an absent container exiting non-zero.
type stuckStopRuntime struct {
	cname     string
	present   bool
	removable bool
	calls     [][]string
}

func (s *stuckStopRuntime) run(argv []string, _ time.Duration) (string, bool, int) {
	s.calls = append(s.calls, argv)
	switch {
	case argv[1] == "inspect" && slices.Contains(argv, "{{.State.Running}}"):
		if s.present {
			return "true\n", true, 0
		}
		return "false\n", true, 0
	case argv[1] == "inspect" && slices.Contains(argv, "{{.State.Status}}"):
		if s.present {
			return "running\n", true, 0
		}
		return "", true, 125
	case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
		if s.present {
			return "", true, 0
		}
		return "", true, 1
	case argv[1] == "stop":
		return "", true, 0
	case argv[1] == "rm":
		if s.removable {
			s.present = false
		}
		return "", true, 125 // podman's forced rm exits 125 and still removes (JL-D82)
	}
	return "", false, 1
}

// shrinkStopGone shrinks stopContainerGone's wait so a test need not wait out its bound.
func shrinkStopGone(t *testing.T) {
	savedWait, savedPoll := stopGoneWait, stopGonePoll
	stopGoneWait, stopGonePoll = 20*time.Millisecond, time.Millisecond
	t.Cleanup(func() { stopGoneWait, stopGonePoll = savedWait, savedPoll })
}

// TestAStopSaysStoppedOnlyOnceTheContainerIsGone is the regression: a stop whose container is still
// there — plain AND --force — must fail, name the container's exact state and the next step, and
// never say "Stopped". Before the change the runtime's rc-0 stop and the keeper's rc-0 teardown were
// the whole judgement, so both said "Stopped" while the container was Up.
func TestAStopSaysStoppedOnlyOnceTheContainerIsGone(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "--force"}[force], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			shrinkStopGone(t)
			cname := runtime.FromWorkspace("/ws")
			f := &stuckStopRuntime{cname: cname, present: true, removable: false}
			var out, errb bytes.Buffer
			rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", f.run, nil, force)
			if rc == 0 {
				t.Fatalf("a stop whose container is still there reported success\nstdout:\n%s", out.String())
			}
			if strings.Contains(out.String(), "Stopped ") {
				t.Errorf("a stop whose container is still there said Stopped:\n%s", out.String())
			}
			for _, want := range []string{
				`state "present"`,
				"`podman rm -f " + cname + "`",
				"runtime administrator",
			} {
				if !strings.Contains(errb.String(), want) {
					t.Errorf("the failure must name %q:\n%s", want, errb.String())
				}
			}
		})
	}
}

// TestAStopForceRemovesAContainerItsStopLeft: a container the runtime reported stopped but left
// there is force-removed, and the stop answers from the existence probe, not the removal's rc (the
// fake's rm exits 125 and still removes, JL-D82). Only then does it say "Stopped".
func TestAStopForceRemovesAContainerItsStopLeft(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shrinkStopGone(t)
	cname := runtime.FromWorkspace("/ws")
	f := &stuckStopRuntime{cname: cname, present: true, removable: true}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", f.run, nil, false); rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "Stopped "+cname) {
		t.Errorf("the stop must say it stopped the jail:\n%s", out.String())
	}
	var removed []string
	for _, c := range f.calls {
		if len(c) > 1 && c[1] == "rm" {
			removed = c
		}
	}
	if want := []string{"podman", "rm", "--force", cname}; !slices.Equal(removed, want) {
		t.Errorf("the forced removal argv = %v, want %v", removed, want)
	}
}

// TestAStopCannotSayStoppedWhenTheRuntimeCannotBeAsked: a runtime that will not answer the existence
// probe is never proof the container is gone (JL-P3), so the stop is a failure, not "Stopped".
func TestAStopCannotSayStoppedWhenTheRuntimeCannotBeAsked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &stopRun{stats: []string{"true\n", "", "-"}} // the stop runs; the existence probe cannot
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", s.run, nil, false); rc != 1 {
		t.Fatalf("rc=%d, want 1\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "Stopped ") {
		t.Errorf("a stop that could not ask said Stopped:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "could not be asked") {
		t.Errorf("the stop must say it could not ask:\n%s", errb.String())
	}
}

func TestStopRefusesUnansweredExistenceAcrossPaths(t *testing.T) {
	for _, path := range []string{"running", "force", "leftover", "absent"} {
		for _, runtimeFailure := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "-exit125", true: "-not-run"}[runtimeFailure], func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				saved := finishStop
				finishStop = func(_, _ io.Writer, _, _, _ string, _ int64, _ func(string, string, string), _ bool) int { return 0 }
				t.Cleanup(func() { finishStop = saved })
				existsCalls := 0
				runner := func(argv []string, _ time.Duration) (string, bool, int) {
					switch argv[1] {
					case "inspect":
						if slices.Contains(argv, "{{.State.Running}}") {
							return strconv.FormatBool(path == "running" || path == "force"), true, 0
						}
						return "exited", true, 0
					case "stop", "rm":
						return "", true, 0
					case "container":
						existsCalls++
						if path == "leftover" && existsCalls == 1 {
							return "", true, 0
						}
						return "", !runtimeFailure, 125
					}
					t.Fatalf("unexpected argv %v", argv)
					return "", false, 1
				}
				var out, errb bytes.Buffer
				if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", runner, nil, path == "force"); rc != 1 {
					t.Fatalf("rc=%d stdout=%s stderr=%s", rc, &out, &errb)
				}
				if strings.Contains(out.String(), "Stopped ") || strings.Contains(out.String(), "Ended the leftover") || strings.Contains(out.String(), "No jail running") {
					t.Fatalf("false success: %s", &out)
				}
				if !strings.Contains(errb.String(), "could not be asked") {
					t.Fatalf("missing unknown-state refusal: %s", &errb)
				}
			})
		}
	}
}

func TestStopUnknownProbeAfterForceRemovalIsNotSuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := &stuckStopRuntime{present: true}
	runner := func(argv []string, _ time.Duration) (string, bool, int) {
		if len(argv) > 2 && argv[1] == "container" && len(f.calls) > 3 {
			return "", true, 125
		}
		return f.run(argv, stopRuntimeCommandTimeout)
	}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", runner, nil, false); rc != 1 || !strings.Contains(errb.String(), "could not be asked") {
		t.Fatalf("rc=%d stdout=%s stderr=%s", rc, &out, &errb)
	}
}

func TestStopAlreadyEndingKeeperStillRequiresAbsence(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			shrinkStopGone(t)
			cname := runtime.FromWorkspace("/ws")
			lockPath := filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
			if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			saved := finishStop
			called := false
			finishStop = func(_, _ io.Writer, _, _, _ string, _ int64, _ func(string, string, string), gotForce bool) int {
				called = true
				if gotForce != force {
					t.Error("force not forwarded")
				}
				return 0
			}
			t.Cleanup(func() { finishStop = saved })
			stuck := &stuckStopRuntime{present: true}
			runner := func(argv []string, _ time.Duration) (string, bool, int) {
				if argv[1] == "inspect" && slices.Contains(argv, "{{.State.Running}}") {
					return "false", true, 0
				}
				return stuck.run(argv, stopRuntimeCommandTimeout)
			}
			var out, errb bytes.Buffer
			if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "podman", runner, nil, force); rc != 1 || !called || !strings.Contains(errb.String(), "still there") {
				t.Fatalf("rc=%d called=%v stdout=%s stderr=%s", rc, called, &out, &errb)
			}
		})
	}
}

func TestStopAppleContainerRequiresTerminalAbsence(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(strconv.FormatBool(unknown), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			shrinkStopGone(t)
			saved := finishStop
			finishStop = func(_, _ io.Writer, _, _, _ string, _ int64, _ func(string, string, string), _ bool) int { return 0 }
			t.Cleanup(func() { finishStop = saved })
			cname := runtime.FromWorkspace("/ws")
			runner := func(argv []string, _ time.Duration) (string, bool, int) {
				switch argv[1] {
				case "ls":
					if len(argv) > 2 && unknown {
						return "", true, 1
					}
					return cname + " image linux arm64 running", true, 0
				case "stop", "rm":
					return "", true, 0
				}
				t.Fatalf("unexpected command %v", argv)
				return "", false, 1
			}
			var out, errb bytes.Buffer
			if rc := stopJail(&out, &errb, "/ws", runtime.FromWorkspace("/ws"), "container", runner, nil, false); rc != 1 || strings.Contains(out.String(), "Stopped ") {
				t.Fatalf("rc=%d stdout=%s stderr=%s", rc, &out, &errb)
			}
		})
	}
}

func TestStopRuntimeCommandTimeoutIsUnanswered(t *testing.T) {
	saved := stopRuntimeCommandTimeout
	stopRuntimeCommandTimeout = 20 * time.Millisecond
	t.Cleanup(func() { stopRuntimeCommandTimeout = saved })
	start := time.Now()
	_, ran, _ := realStopExec([]string{"sh", "-c", "exec sleep 30"}, stopRuntimeCommandTimeout)
	if ran {
		t.Fatal("a timed-out runtime must not be an answered absence")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("runtime command did not respect bound")
	}
}

// Polling must spend its remaining budget on existence, never a diagnostic inspect.
func TestWaitForJailContainerGoneBoundsExistenceProbe(t *testing.T) {
	oldWait, oldPoll, oldTimeout := stopGoneWait, stopGonePoll, stopRuntimeCommandTimeout
	t.Cleanup(func() {
		stopGoneWait, stopGonePoll, stopRuntimeCommandTimeout = oldWait, oldPoll, oldTimeout
	})
	stopGoneWait, stopGonePoll, stopRuntimeCommandTimeout = 40*time.Millisecond, time.Millisecond, 400*time.Millisecond
	dir := t.TempDir()
	marker := filepath.Join(dir, "inspect-called")
	script := "#!/bin/sh\nif [ \"$1\" = inspect ]; then\n  touch '" + marker + "'\n  exec sleep 1\nfi\nexec sleep 1\n"
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	gone, state := waitForJailContainerGone("podman", "test-jail", realStopExec)
	elapsed := time.Since(start)
	if gone || state != "" {
		t.Errorf("timed-out existence is unknown, got gone=%v state=%q", gone, state)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("absence polling ran nonessential diagnostic inspect")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("40ms absence budget took %v", elapsed)
	}
}

func TestWaitForJailContainerGoneCapsEachRuntimeProbe(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			oldWait, oldPoll, oldTimeout := stopGoneWait, stopGonePoll, stopRuntimeCommandTimeout
			t.Cleanup(func() {
				stopGoneWait, stopGonePoll, stopRuntimeCommandTimeout = oldWait, oldPoll, oldTimeout
			})
			stopGoneWait, stopGonePoll, stopRuntimeCommandTimeout = 40*time.Millisecond, time.Second, 20*time.Millisecond
			calls := 0
			runner := func(argv []string, timeout time.Duration) (string, bool, int) {
				calls++
				want := []string{"podman", "container", "exists", "test-jail"}
				if rt == "container" {
					want = []string{"container", "ls", "--all"}
				}
				if !slices.Equal(argv, want) {
					t.Fatalf("unexpected poll command: %v", argv)
				}
				if timeout <= 0 || timeout > stopRuntimeCommandTimeout {
					t.Fatalf("invalid command budget: %v", timeout)
				}
				return "test-jail image linux arm64 stopped", true, 0
			}
			start := time.Now()
			gone, state := waitForJailContainerGone(rt, "test-jail", runner)
			if gone || state == "" || calls != 1 {
				t.Fatalf("surviving container: gone=%v state=%q calls=%d", gone, state, calls)
			}
			if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
				t.Errorf("poll sleep exceeded remaining budget: %v", elapsed)
			}
		})
	}
}

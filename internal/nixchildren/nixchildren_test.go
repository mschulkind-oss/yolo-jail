package nixchildren

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// awaitRunning waits for s to hold n running children.
func awaitRunning(t *testing.T, s *Set, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if got := s.Running(); got == n {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("%d nix children tracked, want %d", got, n)
		}
	}
}

// trapsInterrupt is a stand-in nix that runs until interrupted (testsupport.UntilInterrupted) and
// says so on stderr, and that creates the file started once its trap is set (awaitStarted).
func trapsInterrupt(started string) string {
	return testsupport.UntilInterrupted("echo interrupted >&2", started)
}

// startMark is where a stand-in of t marks its start: a path in a temp dir of t's own.
func startMark(t *testing.T) string { return filepath.Join(t.TempDir(), "started") }

// awaitStarted waits for the stand-in that marks its start at started to have done so, which it
// does once its interrupt trap is set. Being tracked is not enough: the set tracks the stand-in as
// soon as it is started, and an interrupt that reaches the shell before its trap line ends it by
// the signal's default action, which runs no trap.
func awaitStarted(t *testing.T, started string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			return
		} else if time.Now().After(deadline) {
			t.Fatal("the stand-in nix never marked its start")
		}
	}
}

// TestStopKillsANixThatIgnoresTheInterrupt: past the grace, a nix still running is killed rather
// than left behind.
func TestStopKillsANixThatIgnoresTheInterrupt(t *testing.T) {
	s := Isolate(t)
	started := startMark(t)
	done := make(chan error, 1)
	go func() {
		done <- Run(exec.Command("sh", "-c", `trap '' INT; touch `+shquote.Quote(started)+`; while :; do sleep 0.05; done`))
	}()
	awaitRunning(t, s, 1)
	// Once the trap is set: an interrupt before it ends the shell at once, and the run passes
	// without reaching the kill this test is for.
	awaitStarted(t, started)
	s.stop(200*time.Millisecond, nil)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a nix that ignored its interrupt was still running ten seconds after the stop")
	}
}

// TestNoNixStartsAfterTheStop: a stop is not only for what runs when it is called. The launch's
// main goroutine goes on while the signal's teardown runs, and an eval the stop cut short falls
// through to a build, so that build must not start at all.
func TestNoNixStartsAfterTheStop(t *testing.T) {
	s := Isolate(t)
	s.stop(time.Second, nil)
	ran := filepath.Join(t.TempDir(), "ran")
	if err := Run(exec.Command("sh", "-c", "touch "+shquote.Quote(ran))); !errors.Is(err, ErrStopped) {
		t.Errorf("Run after the stop returned %v, want ErrStopped", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("Run started a nix after the stop")
	}
}

// TestAStoppedNixsCallerWaitsForTheExit: the goroutine whose nix a stop cut short runs the stop's
// hand-off before its Run returns, so it cannot go on to report a failed build while the teardown
// that stopped it exits. The stop itself does not wait on that hand-off.
func TestAStoppedNixsCallerWaitsForTheExit(t *testing.T) {
	s := Isolate(t)
	returned := make(chan error, 1)
	script := trapsInterrupt(startMark(t))
	go func() { returned <- Run(exec.Command("sh", "-c", script)) }()
	awaitRunning(t, s, 1)
	exited, handedOff := make(chan struct{}), make(chan struct{}, 1)
	stopped := make(chan struct{})
	go func() {
		s.stop(10*time.Second, func() { handedOff <- struct{}{}; <-exited })
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the stop waited on its own hand-off: the teardown would never reach its exit")
	}
	select {
	case <-handedOff:
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped nix's caller never ran the stop's hand-off")
	}
	select {
	case err := <-returned:
		t.Fatalf("Run returned (%v) before the exit it was handed off to, free to report a "+
			"failed build the signal caused", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(exited)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return once the exit it waited for came")
	}
}

// TestANixRefusedAfterTheStopIsNotHandedOff: a start the stop refuses returns at once. The stop is
// the teardown's first act, so the teardown's goroutine holds no nix to release, but it is the one
// goroutine that could still ask to start one, and waiting there for its own exit would never end.
func TestANixRefusedAfterTheStopIsNotHandedOff(t *testing.T) {
	s := Isolate(t)
	s.stop(time.Second, func() { select {} })
	done := make(chan error, 1)
	go func() { done <- Run(exec.Command("true")) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStopped) {
			t.Errorf("Run after the stop returned %v, want ErrStopped", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a start the stop refused waited on the hand-off")
	}
}

// catchSignal keeps sig from ending the test binary when no arm is installed to catch it, which is
// the defect's own shape: without this a red run would kill the whole package's run.
func catchSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	catch := make(chan os.Signal, 4)
	signal.Notify(catch, sig)
	t.Cleanup(func() { signal.Stop(catch) })
}

// awaitExit reads the status StopOnSignal's teardown ended the process with, or fails.
func awaitExit(t *testing.T, s *Set) int {
	t.Helper()
	select {
	case code := <-s.Exited():
		return code
	case <-time.After(15 * time.Second):
		t.Fatal("the signal did not end the process")
		return 0
	}
}

// TestASignalStopsTheNixAndEndsTheProcess: a signal sent to this process alone, under the arm,
// interrupts the nix it has running and then ends the process 128+N. Without the arm the signal's
// default action ended the process at once, and the nix ran on with no parent.
func TestASignalStopsTheNixAndEndsTheProcess(t *testing.T) {
	s := Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	disarm := StopOnSignal()
	defer disarm()
	var stderr strings.Builder
	returned := make(chan error, 1)
	started := startMark(t)
	go func() {
		cmd := exec.Command("sh", "-c", trapsInterrupt(started))
		cmd.Stderr = &stderr
		returned <- Run(cmd)
	}()
	awaitRunning(t, s, 1)
	awaitStarted(t, started)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := awaitExit(t, s); code != 128+int(syscall.SIGTERM) {
		t.Errorf("the process ended %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	select {
	case <-returned:
		if !strings.Contains(stderr.String(), "interrupted") {
			t.Errorf("the nix was not interrupted; its stderr: %q", stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped nix's Run never returned")
	}
}

// TestADisarmedArmLeavesTheSignalAlone: once disarmed the arm neither stops nix nor ends the
// process, so a command's steps after its nix keep the signal behavior they had.
func TestADisarmedArmLeavesTheSignalAlone(t *testing.T) {
	s := Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	disarm := StopOnSignal()
	disarm()
	disarm() // idempotent
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-s.Exited():
		t.Fatalf("a disarmed arm ended the process %d", code)
	case <-time.After(300 * time.Millisecond):
	}
	if err := Run(exec.Command("true")); err != nil {
		t.Errorf("a disarmed arm stopped the set: %v", err)
	}
}

// TestADisarmDuringTheTeardownWaitsForItsExit: a command that reaches its disarm while the arm's
// teardown is stopping its nix waits there for the teardown's exit, so it never races that exit to
// the end of the process with a status of its own.
func TestADisarmDuringTheTeardownWaitsForItsExit(t *testing.T) {
	s := Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	marker, started := filepath.Join(t.TempDir(), "interrupted"), startMark(t)
	disarm := StopOnSignal()
	go func() {
		_ = Run(exec.Command("sh", "-c", testsupport.UntilInterrupted("touch "+shquote.Quote(marker)+"; sleep 0.3", started)))
	}()
	awaitRunning(t, s, 1)
	awaitStarted(t, started)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the signal never reached the nix")
		}
	}
	disarmed := make(chan struct{})
	go func() { disarm(); close(disarmed) }()
	select {
	case <-disarmed:
		t.Fatal("the disarm returned while the teardown that owns the exit was still stopping nix")
	case code := <-s.Exited():
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the process ended %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the signal did not end the process")
	}
	select {
	case <-disarmed:
	case <-time.After(10 * time.Second):
		t.Fatal("the disarm never returned after the exit")
	}
}

// ignoredSignalHelperEnv names, in the helper TestStopOnSignalHelper re-execs this test binary as,
// the directory its stand-in nix marks an interrupt in. Unset, the helper does nothing.
const ignoredSignalHelperEnv = "YOLO_NIXCHILDREN_IGNORED_SIGNAL_HELPER"

// TestStopOnSignalHelper is the process TestAnIgnoredSignalStaysIgnoredUnderTheArm signals: it arms
// StopOnSignal, runs a tracked stand-in nix that ends by itself after about a second and a half,
// says "ready" once that nix is running and marks an interrupt, and says "finished normally" if the nix ran to its end.
// Run as a test of its own, it does nothing.
func TestStopOnSignalHelper(t *testing.T) {
	marks := os.Getenv(ignoredSignalHelperEnv)
	if marks == "" {
		return
	}
	disarm := StopOnSignal()
	started := filepath.Join(marks, "started")
	cmd := exec.Command("sh", "-c", testsupport.InterruptibleFor(1500*time.Millisecond, "touch "+shquote.Quote(filepath.Join(marks, "interrupted")), started))
	release, err := Start(cmd)
	if err != nil {
		fmt.Println("start:", err)
		os.Exit(2)
	}
	// Ready once the stand-in's trap is set, so an interrupt that reaches it is one it marks.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("the stand-in nix never started")
			os.Exit(2)
		}
	}
	fmt.Println("ready")
	waitErr := cmd.Wait()
	release()
	disarm()
	if waitErr != nil {
		fmt.Println("the nix ended:", waitErr)
		os.Exit(3)
	}
	fmt.Println("finished normally")
	os.Exit(0)
}

// TestAnIgnoredSignalStaysIgnoredUnderTheArm: a SIGHUP or SIGINT this process started with ignored
// is not armed, so it stays ignored. Notify on an ignored SIGHUP or SIGINT turns it back on, so
// under the arm `nohup yolo check` ended 129 on a hangup and stopped its image build, and a `yolo
// check &` from a script, which starts with SIGINT ignored, ended 130 on a Ctrl-C meant for the
// script; before the arm both went on.
func TestAnIgnoredSignalStaysIgnoredUnderTheArm(t *testing.T) {
	for _, tc := range []struct {
		trap string
		sig  syscall.Signal
	}{{"HUP", syscall.SIGHUP}, {"INT", syscall.SIGINT}} {
		t.Run("SIG"+tc.trap, func(t *testing.T) {
			marks := t.TempDir()
			cmd := exec.Command("sh", "-c", "trap '' "+tc.trap+`; exec "$0" -test.run='^TestStopOnSignalHelper$'`,
				os.Args[0])
			cmd.Env = append(os.Environ(), ignoredSignalHelperEnv+"="+marks)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			lines := bufio.NewScanner(stdout)
			ready := make(chan bool, 1)
			go func() { ready <- lines.Scan() && lines.Text() == "ready" }()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("the helper never said its nix was running")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("the helper never said its nix was running")
			}
			if err := cmd.Process.Signal(tc.sig); err != nil {
				t.Fatal(err)
			}
			var rest strings.Builder
			for lines.Scan() {
				rest.WriteString(lines.Text() + "\n")
			}
			waitErr := cmd.Wait()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("a SIG%s the process ignored ended it %d (%v), want it to go on; it said %q",
					tc.trap, code, waitErr, rest.String())
			}
			if !strings.Contains(rest.String(), "finished normally") {
				t.Errorf("the helper's nix did not run to its end; it said %q", rest.String())
			}
			if _, err := os.Stat(filepath.Join(marks, "interrupted")); err == nil {
				t.Errorf("a SIG%s the process ignored stopped its nix", tc.trap)
			}
		})
	}
}

// TestStartIfNixTracksANixAlone: an exec seam that runs nix among other programs tracks `nix` and
// its `nix-*` tools, by name or by path, and starts anything else untracked.
func TestStartIfNixTracksANixAlone(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"nix", "nix-store"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		argv0   string
		tracked bool
	}{
		{"nix", true},
		{"nix-store", true},
		{filepath.Join(bin, "nix"), true},
		{"sleep", false},
	} {
		t.Run(tc.argv0, func(t *testing.T) {
			s := Isolate(t)
			cmd := exec.Command(tc.argv0, "30")
			release, err := StartIfNix(cmd)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Running() == 1; got != tc.tracked {
				t.Errorf("%s tracked: %v, want %v", tc.argv0, got, tc.tracked)
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			release()
			if s.Running() != 0 {
				t.Errorf("%s still tracked after its release", tc.argv0)
			}
		})
	}
}

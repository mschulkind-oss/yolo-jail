package hostservice

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const boundedCommandHelperEnv = "YOLO_TEST_BOUNDED_COMMAND_HELPER"

func TestBoundedCommandCapturesAndClassifies(t *testing.T) {
	ok := RunBoundedCommand([]string{"/bin/sh", "-c", "printf 'ok'; printf 'note' >&2"}, time.Second)
	if ok.Outcome != CommandAccepted || ok.Stdout != "ok" || ok.Stderr != "note" {
		t.Fatalf("success result = %+v", ok)
	}
	refused := RunBoundedCommand([]string{"/bin/sh", "-c", "printf 'no'; exit 7"}, time.Second)
	if refused.Outcome != CommandRefused || refused.ExitCode != 7 || refused.Stdout != "no" {
		t.Fatalf("refusal result = %+v", refused)
	}
	missing := RunBoundedCommand([]string{"/not/a/real/validator"}, time.Second)
	if missing.Outcome != CommandStartFailed {
		t.Fatalf("missing executable result = %+v", missing)
	}
}

func TestBoundedCommandTimesOutAndCapsCombinedOutput(t *testing.T) {
	start := time.Now()
	timed := RunBoundedCommand([]string{"/bin/sh", "-c", "sleep 5"}, 100*time.Millisecond)
	if timed.Outcome != CommandTimedOut || time.Since(start) > time.Second {
		t.Fatalf("timeout result = %+v after %s", timed, time.Since(start))
	}
	capped := RunBoundedCommand([]string{"/bin/sh", "-c", "head -c 5000 /dev/zero | tr '\\000' x; head -c 5000 /dev/zero | tr '\\000' y >&2"}, time.Second)
	if len(capped.Stdout)+len(capped.Stderr) > SettingsCheckOutputMax {
		t.Fatalf("captured %d bytes, limit is %d", len(capped.Stdout)+len(capped.Stderr), SettingsCheckOutputMax)
	}
}

// escapedDescendantHold is how long the escaped leaf keeps both output descriptors at most; it
// exits sooner, at its first write after the bounded command closed them.
const escapedDescendantHold = 30 * time.Second

// This test helper is invoked as a child binary by the escaped-descendant test. The
// intermediate command exits, while a setsid descendant retains both output descriptors.
//
// The intermediate exits only once the leaf says, on a pipe of their own, that it has written
// its marker: a fixed sleep raced the leaf's start-up (a fresh test binary), and under load the
// marker arrived after the intermediate's 6000 bytes had filled the capture, or not at all.
func TestBoundedCommandEscapedDescendantHelper(t *testing.T) {
	switch os.Getenv(boundedCommandHelperEnv) {
	case "spawn":
		ready, readyW, err := os.Pipe()
		if err != nil {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestBoundedCommandEscapedDescendantHelper$")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.ExtraFiles = []*os.File{readyW} // the leaf's fd 3
		child.Env = append(os.Environ(), boundedCommandHelperEnv+"=leaf")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = readyW.Close()
		_ = child.Process.Release()
		if n, _ := ready.Read(make([]byte, 1)); n != 1 {
			os.Exit(3) // the leaf died before its marker
		}
		_, _ = os.Stdout.Write([]byte(strings.Repeat("o", 3000)))
		_, _ = os.Stderr.Write([]byte(strings.Repeat("e", 3000)))
		os.Exit(0)
	case "leaf":
		_, _ = os.Stdout.WriteString("escaped-descendant-started")
		ready := os.NewFile(3, "ready")
		_, _ = ready.Write([]byte{1})
		_ = ready.Close()
		// Hold fd 1 and 2 until the bounded command has closed its ends (a write then fails, or
		// SIGPIPE ends this process), so no leaf outlives the test by the whole hold.
		for end := time.Now().Add(escapedDescendantHold); time.Now().Before(end); {
			time.Sleep(100 * time.Millisecond)
			if _, err := os.Stdout.WriteString("."); err != nil {
				os.Exit(0)
			}
		}
		os.Exit(0)
	}
}

func TestBoundedCommandClosesInheritedOutputWithoutWaitingForEscapedDescendant(t *testing.T) {
	argv := []string{os.Args[0], "-test.run=^TestBoundedCommandEscapedDescendantHelper$"}
	env := append(os.Environ(), boundedCommandHelperEnv+"=spawn")
	start := time.Now()
	cmd := RunBoundedCommandWithEnv(argv, env, 10*time.Second)
	if cmd.Outcome != CommandAccepted || !strings.Contains(cmd.Stdout, "escaped-descendant-started") {
		t.Fatalf("intermediate helper result = %+v", cmd)
	}
	if total := len(cmd.Stdout) + len(cmd.Stderr); total > SettingsCheckOutputMax {
		t.Fatalf("inherited writers exceeded the combined capture limit: got %d, max %d", total, SettingsCheckOutputMax)
	}
	if elapsed := time.Since(start); elapsed > escapedDescendantHold/2 {
		t.Fatalf("validator cleanup waited %s for an escaped descendant holding both output descriptors", elapsed)
	}
}

func TestSafeCommandTextStripsControlAndMarkupAndCaps(t *testing.T) {
	got := SafeCommandText("  [red]refused\n\x1b[2J  " + strings.Repeat("x", 700))
	if strings.ContainsAny(got, "[]\n\x1b") || len([]rune(got)) > startupReasonTextMax {
		t.Fatalf("unsafe or unbounded command diagnostic %q (%d runes)", got, len([]rune(got)))
	}
}

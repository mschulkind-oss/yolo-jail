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

// This test helper is invoked as a child binary by the escaped-descendant test. The
// intermediate command exits, while a setsid descendant retains both output descriptors.
func TestBoundedCommandEscapedDescendantHelper(t *testing.T) {
	switch os.Getenv(boundedCommandHelperEnv) {
	case "spawn":
		child := exec.Command(os.Args[0], "-test.run=^TestBoundedCommandEscapedDescendantHelper$")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.Env = append(os.Environ(), boundedCommandHelperEnv+"=leaf")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = child.Process.Release()
		time.Sleep(50 * time.Millisecond)
		_, _ = os.Stdout.Write([]byte(strings.Repeat("o", 3000)))
		_, _ = os.Stderr.Write([]byte(strings.Repeat("e", 3000)))
		os.Exit(0)
	case "leaf":
		_, _ = os.Stdout.WriteString("escaped-descendant-started")
		time.Sleep(3 * time.Second)
		_, _ = os.Stdout.WriteString("escaped stdout")
		_, _ = os.Stderr.WriteString("escaped stderr")
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
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("validator cleanup waited %s for an escaped descendant holding both output descriptors", elapsed)
	}
}

func TestSafeCommandTextStripsControlAndMarkupAndCaps(t *testing.T) {
	got := SafeCommandText("  [red]refused\n\x1b[2J  " + strings.Repeat("x", 700))
	if strings.ContainsAny(got, "[]\n\x1b") || len([]rune(got)) > startupReasonTextMax {
		t.Fatalf("unsafe or unbounded command diagnostic %q (%d runes)", got, len([]rune(got)))
	}
}

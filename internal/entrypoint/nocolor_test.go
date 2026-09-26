package entrypoint

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// nocolor_test.go pins the jail-side half of NO_COLOR (https://no-color.org): the two places
// the entrypoint adds color — the exec-into "⚡ Executing" line and the interactive shell's
// prompt and `ls` alias — read the variable the launcher carries into the jail.

// captureStderr redirects os.Stderr around body and returns what was written.
func captureStderr(t *testing.T, body func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	body()
	os.Stderr = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestExecBashHonorsNoColor drives execBash — the boot's last step — to its exec (stubbed:
// a real one would replace the test binary) and reads the hand-over line it printed. With
// NO_COLOR unset it is cyan (the control); with NO_COLOR=1 in the jail's environment it
// carries no escape and the same words.
//
// MUTATION: pass true where execBash passes !tty.NoColor(os.Getenv) and the NO_COLOR case
// fails.
func TestExecBashHonorsNoColor(t *testing.T) {
	saved := sysExec
	var execd []string
	sysExec = func(_ string, argv []string, _ []string) error { execd = argv; return nil }
	t.Cleanup(func() { sysExec = saved })
	t.Setenv("PATH", os.Getenv("PATH")) // execBash sets PATH; restore it afterwards

	for _, tc := range []struct {
		noColor  string
		wantANSI bool
	}{{"", true}, {"1", false}} {
		t.Setenv("NO_COLOR", tc.noColor)
		e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
		execd = nil
		var err error
		got := captureStderr(t, func() { err = execBash(e, "echo attached") })
		if err != nil {
			t.Skipf("execBash could not reach its exec here (%v); it needs bash on the boot PATH", err)
		}
		if len(execd) == 0 {
			t.Fatal("execBash returned without exec'ing")
		}
		if !strings.Contains(got, "⚡ Executing: echo attached") {
			t.Fatalf("NO_COLOR=%q: the hand-over line is missing:\n%q", tc.noColor, got)
		}
		if hasANSI := strings.Contains(got, "\x1b["); hasANSI != tc.wantANSI {
			t.Errorf("NO_COLOR=%q: the hand-over line carries ANSI = %v, want %v:\n%q",
				tc.noColor, hasANSI, tc.wantANSI, got)
		}
	}
}

// TestTheJailShellHonorsNoColor sources the .bashrc the boot generates (GenerateBashrc, the
// boot's own step) in bash and reads what it defined: under NO_COLOR the prompt carries no
// escape and `ls` is not aliased to --color; without it both are as they always were.
func TestTheJailShellHonorsNoColor(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	for _, tc := range []struct {
		noColor   string
		wantColor bool
	}{{"", true}, {"1", false}} {
		home := t.TempDir()
		e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": t.TempDir()})
		if err := GenerateBashrc(e); err != nil {
			t.Fatalf("GenerateBashrc: %v", err)
		}
		cmd := exec.Command(bash, "--norc", "--noprofile", "-c",
			`. "$1" >/dev/null 2>&1; printf 'PS1=%s\n' "$PS1"; alias ls 2>/dev/null || echo 'no ls alias'`,
			"bash", e.BashrcPath())
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "NO_COLOR=" + tc.noColor}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sourcing the generated .bashrc failed: %v\n%s", err, out)
		}
		got := string(out)
		if !strings.Contains(got, "YOLO-JAIL") {
			t.Fatalf("NO_COLOR=%q: the prompt was not defined:\n%s", tc.noColor, got)
		}
		if hasEsc := strings.Contains(got, `\033[`); hasEsc != tc.wantColor {
			t.Errorf("NO_COLOR=%q: the prompt carries an escape = %v, want %v:\n%s",
				tc.noColor, hasEsc, tc.wantColor, got)
		}
		if hasAlias := strings.Contains(got, "--color=auto"); hasAlias != tc.wantColor {
			t.Errorf("NO_COLOR=%q: `ls` is aliased to --color = %v, want %v:\n%s",
				tc.noColor, hasAlias, tc.wantColor, got)
		}
	}
}

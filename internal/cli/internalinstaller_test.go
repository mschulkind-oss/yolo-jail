package cli

// internalinstaller_test.go pins the two hidden verbs a vendor installer runs through
// (docs/design/provisioner-sets.md): `yolo internal no-terminal` (PS-D1), which the jail's native
// launcher runs its installer under, and `yolo internal installer-check` (PS-D4), the check step
// of a `via: installer` remedy at the host. Both are driven through runInternal, so the dispatch
// is under test as well as the body.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/installerbody"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// no-terminal is dispatched to internal/notty and exits with the command's status; the
// command's stdin is /dev/null whatever this process was handed (the read fails, so the probe
// exits 4); a death by signal is 128+N; misuse is 2. That the child also has no controlling
// terminal is internal/notty's pty test.
func TestNoTerminalRunsTheCommandWithNoStdin(t *testing.T) {
	if rc := runInternal([]string{"no-terminal", "--", "sh", "-c", "exit 3"}); rc != 3 {
		t.Errorf("rc = %d, want the command's 3", rc)
	}
	if rc := runInternal([]string{"no-terminal", "--", "sh", "-c",
		"if read -r x; then exit 9; fi; exit 4"}); rc != 4 {
		t.Errorf("rc = %d, want 4: the command read a line from a stdin that must be /dev/null", rc)
	}
	if rc := runInternal([]string{"no-terminal", "--", "sh", "-c", "kill -TERM $$"}); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("rc = %d, want 128+SIGTERM for a command killed by it", rc)
	}
	for _, args := range [][]string{{"no-terminal"}, {"no-terminal", "--"}, {"no-terminal", "sh"}} {
		if rc := runInternal(args); rc != 2 {
			t.Errorf("%v: rc = %d, want 2 (misuse)", args, rc)
		}
	}
}

// installer-check is silent with 0 for a script, and for any refused kind prints the launcher's
// refusal naming the URL with exit 1, so the remedy's `&&` runs nothing after it. An unreadable
// file or a wrong argument count is 2.
func TestInstallerCheckRefusesWhatTheLauncherRefuses(t *testing.T) {
	dir := t.TempDir()
	const url = "https://example.invalid/install.sh"
	for _, f := range installerbody.Fixtures() {
		t.Run(f.Name, func(t *testing.T) {
			path := filepath.Join(dir, "body")
			if err := os.WriteFile(path, []byte(f.Body), 0o644); err != nil {
				t.Fatal(err)
			}
			var errw bytes.Buffer
			rc := runInstallerCheck([]string{url, path}, &errw)
			if f.Want == installerbody.Script {
				if rc != 0 || errw.Len() != 0 {
					t.Errorf("a script must pass silently; rc=%d: %s", rc, errw.String())
				}
				return
			}
			if rc != 1 {
				t.Errorf("rc = %d, want 1 for a %s body", rc, f.Want)
			}
			for _, want := range []string{"not a shell script", url, installerbody.Why(f.Want)} {
				if !strings.Contains(errw.String(), want) {
					t.Errorf("the refusal lacks %q:\n%s", want, errw.String())
				}
			}
		})
	}
	var errw bytes.Buffer
	if rc := runInstallerCheck([]string{url, filepath.Join(dir, "absent")}, &errw); rc != 2 {
		t.Errorf("an unreadable file: rc = %d, want 2", rc)
	}
	for _, args := range [][]string{{packdecl.InstallerCheckVerb}, {packdecl.InstallerCheckVerb, url}} {
		if rc := runInternal(args); rc != 2 {
			t.Errorf("%v: rc = %d, want 2 (misuse)", args, rc)
		}
	}
	// And the dispatch reaches it: a web page is refused through runInternal too.
	page := filepath.Join(dir, "page")
	if err := os.WriteFile(page, []byte("<!doctype html>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := runInternal([]string{packdecl.InstallerCheckVerb, url, page}); rc != 1 {
		t.Errorf("runInternal did not reach the check: rc = %d, want 1", rc)
	}
}

// A CTRL-C AT THE INSTALLER STILL STOPS THE LAUNCHER THAT RAN IT (PS-D7). The installer runs in
// a session of its own, so the terminal's interrupt reaches the launcher and this verb, and the
// verb forwards it. bash, waiting on a command when its interrupt arrives, abandons its script
// only when the command DIED of SIGINT, and goes on to the next line when the command exited, even
// with 130. So a verb that caught the signal and exited 130 turned a Ctrl-C into "carry on": the
// launcher touched its stamp, wrote an update receipt over the half-run installer and exec'd the
// agent. The launcher here is that shape reduced: a bash script running the verb, then a line that
// must not run. The test binary stands in for `yolo` (testAsYoloArg), so the dispatch is real.
func TestNoTerminalDiesOfTheInterruptItForwarded(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	script := `"$YOLO" ` + testAsYoloArg + ` internal no-terminal -- sh -c 'echo > "$STARTED"; exec sleep 20' || true
echo CONTINUED`
	launcher := exec.Command(bash, "-c", script)
	launcher.Env = append(os.Environ(), "YOLO="+exe, "STARTED="+started)
	var out bytes.Buffer
	launcher.Stdout, launcher.Stderr = &out, &out
	// Its own process group, standing in for the terminal's foreground group a Ctrl-C reaches.
	launcher.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-launcher.Process.Pid, syscall.SIGKILL)
			_ = launcher.Wait()
			t.Fatalf("the installer never started:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	begun := time.Now()
	if err := syscall.Kill(-launcher.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err = launcher.Wait()
	if elapsed := time.Since(begun); elapsed > 10*time.Second {
		t.Errorf("the installer outlived the interrupt by %s; it was not forwarded", elapsed)
	}
	if strings.Contains(out.String(), "CONTINUED") {
		t.Errorf("the launcher went on after a Ctrl-C at its installer:\n%s", out.String())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("the launcher exited cleanly after a Ctrl-C (%v):\n%s", err, out.String())
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("the launcher must die of the interrupt, as it did running the installer itself; "+
			"got %v:\n%s", err, out.String())
	}
}

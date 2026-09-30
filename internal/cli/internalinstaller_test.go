package cli

// internalinstaller_test.go pins the two hidden verbs a vendor installer runs through
// (docs/design/provisioner-sets.md): `yolo internal no-terminal` (PS-D1), which the jail's native
// launcher runs its installer under, and `yolo internal installer-check` (PS-D4), the check step
// of a `via: installer` remedy at the host. Both are driven through runInternal, so the dispatch
// is under test as well as the body.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

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

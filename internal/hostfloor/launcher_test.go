package hostfloor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// launcher_test.go pins what bin/<bin> may contain: a shebang, comments, and ONE line that runs —
// the exec of the recorded argv. The record's descriptive fields are text a vendor chose (npm's
// package.json version, a directory name a capture's installer made), so none of them may reach
// the file as anything but a comment.

// runnableLines is every line of a shell script that sh would run: not blank, not a comment.
func runnableLines(script string) []string {
	var out []string
	for _, l := range strings.Split(script, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			out = append(out, l)
		}
	}
	return out
}

// TestTheLauncherScriptRunsOnlyItsExecLine: a Version and a Pack carrying line breaks and shell
// syntax stay inside the comment, and running the script runs the program and nothing else.
func TestTheLauncherScriptRunsOnlyItsExecLine(t *testing.T) {
	testsupport.UnsetLCAll(t) // the child shell's output is compared exactly
	dir := resolvedTemp(t)
	prog := filepath.Join(dir, "prog")
	must(t, os.WriteFile(prog, []byte("#!/bin/sh\necho ran \"$@\"\n"), 0o755))
	rec := &Record{Bin: "prog", Version: "1.0.0\ntouch pwned-version\r\x00", Pack: "p\n$(touch pwned-pack)",
		Exec: []string{prog}}
	script := launcherScript(rec)
	if got := runnableLines(script); len(got) != 1 || !strings.HasPrefix(got[0], "exec ") {
		t.Fatalf("the launcher runs %q, want only its exec line:\n%s", got, script)
	}
	launcher := filepath.Join(dir, "launcher")
	must(t, os.WriteFile(launcher, []byte(script), 0o700))
	cmd := exec.Command(launcher, "x")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "ran x\n" {
		t.Fatalf("running the launcher: %q %v", out, err)
	}
	for _, f := range []string{"pwned-version", "pwned-pack"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("the launcher ran text from a record field (%s exists)", f)
		}
	}
}

// TestAVersionACaptureChoseCannotAddALineToTheLauncher drives the same through install, the call
// site: an installer capture's versions directory names the version, and a directory name may
// hold a newline. The launcher Ensure writes still has exactly one runnable line.
func TestAVersionACaptureChoseCannotAddALineToTheLauncher(t *testing.T) {
	w := newLinuxWorld(t)
	cs := newCaptureStore(t)
	cs.add("claude", "2.1.267\ntouch pwned", true)
	w.floor.ResolveCapture = cs.resolve
	st, _, err := w.floor.Ensure(context.Background(), installerProgram("claude", "claude"))
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if !strings.Contains(st.Record.Version, "\n") {
		t.Fatalf("the fixture's version %q lost its newline before the launcher was written", st.Record.Version)
	}
	script, err := os.ReadFile(st.Launcher)
	must(t, err)
	if got := runnableLines(string(script)); len(got) != 1 || !strings.HasPrefix(got[0], "exec ") {
		t.Fatalf("bin/claude runs %q, want only its exec line:\n%s", got, script)
	}
}

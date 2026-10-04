package run

// hostlaunchlog_test.go pins how host-launch.log (HostLaunchLog) tells one launch's lines from
// another's. A host launch yolo stays the parent of (a launch-owned service, a managed Codex login)
// ends hours after it began, and host wrappers make concurrent host launches ordinary, so the file
// interleaves launches: every line a launch writes names it by its process id, and a trailer is
// matched to its header by that id rather than by where it sits.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHostLaunchPID makes the launches this test opens report pid, as separate processes would.
func withHostLaunchPID(t *testing.T, pid int) {
	t.Helper()
	orig := hostLaunchPID
	hostLaunchPID = func() int { return pid }
	t.Cleanup(func() { hostLaunchPID = orig })
}

// A RESIDENT LAUNCH'S TRAILER NAMES IT: launch A (claude, resident) opens, launch B (codex) opens
// after it and hands over by exec, then A's agent exits and A writes its trailer below B's block.
// The trailer must say it is A's, and so must A's own late lines, or the file reads as B's exec
// returning A's exit code.
func TestAResidentHostLaunchsTrailerNamesItsOwnLaunch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	projA, projB := filepath.Join(home, "code", "proja"), filepath.Join(home, "code", "projb")

	withHostLaunchPID(t, 1111)
	a := OpenHostLaunchLog(projA, "claude")
	if a == nil {
		t.Fatal("the log did not open")
	}
	aw := a.Writer(&strings.Builder{})
	if _, err := aw.Write([]byte("yolo host: starting claude\n")); err != nil {
		t.Fatal(err)
	}

	withHostLaunchPID(t, 2222)
	b := OpenHostLaunchLog(projB, "codex")
	bw := b.Writer(&strings.Builder{})
	if _, err := bw.Write([]byte("yolo host: starting codex\n")); err != nil {
		t.Fatal(err)
	}
	b.HandedOver("exec")

	// A's agent exits: a line of A's teardown, then its trailer.
	if _, err := aw.Write([]byte("yolo host: stopped the service\n")); err != nil {
		t.Fatal(err)
	}
	a.Done(0)
	b.Done(126) // the exec returned (failed): B's own trailer, after A's

	data, err := os.ReadFile(HostLaunchLogPath())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	want := strings.Join([]string{
		"=== yolo host launch * (pid 1111) ===",
		"  yolo=*  jail=*  program=claude",
		"(pid 1111) yolo host: starting claude",
		"=== yolo host launch * (pid 2222) ===",
		"  yolo=*  jail=*  program=codex",
		"(pid 2222) yolo host: starting codex",
		"=== handed over (pid 2222): exec ===",
		"(pid 1111) yolo host: stopped the service",
		"=== launch done (pid 1111), rc=0 ===",
		"=== launch done (pid 2222), rc=126 ===",
	}, "\n") + "\n"
	if !globLines(want, got) {
		t.Errorf("host-launch.log =\n%s\nwant (each * any text)\n%s", got, want)
	}
	for _, never := range []string{projA, projB} {
		if strings.Contains(got, never) {
			t.Errorf("the log names the directory %s:\n%s", never, got)
		}
	}
}

// globLines reports whether got matches want line by line, a `*` in a want line matching any text.
func globLines(want, got string) bool {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	if len(w) != len(g) {
		return false
	}
	for i := range w {
		if ok, _ := filepath.Match(strings.ReplaceAll(w[i], "/", "?"), strings.ReplaceAll(g[i], "/", "?")); !ok {
			return false
		}
	}
	return true
}

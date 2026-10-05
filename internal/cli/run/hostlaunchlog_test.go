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

// THE REDACTOR NAMES A PROJECT, AND ONLY A PROJECT, `<cwd>` (podman-reboot-readiness.md PR-D26):
// the directory the command ran in, or a path under it, in either spelling a line gives it, while a
// sibling sharing its prefix is left whole. Run from the root, the home or a directory above the
// home, a launch's directory names no project, and rewriting it would hide every path under the
// home a line names, so the redactor leaves such a line alone. A program typed as a path is named
// by its base name in each spelling: as typed, absolute, and under the home.
func TestTheHostLaunchRedactorNamesOnlyAProjectDirectory(t *testing.T) {
	const home = "/h/u"
	for _, tc := range []struct {
		name, ws, program, line, want string
	}{
		{"a path under the project", "/h/u/proj", "tool", "searched /h/u/proj/bin", "searched <cwd>/bin"},
		{"the project under ~", "/h/u/proj", "tool", "in ~/proj, ok", "in <cwd>, ok"},
		{"the project itself, at the end", "/h/u/proj", "tool", "ran in /h/u/proj", "ran in <cwd>"},
		{"a sibling sharing its prefix", "/h/u/proj", "tool", "see /h/u/project2/x", "see /h/u/project2/x"},
		{"run from the home", home, "tool", "searched /h/u/proj/bin and ~/x", "searched /h/u/proj/bin and ~/x"},
		{"run from the root", "/", "tool", "ran in / (searched /usr/bin)", "ran in / (searched /usr/bin)"},
		{"run from above the home", "/h", "tool", "searched /h/u/bin", "searched /h/u/bin"},
		{"a program typed as a path, absolute", "/h/u/proj", "./sp/tool", "starting /h/u/proj/sp/tool", "starting tool"},
		{"a program typed as a path, under ~", "/h/u/proj", "./sp/tool", "starting ~/proj/sp/tool", "starting tool"},
		{"a program typed as a path, as typed", "/h/u/proj", "./sp/tool", "exec ./sp/tool: denied", "exec tool: denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostLaunchRedactor(tc.ws, tc.program, home)(tc.line); got != tc.want {
				t.Errorf("hostLaunchRedactor(%q, %q)(%q) = %q, want %q", tc.ws, tc.program, tc.line, got, tc.want)
			}
		})
	}
}

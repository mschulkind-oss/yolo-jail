//go:build darwin

package hostprocesses

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The BSD arm against the REAL BSD ps, on every push: ci.yml's check-macos runs
// `go test -short ./...` on macos-latest, and nothing here starts a container.
//
// blackbox_test.go pins the argv and the Go-side selection against a fake ps on any
// platform. What only a Mac can say is whether BSD ps ANSWERS those queries the way
// the parser expects, so every test here leaves hostOS at its production default
// (TestBSDPSRealDefaultIsBSD checks that it is darwin) and runs the system ps.

// startSleep starts /bin/sleep, a process whose names are known in advance, and
// returns its pid.
func startSleep(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return strconv.Itoa(cmd.Process.Pid)
}

// realPS runs the system ps and returns its trimmed stdout.
func realPS(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("ps", args...).Output()
	if err != nil {
		t.Fatalf("ps %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// rowFor returns the first line of out whose first field is pid.
func rowFor(out, pid string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == pid {
			return line, true
		}
	}
	return "", false
}

func TestBSDPSRealDefaultIsBSD(t *testing.T) {
	if dialectFor(hostOS) != bsdPS {
		t.Fatalf("hostOS = %q on darwin: the daemon would run GNU queries against BSD ps", hostOS)
	}
}

// TestBSDPSRealCommIsAPathAndUcommIsTheName confirms the fact the package comment's
// matching decision rests on, read from macOS's ps source and measured here: BSD
// `comm` prints argv[0], a path for a process started by one, and `ucomm` the short
// accounting name, so the allowlist matches `ucomm`. If this fails, only the package
// comment's reason needs rewording: ucomm is darwin's analog of /proc/<pid>/comm
// either way.
func TestBSDPSRealCommIsAPathAndUcommIsTheName(t *testing.T) {
	pid := startSleep(t)
	if ucomm := realPS(t, "-o", "ucomm=", "-p", pid); ucomm != "sleep" {
		t.Errorf("ucomm of /bin/sleep = %q, want %q", ucomm, "sleep")
	}
	if comm := realPS(t, "-o", "comm=", "-p", pid); comm == "sleep" || path.Base(comm) != "sleep" {
		t.Errorf("comm of /bin/sleep = %q, want a path ending in /sleep", comm)
	}
}

func TestBSDPSRealListMode(t *testing.T) {
	pid := startSleep(t)
	ep, stop := startDaemon(t, settings(t, `{"visible":["sleep"],"fields":["pid","comm","args"]}`), "")
	defer stop()
	out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 0 {
		t.Fatalf("list rc=%d, want 0 (stderr=%q)", rc, errOut)
	}
	row, ok := rowFor(string(out), pid)
	if !ok {
		t.Fatalf("list output has no row for the sleep (pid %s):\n%s", pid, out)
	}
	// `comm` is displayed as ucomm: the short name, not /bin/sleep.
	if f := strings.Fields(row); len(f) < 2 || f[1] != "sleep" {
		t.Errorf("row %q: second column is not the ucomm %q", row, "sleep")
	}
	if header, _, _ := strings.Cut(string(out), "\n"); !strings.Contains(header, "UCOMM") {
		t.Errorf("header %q: want a UCOMM column", header)
	}
}

func TestBSDPSRealListNoMatchIsHeaderOnly(t *testing.T) {
	ep, stop := startDaemon(t, settings(t, `{"visible":["yj-no-such-comm"],"fields":["pid","comm"]}`), "")
	defer stop()
	out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if rc != 1 || len(lines) != 1 || !strings.Contains(lines[0], "PID") {
		t.Errorf("no match = rc %d out %q (stderr=%q), want rc 1 and the header alone", rc, out, errOut)
	}
}

func TestBSDPSRealPidMode(t *testing.T) {
	pid := startSleep(t)
	ep, stop := startDaemon(t, settings(t, `{"visible":["sleep"],"fields":["pid","comm"]}`), "")
	defer stop()

	n, _ := strconv.Atoi(pid)
	out, errOut, rc := query(t, ep, map[string]any{"mode": "pid", "pid": n})
	if _, ok := rowFor(string(out), pid); rc != 0 || !ok {
		t.Errorf("pid mode on the sleep = rc %d out %q (stderr=%q), want rc 0 and its row", rc, out, errOut)
	}
	_, errOut, rc = query(t, ep, map[string]any{"mode": "pid", "pid": os.Getpid()})
	if rc != 2 || !strings.Contains(string(errOut), "which is not allowlisted") {
		t.Errorf("pid mode on the test binary = rc %d stderr %q, want rc 2, not allowlisted", rc, errOut)
	}

	// Exactly "not found", which pins the fact commOf reads a gone pid by: BSD ps asked
	// about one prints nothing and SAYS nothing. A ps that said something would be
	// reported as a ps that refused the question, and fail here naming that.
	gone := exec.Command("/usr/bin/true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	_, errOut, rc = query(t, ep, map[string]any{"mode": "pid", "pid": gone.Process.Pid})
	if want := "pid " + strconv.Itoa(gone.Process.Pid) + " not found\n"; rc != 1 || string(errOut) != want {
		t.Errorf("pid mode on an exited pid = rc %d stderr %q, want rc 1 and %q", rc, errOut, want)
	}
}

// TestBSDPSRealTreeMode allowlists THIS test binary, whose child the sleep is, so the
// sleep is kept as a descendant and drawn one level down.
func TestBSDPSRealTreeMode(t *testing.T) {
	pid := startSleep(t)
	self := strconv.Itoa(os.Getpid())
	name := realPS(t, "-o", "ucomm=", "-p", self)
	ep, stop := startDaemon(t, settings(t, `{"visible":["`+name+`"]}`), "")
	defer stop()

	out, errOut, rc := query(t, ep, map[string]any{"mode": "tree"})
	if rc != 0 {
		t.Fatalf("tree rc=%d, want 0 (stderr=%q)", rc, errOut)
	}
	if _, ok := rowFor(string(out), self); !ok {
		t.Errorf("tree has no row for the allowlisted test binary %s (%q):\n%s", self, name, out)
	}
	row, ok := rowFor(string(out), pid)
	if !ok {
		t.Fatalf("tree has no row for the sleep %s, a child of an allowlisted process:\n%s", pid, out)
	}
	if f := strings.Fields(row); len(f) < 2 || f[1] != self || !strings.Contains(row, ` \_ sleep`) {
		t.Errorf("sleep row %q: want ppid %s and a ` \\_ sleep` name, drawn under its parent", row, self)
	}
	if !strings.Contains(row, "/bin/sleep 60") {
		t.Errorf("sleep row %q: want its args from the second query", row)
	}
}

func TestBSDPSRealSelfCheckPasses(t *testing.T) {
	var out strings.Builder
	if rc := selfCheck("", dialectFor(hostOS), &out); rc != 0 || !strings.Contains(out.String(), "answers the BSD queries") {
		t.Errorf("self-check on this Mac = %d\n%s\nwant 0 and the BSD OK line", rc, out.String())
	}
}

// TestBSDPSRealListMatchesALongNameAsGNUDoes: this test binary's file name,
// hostprocesses.test, is longer than the 16 bytes darwin keeps as ucomm, so the kernel
// cut it. List mode must still find it by the FULL name, as GNU -C does on Linux.
func TestBSDPSRealListMatchesALongNameAsGNUDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Base(exe)
	if len(full) <= 16 {
		t.Skipf("test binary %q is not longer than ucomm's 16 bytes", full)
	}
	self := strconv.Itoa(os.Getpid())
	// The package comment's MAXCOMLEN claim. If this fails, only that wording is wrong.
	if ucomm := realPS(t, "-o", "ucomm=", "-p", self); len(ucomm) > 16 {
		t.Errorf("ucomm of %q = %q, longer than the 16 bytes the package comment says darwin keeps", full, ucomm)
	}
	ep, stop := startDaemon(t, settings(t, `{"visible":["`+full+`"],"fields":["pid","comm"]}`), "")
	defer stop()
	out, errOut, rc := query(t, ep, map[string]any{"mode": "list"})
	if _, ok := rowFor(string(out), self); rc != 0 || !ok {
		t.Errorf("list by the full name %q = rc %d out %q (stderr=%q), want rc 0 and this process's row",
			full, rc, out, errOut)
	}
}

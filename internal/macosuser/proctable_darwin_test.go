//go:build darwin

package macosuser

import (
	"os"
	"os/exec"
	"testing"
)

// The guard's darwin read starts no process — ps is setuid root on macOS, and Seatbelt
// refuses a setuid exec in any sandbox, which left the guard blind (macos-user CI run
// 37940733418) — and sees this test, its parent, a child it starts, and their sizes.
func TestDarwinProcessTableSeesTheSessionWithoutPs(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()

	out, readerPid, err := readProcessTable()
	if err != nil {
		t.Fatalf("readProcessTable: %v", err)
	}
	if readerPid != 0 {
		t.Errorf("readerPid = %d: the darwin read must start no process of its own", readerPid)
	}
	rows, err := parsePSTable(out)
	if err != nil {
		t.Fatalf("parsePSTable refused the darwin table: %v", err)
	}
	byPid := map[int]psRow{}
	for _, r := range rows {
		byPid[r.pid] = r
	}
	self, ok := byPid[os.Getpid()]
	if !ok || self.ppid != os.Getppid() || self.rssKiB <= 0 {
		t.Errorf("own row = %+v (found %v), want ppid %d and a positive rss", self, ok, os.Getppid())
	}
	kid, ok := byPid[child.Process.Pid]
	if !ok || kid.ppid != os.Getpid() || kid.rssKiB <= 0 {
		t.Errorf("child row = %+v (found %v), want ppid %d and a positive rss", kid, ok, os.Getpid())
	}
	if tree := sessionTree(rows, os.Getpid(), 0); len(tree) == 0 {
		t.Errorf("the session tree under this test is empty; the guard would sum nothing")
	}
}

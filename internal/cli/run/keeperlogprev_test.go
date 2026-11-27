package run

import (
	"os"
	"strings"
	"testing"
)

// TestANewKeeperKeepsThePreviousKeepersLog: a keeper's log says how its jail ended, and the
// relaunch that follows an unexpected end starts a keeper of the same name at once. Opening the
// log truncated it, so the one record of that end was gone by the time anyone looked (2026-11-26:
// three jails ended together and two of their keeper logs held only the relaunch). The previous
// keeper's log is kept beside it as `.1`, one generation, which the orphan sweep already names.
func TestANewKeeperKeepsThePreviousKeepersLog(t *testing.T) {
	packHome(t)
	cname := "yolo-keeperlog-prev-podman-self"
	first, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = first.WriteString("keeper: the first jail ended\n")
	_ = first.Close()
	second, err := openKeeperLog(cname)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = second.WriteString("keeper: the relaunch\n")
	_ = second.Close()
	prev, err := os.ReadFile(keeperLogPath(cname) + ".1")
	if err != nil || !strings.Contains(string(prev), "the first jail ended") {
		t.Fatalf("the previous keeper's log is not kept as .1 (%v): %q", err, prev)
	}
	cur, _ := os.ReadFile(keeperLogPath(cname))
	if strings.Contains(string(cur), "the first jail ended") || !strings.Contains(string(cur), "the relaunch") {
		t.Errorf("the current log = %q, want the relaunch's lines alone", cur)
	}
	if fi, err := os.Stat(keeperLogPath(cname) + ".1"); err == nil && fi.Mode().Perm() != 0o600 {
		t.Errorf("the kept log is %v, want 0600: it names the jail's services and paths", fi.Mode().Perm())
	}
}

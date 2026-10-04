//go:build darwin

package notty

import (
	"os/exec"
	"strings"
)

// procGone says whether pid has exited: ps knows no such process, or it is a zombie its new
// parent has not reaped. darwin has no /proc, so ps(1) answers what procgone_linux_test.go reads.
func procGone(pid string) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", pid).Output()
	if err != nil {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

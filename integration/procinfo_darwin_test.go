//go:build darwin

package integration

// procinfo_darwin_test.go is the darwin half of the harness's view of host processes
// (procinfo_test.go). A Mac has no /proc, so every answer comes from ps(1), which every macOS
// install ships at /bin/ps.

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// processState is pid's state as ps prints it ("S", "R+", "Z", …). found is false when ps
// matched no process, which it reports by exiting 1 with nothing printed; err is any other
// failure to ask.
func processState(pid int) (state string, found bool, err error) {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	s := strings.TrimSpace(string(out))
	var exitErr *exec.ExitError
	if err != nil && errors.As(err, &exitErr) && s == "" {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if s == "" {
		return "", false, nil
	}
	return s, true, nil
}

// processArgs is pid's command line as ps prints it, split at spaces. ps joins argv with
// spaces, so an argument that itself holds one comes back as two; every caller here matches
// arguments that hold none.
func processArgs(pid int) ([]string, error) {
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// processChildren is every pid whose parent is ppid, from one `ps -A` listing.
func processChildren(ppid int) []int {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	var kids []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[1] != strconv.Itoa(ppid) {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			kids = append(kids, pid)
		}
	}
	return kids
}

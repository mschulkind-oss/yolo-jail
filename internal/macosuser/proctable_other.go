//go:build !darwin

package macosuser

import (
	"os/exec"
	"strings"
)

// psBin and psArgs are the process-table read OFF macOS: every
// process, numeric columns only, no header. Numeric only because a command name can hold
// spaces and a column that can hold anything is a column a parser has to guess at; the guard
// names a process by its pid. ON macOS the guard never runs /bin/ps: ps is setuid root there,
// and Seatbelt refuses a setuid exec inside any sandbox whatever the profile says, so the
// guard read nothing (`fork/exec /bin/ps: operation not permitted`, macos-user CI run
// 37940733418). proctable_darwin.go reads the same three columns from the kernel instead.
const psBin = "/bin/ps"

var psArgs = []string{"-ax", "-o", "pid=,ppid=,rss="}

// readProcessTable is the guard's process-table read off macOS: /bin/ps, which needs no
// privilege there. The guard only runs on macos-user; this half keeps the verb whole on the
// platforms its unit tests run on.
func readProcessTable() (string, int, error) {
	c := exec.Command(psBin, psArgs...)
	var out strings.Builder
	c.Stdout = &out
	if err := c.Start(); err != nil {
		return "", 0, err
	}
	pid := c.Process.Pid
	// A non-zero exit with a table on stdout is still a table: a ps that could not inspect one
	// process may say so in its status, and parsePSTable is what judges the rows. Only a ps
	// that printed nothing is a failure to read.
	if err := c.Wait(); err != nil && strings.TrimSpace(out.String()) == "" {
		return "", pid, err
	}
	return out.String(), pid, nil
}

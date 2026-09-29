package ghbroker

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// rundir.go is where a broker, or `yolo check`'s self-check, lays out the directories its gh
// runs under (RunnerOptions.RunDir): broker/github/run/<pid>-<random>, 0700. Each holds a copy
// of the host's hosts.yml, which is the host login when gh keeps it in the file, so a run dir
// is collected by LIVENESS, like a scope file (BB-D44), never by age: its owner removes it
// when it stops (Runner.Close), and one a killed owner left (SIGKILL, OOM) is removed by the
// next broker or self-check to start, once kill(pid, 0) answers ESRCH for the pid in its
// name. Any other answer, and any name that does not start with a pid, keeps it.

// runRoot is the directory every run dir sits in.
func runRoot() string { return filepath.Join(paths.BrokerSourceDir(Source), "run") }

// newRunDir names this process's run dir. NewRunner creates it.
func newRunDir() string {
	id, err := brokerscope.NewLaunchID()
	if err != nil {
		id = strconv.FormatInt(int64(os.Getpid()), 36)
	}
	return filepath.Join(runRoot(), strconv.Itoa(os.Getpid())+"-"+id)
}

// sweepRunDirs removes the run dirs whose owning process is known gone, and returns them.
func sweepRunDirs() (removed []string) {
	entries, err := os.ReadDir(runRoot())
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pidText, _, ok := strings.Cut(e.Name(), "-")
		pid, err := strconv.Atoi(pidText)
		if !ok || err != nil || pid <= 0 {
			continue
		}
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			p := filepath.Join(runRoot(), e.Name())
			if os.RemoveAll(p) == nil {
				removed = append(removed, p)
			}
		}
	}
	return removed
}

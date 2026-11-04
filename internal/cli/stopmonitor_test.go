//go:build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execmonitor"
)

const stopMonitorTestCID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const stopMonitorTestExecID = "exec-session-42"

// This front-door regression requires the actual force-removal caller to invoke the real helper.
// Deleting that production call leaves the container present and makes runStop fail.
func TestRunStopForceRecoversUnresolvedRunningContainerThroughPinnedExecMonitor(t *testing.T) {
	cname, _ := stopWorkspace(t, "podman-self")
	shrinkStopGone(t)
	procRoot, conmonPath := writeStopMonitorProcFixture(t)
	savedOps := stopMonitorProcessOps
	stopMonitorProcessOps = execmonitor.Ops{
		ProcRoot: procRoot, UID: os.Geteuid(), UIDSet: true, SelfPID: 99,
		PidfdOpen: func(pid int) (int, error) {
			if pid != 100 {
				t.Fatalf("pidfd opened for PID %d, want 100", pid)
			}
			return 77, nil
		},
		SendSignal: func(fd int, sig syscall.Signal) error {
			if fd != 77 || sig != syscall.SIGKILL {
				t.Fatalf("signal fd=%d sig=%v", fd, sig)
			}
			return nil
		},
		ClosePidfd: func(int) error { return nil },
	}
	t.Cleanup(func() { stopMonitorProcessOps = savedOps })
	containerExists, signaled := true, false
	var calls [][]string
	runner := func(argv []string, _ time.Duration) (string, bool, int) {
		calls = append(calls, append([]string(nil), argv...))
		switch {
		case len(argv) > 1 && argv[1] == "info":
			return fmt.Sprintf(`{"version":{"Version":"5.8.9"},"host":{"serviceIsRemote":false,"conmon":{"path":%q,"version":"conmon version 2.2.1, commit: test-build"}}}`, conmonPath), true, 0
		case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{.State.Running}}"):
			return "true\n", true, 0
		case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{.State.Status}}"):
			return "stopping\n", true, 0
		case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{json .}}"):
			return fmt.Sprintf(`{"Id":%q,"State":{"Status":"stopping"},"ExecIDs":[%q]}`, stopMonitorTestCID, stopMonitorTestExecID), true, 0
		case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
			if !containerExists {
				return "", true, 1
			}
			return "", true, 0
		case len(argv) > 1 && argv[1] == "stop":
			return "", true, 125
		case len(argv) > 1 && argv[1] == "rm":
			return "", true, 125
		default:
			t.Fatalf("unexpected runtime command: %v", argv)
			return "", false, 1
		}
	}
	stopMonitorProcessOps.SendSignal = func(fd int, sig syscall.Signal) error {
		if fd != 77 || sig != syscall.SIGKILL {
			t.Fatalf("signal fd=%d sig=%v", fd, sig)
		}
		signaled = true
		containerExists = false
		return nil
	}
	useStopExec(t, runner)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--force"}) })
	if rc != 0 || !signaled {
		t.Fatalf("runStop = %d, signal=%v\nstdout:\n%s\nstderr:\n%s\ncalls: %v", rc, signaled, stdout, stderr, calls)
	}
	if !strings.Contains(stdout, "Stopped "+cname) {
		t.Fatalf("public stop did not report confirmed removal: %s", stdout)
	}
	if !strings.Contains(fmt.Sprint(calls), "[podman info --format json]") {
		t.Errorf("recovery did not obtain the selected Podman's local runtime facts: %v", calls)
	}
}

func TestRunStopForceRecoversUnresolvedTypedLeftoverThroughPinnedExecMonitor(t *testing.T) {
	cname, finished := stopWorkspace(t, "podman-self")
	shrinkStopGone(t)
	procRoot, conmonPath := writeStopMonitorProcFixture(t)
	savedOps := stopMonitorProcessOps
	stopMonitorProcessOps = execmonitor.Ops{
		ProcRoot: procRoot, UID: os.Geteuid(), UIDSet: true, SelfPID: 99,
		PidfdOpen: func(int) (int, error) { return 77, nil },
		SendSignal: func(fd int, sig syscall.Signal) error {
			if fd != 77 || sig != syscall.SIGKILL {
				t.Fatalf("signal fd=%d sig=%v", fd, sig)
			}
			return nil
		},
		ClosePidfd: func(int) error { return nil },
	}
	t.Cleanup(func() { stopMonitorProcessOps = savedOps })
	containerExists, signaled := true, false
	var calls [][]string
	runner := func(argv []string, _ time.Duration) (string, bool, int) {
		calls = append(calls, append([]string(nil), argv...))
		switch {
		case len(argv) > 1 && argv[1] == "info":
			return fmt.Sprintf(`{"version":{"Version":"5.8.9"},"host":{"serviceIsRemote":false,"conmon":{"path":%q,"version":"conmon version 2.2.1, commit: test-build"}}}`, conmonPath), true, 0
		case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{.State.Running}}"):
			return "false\n", true, 0
		case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{.State.Status}}"):
			return "stopping\n", true, 0
		case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{json .}}"):
			return fmt.Sprintf(`{"Id":%q,"State":{"Status":"stopping"},"ExecIDs":[%q]}`, stopMonitorTestCID, stopMonitorTestExecID), true, 0
		case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
			if !containerExists {
				return "", true, 1
			}
			return "", true, 0
		case len(argv) > 1 && argv[1] == "rm":
			return "", true, 125
		default:
			t.Fatalf("unexpected runtime command: %v", argv)
			return "", false, 1
		}
	}
	stopMonitorProcessOps.SendSignal = func(fd int, sig syscall.Signal) error {
		if fd != 77 || sig != syscall.SIGKILL {
			t.Fatalf("signal fd=%d sig=%v", fd, sig)
		}
		signaled = true
		containerExists = false
		return nil
	}
	useStopExec(t, runner)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--force"}) })
	if rc != 0 || !signaled {
		t.Fatalf("runStop = %d, signal=%v\nstdout:\n%s\nstderr:\n%s\ncalls: %v", rc, signaled, stdout, stderr, calls)
	}
	if len(*finished) != 0 {
		t.Fatalf("leftover removal must not run keeper teardown: %v", *finished)
	}
	if !strings.Contains(stdout, "Ended the leftover container "+cname) {
		t.Fatalf("public stop did not report confirmed leftover removal: %s", stdout)
	}
	if !strings.Contains(fmt.Sprint(calls), "[podman rm --force "+cname+"]") {
		t.Errorf("force stop did not traverse the unresolved leftover-removal call site: %v", calls)
	}
}

func writeStopMonitorProcFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	proc := filepath.Join(base, "proc")
	conmon := filepath.Join(base, "conmon")
	if err := os.WriteFile(conmon, []byte("fake conmon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "100", "task", "100"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "200"), 0o700); err != nil {
		t.Fatal(err)
	}
	uid := os.Geteuid()
	write := func(pid, ppid int, state string, start uint64, cmd string) {
		dir := filepath.Join(proc, fmt.Sprint(pid))
		fields := make([]string, 20)
		fields[0], fields[1] = state, fmt.Sprint(ppid)
		for i := 2; i < len(fields); i++ {
			fields[i] = "0"
		}
		fields[19] = fmt.Sprint(start)
		stat := fmt.Sprintf("%d (fake comm) %s\n", pid, strings.Join(fields, " "))
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
			t.Fatal(err)
		}
		status := fmt.Sprintf("Name:\tfake\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o600); err != nil {
			t.Fatal(err)
		}
		if cmd != "" {
			if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmd), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(100, 1, "S", 1000, "conmon\x00-c\x00"+stopMonitorTestCID+"\x00-u\x00"+stopMonitorTestExecID+"\x00-e\x00")
	write(200, 100, "Z", 1001, "")
	if err := os.Symlink(conmon, filepath.Join(proc, "100", "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "100", "task", "100", "children"), []byte("200"), 0o600); err != nil {
		t.Fatal(err)
	}
	return proc, conmon
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want || strings.Contains(value, want) {
			return true
		}
	}
	return false
}

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

func TestRunStopForceRecoveryDeclinesCallerMetadataAndIdentityNegatives(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		force                   bool
		info                    string
		stateAgain              string
		execs                   string
		cmdline                 string
		mutateProc              func(t *testing.T, proc, conmon string)
		mutateAfterFinalInspect func(t *testing.T, proc string)
		pidfdErr                error
		signalErr               error
		selfPID                 int
		inspectID               string
		wantInfo                bool
	}{
		{name: "remote info", force: true, info: `{"version":{"Version":"5.8.9"},"host":{"serviceIsRemote":true,"conmon":{"path":"/bin/conmon","version":"conmon version 2.2.1, commit: test-build"}}}`, wantInfo: true},
		{name: "missing runtime facts", force: true, info: `{}`, wantInfo: true},
		{name: "unknown version", force: true, info: `{"version":{"Version":"5.9.0"},"host":{"serviceIsRemote":false,"conmon":{"path":"/bin/conmon","version":"conmon version 2.2.1, commit: test-build"}}}`, wantInfo: true},
		{name: "stored exec membership absent", force: true, execs: `[]`, wantInfo: true},
		{name: "main monitor lacks exec mode", force: true, cmdline: "conmon\x00-c\x00" + stopMonitorTestCID + "\x00-u\x00" + stopMonitorTestCID + "\x00", wantInfo: true},
		{name: "wrong container id", force: true, cmdline: "conmon\x00-c\x00" + strings.Repeat("f", 64) + "\x00-u\x00" + stopMonitorTestExecID + "\x00-e\x00", wantInfo: true},
		{name: "wrong exec id", force: true, cmdline: "conmon\x00-c\x00" + stopMonitorTestCID + "\x00-u\x00other-session\x00-e\x00", wantInfo: true},
		{name: "no zombie child", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) { writeStopChildren(t, proc, "") }},
		{name: "live direct child", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) { writeStopProcStat(t, proc, 200, 100, "S", 1001) }},
		{name: "foreign uid child", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) {
			uid := os.Geteuid() + 1
			path := filepath.Join(proc, "200", "status")
			if err := os.WriteFile(path, []byte(fmt.Sprintf("Name:\tfake\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "incomplete child enumeration", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) {
			if err := os.Remove(filepath.Join(proc, "100", "task", "100", "children")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "monitor zero start time", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) { writeStopProcStat(t, proc, 100, 1, "S", 0) }},
		{name: "executable mismatch", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, conmon string) {
			other := filepath.Join(filepath.Dir(conmon), "other-conmon")
			if err := os.WriteFile(other, []byte("other"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(proc, "100", "exe")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pid reuse after pidfd", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) { /* mutation occurs in pidfd seam below */ }},
		{name: "unknown monitor state initially", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) { writeStopProcStat(t, proc, 100, 1, "?", 1000) }},
		{name: "unknown monitor state at final recheck", force: true, wantInfo: true, mutateAfterFinalInspect: func(t *testing.T, proc string) { writeStopProcStat(t, proc, 100, 1, "?", 1000) }},
		{name: "monitor changed after final inspect", force: true, wantInfo: true, mutateAfterFinalInspect: func(t *testing.T, proc string) { writeStopProcStat(t, proc, 100, 1, "S", 2000) }},
		{name: "child changed after final inspect", force: true, wantInfo: true, mutateAfterFinalInspect: func(t *testing.T, proc string) { writeStopProcStat(t, proc, 200, 100, "S", 1001) }},
		{name: "pidfd send ESRCH", force: true, wantInfo: true, signalErr: syscall.ESRCH},
		{name: "pidfd send permission denied", force: true, wantInfo: true, signalErr: syscall.EPERM},
		{name: "pidfd unsupported", force: true, pidfdErr: syscall.ENOSYS, wantInfo: true},
		{name: "foreign uid monitor", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) {
			uid := os.Geteuid() + 1
			if err := os.WriteFile(filepath.Join(proc, "100", "status"), []byte(fmt.Sprintf("Name:\tfake\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "live child alongside zombie", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) {
			writeStopAdditionalChild(t, proc, 201, 100, "S", 1002)
			writeStopChildren(t, proc, "200 201")
		}},
		{name: "unreadable child identity", force: true, wantInfo: true, mutateProc: func(t *testing.T, proc, _ string) {
			if err := os.Remove(filepath.Join(proc, "200", "status")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "self pid", force: true, selfPID: 100, wantInfo: true},
		{name: "wrong typed full cid", force: true, inspectID: strings.Repeat("f", 64), wantInfo: true},
		{name: "stopping changed before signal", force: true, stateAgain: "running", wantInfo: true},
		{name: "unknown recheck", force: true, stateAgain: "malformed", wantInfo: true},
		{name: "force false despite internal remove", force: false, wantInfo: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = stopWorkspace(t, `{"runtime":"podman"}`)
			shrinkStopGone(t)
			proc, conmon := writeStopMonitorProcFixture(t)
			if tc.mutateProc != nil {
				tc.mutateProc(t, proc, conmon)
			}
			savedOps := stopMonitorProcessOps
			var signaled bool
			selfPID := tc.selfPID
			if selfPID == 0 {
				selfPID = 99
			}
			stopMonitorProcessOps = execmonitor.Ops{
				ProcRoot: proc, UID: os.Geteuid(), UIDSet: true, SelfPID: selfPID,
				PidfdOpen: func(pid int) (int, error) {
					if tc.name == "pid reuse after pidfd" {
						writeStopProcStat(t, proc, 100, 1, "S", 2000)
					}
					if tc.pidfdErr != nil {
						return -1, tc.pidfdErr
					}
					return 77, nil
				},
				SendSignal: func(int, syscall.Signal) error { signaled = true; return nil },
				ClosePidfd: func(int) error { return nil },
			}
			t.Cleanup(func() { stopMonitorProcessOps = savedOps })
			info := tc.info
			if info == "" {
				info = fmt.Sprintf(`{"version":{"Version":"5.8.9"},"host":{"serviceIsRemote":false,"conmon":{"path":%q,"version":"conmon version 2.2.1, commit: test-build"}}}`, conmon)
			}
			execs := tc.execs
			if execs == "" {
				execs = fmt.Sprintf(`[%q]`, stopMonitorTestExecID)
			}
			cmdline := tc.cmdline
			if cmdline == "" {
				cmdline = "conmon\x00-c\x00" + stopMonitorTestCID + "\x00-u\x00" + stopMonitorTestExecID + "\x00-e\x00"
			}
			if tc.cmdline != "" {
				if err := os.WriteFile(filepath.Join(proc, "100", "cmdline"), []byte(cmdline), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			containerExists, infoCalled := true, false
			inspectCount := 0
			runner := func(argv []string, _ time.Duration) (string, bool, int) {
				switch {
				case len(argv) > 1 && argv[1] == "info":
					infoCalled = true
					return info, true, 0
				case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{.State.Running}}"):
					return "false\n", true, 0
				case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{.State.Status}}"):
					return "stopping\n", true, 0
				case len(argv) > 1 && argv[1] == "inspect" && slicesContains(argv, "{{json .}}"):
					inspectCount++
					if inspectCount == 2 && tc.mutateAfterFinalInspect != nil {
						tc.mutateAfterFinalInspect(t, proc)
					}
					state := "stopping"
					if inspectCount > 1 && tc.stateAgain == "running" {
						state = "running"
					}
					if inspectCount > 1 && tc.stateAgain == "malformed" {
						return "not-json", true, 0
					}
					id := tc.inspectID
					if id == "" {
						id = stopMonitorTestCID
					}
					return fmt.Sprintf(`{"Id":%q,"State":{"Status":%q},"ExecIDs":%s}`, id, state, execs), true, 0
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
			stopMonitorProcessOps.SendSignal = func(int, syscall.Signal) error {
				if tc.signalErr != nil {
					return tc.signalErr
				}
				signaled = true
				containerExists = false
				return nil
			}
			useStopExec(t, runner)
			args := []string{"stop"}
			if tc.force {
				args = append(args, "--force")
			}
			var rc int
			_, _ = captureBoth(t, func() { rc = runStop(args) })
			if rc == 0 || signaled {
				t.Fatalf("negative caller case returned rc=%d signal=%v", rc, signaled)
			}
			if infoCalled != tc.wantInfo {
				t.Fatalf("Podman info called=%v, want %v", infoCalled, tc.wantInfo)
			}
		})
	}
}

func TestRunStopForceUnresolvedAppleContainerDoesNotProbePodmanOrSignal(t *testing.T) {
	cname, _ := stopWorkspace(t, `{"runtime":"container"}`)
	shrinkStopGone(t)
	shrinkStopJailGone(t)
	procRoot := filepath.Join(t.TempDir(), "unused-proc")
	var pidfdOpened, signaled, pidfdClosed bool
	savedOps := stopMonitorProcessOps
	stopMonitorProcessOps = execmonitor.Ops{
		ProcRoot: procRoot, UID: os.Geteuid(), UIDSet: true, SelfPID: 99,
		PidfdOpen:  func(int) (int, error) { pidfdOpened = true; return 77, nil },
		SendSignal: func(int, syscall.Signal) error { signaled = true; return nil },
		ClosePidfd: func(int) error { pidfdClosed = true; return nil },
	}
	t.Cleanup(func() { stopMonitorProcessOps = savedOps })
	listing := "ID IMAGE OS ARCH STATE ADDR\n" + cname + " localhost/yolo-jail:abc linux arm64 running 192.168.64.3/24\n"
	fake := &acStop{t: t, cname: cname, stopRC: 125, lsAfterStop: func(int) (string, int) { return listing, 0 }}
	podmanCalled := false
	runner := func(argv []string, timeout time.Duration) (string, bool, int) {
		if len(argv) > 0 && argv[0] == "podman" {
			podmanCalled = true
		}
		return fake.run(argv, timeout)
	}
	useStopExec(t, runner)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--force"}) })
	if rc != 1 {
		t.Fatalf("runStop = %d, want unresolved failure\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if !strings.Contains(stderr, "`container stop "+cname+"` failed") || fake.lsAsks == 0 {
		t.Fatalf("fake Apple Container did not remain present after its unresolved stop: asks=%d\nstderr:\n%s", fake.lsAsks, stderr)
	}
	if podmanCalled || pidfdOpened || signaled || pidfdClosed {
		t.Fatalf("non-Podman force stop crossed the recovery boundary: podman=%v pidfd-open=%v signal=%v close=%v", podmanCalled, pidfdOpened, signaled, pidfdClosed)
	}
}

func writeStopAdditionalChild(t *testing.T, proc string, pid, ppid int, state string, start uint64) {
	t.Helper()
	dir := filepath.Join(proc, fmt.Sprint(pid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStopProcStat(t, proc, pid, ppid, state, start)
	uid := os.Geteuid()
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(fmt.Sprintf("Name:\tfake\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeStopChildren(t *testing.T, proc, children string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(proc, "100", "task", "100", "children"), []byte(children), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeStopProcStat(t *testing.T, proc string, pid, ppid int, state string, start uint64) {
	t.Helper()
	fields := make([]string, 20)
	fields[0], fields[1] = state, fmt.Sprint(ppid)
	for i := 2; i < len(fields); i++ {
		fields[i] = "0"
	}
	fields[19] = fmt.Sprint(start)
	if err := os.WriteFile(filepath.Join(proc, fmt.Sprint(pid), "stat"), []byte(fmt.Sprintf("%d (fake comm) %s\n", pid, strings.Join(fields, " "))), 0o600); err != nil {
		t.Fatal(err)
	}
}

// shrinkStopJailGone shrinks the post-stop re-probe's wait for an Apple Container listing to stop
// naming the jail (probeJailGoneAfterStop), so a test whose jail stays listed need not wait out
// the production bound.
func shrinkStopJailGone(t *testing.T) {
	savedWait, savedPoll := stopJailGoneWait, stopJailGonePoll
	stopJailGoneWait, stopJailGonePoll = 20*time.Millisecond, time.Millisecond
	t.Cleanup(func() { stopJailGoneWait, stopJailGonePoll = savedWait, savedPoll })
}

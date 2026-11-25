package callbroker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// realMainEnv makes the test binary run the daemon's Main instead of its tests: the child
// process of a lifecycle test is the production entry point, not a helper that mirrors it.
const realMainEnv = "YOLO_TEST_CALLBROKER_MAIN"

// realMain is one daemon Main running as an owned child process in its own process group.
type realMain struct {
	cmd      *exec.Cmd
	socket   string
	stderr   string
	exited   chan struct{}
	exitCode int
}

func startRealMain(t *testing.T, f *onceFixture) *realMain {
	t.Helper()
	root := shortCallBrokerTempDir(t)
	m := &realMain{socket: filepath.Join(root, "broker.sock"), stderr: filepath.Join(root, "daemon.stderr"), exited: make(chan struct{})}
	stderr, err := os.Create(m.stderr)
	if err != nil {
		t.Fatal(err)
	}
	m.cmd = exec.Command(os.Args[0], "--socket", m.socket, "--launch-input", f.inputPath)
	m.cmd.Dir = f.h.workspace
	m.cmd.Env = append(os.Environ(), realMainEnv+"=1")
	m.cmd.Stderr = stderr
	m.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := m.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = m.cmd.Wait()
		m.exitCode = m.cmd.ProcessState.ExitCode()
		_ = stderr.Close()
		close(m.exited)
	}()
	t.Cleanup(func() {
		// The daemon and everything it started end with the test, whatever the test did.
		_ = syscall.Kill(-m.cmd.Process.Pid, syscall.SIGKILL)
		<-m.exited
	})
	deadline := time.Now().Add(holdGuard)
	for {
		if _, err := os.Stat(m.socket); err == nil {
			return m
		}
		select {
		case <-m.exited:
			t.Fatalf("the daemon exited before serving (%d): %s", m.exitCode, mustRead(t, m.stderr))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon never bound its socket")
		}
		time.Sleep(time.Millisecond)
	}
}

func (m *realMain) log(t *testing.T) string {
	t.Helper()
	return string(mustRead(t, m.stderr))
}

// waitLines waits until path holds at least n lines, the event a hook's marker stands for.
func waitLines(t *testing.T, path string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(holdGuard)
	for {
		if data, err := os.ReadFile(path); err == nil {
			if lines := strings.Fields(string(data)); len(lines) >= n {
				return lines
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %d lines", path, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// D13: the production Main runs the user's hook once per filed request without letting its
// output decide anything, holds an asking act in place and continues it when the host answers
// through the public approval path, reports a publication failure, and at SIGTERM closes what
// is still pending (Store.End) before it exits.
func TestScopedMainLifecycle(t *testing.T) {
	t.Run("D13", func(t *testing.T) {
		t.Setenv("YOLO_VERSION", "")
		skipFixtureBind = true
		t.Cleanup(func() { skipFixtureBind = false })
		f := selectedOnceFixtureForApply(t, false, true, false, false)
		marker := filepath.Join(f.home, "hook-requests")
		hook := filepath.Join(f.home, "hook.sh")
		script := "#!/bin/sh\nprintf '%s\\n' \"$YOLO_REQUEST_ID\" >> " + shquote.Quote(marker) + "\nprintf 'approved\\n'\n"
		if err := os.WriteFile(hook, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		user := mustRead(t, f.userPath)
		if err := os.WriteFile(f.userPath, append([]byte("on_request = "+strconv.Quote(shquote.Quote(hook))+"\n"), user...), 0600); err != nil {
			t.Fatal(err)
		}
		// The jail-readable record cannot be written: a regular file stands where its directory goes.
		if err := os.MkdirAll(filepath.Join(f.home, ".yolo"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.home, ".yolo", "approvals"), []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
		m := startRealMain(t, f)
		_, call := connectFront(t, shortCallBrokerTempDir(t), m.socket)
		host := &brokerstore.Store{Dir: f.store.Dir, Source: f.h.input.Source, Revalidate: ApprovalAuthority, Entries: RequestEntries, TimedLimit: ApprovalTimedLimit}

		// An asking act files one request, runs the hook once, and holds in place.
		first := async(call, actFrame("update", "a1"))
		ids := waitLines(t, marker, 1)
		request := onlyPending(t, f)
		if len(ids) != 1 || ids[0] != request.ID || request.State != "pending" || f.effects(t) != 0 {
			t.Fatalf("hook ran for %v, request %+v, effects=%d", ids, request, f.effects(t))
		}
		// The hook printed "approved". That is not consent: the request stays pending, nothing ran.
		if snap, err := f.store.Snapshot(); err != nil || len(snap.Grants) != 0 || f.effects(t) != 0 {
			t.Fatalf("hook output decided something: grants=%+v effects=%d %v", snap.Grants, f.effects(t), err)
		}
		// The host answers through the public approval path; the held call continues in place.
		if _, err := host.Decide(request.ID, "pending", brokerstore.Decision{}); err != nil {
			t.Fatal(err)
		}
		if r := waitResult(t, first); r.rc != 0 || f.effects(t) != 1 {
			t.Fatalf("the held call did not continue when answered: %+v effects=%d daemon=%s", r, f.effects(t), m.log(t))
		}

		// A second asking act holds; the daemon is told to stop; the call returns and the request closes.
		second := async(call, actFrame("update", "a2"))
		waitLines(t, marker, 2)
		if err := syscall.Kill(m.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		r := waitResult(t, second)
		select {
		case <-m.exited:
		case <-time.After(holdGuard):
			t.Fatal("the daemon did not exit after SIGTERM")
		}
		if r.rc != 64 || f.effects(t) != 1 || m.exitCode != 0 {
			t.Fatalf("a stopped daemon: call=%+v effects=%d exit=%d daemon=%s", r, f.effects(t), m.exitCode, m.log(t))
		}
		snap, err := f.store.Snapshot()
		if err != nil || len(snap.Requests) != 2 {
			t.Fatalf("requests: %+v %v", snap.Requests, err)
		}
		closed := 0
		for _, q := range snap.Requests {
			if q.State == "refused" && q.Reason == brokerstore.ReasonBrokerStopped {
				closed++
			}
		}
		if closed != 1 {
			t.Fatalf("Store.End did not close the pending request at stop: %+v", snap.Requests)
		}
		// G11 (callbroker half): the public approval path cannot answer a request the stop closed.
		for _, q := range snap.Requests {
			if q.State == "refused" {
				if _, err := host.Decide(q.ID, "pending", brokerstore.Decision{}); err == nil {
					t.Fatalf("a closed request was answered: %s", q.ID)
				}
			}
		}
		if after, err := f.store.Snapshot(); err != nil || len(after.Grants) != 1 {
			t.Fatalf("a closed request gained a grant: %+v %v", after.Grants, err)
		}
		if lines := waitLines(t, marker, 2); len(lines) != 2 {
			t.Fatalf("hook ran %d times for 2 requests: %v", len(lines), lines)
		}
		if log := m.log(t); !strings.Contains(log, "could not write the jail's record of request") {
			t.Fatalf("a publication failure was not reported: %s", log)
		}
	})
}

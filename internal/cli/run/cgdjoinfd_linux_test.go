//go:build linux

package run

import (
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// TestTheDelegateReceivesAPidfdAndResolvesItToAHostPid sends a real pidfd over a
// real AF_UNIX socket pair, exactly as yolo-cglimit --join does, and checks the
// daemon side reads the line AND resolves the descriptor to the pid the kernel
// attests. Deleting readRequestBounded's SCM_RIGHTS handling, or cgdCaller,
// fails it.
func TestTheDelegateReceivesAPidfdAndResolvesItToAHostPid(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	cf, sf := os.NewFile(uintptr(pair[0]), "client"), os.NewFile(uintptr(pair[1]), "server")
	defer cf.Close()
	defer sf.Close()
	client, err := net.FileConn(cf)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := net.FileConn(sf)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	pfd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Skipf("pidfd_open unavailable: %v", err)
	}
	defer unix.Close(pfd)
	line := []byte(`{"op":"join","name":"g"}` + "\n")
	if _, _, err := client.(*net.UnixConn).WriteMsgUnix(line, syscall.UnixRights(pfd), nil); err != nil {
		t.Fatal(err)
	}

	got, passed := readRequestBounded(server.(*net.UnixConn), 4096)
	defer closeFiles(passed)
	if string(got) != string(line) {
		t.Fatalf("line = %q", got)
	}
	if len(passed) != 1 {
		t.Fatalf("received %d fds, want 1", len(passed))
	}
	caller, target := cgdCaller(77, passed)
	if caller.PID != 77 || caller.Target == nil || target != os.Getpid() {
		t.Errorf("caller %+v target %d, want the pidfd's pid %d", caller, target, os.Getpid())
	}
}

func TestADescriptorThatIsNotAPidfdLeavesNoJoinTarget(t *testing.T) {
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if caller, target := cgdCaller(1, []*os.File{f}); caller.Target != nil || target != 0 {
		t.Errorf("a regular file became a join target: %+v", caller)
	}
}

// TestTheServeLoopRoutesPassedDescriptorsToTheHandler pins the call sites: the
// callee tests above pass even when the serve loop never uses them.
func TestTheServeLoopRoutesPassedDescriptorsToTheHandler(t *testing.T) {
	b, err := os.ReadFile("cgddaemon_linux.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"readRequestBounded(c,", "cgdCaller(peerPID, passed)", "cgd.HandleCaller(", "Target:  targetPID", "defer closeFiles(passed)"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the serve loop no longer contains %q", want)
		}
	}
}

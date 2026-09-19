package supervisor

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestChildForwardsReadinessFD proves the second half of the launch chain.
// entrypoint gives fd 3 to yolo-jaild; this child start must deliberately carry
// it through the next exec or the bridge can publish forever while boot waits on
// a pipe no child can reach.
func TestChildForwardsReadinessFD(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(write.Fd())))

	c := &child{spec: Spec{
		Name:    "ready",
		Cmd:     []string{"/bin/sh", "-c", "printf 'ready wire-bridge\\n' >&3"},
		Restart: "no",
	}, backoff: restartBackoffInitial}
	if err := c.start(); err != nil {
		t.Fatal(err)
	}
	got, err := bufio.NewReader(read).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if got != "ready wire-bridge\n" {
		t.Errorf("readiness = %q, want ready wire-bridge\\n", got)
	}
	stop := make(chan struct{})
	_ = c.waitAndMaybeRestart(stop)
}

// TestStartDoesNotOwnTheInheritedReadinessFD is the other half of the contract
// above: start must FORWARD the inherited descriptor without TAKING it. It
// wrapped the inherited fd in os.NewFile, which takes ownership and arms a
// finalizer that closes it — so every spawn minted another owner of a
// descriptor the supervisor does not own, and the first garbage collection
// after any of them was dropped closed it. Downstream of that, once the kernel
// recycled the number, a later collection closed an unrelated file: on darwin
// CI the casualty was the directory descriptor os.RemoveAll was walking, which
// surfaced as a `bad file descriptor` from unlinkat in a different test
// entirely. Restarts are the production shape (one wrapper per spawn), so spawn
// more than once before collecting.
func TestStartDoesNotOwnTheInheritedReadinessFD(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate LogDir
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	t.Setenv(paths.JailDaemonReadyFDEnv, strconv.Itoa(int(write.Fd())))

	for i := 0; i < 3; i++ {
		c := &child{spec: Spec{
			Name:    "ready",
			Cmd:     []string{"true"},
			Restart: "no", // "always" would sleep out the backoff between spawns
		}, backoff: restartBackoffInitial}
		if err := c.start(); err != nil {
			t.Fatal(err)
		}
		_ = c.waitAndMaybeRestart(make(chan struct{}))
	}
	// Drop every spawn's wrapper and run their finalizers. Two collections:
	// the first queues a wrapper's finalizer, the second lets the goroutine that
	// runs them drain.
	for i := 0; i < 2; i++ {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := write.Write([]byte("still ours\n")); err != nil {
		t.Fatalf("start() took ownership of the inherited readiness fd; "+
			"a collection closed it out from under us: %v", err)
	}
}

package supervisor

import (
	"bufio"
	"os"
	"strconv"
	"testing"

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

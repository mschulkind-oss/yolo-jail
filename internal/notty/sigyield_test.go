package notty

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/sigyield"
)

// TestRunBoundedHoldsTheSignalsItForwards: while its child runs, RunBounded forwards INT, TERM and HUP
// to the child's group and decides from what comes of it, so it holds them from the front door's
// terminal arm (internal/sigyield), and lets go once the child is gone.
func TestRunBoundedHoldsTheSignalsItForwards(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "go")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("sh", "-c", `echo ready; cat "$1" >/dev/null`, "sh", fifo)
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- RunBounded(c, Bound{}) }()
	if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	for _, s := range forwarded {
		if !sigyield.Held(s) {
			t.Errorf("%v is not held while the child runs", s)
		}
	}
	w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, s := range forwarded {
		if sigyield.Held(s) {
			t.Errorf("%v is still held after the child exited", s)
		}
	}
}

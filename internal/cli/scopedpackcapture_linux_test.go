package cli

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
	"golang.org/x/sys/unix"
)

func TestDispatchScopedPackCaptureDrainsSmallPipesDuringRealHelp(t *testing.T) {
	previous := scopedDispatchPipe
	readers := make(chan *os.File, 2)
	scopedDispatchPipe = func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		if _, err := unix.FcntlInt(w.Fd(), unix.F_SETPIPE_SZ, 4096); err != nil {
			r.Close()
			w.Close()
			return nil, nil, err
		}
		readers <- r
		return r, w, nil
	}
	defer func() { scopedDispatchPipe = previous }()
	type captured struct {
		rc          int
		out, stderr string
	}
	done := make(chan captured, 1)
	go func() {
		rc, out, stderr := dispatchScopedPackCapture(t, []string{"--help"})
		done <- captured{rc, out, stderr}
	}()
	deadline := time.NewTimer(testsupport.ReadinessBudget(t))
	defer deadline.Stop()
	select {
	case got := <-done:
		if got.rc != 0 || got.out != packUsage+"\n" {
			t.Fatalf("actual help capture lost bytes: rc=%d bytes=%d want=%d stderr=%s", got.rc, len(got.out), len(packUsage)+1, got.stderr)
		}
	case <-deadline.C:
		// Drain a blocked write and join the capture before failing/restoring its global seam.
		drained := make(chan struct{}, 2)
		for range 2 {
			r := <-readers
			go func() {
				_, _ = io.Copy(io.Discard, r)
				drained <- struct{}{}
			}()
		}
		<-done
		for range 2 {
			<-drained
		}
		t.Fatal("real help dispatch blocked because capture did not drain while writing")
	}
}

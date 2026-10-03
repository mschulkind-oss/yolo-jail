package darwinpkg

import (
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixstderr"
)

// TestStreamStderrTailCapturesFullTail guards the drain-then-Wait fix: every
// stderr line a failing child emits must land in the captured tail. The old
// code called cmd.Wait() before the pump goroutine finished draining
// StderrPipe (the documented-incorrect ordering), which could truncate the
// tail; a synchronous drain-then-Wait cannot. Run under -race to also catch the
// former unlocked concurrent access to the tail buffer.
func TestStreamStderrTailCapturesFullTail(t *testing.T) {
	cmd := exec.Command("sh", "-c", "for i in 1 2 3 4 5; do echo err-line-$i >&2; done; exit 7")
	tail, code, err := streamStderrTail(cmd, io.Discard, 30)
	if err != nil {
		t.Fatalf("streamStderrTail: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	got := strings.Join(tail, "\n")
	for i := 1; i <= 5; i++ {
		want := "err-line-" + string(rune('0'+i))
		if !strings.Contains(got, want) {
			t.Errorf("tail missing %q; got:\n%s", want, got)
		}
	}
}

// TestStreamStderrTailBounded confirms the tail keeps only the last max lines.
func TestStreamStderrTailBounded(t *testing.T) {
	cmd := exec.Command("sh", "-c", "for i in $(seq 1 10); do echo L$i >&2; done")
	tail, code, err := streamStderrTail(cmd, io.Discard, 3)
	if err != nil {
		t.Fatalf("streamStderrTail: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if want := []string{"L8", "L9", "L10"}; strings.Join(tail, ",") != strings.Join(want, ",") {
		t.Errorf("tail = %v, want %v", tail, want)
	}
}

// TestStreamStderrTailReadsPastALongLine: the drain read nix's stderr with a bufio.Scanner capped
// at 1 MiB a line. A longer line ended the read loop, and the Wait after it then waited on a nix
// blocked writing into a pipe nothing read any more, so a macos-user launch building its packages
// hung for good. The stand-in prints a 2 MiB line, then a last line, and exits 0.
func TestStreamStderrTailReadsPastALongLine(t *testing.T) {
	cmd := exec.Command("sh", "-c", `head -c 2097152 /dev/zero | tr '\0' x >&2; echo >&2; echo "after the long line" >&2`)
	type drained struct {
		tail []string
		code int
		err  error
	}
	done := make(chan drained, 1)
	go func() {
		tail, code, err := streamStderrTail(cmd, io.Discard, 30)
		done <- drained{tail, code, err}
	}()
	select {
	case d := <-done:
		if d.err != nil || d.code != 0 {
			t.Fatalf("streamStderrTail = code %d, err %v; want 0, nil", d.code, d.err)
		}
		joined := strings.Join(d.tail, "\n")
		if !strings.Contains(joined, "nix printed a line longer than") {
			t.Errorf("the tail does not say a line was too long to keep (%d bytes)", len(joined))
		}
		if !strings.HasSuffix(joined, "after the long line") {
			t.Errorf("the lines after the long one were not read: the tail ends %q",
				joined[max(0, len(joined)-200):])
		}
		if len(joined) > nixstderr.MaxLine+4096 {
			t.Errorf("the tail kept %d bytes of a 2 MiB line, more than the %d-byte cap", len(joined), nixstderr.MaxLine)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("streamStderrTail never returned from a child that printed a line over 1 MiB and exited: " +
			"the read loop stopped at the long line and Wait waits on a child blocked on its full pipe")
	}
}

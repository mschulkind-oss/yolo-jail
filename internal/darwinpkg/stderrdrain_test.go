package darwinpkg

import (
	"io"
	"os/exec"
	"strings"
	"testing"
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

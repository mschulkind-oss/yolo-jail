package image

import (
	"strings"
	"testing"
)

// TestProgressLineTTYRedrawsInPlace verifies the image-caching progress redraws
// on a single line (carriage return, no per-chunk newlines) on a TTY, throttled
// to when the rendered string changes, and closes with exactly one newline.
func TestProgressLineTTYRedrawsInPlace(t *testing.T) {
	var buf strings.Builder
	p := newProgressLine(&buf, true)
	est := int64(100 * 1024 * 1024) // 100 MB estimate
	// Feed 1 MB increments up to 100 MB — 100 updates. Each MB is a distinct
	// percent, so we expect ~99 redraws (capped at 99%), each starting with \r.
	for mb := int64(1); mb <= 100; mb++ {
		p.update(mb*1024*1024, est)
	}
	p.done()
	out := buf.String()

	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected exactly ONE trailing newline (single redrawn line), got %d:\n%q",
			strings.Count(out, "\n"), out)
	}
	if !strings.Contains(out, "\r") {
		t.Errorf("TTY progress must use carriage-return redraw; got %q", out)
	}
	// Every redraw is prefixed with \r, so \r count == number of visible updates
	// and must be far less than the 100 chunks would be if unthrottled per-line
	// (it is, but the key property is: no bare newlines mid-stream).
	if strings.Count(out, "Caching image...") < 2 {
		t.Errorf("expected multiple progress redraws, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("progress must end with a newline; got %q", out)
	}
}

// TestProgressLineThrottlesDuplicates confirms identical rendered strings are
// not re-emitted (a multi-GB stream at a steady 99% must not spam).
func TestProgressLineThrottlesDuplicates(t *testing.T) {
	var buf strings.Builder
	p := newProgressLine(&buf, true)
	est := int64(10 * 1024 * 1024)
	// Many chunks all rendering "99%" (past the cap) — should emit ONE update.
	for i := 0; i < 50; i++ {
		p.update(est*2+int64(i), est) // well past estimate → capped 99%
	}
	p.done()
	out := buf.String()
	if n := strings.Count(out, "Caching image..."); n != 1 {
		t.Errorf("steady-state progress should redraw once, got %d:\n%q", n, out)
	}
}

// TestProgressLinePipeSuppressed verifies a non-TTY writer gets NO per-chunk
// progress spam (the reported bug: 500 lines of "98 98 99" in a redirected log).
func TestProgressLinePipeSuppressed(t *testing.T) {
	var buf strings.Builder
	p := newProgressLine(&buf, false)
	est := int64(100 * 1024 * 1024)
	for mb := int64(1); mb <= 100; mb++ {
		p.update(mb*1024*1024, est)
	}
	p.done()
	if out := buf.String(); out != "" {
		t.Errorf("piped progress must emit nothing per-chunk; got %q", out)
	}
}

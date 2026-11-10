package internaldaemon

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// captureStartLines points the start line's writer at a buffer and the daemon's own stderr at a
// scratch file, so a test reads each separately.
func captureStartLines(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	saved, savedErr := startLineWriter, os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	startLineWriter, os.Stderr = &buf, f
	t.Cleanup(func() {
		startLineWriter, os.Stderr = saved, savedErr
		_ = f.Close()
	})
	return &buf
}

// TestStartLineIsWrittenOnceBeforeADaemonRuns: a bare git-broker refuses at once (no --socket),
// so the dispatch ends without serving; the start line is still its stderr's first and only one.
func TestStartLineIsWrittenOnceBeforeADaemonRuns(t *testing.T) {
	buf := captureStartLines(t)
	Run([]string{"git-broker"})
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") || !strings.Contains(out, "helper start: name=git-broker ") {
		t.Errorf("start line output = %q, want exactly one newline-terminated `helper start: name=git-broker` line", out)
	}
}

// TestStartLineIsNotWrittenForASelfCheck: a check is not a start.
func TestStartLineIsNotWrittenForASelfCheck(t *testing.T) {
	buf := captureStartLines(t)
	Run([]string{"git-broker", "--self-check"})
	if buf.Len() != 0 {
		t.Errorf("a --self-check wrote %q, want no start line", buf.String())
	}
}

// TestStartLineIsLeftToTheKeeperAndToUsageErrors: the keeper logs its own line (its stdio is
// /dev/null), and an unknown or missing name is not a start.
func TestStartLineIsLeftToTheKeeperAndToUsageErrors(t *testing.T) {
	saved := JailKeeper
	t.Cleanup(func() { JailKeeper = saved })
	JailKeeper = func([]string) int { return 0 }
	buf := captureStartLines(t)
	Run([]string{"jail-keeper"})
	Run([]string{"no-such-daemon"})
	Run(nil)
	if buf.Len() != 0 {
		t.Errorf("wrote %q, want no start line for the keeper or a usage error", buf.String())
	}
}

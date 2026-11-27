package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// THE HOST LAUNCH'S OTHER TERMINAL WRITERS GO THROUGH THE LIVE LINE (progress.Guard): the slow-span
// notice ("yolo: host.floor.pi.prepare took 5.741s") and the jails a host verb boots (hostJailStdout)
// write to the same terminal as the readiness phase's spinner, and used to land after it on one row
// ("Checking the programs packs declare… (6m00s)yolo adds ..."). Each test shows a live line on a
// recording terminal, writes through the production wiring, and reads the terminal as a screen.
// Delete either call site and its test sees a row carrying the spinner and the other text.

// glueScreen replays term as a terminal would show it: "\r" and erase-line rewrite the row.
func glueScreen(term string) []string {
	var rows []string
	var cur []byte
	col := 0
	for i := 0; i < len(term); i++ {
		switch {
		case term[i] == '\n':
			rows, cur, col = append(rows, string(cur)), nil, 0
		case term[i] == '\r':
			col = 0
		case strings.HasPrefix(term[i:], "\x1b[K"):
			cur = cur[:col]
			i += 2
		case strings.HasPrefix(term[i:], "\x1b[2K"):
			cur = nil
			i += 3
		default:
			if col < len(cur) {
				cur[col] = term[i]
			} else {
				cur = append(cur, term[i])
			}
			col++
		}
	}
	return append(rows, string(cur))
}

func assertNoGluedRow(t *testing.T, term, spinner string) {
	t.Helper()
	for _, row := range glueScreen(term) {
		if i := strings.Index(row, "…"); strings.HasPrefix(row, spinner) && i >= 0 && strings.Contains(row[i:], "yolo") {
			t.Errorf("a row carries the spinner and another writer's text: %q\nterminal rows: %q", row, glueScreen(term))
		}
	}
}

func liveLineOn(term io.Writer) *progress.Line {
	return progress.Config{Live: true, Immediate: true, Tick: make(chan time.Time)}.Start(term, "Checking the programs packs declare")
}

func TestTheHostSlowSpanNoticeNeverLandsOnALiveLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(paths.TimingEnv, "")
	t.Setenv(paths.VerboseEnv, "")
	var term bytes.Buffer
	timing := run.HostNotchTimingLog(true, false, func(string) string { return "" }, &term)
	if !timing.Recording() {
		t.Fatal("a typed --timing recorded nothing")
	}
	line := liveLineOn(&term)
	defer line.Abandon()
	timing.Log.Record("host.floor.pi.prepare", 5741*time.Millisecond)
	if !strings.Contains(term.String(), "yolo: host.floor.pi.prepare took 5.741s\n") {
		t.Fatalf("the slow-span notice was not printed whole:\n%q", term.String())
	}
	assertNoGluedRow(t, term.String(), "Checking the programs packs declare")
}

func TestHostJailStdoutNeverLandsOnALiveLine(t *testing.T) {
	// hostJailStdout writes to this process's stderr: point it at a file the test reads back.
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = orig; _ = f.Close() })
	// The live line draws on that same terminal.
	line := liveLineOn(f)
	defer line.Abandon()
	if _, err := io.WriteString(hostJailStdout(), "yolo adds pack-declared programs\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f.Name())
	if !strings.Contains(string(got), "yolo adds pack-declared programs\n") {
		t.Fatalf("the jail's line was not written whole:\n%q", got)
	}
	assertNoGluedRow(t, string(got), "Checking the programs packs declare")
}

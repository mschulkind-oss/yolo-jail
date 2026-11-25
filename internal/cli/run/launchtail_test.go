package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// A build's tail rows are redraws: through the launch tee they reach the terminal and never
// launch.log, whose result line is the only thing the step leaves in it.
func TestTheLaunchLogHoldsNoTailRows(t *testing.T) {
	var term, log bytes.Buffer
	tee := teeLog{w: &term, log: &log}
	line := progress.Config{Live: true, Immediate: true, Plain: true}.Start(tee, "Building")
	tail := line.Tail("fork a")
	_, _ = tail.Write([]byte("Compiling yolo\n"))
	line.Done("")
	if !strings.Contains(term.String(), "Compiling yolo") {
		t.Errorf("the terminal never saw the tail: %q", term.String())
	}
	if strings.Contains(log.String(), "Compiling") || strings.Contains(log.String(), "\x1b") {
		t.Errorf("launch.log received tail rows: %q", log.String())
	}
}

// NO_COLOR makes the tail's rows plain, as every other color decision on the stream is.
func TestTheProgressConfigFollowsNoColor(t *testing.T) {
	o := &Options{Getenv: func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	}}
	if !o.progressConfig().Plain {
		t.Error("NO_COLOR left the tail dimmed")
	}
	if (&Options{Getenv: func(string) string { return "" }}).progressConfig().Plain {
		t.Error("the tail is plain without NO_COLOR")
	}
}

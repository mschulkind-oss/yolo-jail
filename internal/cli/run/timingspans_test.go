package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// The timing gate is a pure OR of the flag and the two host-process env
// opt-ins — pinned as a table so a fourth spelling cannot appear without this
// test noticing (design D1/D5).
func TestTimingEnabledGate(t *testing.T) {
	cases := []struct {
		name string
		flag bool
		env  map[string]string
		want bool
	}{
		{"off by default", false, nil, false},
		{"flag", true, nil, true},
		{"YOLO_TIMING", false, map[string]string{paths.TimingEnv: "1"}, true},
		{"YOLO_VERBOSE", false, map[string]string{paths.VerboseEnv: "1"}, true},
		{"empty env is off", false, map[string]string{paths.TimingEnv: ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{Timing: tc.flag, Getenv: func(k string) string { return tc.env[k] }}
			if got := o.timingEnabled(); got != tc.want {
				t.Errorf("timingEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// initPerf's contract: nothing constructed and no file when the gate is off;
// collector + <ws>/.yolo/host-perf.log + live slow-span notice when it is on.
// The file-existence half is the call-site pin for the sink location (D2).
func TestInitPerfConstructsOnlyWhenEnabled(t *testing.T) {
	ws := t.TempDir()
	o := goldenOptions(ws, t.TempDir())
	o.Timing = false
	o.initPerf("yolo-ws-test0000")
	if o.Perf != nil {
		t.Fatal("initPerf constructed a collector while timing is off")
	}
	if _, err := os.Stat(filepath.Join(ws, ".yolo", HostPerfLogName)); !os.IsNotExist(err) {
		t.Fatal("timing-off launch created host-perf.log")
	}

	o.Timing = true
	// Replace both streams post-fillDefaults (the assemble_notices_test
	// pattern): goldenOptions leaves the process's real streams installed.
	var out, errb bytes.Buffer
	o.Stdout, o.Stderr = &out, &errb
	o.initPerf("yolo-ws-test0000")
	if o.Perf == nil {
		t.Fatal("initPerf did not construct a collector with timing on")
	}

	// A fast span lands in the file; a slow one also names itself on stderr.
	fast := o.Perf.Span("fast")
	fast.End()
	slow := o.Perf.Span("slow")
	time.Sleep(perf.SlowSpanThreshold + 50*time.Millisecond)
	slow.End()

	got, err := os.ReadFile(filepath.Join(ws, ".yolo", HostPerfLogName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"jail=yolo-ws-test0000", "start  fast", "end    slow  dur="} {
		if !strings.Contains(string(got), want) {
			t.Errorf("host-perf.log missing %q; got:\n%s", want, got)
		}
	}
	if !strings.Contains(errb.String(), "slow took") {
		t.Errorf("slow-span notice missing from stderr: %q", errb.String())
	}
	if strings.Contains(errb.String(), "fast took") {
		t.Errorf("fast span crossed the notice threshold: %q", errb.String())
	}
}

package cli

// stop_test.go pins `yolo stop`'s core: idempotence (the property the
// stop-then-launch series depends on — its first half must never fail on
// "nothing to stop"), the graceful-stop argv, and the two backends that are
// not container jails.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// stopRun records the commands it is asked to run and answers from a script:
// each entry is one invocation's (stdout, rc); ran is true unless the entry is
// the literal "-".
type stopRun struct {
	calls [][]string
	stats []string // per-call canned stdout; rc = 0 unless the entry starts "!"
}

func (s *stopRun) run(argv []string) (string, bool, int) {
	s.calls = append(s.calls, argv)
	if len(s.stats) == 0 {
		return "", false, 1
	}
	next := s.stats[0]
	s.stats = s.stats[1:]
	if next == "-" {
		return "", false, 1
	}
	if strings.HasPrefix(next, "!") {
		return next[1:], true, 1
	}
	return next, true, 0
}

func TestStopJail(t *testing.T) {
	t.Run("running jail is stopped gracefully", func(t *testing.T) {
		var out, err bytes.Buffer
		s := &stopRun{stats: []string{"true\n", ""}}
		if rc := stopJail(&out, &err, "/ws", "podman", s.run, nil); rc != 0 {
			t.Fatalf("rc=%d, err=%s", rc, err.String())
		}
		cname := runtime.FromWorkspace("/ws")
		if len(s.calls) != 2 || s.calls[1][0] != "podman" || s.calls[1][1] != "stop" ||
			s.calls[1][2] != cname {
			t.Errorf("the stop argv must be exactly '<rt> stop <cname>': %v", s.calls)
		}
		if !strings.Contains(out.String(), "Stopped "+cname) {
			t.Errorf("the stop must say what it stopped:\n%s", out.String())
		}
	})
	t.Run("nothing running is success — stop is idempotent", func(t *testing.T) {
		var out, err bytes.Buffer
		s := &stopRun{stats: []string{"false\n"}}
		if rc := stopJail(&out, &err, "/ws", "podman", s.run, nil); rc != 0 {
			t.Fatalf("a stopped jail must be success, rc=%d", rc)
		}
		if len(s.calls) != 1 {
			t.Errorf("nothing to stop must issue no stop command: %v", s.calls)
		}
		if !strings.Contains(out.String(), "No jail running") {
			t.Errorf("the no-op must say so:\n%s", out.String())
		}
	})
	t.Run("no container at all is success too", func(t *testing.T) {
		var out, err bytes.Buffer
		s := &stopRun{stats: []string{"!Error: no such container"}}
		if rc := stopJail(&out, &err, "/ws", "podman", s.run, nil); rc != 0 {
			t.Fatalf("an absent container must be success, rc=%d", rc)
		}
		if !strings.Contains(out.String(), "No jail running") {
			t.Errorf("the no-op must say so:\n%s", out.String())
		}
	})
	t.Run("a failed stop is a failure", func(t *testing.T) {
		var out, err bytes.Buffer
		s := &stopRun{stats: []string{"true\n", "!stopped with an error"}}
		if rc := stopJail(&out, &err, "/ws", "podman", s.run, nil); rc != 1 {
			t.Fatalf("a failed stop must fail, rc=%d", rc)
		}
	})
	t.Run("macos-user has nothing to stop", func(t *testing.T) {
		var out, err bytes.Buffer
		s := &stopRun{}
		if rc := stopJail(&out, &err, "/ws", "macos-user", s.run, nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
		if len(s.calls) != 0 || !strings.Contains(out.String(), "no persistent jail") {
			t.Errorf("macos-user must be answered, not probed:\n%s", out.String())
		}
	})
	t.Run("no runtime is a failure", func(t *testing.T) {
		var out, err bytes.Buffer
		if rc := stopJail(&out, &err, "/ws", "", (&stopRun{}).run, nil); rc != 1 {
			t.Fatalf("rc=%d", rc)
		}
	})
}

// The stop-arm span pins: with a collector installed, inspect and stop land as
// spans (in the file, in order); with none, nothing is written. Deleting
// either span call site in stopJail fails this.
func TestStopJailEmitsTimingSpans(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	s := &stopRun{stats: []string{"true", ""}}
	p := perf.New(time.Now)
	rc := stopJail(&strings.Builder{}, &strings.Builder{}, ws, "podman", s.run, p)
	if rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	var report strings.Builder
	p.Report(&report, time.Now())
	for _, want := range []string{"stop.inspect", "stop.stop_container"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("stop report missing %q; got:\n%s", want, report.String())
		}
	}
}

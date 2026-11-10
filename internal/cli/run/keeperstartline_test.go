package run

import (
	"strings"
	"testing"
)

// TestStartLineIsTheKeeperLogsFirstLine: the keeper's stdio is /dev/null, so its own log is where
// its build is recorded (PH-D9); the line comes before the scope line and every other one.
func TestStartLineIsTheKeeperLogsFirstLine(t *testing.T) {
	var first *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		first = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	log := f.keeperLog()
	line, _, _ := strings.Cut(log, "\n")
	if !strings.Contains(line, "helper start: name=jail-keeper ") {
		t.Errorf("the keeper log's first line is %q, want the helper start line for jail-keeper:\n%s", line, log)
	}
	if n := strings.Count(log, "helper start:"); n != 1 {
		t.Errorf("the keeper log holds %d start lines, want 1:\n%s", n, log)
	}
	first.release()
	f.wait()
}

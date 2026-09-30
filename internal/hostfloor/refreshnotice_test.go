package hostfloor

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
)

// refreshnotice_test.go pins the line the evergreen refresh prints BEFORE its registry poll. The
// poll is bounded at the poll timeout (a minute by default) and runs before `yolo host` says what
// it starts, so without the line a slow or unreachable registry is a launch that hangs saying
// nothing — a wait that is yolo's, not the agent's.

// pollWitness is a floor's Out that notes, the moment the notice is written, how many `npm view`
// polls had started: "said before the poll" is then a fact about order, not about a clock.
type pollWitness struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	dist    *floortest.Dist
	viewsAt int
}

const pollNotice = "checking the npm registry for a newer"

func (w *pollWitness) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.viewsAt < 0 && bytes.Contains(p, []byte(pollNotice)) {
		w.viewsAt = len(w.dist.NpmCalls("view"))
	}
	return w.buf.Write(p)
}

func (w *pollWitness) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestTheRefreshSaysItIsPollingBeforeItPolls: no notice on the install or inside the interval,
// where nothing is polled; past it, the notice is written before the one `npm view` starts, and
// names the program, the installed version and the bound.
func TestTheRefreshSaysItIsPollingBeforeItPolls(t *testing.T) {
	w := newWorld(t)
	clk := &clock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	w.floor.Now = clk.now
	w.floor.PollTimeout = 7 * time.Second
	w.publish("opencode-ai", "1.0.0", "bin=opencode")
	p := npmProgram("opencode", "opencode", "opencode-ai")
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(30 * time.Minute)
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.out.String(), pollNotice) {
		t.Fatalf("the notice was printed with no poll to make:\n%s", w.out.String())
	}

	witness := &pollWitness{dist: w.dist, viewsAt: -1}
	w.floor.Out = witness
	clk.t = clk.t.Add(2 * time.Hour)
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if witness.viewsAt != 0 || len(w.npmCalls("view")) != 1 {
		t.Fatalf("notice written after %d polls (want 0), %d polls in all (want 1):\n%s",
			witness.viewsAt, len(w.npmCalls("view")), witness.String())
	}
	if want := "yolo host: checking the npm registry for a newer opencode than 1.0.0 (at most 7s)"; !strings.Contains(witness.String(), want) {
		t.Errorf("the notice is not %q:\n%s", want, witness.String())
	}
}

package callbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
)

// C6, C7, C7a and C7e through the real CLI front and Handle (a held, answered act), with the
// test's scoped leaf: a real-Main child cannot carry scoped asks until a launch publishes them.
func TestGenericFrontWithheldFact(t *testing.T) {
	t.Run("C6 ask files one request and sends no provider read", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		_, call := selectedCLIFront(t, f.h.Handle)
		if r := call(map[string]any{"op": "ask", "argv": []string{"update", issue}}); r.rc != 0 || !strings.Contains(r.out, `"verdict":"asks"`) {
			t.Fatalf("ask: %+v", r)
		}
		f.noProviderSend(t)
		if pending := f.pending(t); len(pending) != 1 || len(pending[0].CallReads) != 1 {
			t.Fatalf("request: %+v", pending)
		}
	})
	t.Run("C7 answered while held resolves the fact and runs the allowed cell", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		done := async(call, actFrame("update", issue))
		n := fake.waitStarted(t)
		f.noProviderSend(t)
		answerOnce(t, f)
		fake.tick(n)
		if r := waitResult(t, done); r.rc != 0 || f.lookups(t) != 1 || f.effects(t) != 1 {
			t.Fatalf("held and answered: %+v lookups=%d writes=%d", r, f.lookups(t), f.effects(t))
		}
	})
	t.Run("C7a a refused resolved cell is refused and the answer is not refunded", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.Ask, brokerpolicy.OnRequest, falseOff, askOnReq)
		fake := newFakeHold()
		f.h.timers = fake.timers()
		_, call := selectedCLIFront(t, f.h.Handle)
		done := async(call, actFrame("update", prr))
		n := fake.waitStarted(t)
		answerOnce(t, f)
		fake.tick(n)
		r := waitResult(t, done)
		if r.rc != 64 || !strings.Contains(r.out, "fixture change ask") || f.lookups(t) != 1 || f.effects(t) != 0 {
			t.Fatalf("%+v lookups=%d writes=%d", r, f.lookups(t), f.effects(t))
		}
		if snap, err := f.store.Snapshot(); err != nil || snap.Grants[0].Uses != 0 {
			t.Fatalf("answer not spent: %+v %v", snap.Grants, err)
		}
	})
	t.Run("C7e a never read files nothing and sends nothing", func(t *testing.T) {
		f := factFixture(t, "2", brokerpolicy.True, brokerpolicy.Never, falseOff, askOnReq)
		_, call := selectedCLIFront(t, f.h.Handle)
		r := call(actFrame("update", issue))
		if r.rc != 64 || len(f.pending(t)) != 0 || !strings.Contains(r.out, "fixture read ceiling") {
			t.Fatalf("%+v", r)
		}
		f.noProviderSend(t)
	})
}

package callbroker

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// The generic broker's budgets (design 6.1). None is configurable. A call that does not ask
// decides and executes inside the one decideBudget; a call that asks gets the three in turn,
// and the front connection outlives all of them.
const (
	// decideBudget is classifying, facts, lookups and filing; it is also the whole budget of a
	// call that never asks. cliHandlerCallBudget is the same number under its older name.
	decideBudget = cliHandlerCallBudget
	// holdBudget is how long an asking act waits in place for its request's answer.
	holdBudget = 30 * time.Second
	// executeBudget is the fresh budget of the call that resumes after an answer: its
	// revalidation, replay and host program, whatever the decide budget had left.
	executeBudget = 30 * time.Second
	// connectionMargin is the slack a front connection keeps beyond the three budgets.
	connectionMargin = 10 * time.Second
	// connectionBudget is how long a front connection stays open: 5 s dial excluded.
	connectionBudget = decideBudget + holdBudget + executeBudget + connectionMargin
	// forwardDialBudget is the dial of a front connection.
	forwardDialBudget = 5 * time.Second
	// holdPollInterval is how often a held call reads its request's state.
	holdPollInterval = 20 * time.Millisecond
)

// connectionDeadline is when a front connection opened at now must be done.
func connectionDeadline(now time.Time) time.Time { return now.Add(connectionBudget) }

// holdTimers are the hold's two clocks. Production's are real timers; a test hands in channels
// it fires itself, so the 29 and 30 second cases never wait on a wall clock.
type holdTimers struct {
	// Expire fires once the budget d has passed.
	Expire func(d time.Duration) (<-chan time.Time, func())
	// Poll fires each time the request's state should be read again.
	Poll func() (<-chan time.Time, func())
	// Context bounds one phase (decide or execute) of a call by d. A test supplies contexts it
	// ends itself, to stand for a phase that used up its whole budget.
	Context func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc)
}

// budget is the context of one phase of a call, bounded by d.
func (h *CLIHandler) budget(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if h.timers.Context != nil {
		return h.timers.Context(parent, d)
	}
	return context.WithTimeout(parent, d)
}

// defaultHoldTimers is what a handler without its own timers uses. Only a test binary changes
// it, so tests that are not about the hold are not made to wait out a real 30 seconds.
var defaultHoldTimers = realHoldTimers

func realHoldTimers() holdTimers {
	return holdTimers{
		Expire: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTimer(d)
			return t.C, func() { t.Stop() }
		},
		Poll: func() (<-chan time.Time, func()) {
			t := time.NewTicker(holdPollInterval)
			return t.C, t.Stop
		},
	}
}

type holdEnd int

const (
	// holdAnswered: the request left pending (allowed, denied, expired or closed).
	holdAnswered holdEnd = iota
	// holdElapsed: the hold budget passed with the request still pending.
	holdElapsed
	// holdDisconnected: the client went away; the request stays pending.
	holdDisconnected
	// holdStopped: the broker is stopping; Store.End will close the request.
	holdStopped
)

// holdForAnswer waits for request id to leave pending without holding the source lock: each
// read is its own short store transaction, so another call of the same source proceeds
// meanwhile. It returns when the request is answered, the hold budget passes, the client
// disconnects, or ctx (the broker's own lifetime) ends. An answer already recorded when the
// budget passes is still returned: the budget bounds the wait, never a read.
func (h *CLIHandler) holdForAnswer(ctx context.Context, gone <-chan struct{}, jail, id string) (brokerstore.Request, holdEnd) {
	timers := h.timers
	if timers.Expire == nil || timers.Poll == nil {
		timers = defaultHoldTimers()
	}
	expired, stopExpire := timers.Expire(holdBudget)
	defer stopExpire()
	tick, stopTick := timers.Poll()
	defer stopTick()
	for {
		q, err := h.store.StatusContext(ctx, jail, h.start, id)
		if err != nil {
			if ctx.Err() != nil {
				return q, holdStopped
			}
			return q, holdElapsed
		}
		if q.State != "pending" {
			return q, holdAnswered
		}
		select {
		case <-ctx.Done():
			return q, holdStopped
		case <-gone:
			return q, holdDisconnected
		case <-expired:
			if last, err := h.store.StatusContext(ctx, jail, h.start, id); err == nil && last.State != "pending" {
				return last, holdAnswered
			}
			return q, holdElapsed
		case <-tick:
		}
	}
}

// resumeFrom marks a call that waited for request ID and runs again because it was allowed.
// Once is an Allow once answer, which only that call's own spend may use: it never files a
// second request, and a spend that finds the answer already used reports so.
type resumeFrom struct {
	ID   string
	Once bool
}

type resumeKey struct{}

func withResume(ctx context.Context, r resumeFrom) context.Context {
	return context.WithValue(ctx, resumeKey{}, r)
}

func resumeOf(ctx context.Context) (resumeFrom, bool) {
	r, ok := ctx.Value(resumeKey{}).(resumeFrom)
	return r, ok
}

// heldCall is what an asking act does after its hold.
type heldCall struct {
	// Verdict is the call's verdict when Resume is false.
	Verdict Verdict
	// Resume is set when the answer allowed the call: the caller runs it again under a fresh
	// execute budget, which revalidates everything at the source lock before spending.
	Resume resumeFrom
	Run    bool
}

// holdOutcome turns the end of a hold into the call's next step. asked is the verdict that
// filed the request.
func (h *CLIHandler) holdOutcome(gone <-chan struct{}, jail string, asked Verdict) heldCall {
	q, end := h.holdForAnswer(h.shutdown, gone, jail, asked.RequestID)
	switch end {
	case holdElapsed, holdDisconnected:
		return heldCall{Verdict: asked}
	case holdStopped:
		return heldCall{Verdict: Verdict{Verdict: "refused", Rule: "broker stopped", Digest: asked.Digest, RequestID: asked.RequestID,
			Next: "The broker stopped before request " + asked.RequestID + " was answered, so it is closed; relaunch the jail and retry the call"}}
	}
	switch q.State {
	case "allowed":
		return heldCall{Run: true, Resume: resumeFrom{ID: q.ID, Once: q.Answer != brokerstore.AnswerFor}}
	case "denied":
		next := "The host denied request " + q.ID + "; change the call or ask the human, then retry"
		if q.DenialNote != "" {
			next = "The host denied request " + q.ID + ": " + q.DenialNote + "; change the call or ask the human, then retry"
		}
		return heldCall{Verdict: Verdict{Verdict: "refused", Rule: "denied", Digest: asked.Digest, RequestID: q.ID, Next: next}}
	case "expired":
		return heldCall{Verdict: Verdict{Verdict: "refused", Rule: "request expired", Digest: asked.Digest, RequestID: q.ID,
			Next: "Request " + q.ID + " expired unanswered; retry the call to file a new request"}}
	}
	return heldCall{Verdict: Verdict{Verdict: "refused", Rule: "request closed", Digest: asked.Digest, RequestID: q.ID,
		Next: "Request " + q.ID + " was closed before it was answered; relaunch the jail if the broker stopped, then retry the call"}}
}

// usedVerdict is the answer to a call that waited for an Allow once another call spent first.
func usedVerdict(digest, id string) Verdict {
	return Verdict{Verdict: "used", Rule: "Allow once already used", Digest: digest, RequestID: id,
		Next: "Another call spent the Allow once for request " + id + " first; retry the call to file a new request if it is still needed"}
}

// verdictExit is the status a generic front call exits with when it did not run: 75 pending,
// 72 used, anything else a refusal.
func verdictExit(verdict string) int {
	switch verdict {
	case "asks":
		return 75
	case "used":
		return 72
	}
	return 64
}

// watchDisconnect reports, on gone, that the peer closed conn. The request was read in full
// before the handler ran, so nothing the peer legitimately sends is lost to this read. stop
// ends the watch and must be called before the connection is reused.
func watchDisconnect(conn net.Conn) (gone <-chan struct{}, stop func()) {
	ch := make(chan struct{})
	if conn == nil {
		return ch, func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					return
				}
				close(ch)
				return
			}
		}
	}()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			_ = conn.SetReadDeadline(time.Now())
			<-done
			_ = conn.SetReadDeadline(time.Time{})
		})
	}
}

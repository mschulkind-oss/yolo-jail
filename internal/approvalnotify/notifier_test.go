package approvalnotify

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func TestForgedActionCannotGrant(t *testing.T) {
	f := newNotifierFixture(t)
	f.start()
	f.signal(":1.99", "ActionInvoked", uint32(41), "once")
	f.signal(":1.7", "ActionInvoked", uint32(99), "once")
	f.signal(":1.7", "ActionInvoked", uint32(41), "default")
	f.signal(":1.7", "ActionInvoked", uint32(41), "deny")
	f.waitDone()
	f.assertDecisions([][]string{{"approve", "r-0123456789abcdef0123456789abcdef", "--deny"}})
}

type notifierFixture struct {
	t         *testing.T
	bus       *fakeBus
	event     brokerstore.EventView
	deps      Dependencies
	cancel    context.CancelFunc
	done      chan error
	decisions [][]string
}
type fakeBus struct {
	lifecycle     []string
	failAt        string
	owner         string
	caps          []string
	signals       chan *dbus.Signal
	posted        chan Notification
	closed        []uint32
	notifications []Notification
	lost          chan struct{}
	err           error
}

func (b *fakeBus) Owner(context.Context) (string, error) { return b.owner, b.err }
func (b *fakeBus) Capabilities(context.Context, string) ([]string, error) {
	if b.failAt == "caps" {
		return nil, errors.New("caps failed")
	}
	return b.caps, b.err
}
func (b *fakeBus) Subscribe(context.Context) error {
	if b.failAt == "subscribe" {
		return errors.New("subscribe failed")
	}
	return b.err
}
func (b *fakeBus) Notify(_ context.Context, _ string, n Notification) (uint32, error) {
	if b.failAt == "notify" || (b.failAt == "feedback" && len(b.notifications) > 0) {
		return 0, errors.New("Notify failed")
	}
	b.notifications = append(b.notifications, n)
	b.posted <- n
	return uint32(40 + len(b.notifications)), b.err
}
func (b *fakeBus) CloseNotification(_ context.Context, _ string, id uint32) error {
	b.lifecycle = append(b.lifecycle, "withdraw")
	b.closed = append(b.closed, id)
	return b.err
}
func (b *fakeBus) Signals() <-chan *dbus.Signal { return b.signals }
func (b *fakeBus) Done() <-chan struct{}        { return b.lost }
func (b *fakeBus) Close() error                 { b.lifecycle = append(b.lifecycle, "connection-close"); return nil }
func newNotifierFixture(t *testing.T) *notifierFixture {
	now := time.Now()
	f := &notifierFixture{t: t, done: make(chan error, 1), bus: &fakeBus{owner: ":1.7", caps: []string{"actions"}, signals: make(chan *dbus.Signal, 32), posted: make(chan Notification, 8), lost: make(chan struct{})}}
	f.event = brokerstore.EventView{Schema: 1, Event: "request-filed", Request: &brokerstore.RequestView{Schema: 1, ID: "r-0123456789abcdef0123456789abcdef", State: "pending", Expires: now.Add(time.Hour), Headline: "headline", Description: "description"}}
	f.deps = Dependencies{Address: "unix:path=/private/bus", Bin: "/absolute/yolo", Now: func() time.Time { return now }, Dial: func(context.Context, string) (Bus, error) { return f.bus, nil }, Decide: func(_ context.Context, _ string, args []string) (int, string, error) {
		f.decisions = append(f.decisions, args)
		return 0, "", nil
	}}
	return f
}
func (f *notifierFixture) start() {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.t.Cleanup(cancel)
	go func() { f.done <- Run(ctx, f.event, f.deps) }()
	select {
	case <-f.bus.posted:
	case err := <-f.done:
		f.t.Fatalf("before Notify: %v", err)
	case <-time.After(30 * time.Second):
		f.t.Fatal("Notify hung")
	}
}
func (f *notifierFixture) signal(sender, member string, body ...any) {
	f.bus.signals <- &dbus.Signal{Sender: sender, Path: "/org/freedesktop/Notifications", Name: "org.freedesktop.Notifications." + member, Body: body}
}
func (f *notifierFixture) waitDone() {
	f.t.Helper()
	select {
	case err := <-f.done:
		if err != nil {
			f.t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		f.t.Fatal("Run hung")
	}
}
func (f *notifierFixture) assertDecisions(want [][]string) {
	f.t.Helper()
	if !reflect.DeepEqual(f.decisions, want) {
		f.t.Fatalf("decisions %v want %v", f.decisions, want)
	}
}

func TestOfferedActionsUseOnlyTheCLIOnce(t *testing.T) {
	for _, key := range []string{"once", "15m", "deny"} {
		t.Run(key, func(t *testing.T) {
			f := newNotifierFixture(t)
			f.start()
			f.signal(":1.7", "ActionInvoked", uint32(41), key)
			f.signal(":1.7", "ActionInvoked", uint32(41), key)
			f.waitDone()
			args := []string{"approve", f.event.Request.ID}
			switch key {
			case "15m":
				args = append(args, "--for", "15m")
			case "deny":
				args = append(args, "--deny")
			}
			f.assertDecisions([][]string{args})
			if !reflect.DeepEqual(f.bus.closed, []uint32{41}) {
				t.Fatalf("withdrawal %v", f.bus.closed)
			}
		})
	}
}
func TestMalformedSignalsCannotAnswer(t *testing.T) {
	f := newNotifierFixture(t)
	f.start()
	f.bus.signals <- &dbus.Signal{Sender: ":1.7", Path: "/wrong", Name: notificationInterface + ".ActionInvoked", Body: []any{uint32(41), "once"}}
	f.signal(":1.7", "Wrong", uint32(41), "once")
	f.signal(":1.7", "ActionInvoked", int32(41), "once")
	f.signal(":1.7", "ActionInvoked", uint32(41), uint32(2))
	f.signal(":1.7", "ActionInvoked", uint32(41))
	f.signal(":1.7", "ActionInvoked", uint32(41), "once", "extra")
	f.signal(":1.7", "ActionInvoked", uint32(41), "unoffered")
	f.signal(":1.7", "ActionInvoked", uint32(41), "deny")
	f.waitDone()
	f.assertDecisions([][]string{{"approve", f.event.Request.ID, "--deny"}})
}
func TestOnlyDismissalReasonTwoDenies(t *testing.T) {
	for _, reason := range []uint32{1, 2, 3, 4} {
		t.Run(fmt.Sprint(reason), func(t *testing.T) {
			f := newNotifierFixture(t)
			f.start()
			f.signal(":1.7", "NotificationClosed", uint32(41), reason)
			if reason != 2 {
				f.signal(":1.7", "ActionInvoked", uint32(41), "once")
			}
			f.waitDone()
			args := []string{"approve", f.event.Request.ID}
			if reason == 2 {
				args = append(args, "--deny")
			}
			f.assertDecisions([][]string{args})
		})
	}
}
func TestHandledStopWithdrawsBeforeConnectionCloses(t *testing.T) {
	for _, expiry := range []bool{false, true} {
		t.Run(fmt.Sprint(expiry), func(t *testing.T) {
			f := newNotifierFixture(t)
			clock := make(chan time.Time, 1)
			f.deps.Timer = func(time.Duration) (<-chan time.Time, func()) { return clock, func() {} }
			f.start()
			if expiry {
				clock <- time.Now()
			} else {
				f.cancel()
			}
			f.waitDone()
			f.assertDecisions(nil)
			if !reflect.DeepEqual(f.bus.closed, []uint32{41}) || !reflect.DeepEqual(f.bus.lifecycle, []string{"withdraw", "connection-close"}) {
				t.Fatalf("cleanup ids=%v lifecycle=%v", f.bus.closed, f.bus.lifecycle)
			}
		})
	}
}
func TestOwnerReplacementNeverInheritsAnID(t *testing.T) {
	for _, changeSignal := range []bool{false, true} {
		t.Run(fmt.Sprint(changeSignal), func(t *testing.T) {
			f := newNotifierFixture(t)
			f.start()
			f.bus.owner = ":1.8"
			if changeSignal {
				f.bus.signals <- &dbus.Signal{Sender: "org.freedesktop.DBus", Path: "/org/freedesktop/DBus", Name: "org.freedesktop.DBus.NameOwnerChanged", Body: []any{notificationInterface, ":1.7", ":1.8"}}
			} else {
				f.signal(":1.8", "ActionInvoked", uint32(41), "once")
				f.signal(":1.7", "ActionInvoked", uint32(41), "once")
			}
			f.waitFailure("owner changed")
			f.assertDecisions(nil)
			if len(f.bus.closed) != 0 {
				t.Fatalf("closed old id at new server %v", f.bus.closed)
			}
		})
	}
}
func TestConnectionLossNeverDecides(t *testing.T) {
	f := newNotifierFixture(t)
	f.start()
	close(f.bus.lost)
	f.waitFailure("connection lost")
	f.assertDecisions(nil)
}
func TestInputAndAvailabilityFailuresHaveNoEffects(t *testing.T) {
	for _, kind := range []string{"expired", "event", "schema", "request-schema", "missing-request", "not-pending", "id", "address", "owner", "capabilities", "dial", "caps-error", "subscribe-error", "notify-error"} {
		t.Run(kind, func(t *testing.T) {
			f := newNotifierFixture(t)
			dialed := false
			originalDial := f.deps.Dial
			f.deps.Dial = func(c context.Context, a string) (Bus, error) {
				dialed = true
				if kind == "dial" {
					return nil, errors.New("dial failed")
				}
				return originalDial(c, a)
			}
			switch kind {
			case "expired":
				f.event.Request.Expires = f.deps.Now()
			case "event":
				f.event.Event = "decision"
			case "schema":
				f.event.Schema = 2
			case "request-schema":
				f.event.Request.Schema = 2
			case "missing-request":
				f.event.Request = nil
			case "not-pending":
				f.event.Request.State = "allowed"
			case "id":
				f.event.Request.ID = "--deny"
			case "address":
				f.deps.Address = ""
			case "owner":
				f.bus.owner = ""
			case "capabilities":
				f.bus.caps = nil
			case "caps-error":
				f.bus.failAt = "caps"
			case "subscribe-error":
				f.bus.failAt = "subscribe"
			case "notify-error":
				f.bus.failAt = "notify"
			}
			err := Run(context.Background(), f.event, f.deps)
			if err == nil || !strings.Contains(err.Error(), "host `yolo approve`") {
				t.Fatalf("failure: %v", err)
			}
			if (kind == "address" || kind == "expired" || kind == "event" || kind == "schema" || kind == "request-schema" || kind == "missing-request" || kind == "not-pending" || kind == "id") && dialed {
				t.Fatal("invalid input dialed")
			}
			f.assertDecisions(nil)
			if kind != "notify-error" && len(f.bus.notifications) != 0 {
				t.Fatal("failure posted")
			}
		})
	}
}
func TestCLIRefusalWithdrawsAndPostsNonActionableFeedback(t *testing.T) {
	for _, status := range []int{3, 5, 6, 2, 1} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newNotifierFixture(t)
			f.deps.Decide = func(_ context.Context, _ string, args []string) (int, string, error) {
				f.decisions = append(f.decisions, args)
				return status, "<b>refusal</b> & \x1b\u202e" + strings.Repeat("x", 10000), nil
			}
			f.start()
			f.signal(":1.7", "ActionInvoked", uint32(41), "15m")
			f.signal(":1.7", "NotificationClosed", uint32(41), uint32(2))
			f.signal(":1.7", "NotificationClosed", uint32(42), uint32(2))
			f.waitFailure(fmt.Sprintf("exited %d", status))
			f.assertDecisions([][]string{{"approve", f.event.Request.ID, "--for", "15m"}})
			if !reflect.DeepEqual(f.bus.closed, []uint32{41}) {
				t.Fatalf("closed %v", f.bus.closed)
			}
			want := 1
			if status == 3 || status == 5 || status == 6 {
				want = 2
			}
			if len(f.bus.notifications) != want {
				t.Fatalf("toasts %d", len(f.bus.notifications))
			}
			if want == 2 {
				n := f.bus.notifications[1]
				if len(n.Actions) != 0 || strings.Contains(n.Body, "<b>") || !strings.Contains(n.Body, "&lt;b&gt;") || !strings.Contains(n.Body, "host `yolo approve`") || len(n.Body) > textLimit+200 {
					t.Fatalf("feedback %+v", n)
				}
			}
		})
	}
}
func TestCLIStartAndFeedbackFailuresRemainFailures(t *testing.T) {
	for _, feedback := range []bool{false, true} {
		t.Run(fmt.Sprint(feedback), func(t *testing.T) {
			f := newNotifierFixture(t)
			f.deps.Decide = func(context.Context, string, []string) (int, string, error) {
				if feedback {
					return 5, "policy refused", nil
				}
				return 0, "", errors.New("exec failed")
			}
			if feedback {
				f.bus.failAt = "feedback"
			}
			f.start()
			f.signal(":1.7", "ActionInvoked", uint32(41), "once")
			if feedback {
				f.waitFailure("exited 5")
			} else {
				f.waitFailure("start approval CLI")
			}
			if !reflect.DeepEqual(f.bus.closed, []uint32{41}) {
				t.Fatal(f.bus.closed)
			}
		})
	}
}
func TestPresentationRemainsStoredText(t *testing.T) {
	f := newNotifierFixture(t)
	f.event.Request.Headline = "<b>x</b>\x1b\u202e"
	f.event.Request.Description = "<b>body</b> & \x1b\u202e"
	f.start()
	f.cancel()
	f.waitDone()
	n := f.bus.notifications[0]
	if n.Summary != "yolo: <b>x</b>\\x1b\\u202e" || n.Body != "&lt;b&gt;body&lt;/b&gt; &amp; \\x1b\\u202e" {
		t.Fatalf("text: %+v", n)
	}
	if !reflect.DeepEqual(n.Actions, []string{"once", "Allow once", "15m", "Allow for 15 minutes", "deny", "Deny"}) {
		t.Fatal(n.Actions)
	}
}
func (f *notifierFixture) waitFailure(sub string) {
	f.t.Helper()
	select {
	case err := <-f.done:
		if err == nil || !strings.Contains(err.Error(), sub) {
			f.t.Fatalf("error %v want %q", err, sub)
		}
	case <-time.After(30 * time.Second):
		f.t.Fatal("failure hung")
	}
}

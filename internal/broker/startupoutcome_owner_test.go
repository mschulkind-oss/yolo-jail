package broker

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// expiringPartialConn delivers n bytes of a frame, then reports a deadline timeout, but only once
// the owner closes it after its readiness deadline: the partial read and the teardown race.
type expiringPartialConn struct {
	net.Conn
	n       int
	total   int
	once    sync.Once
	started chan struct{}
	closed  chan struct{}
	reads   int
}

func (c *expiringPartialConn) SetReadDeadline(time.Time) error { return nil }
func (c *expiringPartialConn) Read(p []byte) (int, error) {
	c.reads++
	if c.reads == 1 {
		close(c.started)
		<-c.closed
	}
	if c.n == 0 {
		return 0, fixtureTimeout{}
	}
	// A prefix announcing a 10-byte body, of which only the first bytes ever arrive.
	frame := []byte{0, 0, 0, 10, '{', '"', 'v', '"', ':', '1'}
	off := c.total - c.n // bytes already delivered
	k := copy(p, frame[off:c.total])
	c.n -= k
	return k, fixtureTimeout{}
}
func (c *expiringPartialConn) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

type fixtureTimeout struct{}

func (fixtureTimeout) Error() string   { return "bounded fixture timeout" }
func (fixtureTimeout) Timeout() bool   { return true }
func (fixtureTimeout) Temporary() bool { return false }

// A partial frame that is still being read when the readiness deadline expires is a channel fault
// in the singleton's outcome, never owner cancellation, whatever closed the read.
func TestEnsureSingletonExpiredPartialFrameIsAFaultNotCancellation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes int
		fault hostservice.StartupReasonReadFault
	}{
		{"partial prefix", 2, hostservice.StartupReasonFaultPartialPrefix},
		{"partial body", 6, hostservice.StartupReasonFaultPartialBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeState{alive: map[int]bool{}}
			deps := newFakeDeps(t, st)
			deps.Name = "review"
			deps.StartupReason = true
			c := &expiringPartialConn{n: tc.bytes, total: tc.bytes, started: make(chan struct{}), closed: make(chan struct{})}
			deps.SpawnWithReason = func([]string, string, string) (int, func() bool, net.Conn, string, error) {
				return 77, func() bool { return false }, c, "attempt", nil
			}
			deps.waitForSocketUntil = func(_ string, d time.Time, _ func() bool, _ chan startupReasonResult) bool {
				select {
				case <-c.started:
				case <-time.After(time.Second):
					t.Fatal("reader was not invoked")
				}
				st.now = d
				return false
			}
			got := EnsureSingleton(deps).Outcome
			if got.Kind != hostservice.StartupKindReadinessTimedOut ||
				got.ReasonRead.Kind != hostservice.StartupReasonReadChannelFault || got.ReasonRead.Fault != tc.fault {
				t.Fatalf("expired %s erased by owner teardown: %+v", tc.name, got)
			}
		})
	}
}

// An empty channel at an expired deadline carries no frame to fault: it stays a no-fault read.
func TestEnsureSingletonExpiredEmptyChannelIsNotAFault(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}}
	deps := newFakeDeps(t, st)
	deps.Name = "review"
	deps.StartupReason = true
	c := &expiringPartialConn{started: make(chan struct{}), closed: make(chan struct{})}
	deps.SpawnWithReason = func([]string, string, string) (int, func() bool, net.Conn, string, error) {
		return 77, func() bool { return false }, c, "attempt", nil
	}
	deps.waitForSocketUntil = func(_ string, d time.Time, _ func() bool, _ chan startupReasonResult) bool {
		<-c.started
		st.now = d
		return false
	}
	got := EnsureSingleton(deps).Outcome
	if got.ReasonRead.Kind == hostservice.StartupReasonReadChannelFault || got.Kind != hostservice.StartupKindReadinessTimedOut {
		t.Fatalf("empty expired channel reported as a fault: %+v", got)
	}
}

// A pack's reason and remedy are literal text in the singleton's warning: brackets in them are
// never interpreted as rich markup, and the dependency remedy is the one printed.
func TestEnsureSingletonPrintsCooperativeTextLiterally(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}}
	deps := newFakeDeps(t, st)
	deps.Name = "review"
	deps.StartupReason = true
	deps.Color = true
	var out bytes.Buffer
	deps.Out = &out
	body, _ := json.Marshal(hostservice.StartupReason{Version: 1, Service: "review", Attempt: "attempt",
		Class: "dependency", Reason: "[red]safe cause[/red]", Remedy: "[green]safe remedy[/green]"})
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	frame = append(frame, body...)
	c := &bufferedStartupReasonConn{reader: bytes.NewReader(frame)}
	deps.SpawnWithReason = func([]string, string, string) (int, func() bool, net.Conn, string, error) {
		return 77, func() bool { return true }, c, "attempt", nil
	}
	deps.waitForSocketUntil = func(_ string, d time.Time, _ func() bool, rs chan startupReasonResult) bool {
		select {
		case r := <-rs:
			rs <- r
		case <-time.After(time.Second):
			t.Fatal("reader did not publish")
		}
		st.now = d
		return false
	}
	got := EnsureSingleton(deps).Outcome
	if got.ReasonRead.Kind != hostservice.StartupReasonReadRecord || got.ReasonClass != "dependency" {
		t.Fatalf("parser not invoked: %+v", got)
	}
	printed := out.String()
	if strings.Contains(printed, "\x1b[31m") || strings.Contains(printed, "\x1b[32m") {
		t.Fatalf("pack record interpreted as rich markup: %q", printed)
	}
	for _, want := range []string{"safe cause", "safe remedy", "red]", "green]"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("literal pack text %q missing from %q", want, printed)
		}
	}
}

// At an exhausted readiness budget, a record the reader already buffered is still the outcome,
// and the reason gets no fresh deadline.
func TestEnsureSingletonTimerArmRetainsABufferedRecord(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}}
	deps := newFakeDeps(t, st)
	deps.Name = "review"
	deps.StartupReason = true
	body, _ := json.Marshal(hostservice.StartupReason{Version: 1, Service: "review", Attempt: "attempt",
		Class: "permission", Reason: "safe", Remedy: "permission remedy"})
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	frame = append(frame, body...)
	c := &bufferedStartupReasonConn{reader: bytes.NewReader(frame)}
	deps.SpawnWithReason = func([]string, string, string) (int, func() bool, net.Conn, string, error) {
		return 77, func() bool { return true }, c, "attempt", nil
	}
	var original time.Time
	calls := 0
	deps.waitForSocketUntil = func(_ string, d time.Time, _ func() bool, rs chan startupReasonResult) bool {
		original = d
		select {
		case r := <-rs:
			rs <- r
		case <-time.After(time.Second):
			t.Fatal("reader did not publish")
		}
		st.now = d.Add(-time.Second)
		return false
	}
	deps.waitForStartupReason = func(_ chan startupReasonResult, remain time.Duration) (startupReasonResult, bool) {
		calls++
		if remain != time.Second {
			t.Fatalf("new budget %s", remain)
		}
		st.now = original
		return startupReasonResult{}, false
	}
	got := EnsureSingleton(deps).Outcome
	if calls != 1 || got.ReasonRead.Kind != hostservice.StartupReasonReadRecord || got.Remedy != "permission remedy" ||
		got.Kind != hostservice.StartupKindCooperativeRefusal {
		t.Fatalf("timer-arm buffered evidence lost: calls=%d outcome=%+v", calls, got)
	}
	if c.readDeadline() != original {
		t.Fatalf("reason reader deadline changed: %v != %v", c.readDeadline(), original)
	}
}

type heldReasonConn struct {
	net.Conn
	started  chan struct{}
	returned chan struct{}
}

func (c *heldReasonConn) Read(p []byte) (int, error) {
	close(c.started)
	defer close(c.returned)
	return c.Conn.Read(p)
}

// A ready singleton whose child holds its reason endpoint open cancels the optional read and
// joins it before returning.
func TestEnsureSingletonReadyCancelsAndJoinsAHeldRead(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}}
	deps := newFakeDeps(t, st)
	deps.Name = "review"
	deps.StartupReason = true
	st.now = time.Now()
	left, right := net.Pipe()
	defer right.Close()
	c := &heldReasonConn{Conn: left, started: make(chan struct{}), returned: make(chan struct{})}
	deps.SpawnWithReason = func([]string, string, string) (int, func() bool, net.Conn, string, error) {
		return 77, func() bool { return false }, c, "attempt", nil
	}
	deps.waitForSocketUntil = func(_ string, _ time.Time, _ func() bool, _ chan startupReasonResult) bool {
		select {
		case <-c.started:
		case <-time.After(time.Second):
			t.Fatal("held reason read did not begin")
		}
		return true
	}
	got := EnsureSingleton(deps).Outcome
	if got.Kind != hostservice.StartupKindSocketObserved || got.ReasonRead.Kind != hostservice.StartupReasonReadCancelled {
		t.Fatalf("ready/held channel has wrong owner disposition: %+v", got)
	}
	select {
	case <-c.returned:
	default:
		t.Fatal("blocked reason read still running after owner return")
	}
}

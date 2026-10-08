package broker

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

func TestBrokerStartupReasonChild(t *testing.T) {
	if os.Getenv("YOLO_TEST_STARTUP_REASON_CHILD") != "1" {
		return
	}
	if err := hostservice.WriteStartupReasonFromEnv(hostservice.StartupReason{
		Class: "configuration", Reason: "Child-side safe refusal.", Remedy: "Fix child settings.",
	}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestRealSpawnWithReasonPassesAttemptBoundChannel(t *testing.T) {
	t.Setenv("YOLO_TEST_STARTUP_REASON_CHILD", "1")
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	_, exited, conn, attempt, err := realSpawnWithReason(
		[]string{os.Args[0], "-test.run=^TestBrokerStartupReasonChild$"}, logPath, "aws-auth")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reason, err := hostservice.ReadStartupReason(conn, "aws-auth", attempt, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatalf("ReadStartupReason: %v", err)
	}
	if reason.Class != "configuration" || reason.Reason != "Child-side safe refusal." ||
		reason.Remedy != "Fix child settings." {
		t.Errorf("reason = %+v, want child record attributed to this spawn", reason)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !exited() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !exited() {
		t.Error("startup-reason child did not exit")
	}
}

type bufferedStartupReasonConn struct {
	net.Conn
	reader   *bytes.Reader
	mu       sync.Mutex
	deadline time.Time
}

func (c *bufferedStartupReasonConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedStartupReasonConn) Close() error               { return nil }
func (c *bufferedStartupReasonConn) SetReadDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadline = deadline
	c.mu.Unlock()
	return nil
}

func (c *bufferedStartupReasonConn) readDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

func TestEnsureSingletonUsesOriginalReadinessDeadlineForReason(t *testing.T) {
	for _, tc := range []struct {
		name            string
		remaining       time.Duration
		childExited     bool
		wantWaitForRead bool
	}{
		{name: "queued result at readiness deadline"},
		{name: "early child exit spends only remaining budget", remaining: 2 * time.Second, childExited: true, wantWaitForRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeState{alive: map[int]bool{}, reachOK: false, spawnPID: 77}
			deps := newFakeDeps(t, st)
			deps.Name = "aws-auth"
			deps.StartupReason = true
			var out bytes.Buffer
			deps.Out = &out
			deps.Now = func() time.Time { return st.now }
			deps.PathExists = func(string) bool { return false }
			var reasonConn *bufferedStartupReasonConn
			var childExited bool
			deps.SpawnWithReason = func(_ []string, _ string, service string) (int, func() bool,
				net.Conn, string, error) {
				const attempt = "controlled-attempt"
				record := hostservice.StartupReason{Version: 1, Service: service, Attempt: attempt,
					Class: "configuration", Reason: "Safe refusal.", Remedy: "Correct the setting."}
				body, err := json.Marshal(record)
				if err != nil {
					return 0, nil, nil, "", err
				}
				frame := make([]byte, 4+len(body))
				binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
				copy(frame[4:], body)
				reasonConn = &bufferedStartupReasonConn{reader: bytes.NewReader(frame)}
				return 77, func() bool { return childExited }, reasonConn, attempt, nil
			}

			var readinessDeadline time.Time
			deps.waitForSocketUntil = func(socketPath string, deadline time.Time, exited func() bool,
				results chan startupReasonResult) bool {
				readinessDeadline = deadline
				if socketPath != deps.SocketPath {
					t.Errorf("readiness socket = %q, want %q", socketPath, deps.SocketPath)
				}
				// This barrier waits until the real attempt-bound protocol reader has
				// validated and published its result, then puts it back in the buffer.
				var result startupReasonResult
				publicationWatchdog := time.NewTimer(2 * time.Second)
				defer publicationWatchdog.Stop()
				select {
				case result = <-results:
				case <-publicationWatchdog.C:
					t.Fatal("attempt-bound reader did not publish a result before the watchdog")
				}
				if result.err != nil || result.reason == nil || result.reason.Attempt != "controlled-attempt" {
					t.Fatalf("attempt-bound read result = %+v, want validated current-attempt record", result)
				}
				results <- result
				if tc.childExited {
					st.now = deadline.Add(-tc.remaining)
					childExited = true
					if !exited() {
						t.Fatal("early readiness failure fixture did not expose the exited child")
					}
				} else {
					st.now = deadline
					if exited() {
						t.Fatal("deadline fixture unexpectedly reports an exited child")
					}
				}
				return false
			}
			var waitCalls int
			deps.waitForStartupReason = func(results chan startupReasonResult, remaining time.Duration) (startupReasonResult, bool) {
				waitCalls++
				if !tc.wantWaitForRead {
					t.Errorf("started an extra startup-reason wait at the readiness deadline (%s remaining)", remaining)
				}
				if remaining != tc.remaining {
					t.Errorf("remaining reason budget = %s, want %s", remaining, tc.remaining)
				}
				result, ok := <-results
				return result, ok
			}

			got := EnsureSingleton(deps)
			defer os.Remove(deps.PIDFilePath)
			wantDeadline := time.Unix(1000, 0).Add(BrokerSpawnTimeout)
			if readinessDeadline != wantDeadline {
				t.Errorf("readiness deadline = %s, want original deadline %s", readinessDeadline, wantDeadline)
			}
			if gotDeadline := reasonConn.readDeadline(); gotDeadline != readinessDeadline {
				t.Errorf("reader deadline = %s, readiness deadline = %s", gotDeadline, readinessDeadline)
			}
			if (waitCalls > 0) != tc.wantWaitForRead {
				t.Errorf("remaining-budget wait calls = %d, want present=%v", waitCalls, tc.wantWaitForRead)
			}
			if got.StartupReason == nil || got.StartupReason.Reason != "Safe refusal." ||
				got.StartupReason.Remedy != "Correct the setting." {
				t.Fatalf("EnsureSingleton did not preserve the validated refusal: %+v", got.StartupReason)
			}
			if strings.Contains(out.String(), "did not bind its socket") {
				t.Errorf("typed refusal was replaced by a generic socket symptom: %s", out.String())
			}
		})
	}
}

func TestEnsureSingletonReadyWithoutReasonRemainsCompatible(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}, reachOK: false, spawnPID: 77}
	deps := newFakeDeps(t, st)
	deps.Name = "aws-auth"
	deps.StartupReason = true
	deps.Out = &bytes.Buffer{}
	deps.SpawnWithReason = func(_ []string, _ string, _ string) (int, func() bool,
		net.Conn, string, error) {
		parent, child := net.Pipe()
		_ = child.Close()
		return 77, func() bool { return false }, parent, "attempt", nil
	}
	// Keep this production-caller fixture bounded: the socket is immediately present, so
	// successful readiness remains independent of the absent optional reason record.
	deps.PathExists = func(string) bool { return true }
	got := EnsureSingleton(deps)
	defer os.Remove(deps.PIDFilePath)
	if got.StartupReason != nil {
		t.Errorf("ready singleton fabricated a startup reason: %+v", got.StartupReason)
	}
	if got.Started != true {
		t.Error("ready singleton was not recorded as started")
	}
	if out := deps.Out.(*bytes.Buffer).String(); out != "" {
		t.Errorf("normal ready startup fabricated a generic success or warning: %q", out)
	}
}

func TestEnsureSingletonReturnsOnlyCurrentAttemptStartupReason(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wrongToken bool
		badFrame   bool
		absent     bool
		wantReason bool
	}{
		{name: "attributed refusal", wantReason: true},
		{name: "wrong attempt is rejected", wrongToken: true},
		{name: "malformed record is rejected", badFrame: true},
		{name: "absent record falls back", absent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeState{alive: map[int]bool{}, reachOK: false, spawnPID: 77}
			deps := newFakeDeps(t, st)
			deps.Name = "aws-auth"
			deps.StartupReason = true
			deps.Now = time.Now
			deps.Sleep = time.Sleep
			deps.PathExists = func(string) bool { return false }
			var out bytes.Buffer
			deps.Out = &out
			deps.SpawnWithReason = func(_ []string, _ string, service string) (int, func() bool,
				net.Conn, string, error) {
				parent, child, attempt, err := hostservice.NewStartupReasonChannel()
				if err != nil {
					return 0, nil, nil, "", err
				}
				if tc.absent {
					_ = child.Close()
					return 77, func() bool { return true }, parent, attempt, nil
				}
				if tc.badFrame {
					var prefix [4]byte
					body := []byte("not-json")
					binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
					_, _ = child.Write(prefix[:])
					_, _ = child.Write(body)
					_ = child.Close()
					return 77, func() bool { return true }, parent, attempt, nil
				}
				wireAttempt := attempt
				if tc.wrongToken {
					wireAttempt = "another-attempt"
				}
				body, err := json.Marshal(hostservice.StartupReason{
					Version: 1, Service: service, Attempt: wireAttempt,
					Class: "configuration", Reason: "Safe refusal.", Remedy: "Correct the setting.",
				})
				if err != nil {
					_ = parent.Close()
					_ = child.Close()
					return 0, nil, nil, "", err
				}
				var prefix [4]byte
				binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
				if _, err := child.Write(prefix[:]); err != nil {
					_ = parent.Close()
					_ = child.Close()
					return 0, nil, nil, "", err
				}
				if _, err := child.Write(body); err != nil {
					_ = parent.Close()
					_ = child.Close()
					return 0, nil, nil, "", err
				}
				_ = child.Close()
				return 77, func() bool { return true }, parent, attempt, nil
			}

			got := EnsureSingleton(deps)
			if (got.StartupReason != nil) != tc.wantReason {
				t.Fatalf("StartupReason = %+v, want present=%v", got.StartupReason, tc.wantReason)
			}
			if tc.wantReason {
				if got.StartupReason.Reason != "Safe refusal." || got.StartupReason.Remedy != "Correct the setting." {
					t.Errorf("startup reason = %+v, want the exact current safe record", got.StartupReason)
				}
				if strings.Contains(out.String(), "did not bind its socket") {
					t.Errorf("a typed refusal was replaced by the derived socket symptom: %s", out.String())
				}
			} else if !strings.Contains(out.String(), "without binding its socket") {
				t.Errorf("badly attributed record was trusted or no fallback was reported: %s", out.String())
			}
			_ = os.Remove(deps.PIDFilePath)
		})
	}
}

// AN ACCEPTED REFUSAL ENDS THE SPAWN WAIT. The daemon writes its record and stays alive with no
// socket; the real readiness wait (no waitForSocketUntil seam, a real clock) returns as soon as the
// record is accepted, well inside BrokerSpawnTimeout, and the refusal is the ensure's answer.
// Dropping the record channel from brokerWaitForSocketUntil makes this wait the full window.
func TestEnsureSingletonAcceptedRefusalEndsTheReadinessWait(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}, reachOK: false, spawnPID: 77}
	deps := newFakeDeps(t, st)
	deps.Name = "aws-auth"
	deps.StartupReason = true
	deps.Out = &bytes.Buffer{}
	deps.Now = time.Now
	deps.Sleep = time.Sleep
	deps.PathExists = func(string) bool { return false }
	deps.SpawnWithReason = func(_ []string, _ string, service string) (int, func() bool, net.Conn, string, error) {
		const attempt = "alive-refusal"
		body, err := json.Marshal(hostservice.StartupReason{Version: 1, Service: service, Attempt: attempt,
			Class: "configuration", Reason: "Safe refusal.", Remedy: "Correct the setting."})
		if err != nil {
			return 0, nil, nil, "", err
		}
		frame := make([]byte, 4+len(body))
		binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
		copy(frame[4:], body)
		parent, child := net.Pipe()
		go func() { _, _ = child.Write(frame) }() // and then stays open, as an alive daemon would
		t.Cleanup(func() { _ = child.Close() })
		return 77, func() bool { return false }, parent, attempt, nil
	}
	started := time.Now()
	got := EnsureSingleton(deps)
	defer os.Remove(deps.PIDFilePath)
	if elapsed := time.Since(started); elapsed > BrokerSpawnTimeout/2 {
		t.Fatalf("EnsureSingleton waited %s after an accepted refusal (window %s)", elapsed, BrokerSpawnTimeout)
	}
	if got.StartupReason == nil || got.StartupReason.Reason != "Safe refusal." ||
		got.Outcome.Kind != hostservice.StartupKindCooperativeRefusal {
		t.Fatalf("ensure lost the refusal: reason=%+v outcome=%+v", got.StartupReason, got.Outcome)
	}
	// The refusing daemon this attempt spawned is this attempt's to end: left alive it would hold
	// the pid file of a singleton that never binds, which the next ensure cannot replace.
	st.mu.Lock()
	defer st.mu.Unlock()
	killed := false
	for _, k := range st.killed {
		if k.pid == 77 {
			killed = true
		}
	}
	if !killed {
		t.Fatalf("ensure left the refusing daemon it spawned running: kills=%v", st.killed)
	}
}

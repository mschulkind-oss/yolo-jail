package ghbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestHandlerSerializesWindowedDispatchAndRejectsStandingWrites(t *testing.T) {
	for _, standing := range []bool{false, true} {
		t.Run(map[bool]string{false: "windowed", true: "standing"}[standing], func(t *testing.T) {
			b, _ := windowFixture(t)
			var grant brokerstore.Grant
			if standing {
				os.MkdirAll(filepath.Dir(paths.UserConfigPath()), 0700)
				if err := os.WriteFile(paths.UserConfigPath(), []byte(`{"brokered":{"github":{"sets":[{"name":"read-write","mode":"standing"}]}}}`), 0600); err != nil {
					t.Fatal(err)
				}
				if code := callWindow(b, context.Background()); code != ExitNoPerm {
					t.Fatalf("standing write escaped refusal: %d", code)
				}
				return
			} else {
				if callWindow(b, context.Background()) != ExitTempFail {
					t.Fatal("not pending")
				}
				rs, _, _ := b.store.List()
				var err error
				grant, err = b.store.Decide(rs[0].ID, "pending", brokerstore.Decision{})
				if err != nil {
					t.Fatal(err)
				}
			}
			entered := make(chan error, 1)
			release := make(chan struct{})
			returned := make(chan int, 1)
			b.execute = func(ctx context.Context, _ []string, _ []byte, _, _ func([]byte)) RunResult {
				lock, err := os.OpenFile(filepath.Join(b.store.Dir, ".lock"), os.O_RDWR, 0600)
				if err == nil {
					err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
					lock.Close()
				}
				if err != syscall.EWOULDBLOCK {
					entered <- fmt.Errorf("executor-start has no shared authorization lock: %v", err)
				} else {
					data, e := os.ReadFile(filepath.Join(b.store.Dir, "grants", grant.ID+".json"))
					var saved brokerstore.Grant
					if e != nil {
						entered <- e
					} else if e = json.Unmarshal(data, &saved); e != nil {
						entered <- e
					} else if saved.Uses != 0 {
						entered <- fmt.Errorf("dispatch before durable spend: %d", saved.Uses)
					} else {
						entered <- nil
					}
				}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return RunResult{Exit: 0}
			}
			go func() { returned <- callWindow(b, context.Background()) }()
			var err error
			select {
			case err = <-entered:
			case code := <-returned:
				t.Fatalf("the call returned %d without reaching its executor", code)
			case <-time.After(dispatchStepTimeout):
				close(release)
				t.Fatalf("the executor did not start within %v", dispatchStepTimeout)
			}
			removing := make(chan struct{})
			removed := make(chan error, 1)
			go func() { close(removing); removed <- brokerscope.Remove(b.authority.path) }()
			<-removing
			if err != nil {
				close(release)
				<-returned
				<-removed
				t.Fatal(err)
			}
			select {
			case e := <-removed:
				close(release)
				<-returned
				t.Fatalf("scope removal won before dispatch completed: %v", e)
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			if code := <-returned; code != 0 {
				t.Fatal(code)
			}
			if e := <-removed; e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestHandlerCanceledBoundedDispatchReleasesAuthorityLock(t *testing.T) {
	b, _ := windowFixture(t)
	b.delivery = &fakeDelivery{ready: true, request: func(q brokerstore.Request) error {
		_, err := b.store.Decide(q.ID, "pending", brokerstore.Decision{})
		return err
	}}
	entered := make(chan struct{})
	b.execute = func(ctx context.Context, _ []string, _ []byte, _, _ func([]byte)) RunResult {
		close(entered)
		<-ctx.Done()
		return RunResult{Exit: ExitTimeout}
	}
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan int, 1)
	go func() { returned <- callWindow(b, ctx) }()
	awaitDispatchStep(t, "the executor to start", entered, returned)
	removed := make(chan error, 1)
	go func() { removed <- brokerscope.Remove(b.authority.path) }()
	cancel()
	select {
	case code := <-returned:
		if code != ExitTimeout {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatch ignored cancellation")
	}
	select {
	case err := <-removed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("authority lock leaked")
	}
	_, gs, err := b.store.List()
	if err != nil || len(gs) != 1 || gs[0].Uses != 0 {
		t.Fatal("canceled dispatch refunded", gs, err)
	}
	requests, _, err := b.store.List()
	if err != nil || len(requests) != 1 {
		t.Fatal("canceled request missing", requests, err)
	}
	events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal("read canceled dispatch audit", err)
	}
	var finalCalls int
	for _, event := range events {
		if event.Event != "call" || event.CommandDigest != requests[0].Digest {
			continue
		}
		finalCalls++
		if event.Outcome != "canceled" || event.Exit == nil || *event.Exit != ExitTimeout ||
			event.Request != requests[0].ID || event.Grant != gs[0].ID || event.StdinDigest != requests[0].StdinDigest {
			t.Fatalf("canceled final audit is not linked to the spent action: %+v", event)
		}
		if !strings.Contains(event.Reason, "inspect its effect before any retry") {
			t.Fatalf("canceled final audit omitted retry warning: %+v", event)
		}
	}
	if finalCalls != 1 {
		t.Fatalf("canceled dispatch wrote %d final call audits, want exactly one", finalCalls)
	}
}

func TestHandlerFinalAuditFailureDoesNotRetryCanceledAction(t *testing.T) {
	b, _ := windowFixture(t)
	b.delivery = &fakeDelivery{ready: true, request: func(q brokerstore.Request) error {
		_, err := b.store.Decide(q.ID, "pending", brokerstore.Decision{})
		return err
	}}
	if err := os.MkdirAll(filepath.Dir(paths.BrokerAuditLog()), 0o700); err != nil {
		t.Fatal(err)
	}
	entered, locked := make(chan struct{}), make(chan struct{})
	calls := 0
	var held *os.File
	b.execute = func(ctx context.Context, _ []string, _ []byte, _, _ func([]byte)) RunResult {
		calls++
		close(entered)
		<-ctx.Done()
		var err error
		held, err = os.OpenFile(filepath.Join(filepath.Dir(paths.BrokerAuditLog()), ".audit.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Errorf("open audit lock during finalization: %v", err)
			close(locked)
			return RunResult{Exit: ExitTimeout}
		}
		if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
			t.Errorf("hold audit lock during finalization: %v", err)
			_ = held.Close()
			held = nil
			close(locked)
			return RunResult{Exit: ExitTimeout}
		}
		close(locked)
		return RunResult{Exit: ExitTimeout}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		if held != nil {
			_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
			_ = held.Close()
		}
	}()
	returned := make(chan int, 1)
	go func() { returned <- callWindow(b, ctx) }()
	awaitDispatchStep(t, "the executor to start", entered, returned)
	cancel()
	awaitDispatchStep(t, "the executor to hold the audit lock", locked, returned)
	select {
	case code := <-returned:
		if code != ExitTimeout {
			t.Fatalf("canceled dispatched action returned %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("failed final audit held the canceled call open")
	}
	if held != nil {
		if err := syscall.Flock(int(held.Fd()), syscall.LOCK_UN); err != nil {
			t.Fatal(err)
		}
		_ = held.Close()
		held = nil
	}
	if calls != 1 {
		t.Fatalf("a final audit failure retried the executed action %d times", calls)
	}
	requests, grants, err := b.store.List()
	if err != nil || len(requests) != 1 || len(grants) != 1 || grants[0].Uses != 0 {
		t.Fatalf("failed final audit altered the executed one-use grant: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
	events, _, err := brokeraudit.Read(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event == "call" && event.Request == requests[0].ID && event.Grant == grants[0].ID && event.Outcome == "canceled" {
			t.Fatal("final audit unexpectedly succeeded while its lock was held")
		}
	}
}

func TestHandlerPendingLimitAndDenialCooldownNeverNotifyAgain(t *testing.T) {
	b, n := windowFixture(t)
	now := time.Now()
	b.store.Now = func() time.Time { return now }
	var delivered []string
	seen := map[string]bool{}
	b.delivery = &fakeDelivery{ready: true, request: func(q brokerstore.Request) error {
		if !seen[q.ID] {
			seen[q.ID] = true
			delivered = append(delivered, q.ID)
		}
		return nil
	}}
	call := func(body string) (int, string) {
		req := writeReq()
		req.Argv[len(req.Argv)-1] = body
		var msg strings.Builder
		code := b.ServeContext(context.Background(), req, paths.JailShortHash(b.authority.scope.Container), func([]byte) {}, func(p []byte) { msg.Write(p) })
		return code, msg.String()
	}
	for _, body := range []string{"a", "b", "c"} {
		if code, _ := call(body); code != ExitTempFail {
			t.Fatal(code)
		}
	}
	if code, _ := call("a"); code != ExitTempFail || len(delivered) != 3 {
		t.Fatal("duplicate did not join", code, delivered)
	}
	if code, msg := call("fourth"); code != ExitTempFail || len(delivered) != 3 {
		t.Fatal("cap notified", code, msg, delivered)
	} else {
		for _, id := range delivered {
			if !strings.Contains(msg, id) {
				t.Fatal("missing cap id", msg)
			}
		}
	}
	if _, err := b.store.Decide(delivered[0], "pending", brokerstore.Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	if code, msg := call("a"); code != ExitNoPerm || len(delivered) != 3 || !strings.Contains(msg, delivered[0]) {
		t.Fatal("denial notified", code, msg, delivered)
	}
	now = now.Add(10 * time.Minute)
	if code, _ := call("a"); code != ExitTempFail || len(delivered) != 4 {
		t.Fatal("cooldown boundary", code, delivered)
	}
	if n.Load() != 0 {
		t.Fatal("limit/cooldown dispatched")
	}
}

// dispatchStepTimeout bounds each step a dispatch test waits on, so a call that returns early
// or wedges fails with what it was waiting for instead of hanging to the package timeout.
const dispatchStepTimeout = 30 * time.Second

// awaitDispatchStep waits for step to close, failing at once if the call returned first (it
// never reached the step) and after dispatchStepTimeout if neither happens.
func awaitDispatchStep(t *testing.T, what string, step <-chan struct{}, returned <-chan int) {
	t.Helper()
	select {
	case <-step:
	case code := <-returned:
		t.Fatalf("waiting for %s: the call returned %d first", what, code)
	case <-time.After(dispatchStepTimeout):
		t.Fatalf("waiting for %s: nothing happened within %v", what, dispatchStepTimeout)
	}
}

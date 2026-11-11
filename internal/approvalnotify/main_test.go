package approvalnotify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerhook"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

type processBus struct {
	*fakeBus
	events net.Conn
}

func (b *processBus) Notify(c context.Context, o string, n Notification) (uint32, error) {
	id, err := b.fakeBus.Notify(c, o, n)
	fmt.Fprintln(b.events, "ready")
	return id, err
}
func (b *processBus) CloseNotification(c context.Context, o string, id uint32) error {
	err := b.fakeBus.CloseNotification(c, o, id)
	fmt.Fprintln(b.events, "withdraw", id)
	return err
}
func (b *processBus) Close() error { fmt.Fprintln(b.events, "connection-close"); return nil }

// The fake transport exists only in this test process. It drives the production
// entry, including signal.NotifyContext, without adding a test-only CLI verb.
func TestApprovalNotifyEntryProcess(t *testing.T) {
	if os.Getenv("YOLO_TEST_NOTIFIER_SOCKET") == "" {
		return
	}
	c, err := net.Dial("unix", os.Getenv("YOLO_TEST_NOTIFIER_SOCKET"))
	if err != nil {
		os.Exit(20)
	}
	defer c.Close()
	b := &processBus{fakeBus: &fakeBus{owner: ":1.7", caps: []string{"actions"}, signals: make(chan *dbus.Signal, 8), posted: make(chan Notification, 8), lost: make(chan struct{})}, events: c}
	rc := entry(nil, os.Stdin, os.Stderr, os.Getenv, "linux", false, func(context.Context, string) (Bus, error) { return b, nil })
	os.Exit(rc)
}
func processListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, path
}
func acceptEvents(t *testing.T, l net.Listener) (net.Conn, *bufio.Reader) {
	t.Helper()
	l.(*net.UnixListener).SetDeadline(time.Now().Add(30 * time.Second))
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(30 * time.Second))
	t.Cleanup(func() { c.Close() })
	return c, bufio.NewReader(c)
}
func expectEvent(t *testing.T, r *bufio.Reader, want string) {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != want {
		t.Fatalf("event=%q want=%q err=%v", line, want, err)
	}
}
func TestMainHandlesSIGINTAndSIGTERM(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			l, path := processListener(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			f := newNotifierFixture(t)
			input, _ := json.Marshal(f.event)
			cmd := exec.Command(exe, "-test.run=^TestApprovalNotifyEntryProcess$")
			cmd.Env = append(os.Environ(), "YOLO_TEST_NOTIFIER_SOCKET="+path, "YOLO_BIN="+exe, "DBUS_SESSION_BUS_ADDRESS=unix:path=/fixture")
			cmd.Stdin = bytes.NewReader(input)
			var out bytes.Buffer
			cmd.Stderr = &out
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cmd.Process.Kill() })
			_, r := acceptEvents(t, l)
			expectEvent(t, r, "ready")
			if err = cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			expectEvent(t, r, "withdraw 41")
			expectEvent(t, r, "connection-close")
			if err = cmd.Wait(); err != nil {
				t.Fatalf("process: %v %s", err, out.String())
			}
		})
	}
}
func TestBrokerHookStopsTheNotifierWhenRequestLeavesPending(t *testing.T) {
	for _, why := range []string{"decided", "expiry", "jail-ended"} {
		t.Run(why, func(t *testing.T) {
			l, path := processListener(t)
			t.Setenv("YOLO_TEST_NOTIFIER_SOCKET", path)
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/fixture")
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var skew atomic.Int64
			s := &brokerstore.Store{Dir: filepath.Join(t.TempDir(), "store"), Source: "github", Now: func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }, Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
			q, err := s.File(brokerstore.Request{Jail: "jail", Start: "start", Workspace: "/fixture", Argv: []string{"pr", "comment", "1"}, Repo: "o/r", Set: "read-write", Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 1}, StdinDigest: "stdin"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			runner := &brokerhook.Runner{Store: s, Start: "start", Command: "exec " + shquote.Join([]string{exe, "-test.run=^TestApprovalNotifyEntryProcess$"}), Bin: exe, Dir: t.TempDir(), Interval: time.Millisecond}
			done := make(chan struct{})
			go func() { runner.Run(ctx); close(done) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(30 * time.Second):
					t.Error("hook failed to stop")
				}
			}()
			_, r := acceptEvents(t, l)
			expectEvent(t, r, "ready")
			switch why {
			case "decided":
				_, err = s.Decide(q.ID, "pending", brokerstore.Decision{Deny: true})
			case "expiry":
				skew.Store(int64(time.Hour))
				_, _, err = s.List()
			case "jail-ended":
				err = s.End("jail", "start")
			}
			if err != nil {
				t.Fatal(err)
			}
			expectEvent(t, r, "withdraw 41")
			expectEvent(t, r, "connection-close")
		})
	}
}
func TestEntryRejectsInvalidEnvironmentAndInput(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"non-linux", "missing-bin", "relative-bin", "not-executable", "oversized-input", "wrong-schema", "trailing-json"} {
		t.Run(kind, func(t *testing.T) {
			f := newNotifierFixture(t)
			data, _ := json.Marshal(f.event)
			bin := exe
			goos := "linux"
			switch kind {
			case "non-linux":
				goos = "darwin"
			case "missing-bin":
				bin = ""
			case "relative-bin":
				bin = "yolo"
			case "not-executable":
				bin = filepath.Join(t.TempDir(), "not-executable")
				os.WriteFile(bin, []byte("x"), 0600)
			case "oversized-input":
				data = bytes.Repeat([]byte("x"), 128*1024+1)
			case "wrong-schema":
				data = []byte(`{"schema":2}`)
			case "trailing-json":
				data = append(data, []byte(`{}`)...)
			}
			called := false
			var diagnostic bytes.Buffer
			rc := entry(nil, bytes.NewReader(data), &diagnostic, func(k string) string {
				if k == "YOLO_BIN" {
					return bin
				}
				return "unix:path=/fixture"
			}, goos, false, func(context.Context, string) (Bus, error) { called = true; return f.bus, nil })
			if rc == 0 || called || !strings.Contains(diagnostic.String(), "host `yolo approve`") {
				t.Fatalf("rc=%d dialed=%v err=%s", rc, called, &diagnostic)
			}
		})
	}
}
func TestDecisionBoundsOutputAndReportsStartFailure(t *testing.T) {
	_, _, err := Decision(context.Background(), "/does/not/exist", []string{"approve"})
	if err == nil {
		t.Fatal("start failure lost")
	}
	b := &boundedOutput{}
	n, err := io.Copy(b, strings.NewReader(strings.Repeat("x", textLimit*10)))
	if err != nil || n != textLimit*10 || len(b.data) != textLimit {
		t.Fatalf("drain=%d retained=%d err=%v", n, len(b.data), err)
	}
}

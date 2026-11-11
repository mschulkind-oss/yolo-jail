package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/mschulkind-oss/yolo-jail/internal/approvalnotify"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestInternalApprovalNotifyDispatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		if rc := runInternal([]string{"approval-notify"}); rc == 0 {
			t.Fatal("non-Linux invocation accepted")
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"arguments", "jail", "invalid-json", "expired", "missing-address", "absent-bus"} {
		t.Run(kind, func(t *testing.T) {
			cmd := exec.Command(exe, testAsYoloArg, "internal", "approval-notify")
			address := ""
			absentBus := filepath.Join(t.TempDir(), "absent-bus.sock")
			if kind == "absent-bus" {
				address = "unix:path=" + absentBus
			}
			cmd.Env = append(os.Environ(), "YOLO_VERSION=", "YOLO_BIN="+exe, "DBUS_SESSION_BUS_ADDRESS="+address)
			input := `{}`
			switch kind {
			case "arguments":
				cmd.Args = append(cmd.Args, "extra")
			case "jail":
				cmd.Env = append(cmd.Env, "YOLO_VERSION=fixture")
			case "invalid-json":
				input = "not-json"
			default:
				e := brokerstore.EventView{Schema: 1, Event: "request-filed", Request: &brokerstore.RequestView{Schema: 1, ID: "r-0123456789abcdef0123456789abcdef", State: "pending", Expires: time.Now().Add(time.Hour)}}
				if kind == "expired" {
					e.Request.Expires = time.Now().Add(-time.Hour)
				}
				b, _ := json.Marshal(e)
				input = string(b)
			}
			cmd.Stdin = strings.NewReader(input)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "host `yolo approve`") || strings.Contains(string(out), "unknown internal") {
				t.Fatalf("dispatch err=%v out=%s", err, out)
			}
			expected := map[string]string{"arguments": "takes no arguments", "jail": "requires the Linux host", "invalid-json": "invalid request JSON", "expired": "unexpired pending", "missing-address": "DBUS_SESSION_BUS_ADDRESS is missing", "absent-bus": "connect to supplied bus"}[kind]
			if !strings.Contains(string(out), expected) {
				t.Fatalf("want %q got %s", expected, out)
			}
			// The production entry dials exactly the supplied private address, never a session bus.
			if kind == "absent-bus" && !strings.Contains(string(out), absentBus) {
				t.Fatalf("did not name the supplied bus %s: %s", absentBus, out)
			}
		})
	}
}

type approvalNotifyBus struct {
	signals chan *dbus.Signal
	toasts  []approvalnotify.Notification
	closed  []uint32
	action  string
}

func (b *approvalNotifyBus) Owner(context.Context) (string, error) { return ":1.7", nil }
func (b *approvalNotifyBus) Capabilities(context.Context, string) ([]string, error) {
	return []string{"actions"}, nil
}
func (b *approvalNotifyBus) Subscribe(context.Context) error { return nil }
func (b *approvalNotifyBus) Notify(_ context.Context, _ string, n approvalnotify.Notification) (uint32, error) {
	b.toasts = append(b.toasts, n)
	id := uint32(40 + len(b.toasts))
	if len(b.toasts) == 1 {
		b.signals <- &dbus.Signal{Sender: ":1.7", Path: "/org/freedesktop/Notifications", Name: "org.freedesktop.Notifications.ActionInvoked", Body: []any{id, b.action}}
	}
	return id, nil
}
func (b *approvalNotifyBus) CloseNotification(_ context.Context, _ string, id uint32) error {
	b.closed = append(b.closed, id)
	return nil
}
func (b *approvalNotifyBus) Signals() <-chan *dbus.Signal { return b.signals }
func (b *approvalNotifyBus) Done() <-chan struct{}        { return nil }
func (b *approvalNotifyBus) Close() error                 { return nil }

func TestApprovalNotifyRealCLIAndStoreBoundary(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectApprovePacks(t, home, "github")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "yolo")
	if err = os.WriteFile(bin, []byte("#!/bin/sh\nexec '"+exe+"' "+testAsYoloArg+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"once", "deny", "already-decided", "duration-policy", "removed-scope"} {
		t.Run(kind, func(t *testing.T) {
			sf := brokerscope.File{Source: ghbroker.Source, LaunchID: kind, PID: os.Getpid(), Container: "fixture-" + kind, Workspace: filepath.Join(root, kind), Repos: []string{"o/r"}, Grants: []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: "*"}}}
			scope, err := brokerscope.Write(sf)
			if err != nil {
				t.Fatal(err)
			}
			start, err := brokerstore.NewStartID()
			if err != nil {
				t.Fatal(err)
			}
			a, err := ghbroker.BindAuthority(scope, sf, start)
			if err != nil {
				t.Fatal(err)
			}
			s := &brokerstore.Store{Dir: paths.BrokerSourceDir(ghbroker.Source), Source: ghbroker.Source, Revalidate: a.Revalidate}
			d, p, err := a.Classify([]string{"pr", "comment", "1", "--body", "hi"}, "o/r")
			if err != nil {
				t.Fatal(err)
			}
			if kind == "duration-policy" {
				p.Window = time.Minute
				p.MaxWindow = 2 * time.Minute
				p.Session = false
			}
			q, err := s.File(brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: start, Workspace: sf.Workspace, LaunchID: sf.LaunchID, Argv: d.Argv, Repo: "o/r", Set: d.Set, Policy: p, StdinDigest: "stdin", About: ghbroker.RequestAbout(d.Argv, d.Repos)})
			if err != nil {
				t.Fatal(err)
			}
			view := s.RequestView(q)
			event := brokerstore.EventView{Schema: 1, Event: "request-filed", Request: &view}
			if kind == "already-decided" {
				if _, err = s.Decide(q.ID, "pending", brokerstore.Decision{Deny: true}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "removed-scope" {
				if err = os.Remove(scope); err != nil {
					t.Fatal(err)
				}
			}
			action := "once"
			if kind == "deny" {
				action = "deny"
			}
			if kind == "duration-policy" {
				action = "15m"
			}
			b := &approvalNotifyBus{signals: make(chan *dbus.Signal, 4), action: action}
			status := -1
			message := ""
			var argv []string
			err = approvalnotify.Run(context.Background(), event, approvalnotify.Dependencies{Address: "unix:path=/fixture", Bin: bin, Dial: func(context.Context, string) (approvalnotify.Bus, error) { return b, nil }, Decide: func(ctx context.Context, bin string, args []string) (int, string, error) {
				argv = args
				var e error
				status, message, e = approvalnotify.Decision(ctx, bin, args)
				return status, message, e
			}})
			wantStatus := 0
			switch kind {
			case "already-decided":
				wantStatus = 3
			case "duration-policy":
				wantStatus = 5
			case "removed-scope":
				wantStatus = 6
			}
			if status != wantStatus {
				t.Fatalf("exit=%d want=%d err=%v CLI=%s", status, wantStatus, err, message)
			}
			wantArgs := []string{"approve", q.ID}
			if action == "deny" {
				wantArgs = append(wantArgs, "--deny")
			}
			if action == "15m" {
				wantArgs = append(wantArgs, "--for", "15m")
			}
			if !reflect.DeepEqual(argv, wantArgs) {
				t.Fatal(argv)
			}
			if wantStatus == 0 && err != nil {
				t.Fatal(err)
			}
			if wantStatus != 0 && (err == nil || !strings.Contains(message, "Next:")) {
				t.Fatalf("refusal lost: %v %q", err, message)
			}
			if !reflect.DeepEqual(b.closed, []uint32{41}) {
				t.Fatal(b.closed)
			}
			if wantStatus != 0 {
				if len(b.toasts) != 2 || len(b.toasts[1].Actions) != 0 || !strings.Contains(b.toasts[1].Body, "host `yolo approve`") {
					t.Fatal(b.toasts)
				}
			}
			working, err := s.Working()
			if err != nil {
				t.Fatal(err)
			}
			if len(working) != 0 {
				t.Fatalf("click widened authority into working entries: %+v", working)
			}
			rs, gs, err := s.List()
			if err != nil {
				t.Fatal(err)
			}
			state := ""
			for _, r := range rs {
				if r.ID == q.ID {
					state = r.State
				}
			}
			wantState := "pending"
			switch kind {
			case "once":
				wantState = "allowed"
			case "deny", "already-decided":
				wantState = "denied"
			}
			if state != wantState {
				t.Fatalf("store state=%s want=%s", state, wantState)
			}
			var requestGrants []brokerstore.Grant
			for _, g := range gs {
				if g.Request == q.ID {
					requestGrants = append(requestGrants, g)
				}
			}
			if kind == "once" {
				if len(requestGrants) != 1 || requestGrants[0].Digest != q.Digest || requestGrants[0].Uses != 1 {
					t.Fatalf("once grants %+v", requestGrants)
				}
			} else if len(requestGrants) != 0 {
				t.Fatalf("refusal/deny granted %+v", requestGrants)
			}
		})
	}
	audit, err := os.ReadFile(paths.BrokerAuditLog())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(audit), `"event":"decision"`) != 2 {
		t.Fatalf("CLI decision audit: %s", audit)
	}
}

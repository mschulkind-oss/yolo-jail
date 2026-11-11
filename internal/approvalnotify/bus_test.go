package approvalnotify

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type wireCall struct {
	destination string
	path        dbus.ObjectPath
	method      string
	flags       dbus.Flags
	args        []any
}
type wireConn struct {
	calls        []wireCall
	signals      chan<- *dbus.Signal
	order        []string
	owner        string
	errAt        string
	duringNotify func()
	ctx          context.Context
}
type wireObject struct {
	dbus.BusObject
	conn        *wireConn
	destination string
	path        dbus.ObjectPath
}

func (c *wireConn) Object(dest string, path dbus.ObjectPath) dbus.BusObject {
	return &wireObject{conn: c, destination: dest, path: path}
}
func (c *wireConn) Signal(ch chan<- *dbus.Signal) {
	c.signals = ch
	c.order = append(c.order, "signal")
}
func (c *wireConn) AddMatchSignalContext(_ context.Context, options ...dbus.MatchOption) error {
	c.order = append(c.order, fmt.Sprint(options))
	return nil
}
func (c *wireConn) Context() context.Context { return c.ctx }
func (c *wireConn) Close() error             { c.order = append(c.order, "close"); return nil }
func (o *wireObject) CallWithContext(_ context.Context, method string, flags dbus.Flags, args ...any) *dbus.Call {
	c := o.conn
	c.calls = append(c.calls, wireCall{o.destination, o.path, method, flags, args})
	c.order = append(c.order, method)
	if c.errAt == method {
		return &dbus.Call{Err: errors.New("wire failure")}
	}
	switch method {
	case "org.freedesktop.DBus.GetNameOwner":
		return &dbus.Call{Body: []any{c.owner}}
	case notificationInterface + ".GetCapabilities":
		return &dbus.Call{Body: []any{[]string{"actions"}}}
	case notificationInterface + ".Notify":
		if c.duringNotify != nil {
			c.duringNotify()
		}
		return &dbus.Call{Body: []any{uint32(41)}}
	case notificationInterface + ".CloseNotification":
		return &dbus.Call{}
	default:
		panic(method)
	}
}
func TestGodbusAdapterAuthenticatesAndRegistersBeforeNotify(t *testing.T) {
	f := newNotifierFixture(t)
	c := &wireConn{owner: ":1.7", ctx: context.Background()}
	c.duringNotify = func() {
		if c.signals == nil {
			t.Error("Notify before signal registration")
			return
		}
		c.signals <- &dbus.Signal{Sender: ":1.99", Path: notificationPath, Name: notificationInterface + ".ActionInvoked", Body: []any{uint32(41), "once"}}
		c.signals <- &dbus.Signal{Sender: ":1.7", Path: notificationPath, Name: notificationInterface + ".ActionInvoked", Body: []any{uint32(41), "deny"}}
	}
	var addresses []string
	f.deps.Dial = func(ctx context.Context, address string) (Bus, error) {
		return dialBus(ctx, address, func(a string) (connection, error) { addresses = append(addresses, a); return c, nil })
	}
	if err := Run(context.Background(), f.event, f.deps); err != nil {
		t.Fatal(err)
	}
	f.assertDecisions([][]string{{"approve", f.event.Request.ID, "--deny"}})
	if !reflect.DeepEqual(addresses, []string{f.deps.Address}) {
		t.Fatal(addresses)
	}
	if len(c.calls) != 6 {
		t.Fatalf("calls %+v", c.calls)
	} // owner, caps, notify, answer owner, cleanup owner, close
	for _, call := range c.calls {
		if call.flags != dbus.FlagNoAutoStart {
			t.Fatalf("auto start enabled: %+v", call)
		}
		if call.method == "org.freedesktop.DBus.GetNameOwner" {
			if call.destination != "org.freedesktop.DBus" || call.path != "/org/freedesktop/DBus" || !reflect.DeepEqual(call.args, []any{notificationInterface}) {
				t.Fatal(call)
			}
		} else {
			if call.destination != ":1.7" || call.path != notificationPath {
				t.Fatal(call)
			}
		}
	}
	if c.calls[1].method != notificationInterface+".GetCapabilities" || len(c.calls[1].args) != 0 {
		t.Fatal(c.calls[1])
	}
	n := c.calls[2]
	if n.method != notificationInterface+".Notify" || dbus.SignatureOf(n.args...).String() != "susssasa{sv}i" {
		t.Fatalf("Notify wire %+v", n)
	}
	if n.args[0] != "yolo" || n.args[1] != uint32(0) || n.args[7] != int32(0) {
		t.Fatal(n.args)
	}
	hints := n.args[6].(map[string]dbus.Variant)
	if hints["urgency"].Value() != byte(2) || hints["resident"].Value() != true {
		t.Fatal(hints)
	}
	if !reflect.DeepEqual(n.args[5], []string{"once", "Allow once", "15m", "Allow for 15 minutes", "deny", "Deny"}) {
		t.Fatal(n.args[5])
	}
	if c.calls[5].method != notificationInterface+".CloseNotification" || !reflect.DeepEqual(c.calls[5].args, []any{uint32(41)}) {
		t.Fatal(c.calls[5])
	}
	// Two actual godbus match rules, installed between capability and Notify.
	if c.order[2] != "signal" || !strings.Contains(c.order[3], notificationInterface) || !strings.Contains(c.order[4], "NameOwnerChanged") || c.order[5] != notificationInterface+".Notify" {
		t.Fatal(c.order)
	}
}
func TestGodbusMissingAddressDoesNotConnect(t *testing.T) {
	called := false
	_, err := dialBus(context.Background(), "", func(string) (connection, error) { called = true; return nil, nil })
	if err == nil || called {
		t.Fatalf("err=%v dial=%v", err, called)
	}
}
func TestGodbusMethodFailuresReachConsumer(t *testing.T) {
	for _, method := range []string{"org.freedesktop.DBus.GetNameOwner", notificationInterface + ".GetCapabilities", notificationInterface + ".Notify"} {
		t.Run(method, func(t *testing.T) {
			f := newNotifierFixture(t)
			c := &wireConn{owner: ":1.7", ctx: context.Background(), errAt: method}
			f.deps.Dial = func(context.Context, string) (Bus, error) {
				return &dbusBus{conn: c, signals: make(chan *dbus.Signal, 4)}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := Run(ctx, f.event, f.deps); err == nil {
				t.Fatal("missing wire failure")
			}
			f.assertDecisions(nil)
		})
	}
}

func TestGodbusRechecksOwnerAtActionTime(t *testing.T) {
	f := newNotifierFixture(t)
	c := &wireConn{owner: ":1.7", ctx: context.Background()}
	c.duringNotify = func() {
		c.owner = ":1.8"
		c.signals <- &dbus.Signal{Sender: ":1.7", Path: notificationPath, Name: notificationInterface + ".ActionInvoked", Body: []any{uint32(41), "once"}}
	}
	f.deps.Dial = func(context.Context, string) (Bus, error) {
		return &dbusBus{conn: c, signals: make(chan *dbus.Signal, 8)}, nil
	}
	err := Run(context.Background(), f.event, f.deps)
	if err == nil || !strings.Contains(err.Error(), "owner changed before answer") {
		t.Fatal(err)
	}
	f.assertDecisions(nil)
	for _, call := range c.calls {
		if call.method == notificationInterface+".CloseNotification" {
			t.Fatalf("closed old owner id: %+v", call)
		}
	}
}

func TestProductionConnectUsesOnlySuppliedAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent-bus.sock")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "invalid-ambient-session-address")
	b, err := connectBus(context.Background(), "unix:path="+path)
	if b != nil {
		b.Close()
		t.Fatal("connected to a missing private bus")
	}
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("did not dial the supplied private address: %v", err)
	}
}

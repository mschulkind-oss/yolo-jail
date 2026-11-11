package approvalnotify

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
)

// This is the actual godbus boundary, shared by production and adapter tests.
type connection interface {
	Object(string, dbus.ObjectPath) dbus.BusObject
	Signal(chan<- *dbus.Signal)
	AddMatchSignalContext(context.Context, ...dbus.MatchOption) error
	Context() context.Context
	Close() error
}
type dbusBus struct {
	conn    connection
	signals chan *dbus.Signal
}

func connectBus(ctx context.Context, address string) (Bus, error) {
	return dialBus(ctx, address, func(address string) (connection, error) { return dbus.Connect(address) })
}
func dialBus(_ context.Context, address string, connect func(string) (connection, error)) (Bus, error) {
	if address == "" {
		return nil, errors.New("missing explicit bus address")
	}
	c, err := connect(address)
	if err != nil {
		return nil, err
	}
	return &dbusBus{conn: c, signals: make(chan *dbus.Signal, 64)}, nil
}
func (b *dbusBus) call(ctx context.Context, owner, method string, args ...any) *dbus.Call {
	return b.conn.Object(owner, notificationPath).CallWithContext(ctx, notificationInterface+"."+method, dbus.FlagNoAutoStart, args...)
}
func (b *dbusBus) Owner(ctx context.Context) (string, error) {
	var owner string
	err := b.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", dbus.FlagNoAutoStart, notificationInterface).Store(&owner)
	return owner, err
}
func (b *dbusBus) Capabilities(ctx context.Context, owner string) ([]string, error) {
	var caps []string
	err := b.call(ctx, owner, "GetCapabilities").Store(&caps)
	return caps, err
}
func (b *dbusBus) Subscribe(ctx context.Context) error {
	b.conn.Signal(b.signals)
	if err := b.conn.AddMatchSignalContext(ctx, dbus.WithMatchObjectPath(notificationPath), dbus.WithMatchInterface(notificationInterface)); err != nil {
		return err
	}
	return b.conn.AddMatchSignalContext(ctx, dbus.WithMatchObjectPath("/org/freedesktop/DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, notificationInterface))
}
func (b *dbusBus) Notify(ctx context.Context, owner string, n Notification) (uint32, error) {
	actions := n.Actions
	if actions == nil {
		actions = []string{}
	}
	// The request keeps the desktop until answered; feedback is normal and expires.
	urgency, resident, expiry := byte(2), true, int32(0)
	if n.Feedback {
		urgency, resident, expiry = 1, false, feedbackExpiryMillis
	}
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency), "resident": dbus.MakeVariant(resident)}
	var id uint32
	err := b.call(ctx, owner, "Notify", "yolo", uint32(0), "", n.Summary, n.Body, actions, hints, expiry).Store(&id)
	return id, err
}

const feedbackExpiryMillis int32 = 10000

func (b *dbusBus) CloseNotification(ctx context.Context, owner string, id uint32) error {
	if id == 0 {
		return nil
	}
	return b.call(ctx, owner, "CloseNotification", id).Err
}
func (b *dbusBus) Signals() <-chan *dbus.Signal { return b.signals }
func (b *dbusBus) Done() <-chan struct{}        { return b.conn.Context().Done() }
func (b *dbusBus) Close() error                 { return b.conn.Close() }

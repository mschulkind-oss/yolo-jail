// Package approvalnotify is an optional per-request Linux doorbell. Only the host
// approval CLI decides; notification delivery and hook output convey no authority.
package approvalnotify

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const notificationInterface = "org.freedesktop.Notifications"
const notificationPath dbus.ObjectPath = "/org/freedesktop/Notifications"
const textLimit = 4096

// Notification is the wire presentation, not an approval decision.
type Notification struct {
	Summary, Body string
	Actions       []string
}

// Bus holds one private connection for one request.
type Bus interface {
	Owner(context.Context) (string, error)
	Capabilities(context.Context, string) ([]string, error)
	Subscribe(context.Context) error
	Notify(context.Context, string, Notification) (uint32, error)
	CloseNotification(context.Context, string, uint32) error
	Signals() <-chan *dbus.Signal
	Done() <-chan struct{}
	Close() error
}

// Dependencies names the effects at the consumer boundary; the CLI runner remains
// the only decision effect, including in tests that exercise the real host store.
type Dependencies struct {
	Address, Bin string
	Dial         func(context.Context, string) (Bus, error)
	Decide       func(context.Context, string, []string) (int, string, error)
	Now          func() time.Time
	Timer        func(time.Duration) (<-chan time.Time, func())
}

var requestID = regexp.MustCompile(`^r-[0-9a-f]{32}$`)

func validate(e brokerstore.EventView, now time.Time) error {
	if e.Schema != 1 || e.Event != "request-filed" || e.Request == nil {
		return errors.New("expected a schema-1 request-filed event")
	}
	q := e.Request
	if q.Schema != 1 || !requestID.MatchString(q.ID) || q.State != "pending" || !q.Expires.After(now) {
		return errors.New("expected an unexpired pending request")
	}
	return nil
}
func plain(s string) string {
	s = termsafe.VisibleLines(s)
	if len(s) > textLimit {
		n := textLimit
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = s[:n] + "…"
	}
	return s
}
func markup(s string) string { return brokerstore.MarkupEscape(plain(s)) }
func failure(err error) error {
	return fmt.Errorf("approval notification: %w; use host `yolo approve` to inspect and answer pending requests", err)
}

// Run posts and owns one request notification until an answer or a handled stop.
func Run(ctx context.Context, e brokerstore.EventView, d Dependencies) (result error) {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	if err := validate(e, now()); err != nil {
		return failure(err)
	}
	if d.Address == "" {
		return failure(errors.New("DBUS_SESSION_BUS_ADDRESS is missing"))
	}
	if !filepath.IsAbs(d.Bin) {
		return failure(errors.New("YOLO_BIN must name an absolute executable"))
	}
	b, err := d.Dial(ctx, d.Address)
	if err != nil {
		return failure(fmt.Errorf("connect to supplied bus: %w", err))
	}
	defer b.Close()
	owner, err := b.Owner(ctx)
	if err != nil || !strings.HasPrefix(owner, ":") {
		return failure(fmt.Errorf("notification server has no owner: %v", err))
	}
	caps, err := b.Capabilities(ctx, owner)
	if err != nil {
		return failure(fmt.Errorf("capabilities: %w", err))
	}
	if !slices.Contains(caps, "actions") {
		return failure(errors.New("notification server does not support actions"))
	}
	if err = b.Subscribe(ctx); err != nil {
		return failure(fmt.Errorf("subscribe: %w", err))
	}
	q := e.Request
	id, err := b.Notify(ctx, owner, Notification{Summary: plain("yolo: " + q.Headline), Body: markup(q.Description), Actions: []string{"once", "Allow once", "15m", "Allow for 15 minutes", "deny", "Deny"}})
	if err != nil {
		return failure(fmt.Errorf("Notify: %w", err))
	}
	if id == 0 {
		return failure(errors.New("Notify returned no id"))
	}
	// Cleanup uses an independent bounded context: cancellation must not prevent
	// withdrawal, and a replacement daemon must never receive an old numeric id.
	withdraw := func() error {
		if id == 0 {
			return nil
		}
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		current, err := b.Owner(c)
		if err != nil || current != owner {
			return nil
		}
		return b.CloseNotification(c, owner, id)
	}
	defer func() {
		if err := withdraw(); err != nil && result == nil {
			result = failure(fmt.Errorf("withdraw: %w", err))
		}
	}()
	timer := d.Timer
	if timer == nil {
		timer = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTimer(d)
			return t.C, func() { t.Stop() }
		}
	}
	expiry, stop := timer(q.Expires.Sub(now()))
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-expiry:
			return nil
		case <-b.Done():
			return failure(errors.New("bus connection lost"))
		case s, ok := <-b.Signals():
			if !ok {
				return failure(errors.New("signal connection lost"))
			}
			if s == nil {
				continue
			}
			if s.Name == "org.freedesktop.DBus.NameOwnerChanged" && s.Sender == "org.freedesktop.DBus" && s.Path == "/org/freedesktop/DBus" && len(s.Body) == 3 {
				name, nok := s.Body[0].(string)
				old, ook := s.Body[1].(string)
				next, xok := s.Body[2].(string)
				if nok && ook && xok && name == notificationInterface && old == owner && next != owner {
					return failure(errors.New("notification server owner changed"))
				}
				continue
			}
			if s.Sender != owner || s.Path != notificationPath || len(s.Body) != 2 {
				continue
			}
			sid, ok := s.Body[0].(uint32)
			if !ok || sid != id {
				continue
			}
			key := ""
			switch s.Name {
			case notificationInterface + ".ActionInvoked":
				key, ok = s.Body[1].(string)
				if !ok || !slices.Contains([]string{"once", "15m", "deny"}, key) {
					continue
				}
			case notificationInterface + ".NotificationClosed":
				reason, ok := s.Body[1].(uint32)
				if !ok || reason != 2 {
					continue
				}
				key = "deny"
			default:
				continue
			}
			if ctx.Err() != nil || !q.Expires.After(now()) {
				return nil
			}
			current, err := b.Owner(ctx)
			if err != nil || current != owner {
				return failure(errors.New("notification server owner changed before answer"))
			}
			args := []string{"approve", q.ID}
			if key == "15m" {
				args = append(args, "--for", "15m")
			}
			if key == "deny" {
				args = append(args, "--deny")
			}
			status, out, err := d.Decide(ctx, d.Bin, args)
			closeErr := withdraw()
			id = 0 // feedback and programmatic close cannot become answers
			if err != nil {
				return failure(fmt.Errorf("start approval CLI: %w", err))
			}
			if status == 0 {
				if closeErr != nil {
					return failure(fmt.Errorf("withdraw: %w", closeErr))
				}
				return nil
			}
			diagnostic := fmt.Errorf("yolo approve exited %d: %s", status, plain(out))
			if status == 3 || status == 5 || status == 6 {
				body := markup(out) + "\nUse host `yolo approve` to inspect and answer pending requests."
				if _, err := b.Notify(ctx, owner, Notification{Summary: "yolo: approval not applied", Body: body}); err != nil {
					diagnostic = fmt.Errorf("%w; refusal feedback: %v", diagnostic, err)
				}
			}
			return failure(diagnostic)
		}
	}
}

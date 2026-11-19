package mcpauthdaemon

// header.go is the one-sign-in tier's header route (docs/design/mcp-auth-sharing.md §9.6; the
// plan's phase D, ID-22): an agent that takes an MCP server's Authorization header from a
// command (pi's `!command` header value) runs `yolo mcp-auth header <server>`, which asks this
// broker for the server's current access token. The broker hands over the access token only,
// issued only while unexpired, with the `header` audit event written for issuance.
// The agent keeps nothing: there is no store of its own to adopt from or write back to.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// headerTimeout is the daemon-side request wait; OAuth work belongs to the poll
// and survives this budget or the requester's disconnected connection.
var headerTimeout = 5 * time.Second
var afterHeaderWaitQueued = func() {}
var newHeaderContext = func() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), headerTimeout)
}

// HeaderRefusal is a header request this broker cannot answer, with the next step in Message.
type HeaderRefusal struct {
	Message string
	// Code is the exit status: 2 for a server this launch does not serve, 1 otherwise.
	Code int
}

func (r *HeaderRefusal) Error() string { return r.Message }

// header issues a strict snapshot, or requests expired-record work from this
// launch's persistent poll. A request never constructs a refreshing broker.
func (b *broker) header(ctx context.Context, name string) (string, error) {
	var server *mcpauth.OnceServer
	for i := range b.share.Once {
		if b.share.Once[i].Name == name {
			server = &b.share.Once[i]
		}
	}
	if server == nil {
		return "", &HeaderRefusal{Code: 2, Message: fmt.Sprintf("mcp-auth: %q is not a one-sign-in server of this launch, so no token is served for it. "+
			"Make it one in your own config with `yolo config set mcp_servers.%s.sign_in once --user`, then relaunch", name, name)}
	}
	once := &mcpauth.OnceBroker{Canonical: &mcpauth.CanonicalStore{Dir: filepath.Join(b.canonical.Dir, "once"), Filename: "records.json", LockName: "records.lock", Wait: mcpauth.HeaderReadWait}, Audit: func(e mcpauth.Event) { b.auditEvents([]mcpauth.Event{e}) }}
	token, err := once.Header(ctx, *server, nil)
	if errors.Is(err, mcpauth.ErrTokenExpired) && b.poll != nil {
		work := b.poll.request(*server)
		if work != nil {
			afterHeaderWaitQueued()
			select {
			case <-ctx.Done():
				return "", &HeaderRefusal{Code: 1, Message: fmt.Sprintf("mcp-auth: %s renewal is still pending; retry shortly or run yolo mcp-auth status", name)}
			case <-work.done:
				if work.err != nil {
					return "", &HeaderRefusal{Code: 1, Message: fmt.Sprintf("mcp-auth: %s renewal did not commit; run yolo mcp-auth status for the next step", name)}
				}
				token, err = once.Header(ctx, *server, nil)
			}
		}
	}
	switch {
	case err == nil:
		return token, nil
	default:
		next := mcpsignin.Diagnose(mcpsignin.DiagnosisInput{Server: name, Once: true, Source: "unavailable", Busy: errors.Is(err, mcpauth.ErrStoreBusy) || ctx.Err() != nil}).NextStep
		if errors.Is(err, mcpauth.ErrNotSignedIn) || errors.Is(err, mcpauth.ErrTokenExpired) {
			for _, row := range ReadOnceStatus(b.canonical.Dir, b.share, b.onceSources(), "this launch's broker", name) {
				if row["name"] == name {
					next, _ = row["next_step"].(string)
					break
				}
			}
		}
		return "", &HeaderRefusal{Code: 1, Message: "mcp-auth: " + next}
	}
}

// handleHeader answers {"op":"header","server":name}: stdout is the whole Authorization header
// value, `Bearer <token>`, and nothing else; a refusal is one stderr line.
func (b *broker) handleHeader(s *hostservice.Session) {
	raw, _ := s.Get("server")
	name, _ := raw.(string)
	formRaw, _ := s.Get("format")
	if formRaw != nil && formRaw != "bare" && formRaw != "json" {
		s.Stderr("mcp-auth: expected header format bare or json; retry with --format bare|json\n")
		s.Exit(2)
		return
	}
	ctx, cancel := newHeaderContext()
	defer cancel()
	token, err := b.header(ctx, name)
	if err != nil {
		code := 1
		var refusal *HeaderRefusal
		if errors.As(err, &refusal) {
			code = refusal.Code
		}
		s.Stderr(err.Error() + "\n")
		s.Exit(code)
		return
	}
	form, _ := formRaw.(string)
	if form == "json" {
		body, _ := json.Marshal(map[string]string{"Authorization": "Bearer " + token})
		s.Stdout(string(body) + "\n")
	} else {
		s.Stdout("Bearer " + token + "\n")
	}
	s.Exit(0)
}

// HostHeader is the header route where no broker runs for the caller: `yolo mcp-auth header` at
// `yolo host`, on the host itself. share is the host's own composition; its sources are
// not touched. The canonical store is the daemon's, so a
// sign-in any jail adopted is served here too.
func HostHeader(ctx context.Context, stateDir string, share mcpauth.Share, name string, warn func(string)) (string, error) {
	if err := share.Validate(); err != nil {
		return "", &HeaderRefusal{Code: 1, Message: "mcp-auth: " + err.Error()}
	}
	b := &broker{share: share, canonical: &mcpauth.CanonicalStore{Dir: stateDir}, warn: warn,
		audit: brokeraudit.Open(filepath.Join(paths.BrokerDir(), "audit.jsonl"), func(line string) {
			if warn != nil {
				warn(line)
			}
		})}
	return b.header(ctx, name)
}

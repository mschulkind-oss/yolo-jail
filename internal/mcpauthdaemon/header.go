package mcpauthdaemon

// header.go is the one-sign-in tier's header route (docs/design/mcp-auth-sharing.md §9.6; the
// plan's phase D, ID-22): an agent that takes an MCP server's Authorization header from a
// command (pi's `!command` header value) runs `yolo mcp-auth header <server>`, which asks this
// broker for the server's current access token. The broker hands over the access token only,
// refreshed first when under mcpauth.HeaderMargin remain, and writes the `header` audit event.
// The agent keeps nothing: there is no store of its own to adopt from or write back to.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

// headerTimeout bounds one header request: a refresh against the authorization server, which
// the token client's own 30 s timeout bounds too. pi runs the command with a 10 s timeout of its
// own and reports the failure by the server's name.
var headerTimeout = 30 * time.Second

// HeaderRefusal is a header request this broker cannot answer, with the next step in Message.
type HeaderRefusal struct {
	Message string
	// Code is the exit status: 2 for a server this launch does not serve, 1 otherwise.
	Code int
}

func (r *HeaderRefusal) Error() string { return r.Message }

// header is the access token for the one-sign-in server name, or a *HeaderRefusal naming the
// next step. A fresh OnceBroker per request: its backoff and refusal maps are not shared
// between concurrent requests, and the record's lock orders the refreshes.
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
	once, err := b.newOnceBroker()
	if err != nil {
		return "", &HeaderRefusal{Code: 1, Message: "mcp-auth: one-sign-in state is unavailable (" + err.Error() + "); repair the host state directory's permissions and retry"}
	}
	token, err := once.Header(ctx, *server, b.onceSources())
	switch {
	case err == nil:
		return token, nil
	case errors.Is(err, mcpauth.ErrNotSignedIn):
		return "", &HeaderRefusal{Code: 1, Message: fmt.Sprintf("mcp-auth: %s is not signed in yet: %s, then retry", name, b.signInStep(name))}
	case errors.Is(err, mcpauth.ErrTokenExpired):
		return "", &HeaderRefusal{Code: 1, Message: fmt.Sprintf("mcp-auth: %s: %v; retry in a minute, and `yolo audit --service mcp-auth` shows the refreshes", name, err)}
	default:
		return "", &HeaderRefusal{Code: 1, Message: fmt.Sprintf("mcp-auth: %s: %v; %s, then retry", name, err, b.signInStep(name))}
	}
}

// signInStep is how to sign name in once: each source's own login hint, or, with none in this
// launch, the agent that adopts it elsewhere.
func (b *broker) signInStep(name string) string {
	hints := make([]string, 0, len(b.share.Sources))
	for _, source := range b.share.Sources {
		hints = append(hints, strings.ReplaceAll(source.Decl.LoginHint, "{name}", name))
	}
	if len(hints) > 0 {
		return "sign in once (" + strings.Join(hints, "; or ") + ")"
	}
	return "sign in to it once in an agent yolo takes MCP sign-ins over from, in any jail or at yolo host " +
		"(for Claude Code, run /mcp in claude and authenticate " + name + ")"
}

// handleHeader answers {"op":"header","server":name}: stdout is the whole Authorization header
// value, `Bearer <token>`, and nothing else; a refusal is one stderr line.
func (b *broker) handleHeader(s *hostservice.Session) {
	raw, _ := s.Get("server")
	name, _ := raw.(string)
	ctx, cancel := context.WithTimeout(context.Background(), headerTimeout)
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
	s.Stdout("Bearer " + token + "\n")
	s.Exit(0)
}

// HostHeader is the header route where no broker runs for the caller: `yolo mcp-auth header` at
// `yolo host`, on the host itself. share is the host's own composition (no replica); its sources
// are the real home's stores a refresh writes back to. The canonical store is the daemon's, so a
// sign-in any jail adopted is served here too.
func HostHeader(ctx context.Context, stateDir string, share mcpauth.Share, name string, warn func(string)) (string, error) {
	b, err := launcherBroker(stateDir, share, warn)
	if err != nil {
		return "", &HeaderRefusal{Code: 1, Message: "mcp-auth: " + err.Error()}
	}
	return b.header(ctx, name)
}

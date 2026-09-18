package openaiauthdaemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

type HandlerConfig struct {
	Broker       openaiauth.Broker
	Upstream     Upstream
	AuthorizeURL string
	LoginTimeout time.Duration
}

// BuildHandler serves the JAIL-FACING protocol: the agent-neutral local actions, with every
// machine-wide mutation refused. "refresh" is Codex-shaped; "token" is the Pi view and
// intentionally omits refresh and ID tokens. Login streams its browser URL on stderr and emits
// one final JSON completion record on stdout.
//
// ⚠ THE SOCKET IS THE AUTHORIZATION, AND A HANDLER CANNOT SEE ITS SOCKET. hostservice says so
// in as many words — "the handler still never learns which of the three carried its bytes" —
// and `Session.JailID` falls back to the client's OWN `jail_id` on the private socket, so it is
// a jail-supplied string and can never gate anything. That is why the host-only actions live
// behind a SECOND HANDLER (BuildHostHandler) that serveSockets hands to the private 0600
// socket alone, rather than behind a check inside one handler. A `JailID`-based check would be
// the boundary asking the untrusted side which side it is.
func BuildHandler(config HandlerConfig) hostservice.Handler {
	return buildHandler(config, jailActionAllowed)
}

// BuildHostHandler serves the PRIVATE HOST SOCKET: everything the jail-facing handler serves,
// plus the machine-wide mutations (`import`, `logout`). Its authorization is the filesystem —
// mode 0600 on a host-owned path, which is the same boundary hostservice.ServeUnix's doc
// states for every host-to-host transport.
//
// Same body, different gate, so a new action cannot reach one handler and miss the other: the
// switch is shared and the two gates are the only difference. jailActionAllowed stays the
// refusing half and TestJailActionsExcludeMachineWideDestructiveOperations stays green.
func BuildHostHandler(config HandlerConfig) hostservice.Handler {
	return buildHandler(config, hostActionAllowed)
}

func buildHandler(config HandlerConfig, allowed func(string) bool) hostservice.Handler {
	return func(session *hostservice.Session) {
		action := field(session, "action")
		if action == "" {
			action = "token"
		}
		if !allowed(action) {
			session.Stderr("OpenAI credential action is unavailable from a jail: " + action + "\n")
			session.Exit(2)
			return
		}
		switch action {
		case "ping":
			_ = session.JSON(map[string]any{"pong": true, "pid": int64(os.Getpid())})
		case "token":
			view := field(session, "view")
			if view != "" && view != "access" && view != "codex" {
				session.Stderr("unknown OpenAI credential view: " + view + "\n")
				session.Exit(2)
				return
			}
			result, err := config.Broker.Refresh(context.Background(), openaiauth.Request{Caller: session.JailID})
			if err != nil {
				replyError(session, err)
				return
			}
			switch view {
			case "", "access":
				_ = session.JSON(accessView(result))
			case "codex":
				_ = session.JSON(codexView(result))
			}
		case "refresh":
			generation, err := parseGenerationMarker(field(session, "refresh_token"))
			if err != nil {
				replyError(session, err)
				return
			}
			result, err := config.Broker.Refresh(context.Background(), openaiauth.Request{
				Caller: session.JailID, CallerGeneration: generation,
			})
			if err != nil {
				replyError(session, err)
				return
			}
			_ = session.JSON(codexView(result))
		case "status":
			state, err := config.Broker.Current()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					_ = session.JSON(map[string]any{"logged_in": false})
					return
				}
				replyError(session, err)
				return
			}
			_ = session.JSON(statusView(state))
		case "login":
			flow, err := StartLogin(config.AuthorizeURL, config.Upstream)
			if err != nil {
				replyError(session, err)
				return
			}
			// Stderr frames stream immediately through openauthclient. Keep stdout
			// to one final JSON value so the framed request remains machine-readable.
			session.Stderr("Open this URL to authenticate:\n" + flow.URL + "\n")
			timeout := config.LoginTimeout
			if timeout <= 0 {
				timeout = 15 * time.Minute
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			tokens, err := flow.Wait(ctx)
			if err != nil {
				replyError(session, err)
				return
			}
			state, err := config.Broker.Replace(tokens)
			if err != nil {
				replyError(session, err)
				return
			}
			_ = session.JSON(map[string]any{"ok": true, "generation": int64(state.Generation), "account_id": state.AccountID})
		case "import":
			// The FILE the host user names, never one this daemon goes looking for: a
			// default path would make `import` a command that silently adopts whatever
			// ~/.codex/auth.json happens to hold, and the whole point of the verb is that
			// a human chose this grant.
			tokens, err := ReadCodexAuthFile(field(session, "path"))
			if err != nil {
				replyError(session, err)
				return
			}
			state, err := config.Broker.Replace(tokens)
			if err != nil {
				replyError(session, err)
				return
			}
			_ = session.JSON(map[string]any{"ok": true, "generation": int64(state.Generation),
				"account_id": state.AccountID, "expires_at": state.ExpiresAtMS})
		case "logout":
			if err := config.Broker.Logout(); err != nil {
				replyError(session, err)
				return
			}
			// IDEMPOTENT, and it reports the same thing either way: Broker.Logout treats
			// missing state as already logged out, so a second logout is a success rather
			// than an error a script has to special-case.
			_ = session.JSON(map[string]any{"ok": true, "logged_out": true})
		default:
			session.Stderr("unknown action: " + action + "\n")
			session.Exit(2)
		}
	}
}

func jailActionAllowed(action string) bool {
	switch action {
	case "ping", "token", "refresh", "status", "login":
		return true
	default:
		return false
	}
}

// hostActionAllowed is the private socket's gate: the jail's actions plus the two that mutate
// the machine-wide grant for every workspace, every jail and the host user at once.
//
// `import` INSTALLS a grant a human already has (a real Codex `auth.json`) as the next
// canonical generation; `logout` deletes the canonical state. Neither is destructive by
// accident — both take the same lock Refresh and Replace take — but both are destructive by
// DESIGN, which is why they are reachable only where the filesystem already says "host user",
// and why the caller is expected to say what it is about to do before it sends one
// (openaiauthhost's operator verb prints the consequence).
func hostActionAllowed(action string) bool {
	switch action {
	case "import", "logout":
		return true
	default:
		return jailActionAllowed(action)
	}
}

func field(session *hostservice.Session, name string) string {
	value, _ := session.Get(name)
	result, _ := value.(string)
	return result
}

func codexView(result openaiauth.Result) map[string]any {
	expires := (result.State.ExpiresAtMS - time.Now().UnixMilli()) / 1000
	if expires < 0 {
		expires = 0
	}
	return map[string]any{
		"access_token": result.State.AccessToken, "id_token": result.State.IDToken,
		"refresh_token": generationMarker(result.State.Generation), "expires_in": expires,
		"expires_at": result.State.ExpiresAtMS,
		"account_id": result.State.AccountID,
		"token_type": "Bearer", "generation": int64(result.State.Generation), "decision": string(result.Decision),
	}
}

func generationMarker(generation uint64) string {
	return "yolo-broker:" + strconv.FormatUint(generation, 10)
}

func parseGenerationMarker(marker string) (uint64, error) {
	const prefix = "yolo-broker:"
	if !strings.HasPrefix(marker, prefix) {
		return 0, errors.New("invalid OpenAI credential generation marker")
	}
	generation, err := strconv.ParseUint(strings.TrimPrefix(marker, prefix), 10, 64)
	if err != nil || generation == 0 {
		return 0, fmt.Errorf("invalid OpenAI credential generation marker")
	}
	return generation, nil
}

func accessView(result openaiauth.Result) map[string]any {
	return map[string]any{
		"access_token": result.State.AccessToken, "expires_at": result.State.ExpiresAtMS,
		"account_id": result.State.AccountID, "generation": int64(result.State.Generation), "decision": string(result.Decision),
	}
}

func statusView(state openaiauth.State) map[string]any {
	return map[string]any{
		"logged_in": true, "login_required": state.LoginRequired,
		"account_id": state.AccountID, "expires_at": state.ExpiresAtMS,
		"generation": int64(state.Generation), "last_refresh": state.LastRefreshMS,
		"last_error_code":     state.LastErrorCode,
		"access_fingerprint":  openaiauth.TokenFingerprint(state.AccessToken),
		"refresh_fingerprint": openaiauth.TokenFingerprint(state.RefreshToken),
	}
}

func replyError(session *hostservice.Session, err error) {
	code := "broker_error"
	if errors.Is(err, openaiauth.ErrLoginRequired) {
		code = "login_required"
	}
	var refreshErr *openaiauth.RefreshError
	if errors.As(err, &refreshErr) && refreshErr.Code != "" {
		code = refreshErr.Code
	}
	session.Stderr(code + ": " + err.Error() + "\n")
	_ = session.JSON(map[string]any{"error": code, "message": err.Error()})
	session.Exit(1)
}

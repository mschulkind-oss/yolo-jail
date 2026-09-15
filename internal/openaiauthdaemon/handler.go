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

// BuildHandler serves the agent-neutral local action protocol. "refresh" is
// Codex-shaped; "token" is the Pi view and intentionally omits refresh and ID
// tokens. Login streams its browser URL on stderr and emits one final JSON
// completion record on stdout.
func BuildHandler(config HandlerConfig) hostservice.Handler {
	return func(session *hostservice.Session) {
		action := field(session, "action")
		if action == "" {
			action = "token"
		}
		if !jailActionAllowed(action) {
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

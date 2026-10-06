package oauthbroker

// hostverbs.go is what `yolo claude-auth` does to the store: sign the machine out, refresh now,
// and describe the canonical login, the shared file, one credentials file and every view —
// never a token, only field names, expiries and fingerprints (TokenFP).
//
// Each runs in the verb's own process, under the same refresh.lock the daemon takes, on the
// same files the daemon re-reads before every decision, so the verb needs no socket and cannot
// disagree with a running daemon about what the store holds.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// lockedErr runs fn under refresh.lock and folds a lock failure into its error.
func lockedErr(fn func() error) error {
	var inner error
	res := withRefreshLock(func() RefreshResult {
		inner = fn()
		return nil
	})
	if res != nil {
		if msg, ok := res.Get("message"); ok {
			return fmt.Errorf("%v", msg)
		}
	}
	return inner
}

// SignOutResult is what a machine sign-out did.
type SignOutResult struct {
	HadLogin bool
	Views    int
}

// SignOut signs the machine out of Claude (`yolo claude-auth logout`): the canonical login is
// deleted, and the shared credentials file interception jails read and every registered view
// lose the account's keys, the login and the four others Claude's own logout removes with it
// (accountKeys, CL-D26), every other key kept. Nothing is revoked upstream, as
// `yolo openai-auth logout` revokes nothing: the grant is removed from this machine, which is
// what "sign the machine out" means, and a user who wants it dead at Anthropic too revokes it
// from the account (CL-D15). Needs ConfigureStore. Signing out a signed-out machine succeeds.
func SignOut() (SignOutResult, error) {
	var res SignOutResult
	if CanonicalPath == "" {
		return res, errors.New("the broker store is not configured")
	}
	err := lockedErr(func() error {
		s := storeFor(LegacyCredsPath())
		current, err := s.loadCanonicalLocked()
		if err == nil {
			res.HadLogin = hasLogin(current)
		}
		res.Views = s.signOutLocked(true)
		logInfo("sign-out: the machine was signed out by `yolo claude-auth logout` "+
			"(had_login=%v, views=%d)", res.HadLogin, res.Views)
		return nil
	})
	return res, err
}

// ForceRefresh refreshes the machine's login now, whatever its expiry, and rewrites every view:
// `yolo claude-auth refresh`, the instrument runbook measures M3, M7 and M8 turn. viewDelay,
// when positive, is held between the canonical write and the view writes, still under the
// lock, so the daemon's tick cannot close the window early: that is M7's revocation window.
// It returns the new canonical login.
func ForceRefresh(delay time.Duration) (*jsonx.OrderedMap, error) {
	if CanonicalPath == "" {
		return nil, errors.New("the broker store is not configured")
	}
	var out *jsonx.OrderedMap
	err := lockedErr(func() error {
		s := storeFor(LegacyCredsPath())
		current, err := s.loadCanonicalLocked()
		if err != nil {
			return err
		}
		rt, _ := stringField(current, "refreshToken")
		if rt == "" {
			return errors.New("the machine has no Claude login to refresh (run /login in a jail)")
		}
		resp, err := refreshUpstream(rt)
		if err != nil {
			var he *httpError
			if errors.As(err, &he) {
				return fmt.Errorf("upstream refused the refresh with HTTP %d", he.code)
			}
			return err
		}
		next := NormalizeOAuth(resp, current, rt)
		if delay > 0 {
			viewDelay = func() { time.Sleep(delay) }
			defer func() { viewDelay = nil }()
		}
		if err := s.saveLocked(next); err != nil {
			return err
		}
		recentlyReplaced.record(rt)
		logInfo("refreshed by `yolo claude-auth refresh`: rt %s -> %s, at -> %s, exp=%s",
			TokenFP(rt), fpOf(next, "refreshToken"), fpOf(next, "accessToken"), expiresAtStr(next))
		out = next
		return nil
	})
	return out, err
}

// DescribeOAuth writes one claudeAiOauth object's safe facts: its field names, whether it holds
// a refresh token, the access token's fingerprint, the expiry and what is left of it, and the
// two non-secret account fields. Never a token.
func DescribeOAuth(w io.Writer, indent string, oauth *jsonx.OrderedMap) {
	if oauth == nil || oauth.Len() == 0 {
		fmt.Fprintf(w, "%sno claudeAiOauth login\n", indent)
		return
	}
	keys := append([]string(nil), oauth.Keys()...)
	fmt.Fprintf(w, "%sfields: %s\n", indent, strings.Join(keys, ", "))
	rt, _ := stringField(oauth, "refreshToken")
	fmt.Fprintf(w, "%srefresh token: %s\n", indent, map[bool]string{true: "PRESENT (fp " + TokenFP(rt) + ")", false: "absent"}[rt != ""])
	at, _ := stringField(oauth, "accessToken")
	fmt.Fprintf(w, "%saccess token: fp %s\n", indent, TokenFP(at))
	if v, ok := oauth.Get("expiresAt"); ok {
		if ms, ok := asInt64(v); ok {
			exp := time.UnixMilli(ms)
			left := time.Until(exp).Round(time.Second)
			fmt.Fprintf(w, "%sexpires: %s (%s)\n", indent, exp.UTC().Format(time.RFC3339),
				map[bool]string{true: "in " + left.String(), false: "EXPIRED " + (-left).String() + " ago"}[left > 0])
		}
	} else {
		fmt.Fprintf(w, "%sexpires: (no expiresAt)\n", indent)
	}
	if s := scopeString(oauth); s != "" {
		fmt.Fprintf(w, "%sscopes: %s\n", indent, s)
	}
	for _, k := range []string{"subscriptionType", "rateLimitTier"} {
		if v, ok := stringField(oauth, k); ok {
			fmt.Fprintf(w, "%s%s: %s\n", indent, k, v)
		}
	}
}

// DescribeCredentialsFile writes a credentials file's safe facts (`yolo claude-auth inspect`):
// its mode and modification time, its top-level keys, and DescribeOAuth of its login. A
// symbolic link is described as one and not followed.
func DescribeCredentialsFile(w io.Writer, path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		fmt.Fprintf(w, "%s: a symbolic link to %s (not followed)\n", path, target)
		return nil
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s\n  mode %s, modified %s\n", path, fi.Mode().Perm(), fi.ModTime().UTC().Format(time.RFC3339Nano))
	decoded, err := jsonx.Decode(data)
	if err != nil {
		fmt.Fprintf(w, "  not JSON (%d bytes)\n", len(data))
		return nil
	}
	root, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		fmt.Fprintf(w, "  JSON, but not an object\n")
		return nil
	}
	keys := append([]string(nil), root.Keys()...)
	sort.Strings(keys)
	fmt.Fprintf(w, "  top-level keys: %s\n", strings.Join(keys, ", "))
	oauth, _ := oauthFromCredsBytes(data)
	DescribeOAuth(w, "  ", oauth)
	return nil
}

// DescribeStore writes the whole store's safe facts (`yolo claude-auth status`): the canonical
// login, the shared credentials file, and every registration with its view.
func DescribeStore(w io.Writer) error {
	if CanonicalPath == "" {
		return errors.New("the broker store is not configured")
	}
	fmt.Fprintf(w, "Canonical login (host-only): %s\n", CanonicalPath)
	if oauth, err := oauthFromCreds(CanonicalPath); err == nil {
		DescribeOAuth(w, "  ", oauth)
	} else if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(w, "  none: this machine is signed out, or has not migrated yet")
	} else {
		fmt.Fprintf(w, "  unreadable: %s\n", err)
	}
	legacy := LegacyCredsPath()
	fmt.Fprintf(w, "Shared credentials file (interception jails): %s\n", legacy)
	if oauth, err := oauthFromCreds(legacy); err == nil {
		DescribeOAuth(w, "  ", oauth)
	} else if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(w, "  absent")
	} else {
		fmt.Fprintf(w, "  unreadable: %s\n", err)
	}
	regs := loadRegistrations()
	fmt.Fprintf(w, "Credential views (%d registered):\n", len(regs))
	for _, r := range regs {
		state := "live"
		if r.LoggedOut {
			state = "SIGNED OUT by /logout " + r.signedOutWhere() + " until its next launch"
		}
		runtime := r.Runtime
		if r.IsHost() {
			// A host view is no workspace's: say which front door registered it, and that the
			// user's own ~/.claude is not it (CL-D27).
			runtime += " (`yolo host --`, a store yolo manages; your own ~/.claude is not this file)"
		}
		fmt.Fprintf(w, "  %s\n    runtime %s, %s\n", r.Path(), runtime, state)
		data, err := r.Read()
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintln(w, "    no view file yet")
		case err != nil:
			fmt.Fprintf(w, "    unreadable: %s\n", err)
		default:
			oauth, perr := oauthFromCredsBytes(data)
			if perr != nil {
				fmt.Fprintln(w, "    does not parse")
				continue
			}
			DescribeOAuth(w, "    ", oauth)
		}
	}
	return nil
}

package oauthbroker

// store.go is the broker's CANONICAL LOGIN and the two things it keeps in step with it: the
// legacy shared credentials file the interception path still reads, and every registered
// workspace's credential view (views.go). docs/design/claude-login-without-interception.md is
// the design; CL-D2 and CL-D9 are the decisions this file implements.
//
// # Three files, one login
//
//	canonical  <BrokerDir>/claude-credentials.json — the one record holding the refresh token.
//	           BrokerDir is host-only: no launch mounts it (CL-D2).
//	legacy     <GlobalHome>/.claude-shared-credentials/.credentials.json — what an interception
//	           jail's Claude reads through the shared_credentials symlink. Written in full,
//	           refresh token included, for as long as the interception exists (CL-D9).
//	views      <workspace>/.yolo/home/<claude>/.credentials.json per registered workspace —
//	           the access token, the real expiry, no refresh token (CL-D1).
//
// Every write of any of them happens under refresh.lock, and every reader re-reads the disk:
// the in-memory state is only the fingerprint sets of refresh.go and views.go.
//
// # Single-file mode
//
// With CanonicalPath unset, the legacy file IS the canonical, which is exactly the broker
// before this file existed. The unit tests of the refresh path run that way, and so does any
// caller that never ran ConfigureStore.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// canonicalName is the canonical login's leaf name under BrokerDir.
const canonicalName = "claude-credentials.json"

// viewRegistryName is the registrations directory's leaf name under BrokerDir.
const viewRegistryName = "claude-views"

var (
	// CanonicalPath is the canonical login. Empty means single-file mode (see above).
	CanonicalPath string
	// ViewRegistryDir holds one registration per workspace whose launch selected the view.
	// Empty means this process maintains no views.
	ViewRegistryDir string
	// RelaySourcePath is the file a broker whose canonical holds no login relays into its
	// views (CL-D6): this user's own `~/.claude/.credentials.json`, which in a jail is that
	// jail's view. It is relayed only when it carries NO refresh token (relaySource).
	RelaySourcePath string
)

// ConfigureStore points this process at the broker's host-only state: the refresh lock, the
// canonical login, the view registrations and the relay source. The daemon calls it at start,
// and so do the two host-side callers that act on the store without the daemon — a launch
// registering its workspace's view, and `yolo claude-auth`.
func ConfigureStore() {
	dir := BrokerDir()
	RefreshLockPath = filepath.Join(dir, "refresh.lock")
	CanonicalPath = filepath.Join(dir, canonicalName)
	ViewRegistryDir = filepath.Join(dir, viewRegistryName)
	if home, err := os.UserHomeDir(); err == nil {
		RelaySourcePath = filepath.Join(home, filepath.FromSlash(claudeview.ViewRel))
	}
}

// LegacyCredsPath is the shared credentials file every interception jail reads, and the
// daemon's `--creds-file` default.
func LegacyCredsPath() string { return defaultCredsPath() }

// store is the canonical/legacy pair one broker call acts on.
type store struct{ canonical, legacy string }

// storeFor returns the pair for a call handed the legacy path, which is what every entry point
// is handed (the daemon's --creds-file).
func storeFor(credsPath string) store {
	if CanonicalPath == "" || CanonicalPath == credsPath {
		return store{canonical: credsPath, legacy: credsPath}
	}
	return store{canonical: CanonicalPath, legacy: credsPath}
}

func (s store) split() bool { return s.canonical != s.legacy }

// readPath is the file an UNLOCKED reader should read: the canonical, or before the first
// migration (no canonical yet) the legacy file it will be adopted from.
func (s store) readPath() string {
	if s.split() {
		if _, err := os.Lstat(s.canonical); errors.Is(err, fs.ErrNotExist) {
			return s.legacy
		}
	}
	return s.canonical
}

// hasLogin reports whether an oauth object holds a login at all.
func hasLogin(oauth *jsonx.OrderedMap) bool {
	if oauth == nil {
		return false
	}
	at, _ := stringField(oauth, "accessToken")
	rt, _ := stringField(oauth, "refreshToken")
	return at != "" || rt != ""
}

// sameLogin reports whether two oauth objects carry the same tokens and expiry.
func sameLogin(a, b *jsonx.OrderedMap) bool {
	for _, k := range []string{"accessToken", "refreshToken"} {
		x, _ := stringField(a, k)
		y, _ := stringField(b, k)
		if x != y {
			return false
		}
	}
	return describeExpiresAt(a) == describeExpiresAt(b)
}

// syncLocked brings the canonical up to date with the legacy shared file, under refresh.lock,
// in split mode only.
//
//   - NO CANONICAL YET: the legacy file's login is adopted as the first canonical generation.
//     This is the migration (CL-D2), and it runs on the first locked operation of any process
//     that configured the store, so it needs no ordering between the launcher and the daemon.
//   - THE LEGACY FILE IS NEWER AND HOLDS A DIFFERENT LOGIN: something other than this broker
//     wrote it, and while the interception exists that is the interception path's own
//     enrollment or sign-out — the entrypoint seeding an empty shared file from a jail's real
//     one, or an interception jail's /logout. The canonical follows it (CL-D9), which keeps the
//     default path's behavior exactly what it was when the shared file was the only record.
//   - Otherwise the canonical wins, and nothing is written: the broker writes the canonical
//     first and the legacy file second, so an unchanged legacy file is never newer.
func (s store) syncLocked() {
	if !s.split() {
		return
	}
	legacyBytes, lfi, lerr := readCreds(s.legacy)
	if lerr != nil {
		if !errors.Is(lerr, fs.ErrNotExist) {
			logWarn("canonical: the shared credentials file is unreadable, not synced: %s", lerr)
		}
		return
	}
	legacy, err := oauthFromCredsBytes(legacyBytes)
	if err != nil {
		logWarn("canonical: the shared credentials file does not parse, not synced: %s", err)
		return
	}
	canonicalBytes, cfi, cerr := readCreds(s.canonical)
	if errors.Is(cerr, fs.ErrNotExist) {
		if !hasLogin(legacy) {
			return
		}
		if err := WriteTokens(s.canonical, legacy); err != nil {
			logError("canonical: could not adopt the shared credentials file: %s", err)
			return
		}
		logInfo("canonical: adopted the shared credentials file as the first canonical "+
			"generation (at=%s rt=%s exp=%s)", fpOf(legacy, "accessToken"),
			fpOf(legacy, "refreshToken"), expiresAtStr(legacy))
		return
	}
	if cerr != nil {
		logError("canonical: unreadable, not synced: %s", cerr)
		return
	}
	canonical, err := oauthFromCredsBytes(canonicalBytes)
	if err != nil {
		logError("canonical: does not parse, not synced: %s", err)
		return
	}
	if sameLogin(canonical, legacy) || !lfi.ModTime().After(cfi.ModTime()) {
		return
	}
	if !hasLogin(legacy) {
		logInfo("canonical: the shared credentials file was signed out after the canonical " +
			"was written (an interception jail's /logout); signing the machine out to match")
		s.signOutLocked(false)
		return
	}
	if err := WriteTokens(s.canonical, legacy); err != nil {
		logError("canonical: could not adopt the newer shared credentials file: %s", err)
		return
	}
	logInfo("canonical: adopted a newer login from the shared credentials file (rt %s -> %s, "+
		"at -> %s)", fpOf(canonical, "refreshToken"), fpOf(legacy, "refreshToken"),
		fpOf(legacy, "accessToken"))
	publishViewsLocked(legacy)
}

// loadCanonicalLocked returns the canonical login, synced first; an empty object when the
// machine is signed out.
func (s store) loadCanonicalLocked() (*jsonx.OrderedMap, error) {
	s.syncLocked()
	oauth, err := oauthFromCreds(s.canonical)
	if errors.Is(err, fs.ErrNotExist) && s.split() {
		return jsonx.NewOrderedMap(), nil
	}
	return oauth, err
}

// saveLocked writes a new canonical generation and everything derived from it, IN THIS ORDER:
// the canonical, then every registered view, then the legacy file (split mode, when its
// directory exists).
//
// THE VIEWS COME IMMEDIATELY AFTER THE CANONICAL, before the legacy file and before any
// bookkeeping (CL-D12). A refresh appears to revoke the access token it replaces at once rather
// than at its expiry (claude-swap#381 measured superseded tokens answering "401 OAuth access
// token has been revoked" hours early; hive-mind#2296 an outage 24 s after a host rewrite), so
// from the moment the upstream refresh returns, every view still holding the old token is an
// outage until its rename lands. The canonical goes first anyway because it alone holds the
// new refresh token: a crash between the refresh and that write loses the login outright.
//
// Only the canonical write can fail the call; the others are logged, because a stale legacy
// file or view is repaired by the next write or the next tick's pass, and a failed refresh is
// not.
func (s store) saveLocked(oauth *jsonx.OrderedMap) error {
	if err := WriteTokens(s.canonical, oauth); err != nil {
		return err
	}
	if viewDelay != nil {
		viewDelay()
	}
	publishViewsLocked(oauth)
	if onViewsPublished != nil {
		onViewsPublished()
	}
	if s.split() {
		if isDir(filepath.Dir(s.legacy)) {
			if err := WriteTokens(s.legacy, oauth); err != nil {
				logWarn("canonical: wrote the canonical, but not the shared credentials file "+
					"interception jails read: %s", err)
			}
		}
	}
	return nil
}

// viewDelay, when set, runs between the canonical write and the view writes. It is how
// the refresh subcommand's view-delay flag (yolo claude-auth) makes the revocation window of
// runbook measure M7 observable; nothing else sets it.
var viewDelay func()

// onViewsPublished, when set, runs right after the views are written and before the legacy
// file is: a test's window onto CL-D12's order, which modification times are too coarse to show.
var onViewsPublished func()

// signOutLocked signs the machine out: the canonical goes, the legacy file (when rewriteLegacy
// and it exists) becomes `{}`, and every registered view loses its claudeAiOauth entry, other
// keys kept. Each registration forgets that the broker wrote it, so the signed-out view is not
// then read as that workspace's /logout (views.go). It returns how many views it rewrote.
func (s store) signOutLocked(rewriteLegacy bool) int {
	if err := os.Remove(s.canonical); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logWarn("sign-out: could not remove the canonical login: %s", err)
	}
	if rewriteLegacy && s.split() {
		if _, err := os.Lstat(s.legacy); err == nil {
			if err := writeFileAtomic(s.legacy, []byte("{}\n"), 0o600); err != nil {
				logWarn("sign-out: could not clear the shared credentials file: %s", err)
			}
		}
	}
	return signOutViewsLocked()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

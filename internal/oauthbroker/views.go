package oauthbroker

// views.go is the broker's half of the Claude credential view
// (docs/design/claude-login-without-interception.md §5.1): the registrations a launch leaves,
// the view written for each, and the three things a jail can do to its view that the broker
// answers — nothing (the view is kept current), a /login (the view shows up carrying a refresh
// token: CL-D4, adopted as the machine's login) and a /logout (the view loses its claudeAiOauth
// entry: this workspace stops being written until its next launch, OQ-CL2).
//
// Everything here runs under refresh.lock. The file work is claudeview.Location's, which never
// follows a link a jail could have planted (CL-D3).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// viewRegistration is one workspace whose launch selected the credential view. It lives in
// ViewRegistryDir, which is under BrokerDir and host-only.
type viewRegistration struct {
	claudeview.Location
	// Runtime and Container say which launch registered it, for `yolo claude-auth status`.
	Runtime   string `json:"runtime"`
	Container string `json:"container,omitempty"`
	// Written is set once the broker has written this view a login. It is what makes a view
	// with no claudeAiOauth entry READABLE as a /logout: before the broker wrote one, an
	// absent entry is only a view nobody has filled yet.
	Written bool `json:"written"`
	// LoggedOut is set when the view lost its claudeAiOauth entry after the broker wrote it
	// one: a /logout in that workspace's jail. The broker writes it nothing more until the
	// next launch registers it again, and that launch says so.
	LoggedOut bool `json:"logged_out"`

	file string
}

// registrationFile names a location's registration: a digest, so a workspace path never has
// to be spelled as a file name.
func registrationFile(loc claudeview.Location) string {
	sum := sha256.Sum256([]byte(loc.Workspace + "\x00" + loc.Subdir))
	return filepath.Join(ViewRegistryDir, hex.EncodeToString(sum[:8])+".json")
}

func (r *viewRegistration) save() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(r.file, append(data, '\n'), 0o600)
}

func (r *viewRegistration) drop() {
	if err := os.Remove(r.file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logWarn("view: could not drop the registration for %s: %s", r.Path(), err)
	}
}

// loadRegistrations reads every registration, sorted by workspace. A registration that does
// not parse is skipped and named.
func loadRegistrations() []*viewRegistration {
	if ViewRegistryDir == "" {
		return nil
	}
	entries, err := os.ReadDir(ViewRegistryDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logWarn("view: registrations unreadable: %s", err)
		}
		return nil
	}
	var out []*viewRegistration
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") || !e.Type().IsRegular() {
			continue
		}
		p := filepath.Join(ViewRegistryDir, e.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			logWarn("view: registration %s unreadable: %s", p, err)
			continue
		}
		var r viewRegistration
		if err := json.Unmarshal(data, &r); err != nil || r.Workspace == "" || r.Subdir == "" {
			logWarn("view: registration %s does not parse; skipped", p)
			continue
		}
		r.file = p
		out = append(out, &r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Workspace < out[j].Workspace })
	return out
}

// viewState is what a view's bytes say, as far as the broker cares.
type viewState struct {
	root    *jsonx.OrderedMap // the whole file; nil when absent or not an object
	oauth   *jsonx.OrderedMap // claudeAiOauth, nil when the key is absent or null
	missing bool              // no file at all
}

// readView reads and classifies a registration's view. An error is returned only for what
// the caller must act on: claudeview.ErrDirGone (the workspace's overlay went) and
// claudeview.ErrViewIsLink (a link where the view or its directory should be).
func readView(r *viewRegistration) (viewState, error) {
	data, err := r.Read()
	if err != nil {
		if errors.Is(err, claudeview.ErrDirGone) || errors.Is(err, claudeview.ErrViewIsLink) {
			return viewState{}, err
		}
		if errors.Is(err, fs.ErrNotExist) {
			return viewState{missing: true}, nil
		}
		return viewState{}, err
	}
	decoded, err := jsonx.Decode(data)
	if err != nil {
		return viewState{}, nil // not JSON: rewritten whole
	}
	root, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return viewState{}, nil
	}
	st := viewState{root: root}
	if v, ok := root.Get("claudeAiOauth"); ok {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			st.oauth = m
		}
	}
	return st, nil
}

// viewBytes renders a view from the file's CURRENT bytes: every other top-level key kept
// (Claude keeps more than its login in this file, MCP servers' OAuth among it, CL-D13),
// claudeAiOauth replaced by the projection, or removed when projection is nil. Bytes that are
// not a JSON object are replaced whole.
func viewBytes(current []byte, projection *jsonx.OrderedMap) ([]byte, error) {
	return storeBytes(current, []string{"claudeAiOauth"}, projection)
}

// accountKeys are the top-level keys of Claude's credential store that belong to the signed-in
// account, which a machine sign-out removes from every Claude store the broker writes, the
// shared file and each view (CL-D26, docs/design/claude-login-without-interception.md).
// MEASURED in the 2.1.284 binary (search `re-login secure-storage prune`): Claude's own logout,
// on the branch that keeps the store's other logins, deletes exactly these five and keeps
// everything else (`mcpOAuth`, `pluginSecrets`, `coworkRemoteDevice` among it). Removing
// claudeAiOauth alone left a Claude Design login, with its own refresh token, the
// trusted-device token and the organization in a file every jail reads.
var accountKeys = []string{"claudeAiOauth", "organizationUuid", "trustedDeviceToken",
	"enterpriseGateway", "designOauth"}

// signedOutBytes renders a signed-out store from the file's CURRENT bytes: accountKeys
// removed, every other top-level key kept. Nothing is revoked upstream (CL-D15).
func signedOutBytes(current []byte) ([]byte, error) {
	return storeBytes(current, accountKeys, nil)
}

// storeBytes is the body viewBytes and signedOutBytes share: current's top-level keys but
// drop, then claudeAiOauth set to projection when there is one.
func storeBytes(current []byte, drop []string, projection *jsonx.OrderedMap) ([]byte, error) {
	out := jsonx.NewOrderedMap()
	if len(current) > 0 {
		if decoded, err := jsonx.Decode(current); err == nil {
			if root, ok := decoded.(*jsonx.OrderedMap); ok {
				for _, k := range root.Keys() {
					if slices.Contains(drop, k) {
						continue
					}
					v, _ := root.Get(k)
					out.Set(k, v)
				}
			}
		}
	}
	if projection != nil {
		out.Set("claudeAiOauth", projection)
	}
	s, err := jsonx.DumpsIndent(out, 2)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// linkWarned remembers the registrations whose link refusal was already logged, so a planted
// link costs one warning per broker process rather than one a minute.
var linkWarned = map[string]bool{}

// handleViewError is the common answer to readView's and writeView's two actionable errors.
func handleViewError(r *viewRegistration, err error) {
	switch {
	case errors.Is(err, claudeview.ErrDirGone):
		logInfo("view: %s no longer exists; dropping its registration", filepath.Dir(r.Path()))
		r.drop()
	case errors.Is(err, claudeview.ErrViewIsLink):
		if !linkWarned[r.file] {
			linkWarned[r.file] = true
			logWarn("view: refusing to write %s: %s", r.Path(), err)
		}
	default:
		logWarn("view: %s: %s", r.Path(), err)
	}
}

// writeView read-modify-writes one registration's view from projection, under Claude's own
// storage lock (claudeview.Location.Update), and reports whether the registration's Written
// mark has to be saved. It does NOT save it: a caller writing many views saves the marks after
// the last view, so no view waits on bookkeeping (CL-D12). A view already holding exactly these
// bytes is not rewritten, so Claude's modification-time check (F5) sees a change only when
// there is one.
func writeView(r *viewRegistration, projection *jsonx.OrderedMap) (markDirty bool) {
	wrote, locked, err := r.Update(func(current []byte) ([]byte, error) {
		return viewBytes(current, projection)
	})
	if err != nil {
		handleViewError(r, err)
		return false
	}
	if wrote {
		lockNote := ""
		if !locked {
			lockNote = " (without Claude's storage lock: it was held past the wait, or unusable)"
		}
		logInfo("view: wrote %s (at=%s exp=%s)%s", r.Path(), fpOf(projection, "accessToken"),
			expiresAtStr(projection), lockNote)
	}
	if projection != nil && !r.Written {
		r.Written = true
		return true
	}
	return false
}

// saveMarks saves the registrations writeView marked.
func saveMarks(regs []*viewRegistration) {
	for _, r := range regs {
		if err := r.save(); err != nil {
			logWarn("view: could not record the write to %s: %s", r.Path(), err)
		}
	}
}

// publishViewsLocked writes every live registration's view from a new canonical login, one
// after another with nothing between them, and only then saves the bookkeeping (CL-D12).
func publishViewsLocked(oauth *jsonx.OrderedMap) {
	projection := claudeview.Project(oauth)
	if projection == nil {
		return
	}
	var dirty []*viewRegistration
	for _, r := range loadRegistrations() {
		if r.LoggedOut {
			continue
		}
		if writeView(r, projection) {
			dirty = append(dirty, r)
		}
	}
	saveMarks(dirty)
}

// signOutViewsLocked removes the login, and the account's other keys (accountKeys), from every
// registered view, for a machine sign-out, and resets each registration so the signed-out view
// is not then read as its workspace's /logout.
func signOutViewsLocked() int {
	n := 0
	for _, r := range loadRegistrations() {
		wrote, _, err := r.Update(func(current []byte) ([]byte, error) {
			if current == nil {
				return nil, nil
			}
			return signedOutBytes(current)
		})
		if err != nil {
			handleViewError(r, err)
		} else if wrote {
			n++
		}
		if r.Written || r.LoggedOut {
			r.Written, r.LoggedOut = false, false
			if serr := r.save(); serr != nil {
				logWarn("sign-out: could not reset the registration for %s: %s", r.Path(), serr)
			}
		}
	}
	return n
}

// refusedEnrollments remembers the refresh tokens (their digests) an enrollment could not
// redeem, so one bad view costs one upstream request rather than one a minute.
var refusedEnrollments = &replacedTokens{}

// viewSource is what views are written from: the canonical login, or when it holds none, the
// relay source (CL-D6). nil when there is neither.
func viewSource(canonical *jsonx.OrderedMap) *jsonx.OrderedMap {
	if hasLogin(canonical) {
		return canonical
	}
	return relaySource()
}

// relaySource is this user's own view (RelaySourcePath) when it holds a login and NO refresh
// token: in a jail, the jail's own view, relayed into the views a nested launch registered
// (CL-D6). A file carrying a refresh token is someone's real login and is never relayed; a
// link there is refused rather than followed.
func relaySource() *jsonx.OrderedMap {
	if RelaySourcePath == "" {
		return nil
	}
	oauth, err := oauthFromCreds(RelaySourcePath)
	if err != nil || !hasLogin(oauth) {
		return nil
	}
	if rt, _ := stringField(oauth, "refreshToken"); rt != "" {
		return nil
	}
	return oauth
}

// maintainViewsLocked is the tick's pass over every registration: keep each view current,
// adopt a /login, honor a /logout, drop a registration whose workspace has gone.
func (s store) maintainViewsLocked() {
	regs := loadRegistrations()
	if len(regs) == 0 {
		return
	}
	for _, r := range regs {
		if r.LoggedOut {
			continue
		}
		s.maintainViewLocked(r)
	}
}

// maintainViewLocked is maintainViewsLocked's body for one registration.
func (s store) maintainViewLocked(r *viewRegistration) {
	st, err := readView(r)
	if err != nil {
		handleViewError(r, err)
		return
	}
	switch {
	case st.oauth != nil && nonEmpty(st.oauth, "refreshToken"):
		s.adoptEnrollmentLocked(r, st)
		return
	case !st.missing && st.root != nil && st.oauth == nil && r.Written:
		r.LoggedOut = true
		if err := r.save(); err != nil {
			logWarn("view: could not record the /logout in %s: %s", r.Path(), err)
		}
		logInfo("view: %s was signed out by /logout in its jail; the broker writes it nothing "+
			"more until that workspace's next launch", r.Path())
		return
	}
	canonical, err := oauthFromCreds(s.canonical)
	if err != nil {
		canonical = nil
	}
	if src := viewSource(canonical); src != nil {
		if writeView(r, claudeview.Project(src)) {
			saveMarks([]*viewRegistration{r})
		}
	}
}

func nonEmpty(m *jsonx.OrderedMap, key string) bool {
	v, _ := stringField(m, key)
	return v != ""
}

// adoptEnrollmentLocked answers a view that carries a refresh token: a /login in that
// workspace's jail wrote a whole credential into its view (CL-D4, M5).
//
// The token is REDEEMED ONCE, here, so the copy the jail's Claude still holds in memory is
// spent and can race nothing; the result becomes the next canonical generation, and every view
// is rewritten from it, this one included, without a refresh token. A token that is already
// the machine's, or that this broker replaced or refused, is only stripped. The inference-scope
// gate is the proxy mirror's (maybePropagateTokenResponse); its client-id gate has no input in
// a view, and the redemption is the check in its place, since it is made with Claude Code's
// client id and fails for a token issued to any other.
func (s store) adoptEnrollmentLocked(r *viewRegistration, st viewState) {
	rt, _ := stringField(st.oauth, "refreshToken")
	canonical, err := oauthFromCreds(s.canonical)
	if err != nil {
		canonical = jsonx.NewOrderedMap()
	}
	currentRT, _ := stringField(canonical, "refreshToken")
	strip := func() {
		src := viewSource(canonical)
		if src == nil {
			src = st.oauth // the jail's own login, minus its refresh token
		}
		if writeView(r, claudeview.Project(src)) {
			saveMarks([]*viewRegistration{r})
		}
	}
	if rt == currentRT || recentlyReplaced.has(rt) || refusedEnrollments.has(rt) {
		strip()
		return
	}
	if scopes := scopeString(st.oauth); scopes != "" && !HasInferenceScope(scopes) {
		logInfo("enrollment: the login in %s has no inference scope (%s); not adopted",
			r.Path(), scopes)
		refusedEnrollments.record(rt)
		strip()
		return
	}
	logInfo("enrollment: %s carries a refresh token (rt=%s), a /login in that jail; redeeming "+
		"it once as the machine's login", r.Path(), TokenFP(rt))
	resp, err := refreshUpstream(rt)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			logError("enrollment: upstream %d redeeming rt=%s from %s; not adopted, and the "+
				"view's refresh token is removed", he.code, TokenFP(rt), r.Path())
			refusedEnrollments.record(rt)
			strip()
			return
		}
		// Transient: the view keeps its token, and the next tick tries again.
		logWarn("enrollment: could not redeem rt=%s from %s: %s; retrying next tick",
			TokenFP(rt), r.Path(), err)
		return
	}
	next := NormalizeOAuth(resp, st.oauth, rt)
	if nrt, _ := stringField(next, "refreshToken"); nrt == rt {
		logWarn("enrollment: the redemption returned no new refresh token; the machine keeps " +
			"the one the jail presented")
	}
	recentlyReplaced.record(rt)
	if currentRT != "" {
		recentlyReplaced.record(currentRT)
	}
	if err := s.saveLocked(next); err != nil {
		logError("enrollment: redeemed, but the canonical write failed: %s", err)
		return
	}
	logInfo("enrollment: adopted the /login in %s as the machine's login (rt %s -> %s, at -> %s)",
		r.Path(), TokenFP(rt), fpOf(next, "refreshToken"), fpOf(next, "accessToken"))
}

// scopeString renders an oauth object's `scopes` array as the space-separated string the
// scope gate reads.
func scopeString(oauth *jsonx.OrderedMap) string {
	v, ok := oauth.Get("scopes")
	if !ok {
		return ""
	}
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// RegisterResult is what RegisterView found and did, for the launch to report.
type RegisterResult struct {
	// WasLoggedOut: this workspace's jail had run /logout since its last launch. Registering
	// it again is what signs it back in, and the launch says so (OQ-CL2).
	WasLoggedOut bool
	// RemovedLegacyLink: the view path held the shared_credentials symlink an interception
	// launch left, and it was removed so a real view could replace it.
	RemovedLegacyLink bool
	// Wrote: a login was written into the view now. False on a signed-out machine.
	Wrote bool
}

// RegisterView records that a launch selected the credential view for loc, and writes loc's
// view from the machine's login now, so the jail starts with one (CL-D3). It needs the store
// configured (ConfigureStore) and the broker's state directory to exist, which the launch's
// ensure of the singleton provides; it creates neither.
func RegisterView(loc claudeview.Location, runtime, container string) (RegisterResult, error) {
	var res RegisterResult
	if ViewRegistryDir == "" || CanonicalPath == "" {
		return res, errors.New("the broker store is not configured")
	}
	if !isDir(filepath.Dir(ViewRegistryDir)) {
		return res, fmt.Errorf("the broker's state directory %s does not exist (is the "+
			"claude-oauth-broker daemon running?)", filepath.Dir(ViewRegistryDir))
	}
	if err := os.Mkdir(ViewRegistryDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return res, err
	}
	if err := loc.EnsureDir(); err != nil {
		return res, err
	}
	var failure error
	locked := withRefreshLock(func() RefreshResult {
		r := &viewRegistration{Location: loc, Runtime: runtime, Container: container,
			file: registrationFile(loc)}
		if prev, err := os.ReadFile(r.file); err == nil {
			var old viewRegistration
			if json.Unmarshal(prev, &old) == nil {
				res.WasLoggedOut = old.LoggedOut
			}
		}
		if err := r.save(); err != nil {
			failure = err
			return nil
		}
		removed, err := loc.RemoveLegacyLink()
		if err != nil {
			failure = err
			return nil
		}
		res.RemovedLegacyLink = removed
		s := storeFor(LegacyCredsPath())
		s.syncLocked()
		s.maintainViewLocked(r)
		res.Wrote = r.Written
		return nil
	})
	if failure != nil {
		return res, failure
	}
	if locked != nil {
		if msg, isErr := locked.Get("message"); isErr {
			return res, fmt.Errorf("%v", msg)
		}
	}
	return res, nil
}

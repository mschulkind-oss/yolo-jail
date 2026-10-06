package oauthbroker

// views_test.go pins the broker's half of the Claude credential view
// (docs/design/claude-login-without-interception.md §5.1, CL-D1 to CL-D6, CL-D12, CL-D13): the
// refresh ahead of expiry that rewrites every registered view without a refresh token, a jail's
// /login adopted and redeemed once, a jail's /logout honored until the next launch, and the
// canonical login migrated out of the shared file.
//
// No real Claude and no real login: the token endpoint is an httptest server, the credential
// files are fixtures under a temp HOME, and every path the store derives from HOME is inside it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

type viewFixture struct {
	home   string
	legacy string

	mu       sync.Mutex
	redeemed []string // the refresh tokens the fake endpoint was sent, in order
	status   int      // what the fake endpoint answers; 200 unless a test says otherwise
}

// newViewFixture points the whole store at a temp HOME and the token endpoint at a fake, and
// restores every package global the store reads when the test ends.
func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	state := filepath.Join(home, "broker-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_BROKER_STATE_DIR", state)
	saved := []string{RefreshLockPath, CanonicalPath, ViewRegistryDir, RelaySourcePath}
	savedReplaced, savedRefused := recentlyReplaced, refusedEnrollments
	recentlyReplaced, refusedEnrollments = &replacedTokens{}, &replacedTokens{}
	t.Cleanup(func() {
		RefreshLockPath, CanonicalPath, ViewRegistryDir, RelaySourcePath = saved[0], saved[1], saved[2], saved[3]
		recentlyReplaced, refusedEnrollments = savedReplaced, savedRefused
	})
	ConfigureStore()
	f := &viewFixture{home: home, legacy: LegacyCredsPath(), status: http.StatusOK}
	if !strings.HasPrefix(f.legacy, home) || !strings.HasPrefix(RelaySourcePath, home) {
		t.Fatalf("the store escaped the temp HOME: legacy %s, relay %s", f.legacy, RelaySourcePath)
	}
	if err := os.MkdirAll(filepath.Dir(f.legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		f.redeemed = append(f.redeemed, req["refresh_token"])
		n, status := len(f.redeemed), f.status
		f.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT_` + strconv.Itoa(n) + `","refresh_token":"RT_` + strconv.Itoa(n) +
			`","expires_in":28800,"scope":"user:inference user:profile"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YOLO_BROKER_UPSTREAM_URL", srv.URL)
	return f
}

func (f *viewFixture) redemptions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.redeemed...)
}

// login writes a claudeAiOauth credential file at path.
func writeLogin(t *testing.T, path, at, rt string, expiresAt int64, extra map[string]any) {
	t.Helper()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", at)
	if rt != "" {
		oa.Set("refreshToken", rt)
	}
	oa.Set("expiresAt", jsonx.IntValue(expiresAt))
	oa.Set("scopes", []any{"user:inference", "user:profile"})
	oa.Set("subscriptionType", "team")
	oa.Set("rateLimitTier", "default_claude_max_5x")
	root := jsonx.NewOrderedMap()
	for k, v := range extra {
		root.Set(k, v)
	}
	root.Set("claudeAiOauth", oa)
	blob, err := jsonx.DumpsIndent(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
}

// workspace registers a fresh workspace's view the way a launch does, and returns it.
func (f *viewFixture) workspace(t *testing.T, name string) claudeview.Location {
	t.Helper()
	ws := filepath.Join(f.home, "code", name)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := claudeview.Location{Workspace: ws, Subdir: claudeview.HostSubdir("podman")}
	if _, err := RegisterView(loc, "podman", "yolo-"+name); err != nil {
		t.Fatalf("RegisterView(%s): %v", name, err)
	}
	return loc
}

// readRoot reads a credentials file as its top-level object.
func readRoot(t *testing.T, path string) *jsonx.OrderedMap {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Decode(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	root, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("%s is not an object", path)
	}
	return root
}

func oauthOf(t *testing.T, path string) *jsonx.OrderedMap {
	t.Helper()
	v, ok := readRoot(t, path).Get("claudeAiOauth")
	if !ok {
		return nil
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

func str(m *jsonx.OrderedMap, k string) string {
	if m == nil {
		return ""
	}
	s, _ := stringField(m, k)
	return s
}

// assertView checks a view holds accessToken at and carries no refresh token at all — not an
// empty one, not a marker: the key is absent (CL-D1).
func assertView(t *testing.T, loc claudeview.Location, at string) *jsonx.OrderedMap {
	t.Helper()
	oa := oauthOf(t, loc.Path())
	if oa == nil {
		t.Fatalf("%s has no claudeAiOauth", loc.Path())
	}
	if _, has := oa.Get("refreshToken"); has {
		t.Fatalf("%s carries a refreshToken key: a jail's Claude would refresh with it (F4)", loc.Path())
	}
	if got := str(oa, "accessToken"); got != at {
		t.Fatalf("%s accessToken = %q, want %q", loc.Path(), got, at)
	}
	for _, k := range []string{"expiresAt", "scopes", "subscriptionType", "rateLimitTier"} {
		if _, ok := oa.Get(k); !ok {
			t.Errorf("%s lacks %s, which Claude reads (CL-D1)", loc.Path(), k)
		}
	}
	return oa
}

func TestTheFirstLockedOperationAdoptsTheSharedFileAsTheCanonicalLogin(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, f.legacy, "AT_shared", "RT_shared", nowMS()+7*3600_000, nil)
	loc := f.workspace(t, "alpha")

	if got := str(oauthOf(t, CanonicalPath), "refreshToken"); got != "RT_shared" {
		t.Fatalf("the canonical refresh token = %q, want the shared file's (CL-D2's migration)", got)
	}
	assertView(t, loc, "AT_shared")
	if len(f.redemptions()) != 0 {
		t.Errorf("registration spent a refresh token: %v", f.redemptions())
	}
}

func TestTheBrokerRefreshesAheadOfExpiryAndRewritesEveryViewWithoutARefreshToken(t *testing.T) {
	f := newViewFixture(t)
	// Forty minutes left: outside the thirty-minute lead, so the tick leaves it alone.
	writeLogin(t, CanonicalPath, "AT_old", "RT_old", nowMS()+40*60_000, nil)
	alpha := f.workspace(t, "alpha")
	beta := f.workspace(t, "beta")
	assertView(t, alpha, "AT_old")

	if BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds) {
		t.Fatal("a tick that should not refresh reported a transient failure")
	}
	if n := len(f.redemptions()); n != 0 {
		t.Fatalf("the tick refreshed a login with forty minutes left: %d upstream calls", n)
	}

	// Twenty minutes left: inside the lead (CL-D5), so the tick refreshes once.
	writeLogin(t, CanonicalPath, "AT_old", "RT_old", nowMS()+20*60_000, nil)
	writeLogin(t, f.legacy, "AT_old", "RT_old", nowMS()+20*60_000, nil)
	// Every view before the shared file (CL-D12): a refresh revokes the old access token, so no
	// other write may stand between the refresh and the views.
	var viewsFirst bool
	onViewsPublished = func() {
		viewsFirst = str(oauthOf(t, f.legacy), "refreshToken") == "RT_old" &&
			str(oauthOf(t, alpha.Path()), "accessToken") == "AT_1"
	}
	t.Cleanup(func() { onViewsPublished = nil })
	before := nowMS()
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if got := f.redemptions(); len(got) != 1 || got[0] != "RT_old" {
		t.Fatalf("upstream was sent %v, want exactly the canonical refresh token once", got)
	}
	for _, loc := range []claudeview.Location{alpha, beta} {
		oa := assertView(t, loc, "AT_1")
		exp, _ := asInt64(func() any { v, _ := oa.Get("expiresAt"); return v }())
		if exp < before+28000_000 || exp > nowMS()+28800_000 {
			t.Errorf("%s expiresAt %d is not the real expiry (now + 8h): CL-D1 forbids a made-up one", loc.Path(), exp)
		}
	}
	if got := str(oauthOf(t, CanonicalPath), "refreshToken"); got != "RT_1" {
		t.Errorf("canonical refresh token = %q, want RT_1", got)
	}
	if got := str(oauthOf(t, f.legacy), "refreshToken"); got != "RT_1" {
		t.Errorf("the shared file interception jails read holds %q, want RT_1 (CL-D9)", got)
	}
	if !viewsFirst {
		t.Error("the views were not written before the shared file: after a refresh the views " +
			"come first, since the old access token is already revoked (CL-D12)")
	}
	if BackgroundRefreshLeadSeconds != 1800 {
		t.Errorf("BackgroundRefreshLeadSeconds = %d, want thirty minutes (CL-D5)", BackgroundRefreshLeadSeconds)
	}
}

func TestAViewCarryingARefreshTokenIsAdoptedRedeemedOnceAndStripped(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	alpha := f.workspace(t, "alpha")
	beta := f.workspace(t, "beta")

	// A /login in alpha's jail: Claude writes a whole credential, refresh token included,
	// beside an MCP server's OAuth it already had.
	mcp := jsonx.NewOrderedMap()
	mcp.Set("server|abc", map[string]any{"accessToken": "mcp-secret"})
	writeLogin(t, alpha.Path(), "AT_jail", "RT_jail", nowMS()+8*3600_000, map[string]any{"mcpOAuth": mcp})

	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if got := f.redemptions(); len(got) != 1 || got[0] != "RT_jail" {
		t.Fatalf("upstream was sent %v, want the jail's refresh token redeemed exactly once (CL-D4)", got)
	}
	if got := str(oauthOf(t, CanonicalPath), "refreshToken"); got != "RT_1" {
		t.Fatalf("the canonical refresh token = %q, want the redemption's RT_1", got)
	}
	assertView(t, alpha, "AT_1")
	assertView(t, beta, "AT_1")
	if _, ok := readRoot(t, alpha.Path()).Get("mcpOAuth"); !ok {
		t.Error("the rewrite dropped the view's mcpOAuth: a view write replaces claudeAiOauth only (CL-D13)")
	}

	// And only once: the next ticks find nothing to adopt.
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if n := len(f.redemptions()); n != 1 {
		t.Errorf("%d upstream calls after the adoption settled, want 1", n)
	}
}

func TestARefusedEnrollmentIsStrippedAndNotRetried(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	alpha := f.workspace(t, "alpha")
	writeLogin(t, alpha.Path(), "AT_jail", "RT_dead", nowMS()+8*3600_000, nil)
	f.status = http.StatusBadRequest

	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	// The jail's Claude still holds the refused token in memory, and a save of its own (an MCP
	// login, say) can write it back (CL-D13): that is not a second enrollment to try.
	writeLogin(t, alpha.Path(), "AT_jail", "RT_dead", nowMS()+8*3600_000, nil)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if got := f.redemptions(); len(got) != 1 {
		t.Fatalf("upstream was sent %v, want one attempt and no retry of a refused token", got)
	}
	assertView(t, alpha, "AT_machine")
	if got := str(oauthOf(t, CanonicalPath), "refreshToken"); got != "RT_machine" {
		t.Errorf("a refused enrollment replaced the machine's login: canonical rt = %q", got)
	}
}

func TestALoggedOutViewStopsThatWorkspacesWritesUntilTheNextLaunch(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	alpha := f.workspace(t, "alpha")
	beta := f.workspace(t, "beta")

	// /logout in alpha's jail: Claude rewrites the view without claudeAiOauth, keeping the rest.
	if err := os.WriteFile(alpha.Path(), []byte(`{"mcpOAuth": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)

	if _, err := ForceRefresh(0); err != nil {
		t.Fatal(err)
	}
	assertView(t, beta, "AT_1")
	if oa := oauthOf(t, alpha.Path()); oa != nil {
		t.Fatalf("the broker wrote alpha's view after its jail's /logout: at=%s (OQ-CL2)", TokenFP(str(oa, "accessToken")))
	}

	// The next launch registers it again, which says so and signs it back in.
	res, err := RegisterView(alpha, "podman", "yolo-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !res.WasLoggedOut {
		t.Error("the relaunch was not told this workspace had been signed out")
	}
	assertView(t, alpha, "AT_1")
}

func TestAViewTheBrokerNeverWroteIsNotReadAsALogout(t *testing.T) {
	f := newViewFixture(t)
	alpha := f.workspace(t, "alpha") // a signed-out machine: nothing is written
	if err := os.WriteFile(alpha.Path(), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	writeLogin(t, f.legacy, "AT_later", "RT_later", nowMS()+7*3600_000, nil)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	assertView(t, alpha, "AT_later")
}

func TestSignOutClearsTheMachineAndEveryViewWithoutReadingItAsALogout(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	writeLogin(t, f.legacy, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	alpha := f.workspace(t, "alpha")

	res, err := SignOut()
	if err != nil {
		t.Fatal(err)
	}
	if !res.HadLogin || res.Views != 1 {
		t.Errorf("SignOut = %+v, want a login removed from one view", res)
	}
	if _, err := os.Stat(CanonicalPath); !os.IsNotExist(err) {
		t.Errorf("the canonical login survived sign-out: %v", err)
	}
	if oa := oauthOf(t, f.legacy); oa != nil {
		t.Error("the shared file still holds a login after sign-out")
	}
	if oa := oauthOf(t, alpha.Path()); oa != nil {
		t.Error("the view still holds a login after sign-out")
	}
	// A later /login elsewhere enrolls the machine, and alpha gets it: the signed-out view
	// was not mistaken for alpha's own /logout.
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	writeLogin(t, CanonicalPath, "AT_new", "RT_new", nowMS()+7*3600_000, nil)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	assertView(t, alpha, "AT_new")
	if len(f.redemptions()) != 0 {
		t.Errorf("sign-out or its aftermath called upstream: %v (logout revokes nothing, CL-D15)", f.redemptions())
	}
}

func TestTheBrokerNeverWritesThroughALinkAJailPlantedAtAView(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	alpha := f.workspace(t, "alpha")
	outside := filepath.Join(f.home, "host-secret")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alpha.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alpha.Path()); err != nil {
		t.Fatal(err)
	}
	if _, err := ForceRefresh(0); err != nil {
		t.Fatal(err)
	}
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if got, _ := os.ReadFile(outside); string(got) != "untouched" {
		t.Fatalf("the broker wrote through a planted link: the host file now holds %q", got)
	}
	if fi, err := os.Lstat(alpha.Path()); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the planted link was replaced rather than refused: %v", err)
	}
}

func TestANestedBrokerWithNoLoginRelaysItsOwnView(t *testing.T) {
	f := newViewFixture(t)
	// This broker's own ~/.claude/.credentials.json is a VIEW (an outer jail's): no refresh token.
	writeLogin(t, RelaySourcePath, "AT_outer", "", nowMS()+7*3600_000, nil)
	inner := f.workspace(t, "nested")
	assertView(t, inner, "AT_outer")
	if _, err := os.Stat(CanonicalPath); !os.IsNotExist(err) {
		t.Error("the relay wrote a canonical login; it only relays into views (CL-D6)")
	}

	// A real login there, refresh token and all, is never relayed.
	writeLogin(t, RelaySourcePath, "AT_host_user", "RT_host_user", nowMS()+7*3600_000, nil)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if oa := oauthOf(t, inner.Path()); str(oa, "accessToken") == "AT_host_user" {
		t.Fatal("the relay copied a credential that carries a refresh token")
	}
}

func TestAViewRegisteredBeforeTheMigrationReplacesYolosOwnLinkOnly(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, f.legacy, "AT_shared", "RT_shared", nowMS()+7*3600_000, nil)
	ws := filepath.Join(f.home, "code", "old")
	dir := filepath.Join(ws, ".yolo", "home", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudeview.LegacyLinkTarget, filepath.Join(dir, claudeview.ViewFile)); err != nil {
		t.Fatal(err)
	}
	res, err := RegisterView(claudeview.Location{Workspace: ws, Subdir: "claude"}, "podman", "yolo-old")
	if err != nil {
		t.Fatal(err)
	}
	if !res.RemovedLegacyLink || !res.Wrote {
		t.Errorf("RegisterView = %+v, want the interception launch's link removed and a view written", res)
	}
	fi, err := os.Lstat(filepath.Join(dir, claudeview.ViewFile))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the view is not a regular file after registration: %v", err)
	}
}

// hostView registers a host-notch view (CL-D27) the way `yolo host -- claude` does: a directory
// yolo manages, outside every workspace, registered under runtime "host".
func (f *viewFixture) hostView(t *testing.T, pack string) claudeview.Location {
	t.Helper()
	loc := claudeview.Location{Dir: filepath.Join(f.home, ".local", "share", "yolo-jail", "host-agents", pack)}
	if _, err := RegisterView(loc, claudeview.HostRuntime, ""); err != nil {
		t.Fatalf("RegisterView(host %s): %v", pack, err)
	}
	return loc
}

// TestAHostViewIsRegisteredRefreshedAdoptedAndSignedOutLikeAWorkspaces pins CL-D27's broker half:
// a registration naming a directory rather than a workspace is written at registration with no
// refresh token, rewritten on a refresh, its /login adopted once, and its login removed at a
// machine sign-out — every path a workspace's view takes.
func TestAHostViewIsRegisteredRefreshedAdoptedAndSignedOutLikeAWorkspaces(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	host := f.hostView(t, "claude")
	alpha := f.workspace(t, "alpha")
	assertView(t, host, "AT_machine")
	if fi, err := os.Stat(host.Dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the host view's directory is %v (%v), want 0700", fi.Mode().Perm(), err)
	}
	if got := filepath.Base(registrationFile(host)); got == filepath.Base(registrationFile(alpha)) {
		t.Fatalf("a host view and a workspace's share a registration file: %s", got)
	}

	if _, err := ForceRefresh(0); err != nil {
		t.Fatal(err)
	}
	assertView(t, host, "AT_1")
	assertView(t, alpha, "AT_1")

	// /login in a `yolo host -- claude` session: Claude writes a whole credential into the view.
	writeLogin(t, host.Path(), "AT_host", "RT_host", nowMS()+8*3600_000, nil)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if got := f.redemptions(); len(got) != 2 || got[1] != "RT_host" {
		t.Fatalf("upstream was sent %v, want the host session's refresh token redeemed once", got)
	}
	assertView(t, host, "AT_2")
	assertView(t, alpha, "AT_2")

	res, err := SignOut()
	if err != nil {
		t.Fatal(err)
	}
	if res.Views != 2 {
		t.Errorf("SignOut removed the login from %d views, want both", res.Views)
	}
	if oa := oauthOf(t, host.Path()); oa != nil {
		t.Error("the host view still holds a login after sign-out")
	}
}

// A registration file written before host views existed — workspace and subdir, no dir — still
// loads, under the digest it always had; and one with neither shape is skipped.
func TestRegistrationsWrittenBeforeHostViewsStillLoad(t *testing.T) {
	newViewFixture(t)
	if err := os.MkdirAll(ViewRegistryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := claudeview.Location{Workspace: "/code/old", Subdir: "claude"}
	sum := sha256.Sum256([]byte(old.Workspace + "\x00" + old.Subdir))
	if want := filepath.Join(ViewRegistryDir, hex.EncodeToString(sum[:8])+".json"); registrationFile(old) != want {
		t.Fatalf("a workspace's registration moved: %s, want %s", registrationFile(old), want)
	}
	writeFileT(t, registrationFile(old), `{"workspace": "/code/old", "subdir": "claude", "runtime": "podman", "written": true}`)
	writeFileT(t, filepath.Join(ViewRegistryDir, "bogus.json"), `{"runtime": "podman"}`)
	writeFileT(t, filepath.Join(ViewRegistryDir, "relative.json"), `{"dir": "relative/dir"}`)
	regs := loadRegistrations()
	if len(regs) != 1 || regs[0].Workspace != "/code/old" || regs[0].IsHost() || !regs[0].Written {
		t.Fatalf("loadRegistrations = %+v, want the old workspace registration alone", regs)
	}
}

// `yolo claude-auth status` names a host view's runtime and that it is not the user's own file.
func TestClaudeAuthStatusNamesAHostView(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	host := f.hostView(t, "claude")
	var b strings.Builder
	if err := DescribeStore(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{host.Path(), "runtime host (`yolo host --`", "your own ~/.claude is not this file"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("status does not say %q:\n%s", want, b.String())
		}
	}
}

func writeFileT(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A BROKER THAT KEEPS HOST VIEWS SAYS SO, naming its own process (CL-D28): the tick that maintains
// every view, a host view included, writes the mark, so a `yolo host` launch can tell this
// daemon from one a yolo older than host views started, which skips every dir-only registration
// as unparseable and would never refresh the view the launch registers. The mark names a pid,
// so a mark another process left is never read as this one's.
func TestABrokerTickMarksThatItKeepsHostViews(t *testing.T) {
	f := newViewFixture(t)
	if HostViewsKeptBy(os.Getpid()) {
		t.Fatal("setup: a mark before any tick")
	}
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	if !HostViewsKeptBy(os.Getpid()) {
		t.Fatal("the tick did not mark this broker as keeping host views")
	}
	if HostViewsKeptBy(os.Getpid() + 1) {
		t.Error("the mark answered for a process that did not write it")
	}
	// Under the broker's state dir, beside the registrations, never a workspace's.
	if _, err := os.Stat(filepath.Join(filepath.Dir(ViewRegistryDir), hostViewsMarkName)); err != nil {
		t.Errorf("no mark beside the registrations: %v", err)
	}
	// An unconfigured store answers no, and writes nothing.
	ViewRegistryDir = ""
	if HostViewsKeptBy(os.Getpid()) {
		t.Error("an unconfigured store vouched for a broker")
	}
}

// Host views are ONE PER DIRECTORY: two packs' stores are two registrations, each named by a
// digest of its own directory, listed after every workspace's; a /logout in a host session is said
// to have run there, not in a jail; and a store whose directory was removed is dropped on the next
// tick, which is how the runbook's H9 puts one back.
func TestHostViewsAreOnePerDirectoryAndGoWithTheirDirectory(t *testing.T) {
	f := newViewFixture(t)
	writeLogin(t, CanonicalPath, "AT_machine", "RT_machine", nowMS()+7*3600_000, nil)
	alpha := f.workspace(t, "alpha")
	claude := f.hostView(t, "claude")
	other := f.hostView(t, "other")
	if registrationFile(claude) == registrationFile(other) {
		t.Fatalf("two host views share one registration file: %s", registrationFile(claude))
	}
	assertView(t, claude, "AT_machine")
	assertView(t, other, "AT_machine")
	var order []string
	for _, r := range loadRegistrations() {
		order = append(order, r.Path())
	}
	if want := []string{alpha.Path(), claude.Path(), other.Path()}; strings.Join(order, " ") != strings.Join(want, " ") {
		t.Errorf("registrations in the order %v, want the workspace's, then each host view's by directory: %v", order, want)
	}

	// /logout in the claude host session: Claude rewrites the store without claudeAiOauth.
	writeFileT(t, claude.Path(), `{"mcpOAuth": {}}`)
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	var b strings.Builder
	if err := DescribeStore(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "SIGNED OUT by /logout in a `yolo host` session until its next launch") {
		t.Errorf("status does not say the host store was signed out in a host session:\n%s", b.String())
	}

	if err := os.RemoveAll(other.Dir); err != nil {
		t.Fatal(err)
	}
	BackgroundRefreshTick(f.legacy, BackgroundRefreshLeadSeconds)
	for _, r := range loadRegistrations() {
		if r.Dir == other.Dir {
			t.Errorf("the registration of a host store whose directory was removed survived a tick: %+v", r)
		}
	}
}

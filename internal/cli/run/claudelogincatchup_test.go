package run

// claudelogincatchup_test.go pins OQ-HD9's at-launch catch-up (claudelogincatchup.go) THROUGH ITS
// CALL SITE: each test drives startLoopholesDisclosed, the spawn boundary both launch arms cross
// (startPlannedLoopholes under it), with a real spawned host daemon under the loophole's name and
// a fixture login in an isolated HOME. Deleting the call in startPlannedLoopholes fails the first
// test; calling it unconditionally fails the other two. The refresh is a fake: no test here
// reaches the network, and none starts an agent.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// No test in this package refreshes a real login: a launch that starts a claude-oauth-broker
// fixture runs the catch-up, and its production refresh would POST to the token endpoint. Every
// test runs in a HOME of its own (TestMain's testsupport.IsolateHome), so there is no login to
// find; this makes sure of it even for a test that plants one and forgets the fake.
func init() {
	claudeCatchUpRefresh = func(string, int) oauthbroker.RefreshResult {
		m := jsonx.NewOrderedMap()
		m.Set("error", "no_network_in_tests")
		return m
	}
}

// catchUpClock is the fixed instant every catch-up test judges the login against.
var catchUpClock = time.Unix(1_800_000_000, 0)

// plantSharedLogin writes the machine's shared Claude credential under the test's HOME, with
// the given life left at catchUpClock, and pins the catch-up's clock there.
func plantSharedLogin(t *testing.T, left time.Duration) {
	t.Helper()
	t.Setenv("YOLO_BROKER_STATE_DIR", "")
	p := oauthbroker.LegacyCredsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"claudeAiOauth":{"accessToken":"fixture-at","refreshToken":"fixture-rt","expiresAt":` +
		strconv.FormatInt(catchUpClock.Add(left).UnixMilli(), 10) + `}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := claudeCatchUpNow
	claudeCatchUpNow = func() time.Time { return catchUpClock }
	t.Cleanup(func() { claudeCatchUpNow = saved })
}

// fakeCatchUpRefresh counts the refreshes the launch asked for and answers each with a fresh
// eight-hour login.
func fakeCatchUpRefresh(t *testing.T) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	saved := claudeCatchUpRefresh
	claudeCatchUpRefresh = func(credsPath string, lead int) oauthbroker.RefreshResult {
		calls.Add(1)
		if credsPath != oauthbroker.LegacyCredsPath() {
			t.Errorf("the catch-up refreshed %s, not the machine's shared credential %s",
				credsPath, oauthbroker.LegacyCredsPath())
		}
		if lead != oauthbroker.BackgroundRefreshLeadSeconds {
			t.Errorf("the catch-up asked for %ds of headroom, not the broker's own lead %ds",
				lead, oauthbroker.BackgroundRefreshLeadSeconds)
		}
		m := jsonx.NewOrderedMap()
		m.Set("access_token", "new-at")
		m.Set("refresh_token", "new-rt")
		m.Set("expires_in", jsonx.IntValue(8*3600))
		m.Set("token_type", "Bearer")
		return m
	}
	t.Cleanup(func() { claudeCatchUpRefresh = saved })
	return &calls
}

// claudeBrokerFixturePack ships a loophole under the claude-oauth-broker name whose host daemon
// is a real spawned process (the -splice-child relay to a handler this test serves).
func claudeBrokerFixturePack(t *testing.T) *packload.Pack {
	t.Helper()
	sock := filepath.Join(paths.HostSingletonDir, "yj-catchup.sock")
	serveFrontedFixture(t, answersTheCheck, sock)
	cmd, err := json.Marshal([]string{os.Args[0], "-splice-child", "{socket}", sock})
	if err != nil {
		t.Fatal(err)
	}
	return writeRealLoopholePack(t, "yjtest-catchup-claude", broker.BrokerLoopholeName, `{
		"name": "`+broker.BrokerLoopholeName+`",
		"description": "a fixture under the Claude broker's name",
		"transport": "loopback-tls",
		"lifecycle": "spawned",
		"host_daemon": {"cmd": `+string(cmd)+`, "publishes": "socket", "detach": true},
		"jail_daemon": {"cmd": ["yolo-jaild", "oauth-terminator"]}
	}`)
}

// launchStartingPacks runs the spawn boundary for podman over packs and returns the launch's
// stderr, its handles' names and its refusal; every service it started is stopped at return.
func launchStartingPacks(t *testing.T, packs ...*packload.Pack) (string, []string, *servicesRefusal) {
	t.Helper()
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules(packs))
	cname := "yolo-catchup-" + strings.ReplaceAll(t.Name(), "/", "-")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.ServiceReadyTimeout = 5 * time.Second
	o.PathExists = func(string) bool { return false } // no cgroup delegate
	handles, refused := o.startLoopholesDisclosed(cname, "podman", newConfig(), packs, nil)
	var names []string
	for _, h := range handles {
		names = append(names, h.name)
		if h.stop != nil {
			h.stop()
		}
	}
	return errBuf.String(), names, refused
}

func catchUpLaunchIsolation(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("spawns host processes and binds AF_UNIX sockets")
	}
	launchCheckIsolation(t)
}

// TestALaunchStartingClaudesBrokerCatchesUpANearExpiryLogin is OQ-HD9's ruling at the launch: a
// launch that starts the claude-oauth-broker loophole, on a machine whose shared login has less
// than the broker's background lead left, refreshes it before the agent runs, and says so in one
// line naming the headroom it had.
func TestALaunchStartingClaudesBrokerCatchesUpANearExpiryLogin(t *testing.T) {
	catchUpLaunchIsolation(t)
	plantSharedLogin(t, 10*time.Minute)
	calls := fakeCatchUpRefresh(t)
	out, started, refused := launchStartingPacks(t, claudeBrokerFixturePack(t))
	if refused != nil {
		t.Fatalf("the launch was refused: %s\n%s", refused.text(), out)
	}
	if !slices.Contains(started, broker.BrokerLoopholeName) {
		t.Fatalf("the broker fixture did not start (started %v):\n%s", started, out)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the launch refreshed the login %d times, want once: the at-launch catch-up "+
			"(startPlannedLoopholes → catchUpClaudeLogin) did not run\n%s", got, out)
	}
	want := "Refreshed this machine's Claude login at launch: it had 10m left, and has 8h now."
	if !strings.Contains(out, want) {
		t.Errorf("the launch did not say it refreshed the login; want %q in:\n%s", want, out)
	}
	// THE FORENSIC LINES land in the broker's own log, where a diagnosis reads every daemon's
	// refresh lines (broker.BrokerLogPath).
	logged, err := os.ReadFile(broker.BrokerLogPath())
	if err != nil {
		t.Fatalf("the catch-up wrote nothing to the broker's log %s: %v", broker.BrokerLogPath(), err)
	}
	for _, w := range []string{"at_launch: catch-up by launch pid", "at_launch: due (600s left, lead 1800s)",
		"at_launch: ok expires_in=28800"} {
		if !strings.Contains(string(logged), w) {
			t.Errorf("the broker's log lacks the catch-up's %q:\n%s", w, logged)
		}
	}
}

// TestALaunchWithoutClaudesBrokerDoesNotCatchUp: the catch-up belongs to the launch that starts
// the Claude broker, and a launch starting other host services leaves the login alone however
// near its expiry.
func TestALaunchWithoutClaudesBrokerDoesNotCatchUp(t *testing.T) {
	catchUpLaunchIsolation(t)
	plantSharedLogin(t, 10*time.Minute)
	calls := fakeCatchUpRefresh(t)
	serveLaunchCheckDaemon(t, answersTheCheck)
	out, started, refused := launchStartingPacks(t, launchCheckPack(t))
	if refused != nil {
		t.Fatalf("the launch was refused: %s\n%s", refused.text(), out)
	}
	if !slices.Contains(started, launchCheckFixtureName) {
		t.Fatalf("the other fixture did not start (started %v):\n%s", started, out)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("a launch that started no Claude broker refreshed the login %d times\n%s", got, out)
	}
	if strings.Contains(out, "Claude login") {
		t.Errorf("a launch that started no Claude broker spoke of the login:\n%s", out)
	}
}

// TestARefusedLaunchDoesNotCatchUp: a launch the spawn boundary refuses runs no agent, so it
// refreshes nothing, even though the Claude broker started before the refusal.
func TestARefusedLaunchDoesNotCatchUp(t *testing.T) {
	catchUpLaunchIsolation(t)
	plantSharedLogin(t, 10*time.Minute)
	calls := fakeCatchUpRefresh(t)
	t.Setenv(perJailReasonChildModeEnv, "exit")
	refusing := writeRealLoopholePack(t, "yjtest-catchup-refusing", frontDoorFixtureName,
		cooperativeRefusalManifest())
	out, started, refused := launchStartingPacks(t, claudeBrokerFixturePack(t), refusing)
	if refused == nil {
		t.Fatalf("the fixture's startup refusal did not refuse the launch:\n%s", out)
	}
	if !slices.Contains(started, broker.BrokerLoopholeName) {
		t.Fatalf("the broker fixture did not start before the refusal (started %v), so this "+
			"test cannot tell a refused launch from one without the broker:\n%s", started, out)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("a refused launch refreshed the login %d times\n%s", got, out)
	}
}

// TestTheCatchUpLineNamesTheNextStep pins the one line each outcome prints: nothing for a launch
// that did nothing, the headroom (or how long ago it expired) on a refresh, and on a failure the
// step that cures it.
func TestTheCatchUpLineNamesTheNextStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  oauthbroker.CatchUp
		want []string
	}{
		{"no login", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpNoLogin}, nil},
		{"not due", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpNotDue, HeadroomMS: 7200_000}, nil},
		{"refreshed in time", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpRefreshed,
			HeadroomMS: 25 * 60_000, ExpiresInS: 8 * 3600},
			[]string{"Refreshed this machine's Claude login at launch: it had 25m left, and has 8h now."}},
		{"refreshed after expiry", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpRefreshed,
			HeadroomMS: -3 * 24 * 3600_000, ExpiresInS: 8 * 3600},
			[]string{"it had expired 3d before"}},
		{"dead login", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpFailed, HeadroomMS: -60_000,
			Error: "upstream_http", Status: 400, Detail: `{"error": "invalid_grant"}`},
			[]string{"Could not refresh", "invalid_grant", "Run /login inside Claude"}},
		{"network", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpFailed, HeadroomMS: 60_000,
			Error: "upstream_unreachable", Detail: "dial tcp: no route to host"},
			[]string{"could not be reached", "keeps retrying in the background"}},
		{"other", oauthbroker.CatchUp{Outcome: oauthbroker.CatchUpFailed, HeadroomMS: 60_000,
			Error: "upstream_http", Status: 503, Detail: "unavailable"},
			[]string{"upstream_http 503: unavailable", "yolo claude-auth refresh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeCatchUpLine(tc.res)
			if tc.want == nil {
				if got != "" {
					t.Fatalf("a catch-up that did nothing printed %q", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("line %q lacks %q", got, w)
				}
			}
		})
	}
}

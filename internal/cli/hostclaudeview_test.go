package cli

// hostclaudeview_test.go pins CL-D27 (docs/design/claude-login-without-interception.md) at the
// exec `yolo host -- claude` performs: with YOLO_CLAUDE_CREDENTIAL_VIEW=1 the launched Claude is
// pointed at a store yolo manages, which the broker's real registration fills with the machine's
// login and no refresh token, and the user's own ~/.claude is not touched; without the switch, or
// for a program that is not the Claude the view is for, nothing changes (OQ-NC7). Only the broker
// daemon's start is stood in for. Delete the hostClaudeView line in hostLaunch and the first test
// fails: the child has no CLAUDE_SECURESTORAGE_CONFIG_DIR.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

// THE SWITCH IS THE CALLER'S, and this package's host cells run as on a host that never set it,
// for TestMain's reason about YOLO_VERSION (hostdepstub_test.go): a jail launched with the view on
// hands its shell YOLO_CLAUDE_CREDENTIAL_VIEW=1 (run/claudecredentialview.go), and so does a
// developer shell that exports the opt-in, and either would turn every `yolo host -- claude` cell
// into a view cell. A view cell sets the switch itself (viewShell). And no cell ensures the REAL
// broker: one that reaches the ensure stands in for it (claudeViewHost), the way depInstallRun is
// guarded, and this guard fails the view loudly in any cell that forgot.
func init() {
	os.Unsetenv(claudeview.SwitchEnv)
	hostClaudeBrokerEnsure = func(io.Writer) error {
		return errors.New("test guard: refusing to ensure the real claude-oauth-broker; stand in " +
			"for hostClaudeBrokerEnsure in this test (claudeViewHost)")
	}
}

// viewShell is the invoking shell of a view cell: the switch as given, and no
// CLAUDE_SECURESTORAGE_CONFIG_DIR of the shell's own (a jail running this suite sets one).
func viewShell(sw string) map[string]string {
	return map[string]string{claudeview.SwitchEnv: sw, claudeview.SecureStorageEnv: ""}
}

// claudeViewHost readies a host home for a credential-view cell: the broker's store under it,
// holding the machine's login, a real ~/.claude login of the user's own, the broker's start
// stood in for, and every package global the store sets restored afterwards.
func claudeViewHost(t *testing.T) {
	t.Helper()
	saved := []string{oauthbroker.RefreshLockPath, oauthbroker.CanonicalPath,
		oauthbroker.ViewRegistryDir, oauthbroker.RelaySourcePath}
	t.Cleanup(func() {
		oauthbroker.RefreshLockPath, oauthbroker.CanonicalPath = saved[0], saved[1]
		oauthbroker.ViewRegistryDir, oauthbroker.RelaySourcePath = saved[2], saved[3]
	})
	// The broker loophole's activity is read through the lazy pack resolver, which memoizes per
	// process: each cell resolves against its own HOME.
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	t.Setenv("YOLO_BROKER_STATE_DIR", "")
	orig := hostClaudeBrokerEnsure
	hostClaudeBrokerEnsure = func(io.Writer) error {
		// What the daemon's start leaves for a registration: its state directory.
		return os.MkdirAll(oauthbroker.BrokerDir(), 0o700)
	}
	t.Cleanup(func() { hostClaudeBrokerEnsure = orig })
}

// writeClaudeLogin writes a claudeAiOauth credential file at p.
func writeClaudeLogin(t *testing.T, p, at, rt string) []byte {
	t.Helper()
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", at)
	if rt != "" {
		oa.Set("refreshToken", rt)
	}
	oa.Set("expiresAt", jsonx.IntValue(4102444800000)) // 2100: never due in a test
	oa.Set("scopes", []any{"user:inference", "user:profile"})
	oa.Set("subscriptionType", "max")
	oa.Set("rateLimitTier", "default")
	root := jsonx.NewOrderedMap()
	root.Set("claudeAiOauth", oa)
	s, err := jsonx.DumpsIndent(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, s)
	return []byte(s)
}

// claudeViewLaunch runs `yolo host -- claude` with the switch set to sw, the machine's login in
// the broker's store and the user's own in ~/.claude, and returns the child's env, the launch's
// stderr, the user's file as written before, and the home.
func claudeViewLaunch(t *testing.T, cfg, sw string) (map[string]string, string, []byte, string) {
	t.Helper()
	var userCreds []byte
	var home string
	rc, env, errs := hostGateRunIn(t, cfg, viewShell(sw), nil, "claude",
		func(h string) {
			home = h
			claudeViewHost(t)
			userCreds = writeClaudeLogin(t, filepath.Join(h, ".claude", ".credentials.json"),
				"AT_user", "RT_user")
			writeClaudeLogin(t, filepath.Join(h, ".local", "share", "yolo-jail", "state",
				"claude-oauth-broker", "claude-credentials.json"), "AT_machine", "RT_machine")
		})
	if rc != 0 || env == nil {
		t.Fatalf("yolo host -- claude rc=%d:\n%s", rc, errs)
	}
	return env, errs, userCreds, home
}

func TestHostClaudeWithTheSwitchReadsTheMachinesLoginFromAManagedStore(t *testing.T) {
	env, errs, userCreds, home := claudeViewLaunch(t, claudeAlone, "1")
	dir := env[claudeview.SecureStorageEnv]
	want := filepath.Join(home, ".local", "share", "yolo-jail", "host-agents", "claude")
	if dir != want {
		t.Fatalf("%s = %q, want the managed store %s:\n%s", claudeview.SecureStorageEnv, dir, want, errs)
	}
	view, err := os.ReadFile(filepath.Join(dir, claudeview.ViewFile))
	if err != nil {
		t.Fatalf("no view in the managed store: %v\n%s", err, errs)
	}
	if !strings.Contains(string(view), "AT_machine") || strings.Contains(string(view), "refreshToken") {
		t.Errorf("the view is not the machine's login without a refresh token:\n%s", view)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); !bytes.Equal(got, userCreds) {
		t.Errorf("the user's own ~/.claude/.credentials.json changed:\nbefore %s\nafter  %s", userCreds, got)
	}
	for _, w := range []string{"reads the machine's shared Claude login from a store yolo manages",
		"your own ~/.claude is not touched"} {
		if !strings.Contains(errs, w) {
			t.Errorf("the launch did not say %q:\n%s", w, errs)
		}
	}
}

// Off (unset or 0), host Claude keeps its own login: OQ-NC7, unchanged for every default user.
func TestHostClaudeWithoutTheSwitchKeepsItsOwnLogin(t *testing.T) {
	for _, sw := range []string{"", "0"} {
		t.Run("switch="+sw, func(t *testing.T) {
			env, errs, _, home := claudeViewLaunch(t, claudeAlone, sw)
			if v := env[claudeview.SecureStorageEnv]; v != "" {
				t.Errorf("%s=%q without the switch:\n%s", claudeview.SecureStorageEnv, v, errs)
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "share", "yolo-jail", "host-agents")); err == nil {
				t.Error("a launch without the switch made the managed store")
			}
		})
	}
}

// A program whose pack does not link Claude's credential is not the Claude the view is for.
func TestTheClaudeViewIsOnlyForClaudesOwnPack(t *testing.T) {
	saved := hostClaudeBrokerEnsure
	called := false
	t.Cleanup(func() { hostClaudeBrokerEnsure = saved })
	rc, env, errs := hostGateRunIn(t, `{"packs": ["claude", "pi"]}`,
		viewShell("1"), nil, "pi", func(string) {
			claudeViewHost(t)
			hostClaudeBrokerEnsure = func(io.Writer) error { called = true; return nil }
		})
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if env[claudeview.SecureStorageEnv] != "" || called {
		t.Errorf("pi was given Claude's credential view (env %q, broker started %v):\n%s",
			env[claudeview.SecureStorageEnv], called, errs)
	}
}

// A view that cannot be set up warns, names where to look, and changes nothing.
func TestAHostClaudeViewThatFailsWarnsAndSetsNothing(t *testing.T) {
	rc, env, errs := hostGateRunIn(t, claudeAlone, viewShell("1"), nil,
		"claude", func(string) {
			claudeViewHost(t)
			hostClaudeBrokerEnsure = func(io.Writer) error { return errors.New("no daemon here") }
		})
	if rc != 0 {
		t.Fatalf("a failed view refused the launch (rc=%d):\n%s", rc, errs)
	}
	if v := env[claudeview.SecureStorageEnv]; v != "" {
		t.Errorf("a failed view still set %s=%q", claudeview.SecureStorageEnv, v)
	}
	for _, w := range []string{"could not give claude the machine's shared Claude login (no daemon here)",
		"`yolo claude-auth status`"} {
		if !strings.Contains(errs, w) {
			t.Errorf("the warning does not say %q:\n%s", w, errs)
		}
	}
}

// A user's own CLAUDE_SECURESTORAGE_CONFIG_DIR is replaced, never duplicated: one value reaches
// the child whichever entry its libc reads first.
func TestSetEnvironReplacesEveryEntryOfTheKey(t *testing.T) {
	got := setEnviron([]string{"A=1", claudeview.SecureStorageEnv + "=/mine", "B=2",
		claudeview.SecureStorageEnv + "=/again"}, claudeview.SecureStorageEnv, "/yolo")
	if strings.Join(got, " ") != "A=1 B=2 "+claudeview.SecureStorageEnv+"=/yolo" {
		t.Errorf("setEnviron = %v", got)
	}
}

// The RESIDENT path carries the view too: a launch whose agent runs beside a launch-owned service
// (claude on the ChatGPT subscription, through the wire bridge) stays the agent's parent
// (launchservice.RunAgent) rather than exec'ing, and its child gets the same environment.
func TestHostClaudeBesideALaunchOwnedServiceCarriesTheView(t *testing.T) {
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	home := hostGateHome(t, codexConfig(upstream.URL), wcShell(viewShell("1")))
	claudeViewHost(t)
	writeClaudeLogin(t, filepath.Join(home, ".local", "share", "yolo-jail", "state",
		"claude-oauth-broker", "claude-credentials.json"), "AT_machine", "RT_machine")
	dump := filepath.Join(t.TempDir(), "env")
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "claude"), "#!/bin/sh\nenv > '"+dump+"'\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	execed := false
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { execed = true; return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"-p", "codex", "--", "claude"}, &out, &errw, false, nil); rc != 0 || execed {
		t.Fatalf("rc=%d execed=%v, want the resident path:\n%s", rc, execed, errw.String())
	}
	env, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the agent never ran (%v):\n%s", err, errw.String())
	}
	want := claudeview.SecureStorageEnv + "=" + filepath.Join(home, ".local", "share", "yolo-jail",
		"host-agents", "claude")
	if !strings.Contains(string(env), want+"\n") {
		t.Errorf("the resident agent's environment lacks %s:\n%s\n%s", want, env, errw.String())
	}
}

// The broker loophole switched off in the user's config: the switch cannot be honored, the launch
// says so and names where to look, and nothing is set or started.
func TestHostClaudeViewWithTheBrokerLoopholeOffSetsNothingAndSaysWhy(t *testing.T) {
	called := false
	rc, env, errs := hostGateRunIn(t, `{"packs": ["claude"], "loopholes": `+
		`{"claude-oauth-broker": {"enabled": false}}}`, viewShell("1"), nil, "claude", func(string) {
		claudeViewHost(t)
		hostClaudeBrokerEnsure = func(io.Writer) error { called = true; return nil }
	})
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if v := env[claudeview.SecureStorageEnv]; v != "" || called {
		t.Errorf("with the loophole off the view was set up (env %q, broker ensured %v):\n%s", v, called, errs)
	}
	if !strings.Contains(errs, "loophole is not active here") || !strings.Contains(errs, "`yolo loopholes status`") {
		t.Errorf("the launch did not say the loophole is off and where to look:\n%s", errs)
	}
}

// A machine with no shared login yet still gets the view, and the launch says the /login in this
// session is what enrolls it.
func TestHostClaudeViewOnAMachineWithNoLoginSaysLoginEnrollsIt(t *testing.T) {
	rc, env, errs := hostGateRunIn(t, claudeAlone, viewShell("1"), nil, "claude", func(string) {
		claudeViewHost(t)
	})
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if env[claudeview.SecureStorageEnv] == "" {
		t.Errorf("no view for a machine with no login yet:\n%s", errs)
	}
	if !strings.Contains(errs, "this machine has no shared Claude login yet; /login in this session enrolls the machine") {
		t.Errorf("the launch did not say /login enrolls the machine:\n%s", errs)
	}
}

// In a jail the host view is never set up: a nested `yolo host -- claude` keeps the jail's own
// Claude store. The same call outside a jail does set it, so the guard is the difference.
func TestHostClaudeViewIsANoOpInAJail(t *testing.T) {
	hostGateHome(t, claudeAlone, viewShell("1"))
	claudeViewHost(t)
	launch := composeHostLaunch("claude", "", nil, func(string) {})
	var errw bytes.Buffer
	if got := hostClaudeView(launch, []string{"A=1"}, &errw); envValue(got, claudeview.SecureStorageEnv) == "" {
		t.Fatalf("setup: outside a jail the view was not set:\n%s", errw.String())
	}
	called := false
	hostClaudeBrokerEnsure = func(io.Writer) error { called = true; return nil }
	t.Setenv("YOLO_VERSION", "test")
	errw.Reset()
	got := hostClaudeView(launch, []string{"A=1"}, &errw)
	if strings.Join(got, " ") != "A=1" || called || errw.Len() > 0 {
		t.Errorf("in a jail: env %v, broker ensured %v, said %q", got, called, errw.String())
	}
}

// fakeClaudeBroker is the host-wide claude-oauth-broker as the ensure sees it, with no process:
// its pid file and socket in a temp dir, liveness in a map, a clock the waits advance.
type fakeClaudeBroker struct {
	deps    broker.Deps
	alive   map[int]bool
	killed  []int
	spawned int
	now     time.Time
	// onSleep runs at each wait the ensure takes (a broker's first tick, say).
	onSleep func()
}

// newFakeClaudeBroker readies a broker running as pid (0: none), whose pid file is age old, with
// the broker's store configured under a temp dir. A spawn starts pid 5151.
func newFakeClaudeBroker(t *testing.T, pid int, age time.Duration) *fakeClaudeBroker {
	t.Helper()
	claudeViewHost(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("YOLO_BROKER_STATE_DIR", filepath.Join(dir, "state"))
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	oauthbroker.ConfigureStore()
	f := &fakeClaudeBroker{alive: map[int]bool{}, now: time.Now()}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	f.deps = broker.Deps{
		Name:        broker.BrokerLoopholeName,
		SocketPath:  filepath.Join(dir, "broker.sock"),
		PIDFilePath: filepath.Join(dir, "broker.pid"),
		LockPath:    filepath.Join(dir, "broker.lock"),
		LogPath:     filepath.Join(dir, "broker.log"),
		Argv:        []string{"yolo-fake-broker"},
		Now:         func() time.Time { return f.now },
		Sleep: func(d time.Duration) {
			f.now = f.now.Add(d)
			if f.onSleep != nil {
				f.onSleep()
			}
		},
		PathExists: exists,
		Reachable:  func(p string, _ time.Duration) bool { return exists(p) },
		Alive:      func(pid int) bool { return f.alive[pid] },
		Kill: func(pid int, _ syscall.Signal) error {
			f.killed = append(f.killed, pid)
			delete(f.alive, pid)
			return nil
		},
		Pgrep: func() []int { return nil },
		Spawn: func([]string, string) (int, func() bool, error) {
			f.spawned++
			f.alive[5151] = true
			writeFile(t, filepath.Join(dir, "broker.sock"), "")
			return 5151, func() bool { return false }, nil
		},
	}
	if pid != 0 {
		f.alive[pid] = true
		writeFile(t, f.deps.PIDFilePath, strconv.Itoa(pid)+"\n")
		writeFile(t, f.deps.SocketPath, "")
		then := f.now.Add(-age)
		if err := os.Chtimes(f.deps.PIDFilePath, then, then); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// markAsThisBuilds runs a tick of this build's broker in this process, which marks this
// process's pid as a broker that keeps host views (oauthbroker.HostViewsKeptBy).
func markAsThisBuilds() {
	oauthbroker.BackgroundRefreshTick(oauthbroker.LegacyCredsPath(), oauthbroker.BackgroundRefreshLeadSeconds)
}

// A BROKER OLDER THAN HOST VIEWS IS NAMED, NOT REPLACED (CL-D28): it would skip this launch's
// registration on every tick, so the view would be written once and never refreshed. The ensure
// fails with the command that replaces it (hostClaudeView turns that into its warning and sets
// nothing), and stops nothing itself: a version difference warns and never kills (HD-D2).
func TestHostClaudeNamesABrokerOlderThanHostViewsAndStopsNothing(t *testing.T) {
	f := newFakeClaudeBroker(t, 4242, time.Hour)
	var errw bytes.Buffer
	err := ensureHostViewBroker(f.deps, &errw)
	if err == nil {
		t.Fatalf("an old broker passed the ensure:\n%s", errw.String())
	}
	for _, want := range []string{"(pid 4242) was started by a yolo older than `yolo host` views",
		"`yolo host-daemon restart claude-oauth-broker` replaces it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the ensure's error does not say %q: %v", want, err)
		}
	}
	if len(f.killed) != 0 || f.spawned != 0 {
		t.Errorf("the ensure touched a shared broker: killed %v, spawned %d", f.killed, f.spawned)
	}
}

// A broker this ensure started is this build's: it is used at once, without waiting for a mark.
func TestHostClaudeUsesTheBrokerItStartedWithoutWaiting(t *testing.T) {
	f := newFakeClaudeBroker(t, 0, 0)
	var errw bytes.Buffer
	if err := ensureHostViewBroker(f.deps, &errw); err != nil || f.spawned != 1 {
		t.Errorf("ensure = %v with %d spawns, want the one it started used:\n%s", err, f.spawned, errw.String())
	}
}

// A broker of this build, which marked itself, is reused and nothing is said.
func TestHostClaudeKeepsABrokerThatKeepsHostViews(t *testing.T) {
	f := newFakeClaudeBroker(t, os.Getpid(), time.Hour)
	markAsThisBuilds()
	var errw bytes.Buffer
	if err := ensureHostViewBroker(f.deps, &errw); err != nil {
		t.Fatalf("ensure: %v\n%s", err, errw.String())
	}
	if len(f.killed) != 0 || f.spawned != 0 || errw.Len() > 0 {
		t.Errorf("a current broker was disturbed: killed %v, spawned %d, said %q", f.killed, f.spawned, errw.String())
	}
}

// A broker started moments ago has not ticked yet: the launch waits for its mark rather than
// taking it for an old one and killing a daemon another launch just started.
func TestHostClaudeWaitsForAJustStartedBrokersMark(t *testing.T) {
	f := newFakeClaudeBroker(t, os.Getpid(), 0)
	f.onSleep = markAsThisBuilds
	var errw bytes.Buffer
	if err := ensureHostViewBroker(f.deps, &errw); err != nil {
		t.Fatalf("ensure: %v\n%s", err, errw.String())
	}
	if len(f.killed) != 0 || f.spawned != 0 {
		t.Errorf("a broker still coming up was replaced: killed %v, spawned %d\n%s", f.killed, f.spawned, errw.String())
	}
}

// No broker running once the ensure is done: the view would never be refreshed, so the launch is
// told, with the command that starts one, and sets nothing (hostClaudeView's warning).
func TestHostClaudeEnsureThatStartsNoBrokerSaysHowToStartOne(t *testing.T) {
	f := newFakeClaudeBroker(t, 0, 0)
	f.deps.Spawn = func([]string, string) (int, func() bool, error) { return 0, nil, errors.New("no exec") }
	var errw bytes.Buffer
	err := ensureHostViewBroker(f.deps, &errw)
	if err == nil || !strings.Contains(err.Error(), "claude-oauth-broker is not running") ||
		!strings.Contains(err.Error(), "`yolo host-daemon restart claude-oauth-broker` starts it") {
		t.Errorf("ensure with no broker running = %v, want it said, with the command that starts one", err)
	}
}

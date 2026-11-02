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
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/claudesecurity"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

func TestHostClaudeCredentialViewAddsScopedSecurityAdapterToLaunchPath(t *testing.T) {
	oldGOOS := hostClaudeViewGOOS
	hostClaudeViewGOOS = "darwin"
	t.Cleanup(func() { hostClaudeViewGOOS = oldGOOS })

	env, errs, _, home := claudeViewLaunch(t, claudeAlone, "1")
	if !strings.Contains(errs, "passes other Keychain operations through") {
		t.Fatalf("Darwin launch did not disclose the scoped Keychain adapter:\n%s", errs)
	}
	path := filepath.SplitList(env["PATH"])
	if len(path) == 0 {
		t.Fatalf("host Claude launch has no PATH:\n%s", errs)
	}
	shim := filepath.Join(path[0], "security")
	st, err := os.Stat(shim)
	if err != nil {
		t.Fatalf("view-enabled Darwin host Claude PATH does not deliver the security adapter first; %s: %v\n%s", shim, err, errs)
	}
	if st.Mode()&0o111 == 0 {
		t.Fatalf("security adapter %s is not executable", shim)
	}
	body, err := os.ReadFile(shim)
	if err != nil || !strings.Contains(string(body), "internal claude-security view") {
		t.Fatalf("PATH's security adapter does not invoke hidden production dispatch: %v\n%s", err, body)
	}
	viewDir := filepath.Join(home, ".local", "share", "yolo-jail", "host-agents", "claude")
	if !strings.Contains(string(body), claudesecurity.ViewService(viewDir)) {
		t.Fatalf("adapter service does not match the exact managed view-store path %s: %s", viewDir, body)
	}
}

func TestHostClaudeSecurityAdapterPreservesExistingHostBlockers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := paths.HostBlockDir()
	original := []entrypoint.BlockerScript{{Name: "grep", Content: "#!/bin/sh\nexit 46\n"}}
	oldDir, err := writeHostBlockDir(root, original)
	if err != nil {
		t.Fatal(err)
	}
	path := oldDir + string(os.PathListSeparator) + "/usr/bin"
	service := claudesecurity.ViewService(filepath.Join(home, "managed"))
	newDir, err := writeHostClaudeSecurityAdapter(filepath.Join(home, "managed"), path)
	if err != nil {
		t.Fatal(err)
	}
	if newDir == oldDir {
		t.Fatal("security adapter did not produce an extended content-addressed bundle")
	}
	gotBlocker, err := os.ReadFile(filepath.Join(newDir, "grep"))
	if err != nil || string(gotBlocker) != original[0].Content {
		t.Fatalf("existing blocker was not retained exactly: %q, %v", gotBlocker, err)
	}
	gotAdapter, err := os.ReadFile(filepath.Join(newDir, "security"))
	if err != nil || !strings.Contains(string(gotAdapter), service) {
		t.Fatalf("view adapter missing its scoped service %q: %q, %v", service, gotAdapter, err)
	}
	if got := blockDirFirst(newDir, root, path); filepath.SplitList(got)[0] != newDir {
		t.Fatalf("combined bundle is not first on PATH: %q", got)
	}
}

func TestHostClaudeSecurityAdapterDoesNotOverrideExistingSecurityShim(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := paths.HostBlockDir()
	const blocked = "#!/bin/sh\nexit 1\n"
	dir, err := writeHostBlockDir(root, []entrypoint.BlockerScript{{Name: "security", Content: blocked}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeHostClaudeSecurityAdapter(filepath.Join(home, "managed"), dir); err == nil {
		t.Fatal("view adapter overrode an existing host security shim")
	}
	got, err := os.ReadFile(filepath.Join(dir, "security"))
	if err != nil || string(got) != blocked {
		t.Fatalf("existing security shim changed: %q, %v", got, err)
	}
}

func TestHostClaudeViewOffDoesNotInstallDarwinSecurityAdapter(t *testing.T) {
	oldGOOS := hostClaudeViewGOOS
	hostClaudeViewGOOS = "darwin"
	t.Cleanup(func() { hostClaudeViewGOOS = oldGOOS })
	env, errs, _, _ := claudeViewLaunch(t, claudeAlone, "0")
	if env[claudeview.SecureStorageEnv] != "" {
		t.Fatalf("view-off host Claude unexpectedly received a managed store: %q", env[claudeview.SecureStorageEnv])
	}
	assertNoHostClaudeSecurityAdapter(t, env)
	if strings.Contains(errs, "scoped security adapter") {
		t.Fatalf("view-off host Claude announced an adapter:\n%s", errs)
	}
}

func assertNoHostClaudeSecurityAdapter(t *testing.T, env map[string]string) {
	t.Helper()
	for _, dir := range filepath.SplitList(env["PATH"]) {
		body, err := os.ReadFile(filepath.Join(dir, "security"))
		if err == nil && strings.Contains(string(body), "internal claude-security view") {
			t.Fatalf("view-off or unrelated launch PATH contains the Darwin host adapter %s", filepath.Join(dir, "security"))
		}
	}
}

// A program whose pack does not link Claude's credential is not the Claude the view is for.
func TestTheClaudeViewIsOnlyForClaudesOwnPack(t *testing.T) {
	oldGOOS := hostClaudeViewGOOS
	hostClaudeViewGOOS = "darwin"
	t.Cleanup(func() { hostClaudeViewGOOS = oldGOOS })
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
	assertNoHostClaudeSecurityAdapter(t, env)
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
	home := hostGateHome(t, codexConfig(t, upstream.URL), wcShell(viewShell("1")))
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

// A user-config `enabled: false` for the broker loophole switches nothing: a pack's loophole runs
// whenever its pack is selected, so the view is set up and the broker ensured exactly as without
// the key, and the launch never says the loophole is off.
func TestHostClaudeViewIgnoresABrokerLoopholeSwitch(t *testing.T) {
	called := false
	rc, env, errs := hostGateRunIn(t, `{"packs": ["claude"], "loopholes": `+
		`{"claude-oauth-broker": {"enabled": false}}}`, viewShell("1"), nil, "claude", func(string) {
		claudeViewHost(t)
		hostClaudeBrokerEnsure = func(io.Writer) error {
			called = true
			return os.MkdirAll(oauthbroker.BrokerDir(), 0o700)
		}
	})
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if v := env[claudeview.SecureStorageEnv]; v == "" || !called {
		t.Errorf("an `enabled: false` took the view away (env %q, broker ensured %v):\n%s", v, called, errs)
	}
	if strings.Contains(errs, "loophole is not active here") {
		t.Errorf("the launch said the loophole is off:\n%s", errs)
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

// THE VIEW'S BROKER IS THIS LAUNCH'S OWN (HD-D23 in docs/design/host-daemon-ownership.md): the
// five cells that stood here drove the ensure of the machine-wide broker through fake deps (an old
// one named and left running, one this ensure started used at once, a marked one reused, a mark
// waited for, none started said). HD-R1 retired that broker, and the launch now spawns a broker of
// its own build, so none of those states can occur; the argv cell below pins the spawn, and the
// daemon's own test (oauthbroker's TestABrokerEndingWithItsLaunchExits) that it ends with the
// launch.
func TestTheHostViewBrokerIsALaunchsOwnAndEndsWithIt(t *testing.T) {
	argv := hostViewBrokerArgv("/tmp/yolo-claude-host-1-ab.sock", 4242)
	got := strings.Join(argv, " ")
	for _, want := range []string{"internal daemon " + broker.BrokerLoopholeName,
		"--socket /tmp/yolo-claude-host-1-ab.sock", "--end-with-pid 4242"} {
		if !strings.Contains(got, want) {
			t.Errorf("the host view's broker argv lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, paths.HostSingletonSocket(broker.BrokerLoopholeName)) {
		t.Errorf("the host view's broker binds the retired machine-wide socket: %s", got)
	}
}

// brokerTick runs one tick of this build's broker in this process, as the launch's own broker would.
func brokerTick() {
	oauthbroker.BackgroundRefreshTick(oauthbroker.LegacyCredsPath(), oauthbroker.BackgroundRefreshLeadSeconds)
}

// A /logout in an earlier host session signed the managed store out of the machine's login (the
// broker writes it nothing more); the next launch registers it again, says it signs the store back
// in, names the machine-wide sign-out, and the view holds the machine's login again.
func TestHostClaudeViewAfterALogoutInItsSessionSaysItSignsTheStoreBackIn(t *testing.T) {
	home := hostGateHome(t, claudeAlone, viewShell("1"))
	claudeViewHost(t)
	writeClaudeLogin(t, filepath.Join(home, ".local", "share", "yolo-jail", "state",
		"claude-oauth-broker", "claude-credentials.json"), "AT_machine", "RT_machine")
	launch := composeHostLaunch("claude", "", nil, func(string) {})
	var errw bytes.Buffer
	dir := envValue(hostClaudeView(launch, nil, &errw), claudeview.SecureStorageEnv)
	if dir == "" {
		t.Fatalf("setup: no view:\n%s", errw.String())
	}
	view := filepath.Join(dir, claudeview.ViewFile)
	// /logout in that session: Claude rewrites its store without claudeAiOauth.
	writeFile(t, view, `{"mcpOAuth": {}}`)
	brokerTick() // the broker's tick, which records the sign-out
	errw.Reset()
	if got := envValue(hostClaudeView(launch, nil, &errw), claudeview.SecureStorageEnv); got != dir {
		t.Fatalf("the relaunch's view is %q, want %q:\n%s", got, dir, errw.String())
	}
	if !strings.Contains(errw.String(), "/logout in an earlier `yolo host -- claude` had signed this store "+
		"out of the machine's login; this launch signs it back in. `yolo claude-auth logout` signs the "+
		"whole machine out") {
		t.Errorf("the relaunch did not say it signs the store back in:\n%s", errw.String())
	}
	if b, _ := os.ReadFile(view); !strings.Contains(string(b), "AT_machine") {
		t.Errorf("the store was not signed back in to the machine's login:\n%s", b)
	}
}

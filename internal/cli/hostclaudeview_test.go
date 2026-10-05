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

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

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

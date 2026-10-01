package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// When pi's auth writer fails because a running pi holds auth.json.lock, the launcher must not
// treat that as a missing login. pi holds the lock while it refreshes its own token, so its
// credential is being kept current; a browser login would be the wrong answer. The launcher
// says so in one line, leaves auth.json as it is, and starts pi.
//
// This drives the REAL generated pi launcher (npmAgentLauncher over the shipped pi pack) with a
// fake `yolo` first on PATH whose token call exits openauthclient.ExitPiAuthLockBusy. Nothing
// reaches a credential service: the environment is stripped of every YOLO_* and _YOLO_*
// variable (an inherited YOLO_AUTH_PRELAUNCH_* once sent a sibling test's calls to the real
// client), and the only prelaunch settings are the two this test sets.
func TestPiLauncherDoesNotLogInWhenARunningPiHoldsItsAuthLock(t *testing.T) {
	out := runPiLauncherAgainstBusyAuthLock(t, nil, false)
	if strings.Contains(out, "OpenAI login is required") {
		t.Errorf("a held lock was reported as a missing login:\n%s", out)
	}
}

// runPiLauncherAgainstBusyAuthLock runs the generated pi launcher with stdin as given (nil is
// /dev/null, i.e. no terminal), asserts the lock-busy contract, and returns the output. With
// loginFirst the first token call fails as a missing login does (exit 1), so a launcher at a
// terminal logs in, and the token call after the login meets the held lock.
func runPiLauncherAgainstBusyAuthLock(t *testing.T, stdin *os.File, loginFirst bool) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	requireNode(t, "the pi launcher against a busy auth lock")
	pi := shippedPiPack(t)
	var install *packdecl.Install
	installs, _ := pi.HonoredInstalls()
	for i := range installs {
		if installs[i].Bin == "pi" {
			install = &installs[i]
		}
	}
	if install == nil {
		t.Fatal("shipped pi pack has no pi program")
	}

	home := t.TempDir()
	authPath := filepath.Join(home, ".pi", "agent", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{"openai-codex":{"type":"oauth","access":"pi-refreshing","refresh":"yolo-broker:7","expires":4102444800000}}` + "\n")
	if err := os.WriteFile(authPath, existing, 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(home, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(home, "yolo-calls.log")
	// The token call fails the way the real client does when a running pi holds the lock: the
	// writer's message on stderr and the dedicated exit status. A login call succeeds, so
	// reaching it cannot be mistaken for some other failure.
	firstToken := ""
	if loginFirst {
		firstToken = "    [ \"$(wc -l < " + shellSingleQuote(calls) + ")\" -gt 1 ] || exit 1\n"
	}
	fakeYolo := "#!/usr/bin/env bash\n" +
		"printf '%s\\n' \"$*\" >> " + shellSingleQuote(calls) + "\n" +
		"case \"$*\" in\n" +
		"  *'openai-auth-client token'*)\n" + firstToken +
		"    echo 'openai-auth-client: lock Pi auth.json: held by a running pi' >&2\n" +
		"    exit " + strconv.Itoa(openauthclient.ExitPiAuthLockBusy) + " ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fakeYolo), 0o755); err != nil {
		t.Fatal(err)
	}

	prefix := filepath.Join(home, ".npm-global")
	realPi := filepath.Join(prefix, "bin", "pi")
	if err := os.MkdirAll(filepath.Dir(realPi), 0o755); err != nil {
		t.Fatal(err)
	}
	// A node script, as pi's real bin is (see TestPiCodexProfileSeedsFreshAuthBeforeNpmLauncherExec).
	if err := os.WriteFile(realPi, []byte("#!/usr/bin/env node\nconsole.log(\"PI_RAN\");\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := npmAgentLauncher(install, filepath.Join(home, "stamps"), filepath.Join(home, "receipts"), false, launcherServers{}, nil)
	launcherPath := filepath.Join(home, "launch-pi")
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(launcherPath)
	cmd.Dir = home
	cmd.Stdin = stdin
	cmd.Env = append(envWithoutYoloVars(),
		"HOME="+home, "NPM_CONFIG_PREFIX="+prefix,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"YOLO_AUTH_PRELAUNCH_PI_FLAG=--pi-auth",
		"YOLO_AUTH_PRELAUNCH_PI_PATH=.pi/agent/auth.json",
	)
	output, err := cmd.CombinedOutput()
	out := string(output)
	if err != nil {
		t.Fatalf("the launcher must start pi when a running pi holds its auth lock: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PI_RAN") {
		t.Fatalf("pi did not start:\n%s", out)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	tokenCall := "internal openai-auth-client token --pi-auth=" + authPath + "\n"
	wantCalls := tokenCall
	if loginFirst {
		wantCalls = tokenCall + "internal openai-auth-client login\n" + tokenCall
	}
	if string(log) != wantCalls {
		t.Errorf("yolo calls = %q, want %q", log, wantCalls)
	}
	for _, want := range []string{"a running pi holds its credential lock", "left the existing " + authPath + " in place"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launcher must say %q:\n%s", want, out)
		}
	}
	if got, _ := os.ReadFile(authPath); string(got) != string(existing) {
		t.Errorf("auth.json changed:\n%s", got)
	}
	return out
}

// envWithoutYoloVars is os.Environ with every YOLO_* and _YOLO_* variable removed, so the
// launcher under test sees only the prelaunch settings the test sets and cannot find a route
// to a real credential service.
func envWithoutYoloVars() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "YOLO_") || strings.HasPrefix(kv, "_YOLO_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

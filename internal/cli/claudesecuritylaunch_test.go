package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudesecurity"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// This is deliberately an executable-caller regression: hostMain composes the final PATH and
// secure-storage directory, its generated shim execs this test binary, and TestMain routes that
// subprocess through cli.Main rather than substituting claudesecurity.Run.
func TestHostClaudeGeneratedSecurityShimDispatchesFallbackAndBrokerAdoption(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the production dispatcher forwards to fixed /usr/bin/security; this fixture must never touch a real Keychain")
	}
	t.Setenv("YOLO_TEST_CLAUDE_SECURITY_DISPATCH", "1")
	oldGOOS := hostClaudeViewGOOS
	hostClaudeViewGOOS = "darwin"
	t.Cleanup(func() { hostClaudeViewGOOS = oldGOOS })

	home := hostGateHome(t, claudeAlone, viewShell("1"))
	claudeViewHost(t)
	userCreds := writeClaudeLogin(t, filepath.Join(home, ".claude", ".credentials.json"), "AT_user", "RT_user")
	serverCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"AT_adopted","refresh_token":"RT_rotated","expires_in":28800,"scope":"user:inference user:profile"}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("YOLO_BROKER_UPSTREAM_URL", server.URL)

	fakeBin := t.TempDir()
	claude := filepath.Join(fakeBin, "claude")
	writeTestExecutable(t, claude, `#!/bin/sh
set +e
service="$YOLO_TEST_EXPECTED_SERVICE"
security find-generic-password -s "$service" >/dev/null 2>&1
find_rc=$?
security delete-generic-password -s "$service" >/dev/null 2>&1
delete_rc=$?
security add-generic-password -s "$service" -a dummy -X dummy >/dev/null 2>&1
add_rc=$?
printf '%s\n' "add-generic-password -s '$service' -a dummy -X dummy" > "$YOLO_TEST_INTERACTIVE_INPUT"
cat "$YOLO_TEST_INTERACTIVE_INPUT" | security -i >/dev/null 2>&1
interactive_rc=$?
printf '%s %s %s %s\n' "$find_rc" "$delete_rc" "$add_rc" "$interactive_rc" > "$YOLO_TEST_SECURITY_RESULTS"
if [ "$find_rc" -ne 44 ] || [ "$delete_rc" -ne 44 ] || [ "$add_rc" -ne 50 ] || [ "$interactive_rc" -ne 50 ]; then exit 61; fi
mkdir -p "$CLAUDE_SECURESTORAGE_CONFIG_DIR"
printf '%s\n' '{"claudeAiOauth":{"accessToken":"AT_fixture","refreshToken":"RT_fixture","expiresAt":4102444800000,"scopes":["user:inference","user:profile"],"subscriptionType":"team"}}' > "$CLAUDE_SECURESTORAGE_CONFIG_DIR/.credentials.json"
`)
	if err := os.Chmod(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	viewDir := filepath.Join(home, ".local", "share", "yolo-jail", "host-agents", "claude")
	wantService := claudesecurity.ViewService(viewDir) // derived independently from the fixture path
	results := filepath.Join(home, "security-results")
	interactiveInput := filepath.Join(home, "security-interactive-input")
	t.Setenv("YOLO_TEST_EXPECTED_SERVICE", wantService)
	t.Setenv("YOLO_TEST_SECURITY_RESULTS", results)
	t.Setenv("YOLO_TEST_INTERACTIVE_INPUT", interactiveInput)
	var childErr string
	origExec := hostSyscallExec
	hostSyscallExec = func(path string, argv, env []string) error {
		var launchPath string
		for _, item := range env {
			if key, value, ok := strings.Cut(item, "="); ok && key == "PATH" {
				launchPath = value
				break
			}
		}
		entries := filepath.SplitList(launchPath)
		if len(entries) == 0 {
			return errors.New("launch PATH is empty; refusing to run any security operation")
		}
		shim, readErr := os.ReadFile(filepath.Join(entries[0], "security"))
		if readErr != nil || !strings.Contains(string(shim), "internal claude-security view") || !strings.Contains(string(shim), wantService) {
			return fmt.Errorf("generated view shim does not pin independently derived service %q; refusing execution: %v", wantService, readErr)
		}
		cmd := exec.Command(path, argv[1:]...)
		cmd.Env = append(env, "YOLO_TEST_CLAUDE_SECURITY_DISPATCH=1",
			"YOLO_TEST_EXPECTED_SERVICE="+wantService, "YOLO_TEST_SECURITY_RESULTS="+results,
			"YOLO_TEST_INTERACTIVE_INPUT="+interactiveInput)
		out, err := cmd.CombinedOutput()
		if err != nil {
			childErr = fmt.Sprintf("%v: %s", err, out)
		}
		return err
	}
	t.Cleanup(func() { hostSyscallExec = origExec })
	var stderr strings.Builder
	rc := hostMain([]string{"--", "claude"}, os.Stdout, &stderr, false, nil)
	if rc != 0 || childErr != "" {
		resultBytes, _ := os.ReadFile(results)
		inputBytes, _ := os.ReadFile(interactiveInput)
		t.Fatalf("hostMain/Claude fixture rc=%d child=%s security=%s stdin=%q stderr=%s", rc, childErr, resultBytes, inputBytes, stderr.String())
	}
	if got, err := os.ReadFile(results); err != nil || strings.TrimSpace(string(got)) != "44 44 50 50" {
		t.Fatalf("generated security shim results %q, err=%v; want find/delete/argv-add/stdin-add 44/44/50/50", got, err)
	}
	if got, err := os.ReadFile(interactiveInput); err != nil || string(got) != "add-generic-password -s '"+wantService+"' -a dummy -X dummy\n" {
		t.Fatalf("interactive stdin fixture %q, err=%v; want the quoted exact-service add", got, err)
	}
	fallbackPath := filepath.Join(viewDir, claudeview.ViewFile)
	fallback, err := os.ReadFile(fallbackPath)
	if err != nil || !strings.Contains(string(fallback), "RT_fixture") {
		t.Fatalf("fixture fallback was not written after the generated shim refused add: %s, %v", fallback, err)
	}
	if got, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); err != nil || string(got) != string(userCreds) {
		t.Fatalf("host launch changed the user's own credential fixture: %v", err)
	}

	// The fixture-written enrollment now takes the real broker tick/adoption path; only the
	// upstream is fake. The view must be redeemed once and republished without a refresh token.
	oauthbroker.BackgroundRefreshTick(oauthbroker.LegacyCredsPath(), oauthbroker.BackgroundRefreshLeadSeconds)
	if serverCalls != 1 {
		t.Fatalf("fixture fallback redemption calls = %d, want exactly one", serverCalls)
	}
	var canonical struct {
		ClaudeAiOauth struct {
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	canonicalBytes, err := os.ReadFile(oauthbroker.CanonicalPath)
	if err != nil || json.Unmarshal(canonicalBytes, &canonical) != nil || canonical.ClaudeAiOauth.RefreshToken != "RT_rotated" {
		t.Fatalf("broker did not adopt the dummy fixture refresh: %s, %v", canonicalBytes, err)
	}
	published, err := os.ReadFile(fallbackPath)
	if err != nil || strings.Contains(string(published), "refreshToken") || !strings.Contains(string(published), "AT_adopted") {
		t.Fatalf("broker publication is not the stripped adopted view: %s, %v", published, err)
	}
}

// Generate the account-mode adapter and Claude program launcher from the shipped Claude pack,
// then exercise both argv and stdin enrollment forms through the launcher's PATH.
func TestAccountClaudeGeneratedLauncherDispatchesFixtureFallback(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("the production dispatcher forwards to fixed /usr/bin/security; this fixture must never touch a real Keychain")
	}
	// Generating the launchers reads jail packs through the tolerant decoder; restore strict host reads for later tests.
	packload.OverrideSkewTolerance(false)
	t.Cleanup(func() { packload.OverrideSkewTolerance(false) })
	t.Setenv("YOLO_TEST_CLAUDE_SECURITY_DISPATCH", "1")
	packs := packload.Embedded()
	root, _ := packload.EmbeddedLocation()
	if len(packs) == 0 || root == "" {
		t.Fatal("shipped pack tree unavailable")
	}
	home := t.TempDir()
	// "false", never "0": the agent_updates wire is JSON, and a number is not a setting, so the
	// policy reader treats it as absent and leaves updates OPEN (config.PackPolicyDecision).
	e := entrypoint.NewEnv(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": root, entrypoint.AgentUpdatesEnv: "false",
		claudesecurity.EnvVar: claudesecurity.AccountMode,
		claudeview.SwitchEnv:  claudeview.ResolvedValue(true),
	})
	if err := entrypoint.GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	// HERMETIC BY CONSTRUCTION: the fixture's agent_updates policy must freeze the pack before the
	// launcher runs. Left open, the launcher's evergreen update re-runs this fake as
	// `claude install` outside the security adapter, and when that fails it probes the pack's real
	// installer URL to decide whether to refuse — so the verdict hung on whether the network
	// answered. Checked on the generated text, so a regression fails here without touching it.
	launcher := filepath.Join(e.LaunchDir(), "claude")
	if body, err := os.ReadFile(launcher); err != nil {
		t.Fatal(err)
	} else if !regexp.MustCompile(`(?m)^UPDATES_ENABLED='?0'?$`).Match(body) {
		t.Fatalf("generated Claude launcher %s would update, and reach its upstream, in a test: want UPDATES_ENABLED=0", launcher)
	}
	program := filepath.Join(home, ".local", "bin", "claude")
	writeTestExecutable(t, program, `#!/bin/sh
set +e
security add-generic-password -s account-dummy -a dummy -X dummy >/dev/null 2>&1
argv_rc=$?
printf '%s\n' 'add-generic-password -s account-dummy -a dummy -X dummy' | security -i >/dev/null 2>&1
stdin_rc=$?
printf '%s %s\n' "$argv_rc" "$stdin_rc" > "$YOLO_TEST_ACCOUNT_RESULTS"
if [ "$argv_rc" -ne 50 ] || [ "$stdin_rc" -ne 50 ]; then exit 62; fi
printf '%s\n' '{"dummy":"account fallback"}' > "$YOLO_TEST_ACCOUNT_VIEW"
`)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, filepath.Join(fakeBin, "yolo"), "#!/bin/sh\nexec \""+binary+"\" "+testAsYoloArg+" \"$@\"\n")
	results := filepath.Join(home, "account-results")
	fallback := filepath.Join(home, "account-view.json")
	pathEnv := strings.Join([]string{e.LaunchDir(), e.BlockDir(), filepath.Join(home, ".local", "bin"), fakeBin, "/usr/bin", "/bin"}, string(os.PathListSeparator))
	cmd := exec.Command(launcher)
	cmd.Env = []string{"HOME=" + home, "PATH=" + pathEnv, "YOLO_TEST_CLAUDE_SECURITY_DISPATCH=1",
		"YOLO_CLAUDE_SECURITY_COMPAT=account", "YOLO_TEST_ACCOUNT_RESULTS=" + results, "YOLO_TEST_ACCOUNT_VIEW=" + fallback}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated account Claude launcher failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "Updating claude") {
		t.Fatalf("generated account Claude launcher ran an update in a test:\n%s", out)
	}
	if got, err := os.ReadFile(results); err != nil || strings.TrimSpace(string(got)) != "50 50" {
		t.Fatalf("account adapter results %q, err=%v; want argv/stdin add refusal 50/50", got, err)
	}
	if got, err := os.ReadFile(fallback); err != nil || !strings.Contains(string(got), "account fallback") {
		t.Fatalf("fixture enrollment fallback not written: %s, %v", got, err)
	}
}

func writeTestExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

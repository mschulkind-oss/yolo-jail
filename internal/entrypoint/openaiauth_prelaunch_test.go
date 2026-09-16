package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestNativeLauncherPreparesDeclaredAuthView exercises the generated launcher end to end
// with fake yolo and agent binaries; no vendor agent starts and no network request is made.
//
// ⚠ ITS CONTRACT CHANGED, and the change is the point. This asserted `token, login, token`
// — the failure-to-login fallback — in a test whose stdin is not a terminal, which is the
// one context where that fallback cannot work: a login prints a URL and waits for a browser
// callback, so with no terminal it can only hang. It did, in CI: `codex --version` blocked
// for fifteen minutes on five jobs before the deadline killed them. The prelaunch now
// declines to start a browser flow it cannot finish, so the sequence here is ONE token
// attempt, no login, and the agent still runs.
//
// What that leaves untested is the interactive fallback itself (terminal present, token
// call failing), which needs a pty and a browser; `TestAuthPrelaunchDoesNotLoginWithoutATerminal`
// pins the guard that keeps anything from reaching it unattended, including a mutation check.
// The claim THIS test owns and that one does not: the agent is exec'd anyway.
func TestNativeLauncherPreparesDeclaredAuthView(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "auth.log")
	fakeYolo := `#!/bin/bash
printf '%s|bypass=%s\n' "$*" "${YOLO_BYPASS_SHIMS:-}" >> ` + shellQuoteForTest(logPath) + `
case "$*" in
  *"openai-auth-client login"*) exit 0 ;;
  *"openai-auth-client token"*) [ "$(wc -l < ` + shellQuoteForTest(logPath) + `)" -gt 1 ]; exit $? ;;
esac
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fakeYolo), 0o755); err != nil {
		t.Fatal(err)
	}
	realBin := filepath.Join(home, ".local", "bin", "probetool")
	if err := os.MkdirAll(filepath.Dir(realBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realBin, []byte("#!/bin/bash\necho AGENT_RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://example.invalid/unused"},
		filepath.Join(home, "stamps"), filepath.Join(home, "receipts.jsonl"), "",
		false, launcherServers{}, nil,
	)
	launcherPath := filepath.Join(home, "launch")
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcherPath)
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
		"YOLO_AUTH_PRELAUNCH_PROBETOOL_FLAG=--codex-auth",
		"YOLO_AUTH_PRELAUNCH_PROBETOOL_PATH=.agent/auth.json",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		log, _ := os.ReadFile(logPath)
		t.Fatalf("launcher failed: %v\n%s\nauth log:\n%s", err, out, log)
	}
	if !strings.Contains(string(out), "AGENT_RAN") {
		t.Fatalf("agent did not run after authentication:\n%s", out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantPath := "--codex-auth=" + filepath.Join(home, ".agent", "auth.json")
	if len(lines) != 1 || !strings.Contains(lines[0], "openai-auth-client token "+wantPath) {
		t.Fatalf("auth calls = %q, want exactly one token attempt: with no terminal the "+
			"prelaunch must not start a browser login it cannot finish", lines)
	}
	if !strings.Contains(string(out), "not an interactive terminal") {
		t.Errorf("the launcher must say why it did not log in, so a missing credential is "+
			"diagnosable rather than silent:\n%s", out)
	}
	for _, line := range lines {
		if !strings.HasSuffix(line, "|bypass=1") {
			t.Errorf("auth call did not bypass shims: %q", line)
		}
	}
}

func TestNativeLauncherAuthHookIsScopedToDeclaredBinary(t *testing.T) {
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://example.invalid/unused"},
		"/tmp/stamps", "/tmp/receipts", "", false, launcherServers{}, nil,
	)
	if strings.Contains(body, `YOLO_AUTH_PRELAUNCH_BIN:-}" = "codex`) {
		t.Fatal("launcher hardcodes Codex instead of matching the pack-declared binary")
	}
	if !strings.Contains(body, `auth_flag_var="YOLO_AUTH_PRELAUNCH_${auth_suffix}_FLAG"`) {
		t.Fatal("launcher does not resolve auth settings from the declared binary")
	}
}

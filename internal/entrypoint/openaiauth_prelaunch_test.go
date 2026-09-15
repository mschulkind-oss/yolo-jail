package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestNativeLauncherPreparesDeclaredAuthView exercises the generated launcher,
// including its failure-to-login fallback. It uses fake yolo and agent binaries;
// no vendor agent starts and no network request is made.
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
		"YOLO_AUTH_PRELAUNCH_BIN=probetool",
		"YOLO_AUTH_PRELAUNCH_FLAG=--codex-auth",
		"YOLO_AUTH_PRELAUNCH_PATH=.agent/auth.json",
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
	if len(lines) != 3 ||
		!strings.Contains(lines[0], "openai-auth-client token "+wantPath) ||
		!strings.Contains(lines[1], "openai-auth-client login") ||
		!strings.Contains(lines[2], "openai-auth-client token "+wantPath) {
		t.Fatalf("auth calls = %q, want token, login, token", lines)
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
	if !strings.Contains(body, `"${YOLO_AUTH_PRELAUNCH_BIN:-}" = "$BIN"`) {
		t.Fatal("launcher does not scope the auth hook to the declared binary")
	}
}

package entrypoint

// claude_codex_login_test.go pins the OpenAI login a claude jail on a bridged Codex route
// ensures before claude starts (docs/design/credential-sources-separation.md ES-D28). Only the
// codex and pi packs declared the prelaunch hook, so `yolo -p codex -- claude` in a jail never
// started the login: on a machine never logged in to OpenAI, claude started, and its first
// request failed inside the bridge (wirebridged's NewCodexResponsesHandler asks the credential
// service for an access view on every request, and there was none to give).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// The shipped claude pack's env for its codex profile opts claude's launcher into the login,
// and no other selection does. It fails if the pack's gated contribution is deleted.
func TestClaudeCodexProfileOptsIntoTheOpenAILogin(t *testing.T) {
	closure := testPacksForAgent(t, "claude")
	env := launchEnvFor(t, closure, map[string]string{"claude": "codex"}, "claude")
	if env["YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN"] != "1" {
		t.Fatalf("claude on codex: YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN = %q, want 1, so the launcher "+
			"ensures the OpenAI login the bridge's Codex route draws on", env["YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN"])
	}
	if env["YOLO_AUTH_PRELAUNCH_CLAUDE_FLAG"] != "" {
		t.Errorf("claude reads no OpenAI auth file, so no view may be written for it: FLAG = %q",
			env["YOLO_AUTH_PRELAUNCH_CLAUDE_FLAG"])
	}
	for _, profiles := range []map[string]string{nil, {"claude": "bedrock"}, {"pi": "codex"}} {
		if got := launchEnvFor(t, closure, profiles, "claude")["YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN"]; got != "" {
			t.Errorf("profiles %v: claude's launcher opts into the OpenAI login (%q) with no Codex route", profiles, got)
		}
	}
}

// The shipped claude program's launcher, run with that env, makes one access-view token call
// (no view written, no flag) before claude runs, and still runs claude when the call fails
// with no terminal to log in from. A fake yolo and a fake claude; no agent, no network.
func TestClaudeLauncherEnsuresTheOpenAILoginOnCodex(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	closure := testPacksForAgent(t, "claude")
	var install *packdecl.Install
	for _, p := range closure {
		installs, _ := p.HonoredInstalls()
		for i := range installs {
			if installs[i].Bin == "claude" {
				install = &installs[i]
			}
		}
	}
	if install == nil {
		t.Fatal("the shipped claude pack installs no claude program")
	}
	for _, tc := range []struct {
		name        string
		tokenExit   int
		wantMessage string
	}{
		{"logged in", 0, ""},
		{"never logged in", 1, "OpenAI login is required, and this is not an interactive terminal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			binDir := filepath.Join(home, "fake-bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(home, "auth.log")
			fakeYolo := "#!/bin/bash\nprintf '%s|bypass=%s\\n' \"$*\" \"${YOLO_BYPASS_SHIMS:-}\" >> " +
				shellQuoteForTest(logPath) + "\nexit " + map[int]string{0: "0", 1: "1"}[tc.tokenExit] + "\n"
			if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fakeYolo), 0o755); err != nil {
				t.Fatal(err)
			}
			realBin := filepath.Join(home, ".local", "bin", "claude")
			if err := os.MkdirAll(filepath.Dir(realBin), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(realBin, []byte("#!/bin/bash\necho CLAUDE_RAN\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			launcher := nativeAgentLauncher("probe", install, filepath.Join(home, "stamps"),
				filepath.Join(home, "receipts.jsonl"), "", false, launcherServers{}, nil)
			launcherPath := filepath.Join(home, "launch-claude")
			if err := os.WriteFile(launcherPath, []byte(launcher), 0o755); err != nil {
				t.Fatal(err)
			}
			// Not `--version`: the shipped claude pack declares it a version probe, which runs no
			// authentication step (probeargs.go); TestAVersionProbeRunsNoUpdateStep pins that.
			cmd := exec.Command(launcherPath, "-p", "hello")
			cmd.Env = append(launcherHermeticEnv(), "HOME="+home,
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			for k, v := range launchEnvFor(t, closure, map[string]string{"claude": "codex"}, "claude") {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "CLAUDE_RAN") {
				t.Fatalf("claude's launcher did not run claude: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("claude's launcher never asked for the OpenAI login: %v\n%s", err, out)
			}
			var tokenCalls []string
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if strings.Contains(line, "openai-auth-client") {
					tokenCalls = append(tokenCalls, line)
				}
			}
			if len(tokenCalls) != 1 || tokenCalls[0] != "internal openai-auth-client token|bypass=1" {
				t.Fatalf("auth calls = %q, want one bare token call (the access view, no file)", tokenCalls)
			}
			if tc.wantMessage != "" && !strings.Contains(string(out), tc.wantMessage) {
				t.Errorf("the launcher must say why it did not log in:\n%s", out)
			}
		})
	}
}

package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The auth prelaunch must never begin an interactive OpenAI login where no terminal can
// answer it. A login prints a URL and waits for a browser callback, so with stdin closed
// the flow cannot complete — it can only hang until something upstream gives up.
//
// This is a REGRESSION TEST with a measured cost behind it: with `packs: ["codex"]` and no
// credential on the machine, `codex --version` printed an auth URL and blocked for fifteen
// minutes, taking five Pack Installs jobs red on both arches. The probe was
// `bash -lc 'codex --version && copilot --version'` — an invocation that needs no
// credential at all.
//
// These tests run the REAL generated shell, not a copy of it: `agentAuthPrelaunchShellFn`
// is the production text spliced into all three launcher templates, and it is exercised
// here under the `set -euo pipefail` those templates set, with a fake `yolo` on PATH that
// records the subcommands it is asked for. A guard that only existed in the Go string
// would pass a text assertion and still hang; running it is what makes deleting the guard
// fail this test.
//
// ONE BRANCH IS DELIBERATELY NOT COVERED: terminal present AND the token call fails, i.e.
// the interactive login itself. Exercising it needs a pty and a browser, so it stays a
// manual path; what these tests pin is that nothing reaches it without a terminal.
func TestAuthPrelaunchDoesNotLoginWithoutATerminal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tokenRC   int
		wantLogin bool
		wantSays  string
	}{
		{
			// The regression: no credential, no terminal. The launcher must carry on so the
			// agent still runs — `set -e` makes a nonzero return here an abort before exec.
			name:      "no credential and no terminal: says so, does not log in, exits 0",
			tokenRC:   1,
			wantLogin: false,
			wantSays:  "not an interactive terminal",
		},
		{
			// The happy path, and the vacuity guard for the case above: when the token call
			// succeeds the function must return before reaching any of the login code, so a
			// "no login was attempted" result means something.
			name:      "credential present: no login attempted at all",
			tokenRC:   0,
			wantLogin: false,
			wantSays:  "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			binDir := filepath.Join(home, "fake-bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(home, "yolo-calls.log")

			// The fake yolo records the openai-auth-client subcommand it was handed and
			// fails or succeeds the `token` call as the case requires. A `login` call always
			// succeeds, so reaching it cannot be mistaken for a failure of something else.
			fake := "#!/usr/bin/env bash\n" +
				"printf '%s\\n' \"$*\" >> " + shellSingleQuote(log) + "\n" +
				"case \"$*\" in\n" +
				"  *' token '*) exit " + strconv.Itoa(tc.tokenRC) + " ;;\n" +
				"  *login*) exit 0 ;;\n" +
				"esac\n" +
				"exit 0\n"
			if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}

			script := "set -euo pipefail\n" +
				"BIN=codex\n" +
				agentAuthPrelaunchShellFn

			cmd := exec.Command("bash", "-c", script)
			cmd.Dir = home
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HOME="+home,
				"YOLO_AUTH_PRELAUNCH_CODEX_FLAG=--codex-auth",
				"YOLO_AUTH_PRELAUNCH_CODEX_PATH=.codex/auth.json",
			)
			// The whole point: stdin is NOT a terminal. `exec.Cmd` with a nil Stdin gives
			// the child /dev/null, which is exactly the CI and agent-invocation shape.
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the prelaunch must not fail the launcher (it runs under set -e): %v\n%s",
					err, out)
			}

			calls, _ := os.ReadFile(log)
			gotLogin := strings.Contains(string(calls), "login")
			if gotLogin != tc.wantLogin {
				t.Errorf("login attempted = %v, want %v — calls:\n%s\noutput:\n%s",
					gotLogin, tc.wantLogin, calls, out)
			}
			if !strings.Contains(string(calls), "token") {
				t.Errorf("the prelaunch never asked for a token, so this case proves "+
					"nothing — calls:\n%s", calls)
			}
			if tc.wantSays != "" && !strings.Contains(string(out), tc.wantSays) {
				t.Errorf("stderr should name why no login was started (%q), got:\n%s",
					tc.wantSays, out)
			}
		})
	}
}

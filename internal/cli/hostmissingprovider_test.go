package cli

// hostmissingprovider_test.go pins the host notch's answer to claude's codex profile on a bare
// `"packs": ["claude"]` (docs/design/credential-sources-separation.md ES-D24, ES-D25). Only
// packs/openai-auth declares the openai-codex provider, and `yolo host` applies no `needs`, so
// that launch holds no such provider. It used to exit 0 and hand claude three context-window
// constants and no address (measured 2026-09-27), so claude ran on its own Claude login while
// the footer said "codex (bridge)". Every spelling of the selection now refuses before the
// exec, naming why and where the profile works.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// claudeAlone is the configuration the defect was measured on.
const claudeAlone = `{"packs": ["claude"]}`

// hostCodexRefusalPhrases are what the refusal must say for claude on codex with the provider's
// pack unselected: the bridge refusal, that the provider's own pack is missing too and changes
// nothing, and the jail launch named as one.
var hostCodexRefusalPhrases = []string{
	"refusing to launch", `profile "codex"`, "http://127.0.0.1:8215", "No host process serves it",
	`Provider "openai-codex" is not in this launch's provider table either`,
	`pack "openai-auth" ships it`, "`yolo host` does not add a pack a selected pack's `needs` names",
	`adding "openai-auth" to ` + "`packs`" + ` does not change the answer either`,
	"`yolo -p claude=codex -- claude`, which is a jail launch, not a `yolo host` one",
}

// In process, through hostMain: `yolo host -p codex -- claude` refuses and reaches no exec.
func TestHostRefusesCodexForClaudeAlone(t *testing.T) {
	rc, env, errs := hostGateRun(t, claudeAlone, nil, []string{"-p", "codex"}, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("yolo host -p codex -- claude on packs [claude] must refuse before the exec: rc = %d, "+
			"env = %v\n%s", rc, env, errs)
	}
	for _, want := range hostCodexRefusalPhrases {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// `yolo host env` shares the composition, so the observe verb refuses the same selection, with
// nothing on stdout for a shell to eval.
func TestHostEnvRefusesCodexForClaudeAlone(t *testing.T) {
	hostGateHome(t, claudeAlone, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "codex"}, &out, &errw, false, nil); rc == 0 {
		t.Fatalf("yolo host env --agent claude -p codex must refuse:\n%s", out.String())
	}
	if out.Len() != 0 || !strings.Contains(errw.String(), `Provider "openai-codex" is not in this launch's provider table`) {
		t.Errorf("stdout = %q, stderr = %q", out.String(), errw.String())
	}
}

// EVERY FRONT DOOR: the four spellings the dig measured exiting 0, each through cli.Main in a
// child process with a fake claude first on PATH, so an exec that did happen would print. The
// child is this test binary re-run with the spelling in an env var, as
// TestLeadingProfileReachesHostExec does, because Main writes to the process's own streams.
func TestEveryHostSpellingOfClaudeOnCodexRefuses(t *testing.T) {
	const helper = "YOLO_TEST_HOST_CODEX_SPELLING"
	if spelling := os.Getenv(helper); spelling != "" {
		os.Exit(Main(append([]string{"yolo"}, strings.Fields(spelling)...)))
	}
	for _, tc := range []struct{ cfg, spelling string }{
		{claudeAlone, "host -p codex -- claude"},
		{claudeAlone, "-p codex host -- claude"},
		{claudeAlone, "--at host -p codex -- claude"},
		{`{"packs": ["claude"], "use_profiles": {"claude": "codex"}}`, "host -- claude"},
	} {
		t.Run(tc.spelling, func(t *testing.T) {
			home := t.TempDir()
			userCfg(t, home, tc.cfg)
			bin := t.TempDir()
			fake := "#!/bin/sh\necho FAKE-CLAUDE-RAN ANTHROPIC_BASE_URL=\"$ANTHROPIC_BASE_URL\"\n"
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestEveryHostSpellingOfClaudeOnCodexRefuses$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(envWithoutYolo(), helper+"="+tc.spelling, "HOME="+home,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_COLOR=1")
			out, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("yolo %s: exit = %v, want 1 (a refusal)\n%s", tc.spelling, err, out)
			}
			if strings.Contains(string(out), "FAKE-CLAUDE-RAN") {
				t.Fatalf("yolo %s ran claude:\n%s", tc.spelling, out)
			}
			for _, want := range hostCodexRefusalPhrases {
				if !strings.Contains(string(out), want) {
					t.Errorf("yolo %s: the refusal must say %q:\n%s", tc.spelling, want, out)
				}
			}
		})
	}
}

// envWithoutYolo is this process's environment minus every YOLO_* variable, so a child run
// from inside a jail does not read itself as one (config.InJail keys on YOLO_VERSION).
func envWithoutYolo() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "YOLO_") {
			out = append(out, kv)
		}
	}
	return out
}

// The profiles claude's pack ships whose provider the host DOES hold still run: bedrock is
// claude's own provider, so the missing-provider refusal never reaches it.
func TestHostStillRunsClaudeOnBedrockWithClaudeAlone(t *testing.T) {
	env, errs := hostGateLaunchWith(t, claudeAlone, nil, []string{"-p", "bedrock"}, "claude")
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("claude on bedrock must still compose its switch: %v\n%s", env, errs)
	}
	if env["AWS_CONTAINER_CREDENTIALS_FULL_URI"] != "" {
		t.Errorf("the host applies no `needs`, so aws-auth's pointer must not appear (ES-D24): %q",
			env["AWS_CONTAINER_CREDENTIALS_FULL_URI"])
	}
}

// codex and pi on their codex profile, with openai-auth unselected, still run at the host: they
// register no env producer, so the provider's row has no reader in their environment, and each
// reaches the subscription through the host's managed OpenAI launch.
func TestHostStillRunsCodexAndPiOnCodexWithoutTheAuthPack(t *testing.T) {
	for _, agent := range []string{"codex", "pi"} {
		rc, env, errs := hostGateRun(t, `{"packs": ["`+agent+`"]}`, nil, []string{"-p", "codex"}, agent)
		if rc != 0 || env == nil {
			t.Errorf("yolo host -p codex -- %s must still run: rc = %d\n%s", agent, rc, errs)
		}
	}
}

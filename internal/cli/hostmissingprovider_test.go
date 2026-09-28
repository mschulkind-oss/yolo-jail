package cli

// hostmissingprovider_test.go pins the host notch's answer to claude's codex profile on a bare
// `"packs": ["claude"]` (docs/design/credential-sources-separation.md ES-D24, ES-D25; notch
// convergence item 6). Only packs/openai-auth declares the openai-codex provider. The host used
// to apply no `needs`, so that launch held no such provider: it exited 0 and handed claude three
// context-window constants and no address (measured 2026-09-27), so claude ran on its own Claude
// login while the footer said "codex (bridge)". Then it refused as a missing provider. Since the
// host closes the selection as a jail does, openai-auth and wire-bridge join through claude's
// `needs`, the provider is in the table, and every spelling of the selection refuses as ES-D18's
// unserved bridge, naming the joined pack as joined (HS-D1) and where the profile works.

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

// hostCodexRefusalPhrases are what the refusal must say for claude on codex with only claude
// listed: the bridge refusal, the bridge worded as a pack claude's `needs` joined, and the jail
// launch named as one.
var hostCodexRefusalPhrases = []string{
	"refusing to launch", `profile "codex"`, "http://127.0.0.1:8215", "No host process serves it",
	`though "wire-bridge" joined this launch (+ wire-bridge (needed by claude))`,
	"`yolo -p claude=codex -- claude`, which is a jail launch, not a `yolo host` one",
}

// hostCodexRefusalNever is what it must no longer say: that the provider is missing, or that the
// host adds no pack a `needs` names, both false once the selection closes (item 6).
var hostCodexRefusalNever = []string{
	"not in this launch's provider table", "does not add a pack", "is in `packs`",
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
	for _, never := range hostCodexRefusalNever {
		if strings.Contains(errs, never) {
			t.Errorf("the refusal must not say %q:\n%s", never, errs)
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
	if out.Len() != 0 || !strings.Contains(errw.String(), `though "wire-bridge" joined this launch`) {
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

// The profiles claude's pack ships whose provider the host holds without a bridge still run:
// bedrock is claude's own provider. aws-auth joins through claude's `needs` (item 6), and its
// bedrock pointer is WITHHELD and NAMED, because the daemon behind it does not run at the host
// (NC-D16): the ES-D24 measurement that kept the host off the closure, answered.
func TestHostStillRunsClaudeOnBedrockWithClaudeAlone(t *testing.T) {
	env, errs := hostGateLaunchWith(t, claudeAlone, nil, []string{"-p", "bedrock"}, "claude")
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("claude on bedrock must still compose its switch: %v\n%s", env, errs)
	}
	for _, k := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"} {
		if env[k] != "" {
			t.Errorf("aws-auth's pointer at a daemon the host does not run must not be exported: %s=%q", k, env[k])
		}
		if !strings.Contains(errs, k) {
			t.Errorf("the withheld %s must be named:\n%s", k, errs)
		}
	}
	if !strings.Contains(errs, "+ aws-auth (needed by claude)") {
		t.Errorf("the pack claude's `needs` joined must be announced (WB-D12):\n%s", errs)
	}
}

// codex and pi on their codex profile, with openai-auth unlisted, still run at the host:
// openai-auth joins through their `needs`, and each reaches the subscription through the host's
// managed OpenAI launch.
func TestHostStillRunsCodexAndPiOnCodexWithoutTheAuthPack(t *testing.T) {
	for _, agent := range []string{"codex", "pi"} {
		rc, env, errs := hostGateRun(t, `{"packs": ["`+agent+`"]}`, nil, []string{"-p", "codex"}, agent)
		if rc != 0 || env == nil {
			t.Errorf("yolo host -p codex -- %s must still run: rc = %d\n%s", agent, rc, errs)
		}
	}
}

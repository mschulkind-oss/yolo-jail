package cli

// hostmissingprovider_test.go pins the host notch's answer to claude's codex profile on a bare
// `"packs": ["claude"]` (docs/design/credential-sources-separation.md ES-D24, ES-D25; notch
// convergence items 3 and 6). Only packs/openai-auth declares the openai-codex provider. The host
// used to apply no `needs`, so that launch held no such provider: it exited 0 and handed claude
// three context-window constants and no address (measured 2026-09-27), so claude ran on its own
// Claude login while the footer said "codex (bridge)". Then it refused, first as a missing
// provider and then as ES-D18's unserved bridge. Now openai-auth and wire-bridge join through
// claude's `needs`, and every spelling starts the bridge's host half for claude
// (docs/design/host-notch-services.md).

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// claudeAlone is the configuration the defect was measured on.
const claudeAlone = `{"packs": ["claude"]}`

// EVERY FRONT DOOR: the four spellings the dig measured exiting 0 on claude's own login, each
// through cli.Main in a child process with a fake claude first on PATH that prints what it was
// handed. Each now starts the wire bridge's host half for claude and points claude at it
// (docs/design/host-notch-services.md §9). The child is this test binary re-run with the spelling
// in an env var, as TestLeadingProfileReachesHostExec does, because Main writes to the process's
// own streams; its OpenAI prelaunch is stubbed, as every host cell's is (hostGateHome).
func TestEveryHostSpellingOfClaudeOnCodexStartsTheBridge(t *testing.T) {
	const helper = "YOLO_TEST_HOST_CODEX_SPELLING"
	if spelling := os.Getenv(helper); spelling != "" {
		prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
		os.Exit(Main(append([]string{"yolo"}, strings.Fields(spelling)...)))
	}
	for _, tc := range []struct{ cfg, spelling string }{
		{claudeAlone, "host -p codex -- claude"},
		{claudeAlone, "-p codex host -- claude"},
		{claudeAlone, "--at host -p codex -- claude"},
		{`{"packs": ["claude"], "profile": {"claude": "codex"}}`, "host -- claude"},
	} {
		t.Run(tc.spelling, func(t *testing.T) {
			out := runHostSpelling(t, "^TestEveryHostSpellingOfClaudeOnCodexStartsTheBridge$", helper,
				tc.cfg, tc.spelling)
			assertBridgedClaude(t, tc.spelling, out)
		})
	}
}

// runHostSpelling runs `yolo <spelling>` in a child of this test binary over cfg, with a fake
// claude on PATH that prints its base URL and whether it holds a caller token, and returns the
// output, failing unless the launch exited 0.
func runHostSpelling(t *testing.T, test, helper, cfg, spelling string) string {
	t.Helper()
	home := t.TempDir()
	userCfg(t, home, cfg)
	bin := t.TempDir()
	fake := "#!/bin/sh\necho FAKE-CLAUDE-RAN ANTHROPIC_BASE_URL=\"$ANTHROPIC_BASE_URL\" TOKEN_LEN=${#ANTHROPIC_AUTH_TOKEN}\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run="+test)
	cmd.Dir = t.TempDir()
	cmd.Env = append(envWithoutYolo(), helper+"="+spelling, "HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("yolo %s: exit = %v, want 0\n%s", spelling, err, out)
	}
	return string(out)
}

// assertBridgedClaude checks a launch's output: claude ran, pointed at a loopback port the launch
// picked rather than the manifest's 8215, with a 64-character caller token, and the launch said
// it started the bridge.
func assertBridgedClaude(t *testing.T, spelling, out string) {
	t.Helper()
	for _, want := range []string{"FAKE-CLAUDE-RAN ANTHROPIC_BASE_URL=http://127.0.0.1:", "TOKEN_LEN=64",
		`started the "wire-bridge" service (pack "wire-bridge"`} {
		if !strings.Contains(out, want) {
			t.Errorf("yolo %s: the output must say %q:\n%s", spelling, want, out)
		}
	}
	if strings.Contains(out, "127.0.0.1:8215") || strings.Contains(out, "refusing") {
		t.Errorf("yolo %s used the manifest's port or refused:\n%s", spelling, out)
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
	for _, k := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"} {
		if env[k] != "" {
			t.Errorf("aws-auth's pointer at a daemon the host does not run must not be exported: %s=%q", k, env[k])
		}
		if !strings.Contains(errs, k) {
			t.Errorf("the withheld %s must be named:\n%s", k, errs)
		}
	}
	for _, want := range []string{"+ bedrock (needed by claude)", "+ aws-auth (needed by bedrock)"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the packs claude's `needs` closure joined must be announced (WB-D12), %q:\n%s", want, errs)
		}
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

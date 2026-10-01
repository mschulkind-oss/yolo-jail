package cli

// hostcredentialgate_test.go pins the credential gate at the HOST NOTCH
// (docs/reference/providers.md, OQ-CN5: the host ships with the jail, since it
// composes an environment for a process outside every sandbox). Each cell runs `yolo host`
// through hostMain to the exec, with the exec itself replaced (hostSyscallExec) so the
// environment the agent would have been handed is what the test reads. The packs are the
// SHIPPED ones, resolved from the user's `packs` the way a host launch resolves them.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostGateLaunch runs `yolo host [flags] -- <agent>` over the shipped claude, codex, pi, zai
// and aws-auth packs with env_sources carrying an AWS pair and a zai key, and returns the
// environment the exec would have handed the agent plus what the launch printed.
func hostGateLaunch(t *testing.T, flags []string, agent string) (map[string]string, string) {
	t.Helper()
	return hostGateLaunchWith(t, `{"packs": ["claude", "codex", "pi", "zai", "aws-auth"], "env_sources": [`+
		`{"AWS_ACCESS_KEY_ID": "AKIA-host", "AWS_SECRET_ACCESS_KEY": "secret-host", `+
		`"ZAI_API_KEY": "tok-host"}]}`, nil, flags, agent)
}

// hostGateNames are the variables the host gate cells read, blanked in the invoking shell
// (blankHostGateShell) so what the agent receives is what yolo composed. AWS_PROFILE and
// AWS_CONFIG_FILE are among them because the region fill reads the region file they choose
// (BR-DIR1): a developer's own would point a cell at their real AWS config. Every shipped
// provider's credential is blanked too, derived rather than listed here
// (shippedCredentialNames); this list is the names no provider declares.
var hostGateNames = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ZAI_API_KEY",
	"CLAUDE_CODE_USE_BEDROCK", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL", "PORT", "DEEPSEEK_API_KEY", "CEREBRAS_API_KEY",
	"OPENROUTER_API_KEY", "KILO_API_KEY", "YOLO_PROVIDERS", "AWS_PROFILE", "AWS_CONFIG_FILE"}

// shippedCredentialNames is every credential variable a SHIPPED provider declares: the
// `api_key_env_name` of each provider the embedded packs contribute, which is what the
// credential gate claims (packload.CredentialEnvNames). It is DERIVED rather than written into
// hostGateNames, because a written list is the one a provider's new name is missing from:
// AWS_SESSION_TOKEN and AWS_BEARER_TOKEN_BEDROCK were, so a developer shell exporting either
// (aws-vault, granted, `aws configure export-credentials`, Claude Code on Bedrock) handed claude
// an AWS credential in a cell asserting that nothing delivers it one, and the cell failed on
// that machine alone.
func shippedCredentialNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, p := range packload.Embedded() {
		for _, prov := range p.Decl.Providers() {
			names = append(names, prov.APIKeyEnvName...)
		}
	}
	if len(names) == 0 {
		t.Fatal("no shipped provider declares a credential name: the embedded packs did not load")
	}
	return names
}

// blankHostGateShell blanks, in the invoking shell, every name a host cell reads: hostGateNames
// and every shipped provider's credential.
func blankHostGateShell(t *testing.T) {
	t.Helper()
	for _, k := range append(slices.Clone(hostGateNames), shippedCredentialNames(t)...) {
		t.Setenv(k, "")
	}
}

// hostGateRegion is the AWS_REGION hostGateHome puts in every host cell's invoking shell.
const hostGateRegion = "us-test-1"

// hostGateLaunchWith is hostGateLaunch over a user config and invoking-shell values of the
// caller's choosing.
func hostGateLaunchWith(t *testing.T, cfg string, shell map[string]string, flags []string,
	agent string) (map[string]string, string) {
	t.Helper()
	rc, env, errs := hostGateRun(t, cfg, shell, flags, agent)
	if rc != 0 {
		t.Fatalf("yolo host %v -- %s: rc = %d\nstderr:\n%s", flags, agent, rc, errs)
	}
	if env == nil {
		t.Fatalf("yolo host %v -- %s never reached the exec\nstderr:\n%s", flags, agent, errs)
	}
	return env, errs
}

// hostGateRun is hostGateLaunchWith without the success assertion, for a cell whose launch is
// meant to refuse: the exit code, the environment the exec was handed (nil when the launch
// never reached it), and what the launch printed.
func hostGateRun(t *testing.T, cfg string, shell map[string]string, flags []string,
	agent string) (int, map[string]string, string) {
	t.Helper()
	return hostGateRunIn(t, cfg, shell, flags, agent, nil)
}

// hostGateRunIn is hostGateRun with a hook that prepares the temp HOME (writes a file into it)
// after it is made and before the launch runs.
func hostGateRunIn(t *testing.T, cfg string, shell map[string]string, flags []string,
	agent string, prepare func(home string)) (int, map[string]string, string) {
	t.Helper()
	home := hostGateHome(t, cfg, shell)
	if prepare != nil {
		prepare(home)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, agent), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var got []string
	origExec := hostSyscallExec
	hostSyscallExec = func(_ string, _, env []string) error {
		got = env
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = origExec })

	var out, errw bytes.Buffer
	args := append(append([]string{}, flags...), "--", agent)
	rc := hostMain(args, &out, &errw, false, nil)
	if got == nil {
		return rc, nil, errw.String()
	}
	env := map[string]string{}
	for _, kv := range got {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return rc, env, errw.String()
}

// hostGateHome sets up a temp HOME with the user config, a blanked shell and the broker seam
// stubbed, for a host-notch cell.
func hostGateHome(t *testing.T, cfg string, shell map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	// The shell `yolo host` inherits is the USER's, and passes through untouched; blank the
	// names under test so what the agent receives is what yolo composed.
	blankHostGateShell(t)
	// A REGION IN THE INVOKING SHELL, by default, and never the one this test process happens
	// to run under (a jail's own AWS_REGION would otherwise decide the region pre-flight here
	// and not in CI). Every cell selecting `bedrock` is about something else, and without a
	// region the pre-flight refuses it (OQ-BR6); hostregion_test.go pins that refusal, and a
	// cell that wants no region passes shell {"AWS_REGION": ""}.
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_REGION", hostGateRegion)
	for k, v := range shell {
		t.Setenv(k, v)
	}
	t.Chdir(t.TempDir())
	userCfg(t, home, cfg)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	return home
}

// THE FIXTURE OWNS EVERY CREDENTIAL IT READS. A host cell asserts what yolo composed, so a
// shipped provider's credential in the developer's own shell must never reach it: each one is
// set here the way such a shell sets it, and hostGateHome must blank it.
func TestHostGateHomeBlanksEveryShippedProviderCredential(t *testing.T) {
	names := shippedCredentialNames(t)
	for _, want := range []string{"AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK"} {
		if !slices.Contains(names, want) {
			t.Fatalf("the shipped bedrock provider no longer declares %s; derived %v", want, names)
		}
	}
	for _, k := range names {
		t.Setenv(k, "from-the-developer-shell")
	}
	hostGateHome(t, claudeAlone, nil)
	for _, k := range names {
		if v := os.Getenv(k); v != "" {
			t.Errorf("hostGateHome left the invoking shell's %s=%q for a host cell to read", k, v)
		}
	}
}

// Done condition 1 at the host: `yolo host -p zai -- pi` hands pi zai's key and none of the
// Bedrock pair env_sources hydrated for a provider pi did not select, and says so.
func TestHostGatePiOnZaiSeesNoAWS(t *testing.T) {
	env, errs := hostGateLaunch(t, []string{"-p", "zai"}, "pi")
	if env["ZAI_API_KEY"] != "tok-host" {
		t.Errorf("pi selected zai: ZAI_API_KEY = %q, want its key", env["ZAI_API_KEY"])
	}
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if env[k] != "" {
			t.Errorf("pi on zai was handed %s=%q, a credential of a provider it did not select", k, env[k])
		}
	}
	for _, want := range []string{"Credential scope", "AWS_ACCESS_KEY_ID", "withheld"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host launch must disclose what it withheld (%q missing):\n%s", want, errs)
		}
	}
}

// Done conditions 2 and 3 at the host: `yolo host -p bedrock -- codex` hands codex the AWS
// pair and nothing of claude's, and a plain `yolo host -- claude` gets none of them.
// aws-auth's pointer is WITHHELD at the host: it points at the in-jail adapter, a jail daemon,
// and no host process serves it (docs/plans/notch-convergence.md §4 item 2).
func TestHostGateCodexOnBedrockAndClaudeUnselected(t *testing.T) {
	codex, _ := hostGateLaunch(t, []string{"-p", "bedrock"}, "codex")
	if codex["AWS_ACCESS_KEY_ID"] != "AKIA-host" {
		t.Errorf("codex on bedrock: AWS_ACCESS_KEY_ID = %q, want AKIA-host", codex["AWS_ACCESS_KEY_ID"])
	}
	if v := codex["AWS_CONTAINER_CREDENTIALS_FULL_URI"]; v != "" {
		t.Errorf("codex on bedrock at the host was handed AWS_CONTAINER_CREDENTIALS_FULL_URI=%s, "+
			"a port no host process serves", v)
	}
	if codex["CLAUDE_CODE_USE_BEDROCK"] != "" {
		t.Errorf("codex on bedrock was handed claude's own CLAUDE_CODE_USE_BEDROCK")
	}

	claude, _ := hostGateLaunch(t, nil, "claude")
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ZAI_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
		if claude[k] != "" {
			t.Errorf("claude selected nothing and was handed %s=%q", k, claude[k])
		}
	}
}

// A KEY HELD ONLY IN THE INVOKING SHELL still reaches the agent's env derive at the host
// notch: `yolo host -p zai -- claude` with no env_sources and ZAI_API_KEY exported must hand
// claude the derive-composed token, not a base URL with none while the pre-flight passes.
// The gate's input for that is its Fallback, which is this notch's own call site.
func TestHostGateRelaysAShellHeldKeyToTheEnvDerive(t *testing.T) {
	env, errs := hostGateLaunchWith(t, `{"packs": ["claude", "zai"]}`,
		map[string]string{"ZAI_API_KEY": "tok-shell"}, []string{"-p", "zai"}, "claude")
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok-shell" {
		t.Errorf("claude on zai with the key only in the shell: ANTHROPIC_AUTH_TOKEN = %q, want "+
			"the shell's key (base URL %q)\n%s", env["ANTHROPIC_AUTH_TOKEN"], env["ANTHROPIC_BASE_URL"], errs)
	}
}

// `yolo host env` is ONE agent's slice, printed for a shell to eval, and it says so: a
// credential another agent's profile claims is withheld from the default agent's script,
// and the gate's disclosure names it on stderr, as `yolo host --` does. It used to drop the
// value from the export with no message at all.
func TestHostEnvDisclosesWhatItsSliceWithholds(t *testing.T) {
	hostGateHome(t, `{"packs": ["claude", "pi", "zai"], "profile": {"pi": "zai"}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-host", "GH_TOKEN": "gh-host"}]}`, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	if strings.Contains(out.String(), "tok-host") || hostExports(out.String(), "ZAI_API_KEY") {
		t.Errorf("claude's slice must not export pi's provider key:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "export GH_TOKEN='gh-host'") {
		t.Errorf("an unclaimed value stays in every slice:\n%s", out.String())
	}
	errs := errw.String()
	// The host notch composes ONE agent, so from claude's slice pi's selection is not in
	// the launch and the key reads as withheld from every process this launch starts.
	for _, want := range []string{"yolo host env: ", "ZAI_API_KEY", "withheld"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr must disclose the withheld credential (%q missing):\n%s", want, errs)
		}
	}
}

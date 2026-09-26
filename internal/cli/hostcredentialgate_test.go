package cli

// hostcredentialgate_test.go pins the credential gate at the HOST NOTCH
// (docs/design/provider-credential-scope.md, OQ-CN5: the host ships with the jail, since it
// composes an environment for a process outside every sandbox). Each cell runs `yolo host`
// through hostMain to the exec, with the exec itself replaced (hostSyscallExec) so the
// environment the agent would have been handed is what the test reads. The packs are the
// SHIPPED ones, resolved from the user's `packs` the way a host launch resolves them.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostGateLaunch runs `yolo host [flags] -- <agent>` over the shipped claude, codex, pi, zai
// and aws-auth packs with env_sources carrying an AWS pair and a zai key, and returns the
// environment the exec would have handed the agent plus what the launch printed.
func hostGateLaunch(t *testing.T, flags []string, agent string) (map[string]string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	// The shell `yolo host` inherits is the USER's, and passes through untouched; blank the
	// names under test so what the agent receives is what yolo composed.
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "ZAI_API_KEY",
		"CLAUDE_CODE_USE_BEDROCK", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
		t.Setenv(k, "")
	}
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude", "codex", "pi", "zai", "aws-auth"], "env_sources": [`+
		`{"AWS_ACCESS_KEY_ID": "AKIA-host", "AWS_SECRET_ACCESS_KEY": "secret-host", `+
		`"ZAI_API_KEY": "tok-host"}]}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(string, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })

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
	if rc := hostMain(args, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host %v: rc = %d\nstderr:\n%s", args, rc, errw.String())
	}
	if got == nil {
		t.Fatalf("yolo host %v never reached the exec\nstderr:\n%s", args, errw.String())
	}
	env := map[string]string{}
	for _, kv := range got {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env, errw.String()
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
// pair and aws-auth's pointer and nothing of claude's, and a plain `yolo host -- claude`
// gets none of them.
func TestHostGateCodexOnBedrockAndClaudeUnselected(t *testing.T) {
	codex, _ := hostGateLaunch(t, []string{"-p", "bedrock"}, "codex")
	for k, want := range map[string]string{
		"AWS_ACCESS_KEY_ID":                  "AKIA-host",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI": "http://127.0.0.1:1461/credentials",
	} {
		if codex[k] != want {
			t.Errorf("codex on bedrock: %s = %q, want %q", k, codex[k], want)
		}
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

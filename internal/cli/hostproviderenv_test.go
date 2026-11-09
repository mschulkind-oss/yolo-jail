package cli

// hostproviderenv_test.go pins PP-D14 at `yolo host --` (docs/design/providers-and-profiles-
// redesign.md, the maintainer's 2026-10-11 ruling: "a Claude started from a Claude shell needs
// to follow the same auth path everywhere"): claude's provider variables are what its profile
// selection says, whatever the invoking shell exported. Through hostMain to the exec, so deleting
// the clearing in hostComposition.environ fails here.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHostEnvScriptAppliesTheSelectedProviderEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		args   []string
		shell  map[string]string
		want   map[string]string
	}{
		{
			name:   "subscription clears inherited Bedrock state",
			config: claudeAlone,
			shell: map[string]string{
				"CLAUDE_CODE_USE_BEDROCK":    "1",
				"ANTHROPIC_BASE_URL":         "http://gateway.invalid/shell-secret",
				"ANTHROPIC_SMALL_FAST_MODEL": "global.anthropic.claude-haiku-4-5",
				"ANTHROPIC_MODEL":            "opus",
				"AWS_PROFILE":                "shell-profile",
				"AWS_REGION":                 "us-test-1",
				"YOLO_TEST_UNRELATED":        "kept",
			},
			want: map[string]string{
				"CLAUDE_CODE_USE_BEDROCK":    "<unset>",
				"ANTHROPIC_BASE_URL":         "<unset>",
				"ANTHROPIC_SMALL_FAST_MODEL": "<unset>",
				"ANTHROPIC_MODEL":            "opus",
				"AWS_PROFILE":                "<unset>",
				"AWS_REGION":                 "<unset>",
				"YOLO_TEST_UNRELATED":        "kept",
			},
		},
		{
			name:   "Bedrock keeps its inputs",
			config: `{"packs": ["claude", "bedrock"]}`,
			args:   []string{"--agent", "claude", "-p", "bedrock"},
			shell: map[string]string{
				"CLAUDE_CODE_USE_BEDROCK":    "0",
				"ANTHROPIC_BASE_URL":         "http://gateway.invalid/shell-secret",
				"ANTHROPIC_SMALL_FAST_MODEL": "global.anthropic.claude-haiku-4-5",
				"ANTHROPIC_MODEL":            "opus",
				"AWS_PROFILE":                "work-sso",
				"AWS_REGION":                 "us-west-2",
				"YOLO_TEST_UNRELATED":        "kept",
			},
			want: map[string]string{
				"CLAUDE_CODE_USE_BEDROCK":    "1",
				"ANTHROPIC_BASE_URL":         "<unset>",
				"ANTHROPIC_SMALL_FAST_MODEL": "global.anthropic.claude-haiku-4-5",
				"ANTHROPIC_MODEL":            "opus",
				"AWS_PROFILE":                "work-sso",
				"AWS_REGION":                 "us-west-2",
				"YOLO_TEST_UNRELATED":        "kept",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Chdir(t.TempDir())
			for key, value := range tc.shell {
				t.Setenv(key, value)
			}
			userCfg(t, home, tc.config)

			var script, errw bytes.Buffer
			if rc := hostEnv(tc.args, &script, &errw); rc != 0 {
				t.Fatalf("yolo host env: rc=%d\n%s", rc, errw.String())
			}
			got := applyHostEnvScript(t, script.String(), tc.shell)
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("applied host env %s = %q, want %q\nscript:\n%s\nstderr:\n%s",
						key, got[key], want, script.String(), errw.String())
				}
				if want == "<unset>" && !strings.Contains(script.String(), "unset '"+key+"'") {
					t.Errorf("host env script does not quote the unset name %s:\n%s", key, script.String())
				}
			}
			if !strings.Contains(errw.String(), "cleared ") ||
				!strings.Contains(errw.String(), "choose a provider that sets them with `yolo active-provider") {
				t.Errorf("stderr must explain the provider keys removed and how selection changes that:\n%s",
					errw.String())
			}
			if strings.Contains(script.String()+errw.String(), "shell-secret") ||
				strings.Contains(script.String()+errw.String(), "shell-profile") {
				t.Errorf("host env leaked inherited values in its script or diagnostics:\n%s\n%s",
					script.String(), errw.String())
			}
		})
	}
}

// applyHostEnvScript evaluates the emitted POSIX shell output over a known inherited shell
// and reads the resulting variables back. This pins the command yolo emits, not only the
// composition helper or a comment describing it.
func applyHostEnvScript(t *testing.T, script string, initial map[string]string) map[string]string {
	t.Helper()
	keys := []string{"CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_BASE_URL", "ANTHROPIC_SMALL_FAST_MODEL",
		"ANTHROPIC_MODEL", "AWS_PROFILE", "AWS_REGION", "YOLO_TEST_UNRELATED"}
	var output strings.Builder
	output.WriteString("eval \"$1\"\nprintf '%s\\n'")
	for _, key := range keys {
		output.WriteString(" \"${" + key + "-<unset>}\"")
	}
	cmd := exec.Command("sh", "-c", output.String(), "sh", script)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for key, value := range initial {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evaluating yolo host env output: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(keys) {
		t.Fatalf("applied output has %d values, want %d: %q", len(lines), len(keys), out)
	}
	got := make(map[string]string, len(keys))
	for i, key := range keys {
		got[key] = lines[i]
	}
	return got
}

func TestHostLaunchMakesClaudesProviderVariablesTheSelections(t *testing.T) {
	// The shell a Bedrock claude leaves behind: its switch, a gateway, a Bedrock model id, and
	// a subscription model id plus a variable no pack decides, which are the user's.
	shell := map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":    "1",
		"ANTHROPIC_BASE_URL":         "http://127.0.0.1:9/gateway",
		"ANTHROPIC_SMALL_FAST_MODEL": "global.anthropic.claude-haiku-4-5",
		"ANTHROPIC_MODEL":            "opus",
		"YOLO_TEST_UNRELATED":        "kept",
	}

	t.Run("subscription", func(t *testing.T) {
		rc, env, errs := hostGateRunIn(t, claudeAlone, shell, nil, "claude", nil)
		if rc != 0 || env == nil {
			t.Fatalf("launch did not exec: rc=%d\n%s", rc, errs)
		}
		for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_BASE_URL",
			"ANTHROPIC_SMALL_FAST_MODEL", "AWS_REGION"} {
			if v, ok := env[k]; ok {
				t.Errorf("%s reached a claude on its subscription as %q", k, v)
			}
		}
		if env["ANTHROPIC_MODEL"] != "opus" || env["YOLO_TEST_UNRELATED"] != "kept" {
			t.Errorf("a value the selection does not decide must stay the user's: %v", env)
		}
		if !strings.Contains(errs, "yolo host: cleared CLAUDE_CODE_USE_BEDROCK, ANTHROPIC_BASE_URL") ||
			!strings.Contains(errs, "choose a provider that sets them with `yolo active-provider") {
			t.Errorf("the launch must say what it cleared and how to set it:\n%s", errs)
		}
	})

	t.Run("bedrock", func(t *testing.T) {
		on := map[string]string{}
		for k, v := range shell {
			on[k] = v
		}
		on["CLAUDE_CODE_USE_BEDROCK"] = "0"
		// Bedrock is an explicitly selected provider pack, not a dependency of claude.
		rc, env, errs := hostGateRunIn(t, `{"packs": ["claude", "bedrock"]}`, on, []string{"-p", "bedrock"}, "claude", nil)
		if rc != 0 || env == nil {
			t.Fatalf("launch did not exec: rc=%d\n%s", rc, errs)
		}
		if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
			t.Errorf("the selection's switch must win over the shell's 0: %q", env["CLAUDE_CODE_USE_BEDROCK"])
		}
		if env["AWS_REGION"] != hostGateRegion {
			t.Errorf("a region Bedrock reads from the shell is the selection's input, kept: %q", env["AWS_REGION"])
		}
		if env["ANTHROPIC_SMALL_FAST_MODEL"] == "" {
			t.Errorf("a Bedrock model id is Bedrock's own on Bedrock: %v", env)
		}
		if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
			t.Errorf("a gateway the Bedrock selection does not set must not reach it: %v", env["ANTHROPIC_BASE_URL"])
		}
	})
}

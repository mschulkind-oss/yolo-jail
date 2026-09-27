package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// secretEnv is a composed launch env holding the two kinds of value this whole mechanism
// exists for: a credential hydrated from env_sources, and a provider token the profile
// channel composed. Both are what a `--dry-run` printed in full before 2026-09-13.
func secretEnv() *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	env.Set("YOLO_GIT_NAME", "Ada Lovelace")
	env.Set("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI-not-a-real-key")
	env.Set("ANTHROPIC_AUTH_TOKEN", "sk-ant-not-a-real-token")
	return env
}

func planWithSecrets(t *testing.T) RunPlan {
	t.Helper()
	cfg := jsonx.NewOrderedMap()
	mise := jsonx.NewOrderedMap()
	mise.Set("neovim", "nightly") // makes ProvisionNeeded true, so the stage argv exists
	cfg.Set("mise_tools", mise)
	return BuildRunPlan("/Users/Shared/proj", cfg, []string{"claude"}, []string{"claude"},
		"/opt/yolo-jail/bin/yolo", "", HomeOverlay{}, HostContext{}, secretEnv(), nil, nil)
}

// THE HEADLINE REGRESSION TEST: no composed value appears anywhere on a command line.
//
// It scans every argv and every privileged command the plan carries, not only the launch,
// because all three sandboxed argvs render from one function and a fix applied to one of
// them would leave the other two leaking while this file's other tests stayed green.
func TestNoComposedSecretRidesAnyMacosUserArgv(t *testing.T) {
	plan := planWithSecrets(t)
	secrets := []string{"wJalrXUtnFEMI-not-a-real-key", "sk-ant-not-a-real-token"}

	argvs := map[string][]string{
		"launch":            plan.LaunchArgv,
		"provisioning":      plan.ProvisionArgv,
		"bootstrap":         plan.BootstrapArgv,
		"env file dir prep": flatten(plan.EnvFileCommands),
		"env file grant":    flatten(plan.EnvFileGrantCommands),
		"stage":             flatten(plan.StageCommands),
	}
	for name, argv := range argvs {
		joined := strings.Join(argv, " ")
		for _, secret := range secrets {
			if strings.Contains(joined, secret) {
				t.Errorf("the %s argv carries a composed secret in cleartext:\n%s", name, joined)
			}
		}
	}

	// The positive half: it is DELIVERED, in the file, or this test passes for a launch
	// that simply lost every credential.
	for _, secret := range secrets {
		if !strings.Contains(plan.EnvFileContent, secret) {
			t.Errorf("the session env file does not carry %s; the agent would authenticate "+
				"against nothing:\n%s", secret, plan.EnvFileContent)
		}
	}
	if plan.EnvFile == "" {
		t.Fatal("a plan with a composed env named no session env file")
	}
}

// The invariant must FIRE when the pairs come back, or it is decoration. This is the
// mutation AGENTS.md asks for: it fails if the envfile call site in sandboxEnvPairs is
// reverted, which is the only way the leak can return.
func TestPlanInvariantsRejectAComposedVariableOnTheArgv(t *testing.T) {
	plan := planWithSecrets(t)
	if problems := PlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("the unmutated plan is not viable: %v", problems)
	}
	for _, name := range []string{"launch", "provisioning stage"} {
		broken := plan
		mutate := func(argv []string) []string {
			out := append([]string{}, argv...)
			// Put one pair back where `env -i` would read it.
			i := idxSlice(out, "-i")
			return append(out[:i+1:i+1],
				append([]string{"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI-not-a-real-key"},
					out[i+1:]...)...)
		}
		if name == "launch" {
			broken.LaunchArgv = mutate(plan.LaunchArgv)
		} else {
			broken.ProvisionArgv = mutate(plan.ProvisionArgv)
		}
		if !hasProblem(PlanInvariants(broken), "AWS_SECRET_ACCESS_KEY") {
			t.Errorf("PlanInvariants accepts a %s argv carrying a composed credential", name)
		}
	}
}

// The OTHER half of the same contract, and the one a leak-only check cannot see: an argv
// that stopped reading the file is a sandbox with no credentials at all, which looks
// perfectly clean to every assertion above.
func TestPlanInvariantsRejectAnArgvThatNeverReadsTheEnvFile(t *testing.T) {
	plan := planWithSecrets(t)
	for _, name := range []string{"launch", "provisioning stage"} {
		broken := plan
		// The unwrapped argv: what LaunchArgv/ProvisionArgv produced before the wrapper.
		strip := func(argv []string) []string {
			var out []string
			for _, w := range argv {
				if w == sandboxEnvReader || w == sandboxEnvReaderName || w == plan.EnvFile ||
					w == sandboxEnvShell || w == "-c" {
					continue
				}
				out = append(out, w)
			}
			return out
		}
		if name == "launch" {
			broken.LaunchArgv = strip(plan.LaunchArgv)
		} else {
			broken.ProvisionArgv = strip(plan.ProvisionArgv)
		}
		if !hasProblem(PlanInvariants(broken), "never reads the session env file") {
			t.Errorf("PlanInvariants accepts a %s argv that never reads the env file", name)
		}
	}
}

// The capture driver is under the same contract, and the reason is sharper there: a
// capture runs a VENDOR INSTALLER, so a credential on that argv is readable by the very
// program yolo is running for the first time.
func TestCaptureDriverCarriesNoComposedSecret(t *testing.T) {
	opts := testCaptureOptions()
	opts.SandboxEnv = secretEnv()
	plan := BuildCapturePlan(opts)
	joined := strings.Join(plan.DriverArgv, " ")
	if strings.Contains(joined, "sk-ant-not-a-real-token") {
		t.Errorf("the capture driver argv carries a provider token in cleartext:\n%s", joined)
	}
	if !strings.Contains(plan.EnvFileContent, "sk-ant-not-a-real-token") {
		t.Errorf("the capture's env file does not carry the token:\n%s", plan.EnvFileContent)
	}
	if !SandboxArgvReadsEnvFile(plan.EnvFile, plan.DriverArgv) {
		t.Errorf("the capture driver never reads its env file:\n%s", joined)
	}
	broken := plan
	broken.DriverArgv = append([]string{"sudo", "--user=_yolojail", "/usr/bin/env", "-i",
		"ANTHROPIC_AUTH_TOKEN=sk-ant-not-a-real-token"}, plan.DriverArgv...)
	if !hasProblem(CapturePlanInvariants(broken), "ANTHROPIC_AUTH_TOKEN") {
		t.Error("CapturePlanInvariants accepts a driver argv carrying a provider token")
	}
	// The sweep is part of the plan, not of a call site somebody can forget: a staging tree
	// is deleted on every exit path, and the file holding this capture's credentials has to
	// go with it.
	if !strings.Contains(strings.Join(flatten(plan.CleanupCommands), " "), plan.EnvFile) {
		t.Errorf("the capture cleanup never removes %s:\n%v", plan.EnvFile, plan.CleanupCommands)
	}
}

// The file is ROOT-OWNED 0600 in a 0700 directory, with one `user:` ACE for the sandbox
// account — which is what makes "off the argv" an improvement rather than a relocation.
// The mode and the ACE are asserted on the emitted commands because only a Mac can run
// them; `chmod +a` is an Apple ACL extension.
func TestSandboxEnvFileIsReadableOnlyByTheSandboxAccount(t *testing.T) {
	plan := planWithSecrets(t)
	dir := strings.Join(flatten(plan.EnvFileCommands), " ")
	wantDir := filepath.Dir(plan.EnvFile)
	for _, want := range []string{
		mkdirBin + " -p " + wantDir,
		chmodBin + " 0700 " + wantDir,
		chmodBin + " +a user:" + SandboxUser + " allow search " + wantDir,
	} {
		if !strings.Contains(dir, want) {
			t.Errorf("the env file's directory is not locked down (%q missing):\n%s", want, dir)
		}
	}
	grant := strings.Join(flatten(plan.EnvFileGrantCommands), " ")
	if !strings.Contains(grant, chmodBin+" +a user:"+SandboxUser+" allow read") {
		t.Errorf("the sandbox account is never granted read on the env file:\n%s", grant)
	}
	// THE CONTENT IS NEVER A COMMAND ARGUMENT. installSandboxEnvFile writes through
	// InstallRootFile, which feeds `sudo tee` on stdin — the same rule
	// setRandomPasswordReal follows for the account password. A command list that carried
	// the bytes would have moved the leak rather than closed it.
	all := strings.Join(append(flatten(plan.EnvFileCommands), flatten(plan.EnvFileGrantCommands)...), " ")
	if strings.Contains(all, "wJalrXUtnFEMI-not-a-real-key") {
		t.Errorf("the env file's own commands carry its content:\n%s", all)
	}
}

// The identity quartet is protected on BOTH crossings. It is dropped from the file for the
// same reason it never reaches the argv: the file is sourced after `env -i` set them, so a
// composed HOME or PATH in it would override the identity this backend assigns.
func TestSandboxEnvFileDropsTheProtectedQuartet(t *testing.T) {
	env := jsonx.NewOrderedMap()
	for _, k := range ProtectedSandboxEnvNames() {
		env.Set(k, "/evil")
	}
	env.Set("OK", "1")
	content := SandboxEnvFileContent(env)
	if strings.Contains(content, "/evil") {
		t.Errorf("a caller overrode a protected variable through the env file:\n%s", content)
	}
	if !strings.Contains(content, "export OK='1'") {
		t.Errorf("a non-protected variable did not survive:\n%s", content)
	}
}

// THE READER IS EXECUTED, not merely spelled. It is POSIX `sh`, so this runs on Linux CI
// and on a Mac alike — the one piece of this backend whose runtime behaviour is not
// macOS-gated, and the piece everything else depends on: a wrapper that sourced nothing
// would leave every launch credential-less with every string assertion still green.
func TestSandboxEnvReaderSourcesTheFileAndExecsTheCommand(t *testing.T) {
	if _, err := os.Stat(sandboxEnvShell); err != nil {
		t.Skipf("no %s on this machine: %v", sandboxEnvShell, err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "session.env")
	env := jsonx.NewOrderedMap()
	env.Set("YOLO_TEST_TOKEN", "tok-with-a-'-quote")
	env.Set("YOLO_TEST_PLAIN", "plain")
	if err := os.WriteFile(file, []byte(SandboxEnvFileContent(env)), 0o600); err != nil {
		t.Fatal(err)
	}

	argv := ExecWithEnvFile(file, []string{"/bin/sh", "-c", `printf '%s|%s' "$YOLO_TEST_TOKEN" "$YOLO_TEST_PLAIN"`})
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{} // `env -i`, which is what the real argv gives it
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("the reader failed: %v (argv %v)", err, argv)
	}
	if want := "tok-with-a-'-quote|plain"; string(got) != want {
		t.Errorf("the reader delivered %q, want %q", got, want)
	}

	// FAIL CLOSED on an unreadable file: an agent that started anyway would authenticate
	// against nothing and fail confusingly minutes later.
	missing := ExecWithEnvFile(filepath.Join(dir, "absent.env"), []string{"/bin/sh", "-c", "exit 0"})
	if err := exec.Command(missing[0], missing[1:]...).Run(); err == nil {
		t.Error("the reader ran the command with no env file; it must fail closed")
	}
}

func flatten(cmds [][]string) []string {
	var out []string
	for _, c := range cmds {
		out = append(out, c...)
	}
	return out
}

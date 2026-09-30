package testsupport

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// signingConfig is a git configuration file with the setting that broke a contributor's run
// of the suite: every commit and tag signed, by a signer that cannot sign.
const signingConfig = "[commit]\n\tgpgsign = true\n[tag]\n\tgpgsign = true\n[gpg]\n\tprogram = " +
	tripwireSigner + "\n"

// requireGitConfigEnv skips on a git older than GIT_CONFIG_SYSTEM (2.32), the newest of the
// variables these tests set; GIT_CONFIG_COUNT is 2.31.
func requireGitConfigEnv(t *testing.T) {
	t.Helper()
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	var major, minor int
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "git version ")
	if _, err := fmt.Sscanf(v, "%d.%d", &major, &minor); err != nil {
		t.Fatalf("parsing %q: %v", out, err)
	}
	if major < 2 || major == 2 && minor < 32 {
		t.Skipf("git %s predates GIT_CONFIG_SYSTEM (git 2.32)", v)
	}
}

// machineEnv is env with the variables in drop removed and set appended: the environment of
// a machine whose git configuration a subtest describes.
func machineEnv(env, drop, set []string) []string {
	var out []string
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		dropped := false
		for _, d := range drop {
			dropped = dropped || key == d
		}
		if !dropped {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}

// gitStateEnv are git's repository-state variables, which a git hook running this suite
// exports and which would point these runs at the committer's repository
// (packsrc.CleanGitEnv's reason, restated because this package cannot import it).
var gitStateEnv = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_PREFIX"}

// commitIn makes a scratch repository and commits one file in it under env, returning git's
// output and error. The identity is on the command line, so a run that reads no
// configuration file still has one.
func commitIn(t *testing.T, env []string) (string, error) {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@e"}, args...)...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run("add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	return run("commit", "-qm", "c")
}

// A FIXTURE'S GIT READS NO MACHINE CONFIGURATION. Each subtest is a machine that says "sign
// every commit" in one place git looks — ~/.gitconfig, $XDG_CONFIG_HOME/git/config, the
// system file (GIT_CONFIG_SYSTEM standing in for /etc/gitconfig), the `-c` flags a hook's git
// exports, the environment scope — and a commit under HermeticGitEnv succeeds on it. Its
// control commit, under the same environment without HermeticGitEnv, must fail on signing,
// or the machine is not the hostile one and the assertion proves nothing.
func TestHermeticGitEnvReadsNoMachineConfig(t *testing.T) {
	requireGitConfigEnv(t)
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	system := filepath.Join(home, "system-gitconfig")
	for _, p := range []string{filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config"), system} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(signingConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Every variable a subtest's machine decides, so the one running the suite decides none.
	decided := append([]string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
		"GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT"}, gitStateEnv...)
	for _, m := range []struct {
		where string
		set   []string
	}{
		{"~/.gitconfig", []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1"}},
		{"$XDG_CONFIG_HOME/git/config", []string{"HOME=" + t.TempDir(), "XDG_CONFIG_HOME=" + xdg,
			"GIT_CONFIG_NOSYSTEM=1"}},
		{"the system file", []string{"HOME=" + t.TempDir(), "GIT_CONFIG_SYSTEM=" + system}},
		{"a hook's -c flags", []string{"HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_PARAMETERS='commit.gpgsign'='true' 'gpg.program'='" + tripwireSigner + "'"}},
		{"the environment scope", []string{"HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=true",
			"GIT_CONFIG_KEY_1=gpg.program", "GIT_CONFIG_VALUE_1=" + tripwireSigner}},
	} {
		t.Run(m.where, func(t *testing.T) {
			machine := machineEnv(os.Environ(), decided, m.set)
			if out, err := commitIn(t, machine); err == nil {
				t.Fatalf("the control commit signed nothing, so this machine is not the hostile one:\n%s", out)
			} else if !strings.Contains(out, "sign") {
				t.Fatalf("the control commit failed, but not on signing: %v\n%s", err, out)
			}
			if out, err := commitIn(t, HermeticGitEnv(machine)); err != nil {
				t.Errorf("a commit under HermeticGitEnv read %s: %v\n%s", m.where, err, out)
			}
		})
	}
}

// A FIXTURE'S GIT RUNS NO HOOK OF THE MACHINE'S. Each subtest is a machine whose hooks refuse
// every commit, as a ticket-number check, git-secrets or a pre-commit framework hook does: one
// named by core.hooksPath in ~/.gitconfig or in $XDG_CONFIG_HOME/git/config, and one the
// repository holds itself, copied in from a template the way `git clone` copies the hooks of
// the user's init.templateDir into the pack store's mirror (GIT_TEMPLATE_DIR stands in for that
// setting, so the template reaches the repository under HermeticGitEnv too). Its control
// commit, under the same environment without HermeticGitEnv, must be refused by the hook, or
// the machine is not the hostile one and the assertion proves nothing.
func TestHermeticGitEnvRunsNoHookOfTheMachines(t *testing.T) {
	requireGitConfigEnv(t)
	tpl := t.TempDir()
	hooks := filepath.Join(tpl, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	const refused = "a hook of this machine refused the commit"
	for _, hook := range []string{"pre-commit", "commit-msg"} {
		if err := os.WriteFile(filepath.Join(hooks, hook),
			[]byte("#!/bin/sh\necho '"+refused+"' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hooksPath := "[core]\n\thooksPath = " + hooks + "\n"
	home := t.TempDir()
	xdg := filepath.Join(t.TempDir(), "xdg")
	for _, p := range []string{filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(hooksPath), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	decided := append([]string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
		"GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_TEMPLATE_DIR"},
		gitStateEnv...)
	for _, m := range []struct {
		where string
		set   []string
	}{
		{"core.hooksPath in ~/.gitconfig", []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1"}},
		{"core.hooksPath in $XDG_CONFIG_HOME/git/config", []string{"HOME=" + t.TempDir(),
			"XDG_CONFIG_HOME=" + xdg, "GIT_CONFIG_NOSYSTEM=1"}},
		{"the repository's own hooks, from a template", []string{"HOME=" + t.TempDir(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_TEMPLATE_DIR=" + tpl}},
	} {
		t.Run(m.where, func(t *testing.T) {
			machine := machineEnv(os.Environ(), decided, m.set)
			if out, err := commitIn(t, machine); err == nil || !strings.Contains(out, refused) {
				t.Fatalf("the control commit was not refused by the hook, so this machine is not "+
					"the hostile one (err %v):\n%s", err, out)
			}
			if out, err := commitIn(t, HermeticGitEnv(machine)); err != nil {
				t.Errorf("a commit under HermeticGitEnv ran a hook from %s: %v\n%s", m.where, err, out)
			}
		})
	}
}

// HermeticGitEnv returns a slice of its own: callers append to the slice they passed in, so the
// helper must neither hand that slice back nor write into its spare capacity.
func TestHermeticGitEnvLeavesItsArgumentAlone(t *testing.T) {
	base := make([]string, 1, 16)
	base[0] = "A=1"
	got := HermeticGitEnv(base)
	if !slices.Contains(got, "GIT_CONFIG_GLOBAL="+os.DevNull) {
		t.Fatalf("HermeticGitEnv(%q) = %q: no GIT_CONFIG_GLOBAL", base, got)
	}
	before := slices.Clone(got)
	again := append(base, "B=2", "C=3", "D=4", "E=5", "F=6", "G=7")
	if !slices.Equal(got, before) {
		t.Errorf("HermeticGitEnv shares its argument's backing array: got %q, then %q after "+
			"appending %q to the argument", before, got, again[1:])
	}
}

// THE TRIPWIRE FAILS A COMMIT THAT READS IT, AND ONLY THAT ONE. Armed, a git run given this
// process's environment cannot commit, on a machine with no signing of its own; the same run
// under HermeticGitEnv can; and GitConfigTripwireArmed, which each package's pin reads, says
// which state the environment is in.
func TestArmGitConfigTripwireFailsACommitThatReadsIt(t *testing.T) {
	requireGitConfigEnv(t)
	// t.Setenv records each value ArmGitConfigTripwire overwrites, so the test restores them.
	t.Setenv(envConfigCount, "")
	for i := range tripwireConfig {
		t.Setenv(fmt.Sprintf("%s%d", envConfigKey, i), "")
		t.Setenv(fmt.Sprintf("%s%d", envConfigValue, i), "")
	}
	if err := os.Unsetenv(envConfigCount); err != nil {
		t.Fatal(err)
	}
	if GitConfigTripwireArmed() {
		t.Fatal("GitConfigTripwireArmed reports an unarmed environment as armed")
	}

	ArmGitConfigTripwire()
	if !GitConfigTripwireArmed() {
		t.Fatal("GitConfigTripwireArmed = false straight after ArmGitConfigTripwire")
	}
	// A machine with no signing of its own: only the tripwire can make this commit sign.
	env := machineEnv(os.Environ(), append([]string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_SYSTEM", "GIT_CONFIG_PARAMETERS"}, gitStateEnv...),
		[]string{"HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1"})
	if out, err := commitIn(t, env); err == nil {
		t.Errorf("a commit reading the process's git configuration succeeded under the tripwire:\n%s", out)
	} else if !strings.Contains(out, tripwireSigner) {
		t.Errorf("the commit failed, but not by running the tripwire's signer: %v\n%s", err, out)
	}
	if out, err := commitIn(t, HermeticGitEnv(env)); err != nil {
		t.Errorf("a commit under HermeticGitEnv read the tripwire: %v\n%s", err, out)
	}
}

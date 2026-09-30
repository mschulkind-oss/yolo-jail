package testsupport

import (
	"os"
	"strconv"
	"strings"
)

// gitconfig.go decides which git configuration a test's git runs read, and which hooks they run.
//
// A FIXTURE here is a git repository a test builds for itself (a pack to fetch, a fork to
// pin) by running git directly. Such a run used to read the configuration of the machine
// running the suite: ~/.gitconfig, $XDG_CONFIG_HOME/git/config and the system file. Settings a
// developer may well have there broke the suite on that machine alone. Measured 2026-09-30
// with git 2.55:
//
//   - commit.gpgsign=true with a signer that prompts, or that is not there, failed every
//     fixture commit with "gpg failed to sign the data": 71 tests in internal/packsrc,
//     internal/cli and internal/cli/check with the setting in ~/.gitconfig; 83 with it in the
//     system file, adding internal/cli/run and internal/config, whose tests point HOME away
//     but not the system file.
//   - core.hooksPath naming a pre-commit or commit-msg hook that refuses an arbitrary commit
//     (a ticket-number check, git-secrets, a pre-commit framework hook) refused every fixture
//     commit: 83 tests in five packages from ~/.gitconfig, and 93 from
//     $XDG_CONFIG_HOME/git/config, which a test's own HOME does not move.
//   - In internal/packsrc, safe.bareRepository=explicit failed a helper's run in the pack
//     store's bare mirror ("cannot use bare repository"), a post-checkout hook exiting 1
//     failed a helper's checkout, and core.fsmonitor=true left a daemon per scratch repository.
//
// CI runners and yolo's own jail set none of these, so the suite passed on every machine we
// ran it on.

// HermeticGitEnv returns env for a fixture's git run: git then reads no configuration but the
// repository's own and the run's own `-c` flags, and runs no hook. It points GIT_CONFIG_GLOBAL
// at os.DevNull, sets GIT_CONFIG_NOSYSTEM=1 (which git honors over GIT_CONFIG_SYSTEM), and
// drops the configuration git takes from the environment: GIT_CONFIG_PARAMETERS, which git
// exports to the hooks of a `git -c … commit` that may be running the suite, and
// GIT_CONFIG_COUNT with its GIT_CONFIG_KEY_<n> and GIT_CONFIG_VALUE_<n> pairs, which carry
// ArmGitConfigTripwire.
//
// NO HOOK RUNS, because a repository can hold the machine's hooks with no configuration file
// naming them: `git clone` and `git init` copy the hooks of the user's init.templateDir into
// the new repository, so a mirror the pack store cloned on the user's behalf holds them, and a
// helper checking a tree out of that mirror ran their post-checkout. So the environment also
// carries one setting of git's environment scope, core.hooksPath=os.DevNull, which replaces
// $GIT_DIR/hooks as well as any configured hooks folder.
//
// Compose it with packsrc.CleanGitEnv, which drops a different set (git's repository-state
// variables): testsupport cannot call it, because packsrc's own tests import this package.
//
// Fixtures only. The code under test keeps whatever configuration its process has, as a
// user's git runs keep theirs (credential helpers, url.<base>.insteadOf).
func HermeticGitEnv(env []string) []string {
	out := make([]string, 0, len(env)+len(hermeticGitSettings))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case key == "GIT_CONFIG_GLOBAL", key == "GIT_CONFIG_SYSTEM", key == "GIT_CONFIG_NOSYSTEM",
			key == "GIT_CONFIG_PARAMETERS", key == envConfigCount,
			strings.HasPrefix(key, envConfigKey), strings.HasPrefix(key, envConfigValue):
			continue
		}
		out = append(out, kv)
	}
	return append(out, hermeticGitSettings...)
}

// hermeticGitSettings is what HermeticGitEnv appends: no global or system configuration file,
// and hooks off.
var hermeticGitSettings = []string{
	"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	envConfigCount + "=1", envConfigKey + "0=core.hooksPath", envConfigValue + "0=" + os.DevNull,
}

// ArmGitConfigTripwire makes every git run this process starts, and every run their children
// start, sign each commit and tag through a signer that does not exist. Call it from the
// TestMain of every package with a fixture, before m.Run.
//
// The TRIPWIRE (a word coined in this file) is that configuration: it gives every machine
// the one fact that broke the suite on a contributor's, so a fixture that reads the
// process's git configuration instead of HermeticGitEnv's fails on CI and in this jail
// too, and not only on a machine whose owner signs their commits. It lives in git's
// environment scope (GIT_CONFIG_COUNT), which outranks every configuration file and yields
// to a run's own `-c`, and needs no file to create or remove, so a helper child that exits
// from inside a test leaves nothing behind.
//
// The code under test runs under it as well, which it can afford: yolo never commits or
// tags, so a signing setting changes none of its git runs, and a machine that signs is one
// its users have.
func ArmGitConfigTripwire() {
	// An inherited GIT_CONFIG_KEY_<n> past the tripwire's count would be ignored anyway; they
	// are dropped so the environment says only what the tripwire does.
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, envConfigKey) || strings.HasPrefix(key, envConfigValue) {
			_ = os.Unsetenv(key)
		}
	}
	for i, kv := range tripwireConfig {
		n := strconv.Itoa(i)
		_ = os.Setenv(envConfigKey+n, kv[0])
		_ = os.Setenv(envConfigValue+n, kv[1])
	}
	_ = os.Setenv(envConfigCount, strconv.Itoa(len(tripwireConfig)))
}

// GitConfigTripwireArmed reports whether this process's environment carries the tripwire,
// for the test in each package that pins its TestMain's ArmGitConfigTripwire call.
func GitConfigTripwireArmed() bool {
	if os.Getenv(envConfigCount) != strconv.Itoa(len(tripwireConfig)) {
		return false
	}
	for i, kv := range tripwireConfig {
		n := strconv.Itoa(i)
		if os.Getenv(envConfigKey+n) != kv[0] || os.Getenv(envConfigValue+n) != kv[1] {
			return false
		}
	}
	return true
}

const (
	envConfigCount = "GIT_CONFIG_COUNT"
	envConfigKey   = "GIT_CONFIG_KEY_"
	envConfigValue = "GIT_CONFIG_VALUE_"

	// tripwireSigner is the signer the tripwire names. It must not exist on any machine, so
	// git's failure to run it is the same everywhere.
	tripwireSigner = "/nonexistent/yolo-test-git-signer"
)

// tripwireConfig is the tripwire, in the order git reads it. gpg.format pins the openpgp
// signer, so an ssh or x509 signer the machine's own configuration names (1Password's
// op-ssh-sign, say, which prompts) is never the one git runs; both spellings of that signer
// are set, since git takes whichever it reads last.
var tripwireConfig = [][2]string{
	{"commit.gpgsign", "true"},
	{"tag.gpgsign", "true"},
	{"gpg.format", "openpgp"},
	{"gpg.program", tripwireSigner},
	{"gpg.openpgp.program", tripwireSigner},
}

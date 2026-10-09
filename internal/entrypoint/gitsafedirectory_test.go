package entrypoint

// gitsafedirectory_test.go pins the workspace's safe.directory entry against REAL git, never
// against the argv configureGit builds: the question is whether git in the sandbox accepts
// the workspace, and only git can answer it.
//
// THE OWNERSHIP MISMATCH IS SIMULATED WITH git's OWN SWITCH. On macos-user the agent runs as
// the sandbox account and the workspace belongs to the human, and no test here can make two
// accounts. GIT_TEST_ASSUME_DIFFERENT_OWNER=1 makes git treat every repository as owned by
// someone else — the switch git's own t0033-safe-directory.sh drives this check with — so the
// test reproduces the refusal the agent meets (exit 128, "dubious ownership") and then shows
// the config configureGit writes clears it.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitBin is the real git every helper below runs, BY PATH: the bootstrap test takes git off
// the process PATH, as the macos-user launch does, and the helpers must still reach it.
var gitBin string

// hermeticGit points git at nothing of the invoking machine's: no system config (a distro
// that ships `safe.directory = *` would pass every case below vacuously), no inherited
// global override, no XDG tree.
func hermeticGit(t *testing.T) {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	gitBin = p
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_DIR", "GIT_WORK_TREE", "GIT_TEST_ASSUME_DIFFERENT_OWNER"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// gitEnv is a loud Env whose AGENT'S PATH holds the real git, as $YOLO_DARWIN_LOGIN_PATH
// carries it on the one backend configureGit runs on. gitForConfig looks there and nowhere
// else, so the fixture names the directory rather than relying on the image's /bin to hold one.
func gitEnv(t *testing.T) (e *Env, stderr, logOnly *bytes.Buffer) {
	t.Helper()
	e, stderr, logOnly = loudEnv(t)
	e.Vars[DarwinLoginPathEnv] = filepath.Dir(gitBin)
	return e, stderr, logOnly
}

// gitRepo makes a real repository and returns its RESOLVED path (the darwin TMPDIR class:
// on a Mac t.TempDir() is under the /var -> /private/var link, and git compares physical
// paths).
func gitRepo(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(base, "proj")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(gitBin, "init", "-q", ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return ws
}

// gitAsAnotherOwner runs `git -C dir <args>` the way the sandbox account meets the
// workspace: as a user who does not own it, with home's global config.
func gitAsAnotherOwner(home, dir string, args ...string) (string, int) {
	cmd := exec.Command(gitBin, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_TEST_ASSUME_DIFFERENT_OWNER=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		return string(out) + err.Error(), -1
	}
	return string(out), 0
}

// safeDirectories is the global config's safe.directory list, in file order. Each value is
// read whole, NUL-terminated (--null): an entry is a path, and a path may hold a space, so a
// split on whitespace would report one workspace as two entries.
func safeDirectories(t *testing.T, home string) []string {
	t.Helper()
	cmd := exec.Command(gitBin, "config", "--global", "--null", "--get-all", "safe.directory")
	cmd.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"))
	out, err := cmd.Output()
	if err != nil {
		return nil // exit 1: no entry at all
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// TestConfigureGitTrustsAWorkspaceAnotherAccountOwns is the day-one failure and its fix: the
// first command fails exactly as the agent's does, and the same command passes once
// configureGit has run. Delete trustWorkspace's call and the second status fails.
func TestConfigureGitTrustsAWorkspaceAnotherAccountOwns(t *testing.T) {
	hermeticGit(t)
	ws := gitRepo(t)
	e, _, _ := gitEnv(t)
	e.Workspace = ws

	out, rc := gitAsAnotherOwner(e.Home, ws, "status")
	if rc != 128 || !strings.Contains(out, "dubious ownership") {
		t.Fatalf("the fixture does not reproduce the refusal (rc %d, want 128 naming dubious "+
			"ownership), so the pass below would prove nothing:\n%s", rc, out)
	}

	configureGit(e)

	if out, rc := gitAsAnotherOwner(e.Home, ws, "status"); rc != 0 {
		t.Fatalf("git still refuses the workspace after configureGit (rc %d):\n%s", rc, out)
	}
	// EXACTLY the workspace: `*` would also make the status above pass, and trust every
	// repository on the machine.
	if got := safeDirectories(t, e.Home); !slices.Equal(got, []string{ws}) {
		t.Errorf("safe.directory = %q, want exactly [%q]", got, ws)
	}
}

// A repository NESTED under the workspace is not trusted by the entry: the exact-path form is
// the ruling, and a `<ws>/*` or `*` that crept in would pass the test above too.
func TestTheWorkspaceEntryDoesNotTrustANestedRepository(t *testing.T) {
	hermeticGit(t)
	ws := gitRepo(t)
	nested := filepath.Join(ws, "vendor", "other")
	if out, err := exec.Command(gitBin, "init", "-q", nested).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	e, _, _ := gitEnv(t)
	e.Workspace = ws
	configureGit(e)

	if out, rc := gitAsAnotherOwner(e.Home, nested, "status"); rc != 128 {
		t.Errorf("a repository nested in the workspace is trusted too (rc %d), so the entry is "+
			"wider than the workspace:\n%s", rc, out)
	}
}

// Every launch runs configureGit into a global config that persists per workspace, and the
// user may keep entries of their own there. So a second run adds no duplicate, and an entry
// the user added survives — a plain `git config safe.directory X` refuses outright once two
// entries exist ("cannot overwrite multiple values"), which would have cost the workspace its
// entry at the second launch after the user added one.
func TestTheSafeDirectoryEntryIsIdempotentAndKeepsTheUsersOwn(t *testing.T) {
	hermeticGit(t)
	ws := gitRepo(t)
	e, stderr, _ := gitEnv(t)
	e.Workspace = ws

	configureGit(e)
	add := exec.Command(gitBin, "config", "--global", "--add", "safe.directory", "/Users/Shared/yolo/elsewhere")
	add.Env = append(os.Environ(), "HOME="+e.Home, "GIT_CONFIG_GLOBAL="+filepath.Join(e.Home, ".gitconfig"))
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git config --add: %v\n%s", err, out)
	}
	configureGit(e)
	configureGit(e)

	want := []string{ws, "/Users/Shared/yolo/elsewhere"}
	if got := safeDirectories(t, e.Home); !slices.Equal(got, want) {
		t.Errorf("after three runs and one entry of the user's, safe.directory = %q, want %q", got, want)
	}
	if strings.Contains(stderr.String(), "safe.directory") {
		t.Errorf("a repeated run reported a failure:\n%s", stderr.String())
	}
}

// git compares the entry against the repository's PHYSICAL path — older gits, Apple's among
// them, without resolving the entry — so a workspace named through a link must be written
// resolved. The darwin TMPDIR class in miniature: /tmp is /private/tmp on a Mac.
func TestTheSafeDirectoryEntryIsTheResolvedWorkspace(t *testing.T) {
	hermeticGit(t)
	ws := gitRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	e, _, _ := gitEnv(t)
	e.Workspace = link
	configureGit(e)

	if got := safeDirectories(t, e.Home); !slices.Equal(got, []string{ws}) {
		t.Errorf("safe.directory = %q, want the resolved workspace [%q], not the link %q", got, ws, link)
	}
}

// A write that fails is said, naming the setting, the symptom and the command that fixes it:
// the agent otherwise meets a bare exit 128 with nothing pointing back at the boot.
//
// THE COMMAND IS RUN, not read: the workspace's name has a space in it, the remedy is taken out
// of the warning exactly as printed and handed to a shell with the real git, and the one entry
// it leaves must be the whole workspace path. Unquoted, the shell split it and git set
// safe.directory to the path's first word and failed on the second.
func TestAFailedSafeDirectoryWriteIsReported(t *testing.T) {
	hermeticGit(t)
	const ws = "/Users/Shared/yolo/My Project"
	failing := fakeBin(t, "git", "exit 1")
	e, stderr, _ := loudEnv(t)
	e.Vars[DarwinLoginPathEnv] = failing
	e.Workspace = ws
	configureGit(e)
	mustContain(t, "a failed safe.directory write", stderr, "safe.directory", ws, "dubious ownership")

	const lead = "until it is set: "
	i := strings.Index(stderr.String(), lead)
	if i < 0 {
		t.Fatalf("the warning names no command to run:\n%s", stderr)
	}
	remedy := strings.TrimSpace(strings.SplitN(stderr.String()[i+len(lead):], "\n", 2)[0])
	home := t.TempDir()
	sh := exec.Command("/bin/sh", "-c", remedy)
	sh.Env = append(os.Environ(), "PATH="+filepath.Dir(gitBin), "HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"))
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("the printed remedy %q fails in a shell: %v\n%s", remedy, err, out)
	}
	if got := safeDirectories(t, home); !slices.Equal(got, []string{ws}) {
		t.Errorf("the printed remedy %q set safe.directory to %q, want exactly [%q]", remedy, got, ws)
	}
}

// plantGit writes a `git` into dir that records its argv in marker and exits 0 — what an
// agent's planted binary would do to go unnoticed: answer every call with success.
func plantGit(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$0 $*\" >> '" + marker + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// THE BOOTSTRAP RUNS OUTSIDE SEATBELT, so the git it runs must be one the agent cannot write.
// The agent's PATH on macos-user opens with directories in the sandbox home, which the profile
// lets it write: a `git` in ~/.local/bin, or a mise shim (the mise store is machine-wide, so
// one workspace's agent can plant it for every other workspace's bootstrap), would otherwise
// run here unconfined as the sandbox account at the next launch. Both are planted AHEAD of
// the real git, the order the login path gives them.
//
// It fails if gitForConfig searches the whole agent's PATH (agentPath) rather than its part
// outside the home, and if configureGit stops asking gitForConfig; the second assertion fails
// if the filter drops the real git too.
func TestConfigureGitNeverRunsAGitFromTheSandboxHome(t *testing.T) {
	hermeticGit(t)
	ws := gitRepo(t)
	e, _, _ := loudEnv(t)
	e.Workspace = ws
	e.Vars["YOLO_GIT_EMAIL"] = "someone@example.com"
	marker := filepath.Join(t.TempDir(), "planted-git-ran")
	local := filepath.Join(e.Home, ".local", "bin")
	shims := filepath.Join(e.Home, ".yolo", "mise", "shims")
	plantGit(t, local, marker)
	plantGit(t, shims, marker)
	t.Setenv("PATH", "")
	e.Vars[DarwinLoginPathEnv] = strings.Join([]string{local, shims, filepath.Dir(gitBin)}, ":")

	configureGit(e)

	if ran, err := os.ReadFile(marker); err == nil {
		t.Fatalf("configureGit ran a git from the sandbox home, outside Seatbelt:\n%s", ran)
	}
	if got := safeDirectories(t, e.Home); !slices.Equal(got, []string{ws}) {
		t.Errorf("safe.directory = %q, want [%q] written by the git outside the home", got, ws)
	}
}

// THE CALL SITE, ON THE BACKEND THAT NEEDS IT: the real macos-user bootstrap, entered the way
// `yolo internal darwin-bootstrap` enters it (DarwinEnvFrom, then RunDarwinBootstrap), leaves
// a home whose git accepts the workspace the launch named. It fails if the configure_git step
// stops running on the darwin boot, if the step stops calling configureGit, or if the
// bootstrap's Env stops carrying YOLO_DARWIN_WORKSPACE — each of which leaves the entry naming
// some other path, or none.
//
// THE PROCESS HAS NO PATH, as on a Mac: the launch runs the bootstrap under `env -i` and names
// no PATH (DarwinBootstrapArgv), so exec.LookPath finds nothing there. The agent's PATH arrives
// as YOLO_DARWIN_LOGIN_PATH instead. Until configureGit looked there, it noted "no git on PATH"
// and wrote nothing at all on this backend — no safe.directory, and no identity either.
func TestTheMacosUserBootstrapLeavesAHomeWhoseGitAcceptsTheWorkspace(t *testing.T) {
	hermeticGit(t)
	ws := gitRepo(t)
	home := t.TempDir()
	t.Setenv("PATH", "")
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME":             home,
		"YOLO_DARWIN_WORKSPACE": ws,
		"YOLO_GIT_EMAIL":        "someone@example.com",
		DarwinLoginPathEnv:      filepath.Dir(gitBin),
	}, home)
	e.Stderr = &strings.Builder{}

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})

	if out, rc := gitAsAnotherOwner(home, ws, "status"); rc != 0 {
		t.Fatalf("after the macos-user bootstrap, git as another account still refuses the "+
			"workspace (rc %d):\n%s\nbootstrap said:\n%s", rc, out, e.Stderr)
	}
	// The identity rides the same lookup, so it was missing on this backend for the same reason.
	if out, rc := gitAsAnotherOwner(home, ws, "config", "--global", "user.email"); rc != 0 ||
		strings.TrimSpace(out) != "someone@example.com" {
		t.Errorf("after the macos-user bootstrap, user.email = %q (rc %d), want the forwarded "+
			"identity\nbootstrap said:\n%s", out, rc, e.Stderr)
	}
}

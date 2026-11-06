package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// privatePathFixture is a bound jail's home with its launchers generated, a fake mise-installed
// semver, and a stub `mise` that hands the fake's dir to PATH the way mise does: `mise env`
// prepends it once, and `mise activate` prepends it and re-prepends it from its prompt and
// chpwd hooks. The stub sits in $NPM_CONFIG_PREFIX/bin, which the .bashrc's own PATH carries ahead
// of /bin, so neither shell shape reaches a real mise.
type privatePathFixture struct {
	e                     *Env
	home, npmPrefix, fake string
}

func newPrivatePathFixture(t *testing.T, bound bool) privatePathFixture {
	t.Helper()
	home, packRoot := t.TempDir(), writeSemverPack(t)
	vars := map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot}
	if bound {
		value, err := MarshalPrivateClients([]PrivateClientBinding{fixturePrivateBinding()})
		if err != nil {
			t.Fatal(err)
		}
		vars[PrivateHTTPClientsEnv] = value
	}
	e := NewEnv(vars)
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	f := privatePathFixture{e: e, home: home, npmPrefix: t.TempDir(), fake: filepath.Join(t.TempDir(), "mise-tool-bin")}
	if err := os.MkdirAll(f.fake, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.fake, "semver"), []byte("#!/bin/sh\necho mise-copy\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
case "$1" in
env) echo 'export PATH=` + f.fake + `:"$PATH"' ;;
activate) cat <<'ACT'
export PATH=` + f.fake + `:"$PATH"
_mise_hook_prompt_command() { PATH=` + f.fake + `:"$PATH"; }
PROMPT_COMMAND="_mise_hook_prompt_command${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
_mise_hook_chpwd() { PATH=` + f.fake + `:"$PATH"; }
chpwd_functions+=(_mise_hook_chpwd)
cd() { builtin cd "$@" && for h in "${chpwd_functions[@]}"; do "$h"; done; }
ACT
;;
esac
`
	if err := os.MkdirAll(filepath.Join(f.npmPrefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.npmPrefix, "bin", "mise"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f privatePathFixture) run(t *testing.T, argv ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cmd := exec.Command(bash, argv...)
	cmd.Env = []string{"HOME=" + f.home, "NPM_CONFIG_PREFIX=" + f.npmPrefix,
		"PATH=" + filepath.Join(f.npmPrefix, "bin") + ":" + BootPath(f.e),
		PrivateHTTPClientsEnv + "=" + f.e.Getenv(PrivateHTTPClientsEnv)}
	cmd.Dir = f.home
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestABoundBinResolvesToItsBootstrapAfterMiseActivation: a project mise tool that installs the
// bound bin (under any key) puts its dir ahead of the launch dir; the session's activation must
// still resolve the bound name to the bootstrap launcher in every shell shape, by position.
func TestABoundBinResolvesToItsBootstrapAfterMiseActivation(t *testing.T) {
	f := newPrivatePathFixture(t, true)
	want := filepath.Join(f.e.GeneratedBinDir(), "private", "semver")

	// A session's non-interactive shell, the agent launch's included (execBash, runProvisionStage).
	out, err := f.run(t, "-c", activationPrefix(f.e)+`command -v semver`)
	if err != nil || strings.TrimSpace(out) != want {
		t.Fatalf("after `mise env`, semver resolves to %q (%v), want its bootstrap %s", out, err, want)
	}

	// An interactive shell after `mise activate`, then after its prompt and chpwd hooks run.
	bashrc := filepath.Join(f.home, "test.bashrc")
	if err := os.WriteFile(bashrc, []byte(Bashrc(f.e)), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `command -v semver; for c in "${PROMPT_COMMAND[@]}"; do eval "$c"; done; command -v semver; cd /; command -v semver`
	out, err = f.run(t, "--rcfile", bashrc, "-i", "-c", script)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err != nil || len(lines) < 3 {
		t.Fatalf("interactive shell: %v\n%s", err, out)
	}
	for i, got := range lines[len(lines)-3:] {
		if got != want {
			t.Errorf("interactive step %d: semver resolves to %q, want its bootstrap %s\n%s", i, got, want, out)
		}
	}
}

// A bound session whose bin does not resolve to its bootstrap after activation stops before its
// command, naming the next step, rather than running whatever else provides the name.
func TestABoundSessionStopsWhenItsBinDoesNotResolveToTheBootstrap(t *testing.T) {
	f := newPrivatePathFixture(t, true)
	if err := os.Remove(filepath.Join(f.e.PrivateClientDir(), "semver")); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(t, "-c", activationPrefix(f.e)+`echo the-command-ran`)
	if err == nil || strings.Contains(out, "the-command-ran") || !strings.Contains(out, "semver") || !strings.Contains(out, "rerun yolo") {
		t.Fatalf("a session whose bound bin resolves elsewhere ran its command (%v):\n%s", err, out)
	}
}

// An unbound jail's activation and .bashrc carry none of it: ordinary programs keep BootPath's
// order, and mise's copy is what an unbound semver is.
func TestAnUnboundJailKeepsMisesOrder(t *testing.T) {
	f := newPrivatePathFixture(t, false)
	if strings.Contains(activationPrefix(f.e), "_yolo_private_first") || strings.Contains(Bashrc(f.e), "_yolo_private_first") {
		t.Fatal("an unbound jail's shell text moves the private dir")
	}
	out, err := f.run(t, "-c", activationPrefix(f.e)+`command -v semver`)
	if err != nil || strings.TrimSpace(out) != filepath.Join(f.fake, "semver") {
		t.Fatalf("an unbound semver resolves to %q (%v), want mise's copy", out, err)
	}
}

// A bound bin that is also a blocked tool fails the boot: the private dir goes ahead of the
// block dir after activation, so writing it would bypass the block.
func TestABoundBinThatIsBlockedFailsTheBoot(t *testing.T) {
	home, packRoot := t.TempDir(), writeSemverPack(t)
	value, err := MarshalPrivateClients([]PrivateClientBinding{fixturePrivateBinding()})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, PrivateHTTPClientsEnv: value})
	if err := os.MkdirAll(e.BlockDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.BlockDir(), "semver"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = GenerateAgentLaunchers(e)
	if err == nil || !strings.Contains(err.Error(), "blocked") || !strings.Contains(err.Error(), "rerun yolo") {
		t.Fatalf("a blocked bound bin got a launcher: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(e.PrivateClientDir(), "semver")); !os.IsNotExist(statErr) {
		t.Fatalf("a blocked bound bin was written to the private dir: %v", statErr)
	}
}

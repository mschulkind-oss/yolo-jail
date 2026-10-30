package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMain points HOME at an empty directory for the whole package.
//
// A CONFIG UNIT TEST MUST NOT READ THE DEVELOPER'S CONFIG. ValidateConfig and LoadConfig
// merge the USER-level file (~/.config/yolo-jail/config.jsonc) under the workspace one, so
// every test that passes a t.TempDir() workspace and believes it is testing a fixture is
// in fact testing that fixture PLUS whatever the person running it happens to have
// configured. The tests that already call t.Setenv("HOME", …) still do — that wins over
// this, and several of them are specifically about user-scope merging.
//
// It surfaced when `allow_exec` was retired: a dozen tests across this package failed at
// once with `config.packs[8].allow_exec: unknown key`, on a machine whose real config
// still carried the key. Not one of those tests is about packs. The failure was honest
// about the key and misleading about everything else — a loophole-placement test reporting
// a pack error is a test whose subject nobody can read off its output.
//
// os.Setenv rather than t.Setenv because TestMain has no *testing.T, and the value is
// process-wide by design: the point is that NO test in this package inherits the real one.
//
// It also resolves TMPDIR (testsupport.UsePhysicalTempDir) first: the edit-target resolver
// binds the workspace through filepath.EvalSymlinks, so a fixture minted under macOS's /var
// (a link to /private/var) never equals the path it reports.
func TestMain(m *testing.M) {
	testsupport.UsePhysicalTempDir()
	home, err := os.MkdirTemp("", "yolo-config-tests-home-")
	if err != nil {
		panic("config tests: creating an isolated HOME: " + err.Error())
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic("config tests: setting HOME: " + err.Error())
	}
	// The same rule for git: an empty HOME hides ~/.gitconfig but not the system file, so a
	// fixture's git reads no machine configuration (testsupport.HermeticGitEnv), and the
	// tripwire makes one that does fail here and on CI, not only on a machine that signs.
	testsupport.ArmGitConfigTripwire()
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// TestGitConfigTripwireIsArmedHere pins the TestMain call above: without it a fixture that
// reads the machine's git configuration passes again on every machine that does not sign.
func TestGitConfigTripwireIsArmedHere(t *testing.T) {
	if !testsupport.GitConfigTripwireArmed() {
		t.Fatal("the git configuration tripwire is not armed; this package's TestMain must call testsupport.ArmGitConfigTripwire")
	}
}

// TestTempDirIsPhysicalHere pins the UsePhysicalTempDir call in TestMain: without it the
// edit-target comparisons pass on Linux and fail on a host whose temp directory is a symlink.
func TestTempDirIsPhysicalHere(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != dir {
		t.Fatalf("t.TempDir() = %q resolves to %q; this package's TestMain must call testsupport.UsePhysicalTempDir", dir, resolved)
	}
}

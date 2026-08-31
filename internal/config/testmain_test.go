package config

import (
	"os"
	"testing"
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
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "yolo-config-tests-home-")
	if err != nil {
		panic("config tests: creating an isolated HOME: " + err.Error())
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic("config tests: setting HOME: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

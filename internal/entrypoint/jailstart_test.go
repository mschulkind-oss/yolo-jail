package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jailstart_test.go pins the jail-start record (jailstart.go): the boot's write_jail_start step
// writes one fresh id per jail start, on both boots, and not on a session's pass.

func readJailStart(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(JailStartRel)))
	if err != nil {
		t.Fatalf("no jail-start record: %v", err)
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		t.Fatal("the jail-start record is empty")
	}
	return id
}

// runJailStartStep runs the TABLE'S write_jail_start entry (looked up by name, so deleting the
// entry fails every caller) as the given boot and pass.
func runJailStartStep(t *testing.T, home string, target bootTarget, sessionPass bool) {
	t.Helper()
	e := &Env{Home: home, Vars: map[string]string{}, Stderr: &strings.Builder{}}
	runSteps(&bootRun{e: e, target: target, sessionPass: sessionPass},
		[]bootStep{mustBootStep(t, "write_jail_start")})
}

// Ids differ across boots, are equal across a session's pass, and the macos-user bootstrap
// writes one too.
func TestTheBootWritesAFreshJailStartRecordPerBootAndNotPerAttach(t *testing.T) {
	home := t.TempDir()
	runJailStartStep(t, home, bootContainer, false)
	first := readJailStart(t, home)

	runJailStartStep(t, home, bootContainer, true)
	if got := readJailStart(t, home); got != first {
		t.Errorf("a session's pass (an attach) must not mint a new start: %q then %q", first, got)
	}

	runJailStartStep(t, home, bootContainer, false)
	second := readJailStart(t, home)
	if second == first {
		t.Errorf("a second boot must write a different id, both %q", first)
	}

	dhome := t.TempDir()
	runJailStartStep(t, dhome, bootDarwin, false)
	did := readJailStart(t, dhome)
	runJailStartStep(t, dhome, bootDarwin, false)
	if got := readJailStart(t, dhome); got == did {
		t.Errorf("each macos-user bootstrap is a jail start, but both wrote %q", did)
	}
}

// The record is written whole (no temp file left beside it) and is one line the shell reads with
// $(<file).
func TestTheJailStartRecordIsOneIdAndLeavesNothingBeside(t *testing.T) {
	home := t.TempDir()
	if err := writeJailStart(home); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, filepath.Dir(filepath.FromSlash(JailStartRel)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "jail-start" {
		t.Errorf("want only the record in %s, got %v", dir, entries)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "jail-start"))
	if s := string(raw); len(s) != 33 || s[32] != '\n' {
		t.Errorf("want 32 hex digits and a newline, got %q", s)
	}
}

// A failed write is said, not fatal: the launchers handle an absent record.
func TestAFailedJailStartWriteIsSaidAndTheBootGoesOn(t *testing.T) {
	home := t.TempDir()
	// A FILE where the state directory must go makes the write fail without any permission trick.
	if err := os.MkdirAll(filepath.Join(home, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "state"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	e := &Env{Home: home, Vars: map[string]string{}, Stderr: &out}
	runSteps(&bootRun{e: e, target: bootContainer}, []bootStep{mustBootStep(t, "write_jail_start")})
	if !strings.Contains(out.String(), "could not write the jail-start record") || !strings.Contains(out.String(), "Relaunch the jail") {
		t.Errorf("the failure must be said with its next step, got %q", out.String())
	}
}

// claimCheck is a jail start whose check for bin has already been claimed: the boot's record, and
// the marker the claiming launcher left.
func claimCheck(t *testing.T, home, bin string) {
	t.Helper()
	if err := writeJailStart(home); err != nil {
		t.Fatal(err)
	}
	id := readJailStart(t, home)
	dir := filepath.Join(home, filepath.FromSlash(LaunchChecksDirRel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bin+"."+id), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

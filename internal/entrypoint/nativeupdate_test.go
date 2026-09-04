package entrypoint

// nativeupdate_test.go RUNS the native launcher's evergreen branch (program-delivery.md
// §3.5, OQ-PD12/PD14). The template's old shape — `"$REAL_BIN" install` on an hourly
// stamp — is what these cells replace, and each one is written to go red for a specific
// deletion rather than to describe the script.
//
// Everything here drives a FAKE program: the "vendor" is a shell script the test writes
// into $HOME/.local/bin that appends a line to a log when it is run. No agent is started
// and nothing reaches a network (AGENTS.md's "No agent tests" rule).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// updateProbe is one launcher under test plus the fake program it manages.
type updateProbe struct {
	home    string
	log     string // the fake program appends its argv here, one line per invocation
	stamps  string
	script  string
	realBin string
}

// newUpdateProbe writes a launcher for `probetool` with the given verb and policy, and
// seeds $HOME/.local/bin/probetool with a fake vendor binary that logs its argv.
//
// The seeded binary is what makes the update branch reachable at all: with nothing at
// REAL_BIN the launcher takes the cold-install arm, which is a different cell.
func newUpdateProbe(t *testing.T, verb []string, updates bool, install bool) *updateProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	p := &updateProbe{
		home:    home,
		log:     filepath.Join(home, "argv.log"),
		stamps:  filepath.Join(home, "stamps"),
		script:  filepath.Join(home, "launch-probetool"),
		realBin: filepath.Join(home, ".local", "bin", "probetool"),
	}
	if install {
		if err := os.MkdirAll(filepath.Dir(p.realBin), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n"
		if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	launcher := nativeAgentLauncher(
		&packdecl.Install{
			Kind: "native", Bin: "probetool",
			InstallerURL: "https://example.invalid/never-fetched.sh",
			UpdateVerb:   verb,
		},
		p.stamps,
		filepath.Join(home, "ws", ".yolo", "receipts.jsonl"),
		"", // no capture store
		updates,
	)
	if err := os.WriteFile(p.script, []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// shellQuoteForTest single-quotes a path for splicing into the fake program's body. The
// temp dirs this file uses never contain a quote; this keeps the fake honest anyway.
func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (p *updateProbe) run(t *testing.T, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(p.script)
	cmd.Dir = p.home
	cmd.Env = append([]string{"HOME=" + p.home, "PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("launcher could not be run at all: %v\n%s", err, out)
	}
	return string(out), rc
}

func (p *updateProbe) argvLog(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(p.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestNativeLauncherRunsTheDeclaredUpdateVerb is the cell OQ-PD14 exists for: the argv the
// PACK declared reaches the program, and it reaches it BEFORE the launch.
//
// It fails if the verb is dropped from the template, if the projection stops carrying it,
// or if the update branch is deleted — the last being the mutation that matters, because
// the old template's `"$REAL_BIN" install` was already a no-op for every vendor whose verb
// is spelled otherwise, and nothing noticed for nine days of unmoved stamps.
func TestNativeLauncherRunsTheDeclaredUpdateVerb(t *testing.T) {
	p := newUpdateProbe(t, []string{"update", "--self"}, true, true)

	out, rc := p.run(t)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := p.argvLog(t)
	if len(got) != 2 {
		t.Fatalf("expected the update THEN the launch, got %v\n%s", got, out)
	}
	if got[0] != "RAN:update --self" {
		t.Errorf("the declared verb did not reach the program: %q", got[0])
	}
	if got[1] != "RAN:" {
		t.Errorf("the launcher must exec the program after updating it: %q", got[1])
	}
}

// TestNativeLauncherThrottlesOnTheStamp: a second invocation inside UPDATE_INTERVAL does no
// update work at all. Without this the vendor updater runs on every single invocation.
func TestNativeLauncherThrottlesOnTheStamp(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)

	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("first run rc=%d", rc)
	}
	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("second run rc=%d", rc)
	}
	got := p.argvLog(t)
	// update + launch, then launch alone.
	if len(got) != 3 {
		t.Fatalf("expected 3 invocations (update, launch, launch), got %v", got)
	}
	if got[2] != "RAN:" {
		t.Errorf("the second invocation must be a plain launch, got %q", got[2])
	}
}

// TestNativeLauncherHonoursAFrozenPolicy: `agent_updates` off means the emitted launcher
// carries no update branch it could be talked into — it launches and nothing else.
//
// The mutation this catches is the tempting one: reading the policy at RUN time from the
// environment instead of baking it. Then an agent that can set a variable can also unfreeze
// itself, and macos-user (which execs launchers under `env -i`) would read it as absent.
func TestNativeLauncherHonoursAFrozenPolicy(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, false, true)

	out, rc := p.run(t)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := p.argvLog(t)
	if len(got) != 1 || got[0] != "RAN:" {
		t.Errorf("a frozen pack must be launched and never updated, got %v\n%s", got, out)
	}
}

// TestNativeLauncherIsANoOpWhenReEntered is trap 4, and the symptom it prevents is a fork
// bomb rather than a wrong answer.
//
// B2 puts the launch dir ahead of the install prefixes, so a bare-name call of the program
// from INSIDE its own update resolves back to the launcher. The guard is asserted from the
// outside: the launcher is invoked with the guard variable already naming this bin, which
// is exactly the environment a vendor installer's child would see.
func TestNativeLauncherIsANoOpWhenReEntered(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)

	out, rc := p.run(t, "_YOLO_LAUNCHER_ACTIVE=:probetool")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := p.argvLog(t)
	if len(got) != 1 || got[0] != "RAN:" {
		t.Errorf("a re-entered launcher must exec the program with no update logic, got %v\n%s",
			got, out)
	}
	// And a DIFFERENT bin in the guard must not suppress this one's update — the variable
	// is a set, not a boolean, or the first launcher to run would freeze every other.
	q := newUpdateProbe(t, []string{"update"}, true, true)
	if _, rc := q.run(t, "_YOLO_LAUNCHER_ACTIVE=:someotherbin"); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := q.argvLog(t); len(got) != 2 {
		t.Errorf("another bin in the guard set must not suppress this one's update: %v", got)
	}
}

// TestNativeLauncherExportsTheReEntryGuard is the other half: the guard only works if the
// launcher's own children inherit it, and an unexported assignment would leave the fork
// bomb in place while the cell above still passed.
func TestNativeLauncherExportsTheReEntryGuard(t *testing.T) {
	p := newUpdateProbe(t, nil, true, true)
	body, err := os.ReadFile(p.script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `export _YOLO_LAUNCHER_ACTIVE=`) {
		t.Error("the re-entry guard must be EXPORTED — a vendor installer's child shell is " +
			"what re-enters, and it only sees exported variables")
	}
}

// TestNativeLauncherProceedsWithoutUpdatingWhenTheLockIsHeld is §3.5's contention rule,
// and both halves of it are the ruling: it must NOT wait and must NOT fail.
func TestNativeLauncherProceedsWithoutUpdatingWhenTheLockIsHeld(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	// Another writer already holds the install-prefix lock.
	if err := os.MkdirAll(filepath.Join(p.home, ".local", ".yolo-update.lock"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out, rc := p.run(t)
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the launcher waited %s for the lock — it must never block the command the "+
			"user typed", elapsed)
	}
	if rc != 0 {
		t.Errorf("a held lock must not fail the invocation, rc=%d\n%s", rc, out)
	}
	got := p.argvLog(t)
	if len(got) != 1 || got[0] != "RAN:" {
		t.Errorf("a held lock means launch-without-updating, got %v", got)
	}
	if !strings.Contains(out, "another update is in progress") {
		t.Errorf("...and it must SAY so, got:\n%s", out)
	}
}

// TestNativeLauncherBreaksAStaleLock: a holder that died must not freeze updates for the
// life of the home. Without this the lock is a one-way door.
func TestNativeLauncherBreaksAStaleLock(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	lock := filepath.Join(p.home, ".local", ".yolo-update.lock")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}

	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := p.argvLog(t); len(got) != 2 {
		t.Errorf("a lock older than STALE_LOCK must be broken and the update run, got %v", got)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the launcher must release the lock it took (err=%v)", err)
	}
}

// TestNativeLauncherReleasesTheLock: the normal path must leave the prefix unlocked, or the
// SECOND invocation an hour later takes the "in progress" arm forever.
func TestNativeLauncherReleasesTheLock(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(p.home, ".local", ".yolo-update.lock")); !os.IsNotExist(err) {
		t.Errorf("the lock survived a successful update (err=%v)", err)
	}
}

// TestNativeLauncherReportsAFailedUpdateAndStillLaunches: an update that fails is scoped to
// the update. The user asked to run the agent, not to update it, so a working binary must
// still run — and the failure must be said out loud rather than swallowed.
func TestNativeLauncherReportsAFailedUpdateAndStillLaunches(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	// Re-point the fake program at a body that fails for the update argv and succeeds for
	// the bare launch — the shape of a vendor updater that cannot reach its release host.
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
		"if [ \"${1:-}\" = update ]; then exit 7; fi\n"
	if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	out, rc := p.run(t)
	if rc != 0 {
		t.Errorf("a failed UPDATE must not fail the invocation — there is a working binary "+
			"to run: rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "update failed") {
		t.Errorf("the failure must be reported, got:\n%s", out)
	}
	got := p.argvLog(t)
	if len(got) != 2 || got[1] != "RAN:" {
		t.Errorf("the program must still be launched after a failed update, got %v", got)
	}
}

// TestNativeLauncherUpdateModeReportsTheVerbsStatus is trap 10: `yolo pack update` has no
// signal but the exit code, so a no-op that returns 0 makes the CLI report success for a
// refresh that refreshed nothing.
func TestNativeLauncherUpdateModeReportsTheVerbsStatus(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\nexit 7\n"
	if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	out, rc := p.run(t, "YOLO_PACK_UPDATE=1")
	if rc == 0 {
		t.Errorf("update mode must exit non-zero when the verb failed:\n%s", out)
	}
	if got := p.argvLog(t); len(got) != 1 {
		t.Errorf("update mode must refresh and EXIT, never launch: %v", got)
	}
}

// TestNativeLauncherUpdateModeIgnoresTheStampAndThePolicy: `yolo pack update` is the act a
// human performs, and §3.5 leaves it exactly one job the launcher cannot do — refreshing a
// pack whose `agent_updates` is false, now, without restarting the jail.
func TestNativeLauncherUpdateModeIgnoresTheStampAndThePolicy(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, false /* frozen */, true)
	seedFreshStamp(t, p.stamps, "probetool")

	out, rc := p.run(t, "YOLO_PACK_UPDATE=1")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	got := p.argvLog(t)
	if len(got) != 1 || got[0] != "RAN:update" {
		t.Errorf("an explicit `yolo pack update` must refresh a frozen, freshly-stamped "+
			"pack, got %v\n%s", got, out)
	}
}

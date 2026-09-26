package run

// command_refusal_test.go RUNS the composed container command — both branches of
// buildFinalInternalCmd — against fake steps, because every property it pins is a property
// of the composition rather than of any one constant: that a refusing bootstrap ends the
// command before the target (the container half of docs/reference/agent-program-runtimes.md
// OQ-AR3), that the refusal skips no unrelated step, that an ordinary failure still
// degrades, and that the Executing banner prints the target it is about to run.
//
// ⚠ THE LOG PATH IS REWRITTEN BEFORE ANYTHING RUNS. The composed bytes name
// /workspace/.yolo/startup.log, and inside a jail /workspace/.yolo is the LIVE session's
// state dir; running them as-is would truncate that session's startup log. finalCmdIn
// substitutes a temp path and refuses to run a command that still names /workspace/.yolo.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// stageFixture is a temp HOME with fake `mise`, a fake bootstrap exiting bootstrapRC, and a
// fake venv step that records that it ran.
type stageFixture struct {
	home, fakeBin, log, venvRan string
}

func newStageFixture(t *testing.T, bootstrapRC int) stageFixture {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH; the composed command is bash")
	}
	home := t.TempDir()
	f := stageFixture{
		home:    home,
		fakeBin: filepath.Join(home, "fake-bin"),
		log:     filepath.Join(home, "ws", ".yolo", "startup.log"),
		venvRan: filepath.Join(home, "venv-ran"),
	}
	for _, d := range []string{f.fakeBin, filepath.Dir(f.log)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		// `mise install --quiet` succeeds; `mise env -s bash` prints nothing to eval.
		filepath.Join(f.fakeBin, "mise"): "#!/bin/sh\nexit 0\n",
		filepath.Join(home, ".yolo-venv-precreate.sh"): "#!/bin/sh\ntouch " +
			shellQuoteForTest(f.venvRan) + "\n",
		filepath.Join(home, ".yolo-bootstrap.sh"): "#!/bin/sh\necho 'bootstrap says why' >&2\nexit " +
			strconv.Itoa(bootstrapRC) + "\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func shellQuoteForTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// run executes buildFinalInternalCmd(target, timing) in bash, stdin at /dev/null (the
// non-interactive shape every harnessed launch has).
func (f stageFixture) run(t *testing.T, target string, timing bool) (rc int, stdout, stderr string) {
	t.Helper()
	return f.runComposed(t, buildFinalInternalCmd(target, timing, true))
}

// runComposed runs an already-composed container command the way run does.
func (f stageFixture) runComposed(t *testing.T, composed string) (rc int, stdout, stderr string) {
	t.Helper()
	cmdText := finalCmdIn(t, composed, f.log)
	cmd := exec.Command("bash", "-c", cmdText)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "PATH=" + f.fakeBin + ":/bin:/usr/bin"}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the composed command could not be run: %v", err)
	}
	return rc, out.String(), errb.String()
}

// finalCmdIn points the composed command's startup log at logPath, and refuses to hand back
// anything that still names the jail's /workspace/.yolo (see the file comment).
func finalCmdIn(t *testing.T, composed, logPath string) string {
	t.Helper()
	if !strings.Contains(composed, startupLog) {
		t.Fatalf("the composed command no longer names %s, so this rewrite is stale:\n%s", startupLog, composed)
	}
	out := strings.ReplaceAll(composed, startupLog, logPath)
	if strings.Contains(out, "/workspace/.yolo") {
		t.Fatalf("refusing to run a command that still names /workspace/.yolo — inside a jail "+
			"that is the live session's state dir:\n%s", out)
	}
	return out
}

// targetMarker is printed only by the target itself: the Executing banner displays the
// command TEXT, `echo REACHED-$((40+2))`, and only running it prints REACHED-42.
const (
	targetCmdForTest = "echo REACHED-$((40+2))"
	targetMarker     = "REACHED-42"
)

// TestARefusedStageNeverReachesTheTarget is OQ-AR3's container half: "the jail does not
// start". A bootstrap exiting provision.RefusedStatus must end the command with that
// status, before miseActivate, the banner and the target — in BOTH branches, since the
// timing one has no golden and would otherwise be free to regress alone.
func TestARefusedStageNeverReachesTheTarget(t *testing.T) {
	for _, timing := range []bool{false, true} {
		f := newStageFixture(t, provision.RefusedStatus)
		rc, stdout, stderr := f.run(t, targetCmdForTest, timing)
		if rc != provision.RefusedStatus {
			t.Errorf("timing=%v: rc = %d, want provision.RefusedStatus (%d) — the container's "+
				"exit is how the host sees the refusal", timing, rc, provision.RefusedStatus)
		}
		if strings.Contains(stdout, targetMarker) {
			t.Errorf("timing=%v: the target ran after the stage refused the launch:\n%s", timing, stdout)
		}
		if strings.Contains(stderr, "Executing:") {
			t.Errorf("timing=%v: the Executing banner printed after a refusal, announcing a "+
				"command that must not run:\n%s", timing, stderr)
		}
		if !strings.Contains(stderr, "bootstrap says why") {
			t.Errorf("timing=%v: the refusing step's own message did not reach the console:\n%s", timing, stderr)
		}
		body, _ := os.ReadFile(f.log)
		if !strings.Contains(string(body), provision.FailedMarker) {
			t.Errorf("timing=%v: the refusal was not recorded in the startup log:\n%s", timing, body)
		}
	}
}

// TestARefusedFloorSkipsNoUnrelatedStep: the refusal costs nothing but the target. The
// venv step runs whatever the bootstrap does, because it sits BEFORE the bootstrap in
// setupScript — it used to follow it, and the `&&` join skipped it on any bootstrap failure.
func TestARefusedFloorSkipsNoUnrelatedStep(t *testing.T) {
	f := newStageFixture(t, provision.RefusedStatus)
	f.run(t, targetCmdForTest, false)
	if _, err := os.Stat(f.venvRan); err != nil {
		t.Errorf("the venv step did not run when the bootstrap refused — a Node floor must not "+
			"cost a workspace its python venv: %v", err)
	}
}

// TestAnOrdinaryBootstrapFailureStillReachesTheTarget is the vacuity guard for the two tests
// above and the half that must not move: any OTHER failure is recorded and degrades, and the
// target runs with its own status.
func TestAnOrdinaryBootstrapFailureStillReachesTheTarget(t *testing.T) {
	for _, timing := range []bool{false, true} {
		f := newStageFixture(t, 1)
		rc, stdout, _ := f.run(t, targetCmdForTest+"; exit 5", timing)
		if !strings.Contains(stdout, targetMarker) {
			t.Errorf("timing=%v: an ordinary failed bootstrap stopped the launch — only "+
				"provision.RefusedStatus may:\n%s", timing, stdout)
		}
		if rc != 5 {
			t.Errorf("timing=%v: rc = %d, want the target's own 5", timing, rc)
		}
	}
}

// TestExecutingBannerPrintsTheTargetVerbatim: the banner shows the command that is about to
// run, byte for byte. `%` and `\` used to be printf directives, because the target was
// spliced into the FORMAT: `stat -c "%u:%g %a"` printed as `stat -c "0:0 0x0p+0"`.
//
// Run through bash rather than grepped for an escape, and checked in both branches of
// buildFinalInternalCmd, so deleting either call site of executingBanner fails here.
func TestExecutingBannerPrintsTheTargetVerbatim(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	for _, target := range []string{
		"bash",
		`stat -c "%u:%g %a" /workspace`,
		`printf 'a\tb\n' %s %d`,
		`echo back\\slash \n \033 done`,
		`echo 'it'"'"'s' "$HOME" $(id -u) ` + "`id`",
		"%",
		`\`,
	} {
		banner := executingBanner(target, true)
		for _, timing := range []bool{false, true} {
			if !strings.Contains(buildFinalInternalCmd(target, timing, true), banner+"; "+target) {
				t.Errorf("timing=%v: buildFinalInternalCmd does not print executingBanner(%q) "+
					"immediately before the target", timing, target)
			}
		}
		out, err := exec.Command(bash, "-c", "{ "+banner+"; } 2>&1").Output()
		if err != nil {
			t.Errorf("the banner for %q does not run: %v", target, err)
			continue
		}
		if want := "\033[1;36m⚡ Executing: " + target + "\033[0m\n"; string(out) != want {
			t.Errorf("the banner mis-renders the target:\n got: %q\nwant: %q", out, want)
		}
	}
}

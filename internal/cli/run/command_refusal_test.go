package run

// command_refusal_test.go RUNS what the first session runs — the provisioning stage, then
// both branches of buildSessionCmd — against fake steps, because every property it pins is a
// property of the composition rather than of any one constant: that a refusing bootstrap ends
// the session before the target (the container half of docs/reference/agent-program-runtimes.md
// OQ-AR3), that the refusal skips no unrelated step, that an ordinary failure still
// degrades, and that the Executing banner prints the target it is about to run.
//
// ⚠ THE STAGE IS COMPOSED AT A TEMP LOG PATH BEFORE ANYTHING RUNS. The launch's bytes name
// /workspace/.yolo/startup.log, and inside a jail /workspace/.yolo is the LIVE session's
// state dir; running them as-is would truncate that session's startup log. stageAt composes
// the stage with startupLog pointed at a temp path, so the product's own quoting embeds it,
// and refuses to run a command that still names /workspace/.yolo.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// stageFixture is a temp HOME with fake `mise`, a fake bootstrap exiting bootstrapRC that records
// that it ran, and a fake venv step that records that it ran.
type stageFixture struct {
	home, fakeBin, log, venvRan, bootRan string
}

func newStageFixture(t *testing.T, bootstrapRC int) stageFixture {
	t.Helper()
	return newStageFixtureMise(t, bootstrapRC, 0)
}

// newStageFixtureMise is newStageFixture with `mise install` exiting miseInstallRC (offline, or
// a broken workspace mise.toml, when it is not 0). `mise env` and `mise ls` still succeed.
func newStageFixtureMise(t *testing.T, bootstrapRC, miseInstallRC int) stageFixture {
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
		bootRan: filepath.Join(home, "bootstrap-ran"),
	}
	for _, d := range []string{f.fakeBin, filepath.Dir(f.log)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		// `mise install --quiet` exits miseInstallRC; `mise env -s bash` prints nothing to eval.
		filepath.Join(f.fakeBin, "mise"): "#!/bin/sh\n[ \"$1\" = install ] && exit " +
			strconv.Itoa(miseInstallRC) + "\nexit 0\n",
		filepath.Join(home, ".yolo-venv-precreate.sh"): "#!/bin/sh\ntouch " +
			shellQuoteForTest(f.venvRan) + "\n",
		filepath.Join(home, ".yolo-bootstrap.sh"): "#!/bin/sh\ntouch " + shellQuoteForTest(f.bootRan) +
			"\necho 'bootstrap says why' >&2\nexit " + strconv.Itoa(bootstrapRC) + "\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func shellQuoteForTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// run executes the first session the way the entrypoint does (runFirstSession), with the
// stage and the command this launch composes, stdin at /dev/null (the non-interactive shape
// every harnessed launch has).
func (f stageFixture) run(t *testing.T, target string, timing bool) (rc int, stdout, stderr string) {
	t.Helper()
	return f.runFirstSession(t, func() string { return buildProvisionStage(true) },
		buildSessionCmd(target, timing, true))
}

// runFirstSession runs what the first session runs, in the entrypoint's order
// (entrypoint/jailmain.go): the stage as its own bash; then, only when the stage exited 0, the
// session's command in a second bash, with the stage's duration where the entrypoint puts it.
// A stage that exits non-zero is the session's status, and the command never runs — which is
// the entrypoint's gate, pinned in internal/entrypoint by
// TestARefusedProvisioningRefusesEveryWaiterWithItsStatus and TestMainWiresTheHoldAndTheGateInOrder.
//
// It takes the stage's COMPOSER rather than its bytes and composes it through stageAt itself,
// so no caller can hand it a stage that still logs to the jail's /workspace/.yolo.
func (f stageFixture) runFirstSession(t *testing.T, stage func() string, session string) (rc int, stdout, stderr string) {
	t.Helper()
	var out, errb strings.Builder
	rc = f.runBash(t, stageAt(t, f.log, stage), nil, &out, &errb)
	if rc != 0 {
		return rc, out.String(), errb.String()
	}
	rc = f.runBash(t, session, []string{entrypoint.ProvisionMillisEnv + "=12"}, &out, &errb)
	return rc, out.String(), errb.String()
}

func (f stageFixture) runBash(t *testing.T, text string, env []string, out, errb *strings.Builder) int {
	t.Helper()
	cmd := exec.Command("bash", "-c", text)
	cmd.Dir = f.home
	cmd.Env = append([]string{"HOME=" + f.home, "PATH=" + f.fakeBin + ":/bin:/usr/bin"}, env...)
	cmd.Stdout, cmd.Stderr = out, errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the composed command could not be run: %v", err)
	}
	return 0
}

// stageAt runs compose — buildProvisionStage, or a launch's own provisionStage — with
// startupLog pointed at logPath instead of the jail's, and refuses to hand back anything that
// still names the jail's /workspace/.yolo (see the file comment).
//
// It points startupLog itself rather than rewriting the composed text, because the stage
// embeds the path in shell source in two quoting contexts (shquote.Quote for the redirections,
// a double-quoted printf format for the console line), and only the composer knows which is
// which. A textual swap of the bare jail path put an UNQUOTED temp path into both, so a temp
// dir with a space in it split every redirection into two words. The package has no parallel
// tests, so nothing but this composition sees the swap.
func stageAt(t *testing.T, logPath string, compose func() string) string {
	t.Helper()
	jailLog := startupLog
	startupLog = logPath
	stage := func() string {
		defer func() { startupLog = jailLog }()
		return compose()
	}()
	if strings.Contains(stage, "/workspace/.yolo") {
		t.Fatalf("refusing to run a command that still names /workspace/.yolo — inside a jail "+
			"that is the live session's state dir, so the stage no longer takes its log path "+
			"from startupLog:\n%s", stage)
	}
	return stage
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

// TestAFailedMiseInstallStillRunsTheBootstrap is AR-L4 (docs/reference/agent-program-runtimes.md)
// on the container: a failed `mise install` used to skip the bootstrap through the `&&` join, so
// a workspace's broken mise.toml silenced the Node floor's refusal. The bootstrap now runs
// whatever `mise install` did. Its refusal still stops the launch; with no refusal the stage
// degrades as a failed `mise install` always did, recorded and continued, and the venv step,
// which needs `mise install`, is still skipped.
func TestAFailedMiseInstallStillRunsTheBootstrap(t *testing.T) {
	t.Run("the bootstrap refuses", func(t *testing.T) {
		f := newStageFixtureMise(t, provision.RefusedStatus, 1)
		rc, stdout, _ := f.run(t, targetCmdForTest, false)
		if _, err := os.Stat(f.bootRan); err != nil {
			t.Fatalf("a failed `mise install` skipped the bootstrap, so no Node floor was checked: %v", err)
		}
		if rc != provision.RefusedStatus {
			t.Errorf("rc = %d, want provision.RefusedStatus (%d): the refusal must win over the "+
				"earlier failure", rc, provision.RefusedStatus)
		}
		if strings.Contains(stdout, targetMarker) {
			t.Errorf("the target ran after the bootstrap refused:\n%s", stdout)
		}
	})
	t.Run("the bootstrap succeeds", func(t *testing.T) {
		f := newStageFixtureMise(t, 0, 1)
		rc, stdout, stderr := f.run(t, targetCmdForTest+"; exit 5", false)
		if _, err := os.Stat(f.bootRan); err != nil {
			t.Fatalf("a failed `mise install` skipped the bootstrap: %v", err)
		}
		if _, err := os.Stat(f.venvRan); err == nil {
			t.Error("the venv step ran after `mise install` failed; the steps before the " +
				"bootstrap stay joined with &&")
		}
		if !strings.Contains(stderr, "Provisioning failed (exit 1)") {
			t.Errorf("a failed `mise install` must still be reported with its own status:\n%s", stderr)
		}
		if !strings.Contains(stdout, targetMarker) || rc != 5 {
			t.Errorf("a failed `mise install` must still degrade and reach the target (rc %d):\n%s",
				rc, stdout)
		}
	})
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
// buildSessionCmd, so deleting either call site of executingBanner fails here.
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
			if !strings.Contains(buildSessionCmd(target, timing, true), banner+"; "+target) {
				t.Errorf("timing=%v: buildSessionCmd does not print executingBanner(%q) "+
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

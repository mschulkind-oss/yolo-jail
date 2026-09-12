package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The container's stage and macos-user's are two different subsets of the same six steps,
// run by two packages that cannot import each other. These tests pin the properties that
// have to hold for BOTH, because the alternative is each backend asserting its own copy
// and the two drifting apart in the one place nothing compares them.

// The FailedMarker is a cross-process contract with a reader in another package and a
// third reader that is PROSE shipped to every agent. A rename that kept both sides of the
// Go contract compiling would still invalidate the skill text, so the literal is pinned.
func TestFailedMarkerIsTheLiteralTheSkillAndTheBriefingLookFor(t *testing.T) {
	if FailedMarker != "PROVISIONING FAILED" {
		t.Fatalf("FailedMarker = %q. Two readers outside Go depend on this exact string: "+
			"jailcontent.ReadProvisioningFailed greps the startup log for it to decide "+
			"whether the briefing shows its banner, and the built-in diagnosing-the-jail "+
			"skill tells every agent to look for it. Renaming it makes a failed provision "+
			"report as healthy.", FailedMarker)
	}
}

// The marker has to reach the LOG, not only the console: the console line scrolls away
// and the log is what the next launch's briefing reads.
func TestScriptWritesTheMarkerIntoTheLog(t *testing.T) {
	s := Script(StartupLog("/ws"), Setup("false"))
	idx := strings.Index(s, FailedMarker)
	if idx < 0 {
		t.Fatal("the script never emits the marker")
	}
	if !strings.Contains(s[idx:], ">>/ws/.yolo/startup.log") {
		t.Errorf("the marker is printed but not appended to the log:\n%s", s[idx:])
	}
}

// A FAILED STAGE THAT NOBODY VETOED MUST NOT ABORT THE LAUNCH. The only path out with a
// non-zero status is the interactive `n` answer; a non-interactive run (no tty) has
// nobody to ask, and a jail whose tools did not install is still a jail the user asked
// for — the record is in the log.
func TestOnlyAnExplicitNoPropagatesAFailure(t *testing.T) {
	s := Script(StartupLog("/ws"), Setup("false"))
	if !strings.Contains(s, `if [ -t 0 ]`) {
		t.Error("the prompt is not gated on a tty, so a non-interactive launch would block " +
			"or abort on a failed stage")
	}
	if !strings.Contains(s, `case "$_ans" in [nN]*) exit "$_prc";; esac`) {
		t.Error("the only exit path is no longer the explicit `n` answer")
	}
}

// The stage's own exit status has to survive the pipe into tee, or a failure is invisible:
// the pipeline's status is tee's, which is 0 whenever tee could write.
func TestTheStagesStatusIsReadThroughPIPESTATUS(t *testing.T) {
	s := Script(StartupLog("/ws"), Setup("false"))
	if !strings.Contains(s, `_prc="${PIPESTATUS[0]}"`) {
		t.Error("the script reads the pipeline's status rather than the stage's; a failed " +
			"stage would report success because tee succeeded")
	}
}

// A workspace path is arbitrary on macos-user, where the container's was the fixed
// /workspace bind — so the path reaches the script in two places with two different
// quoting rules: single-quoted as a redirect target, and inside a DOUBLE-quoted printf
// format where a `$` would be expanded and a `"` would end the string early.
//
// ⚠ ASSERTED BY PARSING IT, not by grepping for escapes. A substring test over the
// composed string is the shape that reads plausible and checks nothing: the raw path
// appears LEGITIMATELY inside the single-quoted redirects, so "the raw path is absent"
// fails on a correct script, and "the escaped form is present" passes on one that is
// escaped in the safe position and raw in the dangerous one. `bash -n` is the only
// assertion here that distinguishes those.
func TestAnAwkwardWorkspacePathCannotBreakTheScript(t *testing.T) {
	for _, ws := range []string{
		`/Users/Shared/a"b$c`,
		`/Users/Shared/it's a repo`,
		"/Users/Shared/`whoami`",
		`/Users/Shared/back\slash`,
	} {
		assertParses(t, Script(StartupLog(ws), Setup(StepRunBootstrapAt(ws+"/.yolo/home/yolo-bootstrap.sh"))))
	}
}

// And the shapes both backends really emit, parsed the same way: a syntax error here is
// a launch that dies before it provisions anything.
func TestBothBackendsComposeParseableScripts(t *testing.T) {
	assertParses(t, Script(StartupLog("/workspace"), SetupBypassingShims(
		StepPruneStore, StepAnnounceMiseInstall, StepMiseInstall,
		StepAnnounceBootstrap, StepRunBootstrap, StepRunVenvPrecreate)))
	assertParses(t, Script(StartupLog("/Users/Shared/yolo/proj"), Setup(
		StepAnnounceMiseInstall, StepMiseInstall, StepAnnounceBootstrap,
		StepRunBootstrapAt("/Users/Shared/yolo/proj/.yolo/home/yolo-bootstrap.sh"))))
}

// assertParses runs `bash -n` over a composed script. Skipped where bash is absent —
// this is a parse check, and a machine without bash is not evidence either way.
func assertParses(t *testing.T, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH; the parse check needs one")
	}
	path := filepath.Join(t.TempDir(), "stage.sh")
	if err := os.WriteFile(path, []byte(script+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bash, "-n", path).CombinedOutput()
	if err != nil {
		t.Errorf("the composed stage script does not parse: %v\n%s\n--- script ---\n%s",
			err, out, script)
	}
}

// And the SAFE path — every container path, and every sane Mac one — must pass through
// untouched, which is what lets the container's golden stay byte-identical across the
// move of these bytes into this package.
func TestSafePathsAreUnchanged(t *testing.T) {
	if got := StartupLog("/workspace"); got != "/workspace/.yolo/startup.log" {
		t.Fatalf("StartupLog = %q", got)
	}
	if got := dquoteEscape("/workspace/.yolo/startup.log"); got != "/workspace/.yolo/startup.log" {
		t.Errorf("a safe path was rewritten: %q", got)
	}
}

// StepRunBootstrapAt is how a backend with no bind names the script. A path with a quote
// in it must not be able to close the command.
func TestBootstrapStepQuotesItsPath(t *testing.T) {
	if got := StepRunBootstrapAt("/ws/.yolo/home/yolo-bootstrap.sh"); got != "/ws/.yolo/home/yolo-bootstrap.sh >&2" {
		t.Errorf("a safe path was quoted unnecessarily: %q", got)
	}
	got := StepRunBootstrapAt(`/ws/a'b/yolo-bootstrap.sh`)
	if strings.HasPrefix(got, "/ws/a'b") {
		t.Errorf("an unquoted path with a single quote in it: %q", got)
	}
}

// The shim bypass is not cosmetic: the generated bootstrap script uses `find` and `grep`,
// and a jail that selects the guardrails pack refuses both with exit 127.
func TestSetupBypassingShimsActuallyBypasses(t *testing.T) {
	if !strings.HasPrefix(SetupBypassingShims("true"), "YOLO_BYPASS_SHIMS=1 sh -c '") {
		t.Error("the container's stage no longer bypasses the blocked-tool shims")
	}
}

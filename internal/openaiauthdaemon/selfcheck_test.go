package openaiauthdaemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
)

// The self-check is the openai-auth-broker loophole's doctor_cmd, and `yolo check` reads its
// stdout through nixdiag.SplitSelfCheckLines — the three-level "OK:" / "NOTE:" / "FAIL:"
// protocol. Every state below is asserted THROUGH that parser, because the defect these
// pin was output the grader could not read: on a fresh HOME the check printed
// `[FAIL] loophole openai-auth-broker: self-check failed (rc=1)` over the note "no output",
// while the self-check had in fact printed an ungraded "OpenAI authentication: unavailable:
// open …: no such file or directory" line and exited 1 for a machine that had simply never
// logged in.

// freshStatePath is the doctor_cmd's `{state}/credentials.json` on a machine that has never
// logged in: a temp HOME, and nothing under it.
func freshStatePath(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".local", "share", "yolo-jail", "openai-auth", "credentials.json")
}

// writeStateFixture writes a canonical state file with the given extra JSON members.
func writeStateFixture(t *testing.T, path, extra string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(2 * time.Hour).UnixMilli()
	body := `{"version":1,"access_token":"at-SECRET","id_token":"id-SECRET",` +
		`"refresh_token":"rt-SECRET","expires_at":` + strconv.FormatInt(expires, 10) +
		`,"account_id":"acct-123","generation":4` + extra + `}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runSelfCheck(t *testing.T, statePath string) (int, string, []nixdiag.GradedLine) {
	t.Helper()
	var out bytes.Buffer
	rc := selfCheck(statePath, &out)
	return rc, out.String(), nixdiag.SplitSelfCheckLines(out.String())
}

// TestSelfCheckOnAFreshHomeIsNotAFailure: no credential state is the normal state of a
// machine that has never logged in — the daemon's own `status` answers `logged_in: false`
// for it rather than an error — so the self-check exits 0 and says so in ONE graded NOTE
// naming the ways to log in, instead of a failure `yolo check` could not even read.
func TestSelfCheckOnAFreshHomeIsNotAFailure(t *testing.T) {
	rc, out, lines := runSelfCheck(t, freshStatePath(t))
	if rc != 0 {
		t.Errorf("rc = %d on a fresh home, want 0 (not logged in yet is not a failure):\n%s", rc, out)
	}
	if len(lines) != 1 || lines[0].Grade != nixdiag.GradeNote {
		t.Fatalf("want exactly one graded NOTE line, got %+v:\n%s", lines, out)
	}
	for _, want := range []string{"no OpenAI subscription login", "yolo host codex",
		"yolo openai-auth import"} {
		if !strings.Contains(out, want) {
			t.Errorf("the fresh-home note does not say %q:\n%s", want, out)
		}
	}
}

// TestSelfCheckGradesAGrantThatNeedsLogin: a grant OpenAI refused to refresh is a real
// failure — every jail borrowing it fails its next request — so it is a graded FAIL naming
// the refusal code and the remedy, with rc 1.
func TestSelfCheckGradesAGrantThatNeedsLogin(t *testing.T) {
	path := freshStatePath(t)
	writeStateFixture(t, path, `,"login_required":true,"last_error_code":"refresh_token_reused"`)
	rc, out, lines := runSelfCheck(t, path)
	if rc != 1 {
		t.Errorf("rc = %d, want 1:\n%s", rc, out)
	}
	if len(lines) == 0 || lines[0].Grade != nixdiag.GradeFail ||
		!strings.Contains(out, "refresh_token_reused") || !strings.Contains(out, "yolo host codex") {
		t.Errorf("a login-required grant is not a graded FAIL naming the code and the "+
			"remedy: %+v\n%s", lines, out)
	}
	assertNoSecrets(t, out)
}

// TestSelfCheckGradesUnreadableState: a state file that exists and does not decode is the
// genuine fault, so it is a graded FAIL carrying the path and the reason.
func TestSelfCheckGradesUnreadableState(t *testing.T) {
	path := freshStatePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, lines := runSelfCheck(t, path)
	if rc != 1 || len(lines) == 0 || lines[0].Grade != nixdiag.GradeFail ||
		!strings.Contains(out, path) {
		t.Errorf("an undecodable state file is not a graded FAIL naming its path "+
			"(rc=%d): %+v\n%s", rc, lines, out)
	}
}

// TestSelfCheckReportsAHealthyGrantAsGradedLines: a live grant passes with OK lines the
// grader renders — the account and the expiry — and never the tokens.
func TestSelfCheckReportsAHealthyGrantAsGradedLines(t *testing.T) {
	path := freshStatePath(t)
	writeStateFixture(t, path, "")
	rc, out, lines := runSelfCheck(t, path)
	if rc != 0 {
		t.Errorf("rc = %d for a healthy grant, want 0:\n%s", rc, out)
	}
	if len(lines) == 0 {
		t.Fatalf("a healthy grant printed no graded line — `yolo check` shows nothing it "+
			"measured:\n%s", out)
	}
	for _, l := range lines {
		if l.Grade != nixdiag.GradeOK {
			t.Errorf("a healthy grant drew a non-OK line %+v:\n%s", l, out)
		}
	}
	if !strings.Contains(out, "acct-123") {
		t.Errorf("the healthy report does not name the account:\n%s", out)
	}
	assertNoSecrets(t, out)
}

func assertNoSecrets(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "SECRET") {
		t.Errorf("the self-check printed token material:\n%s", out)
	}
}

// TestMainRoutesSelfCheckOnAFreshHome pins the CALL SITE: the doctor_cmd reaches selfCheck
// only through Main's --self-check branch. Without it Main demands --socket and returns 2,
// so a fresh machine's 0 is a code only that branch produces.
func TestMainRoutesSelfCheckOnAFreshHome(t *testing.T) {
	if rc := Main([]string{"--self-check", "--state-file", freshStatePath(t)}); rc != 0 {
		t.Fatalf("Main --self-check on a fresh home rc = %d, want 0", rc)
	}
}

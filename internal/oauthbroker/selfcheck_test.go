package oauthbroker

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
)

// writeCreds writes a shared-credentials file with the given expiresAt (ms) and
// returns its path, back-dating its mtime by mtimeAgo.
func writeCreds(t *testing.T, dir string, expiresAtMS int64, mtimeAgo time.Duration, now time.Time) string {
	t.Helper()
	p := filepath.Join(dir, ".credentials.json")
	body := []byte(`{"claudeAiOauth":{"expiresAt":` + strconv.FormatInt(expiresAtMS, 10) + `}}`)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	mt := now.Add(-mtimeAgo)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestGradeSharedCredsClock exercises the freshness grading's three branches
// through an injected clock: expired (FAIL), <1h (NOTE), healthy (OK).
//
// Ported from internal/cli/check's TestCredsFreshnessClock, which graded the same
// three cases against the same message text — the check moved behind the broker
// loophole's doctor_cmd, so the assertions moved with it. The exact strings are
// pinned on purpose: the REMAINING LIFETIME is the useful half of this check, and
// a DoctorResult only carries pass/fail, so the number survives to the user only
// as the text of a graded line.
func TestGradeSharedCredsClock(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0).UTC()
	nowMS := now.UnixMilli()

	cases := []struct {
		name        string
		expiresAtMS int64
		mtimeAgo    time.Duration
		wantGrade   string
		wantTitle   string
	}{
		{"expired", nowMS - int64(2*time.Hour/time.Millisecond), 3 * time.Hour,
			"FAIL", "shared creds expired 2h0m ago (last write 3h0m ago)"},
		{"expiring", nowMS + int64(30*time.Minute/time.Millisecond), 90 * time.Minute,
			"NOTE", "shared creds expire in 30m (last write 1h30m ago)"},
		{"healthy", nowMS + int64(5*time.Hour/time.Millisecond), 10 * time.Minute,
			"OK", "shared creds valid for 5h0m, last write 10m ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeCreds(t, dir, tc.expiresAtMS, tc.mtimeAgo, now)
			got := gradeSharedCreds(p, now)
			if len(got) != 1 {
				t.Fatalf("want exactly one graded line, got %#v", got)
			}
			if got[0].grade != tc.wantGrade || got[0].title != tc.wantTitle {
				t.Errorf("grade=%q title=%q, want %q / %q",
					got[0].grade, got[0].title, tc.wantGrade, tc.wantTitle)
			}
		})
	}
}

// TestGradeSharedCredsPreLogin: the two states that precede a first `/login` are
// not findings. An ABSENT file is a NOTE naming the fix; an EMPTY one is the
// documented placeholder `yolo run` creates so the bind mount has something to
// bind, and grading it would turn every fresh install into a warning.
func TestGradeSharedCredsPreLogin(t *testing.T) {
	dir := t.TempDir()

	absent := filepath.Join(dir, "nope.json")
	got := gradeSharedCreds(absent, time.Now())
	if len(got) != 1 || got[0].grade != "NOTE" || !strings.Contains(got[0].title, "`/login`") {
		t.Errorf("absent creds => %#v, want a single NOTE pointing at /login", got)
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gradeSharedCreds(empty, time.Now()); len(got) != 0 {
		t.Errorf("empty placeholder => %#v, want no findings", got)
	}
}

// TestGradeSharedCredsCorrupt: unparseable is a FAIL (the broker cannot refresh
// from it), but parseable-without-an-expiry is only a NOTE — that is what a
// half-written or logged-out file looks like, and it is not an outage.
func TestGradeSharedCredsCorrupt(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gradeSharedCreds(bad, time.Now()); len(got) != 1 || got[0].grade != "FAIL" {
		t.Errorf("corrupt creds => %#v, want a single FAIL", got)
	}

	noExp := filepath.Join(dir, "noexp.json")
	if err := os.WriteFile(noExp, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := gradeSharedCreds(noExp, time.Now())
	if len(got) != 1 || got[0].grade != "NOTE" || !strings.Contains(got[0].title, "expiresAt") {
		t.Errorf("creds without expiresAt => %#v, want a single NOTE naming expiresAt", got)
	}
}

// captureSelfCheckStdout redirects os.Stdout for the duration of body and returns what was
// written. SelfCheck prints with fmt.Println — that IS its wire — so this is the only seam.
func captureSelfCheckStdout(t *testing.T, body func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			sb.Write(buf[:n])
			if rerr != nil {
				break
			}
		}
		done <- sb.String()
	}()
	body()
	os.Stdout = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// primeBrokerState points BrokerDir() at a temp dir holding a CA and leaf, so SelfCheck's
// certificate half grades clean and the assertions below are about the credential half only.
// Without the files the openssl branch can add a FAIL on a machine without openssl, which
// would make this test's exit code depend on the host's PATH.
func primeBrokerState(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YOLO_BROKER_STATE_DIR", dir)
	for _, name := range []string{"ca.crt", "server.crt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// pinBrokerClock freezes nowMS() for the duration of the test.
func pinBrokerClock(t *testing.T, now time.Time) {
	t.Helper()
	saved := nowFunc
	nowFunc = func() int64 { return now.UnixMilli() }
	t.Cleanup(func() { nowFunc = saved })
}

// TestSelfCheckOutputSurvivesTheDoctorSeam pins THE WIRE, which is the only reason the
// remaining-lifetime number reaches a user at all.
//
// `yolo check` does not compute freshness any more (e2fe0f7): it runs the loophole's
// doctor_cmd, captures stdout, and hands it to nixdiag.SplitSelfCheckLines, which renders
// FAIL/NOTE/OK rows. A DoctorResult carries only a return code and that text — so the lifetime
// survives ONLY as the title of a correctly-prefixed line. The two tests either side of this
// one both pass while that link is broken: TestGradeSharedCredsClock stops at the internal
// struct, and check's TestReportSelfCheckLinesGradesTheWholeProtocol feeds the parser a
// hand-written string. Measured 2026-08-17: changing SelfCheck's `grade + ": " + title` to
// `grade + " " + title` left `go test -short ./...` entirely green, while `yolo check` would
// print "[FAIL] loophole claude-oauth-broker: self-check failed (rc=1) / no output" and the
// number would be gone. So this test drives the real SelfCheck and parses its real stdout.
func TestSelfCheckOutputSurvivesTheDoctorSeam(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	pinBrokerClock(t, now)

	t.Run("healthy creds arrive as an OK line carrying the lifetime", func(t *testing.T) {
		primeBrokerState(t)
		creds := writeCreds(t, t.TempDir(),
			now.UnixMilli()+int64(5*time.Hour/time.Millisecond), 10*time.Minute, now)

		var rc int
		out := captureSelfCheckStdout(t, func() { rc = SelfCheck(creds) })
		if rc != 0 {
			t.Errorf("rc = %d, want 0 for healthy creds\n%s", rc, out)
		}
		lines := nixdiag.SplitSelfCheckLines(out)
		if len(lines) != 1 {
			t.Fatalf("parsed %d graded lines, want exactly 1 (the trailing bare OK summary is "+
				"not one)\nraw output:\n%s\nparsed: %#v", len(lines), out, lines)
		}
		if lines[0].Grade != nixdiag.GradeOK {
			t.Errorf("grade = %v, want GradeOK — a healthy measurement must render as a PASS "+
				"row, not a warning", lines[0].Grade)
		}
		if lines[0].Title != "shared creds valid for 5h0m, last write 10m ago" {
			t.Errorf("title = %q, want the remaining lifetime verbatim", lines[0].Title)
		}
	})

	t.Run("expired creds arrive as a FAIL line with its remediation", func(t *testing.T) {
		primeBrokerState(t)
		creds := writeCreds(t, t.TempDir(),
			now.UnixMilli()-int64(2*time.Hour/time.Millisecond), 3*time.Hour, now)

		var rc int
		out := captureSelfCheckStdout(t, func() { rc = SelfCheck(creds) })
		// rc drives check's branch selection, and expired creds are the case that made
		// reportBrokerDaemon move out of the rc==0 branch.
		if rc != 1 {
			t.Errorf("rc = %d, want 1 for expired creds\n%s", rc, out)
		}
		lines := nixdiag.SplitSelfCheckLines(out)
		if len(lines) != 1 {
			t.Fatalf("parsed %d graded lines, want exactly 1\nraw output:\n%s", len(lines), out)
		}
		if lines[0].Grade != nixdiag.GradeFail {
			t.Errorf("grade = %v, want GradeFail", lines[0].Grade)
		}
		if lines[0].Title != "shared creds expired 2h0m ago (last write 3h0m ago)" {
			t.Errorf("title = %q, want the expired lifetime verbatim", lines[0].Title)
		}
		// The indented continuation must land as the finding's DETAIL, not as a second
		// finding — that is what puts the fix in front of the user.
		if !strings.Contains(lines[0].Detail, "Run /login from inside a jail") {
			t.Errorf("detail = %q, want the remediation text", lines[0].Detail)
		}
	})
}

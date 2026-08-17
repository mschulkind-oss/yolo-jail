package oauthbroker

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

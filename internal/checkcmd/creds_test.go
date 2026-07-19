package checkcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCreds writes a shared-credentials file with the given expiresAt (ms) and
// returns its path, back-dating its mtime by mtimeAgo.
func writeCreds(t *testing.T, home string, expiresAtMS int64, mtimeAgo time.Duration, now time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "yolo-jail", "home", ".claude-shared-credentials")
	must(t, os.MkdirAll(dir, 0o755))
	p := filepath.Join(dir, ".credentials.json")
	body := []byte(`{"claudeAiOauth":{"expiresAt":` + itoa(int(expiresAtMS)) + `}}`)
	must(t, os.WriteFile(p, body, 0o644))
	mt := now.Add(-mtimeAgo)
	must(t, os.Chtimes(p, mt, mt))
	return p
}

// TestCredsFreshnessClock exercises _check_broker_creds_freshness's three
// branches through the INJECTED CLOCK: expired (FAIL), <1h (WARN), healthy
// (PASS). The clock seam is what makes this time-dependent output.
func TestCredsFreshnessClock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Unix(1_700_000_000, 0).UTC()
	nowMS := now.UnixMilli()

	cases := []struct {
		name        string
		expiresAtMS int64
		mtimeAgo    time.Duration
		wantBadge   string
		wantSubstr  string
	}{
		{"expired", nowMS - int64(2*time.Hour/time.Millisecond), 3 * time.Hour, "[FAIL]", "shared creds expired 2h0m ago (last write 3h0m ago)"},
		{"expiring", nowMS + int64(30*time.Minute/time.Millisecond), 90 * time.Minute, "[WARN]", "shared creds expire in 30m (last write 1h30m ago)"},
		{"healthy", nowMS + int64(5*time.Hour/time.Millisecond), 10 * time.Minute, "[PASS]", "shared creds valid for 5h0m, last write 10m ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeCreds(t, home, tc.expiresAtMS, tc.mtimeAgo, now)
			var out bytes.Buffer
			o := Options{
				Now:        func() time.Time { return now },
				Getenv:     func(string) string { return "" },
				LookPath:   func(string) (string, bool) { return "", false },
				Exec:       func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} },
				Stdout:     &out,
				PathExists: func(p string) bool { _, err := os.Stat(p); return err == nil },
			}
			fillDefaults(&o)
			r := newReporter(&out, false)
			o.checkBrokerCredsFreshness(r)
			got := out.String()
			if !contains(got, tc.wantBadge) {
				t.Errorf("%s: want badge %q in %q", tc.name, tc.wantBadge, got)
			}
			if !contains(got, tc.wantSubstr) {
				t.Errorf("%s: want %q in output:\n%s", tc.name, tc.wantSubstr, got)
			}
		})
	}
}

// TestCredsFreshnessNoFile: absent creds → nothing graded (empty output).
func TestCredsFreshnessNoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	o := Options{Now: time.Now, Getenv: func(string) string { return "" }, Stdout: &out,
		PathExists: func(string) bool { return false }}
	fillDefaults(&o)
	r := newReporter(&out, false)
	o.checkBrokerCredsFreshness(r)
	if out.Len() != 0 {
		t.Errorf("expected no output for missing creds, got %q", out.String())
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

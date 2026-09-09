package cgd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuditorRecordsPidOpCgroupAndResult pins the four things Principle 5 of
// docs/reference/security-shim.md entitles an auditor to expect.
func TestAuditorRecordsPidOpCgroupAndResult(t *testing.T) {
	dir := t.TempDir()
	a := NewAuditor(dir, nil)
	a.Append(AuditEvent{PeerPID: 4242, Op: "create_and_join", Cgroup: "/sys/fs/cgroup/x", OK: true})

	b, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	got := string(b)
	for _, want := range []string{"pid=4242", "op=create_and_join", "cgroup=/sys/fs/cgroup/x", "result=ok"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit line missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("one Append must write exactly one line, got %d:\n%s", strings.Count(got, "\n"), got)
	}
}

// TestAuditorRecordsFailuresAsFailures — a trail that only holds successes
// answers the wrong question. The interesting entry is the refused one.
func TestAuditorRecordsFailuresAsFailures(t *testing.T) {
	dir := t.TempDir()
	a := NewAuditor(dir, nil)
	a.Append(AuditEvent{PeerPID: 7, Op: "set_limits", Cgroup: "/c", OK: false, Err: "no such controller"})

	b, _ := os.ReadFile(a.Path())
	got := string(b)
	if !strings.Contains(got, "result=ERR") || !strings.Contains(got, "error=no such controller") {
		t.Errorf("a refused operation must be recorded as refused, with its reason:\n%s", got)
	}
}

// TestAuditorRendersAnUnparseableRequest pins that "" op does not vanish. An
// unparseable request is what a probe of the socket looks like from the host
// side, so it is the last thing that may be logged as blank.
func TestAuditorRendersAnUnparseableRequest(t *testing.T) {
	dir := t.TempDir()
	a := NewAuditor(dir, nil)
	a.Append(AuditEvent{PeerPID: 9, Err: "request did not parse"})

	b, _ := os.ReadFile(a.Path())
	if got := string(b); !strings.Contains(got, "op=UNPARSEABLE") {
		t.Errorf("an unparseable request must be visible as such, not blank:\n%s", got)
	}
}

// TestAuditorRefusesLogInjection is the one adversarial case. Op and cgroup
// names arrive from the UNTRUSTED side of the boundary, so a newline in either
// would let a caller forge an entry in the file whose whole purpose is to be
// trusted afterwards.
func TestAuditorRefusesLogInjection(t *testing.T) {
	dir := t.TempDir()
	a := NewAuditor(dir, nil)
	a.Append(AuditEvent{
		PeerPID: 1,
		Op:      "join\n2026-01-01T00:00:00Z\tpid=0\top=forged\tcgroup=-\tresult=ok",
		Cgroup:  "/c",
		OK:      true,
	})

	b, _ := os.ReadFile(a.Path())
	got := string(b)

	// THE PROPERTY IS "cannot forge a LINE", not "cannot contain the bytes".
	// An escaped payload still holds the substring `op=forged` inside one field,
	// and that is harmless: every reader of this file — a human with grep, or a
	// parser — works line by line, so a payload that cannot end a line cannot
	// become an entry. Asserting on the substring instead would fail against
	// correct code, which is how a real check gets loosened into a fake one.
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("a newline in an untrusted field forged %d lines:\n%s", len(lines), got)
	}
	// The payload carried four tabs of its own. A correct line has exactly the
	// separators THIS code wrote — 4 for the five always-present fields — so
	// counting them proves the untrusted field contributed none. That is the
	// parser-facing half of the property; the line count above is the human-facing
	// half.
	if tabs := strings.Count(lines[0], "\t"); tabs != 4 {
		t.Errorf("want 4 field separators (the ones this code writes), got %d — "+
			"the payload contributed separators and could shift a parser's field map:\n%s",
			tabs, got)
	}
	if !strings.Contains(got, `op=join\n`) {
		t.Errorf("the newline should be present as an ESCAPE, proving it was neutralised "+
			"rather than stripped (stripping would lose evidence of the attempt):\n%s", got)
	}
}

// TestAuditorNeverFailsTheOperationAndSaysSoOnce covers the best-effort
// contract: an unwritable log must not break a cgroup operation, and must not
// go quiet about it either.
func TestAuditorNeverFailsTheOperationAndSaysSoOnce(t *testing.T) {
	dir := t.TempDir()
	// A regular file where the log dir must be makes MkdirAll fail.
	blocked := filepath.Join(dir, "logs")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	var warnings []string
	a := NewAuditor(blocked, func(m string) { warnings = append(warnings, m) })
	a.Append(AuditEvent{PeerPID: 1, Op: "a", OK: true}) // must not panic
	a.Append(AuditEvent{PeerPID: 2, Op: "b", OK: true})
	a.Append(AuditEvent{PeerPID: 3, Op: "c", OK: true})

	if len(warnings) != 1 {
		t.Errorf("want exactly one warning for a persistently unwritable log, got %d: %v",
			len(warnings), warnings)
	}
	if len(warnings) == 1 && !strings.Contains(warnings[0], "UNRECORDED") {
		t.Errorf("the warning must say operations are continuing unrecorded, got %q", warnings[0])
	}
	if n := a.Appended(); n != 0 {
		t.Errorf("a failed append must not be counted as written, got Appended()=%d", n)
	}
}

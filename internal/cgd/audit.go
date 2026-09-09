package cgd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// audit.go implements Principle 5 of docs/reference/security-shim.md: every
// cgroup operation is recorded with the caller's host PID, the operation and the
// result, on the host filesystem outside the container's reach.
//
// IT WAS UNIMPLEMENTED UNTIL 2026-09-09, and the shape of the miss is worth
// keeping. RequestOp has always carried the doc comment "extracts the op field
// ... for the audit log" and had no production caller: the in-process server
// parsed a request, resolved the container cgroup, answered, and wrote nothing.
// The principle was stated in the design, the helper it needed existed, and the
// one line joining them was never written — so an audit of the shim would have
// found a documented trail that produced no bytes.
//
// WHY THIS IS BEST-EFFORT, stated because "best-effort audit log" deserves an
// argument rather than a shrug. Refusing a cgroup operation because its log line
// could not be appended would turn a full disk into a jail that cannot set a
// memory limit, and the delegate is a convenience surface — the security boundary
// is the three-operation protocol and the container-scoped cgroup root, not this
// file. So a write failure never fails the operation. It is also never silent:
// the first failure per process is reported through the caller's own failure sink,
// because an audit trail that stops without saying so is worse than none.
//
// The log is APPEND-ONLY from this process's side and lives under the host's
// state dir, which a jail cannot write. It is not a tamper-proof store and does
// not claim to be; it is a forensic record of what crossed the boundary.

// AuditEvent is one line of the delegate's audit trail.
type AuditEvent struct {
	// PeerPID is the connecting process's HOST pid, from SO_PEERCRED. Zero when
	// the platform or the socket could not supply it — recorded as 0 rather than
	// omitted, so a missing credential is visible instead of looking like absence
	// of a caller.
	PeerPID int
	// Op is the requested operation, or "" when the request did not parse. An
	// unparseable request IS an event: it is what a probe looks like from here.
	Op string
	// Cgroup is the container cgroup the operation was scoped to.
	Cgroup string
	// OK is the answer the handler produced.
	OK bool
	// Err is the handler's error text when OK is false, or the reason a request
	// was rejected before dispatch.
	Err string
}

// Auditor appends AuditEvents to a file. Safe for concurrent use: the delegate
// serves each connection on its own goroutine.
type Auditor struct {
	path string
	warn func(string) // reports the first write failure; may be nil

	mu       sync.Mutex
	warned   bool
	appended int
}

// NewAuditor returns an Auditor writing to logDir/cgroup-delegate-audit.log.
// warn is called at most once, with a human-readable reason, if appending ever
// fails; pass nil to discard. It does NOT create logDir — Append does, so a
// delegate that never receives a request leaves no directory behind.
func NewAuditor(logDir string, warn func(string)) *Auditor {
	return &Auditor{path: filepath.Join(logDir, "cgroup-delegate-audit.log"), warn: warn}
}

// Path is where this Auditor appends. Exported for the launch banner and tests.
func (a *Auditor) Path() string { return a.path }

// Appended is how many lines this Auditor has written. Tests use it to tell
// "logged nothing" from "logged something unreadable".
func (a *Auditor) Appended() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appended
}

// Append writes one line. Never returns an error: see the best-effort argument
// in this file's header.
func (a *Auditor) Append(ev AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.appendLocked(ev); err != nil {
		// Warn ONCE per process, but return on every failure — an earlier warning
		// must not make a later failed append count as written.
		if !a.warned {
			a.warned = true
			if a.warn != nil {
				a.warn("cgroup-delegate audit log unwritable at " + a.path + ": " + err.Error() +
					" — operations continue UNRECORDED until this is fixed")
			}
		}
		return
	}
	a.appended++
}

func (a *Auditor) appendLocked(ev AuditEvent) error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(ev.line())
	return err
}

// line renders one event. Fixed field order, tab-separated, one line — greppable
// by pid or op without a parser, which is what someone reading an audit trail
// under pressure actually does.
func (ev AuditEvent) line() string {
	op := ev.Op
	if op == "" {
		op = "UNPARSEABLE"
	}
	result := "ok"
	if !ev.OK {
		result = "ERR"
	}
	fields := []string{
		time.Now().UTC().Format(time.RFC3339),
		fmt.Sprintf("pid=%d", ev.PeerPID),
		"op=" + sanitizeAuditField(op),
		"cgroup=" + sanitizeAuditField(ev.Cgroup),
		"result=" + result,
	}
	if ev.Err != "" {
		fields = append(fields, "error="+sanitizeAuditField(ev.Err))
	}
	return strings.Join(fields, "\t") + "\n"
}

// sanitizeAuditField keeps one event on one line. Op and cgroup names reach here
// from the UNTRUSTED side of the boundary, so a newline in either would let a
// caller forge an audit entry — the log-injection shape, in the one file whose
// whole purpose is to be trusted later.
func sanitizeAuditField(s string) string {
	if s == "" {
		return "-"
	}
	r := strings.NewReplacer("\n", "\\n", "\r", "\\r", "\t", "\\t")
	return r.Replace(s)
}

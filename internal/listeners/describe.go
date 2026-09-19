package listeners

// describe.go is the PROSE half of this package, for the callers that append one
// sentence to an error rather than paste a table into a bug report.
//
// [Render] is for a human reading a whole snapshot. These are for a line that has
// to say who holds a port inside a bind failure or a skipped port forward — a
// context where the reader gets one sentence and no second chance to ask.
//
// They live here rather than in either caller because the hard part is not the
// wording, it is the TRI-STATE: an owner-less socket means "nobody in this
// namespace holds it" when the walk was exhaustive and "the walk could not look"
// when it was not, and the two must never be spelled alike. That reasoning was
// written once in internal/wirebridged, and the second caller (the entrypoint's
// port-forward skip) would have been the second copy of it. The framing sentence
// stays with each caller, because it is the part that differs.

import (
	"fmt"
	"strconv"
	"strings"
)

// maxArgv is where an owner's argv is cut for a one-sentence report. Longer than
// [maxCmdline] — a sentence has a whole line rather than a table column — and
// bounded for the same reason: this is an arbitrary process's argv, and on the
// wire-bridge path the sentence crosses a line-framed readiness pipe.
const maxArgv = 400

// DescribeGaps renders why a snapshot could not answer. Every gap is named rather
// than summarised to a count, because the reader's next action depends on WHICH
// read failed: an unreadable /proc/net/tcp and an unreadable /proc/<pid>/fd lead
// to different places.
func DescribeGaps(s Snapshot) string {
	if len(s.Gaps) == 0 {
		return fmt.Sprintf("the socket tables reported %s availability with no stated reason",
			s.Availability())
	}
	parts := make([]string, 0, len(s.Gaps))
	for _, g := range s.Gaps {
		parts = append(parts, g.Source+": "+g.Reason)
	}
	return strings.Join(parts, "; ")
}

// DescribeOwners names the processes holding one socket, or says precisely why it
// is unnamed. An unattributed socket is reported AS unattributed — the socket
// exists and is the conflict whether or not its owner is readable from this uid,
// and a caller told only "no owner" would conclude the port is free.
//
// The three unowned arms are the tri-state, and collapsing any two is the defect
// this package exists to prevent:
//
//   - attribution never ran, so the owner was not looked up;
//   - it ran and was incomplete, so the owner may simply not have been reached;
//   - it ran exhaustively, so no readable /proc/<pid>/fd points at the socket and
//     the holder belongs to another user or another namespace.
func DescribeOwners(l Listener, s Snapshot) string {
	if len(l.Owners) == 0 {
		switch {
		case !s.AttributionRan:
			return fmt.Sprintf("owned by socket inode %d (uid %d), whose process was not looked "+
				"up: %s", l.Inode, l.UID, DescribeGaps(s))
		case !s.AttributionComplete():
			return fmt.Sprintf("owned by socket inode %d (uid %d), whose process could not be "+
				"identified: the owner search was incomplete (%d of %d processes readable, "+
				"capped=%v)", l.Inode, l.UID, s.PIDsScanned, s.PIDsFound, s.Capped)
		default:
			return fmt.Sprintf("owned by socket inode %d (uid %d), and no readable /proc/<pid>/fd "+
				"entry points at it — the holder is another user's or another namespace's process",
				l.Inode, l.UID)
		}
	}
	parts := make([]string, 0, len(l.Owners))
	for _, o := range l.Owners {
		desc := "held by pid " + strconv.Itoa(o.PID)
		if o.Comm != "" {
			desc += " (" + o.Comm + ")"
		}
		if argv := BoundArgv(o.Cmdline); argv != "" {
			desc += ", argv: " + argv
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, " and ")
}

// BoundArgv renders an owner's argv for a one-line record: single-lined and
// bounded, because it is arbitrary process input and at least one caller writes it
// to a line-framed pipe where an embedded newline would be read as a second
// record.
func BoundArgv(cmdline string) string {
	argv := strings.Join(strings.Fields(cmdline), " ")
	if len(argv) > maxArgv {
		argv = argv[:maxArgv] + "…(truncated)"
	}
	return argv
}

package wirebridged

// portholder.go answers the one question the 127.0.0.1:8214 bind collision left
// unanswerable: WHAT ALREADY HOLDS THE PORT.
//
// A bind failure's errno says "address already in use" and stops there, and the
// obvious host-side instrument does not help — `ss -ltnp` on the HOST was empty,
// because the conflicting listener was a container-side socat and the host half
// of that forward is a UNIX socket. The holder was in the jail the whole time,
// one /proc read away, and no log on either side had asked.
//
// The /proc reading itself is NOT here. It is [listeners], which exists for this
// exact measurement and is the only copy: this file is the SENTENCE, that package
// is the instrument. The two were written the same day by different hands and the
// duplicate parser that used to sit here was deleted in favour of the one that is
// bounded, covers net/unix, and reports its own partiality — see the note on
// budgets below.
//
// TRI-STATE, like every other probe in this repo: "nothing holds it", "something
// holds it and this is what" and "I could not ask" are three different answers
// and are never collapsed. A probe that cannot read /proc says so rather than
// reporting an empty table as an absence — that is exactly how the host's empty
// `ss` output misled four hypotheses in a row. The three arms map onto
// [listeners.Snapshot.Availability] plus whether the port was found, so the
// distinction is the instrument's rather than this file's to get right.

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/listeners"
)

// portHolderSnapshot is how this file reads the machine, as a variable only so a
// test can point it at a fixture tree; production never rebinds it.
//
// It takes ONE snapshot and asks it about the port, rather than walking /proc once
// per socket inode the way the deleted parser here did. That walk was unbounded by
// construction — one full /proc tree readlink sweep per matching listener, and a
// dual-stack collision matches two — on the failure path of a boot. [listeners]
// attributes every socket in a single sweep under [listeners.DefaultMaxFDLinks].
var portHolderSnapshot = listeners.Collect

// describePortHolder is the sentence appended to a bind failure. It always
// returns something sayable: the holder, the unidentifiable holder, the absence,
// or the reason it could not look.
func describePortHolder(addr string) string {
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("the port holder was not looked up: %q is not a host:port address (%v)", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Sprintf("the port holder was not looked up: port %q is not numeric (%v)", portText, err)
	}

	snap := portHolderSnapshot()
	found := snap.OnPort(port)
	if len(found) == 0 {
		// COULD NOT ASK vs NOTHING HOLDS IT. Both are an empty result one layer
		// down, and reading them alike is the mistake this package is fixing. Only
		// a COMPLETE snapshot can assert an absence; anything less says so.
		if snap.Availability() != listeners.Complete {
			return "the port holder could not be identified: " + describeGaps(snap)
		}
		return fmt.Sprintf("no LISTEN socket on port %d appears in this network namespace's "+
			"socket tables, so the conflict is not a listener here — a socket in another state, "+
			"another netns, or a SO_REUSEADDR race are what is left", port)
	}

	parts := make([]string, 0, len(found))
	for _, l := range found {
		parts = append(parts, describeListener(l, snap))
	}
	return "the address is already held: " + strings.Join(parts, "; ")
}

// describeGaps renders why the snapshot could not answer. Every gap is named
// rather than summarised to a count: on this path the reader's next action depends
// on WHICH read failed.
func describeGaps(snap listeners.Snapshot) string {
	if len(snap.Gaps) == 0 {
		return fmt.Sprintf("the socket tables reported %s availability with no stated reason",
			snap.Availability())
	}
	parts := make([]string, 0, len(snap.Gaps))
	for _, g := range snap.Gaps {
		parts = append(parts, g.Source+": "+g.Reason)
	}
	return strings.Join(parts, "; ")
}

// describeListener is one holder, in the bind error's own vocabulary.
func describeListener(l listeners.Listener, snap listeners.Snapshot) string {
	overlap := ""
	if l.Addr.IsValid() && l.Addr.IsUnspecified() {
		// A wildcard listener (0.0.0.0/::) is the common reason a specific
		// loopback bind fails, and a reader who only saw "already in use" would
		// hunt for a listener on the exact address and find none. This asks the
		// typed address whether it is unspecified rather than comparing the
		// rendered string to the wanted host, which also called a listener on a
		// DIFFERENT specific address an overlap — it cannot be one.
		overlap = " (a listener on this address covers the one the bridge wanted)"
	}
	return fmt.Sprintf("%s%s %s [%s]", l.Local(), overlap, describeOwners(l, snap), l.Kind)
}

// describeOwners names the holding process, or says precisely why it is unnamed.
// An unattributed socket is reported AS unattributed — the socket exists and is
// the conflict whether or not its owner is readable from this uid.
func describeOwners(l listeners.Listener, snap listeners.Snapshot) string {
	if len(l.Owners) == 0 {
		switch {
		case !snap.AttributionRan:
			return fmt.Sprintf("owned by socket inode %d (uid %d), whose process was not looked "+
				"up: %s", l.Inode, l.UID, describeGaps(snap))
		case !snap.AttributionComplete():
			return fmt.Sprintf("owned by socket inode %d (uid %d), whose process could not be "+
				"identified: the owner search was incomplete (%d of %d processes readable, "+
				"capped=%v)", l.Inode, l.UID, snap.PIDsScanned, snap.PIDsFound, snap.Capped)
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
		if argv := boundArgv(o.Cmdline); argv != "" {
			desc += ", argv: " + argv
		}
		parts = append(parts, desc)
	}
	return strings.Join(parts, " and ")
}

// boundArgv renders a holder's argv for the record. It is bounded and
// single-lined: this string reaches the readiness pipe, whose protocol is one line
// per record, and the argv of an arbitrary process is arbitrary bytes.
func boundArgv(cmdline string) string {
	argv := oneLine(strings.TrimSpace(cmdline))
	const max = 400
	if len(argv) > max {
		argv = argv[:max] + "…(truncated)"
	}
	return argv
}

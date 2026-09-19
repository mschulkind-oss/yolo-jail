// Package listeners answers one question from inside a jail, with no external
// binaries: WHICH PROCESS HOLDS WHICH LISTENING SOCKET.
//
// It exists because that measurement existed nowhere. A provider-table aliasing
// bug once had socat bind 127.0.0.1:8214 four lines before the supervisor started
// the wire bridge, and the bridge's bind then failed; `ss -ltnp` on the HOST came
// back empty (the host half of that path is a UNIX socket), which sent the
// investigation into a blind alley for four wrong hypotheses. The whole point of
// this package is that the answer is read from inside the namespace that owns the
// socket, out of /proc, with no `ss`, no `lsof` and no `netstat` — the jail cannot
// assume host tools, and the macos-user backend bakes nothing.
//
// # Three properties, each deliberate
//
// PARTIAL IS THE NORMAL CASE, AND IT IS NOT EMPTY. "nothing is listening" and "I
// could not read /proc" are the same empty slice, and this repo's standing rule is
// that a component which cannot ask the runtime declines rather than answering —
// so a [Snapshot] carries both what was learned and what could not be
// ([Snapshot.Gaps], [Snapshot.Availability]). Attribution is partial more often
// than not: a socket held by a process in another PID namespace, or by a pid whose
// /proc/<pid>/fd this process may not read, is normal and not an error.
//
// IT CANNOT FAIL THE CALLER. Every entry point returns a value, never an error.
// A diagnostic that can break the thing it is diagnosing is worse than no
// diagnostic, so there is no panic, no fatal path, and no unbounded work.
//
// WORK IS BOUNDED BY CONSTRUCTION, not by a clock. Reading /proc/*/fd is one
// readlink per file descriptor on the machine, so the walk stops at
// [DefaultMaxPIDs] processes and [DefaultMaxFDLinks] readlinks and says it
// stopped ([Snapshot.Capped]). There is no wall-clock deadline on purpose: the
// budgets make the cost reproducible, which a deadline would not, and nothing
// here can block — readlink on /proc/<pid>/fd does not wait on the process.
// When nothing is listening the fd walk does not run at all.
package listeners

import (
	"net/netip"
	"sort"
	"strconv"
)

// Kind is the socket family a listener was read from, named for the /proc file it
// came out of.
type Kind string

const (
	KindTCP  Kind = "tcp"
	KindTCP6 Kind = "tcp6"
	KindUnix Kind = "unix"
)

// Listener is one listening socket, plus whatever could be attributed to it.
type Listener struct {
	Kind Kind
	// Addr and Port are the bound local address; zero and 0 for KindUnix.
	//
	// Addr keeps the /proc spelling rather than a normalized one: an IPv4-mapped
	// address in net/tcp6 stays "::ffff:127.0.0.1" because which table a socket
	// is in is part of the answer when a bind collides.
	Addr netip.Addr
	Port int
	// Path is the UNIX socket's name: an absolute path, "@name" for an abstract
	// socket (the kernel's own spelling of the leading NUL), or "" for an unnamed
	// one. Empty for the TCP kinds.
	Path string
	// Inode is the socket inode, the join key to /proc/<pid>/fd.
	Inode uint64
	// UID is the socket's owning uid as the table reported it, or -1 if it did not.
	UID int
	// Owners is every process found holding a descriptor for Inode, ascending by
	// pid. Empty means NOT FOUND, which is not the same as "no owner exists" —
	// see [Snapshot.AttributionComplete].
	Owners []Owner
}

// Local renders the bound address the way a bind error would name it:
// "127.0.0.1:8214", "[::]:8080", or the UNIX path.
func (l Listener) Local() string {
	if l.Kind == KindUnix {
		if l.Path == "" {
			return "(unnamed)"
		}
		return l.Path
	}
	if !l.Addr.IsValid() {
		return ":" + strconv.Itoa(l.Port)
	}
	return netip.AddrPortFrom(l.Addr, uint16(l.Port)).String()
}

// Owner is a process found holding a descriptor for a listening socket.
type Owner struct {
	PID int
	// Comm is /proc/<pid>/comm, the 15-char kernel name, or "" if unreadable.
	Comm string
	// Cmdline is /proc/<pid>/cmdline with its NULs turned into spaces, or "" if
	// unreadable (a kernel thread has an empty cmdline, legitimately).
	Cmdline string
}

// Gap is one thing the collector could not read. A Gap is never an error the
// caller has to handle; it is the half of the answer that says how much of the
// answer to trust.
type Gap struct {
	// Source is the /proc-relative name that failed, or the scan phase that was
	// cut short ("fd-scan").
	Source string
	Reason string
}

// Availability is the tri-state a caller reads before believing an empty
// [Snapshot].
type Availability int

const (
	// Unavailable means no socket table could be read at all: not Linux, no
	// /proc, or /proc unreadable. The socket list means nothing.
	Unavailable Availability = iota
	// Partial means at least one table was read and at least one was not, or a
	// line in one did not parse. Sockets listed are real; sockets missing may
	// not be.
	Partial
	// Complete means every socket table parsed cleanly. The list is the whole
	// list — an empty one really does mean nothing is listening.
	Complete
)

func (a Availability) String() string {
	switch a {
	case Unavailable:
		return "unavailable"
	case Partial:
		return "partial"
	case Complete:
		return "complete"
	default:
		return "unknown"
	}
}

// Snapshot is one reading: the sockets found, the owners attributed, and the
// gaps. Every count is here rather than folded into a single "ok" bool because
// the question a caller asks after a bind failure — "is this list missing the
// process that beat me to the port?" — is answerable only from the gaps.
type Snapshot struct {
	// Sockets is the listening sockets found, in a deterministic order (kind,
	// then port, then address, then inode).
	Sockets []Listener
	// Gaps is everything that could not be read, one entry per failed source or
	// per cut-short phase.
	Gaps []Gap

	// TablesAttempted/TablesRead count the socket tables (net/tcp, net/tcp6,
	// net/unix). TablesRead == 0 is the "could not ask" answer.
	TablesAttempted int
	TablesRead      int
	// MalformedLines counts lines in a table that were read but did not parse.
	// Non-zero means a socket may be missing from Sockets.
	MalformedLines int

	// PIDsFound is the numeric entries in /proc; PIDsScanned the ones whose fd
	// directory was listed; PIDsUnreadable the ones that refused (another user,
	// or exited mid-walk — both normal).
	PIDsFound      int
	PIDsScanned    int
	PIDsUnreadable int
	// FDLinksRead is the readlink count, the cost actually paid.
	FDLinksRead int
	// Capped is true when a budget stopped the fd walk early, so an owner may
	// exist that is not listed.
	Capped bool
	// AttributionRan is false when the fd walk never started: nothing was
	// listening, [Options.SkipAttribution] was set, or /proc itself could not be
	// listed (then a Gap says so). It distinguishes "no owner found" from "no
	// owner looked for".
	AttributionRan bool
}

// Availability reports whether the SOCKET LIST can be trusted. It says nothing
// about owner attribution, which has its own predicate
// ([Snapshot.AttributionComplete]) because the two fail independently: a jail can
// read every table and still not see the process in another namespace that holds
// the port.
func (s Snapshot) Availability() Availability {
	switch {
	case s.TablesRead == 0:
		return Unavailable
	case s.TablesRead < s.TablesAttempted, s.MalformedLines > 0:
		return Partial
	default:
		return Complete
	}
}

// AttributionComplete reports whether the owner search was exhaustive: it ran,
// no budget cut it short, and every process's fd directory could be listed. When
// it is false, a [Listener] with no Owners means "not found", not "unowned".
func (s Snapshot) AttributionComplete() bool {
	return s.AttributionRan && !s.Capped && s.PIDsUnreadable == 0 && s.PIDsScanned == s.PIDsFound
}

// OnPort returns the listeners bound to a TCP port, which is the question a bind
// collision asks. Port 0 returns nothing rather than every UNIX socket.
func (s Snapshot) OnPort(port int) []Listener {
	if port == 0 {
		return nil
	}
	var out []Listener
	for _, l := range s.Sockets {
		if l.Port == port && l.Kind != KindUnix {
			out = append(out, l)
		}
	}
	return out
}

// OnPath returns the listeners bound to a UNIX socket path, the other half of
// what a failed bind can name.
func (s Snapshot) OnPath(path string) []Listener {
	if path == "" {
		return nil
	}
	var out []Listener
	for _, l := range s.Sockets {
		if l.Kind == KindUnix && l.Path == path {
			out = append(out, l)
		}
	}
	return out
}

// Defaults for [Options]. Both are budgets, not timeouts — see the package
// comment. DefaultMaxFDLinks at roughly a microsecond per readlink is a
// tenth-of-a-second ceiling on the dominant cost; a jail with 60 processes and 30
// descriptors each spends 2000 of them.
const (
	DefaultMaxPIDs    = 8192
	DefaultMaxFDLinks = 100_000
)

// Options tunes the bounds. The zero value is the intended configuration.
type Options struct {
	// MaxPIDs caps the processes whose fd directory is listed, lowest pid first.
	// Zero means DefaultMaxPIDs; negative means no attribution.
	MaxPIDs int
	// MaxFDLinks caps total readlinks across all processes. Zero means
	// DefaultMaxFDLinks.
	MaxFDLinks int
	// SkipAttribution reads the socket tables only. For a caller that wants the
	// bound ports and not the owners.
	SkipAttribution bool
}

func (o Options) normalized() Options {
	if o.MaxPIDs == 0 {
		o.MaxPIDs = DefaultMaxPIDs
	}
	if o.MaxFDLinks == 0 {
		o.MaxFDLinks = DefaultMaxFDLinks
	}
	if o.MaxPIDs < 0 || o.MaxFDLinks < 0 {
		o.SkipAttribution = true
	}
	return o
}

// tables is the read order, and the order [Snapshot.Sockets] sorts into.
var tables = []struct {
	name string
	kind Kind
}{
	{"net/tcp", KindTCP},
	{"net/tcp6", KindTCP6},
	{"net/unix", KindUnix},
}

// CollectFrom is the whole collector, against any [Source]. [Collect] is this
// with /proc; tests are this with a fixture.
//
// It returns a value under every failure: an unreadable table is a [Gap], an
// unparseable line is a count, an unreadable process is a count. Nothing here
// returns an error and nothing here panics.
func CollectFrom(src Source, opts Options) Snapshot {
	opts = opts.normalized()
	snap := Snapshot{TablesAttempted: len(tables)}
	if src == nil {
		snap.Gaps = append(snap.Gaps, Gap{Source: ProcRoot, Reason: "no source"})
		return snap
	}

	for _, t := range tables {
		data, err := src.ReadFile(t.name)
		if err != nil {
			snap.Gaps = append(snap.Gaps, Gap{Source: t.name, Reason: err.Error()})
			continue
		}
		snap.TablesRead++
		var found []Listener
		var malformed int
		if t.kind == KindUnix {
			found, malformed = parseUnix(data)
		} else {
			found, malformed = parseTCP(data, t.kind)
		}
		snap.Sockets = append(snap.Sockets, found...)
		snap.MalformedLines += malformed
		if malformed > 0 {
			snap.Gaps = append(snap.Gaps, Gap{
				Source: t.name,
				Reason: strconv.Itoa(malformed) + " unparseable line(s) skipped",
			})
		}
	}

	sortListeners(snap.Sockets)

	if !opts.SkipAttribution {
		attribute(src, &snap, opts)
	}
	return snap
}

// sortListeners puts the sockets in a stable, human-scannable order: table order
// first (so a tcp/tcp6 pair on one port sits together), then port, then address,
// then path, then inode. Deterministic output is what makes a rendered snapshot
// diffable between two boots.
func sortListeners(ls []Listener) {
	kindRank := func(k Kind) int {
		for i, t := range tables {
			if t.kind == k {
				return i
			}
		}
		return len(tables)
	}
	sort.SliceStable(ls, func(i, j int) bool {
		a, b := ls[i], ls[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if as, bs := a.Addr.String(), b.Addr.String(); as != bs {
			return as < bs
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Inode < b.Inode
	})
}

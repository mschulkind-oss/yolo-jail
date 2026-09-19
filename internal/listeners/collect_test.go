package listeners

import (
	"sort"
	"strings"
	"testing"
)

// listenerOn8214 is the shape of the bug this package was built for: something
// already holds 127.0.0.1:8214 when the wire bridge tries to bind it.
const listenerOn8214 = "   0: 0100007F:2016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1 0000000000000000 100 0 0 10 0"

// oneSocket is a source with exactly one listening socket (inode 4242) and no
// processes, so a test can add just the pids it cares about.
func oneSocket() *mapSource {
	m := emptyTables()
	m.files["net/tcp"] = tcpTable(listenerOn8214)
	m.dirs = map[string][]string{}
	m.links = map[string]string{}
	return m
}

// withProcess gives the source a pid holding the named fd targets. The fd names
// are sorted so a budget test cuts at a predictable place.
func withProcess(m *mapSource, pid string, comm, cmdline string, fdTargets map[string]string) {
	var fds []string
	for fd, target := range fdTargets {
		fds = append(fds, fd)
		m.links[pid+"/fd/"+fd] = target
	}
	sort.Strings(fds)
	m.dirs[pid+"/fd"] = fds
	m.files[pid+"/comm"] = comm + "\n"
	m.files[pid+"/cmdline"] = cmdline
	m.dirs["."] = append(m.dirs["."], pid)
}

// THE CENTRAL PROPERTY: "nothing is listening" and "I could not read /proc" are the
// same empty slice, so they must differ everywhere else.
func TestEmptyAndUnreadableAreDistinguishable(t *testing.T) {
	clean := CollectFrom(emptyTables(), Options{})
	if got := clean.Availability(); got != Complete {
		t.Errorf("readable-but-empty /proc: Availability = %v, want complete", got)
	}
	if len(clean.Sockets) != 0 || len(clean.Gaps) != 0 {
		t.Errorf("readable-but-empty /proc: %d sockets, %d gaps; want 0, 0", len(clean.Sockets), len(clean.Gaps))
	}

	blind := CollectFrom(&mapSource{}, Options{}) // no files at all
	if got := blind.Availability(); got != Unavailable {
		t.Errorf("unreadable /proc: Availability = %v, want unavailable", got)
	}
	if len(blind.Gaps) != len(tables) {
		t.Errorf("unreadable /proc: %d gaps, want one per table (%d)", len(blind.Gaps), len(tables))
	}
	if blind.TablesRead != 0 {
		t.Errorf("TablesRead = %d, want 0", blind.TablesRead)
	}

	half := emptyTables()
	half.failFiles = map[string]bool{"net/unix": true}
	if got := CollectFrom(half, Options{}).Availability(); got != Partial {
		t.Errorf("one unreadable table: Availability = %v, want partial", got)
	}

	garbled := emptyTables()
	garbled.files["net/tcp"] = tcpTable("   0: garbage")
	g := CollectFrom(garbled, Options{})
	if got := g.Availability(); got != Partial {
		t.Errorf("unparseable line: Availability = %v, want partial (a socket may be missing)", got)
	}
	if g.MalformedLines != 1 {
		t.Errorf("MalformedLines = %d, want 1", g.MalformedLines)
	}
}

func TestCollectFromNilSourceIsUnavailable(t *testing.T) {
	snap := CollectFrom(nil, Options{})
	if snap.Availability() != Unavailable || len(snap.Gaps) != 1 {
		t.Fatalf("nil source: %v with %d gaps; want unavailable with 1", snap.Availability(), len(snap.Gaps))
	}
}

func TestCollectFromAttributesTheOwnerByInode(t *testing.T) {
	m := oneSocket()
	withProcess(m, "17", "socat", "socat\x00TCP-LISTEN:8214,fork\x00EXEC:x\x00", map[string]string{
		"0": "/dev/null",
		"3": "socket:[4242]",
		"5": "pipe:[99]",
	})
	withProcess(m, "23", "yolo-jaild", "yolo-jaild\x00supervise\x00", map[string]string{
		"3": "socket:[7777]", // a different socket
	})

	snap := CollectFrom(m, Options{})
	if len(snap.Sockets) != 1 {
		t.Fatalf("got %d sockets, want 1", len(snap.Sockets))
	}
	owners := snap.Sockets[0].Owners
	if len(owners) != 1 {
		t.Fatalf("got %d owners, want 1: %+v", len(owners), owners)
	}
	if owners[0].PID != 17 {
		t.Errorf("pid = %d, want 17", owners[0].PID)
	}
	if owners[0].Comm != "socat" {
		t.Errorf("comm = %q, want socat", owners[0].Comm)
	}
	if owners[0].Cmdline != "socat TCP-LISTEN:8214,fork EXEC:x" {
		t.Errorf("cmdline = %q — NULs must become spaces and the trailing NUL must go", owners[0].Cmdline)
	}
	if !snap.AttributionComplete() {
		t.Errorf("attribution should be complete: %+v", snap)
	}
	if snap.PIDsFound != 2 || snap.PIDsScanned != 2 {
		t.Errorf("PIDsFound/Scanned = %d/%d, want 2/2", snap.PIDsFound, snap.PIDsScanned)
	}
}

// Two processes holding ONE listening socket (a fork, or a passed fd) is the shape
// the 8214 collision actually had, so the walk must not stop at the first hit — and
// one process holding it on two descriptors is still one owner.
func TestCollectFromFindsEveryOwnerOfOneSocketExactlyOnce(t *testing.T) {
	m := oneSocket()
	withProcess(m, "9", "child", "socat\x00", map[string]string{"3": "socket:[4242]"})
	withProcess(m, "4", "parent", "socat\x00", map[string]string{
		"3": "socket:[4242]",
		"4": "socket:[4242]", // dup of the same socket
	})

	snap := CollectFrom(m, Options{})
	owners := snap.Sockets[0].Owners
	if len(owners) != 2 {
		t.Fatalf("got %d owners, want 2 (dup must not double-count): %+v", len(owners), owners)
	}
	if owners[0].PID != 4 || owners[1].PID != 9 {
		t.Errorf("owner pids = %d,%d; want 4,9 ascending", owners[0].PID, owners[1].PID)
	}
}

func TestCollectFromCountsUnreadableProcessesWithoutFailing(t *testing.T) {
	m := oneSocket()
	withProcess(m, "5", "holder", "holder\x00", map[string]string{"3": "socket:[4242]"})
	// A pid in /proc whose fd dir refuses: another namespace, another user, or it
	// exited between the two reads. Normal, not an error.
	m.dirs["."] = append(m.dirs["."], "6")
	m.failDirs = map[string]bool{"6/fd": true}

	snap := CollectFrom(m, Options{})
	if len(snap.Sockets[0].Owners) != 1 {
		t.Fatalf("the readable holder must still be attributed: %+v", snap.Sockets[0].Owners)
	}
	if snap.PIDsUnreadable != 1 {
		t.Errorf("PIDsUnreadable = %d, want 1", snap.PIDsUnreadable)
	}
	if snap.AttributionComplete() {
		t.Error("AttributionComplete must be false when a process could not be read")
	}
	if snap.Availability() != Complete {
		t.Errorf("Availability = %v: an unreadable PROCESS says nothing about the socket LIST", snap.Availability())
	}
	if !hasGap(snap, "<pid>/fd") {
		t.Errorf("want a gap naming the unreadable process directories: %+v", snap.Gaps)
	}
}

// A socket with no owner found and no way to look must not read as "unowned".
func TestCollectFromRecordsWhenProcEnumerationFails(t *testing.T) {
	m := oneSocket() // has net/tcp, but no "." directory
	snap := CollectFrom(m, Options{})
	if len(snap.Sockets) != 1 || len(snap.Sockets[0].Owners) != 0 {
		t.Fatalf("unexpected sockets: %+v", snap.Sockets)
	}
	if snap.AttributionRan {
		t.Error("AttributionRan must be false when /proc itself could not be listed")
	}
	if snap.AttributionComplete() {
		t.Error("AttributionComplete must be false when the walk never ran")
	}
	if !hasGap(snap, ".") {
		t.Errorf("want a gap naming /proc: %+v", snap.Gaps)
	}
}

// The fd walk is the expensive half, so it must not run at all when there is
// nothing to attribute.
func TestCollectFromSkipsTheFDWalkWhenNothingIsListening(t *testing.T) {
	m := emptyTables()
	snap := CollectFrom(m, Options{})
	if m.dirReads != 0 || m.linkReads != 0 {
		t.Errorf("walked /proc anyway: %d dir reads, %d readlinks; want 0, 0", m.dirReads, m.linkReads)
	}
	if snap.AttributionRan || snap.FDLinksRead != 0 {
		t.Errorf("AttributionRan = %v, FDLinksRead = %d; want false, 0", snap.AttributionRan, snap.FDLinksRead)
	}
}

func TestSkipAttributionReadsTablesOnly(t *testing.T) {
	m := oneSocket()
	withProcess(m, "5", "holder", "holder\x00", map[string]string{"3": "socket:[4242]"})

	snap := CollectFrom(m, Options{SkipAttribution: true})
	if len(snap.Sockets) != 1 {
		t.Fatalf("got %d sockets, want 1", len(snap.Sockets))
	}
	if m.linkReads != 0 || snap.AttributionRan {
		t.Errorf("SkipAttribution walked anyway: %d readlinks, AttributionRan=%v", m.linkReads, snap.AttributionRan)
	}
	// A negative budget is the same instruction by another spelling.
	if snap := CollectFrom(oneSocket(), Options{MaxPIDs: -1}); snap.AttributionRan {
		t.Error("MaxPIDs<0 must skip attribution")
	}
}

func TestThePIDBudgetStopsTheWalkAndSaysSo(t *testing.T) {
	m := oneSocket()
	for _, pid := range []string{"11", "22", "33"} {
		withProcess(m, pid, "holder"+pid, "holder\x00", map[string]string{"3": "socket:[4242]"})
	}

	snap := CollectFrom(m, Options{MaxPIDs: 2})
	if !snap.Capped {
		t.Fatal("Capped must be true when the pid budget cut the walk short")
	}
	if snap.PIDsScanned != 2 {
		t.Errorf("PIDsScanned = %d, want 2", snap.PIDsScanned)
	}
	if owners := snap.Sockets[0].Owners; len(owners) != 2 || owners[0].PID != 11 || owners[1].PID != 22 {
		t.Errorf("owners = %+v; want the two lowest pids (a reproducible cut)", owners)
	}
	if snap.AttributionComplete() {
		t.Error("AttributionComplete must be false once a budget has cut the walk")
	}
	if !hasGap(snap, "fd-scan") {
		t.Errorf("want a gap naming the budget: %+v", snap.Gaps)
	}
}

func TestTheReadlinkBudgetStopsTheWalkAndSaysSo(t *testing.T) {
	m := oneSocket()
	withProcess(m, "7", "holder", "holder\x00", map[string]string{
		"0": "/dev/null", "1": "/dev/null", "2": "/dev/null", "3": "/dev/null",
		"4": "socket:[4242]",
	})

	snap := CollectFrom(m, Options{MaxFDLinks: 2})
	if snap.FDLinksRead != 2 {
		t.Errorf("FDLinksRead = %d, want exactly the budget (2)", snap.FDLinksRead)
	}
	if !snap.Capped || snap.AttributionComplete() {
		t.Errorf("Capped = %v, AttributionComplete = %v; want true, false", snap.Capped, snap.AttributionComplete())
	}
	// The budget ran out before fd 4, so the owner is NOT FOUND — which the
	// snapshot must not let a reader mistake for "unowned".
	if owners := snap.Sockets[0].Owners; len(owners) != 0 {
		t.Errorf("owners = %+v, want none found within the budget", owners)
	}
}

func TestDefaultsAreTheIntendedConfiguration(t *testing.T) {
	got := Options{}.normalized()
	if got.MaxPIDs != DefaultMaxPIDs || got.MaxFDLinks != DefaultMaxFDLinks || got.SkipAttribution {
		t.Errorf("Options{}.normalized() = %+v; want the documented defaults", got)
	}
}

func TestSocketOrderIsDeterministicAndGroupsTheTables(t *testing.T) {
	m := emptyTables()
	m.files["net/tcp"] = tcpTable(rowListen443, rowListen1460)
	m.files["net/tcp6"] = tcp6Header + "\n" +
		"   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 0 0 0 0 0 7 1 0 100 0 0 10 0\n"
	m.files["net/unix"] = unixTable(unixListening)

	var got []string
	for _, l := range CollectFrom(m, Options{}).Sockets {
		got = append(got, string(l.Kind)+" "+l.Local())
	}
	want := []string{"tcp 127.0.0.1:443", "tcp 127.0.0.1:1460", "tcp6 [::]:443", "unix /tmp/cc-socks/2.sock"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestOnPortAndOnPathSelectTheBindCollisionCandidates(t *testing.T) {
	m := emptyTables()
	m.files["net/tcp"] = tcpTable(listenerOn8214)
	m.files["net/unix"] = unixTable(unixListening)
	snap := CollectFrom(m, Options{SkipAttribution: true})

	if got := snap.OnPort(8214); len(got) != 1 || got[0].Port != 8214 {
		t.Errorf("OnPort(8214) = %+v, want the one tcp listener", got)
	}
	if got := snap.OnPort(9999); len(got) != 0 {
		t.Errorf("OnPort(9999) = %+v, want none", got)
	}
	if got := snap.OnPort(0); len(got) != 0 {
		t.Errorf("OnPort(0) = %+v, want none — port 0 must not return every unix socket", got)
	}
	if got := snap.OnPath("/tmp/cc-socks/2.sock"); len(got) != 1 {
		t.Errorf("OnPath = %+v, want the one unix listener", got)
	}
	if got := snap.OnPath(""); len(got) != 0 {
		t.Errorf("OnPath(\"\") = %+v, want none", got)
	}
}

func hasGap(s Snapshot, source string) bool {
	for _, g := range s.Gaps {
		if g.Source == source {
			return true
		}
	}
	return false
}

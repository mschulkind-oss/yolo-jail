package wirebridged

// portholder_test.go pins the measurement that existed nowhere. These are
// behavioural: a real listener is opened and the probe must find the real process
// holding it, so a parser that mis-reads /proc's little-endian address column or
// walks the wrong fd tree fails here rather than printing a plausible wrong
// address into a bind failure — which is worse than printing nothing.
//
// The /proc PARSING is pinned in internal/listeners, which owns it, and more
// thoroughly than the duplicate deleted from this package was: the little-endian
// IPv4 decode, the per-group IPv6 decode, the v4-mapped spelling and the malformed
// row counting all have their own tests there. What is pinned HERE is the
// sentence, the tri-state, and the DELEGATION — every fixture test below works by
// replacing portHolderSnapshot, so a describePortHolder that stopped consulting it
// would fail rather than quietly reading the real machine.

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/listeners"
)

func requireProcNetTCP(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skipf("/proc/net/tcp is unavailable (%v); the holder probe degrades to its "+
			"could-not-ask arm, which TestDescribePortHolderCannotAskWithoutProc covers", err)
	}
}

// withSnapshotFrom points the probe at a fixture tree for one test. Overriding the
// snapshot rather than a set of /proc path variables is what makes the delegation
// itself testable.
func withSnapshotFrom(t *testing.T, root string) {
	t.Helper()
	old := portHolderSnapshot
	portHolderSnapshot = func() listeners.Snapshot {
		return listeners.CollectFrom(listeners.DirSource(root), listeners.Options{})
	}
	t.Cleanup(func() { portHolderSnapshot = old })
}

// The real thing: this test process holds a port, and the probe must name it.
func TestDescribePortHolderNamesTheHoldingProcess(t *testing.T) {
	requireProcNetTCP(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := describePortHolder(ln.Addr().String())
	for _, want := range []string{
		"already held",
		ln.Addr().String(),
		"held by pid " + strconv.Itoa(os.Getpid()),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("describePortHolder(%s) = %q, missing %q", ln.Addr().String(), got, want)
		}
	}
}

// An unheld port must produce the ABSENCE answer, not a holder and not the
// could-not-ask answer. Conflating any two of the three is how an empty `ss`
// output sent the 8214 hunt down four wrong paths.
func TestDescribePortHolderReportsAnAbsentListenerAsAbsent(t *testing.T) {
	requireProcNetTCP(t)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	got := describePortHolder(addr)
	if !strings.Contains(got, "no LISTEN socket") {
		t.Errorf("describePortHolder on a free port = %q, want the absence answer", got)
	}
	if strings.Contains(got, "could not be identified") {
		t.Errorf("an empty table is not an unreadable one: %q", got)
	}
}

// THE TRI-STATE ARM. Point the probe at a /proc that is not there: it must say it
// could not look, never that nothing holds the port — and it must name WHICH read
// failed, because that is what the reader's next action turns on.
func TestDescribePortHolderCannotAskWithoutProc(t *testing.T) {
	withSnapshotFrom(t, filepath.Join(t.TempDir(), "absent"))

	got := describePortHolder("127.0.0.1:8214")
	if !strings.Contains(got, "could not be identified") {
		t.Errorf("an unreadable listener table = %q, want the could-not-ask answer", got)
	}
	if strings.Contains(got, "no LISTEN socket") {
		t.Errorf("an unreadable table was reported as an absent listener: %q", got)
	}
	if !strings.Contains(got, "net/tcp") {
		t.Errorf("the could-not-ask answer does not name the read that failed: %q", got)
	}
}

// A PARTIAL snapshot with no match is NOT an absence either: one readable table out
// of three cannot assert that nothing is listening. This is the arm most easily
// collapsed into the absence answer, because it has real data in it.
func TestDescribePortHolderTreatsAPartialTableAsUnknown(t *testing.T) {
	root := t.TempDir()
	writeProcNetTCP(t, root, procListenRow("0100007F", "01BB", "318408350")) // 127.0.0.1:443
	withSnapshotFrom(t, root)

	got := describePortHolder("127.0.0.1:8214")
	if strings.Contains(got, "no LISTEN socket") {
		t.Errorf("a partially-read snapshot claimed an absence: %q", got)
	}
	if !strings.Contains(got, "could not be identified") {
		t.Errorf("describePortHolder = %q, want the could-not-ask answer", got)
	}
}

// A wildcard listener is the common reason a specific loopback bind fails, and a
// reader told only "already in use" would look for a listener on the exact
// address and find none. The fixture is a hand-built /proc so the case is
// reproducible without binding 0.0.0.0 in a test.
func TestDescribePortHolderExplainsAWildcardListener(t *testing.T) {
	root := t.TempDir()
	writeProcNetTCP(t, root, procListenRow("00000000", "2016", "318411330")) // 0.0.0.0:8214
	withSnapshotFrom(t, root)

	got := describePortHolder("127.0.0.1:8214")
	for _, want := range []string{"0.0.0.0:8214", "covers the one the bridge wanted"} {
		if !strings.Contains(got, want) {
			t.Errorf("describePortHolder = %q, missing %q", got, want)
		}
	}
}

// The overlap sentence is for an UNSPECIFIED address only. A listener on some other
// specific address cannot collide with the bridge's bind, so claiming it "covers"
// the wanted address sends the reader after the wrong process — which the deleted
// string-prefix check did.
func TestDescribePortHolderDoesNotCallASpecificAddressAnOverlap(t *testing.T) {
	root := t.TempDir()
	writeProcNetTCP(t, root, procListenRow("0500000A", "2016", "318411330")) // 10.0.0.5:8214
	withSnapshotFrom(t, root)

	got := describePortHolder("127.0.0.1:8214")
	if !strings.Contains(got, "10.0.0.5:8214") {
		t.Errorf("describePortHolder = %q, want the holder's own address", got)
	}
	if strings.Contains(got, "covers the one the bridge wanted") {
		t.Errorf("a listener on a different specific address was called an overlap: %q", got)
	}
}

// An unattributable socket is reported AS unattributable: the socket exists and is
// the conflict whether or not its owner is readable from here.
func TestDescribePortHolderNamesTheSocketWhenItsOwnerIsUnreadable(t *testing.T) {
	root := t.TempDir()
	writeProcNetTCP(t, root, procListenRow("00000000", "2016", "318411330"))
	withSnapshotFrom(t, root)

	got := describePortHolder("127.0.0.1:8214")
	if !strings.Contains(got, "socket inode 318411330") {
		t.Errorf("describePortHolder = %q, want the socket inode it could not attribute", got)
	}
	if strings.Contains(got, "held by pid") {
		t.Errorf("describePortHolder invented an owner: %q", got)
	}
}

// A malformed address is answered, not panicked over: this runs on the failure
// path of a boot, where a second fault would replace an actionable bind error
// with a stack trace.
func TestDescribePortHolderOnAMalformedAddress(t *testing.T) {
	got := describePortHolder("not-an-address")
	if !strings.Contains(got, "not a host:port address") {
		t.Errorf("describePortHolder = %q, want an explanation", got)
	}
}

// A non-numeric port reaches the probe from a provider's base_url, which is user
// data: it must be answered rather than parsed twice.
func TestDescribePortHolderOnANonNumericPort(t *testing.T) {
	got := describePortHolder("127.0.0.1:https")
	if !strings.Contains(got, "not numeric") {
		t.Errorf("describePortHolder = %q, want the non-numeric-port answer", got)
	}
}

// THE BOUNDED-WALK PROPERTY, which is why the duplicate parser here was deleted.
// The old code walked the whole /proc tree once per matching socket inode, and a
// dual-stack collision matches two — on a boot's failure path. One snapshot answers
// for every listener on the port.
func TestDescribePortHolderTakesOneSnapshot(t *testing.T) {
	root := t.TempDir()
	// Two LISTEN sockets on the same port: the dual-stack shape.
	writeProcNetTCP(t, root,
		procListenRow("00000000", "2016", "318411330"),
		procListenRow("0100007F", "2016", "318411331"))
	old := portHolderSnapshot
	calls := 0
	portHolderSnapshot = func() listeners.Snapshot {
		calls++
		return listeners.CollectFrom(listeners.DirSource(root), listeners.Options{})
	}
	t.Cleanup(func() { portHolderSnapshot = old })

	got := describePortHolder("127.0.0.1:8214")
	if calls != 1 {
		t.Errorf("describePortHolder read the machine %d times, want exactly 1", calls)
	}
	for _, want := range []string{"0.0.0.0:8214", "127.0.0.1:8214"} {
		if !strings.Contains(got, want) {
			t.Errorf("describePortHolder = %q, missing holder %q", got, want)
		}
	}
}

// THE ARGV'S FLATTENING IS PINNED IN internal/listeners, which now owns it
// ([listeners.BoundArgv]). What is pinned HERE is that the flattened argv reaches
// this file's sentence at all, which the holder test above asserts by name — the
// readiness pipe this string crosses is line-framed, so an argv that arrived with
// its newlines intact would turn a precise bind failure into "jail daemon reported
// unexpected readiness".
func TestDescribePortHolderKeepsTheSentenceOnOneLine(t *testing.T) {
	requireProcNetTCP(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := describePortHolder(ln.Addr().String())
	if strings.ContainsAny(got, "\n\x00") {
		t.Errorf("describePortHolder returned a multi-line record: %q", got)
	}
}

// procListenRow is one LISTEN row of /proc/net/tcp, in the kernel's column layout.
// hexAddr and hexPort are the kernel's spellings (little-endian per 32-bit word for
// the address), which is why the fixtures above read as hex rather than as dotted
// quads.
func procListenRow(hexAddr, hexPort, inode string) string {
	return "   0: " + hexAddr + ":" + hexPort + " 00000000:0000 0A 00000000:00000000 " +
		"00:00000000 00000000     0        0 " + inode + " 2 000000006fad1d5e 100 0 0 10 0"
}

func writeProcNetTCP(t *testing.T, root string, rows ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt" +
		"   uid  timeout inode\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

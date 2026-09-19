package wirebridged

// portholder_test.go pins the measurement that existed nowhere. These are
// behavioural: a real listener is opened and the probe must find the real process
// holding it, so a parser that mis-reads /proc's little-endian address column or
// walks the wrong fd tree fails here rather than printing a plausible wrong
// address into a bind failure — which is worse than printing nothing.

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func requireProcNetTCP(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skipf("/proc/net/tcp is unavailable (%v); the holder probe degrades to its "+
			"could-not-ask arm, which TestDescribePortHolderCannotAskWithoutProc covers", err)
	}
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
// could not look, never that nothing holds the port.
func TestDescribePortHolderCannotAskWithoutProc(t *testing.T) {
	oldFiles, oldRoot := procNetTCPFiles, procRoot
	missing := filepath.Join(t.TempDir(), "absent")
	procNetTCPFiles = []string{filepath.Join(missing, "net", "tcp")}
	procRoot = missing
	t.Cleanup(func() { procNetTCPFiles, procRoot = oldFiles, oldRoot })

	got := describePortHolder("127.0.0.1:8214")
	if !strings.Contains(got, "could not be identified") {
		t.Errorf("an unreadable listener table = %q, want the could-not-ask answer", got)
	}
	if strings.Contains(got, "no LISTEN socket") {
		t.Errorf("an unreadable table was reported as an absent listener: %q", got)
	}
}

// A wildcard listener is the common reason a specific loopback bind fails, and a
// reader told only "already in use" would look for a listener on the exact
// address and find none. The fixture is a hand-built /proc so the case is
// reproducible without binding 0.0.0.0 in a test.
func TestDescribePortHolderExplainsAWildcardListener(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 0.0.0.0:8214 in LISTEN, inode 0 so no owner can be resolved.
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:2016 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1\n"
	path := filepath.Join(root, "net", "tcp")
	if err := os.WriteFile(path, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	oldFiles, oldRoot := procNetTCPFiles, procRoot
	procNetTCPFiles, procRoot = []string{path}, root
	t.Cleanup(func() { procNetTCPFiles, procRoot = oldFiles, oldRoot })

	got := describePortHolder("127.0.0.1:8214")
	for _, want := range []string{"0.0.0.0:8214", "covers the one the bridge wanted"} {
		if !strings.Contains(got, want) {
			t.Errorf("describePortHolder = %q, missing %q", got, want)
		}
	}
}

// /proc/net/tcp's address column is LITTLE-endian per 32-bit word, which is the
// single most common way to misread that table; a reversed address in a bind
// diagnostic sends the reader looking for the wrong listener.
func TestFormatProcAddr(t *testing.T) {
	cases := map[string]string{
		"0100007F":                         "127.0.0.1",
		"00000000":                         "0.0.0.0",
		"0500000A":                         "10.0.0.5",
		"00000000000000000000000000000000": "::",
		// A v4-mapped v6 socket renders as the v4 address, which is the spelling
		// the reader is hunting for in their own config.
		"0000000000000000FFFF00000100007F": "127.0.0.1",
		"nothex":                           "0xnothex",
	}
	for in, want := range cases {
		if got := formatProcAddr(in); got != want {
			t.Errorf("formatProcAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// The argv reaches the line-framed readiness pipe, so it is flattened and
// bounded: it is arbitrary process input.
func TestReadProcCmdlineIsSingleLineAndBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "42"), 0o755); err != nil {
		t.Fatal(err)
	}
	argv := "socat\x00TCP-LISTEN:8214,bind=127.0.0.1\x00a\nb\x00" + strings.Repeat("x", 2000)
	if err := os.WriteFile(filepath.Join(root, "42", "cmdline"), []byte(argv), 0o644); err != nil {
		t.Fatal(err)
	}
	oldRoot := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = oldRoot })

	got := readProcCmdline("42")
	if strings.ContainsAny(got, "\n\x00") {
		t.Errorf("readProcCmdline left a newline or NUL in %q", got)
	}
	if !strings.Contains(got, "TCP-LISTEN:8214,bind=127.0.0.1") {
		t.Errorf("readProcCmdline dropped the argument that identifies the holder: %q", got)
	}
	if len(got) > 500 {
		t.Errorf("readProcCmdline returned %d bytes; the readiness record is one line", len(got))
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

// listenersOnPort's hex port matching, at the exact width /proc uses.
func TestListenersOnPortMatchesTheHexPortColumn(t *testing.T) {
	requireProcNetTCP(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	found, readErrs := listenersOnPort(port)
	if len(found) == 0 {
		t.Fatalf("listenersOnPort(%s) found nothing for a live listener (read errors: %v)", port, readErrs)
	}
	for _, l := range found {
		if !strings.HasSuffix(l.local, ":"+port) {
			t.Errorf("listener %+v does not carry the port it was matched on", l)
		}
		if l.inode == "" || l.inode == "0" {
			t.Errorf("listener %+v carries no socket inode, so no owner can ever be resolved", l)
		}
	}
}

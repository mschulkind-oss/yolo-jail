package listeners

import "testing"

// Rows copied from a live jail's /proc/net/tcp, with the inode and uid left as the
// kernel printed them. 05B4 is port 1460, 01BB is 443.
const (
	rowListen1460 = "   0: 0100007F:05B4 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 318411330 2 000000006fad1d5e 100 0 0 10 0                 "
	rowListen443  = "   1: 0100007F:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 318408350 2 00000000062f48e5 100 0 0 10 0                 "
	rowConnected  = "   2: 9101A8C0:C728 ABF0D42C:01BB 01 00000000:00000000 02:000000D8 00000000     0        0 318751830 2 0000000074aeb43e 20 4 30 13 -1                "
)

func TestParseTCPDecodesLittleEndianIPv4AndKeepsOnlyLISTEN(t *testing.T) {
	got, malformed := parseTCP([]byte(tcpTable(rowListen1460, rowConnected, rowListen443)), KindTCP)
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2 (the connected row must be skipped): %+v", len(got), got)
	}
	// 0100007F must decode to 127.0.0.1, NOT 1.0.0.127: the kernel prints the
	// 4-byte group as a native-endian word.
	if addr := got[0].Addr.String(); addr != "127.0.0.1" {
		t.Errorf("addr = %q, want 127.0.0.1 (byte order)", addr)
	}
	if got[0].Port != 1460 {
		t.Errorf("port = %d, want 1460 (05B4 is hex)", got[0].Port)
	}
	if got[0].Inode != 318411330 {
		t.Errorf("inode = %d, want 318411330", got[0].Inode)
	}
	if got[0].UID != 0 || got[1].UID != 1000 {
		t.Errorf("uid = %d,%d, want 0,1000", got[0].UID, got[1].UID)
	}
	if got[0].Kind != KindTCP {
		t.Errorf("kind = %q, want tcp", got[0].Kind)
	}
	if got[1].Port != 443 {
		t.Errorf("second port = %d, want 443", got[1].Port)
	}
}

// The IPv6 spelling is per-4-byte-group little-endian, and a whole-16-byte reversal
// is the plausible wrong answer — it yields "7f00:1:0:ffff::" for the v4-mapped row
// below, so this test discriminates between the two implementations rather than
// merely exercising one.
func TestParseTCPDecodesIPv6PerFourByteGroup(t *testing.T) {
	rows := []struct {
		name, hexAddr, want string
	}{
		{"loopback", "00000000000000000000000001000000", "::1"},
		{"wildcard", "00000000000000000000000000000000", "::"},
		{"v4mapped", "0000000000000000FFFF00000100007F", "::ffff:127.0.0.1"},
		{"global", "0D0120FE00000000FFFFFFFFFFFFFFFF", "fe20:10d::ffff:ffff:ffff:ffff"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			line := "   0: " + r.hexAddr + ":1F90 " + r.hexAddr +
				":0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1 0000000000000000 100 0 0 10 0"
			got, malformed := parseTCP([]byte(tcp6Header+"\n"+line+"\n"), KindTCP6)
			if malformed != 0 || len(got) != 1 {
				t.Fatalf("got %d listeners, %d malformed; want 1, 0", len(got), malformed)
			}
			if addr := got[0].Addr.String(); addr != r.want {
				t.Errorf("addr = %q, want %q", addr, r.want)
			}
			if got[0].Port != 8080 {
				t.Errorf("port = %d, want 8080", got[0].Port)
			}
		})
	}
}

func TestParseTCPCountsMalformedLinesAndKeepsGoing(t *testing.T) {
	bad := []string{
		"   1: truncated 0A",                                        // too few fields
		"   2: NOTHEX:05B4 00000000:0000 0A 0 0 0 0 0 5",            // LISTEN, unparseable address
		"   3: 0100007F:05B4 00000000:0000 0A 0 0 0 0 0 notaninode", // LISTEN, bad inode
	}
	table := tcpTable(append([]string{rowListen1460}, bad...)...)
	got, malformed := parseTCP([]byte(table), KindTCP)
	if malformed != len(bad) {
		t.Errorf("malformed = %d, want %d", malformed, len(bad))
	}
	if len(got) != 1 || got[0].Port != 1460 {
		t.Fatalf("a malformed line must not lose the good ones; got %+v", got)
	}
}

func TestParseTCPHeaderOnlyTableIsEmptyNotMalformed(t *testing.T) {
	// Verbatim from a live jail with no IPv6 listener: the empty case must be
	// clean, or every snapshot on such a machine reports itself partial.
	got, malformed := parseTCP([]byte(tcp6Header+"\n"), KindTCP6)
	if len(got) != 0 || malformed != 0 {
		t.Fatalf("got %d listeners, %d malformed; want 0, 0", len(got), malformed)
	}
}

func TestParseTCPEmptyFile(t *testing.T) {
	got, malformed := parseTCP(nil, KindTCP)
	if len(got) != 0 || malformed != 0 {
		t.Fatalf("got %d listeners, %d malformed; want 0, 0", len(got), malformed)
	}
	gotUnix, malformedUnix := parseUnix(nil)
	if len(gotUnix) != 0 || malformedUnix != 0 {
		t.Fatalf("unix: got %d listeners, %d malformed; want 0, 0", len(gotUnix), malformedUnix)
	}
}

// A header line arriving after a blank line must still be recognised as a header:
// skipping by line NUMBER would count it as malformed.
func TestParseTCPHeaderIsRecognisedByItsFirstField(t *testing.T) {
	got, malformed := parseTCP([]byte("\n"+tcpHeader+"\n"+rowListen1460+"\n"), KindTCP)
	if malformed != 0 || len(got) != 1 {
		t.Fatalf("got %d listeners, %d malformed; want 1, 0", len(got), malformed)
	}
}

const (
	unixListening = "000000000cf9385d: 00000002 00000000 00010000 0001 01 318422157 /tmp/cc-socks/2.sock"
	unixConnected = "00000000640cbc71: 00000003 00000000 00000000 0001 03 318599746 /tmp/yolo-claude-oauth-broker.sock"
)

func TestParseUnixRequiresTheAcceptConFlag(t *testing.T) {
	got, malformed := parseUnix([]byte(unixTable(unixListening, unixConnected)))
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(got) != 1 {
		t.Fatalf("got %d listeners, want 1 (the connected socket has no SO_ACCEPTCON): %+v", len(got), got)
	}
	if got[0].Path != "/tmp/cc-socks/2.sock" {
		t.Errorf("path = %q", got[0].Path)
	}
	if got[0].Inode != 318422157 {
		t.Errorf("inode = %d, want 318422157", got[0].Inode)
	}
	if got[0].Kind != KindUnix || got[0].Port != 0 {
		t.Errorf("kind/port = %q/%d, want unix/0", got[0].Kind, got[0].Port)
	}
	if got[0].UID != -1 {
		t.Errorf("uid = %d, want -1: /proc/net/unix has no uid column, and reporting 0 would claim root", got[0].UID)
	}
}

func TestParseUnixKeepsPathsWithSpacesAndAbstractSockets(t *testing.T) {
	rows := []string{
		"0000000000000001: 00000002 00000000 00010000 0001 01 11 /tmp/a path/with spaces.sock",
		"0000000000000002: 00000002 00000000 00010000 0001 01 12 @/tmp/abstract.sock",
		"0000000000000003: 00000002 00000000 00010000 0001 01 13",
	}
	got, malformed := parseUnix([]byte(unixTable(rows...)))
	if malformed != 0 || len(got) != 3 {
		t.Fatalf("got %d listeners, %d malformed; want 3, 0", len(got), malformed)
	}
	if got[0].Path != "/tmp/a path/with spaces.sock" {
		t.Errorf("path = %q — splitting on whitespace truncates a legal path", got[0].Path)
	}
	if got[1].Path != "@/tmp/abstract.sock" {
		t.Errorf("abstract path = %q, want the kernel's leading @", got[1].Path)
	}
	if got[2].Path != "" {
		t.Errorf("unnamed socket path = %q, want empty", got[2].Path)
	}
}

func TestParseUnixCountsMalformedLines(t *testing.T) {
	got, malformed := parseUnix([]byte(unixTable("0000000000000001: 00000002 nope", unixListening)))
	if malformed != 1 {
		t.Errorf("malformed = %d, want 1", malformed)
	}
	if len(got) != 1 {
		t.Errorf("got %d listeners, want 1", len(got))
	}
}

func TestLocalRendersTheAddressABindErrorWouldName(t *testing.T) {
	cases := []struct {
		name string
		l    Listener
		want string
	}{
		{"ipv4", mustListener(t, KindTCP, "0100007F:2016"), "127.0.0.1:8214"},
		{"ipv6 wildcard", mustListener(t, KindTCP6, "00000000000000000000000000000000:2016"), "[::]:8214"},
		{"unix", Listener{Kind: KindUnix, Path: "/tmp/s.sock"}, "/tmp/s.sock"},
		{"unnamed unix", Listener{Kind: KindUnix}, "(unnamed)"},
	}
	for _, c := range cases {
		if got := c.l.Local(); got != c.want {
			t.Errorf("%s: Local() = %q, want %q", c.name, got, c.want)
		}
	}
}

func mustListener(t *testing.T, kind Kind, hexAddrPort string) Listener {
	t.Helper()
	addr, port, ok := parseHexAddrPort(hexAddrPort)
	if !ok {
		t.Fatalf("parseHexAddrPort(%q) failed", hexAddrPort)
	}
	return Listener{Kind: kind, Addr: addr, Port: port}
}

func TestParseHexAddrRejectsWrongLengths(t *testing.T) {
	for _, h := range []string{"", "01", "0100007", "0100007FF", "zzzzzzzz"} {
		if _, ok := parseHexAddr(h); ok {
			t.Errorf("parseHexAddr(%q) accepted a length/charset it must reject", h)
		}
	}
}

func TestSocketInode(t *testing.T) {
	if n, ok := socketInode("socket:[318411330]"); !ok || n != 318411330 {
		t.Errorf("socketInode = %d, %v; want 318411330, true", n, ok)
	}
	for _, target := range []string{"/dev/null", "pipe:[123]", "socket:[]", "socket:[abc]", "socket:[12", "anon_inode:[eventpoll]"} {
		if _, ok := socketInode(target); ok {
			t.Errorf("socketInode(%q) must not match", target)
		}
	}
}

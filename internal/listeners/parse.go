package listeners

import (
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
)

// tcpListenState is the `st` column for TCP_LISTEN in /proc/net/tcp — the kernel
// prints the enum in hex, and 0x0A is TCP_LISTEN. Every other state is a
// connection, not a bind, and is skipped rather than counted as malformed.
const tcpListenState = "0A"

// unixAcceptConFlag is SO_ACCEPTCON in /proc/net/unix's Flags column. It is the
// listening predicate for a UNIX socket, and the reason the St column alone is not:
// an unconnected stream socket that never called listen(2) also shows St=01. This
// is what `ss -l` keys on.
const unixAcceptConFlag = 0x10000

// parseTCP reads /proc/net/tcp or /proc/net/tcp6 and returns the LISTEN rows.
//
// Column indices, from the kernel's own format: 1 local_address, 3 st, 7 uid,
// 9 inode. A row with fewer than 10 fields, or one in LISTEN whose address or
// inode does not parse, is counted as malformed and skipped — a diagnostic that
// drops a row it cannot read still beats one that refuses to answer, as long as it
// says it dropped it.
func parseTCP(data []byte, kind Kind) (out []Listener, malformed int) {
	for _, line := range splitLines(data) {
		f := strings.Fields(line)
		if len(f) == 0 || isTableHeader(f) {
			continue
		}
		if len(f) < 10 {
			malformed++
			continue
		}
		if !strings.EqualFold(f[3], tcpListenState) {
			continue
		}
		addr, port, addrOK := parseHexAddrPort(f[1])
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if !addrOK || err != nil {
			malformed++
			continue
		}
		out = append(out, Listener{
			Kind:  kind,
			Addr:  addr,
			Port:  port,
			Inode: inode,
			UID:   parseUID(f[7]),
		})
	}
	return out, malformed
}

// parseUnix reads /proc/net/unix and returns the rows with SO_ACCEPTCON set.
//
// Columns: 3 Flags, 6 Inode, 7 Path. The path is taken as the REST OF THE LINE,
// not as field 7, because a UNIX socket path may contain spaces and splitting on
// whitespace would silently truncate it — and a truncated path in a bind-collision
// report is a wrong answer, not a missing one. /proc/net/unix has no uid column, so
// UID is -1 here by construction.
func parseUnix(data []byte) (out []Listener, malformed int) {
	for _, line := range splitLines(data) {
		f, rest := fieldsN(line, 7)
		if len(f) == 0 || isTableHeader(f) {
			continue
		}
		if len(f) < 7 {
			malformed++
			continue
		}
		flags, flagsErr := strconv.ParseUint(f[3], 16, 64)
		inode, inodeErr := strconv.ParseUint(f[6], 10, 64)
		if flagsErr != nil || inodeErr != nil {
			malformed++
			continue
		}
		if flags&unixAcceptConFlag == 0 {
			continue
		}
		out = append(out, Listener{Kind: KindUnix, Path: rest, Inode: inode, UID: -1})
	}
	return out, malformed
}

// isTableHeader recognises the one header line each table starts with. It is
// matched by first field rather than by line number so a leading blank line cannot
// promote the header into the data: no data row's first field is "sl" or "Num" —
// they are "<n>:" and "<kernel address>:" respectively.
func isTableHeader(fields []string) bool {
	return len(fields) > 1 && (fields[0] == "sl" || fields[0] == "Num")
}

// parseHexAddrPort splits /proc's "<hex address>:<hex port>".
func parseHexAddrPort(s string) (netip.Addr, int, bool) {
	colon := strings.LastIndexByte(s, ':')
	if colon < 0 {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(s[colon+1:], 16, 16)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	addr, ok := parseHexAddr(s[:colon])
	if !ok {
		return netip.Addr{}, 0, false
	}
	return addr, int(port), true
}

// parseHexAddr decodes /proc's address spelling, which is where a hand-rolled
// parser goes wrong.
//
// The kernel prints each 4-byte group as a native-endian 32-bit word, so on every
// little-endian machine the bytes within a group appear REVERSED: 127.0.0.1 is
// "0100007F", and ::1 is "00000000000000000000000001000000". An IPv4-mapped
// address in net/tcp6 therefore reads "0000000000000000FFFF00000100007F". Reversing
// per 4-byte group is the whole transformation — reversing the entire 16 bytes
// instead produces a plausible-looking address that is wrong, which is exactly the
// bug this comment exists to prevent.
//
// The 16-byte form is kept as an IPv6 address even when it is IPv4-mapped, because
// which table a socket sits in is part of the answer.
func parseHexAddr(h string) (netip.Addr, bool) {
	raw, err := hex.DecodeString(h)
	if err != nil {
		return netip.Addr{}, false
	}
	switch len(raw) {
	case 4:
		return netip.AddrFrom4([4]byte{raw[3], raw[2], raw[1], raw[0]}), true
	case 16:
		var b [16]byte
		for g := 0; g < 4; g++ {
			base := g * 4
			for i := 0; i < 4; i++ {
				b[base+i] = raw[base+3-i]
			}
		}
		return netip.AddrFrom16(b), true
	default:
		return netip.Addr{}, false
	}
}

// parseUID returns the uid column, or -1 when it is not a number. -1 rather than 0
// because 0 is root, and a diagnostic that reports an unknown owner as root is
// worse than one that reports it as unknown.
func parseUID(s string) int {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

// splitLines splits on \n and drops the \r a fixture might carry. It does not trim
// other trailing whitespace: /proc/net/unix's path is the rest of the line.
func splitLines(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// fieldsN splits off at most n space- or tab-separated fields and returns the
// remainder verbatim with its leading separators removed. strings.Fields cannot do
// this: the remainder is a UNIX socket path, which may contain spaces.
func fieldsN(s string, n int) (fields []string, rest string) {
	i := 0
	isSep := func(c byte) bool { return c == ' ' || c == '\t' }
	for len(fields) < n {
		for i < len(s) && isSep(s[i]) {
			i++
		}
		if i >= len(s) {
			return fields, ""
		}
		start := i
		for i < len(s) && !isSep(s[i]) {
			i++
		}
		fields = append(fields, s[start:i])
	}
	for i < len(s) && isSep(s[i]) {
		i++
	}
	return fields, s[i:]
}

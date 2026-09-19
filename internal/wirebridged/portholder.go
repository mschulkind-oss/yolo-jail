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
// So the daemon asks, itself, at the moment the bind fails: the LISTEN sockets in
// /proc/net/tcp{,6} give the socket inode, /proc/<pid>/fd gives the owner, and
// /proc/<pid>/{comm,cmdline} names it. The result goes in the same line as the
// errno, because the two facts are only useful together.
//
// TRI-STATE, like every other probe in this repo: "nothing holds it", "something
// holds it and this is what" and "I could not ask" are three different answers
// and are never collapsed. A probe that cannot read /proc says so rather than
// reporting an empty table as an absence — that is exactly how the host's empty
// `ss` output misled four hypotheses in a row.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procNetTCPFiles is the set of per-netns listener tables consulted, in order.
// Both are read because an IPv4 bind collides with a dual-stack listener that
// only appears in the v6 table.
var procNetTCPFiles = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// procRoot is /proc, as a variable only so a test can point the parser at a
// fixture tree; production never rebinds it.
var procRoot = "/proc"

// tcpStateListen is the hex state column's value for LISTEN in /proc/net/tcp.
const tcpStateListen = "0A"

// describePortHolder is the sentence appended to a bind failure. It always
// returns something sayable: the holder, the unidentifiable holder, the absence,
// or the reason it could not look.
func describePortHolder(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("the port holder was not looked up: %q is not a host:port address (%v)", addr, err)
	}
	found, readErrs := listenersOnPort(port)
	if len(found) == 0 {
		if len(readErrs) > 0 {
			// COULD NOT ASK — not "nothing holds it". Both are an empty table
			// one layer down, and reading them alike is the mistake this
			// package is fixing.
			return "the port holder could not be identified: " + strings.Join(readErrs, "; ")
		}
		return fmt.Sprintf("no LISTEN socket on port %s appears in %s, so the conflict is not a "+
			"listener in this network namespace — a socket in another state, another netns, or a "+
			"SO_REUSEADDR race are what is left", port, strings.Join(procNetTCPFiles, " or "))
	}
	parts := make([]string, 0, len(found))
	for _, l := range found {
		parts = append(parts, l.describe(host))
	}
	return "the address is already held: " + strings.Join(parts, "; ")
}

// procListener is one LISTEN row of /proc/net/tcp{,6}.
type procListener struct {
	local string // human-readable, e.g. "127.0.0.1:8214"
	inode string
	uid   string
	table string
}

func (l procListener) describe(wantHost string) string {
	who := l.owner()
	overlap := ""
	if l.local != "" && !strings.HasPrefix(l.local, wantHost+":") {
		// A wildcard listener (0.0.0.0/::) is the common reason a specific
		// loopback bind fails, and a reader who only saw "already in use" would
		// hunt for a listener on the exact address and find none.
		overlap = " (a listener on this address covers the one the bridge wanted)"
	}
	return fmt.Sprintf("%s%s %s [%s]", l.local, overlap, who, l.table)
}

// owner resolves the socket inode to a process by walking /proc/<pid>/fd. An
// unresolvable inode is reported AS unresolvable — the socket exists and is the
// conflict whether or not its owner is readable from this uid.
func (l procListener) owner() string {
	pid, err := pidForSocketInode(l.inode)
	if err != nil {
		return fmt.Sprintf("owned by socket inode %s (uid %s), whose process could not be "+
			"identified: %v", l.inode, l.uid, err)
	}
	if pid == "" {
		return fmt.Sprintf("owned by socket inode %s (uid %s), and no readable %s/<pid>/fd entry "+
			"points at it — the holder is another user's or another namespace's process",
			l.inode, l.uid, procRoot)
	}
	desc := "held by pid " + pid
	if comm := readProcField(pid, "comm"); comm != "" {
		desc += " (" + comm + ")"
	}
	if argv := readProcCmdline(pid); argv != "" {
		desc += ", argv: " + argv
	}
	return desc
}

// listenersOnPort returns every LISTEN socket bound to port, plus the read
// failures that make an empty result mean "unknown" rather than "none".
func listenersOnPort(port string) (found []procListener, readErrs []string) {
	want, err := strconv.Atoi(port)
	if err != nil {
		return nil, []string{fmt.Sprintf("port %q is not numeric (%v)", port, err)}
	}
	wantHex := fmt.Sprintf("%04X", want)
	for _, table := range procNetTCPFiles {
		data, err := os.ReadFile(table)
		if err != nil {
			readErrs = append(readErrs, fmt.Sprintf("reading %s: %v", table, err))
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			// sl local rem st tx rx tr tm retr uid timeout inode
			if len(fields) < 10 || fields[3] != tcpStateListen {
				continue
			}
			local := fields[1]
			colon := strings.LastIndexByte(local, ':')
			if colon < 0 || !strings.EqualFold(local[colon+1:], wantHex) {
				continue
			}
			l := procListener{local: formatProcAddr(local[:colon]) + ":" + port, table: table, uid: fields[7]}
			if len(fields) >= 10 {
				l.inode = fields[9]
			}
			found = append(found, l)
		}
	}
	return found, readErrs
}

// formatProcAddr turns /proc/net/tcp's hex address into its usual spelling.
// IPv4 words are LITTLE-endian per 32-bit group — the single most common way to
// misread this table, and a reversed address in a diagnostic is worse than none.
func formatProcAddr(hex string) string {
	if len(hex)%8 != 0 || len(hex) == 0 {
		return "0x" + hex
	}
	raw := make([]byte, 0, len(hex)/2)
	for i := 0; i < len(hex); i += 8 {
		var word [4]byte
		for j := 0; j < 4; j++ {
			b, err := strconv.ParseUint(hex[i+j*2:i+j*2+2], 16, 8)
			if err != nil {
				return "0x" + hex
			}
			word[j] = byte(b)
		}
		raw = append(raw, word[3], word[2], word[1], word[0])
	}
	ip := net.IP(raw)
	if ip.To16() == nil {
		return "0x" + hex
	}
	return ip.String()
}

// pidForSocketInode finds the process holding socket:[inode]. The error return
// is the "could not ask" arm: an unreadable /proc is not an absent holder.
func pidForSocketInode(inode string) (string, error) {
	if inode == "" || inode == "0" {
		return "", fmt.Errorf("the listener row carried no socket inode")
	}
	want := "socket:[" + inode + "]"
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		pid := entry.Name()
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(procRoot, pid, "fd"))
		if err != nil {
			// Another uid's process, or one that exited mid-walk. Neither is an
			// answer about our inode, so keep looking.
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(procRoot, pid, "fd", fd.Name()))
			if err == nil && link == want {
				return pid, nil
			}
		}
	}
	return "", nil
}

func readProcField(pid, name string) string {
	data, err := os.ReadFile(filepath.Join(procRoot, pid, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readProcCmdline renders the holder's argv. It is bounded and single-lined:
// this string reaches the readiness pipe, whose protocol is one line per record,
// and the argv of an arbitrary process is arbitrary bytes.
func readProcCmdline(pid string) string {
	data, err := os.ReadFile(filepath.Join(procRoot, pid, "cmdline"))
	if err != nil {
		return ""
	}
	argv := strings.Join(strings.FieldsFunc(string(data), func(r rune) bool { return r == 0 }), " ")
	argv = oneLine(argv)
	const max = 400
	if len(argv) > max {
		argv = argv[:max] + "…(truncated)"
	}
	return argv
}

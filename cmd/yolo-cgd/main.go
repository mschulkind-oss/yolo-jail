// Command yolo-cgd is the cgroup-delegate daemon. It listens on a Unix socket,
// reads a
// single-line JSON request per connection, identifies the caller by
// SO_PEERCRED, performs the cgroup op against the container cgroup subtree, and
// writes a single-line JSON response.
//
// Frozen: the socket is chmod 0777 (the container-mapped UID needs it), the
// protocol is single-line JSON both ways, and the container cgroup path comes
// from --container-cgroup.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/cgd"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func main() {
	socket := flag.String("socket", "", "Unix socket to bind")
	containerCgroup := flag.String("container-cgroup", "", "container cgroup v2 path on the host")
	logFile := flag.String("log-file", "", "append per-request audit log here (default: stderr)")
	flag.Parse()
	if *socket == "" || *containerCgroup == "" {
		fmt.Fprintln(os.Stderr, "yolo-cgd: --socket and --container-cgroup are required")
		os.Exit(2)
	}
	setupLog(*logFile)

	_ = os.Remove(*socket)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: *socket, Net: "unix"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo-cgd:", err)
		os.Exit(1)
	}
	ln.SetUnlinkOnClose(false)
	// chmod 0777 — the container-mapped UID must be able to connect.
	_ = os.Chmod(*socket, 0o777)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; ln.Close(); os.Remove(*socket) }()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			break
		}
		go handleConn(conn, *containerCgroup)
	}
	os.Remove(*socket)
}

func handleConn(conn *net.UnixConn, containerCgroup string) {
	defer conn.Close()

	peerPID := peerCredPID(conn)

	// Read the request, capped at 4096 bytes, matching the Python recv loop
	// (`while b"\n" not in data and len(data) < 4096`): stop at the first
	// newline OR the cap, then decode whatever accumulated. An unbounded read
	// would let a newline-less client grow daemon memory.
	line := readCapped(conn, 4096)
	if len(line) == 0 {
		return // empty request — Python returns without responding
	}
	// Strip a trailing newline for the JSON decode (json5 tolerates it, but
	// match Python which splits it off).
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}

	// Per-request audit log (the module map freezes this for cgd): one line
	// with op + peer_pid + the raw request, then the response.
	logf("op=%s peer_pid=%d request=%s", cgd.RequestOp(line), peerPID, string(line))

	req, ok := cgd.ParseRequest(line)
	if !ok {
		resp := errShape("invalid request")
		writeResp(conn, resp)
		logResp(resp)
		return
	}
	resp := cgd.Handle(req, containerCgroup, peerPID)
	writeResp(conn, resp)
	logResp(resp)
}

// logging plumbing (audit trail; the daemon supervisor also captures stderr).
var auditLog *log.Logger

func setupLog(path string) {
	out := os.Stderr
	if path != "" {
		if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644); err == nil {
			out = f
		}
	}
	auditLog = log.New(out, "", log.LstdFlags)
}

func logf(format string, args ...any) {
	if auditLog != nil {
		auditLog.Printf(format, args...)
	}
}

func logResp(resp *jsonx.OrderedMap) {
	s, _ := jsonx.DumpsCompact(resp)
	logf("  response=%s", s)
}

// readCapped reads until the first '\n' or `cap` bytes, whichever comes first,
// returning the accumulated bytes (Python: recv-4096-chunks until a newline is
// seen or len >= cap). The newline, if any, is included (the caller strips it).
func readCapped(conn *net.UnixConn, cap int) []byte {
	buf := make([]byte, 0, 256)
	chunk := make([]byte, 4096)
	for len(buf) < cap {
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for _, b := range chunk[:n] {
				if b == '\n' {
					return buf
				}
			}
		}
		if err != nil {
			break
		}
	}
	if len(buf) > cap {
		buf = buf[:cap]
	}
	return buf
}

func writeResp(conn *net.UnixConn, resp *jsonx.OrderedMap) {
	s, err := jsonx.DumpsCompact(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write([]byte(s + "\n"))
}

func errShape(msg string) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("ok", false)
	m.Set("error", msg)
	return m
}

// peerCredPID is defined per-GOOS: peercred_linux.go reads SO_PEERCRED;
// peercred_other.go is a stub returning 0 (the daemon is Linux-only, but the
// whole Go tree must still build on darwin — macos-user is a supported host).

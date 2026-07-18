// Command yolo-cgd is the Go port of the builtin cgroup-delegate daemon
// (go-port plan Stage 7, Commit B). It listens on a Unix socket, reads a
// single-line JSON request per connection, identifies the caller by
// SO_PEERCRED, performs the cgroup op against the container cgroup subtree, and
// writes a single-line JSON response.
//
// Frozen: the socket is chmod 0777 (the container-mapped UID needs it), the
// protocol is single-line JSON both ways, and the container cgroup path comes
// from --container-cgroup.
package main

import (
	"bufio"
	"flag"
	"fmt"
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
	flag.Parse()
	if *socket == "" || *containerCgroup == "" {
		fmt.Fprintln(os.Stderr, "yolo-cgd: --socket and --container-cgroup are required")
		os.Exit(2)
	}

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

	// Read a single line (up to 4096 bytes), matching the Python recv loop.
	reader := bufio.NewReaderSize(conn, 4096)
	line, err := reader.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		return // empty request — Python returns without responding
	}
	// Strip the trailing newline for the JSON decode.
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}

	req, ok := cgd.ParseRequest(line)
	if !ok {
		writeResp(conn, errShape("invalid request"))
		return
	}
	resp := cgd.Handle(req, containerCgroup, peerPID)
	writeResp(conn, resp)
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

// peerCredPID returns the connecting peer's PID via SO_PEERCRED (Linux), or 0
// if unavailable. Mirrors the SO_PEERCRED read in _cgroup_delegate_handler
// (only the PID is used; uid/gid are ignored).
func peerCredPID(conn *net.UnixConn) int {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0
	}
	var pid int
	_ = raw.Control(func(fd uintptr) {
		ucred, cerr := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if cerr == nil && ucred != nil {
			pid = int(ucred.Pid)
		}
	})
	return pid
}

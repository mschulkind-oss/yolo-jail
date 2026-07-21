//go:build linux

package run

import (
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/cgd"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// startCgroupDelegateInProc runs the builtin cgroup delegate as an IN-PROCESS
// goroutine: bind the socket, chmod 0777, and serve single-line JSON requests,
// LAZILY resolving the container cgroup on the first request (the container is
// up by then). Reuses the internal/cgd handler. Returns a stop func + true, or
// false when cgroup v2 is unavailable.
func (o *Options) startCgroupDelegateInProc(cname, rt, sockPath string) (func(), bool) {
	if o.IsMacOS || !o.PathExists("/sys/fs/cgroup/cgroup.controllers") {
		return nil, false
	}
	_ = os.Remove(sockPath)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		return nil, false
	}
	ln.SetUnlinkOnClose(false)
	_ = os.Chmod(sockPath, 0o777)

	var (
		mu              sync.Mutex
		containerCgroup string
		resolved        bool
		done            = make(chan struct{})
	)
	go func() {
		for {
			conn, aerr := ln.AcceptUnix()
			if aerr != nil {
				return
			}
			select {
			case <-done:
				_ = conn.Close()
				return
			default:
			}
			go func(c *net.UnixConn) {
				defer c.Close()
				line := readLineBounded(c, 4096)
				if len(line) == 0 {
					return
				}
				r, ok := cgd.ParseRequest(line)
				if !ok {
					return
				}
				mu.Lock()
				if !resolved {
					containerCgroup = o.resolveContainerCgroup(cname, rt)
					resolved = true
				}
				cg := containerCgroup
				mu.Unlock()
				if cg == "" {
					resp := jsonx.NewOrderedMap()
					resp.Set("ok", false)
					resp.Set("error", "Container cgroup not yet available")
					writeJSONLine(c, resp)
					return
				}
				peerPID := cgdPeerPID(c)
				resp := cgd.Handle(r, cg, peerPID)
				writeJSONLine(c, resp)
			}(conn)
		}
	}()
	stop := func() {
		close(done)
		// Wake the accept loop, then close the listener.
		if wake, derr := net.DialTimeout("unix", sockPath, 200_000_000); derr == nil {
			_ = wake.Close()
		}
		_ = ln.Close()
	}
	return stop, true
}

// cgdPeerPID reads the connecting peer's host PID via SO_PEERCRED (Linux).
func cgdPeerPID(conn *net.UnixConn) int {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0
	}
	var pid int
	_ = raw.Control(func(fd uintptr) {
		if ucred, cerr := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED); cerr == nil && ucred != nil {
			pid = int(ucred.Pid)
		}
	})
	return pid
}

// readLineBounded reads until '\n' or cap bytes (the request framing).
func readLineBounded(conn *net.UnixConn, cap int) []byte {
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 4096)
	for len(buf) < cap {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for _, b := range buf {
				if b == '\n' {
					return buf
				}
			}
		}
		if err != nil {
			break
		}
	}
	return buf
}

func writeJSONLine(conn *net.UnixConn, m *jsonx.OrderedMap) {
	s, err := jsonx.DumpsCompact(m)
	if err != nil {
		return
	}
	_, _ = conn.Write(append([]byte(s), '\n'))
}

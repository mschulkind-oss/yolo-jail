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

	// PRINCIPLE 5 of docs/reference/security-shim.md: every operation is recorded
	// with the caller's host PID, the operation and the result, on the host
	// filesystem outside the container's reach. loopholeLogDir() is under the
	// host's own state dir, which a jail cannot write.
	//
	// The warn sink is the housekeeping log, never the terminal: this runs behind a
	// launch that may be handing the TTY to an agent, and a notice printed there
	// overlays whatever is drawing.
	audit := cgd.NewAuditor(loopholeLogDir(), func(msg string) {
		o.housekeepingNote("cgd-audit: %s", msg)
	})

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
					// AN UNPARSEABLE REQUEST IS AN EVENT, not nothing: it is what a
					// probe of this socket looks like from in here, so it is the last
					// thing that should be dropped silently. cgd.RequestOp returns ""
					// for it, which the log renders as UNPARSEABLE.
					audit.Append(cgd.AuditEvent{
						PeerPID: cgdPeerPID(c),
						Op:      cgd.RequestOp(line),
						Err:     "request did not parse",
					})
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
					audit.Append(cgd.AuditEvent{
						PeerPID: cgdPeerPID(c),
						Op:      cgd.RequestOp(line),
						Err:     "container cgroup not yet available",
					})
					return
				}
				peerPID := cgdPeerPID(c)
				resp := cgd.Handle(r, cg, peerPID)
				writeJSONLine(c, resp)
				// Logged AFTER the answer so a slow disk cannot delay the reply, and
				// with the handler's own verdict rather than a second derivation of it.
				ok2, errText := cgdResponseVerdict(resp)
				audit.Append(cgd.AuditEvent{
					PeerPID: peerPID,
					Op:      cgd.RequestOp(line),
					Cgroup:  cg,
					OK:      ok2,
					Err:     errText,
				})
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

// cgdResponseVerdict reads the handler's own answer rather than recomputing it.
// The audit line must say what the caller was TOLD, so deriving the verdict a
// second time from the request would let the two disagree — which is exactly the
// kind of divergence an audit trail is consulted to rule out.
func cgdResponseVerdict(resp *jsonx.OrderedMap) (ok bool, errText string) {
	if v, present := resp.Get("ok"); present {
		if b, isBool := v.(bool); isBool {
			ok = b
		}
	}
	if v, present := resp.Get("error"); present {
		if s, isStr := v.(string); isStr {
			errText = s
		}
	}
	return ok, errText
}

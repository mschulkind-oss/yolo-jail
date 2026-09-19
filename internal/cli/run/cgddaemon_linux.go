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
//
// EVERY `false` RETURN SAYS WHY, and this is the place the manifest already points
// at for it: packs/cgroup-delegate's own comment says the cgroup-v2 question stays
// here because this is "the only place that can report it in the terms an operator
// can act on". It did not report it — all three declines were a bare `return nil,
// false` that the caller turned into a loophole the user switched ON and that then
// simply was not there. Which decline it was decides the register; see
// noteCgroupDelegateUnavailable and noteCgroupDelegateFailed.
func (o *Options) startCgroupDelegateInProc(cname, rt, sockPath string) (func(), bool) {
	if o.IsMacOS || !o.PathExists("/sys/fs/cgroup/cgroup.controllers") {
		o.noteCgroupDelegateUnavailable("this kernel exposes no cgroup v2 " +
			"(/sys/fs/cgroup/cgroup.controllers is absent)")
		return nil, false
	}
	// Discarded: ENOENT is the normal case, and a stale socket that survives fails
	// the ListenUnix below with EADDRINUSE, which IS reported — with the error, which
	// is the part a reader needs.
	_ = os.Remove(sockPath)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		o.noteCgroupDelegateFailed("could not bind " + sockPath + ": " + err.Error())
		return nil, false
	}
	ln.SetUnlinkOnClose(false)
	if cerr := os.Chmod(sockPath, 0o777); cerr != nil {
		// NOT a decline: the listener is up and yolo will serve on it. But 0777 is
		// what lets the jail's own uid open it, so without the chmod the delegate is
		// running and unusable — a capability present on the host and missing in the
		// jail, which is the hardest version of this failure to diagnose from inside.
		o.noteCgroupDelegateFailed("bound " + sockPath +
			" but could not chmod it 0777 (" + cerr.Error() +
			"); the jail may not be able to open it")
	}

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

// noteCgroupDelegateUnavailable and noteCgroupDelegateFailed are how the in-process
// delegate reports NOT starting. Two functions rather than one with a severity
// argument, because the distinction is the whole content:
//
//   - UNAVAILABLE is "yolo could not ask" — this kernel has no cgroup v2, so there is
//     nothing to install and nothing to fix here. The register is [dim] and the
//     sentence says so outright, for the reason loophole-system.md gives the
//     unsupported-platform message: a reader not told that nothing is missing spends
//     the afternoon proving it.
//   - FAILED is "yolo asked and it did not work" — a bind or a chmod that returned an
//     error, naming the path and the error. That is an actionable fault on a machine
//     that CAN run the delegate, so it is a [yellow] warning.
//
// Collapsing the two would flatten exactly the distinction AGENTS.md preserves for the
// loopback witness (OQ-R3): a host yolo could not ask is never reported as a failure it
// could have avoided. Both are unconditional — the delegate only reaches here when the
// user switched its loophole ON, so neither line can appear on a launch that did not
// ask for the capability.
//
// THEY LIVE IN THE LINUX FILE BECAUSE THEY DESCRIBE LINUX FACTS. Every decline they
// word is one this file takes, and off Linux there is no such decline to report — the
// platform axis answers instead (cgddaemon_other.go). Putting them in the untagged
// loopholesruntime.go, where they started, made them unused on darwin and the
// `GOOS=darwin staticcheck` arm of `just lint-ci` rejected the tree (U1000).
func (o *Options) noteCgroupDelegateUnavailable(reason string) {
	o.pr(o.Stdout).print("[dim]The cgroup-delegate loophole is enabled but the delegate " +
		"cannot run on this machine: " + reason + ". Nothing is missing — yolo-cglimit " +
		"will be unavailable in the jail.[/dim]")
}

func (o *Options) noteCgroupDelegateFailed(reason string) {
	o.pr(o.Stdout).print("[yellow]Warning: the cgroup-delegate loophole is enabled but " +
		"its delegate " + reason + " — yolo-cglimit will not work in this jail.[/yellow]")
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

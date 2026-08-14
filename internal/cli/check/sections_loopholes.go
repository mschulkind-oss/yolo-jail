package check

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// checkLoopholes surfaces loophole discovery + each
// loophole's own self-check. Bad manifests warn; non-zero self-checks fail.
func (o *Options) checkLoopholes(r *reporter) {
	if o.inJail() {
		r.ok("Inside jail — loophole checks skipped (managed by host)")
		return
	}
	entries := loopholes.ValidateLoopholes("", false, true)
	if len(entries) == 0 {
		r.ok(fmt.Sprintf("No loopholes installed (%s)", loopholes.UserLoopholesDir()))
		return
	}
	// `enabled` is writable at workspace scope (loophole-packaging.md §4.3b), so
	// the disclosure is the only protection left for a default-on loophole: a
	// workspace-sourced disable must WARN and name the file, never render as a
	// green line. Only a disable from the loophole's own manifest is an ok.
	wsDisabled := config.WorkspaceDisabledLoopholes(o.Workspace)
	for _, e := range entries {
		if e.Err != "" {
			r.warn("loophole "+filepath.Base(e.Path)+": invalid manifest", e.Err)
			continue
		}
		lp := e.Loophole
		if file, ok := wsDisabled[lp.Name]; ok {
			r.warn("loophole "+lp.Name+": disabled by "+file+" (workspace scope)",
				"An agent-editable file turned an installed loophole off; jails "+
					"launched from this workspace run without it. Re-enable it there, "+
					"or move the override to "+paths.UserConfigPath()+".")
			continue
		}
		if !lp.Enabled {
			r.ok("loophole " + lp.Name + ": disabled")
			continue
		}
		if !lp.RequirementsMet() {
			reason, _ := lp.InactiveReason()
			r.ok("loophole " + lp.Name + ": inactive (" + reason + ")")
			continue
		}
		if len(lp.DoctorCmd) == 0 {
			r.ok("loophole " + lp.Name + ": no self-check declared")
			continue
		}
		results := loopholes.RunDoctorChecks([]*loopholes.Loophole{lp}, 10*time.Second)
		res := results[0]
		switch {
		case res.RC != nil && *res.RC == 0:
			r.ok("loophole " + lp.Name + ": self-check ok")
			if lp.Name == brokerLoopholeName {
				o.checkBrokerCredsFreshness(r)
				o.reportBrokerDaemon(r)
			}
		case res.RC == nil:
			out := res.Output
			if out == "" {
				out = "command missing"
			}
			r.warn("loophole "+lp.Name+": self-check could not run", out)
		default:
			problems := nixdiag.SplitSelfCheckProblems(res.Output)
			if len(problems) == 0 {
				r.fail(fmt.Sprintf("loophole %s: self-check failed (rc=%d)", lp.Name, *res.RC), "no output")
			} else {
				for _, p := range problems {
					r.fail("loophole "+lp.Name+": "+p.Title, p.Detail)
				}
			}
		}
	}
}

// reportBrokerDaemon reports the broker liveness block (after a green
// self-check): live / not running / stale PID / unresponsive.
func (o *Options) reportBrokerDaemon(r *reporter) {
	status := o.brokerStatus()
	switch {
	case status.pidLive && status.pingOK:
		r.ok(fmt.Sprintf("loophole claude-oauth-broker: daemon live (pid=%d, ping ok)", status.pid))
	case !status.pidPresent:
		r.warn("loophole claude-oauth-broker: daemon not running",
			"First `yolo run` will spawn it; "+
				"`yolo broker status` reports state, "+
				"`yolo broker restart` cycles.")
	case !status.pidLive:
		r.fail(fmt.Sprintf("loophole claude-oauth-broker: stale PID file, pid %d not running", status.pid),
			"Run `yolo broker restart` to clean up and respawn.")
	default:
		socketState := "missing"
		if status.socketExists {
			socketState = "present"
		}
		r.fail(fmt.Sprintf("loophole claude-oauth-broker: daemon unresponsive (pid=%d, socket %s, ping failed)", status.pid, socketState),
			"Run `yolo broker restart` — typical after a "+
				"binary upgrade; old code still loaded in memory.")
	}
}

// checkBrokerRelay probes one jail's broker relay end-to-end THROUGH THE HOP THE
// JAIL USES — read the endpoint file, pin its certificate, present its token, then
// ping the singleton behind it — naming the failing LAYER.
//
// Probing the relay's own Unix socket instead would test a path no jail travels:
// that socket is host-only now, so it can be perfectly healthy while the jail's
// half is unpublished, stale, or mismatched. The prober can authenticate at all
// only because it runs as the uid that published the file and reads the same 0600
// file the jail does — a property that exists because the token lives there rather
// than in the jail's environment. It never prints the file's contents.
func (o *Options) checkBrokerRelay(r *reporter, label, endpointPath, rt, cname string) {
	if !o.PathExists(endpointPath) {
		r.fail(label+": relay endpoint missing",
			fmt.Sprintf("Expected %s.  The per-jail relay never started, its front "+
				"failed to publish, or its host-services dir was removed.  Any `yolo` "+
				"invocation against this jail respawns it.", endpointPath))
		return
	}
	if !svcendpoint.Probe(endpointPath) {
		r.fail(label+": relay endpoint incomplete",
			fmt.Sprintf("%s exists but does not parse as a complete endpoint "+
				"(address, certificate, token).  It was truncated or written by an "+
				"older yolo; any `yolo` invocation against this jail republishes it.",
				endpointPath))
		return
	}
	// DialLocal, not Dial: the published address names the container runtime's
	// gateway, which a jail resolves and this host does not. Same port, same pinned
	// certificate, same token — only the name substituted.
	conn, err := svcendpoint.DialLocal(endpointPath, 2*time.Second)
	if err != nil {
		if errors.Is(err, svcendpoint.ErrAuthRejected) {
			r.fail(label+": relay rejected this jail's token",
				fmt.Sprintf("The relay named by %s refused the token in that file, so "+
					"the file is stale relative to the running relay (it restarted and "+
					"republished, or a predecessor's file was left behind).  Any `yolo` "+
					"invocation against this jail republishes it.", endpointPath))
			return
		}
		r.fail(label+": relay endpoint dead",
			fmt.Sprintf("Dialing the relay named by %s failed: %s.  The relay process "+
				"exited; any `yolo` invocation against this jail respawns it.", endpointPath, err))
		return
	}
	ok := brokerPingConn(conn, 2*time.Second)
	_ = conn.Close()
	if ok {
		if v := o.relayEndpointVisibleInJail(rt, cname); v != nil && !*v {
			r.fail(label+": relay ok on host, endpoint invisible in-jail",
				"The host-services dir was recreated after the container mounted "+
					"it (host /tmp cleanup or a teardown/startup race): the "+
					"jail's bind mount still points at the old, deleted "+
					"directory, so in-jail auth requests 502 even though the "+
					"host-side relay answers.  That directory now holds this jail's "+
					"CREDENTIAL, so re-reading cannot recover it either.  Relaunch "+
					"the jail to remount the directory.")
		} else {
			r.ok(label + ": relay ok (cert-pinned, token-authenticated), broker answers through it")
		}
	} else {
		r.fail(label+": relay up, broker unreachable",
			"The relay authenticated and accepted, but the singleton broker did not "+
				"answer the proxied ping.  Check `yolo broker status` / "+
				"`yolo broker restart`.")
	}
}

// checkHostServiceLiveness verifies, for each running
// jail, that each external host_daemon's socket is alive.
func (o *Options) checkHostServiceLiveness(r *reporter) {
	if o.inJail() {
		return // inside jail — host sockets aren't reachable
	}
	entries := loopholes.ValidateLoopholes("", false, true)
	var externals []*loopholes.Loophole
	for _, e := range entries {
		lp := e.Loophole
		if lp != nil && e.Err == "" && lp.Enabled && lp.RequirementsMet() && lp.HostDaemon != nil {
			externals = append(externals, lp)
		}
	}
	if len(externals) == 0 {
		r.ok("no host-side daemons to probe")
		return
	}
	rt := o.detectRuntimeForListing()
	if rt == "" {
		r.warn("no container runtime found — skipping liveness probe", "")
		return
	}
	cnames, listErr := o.listRunningJailNames(rt)
	if listErr != "" {
		r.warn("could not list running jails via "+rt, firstLine(listErr))
		return
	}
	if len(cnames) == 0 {
		r.ok("no jails running — nothing to probe")
		return
	}
	for _, cname := range cnames {
		socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
		for _, lp := range externals {
			label := fmt.Sprintf("loophole %s @ %s", lp.Name, cname)
			if lp.Name == brokerLoopholeName {
				o.checkBrokerRelay(r, label,
					filepath.Join(socketsDir, lp.Name+paths.ServiceEndpointExt), rt, cname)
				continue
			}
			if lp.Transport == loopholes.TransportLoopbackTLS {
				o.checkLoopbackTLSService(r, label, filepath.Join(socketsDir, lp.Name+paths.ServiceEndpointExt), lp.Name)
				continue
			}
			sockPath := filepath.Join(socketsDir, lp.Name+".sock")
			if !o.PathExists(sockPath) {
				r.fail(label+": no socket",
					fmt.Sprintf("Expected %s.  Daemon never started or "+
						"crashed at spawn.  Tail "+
						"~/.local/share/yolo-jail/logs/host-service-%s.log "+
						"for the reason; restart the jail to respawn.", sockPath, lp.Name))
				continue
			}
			conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
			if err != nil {
				r.fail(label+": socket dead",
					fmt.Sprintf("connect(%s) failed: %s.  "+
						"Daemon process likely exited; restart the jail.", sockPath, err))
				continue
			}
			_ = conn.Close()
			r.ok(label + ": socket accepting")
		}
	}
}

// checkLoopbackTLSService probes one loopback-TLS host service end-to-end, naming
// the failing LAYER.
//
// The prober runs HOST-SIDE and as the same uid that published the file, so it can
// read the endpoint and authenticate exactly as the jail does — a property that only
// exists because the token lives in the file rather than in the jail's environment.
// It never prints the file's contents: that file is this jail's credential.
func (o *Options) checkLoopbackTLSService(r *reporter, label, endpointPath, name string) {
	if !o.PathExists(endpointPath) {
		r.fail(label+": no endpoint published",
			fmt.Sprintf("Expected %s.  Daemon never started or "+
				"crashed at spawn.  Tail "+
				"~/.local/share/yolo-jail/logs/host-service-%s.log "+
				"for the reason; restart the jail to respawn.", endpointPath, name))
		return
	}
	if !svcendpoint.Probe(endpointPath) {
		r.fail(label+": endpoint file incomplete",
			fmt.Sprintf("%s exists but does not parse as a complete endpoint "+
				"(address, certificate, token).  It was truncated or written by an "+
				"older yolo; restart the jail to republish it.", endpointPath))
		return
	}
	// DialLocal, not Dial: the published address names the runtime's gateway, which
	// a jail resolves and this host does not. Same port, same pinned cert, same
	// token — only the name substituted.
	conn, err := svcendpoint.DialLocal(endpointPath, 2*time.Second)
	if err != nil {
		detail := fmt.Sprintf("Dialing the listener named by %s failed: %s.  "+
			"Daemon process likely exited; restart the jail.", endpointPath, err)
		if errors.Is(err, svcendpoint.ErrAuthRejected) {
			detail = fmt.Sprintf("The listener named by %s rejected the token in that "+
				"file.  The file is stale relative to the running daemon (it restarted "+
				"and republished, or a predecessor's file was left behind); restart the "+
				"jail.", endpointPath)
		}
		r.fail(label+": listener unreachable", detail)
		return
	}
	_ = conn.Close()
	r.ok(label + ": endpoint accepting (cert-pinned, token-authenticated)")
}

// firstLine returns the first line of s, or "" when s is empty.
func firstLine(s string) string {
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}

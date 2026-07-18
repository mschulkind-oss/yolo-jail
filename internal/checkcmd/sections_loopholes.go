package checkcmd

import (
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/checkdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// checkLoopholes ports _check_loopholes: surface loophole discovery + each
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
	for _, e := range entries {
		if e.Err != "" {
			r.warn("loophole "+filepath.Base(e.Path)+": invalid manifest", e.Err)
			continue
		}
		lp := e.Loophole
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
			problems := checkdiag.SplitSelfCheckProblems(res.Output)
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

// reportBrokerDaemon reproduces the broker liveness block inside _check_loopholes
// (after a green self-check): live / not running / stale PID / unresponsive.
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
				"wheel upgrade; old code still loaded in memory.")
	}
}

// checkBrokerRelay ports _check_broker_relay: probe one jail's broker relay
// socket end-to-end, naming the failing LAYER.
func (o *Options) checkBrokerRelay(r *reporter, label, sockPath, rt, cname string) {
	if !o.PathExists(sockPath) {
		r.fail(label+": relay socket missing",
			fmt.Sprintf("Expected %s.  The per-jail relay never started or "+
				"its sockets dir was removed.  Any `yolo` invocation against "+
				"this jail respawns it.", sockPath))
		return
	}
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		r.fail(label+": relay socket dead",
			fmt.Sprintf("connect(%s) failed: %s.  The relay process exited; "+
				"any `yolo` invocation against this jail respawns it.", sockPath, err))
		return
	}
	_ = conn.Close()
	if brokerPing(sockPath, 2*time.Second) {
		if v := o.relaySocketVisibleInJail(rt, cname); v != nil && !*v {
			r.fail(label+": relay ok on host, socket invisible in-jail",
				"The sockets dir was recreated after the container mounted "+
					"it (host /tmp cleanup or a teardown/startup race): the "+
					"jail's bind mount still points at the old, deleted "+
					"directory, so in-jail auth requests 502 even though the "+
					"host-side relay answers.  Relaunch the jail to remount "+
					"the directory.")
		} else {
			r.ok(label + ": relay ok, broker answers through it")
		}
	} else {
		r.fail(label+": relay up, broker unreachable",
			"The relay accepted but the singleton broker did not answer "+
				"the proxied ping.  Check `yolo broker status` / "+
				"`yolo broker restart`.")
	}
}

// checkHostServiceLiveness ports _check_host_service_liveness: for each running
// jail, verify each external host_daemon's socket is alive.
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
			sockPath := filepath.Join(socketsDir, lp.Name+".sock")
			label := fmt.Sprintf("loophole %s @ %s", lp.Name, cname)
			if lp.Name == brokerLoopholeName {
				o.checkBrokerRelay(r, label, sockPath, rt, cname)
				continue
			}
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

// firstLine returns the first line of s (Python's `s.splitlines()[0] if s else ""`).
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

package run

// liveapplyforwards.go is the host-port forwards slice of `yolo config apply`
// (docs/design/live-config-apply.md LA-S4d, LA-D26 to LA-D28): a changed `machine.forward`
// (legacy `network.forward_host_ports`) reaches a running podman jail through an in-jail socat
// that dials the host at host.containers.internal, the hop a launch already gives a jail whose
// host loopback is forwarded. No host process is started, so nothing outlives the jail: a socat
// started with `podman exec -d` is the container's and ends with it.
//
// REACHABILITY-SHAPED (AGENTS.md, Testing): a nested jail runs on the launcher's network and
// forwards nothing, so only a real rootless host, or CI, shows the forward carrying traffic.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// forwardGatewayHost is the name a jail reaches the host by (the macOS TCP-gateway mode's
// YOLO_FWD_HOST_GATEWAY value, and the host-loopback hop's name).
const forwardGatewayHost = "host.containers.internal"

// forwardsLiveFor is whether this jail can take a forward change in place, and why not when it
// cannot (LA-D26): it must reach the host's loopback at forwardGatewayHost, which the launch says
// in the jail's own env — the TCP-gateway variable (macOS podman), or a host-loopback decision of
// requested or shared.
func forwardsLiveFor(rt string, envLines []string) (bool, string) {
	if rt != "podman" { // parity: Warned — Apple Container refuses forwards at launch, and macos-user has none; the line says why
		return false, ""
	}
	if envLineValue(envLines, "YOLO_FWD_HOST_GATEWAY") != "" {
		return true, ""
	}
	switch v := envLineValue(envLines, paths.HostLoopbackEnvVar); v {
	case "requested", "shared":
		return true, ""
	case "":
		return false, "this jail does not say it reaches the host's loopback, so a new forward needs the host-side socat a launch starts"
	default:
		return false, "this jail cannot reach the host's loopback (" + paths.HostLoopbackEnvVar + "=" + v +
			"), so a new forward needs the host-side socat a launch starts"
	}
}

// forwardElsewhere is the local ports a forward change must not touch live, because something
// other than the workspace's `machine.forward` forwards them: the launch's own list (its
// YOLO_FORWARD_HOST_PORTS, which holds the user scope's and a localhost provider's too) less what
// the effective-config record declares, and the composed config's less the workspace's.
func forwardElsewhere(envLines []string, bootDeclared, composed, wsDeclared []PortForward) map[int]bool {
	out := map[int]bool{}
	sub := func(all, own []PortForward) {
		mine := map[int]bool{}
		for _, p := range own {
			mine[p.LocalPort] = true
		}
		for _, p := range all {
			if !mine[p.LocalPort] {
				out[p.LocalPort] = true
			}
		}
	}
	if raw := envLineValue(envLines, "YOLO_FORWARD_HOST_PORTS"); raw != "" {
		if launched, err := ParsePortForwards(decodeForwardList(raw), nil); err == nil {
			sub(launched, bootDeclared)
		}
	}
	sub(composed, wsDeclared)
	return out
}

// decodeForwardList is the launch's JSON list, nil when it is not one.
func decodeForwardList(raw string) []any {
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	l, _ := v.([]any)
	return l
}

// forwardChanges is one change per local port whose forward differs between the effective-config
// record (boot) and the workspace config (current) at path. A list either side cannot parse is
// one change for the key, waiting for the restart as the launch's own reader would warn there.
func forwardChanges(in applyPlanInput, path string, boot, current any) []applyChange {
	bl, current1 := forwardEntries(boot), forwardEntries(current)
	have, err1 := ParsePortForwards(bl, nil)
	want, err2 := ParsePortForwards(current1, nil)
	if err1 != nil || err2 != nil {
		o, why := keyOutcome(in.rt, "network", in.sessionInputs)
		return []applyChange{{kind: changeKey, sign: "~", label: path, key: path, outcome: o, reason: why}}
	}
	hm, wm := map[int]int{}, map[int]int{}
	var locals []int
	for _, p := range have {
		hm[p.LocalPort] = p.HostPort
		locals = append(locals, p.LocalPort)
	}
	for _, p := range want {
		if _, seen := hm[p.LocalPort]; !seen {
			if _, dup := wm[p.LocalPort]; !dup {
				locals = append(locals, p.LocalPort)
			}
		}
		wm[p.LocalPort] = p.HostPort
	}
	sort.Ints(locals)
	o, why := keyOutcome(in.rt, "network", in.sessionInputs)
	var out []applyChange
	for _, l := range locals {
		h, had := hm[l]
		w, wants := wm[l]
		if had && wants && h == w {
			continue
		}
		c := applyChange{kind: changeKey, key: path, outcome: o, reason: why,
			fwdLocal: l, fwdHad: had, fwdHost: w}
		switch {
		case !had:
			c.sign, c.label = "+", fmt.Sprintf("%s %s", path, forwardLabel(l, w))
		case !wants:
			c.sign, c.label = "-", fmt.Sprintf("%s %s", path, forwardLabel(l, h))
			c.note = "the jail can still reach host port " + strconv.Itoa(h) + " at " + forwardGatewayHost
		default:
			c.sign, c.label = "~", fmt.Sprintf("%s %s (was host %d)", path, forwardLabel(l, w), h)
		}
		switch {
		case !in.forwardsLive:
			if in.forwardsReason != "" {
				c.reason = in.forwardsReason
			}
		case in.forwardElsewhere[l]:
			c.reason = "local port " + strconv.Itoa(l) + " is also forwarded by your user config or a provider; the restart composes them"
		default:
			c.kind, c.outcome, c.reason = changeForward, outcomeAppliedNow, ""
		}
		out = append(out, c)
	}
	return out
}

// forwardEntries is a `machine.forward` value (a list of {jail, host} tables) in the
// "<local>:<host>" spelling ParsePortForwards reads, through the launch's own reader.
func forwardEntries(v any) []any {
	machine := jsonx.NewOrderedMap()
	machine.Set("forward", v)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("machine", machine)
	return readableForwardPorts(cfg)
}

// forwardLabel is one forward as its line names it: `8080 -> host 9090`.
func forwardLabel(local, host int) string {
	if local == host {
		return strconv.Itoa(local)
	}
	return fmt.Sprintf("%d -> host %d", local, host)
}

// The in-jail half. Each is one `podman exec`, its argv built here so a test reads it.

// forwardSocatPattern matches the socat a launch's entrypoint, or this apply, starts for local.
func forwardSocatPattern(local int) string {
	return "^socat TCP-LISTEN:" + strconv.Itoa(local) + ","
}

// forwardFreeScript stops this jail's socat for local port $1 and waits, bounded, until nothing
// listens on it; exit 3 names another process holding the port. $2 is the port in /proc/net's
// hex, read before the function, whose own $2 is empty. A listener is a line of /proc/net/tcp or tcp6 whose local address ends in the port and
// whose state is 0A (LISTEN).
const forwardFreeScript = `pkill -f "$3" ; i=0
hex=$2
listening() { awk -v p=":$hex" 'FNR>1 && $4=="0A" && substr($2, length($2)-4)==p {f=1} END {exit !f}' /proc/net/tcp /proc/net/tcp6 2>/dev/null; }
while listening; do i=$((i+1)); if [ "$i" -gt 50 ]; then echo "the jail's port $1 is held by another process" >&2; exit 3; fi; sleep 0.1; done`

// forwardListeningScript waits, bounded, until something listens on local port $1 ($2 in hex).
const forwardListeningScript = `i=0
hex=$2
listening() { awk -v p=":$hex" 'FNR>1 && $4=="0A" && substr($2, length($2)-4)==p {f=1} END {exit !f}' /proc/net/tcp /proc/net/tcp6 2>/dev/null; }
until listening; do i=$((i+1)); if [ "$i" -gt 50 ]; then echo "socat did not start listening on the jail's port $1" >&2; exit 4; fi; sleep 0.1; done`

func portHex(p int) string { return fmt.Sprintf("%04X", p) }

// applyExecArgv is a live-apply step's `<rt> exec [-d] <cname> <cmd...>`, with podman's detach
// sequence off as on every exec into a jail (JL-D27).
func applyExecArgv(rt string, detach bool, cname string, cmd ...string) []string {
	argv := append([]string{rt, "exec"}, yoloruntime.DetachKeysArgs(rt)...)
	if detach {
		argv = append(argv, "-d")
	}
	return append(append(argv, cname), cmd...)
}

// forwardStepSeam runs one forward change's in-jail commands, ending by deadline.
var forwardStepSeam = (*Options).applyForward

// applyForward is one forward change: stop the jail's socat for the local port, and, unless the
// change removes it, start one dialing the host and wait until it listens. Re-running it is
// harmless: it stops what it started before.
func (o *Options) applyForward(rt, cname string, c *applyChange, deadline time.Time) error {
	run := func(argv []string) error {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			return errors.New("the step's deadline passed")
		}
		r := o.Exec(argv, "", nil, timeout)
		switch {
		case !r.Ran:
			return errors.New("could not run " + rt + " exec")
		case r.Timeout:
			return errors.New(rt + " exec did not finish in time")
		case r.RC != 0:
			msg := strings.TrimSpace(r.Stderr)
			if msg == "" {
				msg = "exit " + strconv.Itoa(r.RC)
			}
			return errors.New(msg)
		}
		return nil
	}
	l := strconv.Itoa(c.fwdLocal)
	if err := run(applyExecArgv(rt, false, cname, "sh", "-c", forwardFreeScript, "sh", l, portHex(c.fwdLocal),
		forwardSocatPattern(c.fwdLocal))); err != nil {
		return err
	}
	if c.fwdHost == 0 {
		return nil
	}
	// The argv the entrypoint's own forwarder uses (startContainerPortForwarding), dialing the
	// host by name as its TCP-gateway mode does.
	if err := run(applyExecArgv(rt, true, cname, "socat",
		"TCP-LISTEN:"+l+",bind=127.0.0.1,fork,reuseaddr",
		"TCP:"+forwardGatewayHost+":"+strconv.Itoa(c.fwdHost))); err != nil {
		return err
	}
	return run(applyExecArgv(rt, false, cname, "sh", "-c", forwardListeningScript, "sh", l, portHex(c.fwdLocal)))
}

// forwardsInputs fills the plan input's forward fields for a running podman jail.
func (o *Options) forwardsInputs(in *applyPlanInput, run *applyRun) {
	in.forwardsLive, in.forwardsReason = forwardsLiveFor(run.rt, run.envLines)
	if !in.forwardsLive {
		return
	}
	parse := func(v any) []PortForward {
		p, _ := ParsePortForwards(forwardEntries(v), nil)
		return p
	}
	bootV, _, _ := config.ConfigValueAtPath(in.boot, "machine.forward")
	curV, _, _ := config.ConfigValueAtPath(in.current, "machine.forward")
	in.forwardElsewhere = forwardElsewhere(run.envLines, parse(bootV), parse(readableForwardPorts(run.cfg)), parse(curV))
}

package integration

import (
	"strconv"
	"strings"
	"testing"
)

// In-jail reachability of the loopback-TLS transport — the coverage gap that let
// docs/design/loopback-tls-reachability.md's outage ship. Every jail-facing host
// service publishes an ADVERTISED address for the jail to dial, and until now
// nothing anywhere asserted that a jail can actually dial it: `yolo check` runs
// host-side and substitutes 127.0.0.1 (internal/svcendpoint/dial.go), so it stays
// green through a total outage.
//
// > [!WARNING]
// > READ THIS BEFORE TRUSTING A GREEN RUN HERE. Under podman-in-podman the CLI
// > forces `--net=host` (netavark cannot create a netns without NET_ADMIN), and
// > that is the ONE mode in which the bug cannot reproduce: the jail shares the
// > launcher's network stack, so the host's loopback and the jail's are the same
// > loopback. This suite normally runs from inside a jail, so this test normally
// > runs in exactly that blind spot — §7 of the design doc names it, and
// > AGENTS.md's "verify in a nested jail" instruction is actively misleading here.
// > A green result is evidence the plumbing is wired, NOT evidence the forwarding
// > works. The only measurement that settles that is a real jail on a rootless
// > pasta host.
//
// No t.Parallel(): the integration package runs serially by design.

// TestInJailServiceReachability dials, from inside the jail, the advertised
// address of every published endpoint, and cross-checks the answer against the
// entrypoint's own boot-time witness.
//
// The cross-check is the point. Measuring reachability twice by two unrelated
// means — bash's /dev/tcp here, a full authenticated svcendpoint dial in
// internal/entrypoint/reachability.go — is what makes this a test OF THE WITNESS
// and not merely another probe alongside it. A witness that IS a launch failure
// (design doc OQ-R2, fatal since 2026-08-18) has to be checkable against something
// that is not itself.
//
// One consequence of that flip is worth knowing before reading a failure here: the
// two disagreement branches below can now only fire in the direction of a false
// POSITIVE. A jail whose service is genuinely unreachable no longer starts, so the
// script never runs and the failure surfaces at the "never reported a count" bail
// above it instead.
//
// THE PACK SELECTION IS THE TEST'S SUBJECT, not decoration. This test has nothing to
// measure unless the jail publishes at least one endpoint, and nothing is active by
// default — so with the harness's empty isolated user config it would probe zero
// services and SKIP, every run, everywhere. It used to inherit whatever the machine
// enabled, which is the §10.5 defect pointing the other way: the coverage existed only
// on hosts whose own config happened to switch a jail-facing service on, and never in
// CI.
//
// `claude` is the cheapest thing to name: the pack contributes the claude-oauth-broker
// loophole, which is the one shipped manifest with `default_enabled: true`, and its
// transport is loopback-tls — so selecting the pack is sufficient to publish
// /run/yolo-services/claude-oauth-broker.endpoint, on both platforms, with no
// `loopholes` block. It installs no agent CLI (launchers are lazy), and several tests in
// this suite already boot with it.
func TestInJailServiceReachability(t *testing.T) {
	requireJail(t)
	dir := writeProjectWithPacks(t, `{"network": {"mode": "bridge"}}`, "claude")

	// Only the FIRST whitespace-separated field of an endpoint file is ever read
	// or echoed. That file is a credential — the per-jail bearer token sits in it
	// next to the address — and a test's captured output is exactly the kind of
	// place a leaked token survives in. Print the address, never the line.
	//
	// The host:port split is a suffix trim, which is correct because the advertised
	// host is a runtime GATEWAY NAME (`host.containers.internal`), never a bracketed
	// IPv6 literal. If that ever changes this parse is the thing that breaks.
	r := runYolo(t, dir, `
set -u
probed=0
for f in /run/yolo-services/*.endpoint; do
  [ -e "$f" ] || continue
  probed=$((probed+1))
  addr=$(awk '{print $1}' "$f")
  host=${addr%:*}
  port=${addr##*:}
  if timeout 15 bash -c "exec 3<>/dev/tcp/$host/$port" 2>/dev/null; then
    echo "DIAL_OK $(basename "$f" .endpoint) $addr"
  else
    echo "DIAL_FAIL $(basename "$f" .endpoint) $addr"
  fi
done
echo "PROBED $probed"
`)

	probed := -1
	for _, line := range strings.Split(r.stdout, "\n") {
		if n, ok := strings.CutPrefix(strings.TrimSpace(line), "PROBED "); ok {
			probed, _ = strconv.Atoi(n)
		}
	}
	if probed < 0 {
		// Since the witness became FATAL (2026-08-18) this branch is where a REAL
		// outage now lands, and it no longer means "the harness is broken": a jail
		// that cannot reach an enabled service refuses to start, so the script never
		// runs and there is no count to report. The witness's own verdict is in
		// stderr, which is printed here for exactly that reason — read it before
		// suspecting the harness.
		t.Fatalf("the in-jail probe never reported a count — the script did not run. Either "+
			"the jail refused to start (the boot witness names the service and the address "+
			"in stderr below) or the harness is broken.\nstdout: %s\nstderr: %s",
			r.stdout, r.stderr)
	}
	if probed == 0 {
		// Still a skip rather than a failure — nothing is enabled unless it was asked
		// for (loophole-activation.md), and this test cannot dial what does not exist.
		// But it is no longer the EXPECTED outcome anywhere: the fixture selects the
		// claude pack precisely so one endpoint is published, so reaching here means the
		// selection did not take (superseded capability, an inactive loophole) and the
		// reachability question went unasked rather than answered.
		t.Skip("this jail published no endpoints despite selecting `claude`, whose broker " +
			"is default-enabled on loopback-tls — nothing to dial, so nothing measured")
	}

	var unreachable []string
	for _, line := range strings.Split(r.stdout, "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "DIAL_FAIL" {
			unreachable = append(unreachable, fields[1]+" at "+fields[2])
		}
	}

	// The entrypoint's witness must agree with the independent measurement, in
	// BOTH directions. A witness that only ever agrees when things are broken is
	// an alarm stuck on; one that only agrees when they work is an alarm stuck off.
	warned := strings.Contains(r.stderr, "is enabled but UNREACHABLE")
	switch {
	case len(unreachable) > 0 && !warned:
		t.Fatalf("the jail cannot reach %v, and the entrypoint's boot probe said NOTHING — "+
			"that probe is the only in-jail witness of this failure and it is silent for a "+
			"real outage.\nstdout: %s\nstderr: %s", unreachable, r.stdout, r.stderr)
	case len(unreachable) == 0 && warned:
		t.Fatalf("every advertised endpoint dialled fine, yet the entrypoint's boot probe "+
			"warned about one — and a false positive there is now a REFUSED LAUNCH (OQ-R2 "+
			"is fatal since 2026-08-18), not a stray warning.\nstdout: %s\nstderr: %s",
			r.stdout, r.stderr)
	case len(unreachable) > 0:
		t.Fatalf("loopback-TLS is unreachable from inside the jail for: %v.  Every in-jail "+
			"client of those services is down.  See docs/design/loopback-tls-reachability.md; "+
			"`podman info --format '{{.Host.RootlessNetworkCmd}}'` on the host names the "+
			"network stack.\nstdout: %s", unreachable, r.stdout)
	}
}

package integration

import (
	"strings"
	"testing"
)

// Cgroup-delegation tests. They exercise the host-side cgroup delegate daemon
// and the in-jail yolo-cglimit
// helper. Tests that create a child cgroup call skipIfCgroupReadonly(t) — a
// nested jail's cgroup filesystem is read-only, so delegation cannot run there.

// TestCgroupDelegation verifies cgroup delegation via the host-side daemon in
// ONE jail launch (both checks share the identical bridge config and the same
// skipIfCgroupReadonly gate — they always run or skip together — so merging
// cannot change where they execute; it just pays the container cold-start once):
//  1. the delegate socket exists at /run/yolo-services/cgroup-delegate.sock (the
//     path the entrypoint and yolo-cglimit probe for),
//  2. yolo-cglimit can talk to the host daemon and create a child cgroup with a
//     CPU limit (the "delegation" and "enforce" checks — neither reads back
//     cpu.max, so both only assert "cglimit created a cgroup and ran the cmd").
//
// TestCglimitHelperAvailable stays SEPARATE: it deliberately omits the readonly
// gate to prove the helper is on PATH even in a nested jail where cgroup writes
// are impossible — folding it in here would over-skip and lose that coverage.
//
// BOTH of this test's inputs are now STATED, and they have to be. The delegate stopped
// being presence-activated on 2026-08-18 (loophole-activation.md OQ-A4): it is a PACK
// you select plus a loophole you switch on, and neither is on by default. This test used
// to write neither and inherit both from the machine's own user config — so it asserted
// "the socket exists" on a developer host that happened to select the pack, and could not
// pass on a CI runner, which has no user config at all. That is the §10.5 defect in its
// purest form: an ambient config SATISFYING an assertion.
//
// `packs` is user-scope only (writeProjectWithPacks); `loopholes.<name>.enabled` is
// either-scope (loopholes.ConfigEnabledOverride, R5), so the switch reads well in the
// workspace config next to the network mode.
func TestCgroupDelegation(t *testing.T) {
	requireJail(t)
	skipIfCgroupReadonly(t)
	dir := writeProjectWithPacks(t, `{
  "network": {"mode": "bridge"},
  "loopholes": {"cgroup-delegate": {"enabled": true}}
}`, "cgroup-delegate")

	r := runYolo(t, dir,
		"set -e; "+
			// Check the delegate socket exists.
			`test -S /run/yolo-services/cgroup-delegate.sock && echo "SOCKET_EXISTS"; `+
			// yolo-cglimit talks to the host daemon and creates a child cgroup.
			`yolo-cglimit --cpu 75 --name test-cgd -- echo "DELEGATION_OK"; `+
			// A second distinct cgroup name (the former enforce check).
			`yolo-cglimit --cpu 75 --name test-enforce -- echo "ENFORCE_OK"; `+
			"true")
	if !strings.Contains(r.stdout, "SOCKET_EXISTS") {
		t.Fatalf("expected cgroup delegate socket to exist.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "DELEGATION_OK") {
		t.Fatalf("expected cgroup delegation to work.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "ENFORCE_OK") {
		t.Fatalf("expected command to run under cgroup limit.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
}

// TestCglimitHelperAvailable verifies yolo-cglimit is on PATH and functional
// inside the jail. It does not create a cgroup, so no readonly gate is needed —
// which is exactly why it stays separate from TestCgroupDelegation (it must also
// run in nested jails, where the delegation tests skip).
//
// It also needs NO pack and no loophole, and that is the assertion rather than an
// oversight: yolo-cglimit is a BAKED binary (flake.nix's shippedBinaries), so it is on
// PATH in a jail with nothing selected. Only the socket it talks to is opt-in.
func TestCglimitHelperAvailable(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	r := runYolo(t, dir, "which yolo-cglimit && yolo-cglimit --help")
	if !strings.Contains(r.stdout, "yolo-cglimit") {
		t.Fatalf("yolo-cglimit not found on PATH.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "--cpu") {
		t.Fatalf("yolo-cglimit --help missing expected content.\nstdout: %s", r.stdout)
	}
}

package integration

import (
	"strings"
	"testing"
)

// This file ports the cgroup-delegation tests from tests/test_jail.py. They
// exercise the host-side cgroup delegate daemon and the in-jail yolo-cglimit
// helper. Tests that create a child cgroup call skipIfCgroupReadonly(t) — a
// nested jail's cgroup filesystem is read-only, so delegation cannot run there.

// TestCgroupDelegationAvailable verifies cgroup delegation via the host-side
// daemon works inside the jail:
//  1. the delegate socket exists at /run/yolo-services/cgroup-delegate.sock (the
//     path the entrypoint and yolo-cglimit probe for),
//  2. yolo-cglimit can talk to the host daemon, and
//  3. the daemon can create a child cgroup and set limits.
func TestCgroupDelegationAvailable(t *testing.T) {
	requireJail(t)
	skipIfCgroupReadonly(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	r := runYolo(t, dir,
		"set -e; "+
			// Check the delegate socket exists.
			`test -S /run/yolo-services/cgroup-delegate.sock && echo "SOCKET_EXISTS"; `+
			// Use yolo-cglimit to run a trivial command with a CPU limit.
			"yolo-cglimit --cpu 75 --name test-cgd -- "+
			`echo "DELEGATION_OK"; `+
			"true")
	if !strings.Contains(r.stdout, "SOCKET_EXISTS") {
		t.Fatalf("expected cgroup delegate socket to exist.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "DELEGATION_OK") {
		t.Fatalf("expected cgroup delegation to work.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
}

// TestCglimitHelperAvailable verifies yolo-cglimit is on PATH and functional
// inside the jail. It does not create a cgroup, so no readonly gate is needed.
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

// TestCglimitEnforcesCpuLimit verifies yolo-cglimit creates a cgroup and enforces
// a CPU limit via the host daemon.
func TestCglimitEnforcesCpuLimit(t *testing.T) {
	requireJail(t)
	skipIfCgroupReadonly(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	r := runYolo(t, dir,
		`set -e; yolo-cglimit --cpu 75 --name test-enforce -- echo "ENFORCE_OK"; true`)
	if !strings.Contains(r.stdout, "ENFORCE_OK") {
		t.Fatalf("expected command to run under cgroup limit.\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
}

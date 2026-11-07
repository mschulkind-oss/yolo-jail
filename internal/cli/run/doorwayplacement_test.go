package run

// doorwayplacement_test.go pins the placement refusal of a doorway's host service on the backends
// with a jail (doorwayplacement.go; docs/design/host-notch-services.md HS-D34), as
// hostdoorwaysplacement_test.go pins it at `yolo host`: when the placement rule refuses the host
// service a doorway forwards to, the launch refuses before anything starts, where it used to open
// the doorway (outside the macos-user sandbox, or inside a container as its jail daemon) pointed at
// a service that never started, answering every request ServiceUnreachable.
//
// DRIVEN THROUGH Run(), so deleting either arm's refusePlacementOfJailDoorways call fails a test
// here: with it gone, the macos-user launch opens the doorway and runs the sandbox, and the podman
// launch spawns its keeper.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// writePlacedDoorwayPack puts a doorway in the conventional local pack whose loophole's host daemon
// names a program inside ws, the workspace a jail hands its agent read-write: the placement rule
// refuses that service on every backend with a jail. It returns the program's path.
func writePlacedDoorwayPack(t *testing.T, home, ws string) string {
	t.Helper()
	program := filepath.Join(ws, "acme-daemon")
	// It exits non-zero at once: a crash ends the readiness wait when the process exits, where a
	// clean exit would be taken for a daemonizing wrapper and waited on for the whole window.
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalLoopholePack(t, home, "acme-proxy", `{"name": "acme-proxy",
		"description": "acme proxy", "transport": "loopback-tls",
		"host_daemon": {"publishes": "socket", "cmd": ["`+program+`", "--socket", "{socket}"]},
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		"listen": "127.0.0.1:1999", "caller_token": true,
		"host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}}`)
	writeUserConfigJSON(t, home, `{"packs": []}`)
	return program
}

// assertPlacementRefusal fails unless out names the loophole's placement refusal and the launch's
// refusal of its doorway, with the step that fixes it, after lead.
func assertPlacementRefusal(t *testing.T, out, lead, program string) {
	t.Helper()
	for _, want := range []string{
		"Refusing to start loophole acme-proxy",
		program,
		lead + `the "acme-proxy" doorway (pack "local") forwards to loophole "acme-proxy"'s host service, ` +
			"which the placement rule refuses to start",
		"so the doorway is not opened",
		"Move the pack's files out of that tree, then run the launch again",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch must say %q:\n%s", want, out)
		}
	}
}

// AT macos-user THE DOORWAY IS NOT OPENED BEHIND ITS SERVICE'S PLACEMENT REFUSAL, and no keeper is
// spawned to hold it: the refusal comes before the arm's keeper/keeperless split, so it is the same
// answer for every session of the workspace. The sandbox never runs.
func TestMacosUserRefusesADoorwayWhoseServiceThePlacementRuleRefuses(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	program := writePlacedDoorwayPack(t, home, ws)
	doors := observeDoorways(t)
	spawns := countKeeperSpawns(t)

	got := macosUserLaunch(t, ws)
	if got.rc != 1 || got.env != nil {
		t.Fatalf("Run() = %d, sandbox reached = %v: a doorway whose service the placement rule refuses "+
			"must refuse the launch before the command\n%s", got.rc, got.env != nil, got.out)
	}
	if len(doors.plans) != 0 {
		t.Errorf("the doorway opened behind its service's placement refusal: %v", doors.plans[0].Cmd)
	}
	if n := spawns.Load(); n != 0 {
		t.Errorf("the launch spawned %d keepers for a doorway it refuses", n)
	}
	assertPlacementRefusal(t, got.out, "Refusing the macos-user launch: ", program)
}

// A macos-user DRY RUN IS REFUSED TOO, as the launch it describes would be, rather than printing a
// plan that opens the doorway.
func TestMacosUserDryRunRefusesADoorwayWhoseServiceThePlacementRuleRefuses(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	program := writePlacedDoorwayPack(t, home, ws)
	got := macosUserLaunchWithOptions(t, ws, nil, func(o *Options) { o.DryRun = true })
	if got.rc != 1 {
		t.Fatalf("Run() = %d, want 1: the dry run planned a doorway the launch would refuse\n%s", got.rc, got.out)
	}
	if strings.Contains(got.out, `Would open the "acme-proxy" doorway`) {
		t.Errorf("the dry run says it would open the refused doorway:\n%s", got.out)
	}
	assertPlacementRefusal(t, got.out, "Refusing the macos-user launch: ", program)
}

// THE SAME DOORWAY OUTSIDE THE WORKSPACE OPENS: the refusal is the placement rule's, not every
// doorway's. Its host service is a program that crashes at once and publishes nothing, so this is
// also HS-D19's case, a service that fails at spawn for another reason, whose doorway still opens.
func TestMacosUserOpensADoorwayWhoseServiceThePlacementRuleAdmits(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writePlacedDoorwayPack(t, home, t.TempDir())
	doors := observeDoorways(t)

	got := macosUserLaunch(t, ws)
	if strings.Contains(got.out, "placement rule") {
		t.Errorf("a doorway outside the workspace was refused by the placement rule:\n%s", got.out)
	}
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	if !strings.Contains(got.out, "host service 'acme-proxy' exited at startup") {
		t.Fatalf("the host service did not fail at its spawn, so this is not HS-D19's case:\n%s", got.out)
	}
	if plan, _ := doors.only(t, "acme-proxy"); plan.Pack != "local" {
		t.Errorf("the doorway's plan names pack %q, want local", plan.Pack)
	}
}

// IN A CONTAINER THE JAIL'S ADAPTER IS NOT STARTED BEHIND ITS SERVICE'S PLACEMENT REFUSAL: a podman
// launch refuses before its keeper's spawn, which would start the host services and the jail, whose
// supervisor would run the doorway with no endpoint to forward to.
func TestContainerRefusesADoorwayWhoseServiceThePlacementRuleRefuses(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	program := writePlacedDoorwayPack(t, home, ws)
	// A SAFETY NET, as hostservicesleak_test.go's: nothing named podman is reachable, so a launch
	// that stopped refusing here could not start a real container.
	t.Setenv("PATH", t.TempDir())
	spawns := countKeeperSpawns(t)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0} // no container of this name
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	rc := Run(*o)
	out := stdout.String() + stderr.String()
	if rc != 1 {
		t.Fatalf("Run() = %d, want 1\n%s", rc, out)
	}
	if n := spawns.Load(); n != 0 {
		t.Errorf("the launch spawned %d keepers for a doorway it refuses", n)
	}
	assertPlacementRefusal(t, out, "Refusing to launch: ", program)
}

package run

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// A keeper whose stop left a jail behind retains its start record for the
// next arrival to find, regardless of whether the runtime can describe it.
func TestSelectedKeeperRetainsStartRecordOfUnkeptJail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unknown bool
	}{
		{name: "still running"},
		{name: "runtime unknown", unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := keeperGoneAttempts
			keeperGoneAttempts = 1
			t.Cleanup(func() { keeperGoneAttempts = saved })
			id := strings.Repeat("a", 32)
			var session *sessionLock
			f := startKeeperFixture(t, true, func(p *keeperPlan) {
				p.MCPTeardownID = id
				if err := createMCPOutcomeGeneration(p.Cname, id, p.Workspace, p.Runtime); err != nil {
					t.Fatal(err)
				}
				var err error
				session, _, err = takeSessionLock(p.Cname)
				if err != nil {
					t.Fatal(err)
				}
			})
			if !f.relay() {
				t.Fatalf("selected keeper did not reach ready: %s", f.errOut.String())
			}
			f.jail.mu.Lock()
			f.jail.stuck, f.jail.runningUnknown = true, tc.unknown
			f.jail.mu.Unlock()
			session.release()
			if rc := f.wait(); rc != 0 {
				t.Fatalf("keeper rc=%d, expected its original process status", rc)
			}
			if _, ok := readKeeperRecord(f.cname); !ok {
				t.Fatal("the unkept jail lost its start record")
			}
		})
	}
}

// A selected service's real final stop must not certify an unkept jail, even
// though the harvest itself completed cleanly and every other success gate is met.
func TestSelectedKeeperCannotConfirmFinalHarvestOfUnkeptJail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unknown bool
	}{
		{name: "still running"},
		{name: "runtime unknown", unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := keeperGoneAttempts
			keeperGoneAttempts = 1
			t.Cleanup(func() { keeperGoneAttempts = saved })
			id := strings.Repeat("a", 32)
			k, jail := readySelectedMCPKeeper(t, id)
			jail.mu.Lock()
			jail.stuck, jail.runningUnknown = true, tc.unknown
			jail.mu.Unlock()
			k.endJail(keeperSignalledReason(k.pid), true)
			if rc := k.finish(128+int(syscall.SIGTERM), nil); rc != 128+int(syscall.SIGTERM) {
				t.Errorf("keeper rc=%d, want unchanged signal exit", rc)
			}
			if !k.jailLeft || !k.teardownComplete || !k.selectedMCPStopped || k.harvestFailed || jail.stopCount() != 1 {
				t.Fatalf("absence must be the only unsatisfied success gate: jailLeft=%v teardown=%v stopped=%v failed=%v jail stops=%d",
					k.jailLeft, k.teardownComplete, k.selectedMCPStopped, k.harvestFailed, jail.stopCount())
			}
			if got := readMCPTeardownOutcome(k.plan.Cname, id); got != "" {
				t.Errorf("selected outcome = %q, want unconfirmed", got)
			}
			o := NewDefaultOptions()
			o.mcpTeardownID = id
			var out bytes.Buffer
			o.Stderr = &out
			if !o.streamKeeperTeardown(k.plan.Cname, 0) || !strings.Contains(out.String(), "not confirmed") {
				t.Fatalf("the actual final-harvest reader accepted an unkept jail: %s", out.String())
			}
		})
	}
}

func TestSelectedKeeperWithoutStartedServiceCannotConfirmFinalHarvest(t *testing.T) {
	id := strings.Repeat("d", 32)
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		p.MCPTeardownID = id
		if err := createMCPOutcomeGeneration(p.Cname, id, p.Workspace, p.Runtime); err != nil {
			t.Fatal(err)
		}
		var err error
		session, _, err = takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
	})
	if !f.relay() {
		t.Fatalf("selected keeper did not reach ready: %s", f.errOut.String())
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("keeper rc=%d, want unchanged process status", rc)
	}
	if got := readMCPTeardownOutcome(f.cname, id); got != "" {
		t.Errorf("empty service teardown certified final harvest: %q", got)
	}
	o := NewDefaultOptions()
	o.mcpTeardownID = id
	var out bytes.Buffer
	o.Stderr = &out
	if !o.streamKeeperTeardown(f.cname, 0) || !strings.Contains(out.String(), "not confirmed") {
		t.Fatalf("actual selected reader accepted a missing service: %s", out.String())
	}
}

func TestSelectedKeeperForcedStopWithoutServiceCannotConfirmFinalHarvest(t *testing.T) {
	id := strings.Repeat("b", 32)
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		p.MCPTeardownID = id
		if err := createMCPOutcomeGeneration(p.Cname, id, p.Workspace, p.Runtime); err != nil {
			t.Fatal(err)
		}
	})
	if !f.relay() {
		t.Fatalf("selected keeper did not reach ready: %s", f.errOut.String())
	}
	f.signals <- syscall.SIGTERM
	if rc := f.wait(); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("keeper rc=%d, want signal exit", rc)
	}
	if got := readMCPTeardownOutcome(f.cname, id); got != "" {
		t.Errorf("forced-stop empty service teardown certified final harvest: %q", got)
	}
	o := NewDefaultOptions()
	o.mcpTeardownID = id
	var out bytes.Buffer
	o.Stderr = &out
	if !o.streamKeeperTeardown(f.cname, 0) || !strings.Contains(out.String(), "not confirmed") {
		t.Fatalf("actual selected reader accepted a forced stop with no service: %s", out.String())
	}
	if f.jail.stopCount() != 1 {
		t.Fatal("forced-stop caller did not stop the jail")
	}
}

func readySelectedMCPKeeper(t *testing.T, id string) (*keeper, *fakeJail) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	// AF_UNIX's path limit is shorter than t.TempDir's long test-name path.
	sockets, err := os.MkdirTemp("", "mcp-final-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	cname := "yolo-mcp-observed-service-harvest"
	p := &keeperPlan{Cname: cname, Runtime: "podman", Workspace: t.TempDir(), MCPTeardownID: id,
		SocketsDir: sockets}
	if err := createMCPOutcomeGeneration(cname, id, p.Workspace, p.Runtime); err != nil {
		t.Fatal(err)
	}
	jail := newFakeJail(t, cname)
	k := newKeeper(p, KeeperSeams{}, nil, nil, nil, nil)
	k.o.Exec = jail.exec
	var startup lockedBuffer
	k.o.Stderr = &startup
	k.pid = os.Getpid()
	// The host-service startup's actual ready handle, not a manufactured
	// start flag or an outcome ID, enters the keeper's teardown chain. This
	// synthetic process binds a Unix socket and exits cleanly on SIGTERM.
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"/bin/python3", "-c", `import signal, socket, sys, time
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
s = socket.socket(socket.AF_UNIX)
s.bind(sys.argv[1])
s.listen(1)
while True: time.sleep(.05)
`, "{socket}"})
	oldBudget := mcpAuthReadinessBudget
	mcpAuthReadinessBudget = 3 * time.Second
	t.Cleanup(func() { mcpAuthReadinessBudget = oldBudget })
	h, ok := k.o.startExternalService(selectedMCPHostService, spec, p.SocketsDir, "", "",
		&loopholes.HostDaemon{Detach: true, LaunchInput: "mcp-auth-share"})
	if !ok || h.stop == nil || h.finalFailure == nil {
		t.Fatalf("synthetic selected MCP service did not start with observable final harvest: %s", startup.String())
	}
	t.Cleanup(func() {
		if !k.teardownComplete {
			h.stop()
		}
	})
	k.handles = []loopholeDaemon{h}
	k.jm = &jailMain{exited: make(chan struct{})}
	close(k.jm.exited) // the fake runtime's client has already exited
	return k, jail
}

func TestSelectedKeeperForcedStopWithObservedServiceHarvestSucceeds(t *testing.T) {
	id := strings.Repeat("e", 32)
	k, jail := readySelectedMCPKeeper(t, id)
	k.endJail(keeperSignalledReason(k.pid), true)
	if rc := k.finish(128+int(syscall.SIGTERM), nil); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("keeper rc=%d, want unchanged signal exit", rc)
	}
	if k.harvestFailed || !k.selectedMCPStopped || jail.stopCount() != 1 {
		t.Fatalf("selected service final stop did not complete: failed=%v stopped=%v jail stops=%d",
			k.harvestFailed, k.selectedMCPStopped, jail.stopCount())
	}
	if got := readMCPTeardownOutcome(k.plan.Cname, id); got != "ok" {
		t.Errorf("final harvest = %q, want ok after observed service stop and absent jail", got)
	}
	o := NewDefaultOptions()
	o.mcpTeardownID = id
	var out bytes.Buffer
	o.Stderr = &out
	if o.streamKeeperTeardown(k.plan.Cname, 0) {
		t.Fatalf("reader refused completed selected harvest: %s", out.String())
	}
}

func TestSelectedKeeperFailedServiceTeardownOverridesSignalStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id := strings.Repeat("c", 32)
	cname := "yolo-mcp-failed-service-harvest"
	p := &keeperPlan{Cname: cname, Runtime: "podman", Workspace: t.TempDir(), MCPTeardownID: id}
	if err := createMCPOutcomeGeneration(cname, id, p.Workspace, p.Runtime); err != nil {
		t.Fatal(err)
	}
	jail := newFakeJail(t, cname)
	k := newKeeper(p, KeeperSeams{}, nil, nil, nil, nil)
	k.o.Exec = jail.exec
	k.pid = os.Getpid()
	stopped := false
	k.handles = []loopholeDaemon{{name: "synthetic-mcp", stop: func() { stopped = true },
		finalFailure: func() error { return errors.New("synthetic harvest failure") }}}
	k.endJail(keeperSignalledReason(k.pid), true)
	if rc := k.finish(128+int(syscall.SIGTERM), nil); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("keeper rc=%d, want signal exit", rc)
	}
	if !stopped || !k.harvestFailed {
		t.Fatal("endJail did not observe the failed service teardown")
	}
	if got := readMCPTeardownOutcome(cname, id); got != "failed" {
		t.Errorf("failed service harvest outcome = %q, want failed", got)
	}
}

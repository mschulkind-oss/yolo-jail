package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// A restrictive umask makes the launch lock 0600 or 0640, not 0644. The lifecycle gate must
// still be granted: it demands an owned regular file nobody else can write, not one exact mode.
func TestManagedMCPCallbackLifecycleGateAcceptsLockCreatedUnderRestrictiveUmask(t *testing.T) {
	packHome(t)
	for _, umask := range []int{0o077, 0o027} {
		old := syscall.Umask(umask)
		func() {
			defer syscall.Umask(old)
			root := filepath.Join(t.TempDir(), "leases")
			o := callbackLeaseOptions(t, root, true, "")
			prev := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "umask-previous", Runtime: "podman", Generation: "old"}
			if disposition, _ := o.acquireCallbackLease(prev); disposition != "publication pending" {
				t.Fatalf("umask %o: first lease: %s", umask, disposition)
			}
			next := prev
			next.Launch, next.Generation = "umask-next", "new"
			if disposition, _ := o.acquireCallbackLease(next); disposition != "publication pending" {
				t.Fatalf("umask %o: other owner's gate refused a private lock: %s", umask, disposition)
			}
			o.holdLaunchLock(next.Launch)
			defer o.releaseLaunchLock()
			same := next
			same.Generation = "same-cname-new"
			if disposition, _ := o.acquireCallbackLease(same); disposition != "publication pending" {
				t.Fatalf("umask %o: own held lock refused: %s", umask, disposition)
			}
		}()
	}
}

func TestManagedMCPCallbackAcquireReclamationHoldsPreviousLifecycleAndOwnCname(t *testing.T) {
	packHome(t)
	root := filepath.Join(t.TempDir(), "leases")
	o := callbackLeaseOptions(t, root, true, "")
	old := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "previous", Runtime: "podman", Generation: "old"}
	if disposition, _ := o.acquireCallbackLease(old); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	probed := false
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		probed = true
		if lock, acquired := tryWorkspaceLock(old.Launch); acquired {
			lock.Close()
			t.Error("absence proof did not hold previous lifecycle exclusion")
		}
		return ExecResult{Ran: true}
	}
	next := old
	next.Launch, next.Generation = "next", "new"
	if disposition, _ := o.acquireCallbackLease(next); disposition != "publication pending" || !probed {
		t.Fatal("known-gone reclamation failed", disposition, probed)
	}
	o.holdLaunchLock(next.Launch)
	defer o.releaseLaunchLock()
	old = next
	next.Generation = "same-cname-new"
	if disposition, _ := o.acquireCallbackLease(next); disposition != "publication pending" {
		t.Fatal("already-owned same-cname gate was reacquired or guessed", disposition)
	}
}

func TestManagedMCPCallbackAcquireUnsafePreviousLifecycleKeepsLease(t *testing.T) {
	packHome(t)
	for _, kind := range []string{"symlink", "writable"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "leases")
			o := callbackLeaseOptions(t, root, true, "")
			old := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "unsafe-" + kind, Runtime: "podman", Generation: "old"}
			if disposition, _ := o.acquireCallbackLease(old); disposition != "publication pending" {
				t.Fatal(disposition)
			}
			lockPath := launchLockPath(old.Launch)
			if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("untouched"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, lockPath); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(lockPath, nil, 0666); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(lockPath, 0666); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(mcpsignin.CallbackLeasePath(root, old.Port))
			o.Exec = func([]string, string, []string, time.Duration) ExecResult {
				t.Error("unsafe lifecycle probed runtime")
				return ExecResult{}
			}
			next := old
			next.Launch, next.Generation = "new", "new"
			if disposition, _ := o.acquireCallbackLease(next); disposition != "held" {
				t.Fatal("unsafe previous gate granted publication", disposition)
			}
			after, err := os.ReadFile(mcpsignin.CallbackLeasePath(root, old.Port))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("unsafe previous owner was replaced", err)
			}
		})
	}
}

func TestManagedMCPCallbackPrepareRejectsWrongAlreadyHeldLifecycleDescriptor(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	f.tune = func(_ *callbackRun, o *Options) { o.holdLaunchLock("not-this-launch") }
	run := f.Start("wrong-gate", false, false, false)
	if run.rc != 0 || len(run.plan.CallbackLeases) != 0 || run.plan.LaunchInputMCP.SignIn.Callbacks[0].Disposition != "unavailable" {
		t.Fatal("another cname's held descriptor authorized callback publication")
	}
}

// The keeper-spawn event is after actual Run/prepare registered A's lease and
// before its inert runtime can create anything. B runs in that exact window.
func TestManagedMCPCallbackAcquireCannotStealPendingLaunchBeforeRuntimeCreate(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	entered := false
	f.beforeSpawn = func(first *callbackRun) {
		if entered {
			return
		}
		entered = true
		if len(first.plan.CallbackLeases) != 1 {
			t.Fatal("first launch did not register its callback")
		}
		lease := first.plan.CallbackLeases[0]
		if lock, acquired := tryWorkspaceLock(lease.Launch); acquired {
			lock.Close()
			t.Fatal("first launch did not hold its lifecycle gate")
		}
		if _, err := os.Stat(first.root + "/created"); !os.IsNotExist(err) {
			t.Fatal("first inert runtime was already created", err)
		}
		id, known := first.options.probeExistingContainer(lease.Launch, lease.Runtime, trackingProbeTimeout)
		if !known || id != "" {
			t.Fatal("fixture did not prove runtime absent", known, id)
		}
		path := mcpsignin.CallbackLeasePath(first.options.callbackRoot(), lease.Port)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		second := f.Start("competing-pending", false, false, false)
		after, err := os.ReadFile(path)
		if second.rc != 0 || second.plan == nil || len(second.plan.CallbackLeases) != 0 || err != nil || !bytes.Equal(before, after) {
			t.Fatalf("pending runtime-absent launch lost its lease: second rc=%d leases=%v unchanged=%v err=%v", second.rc, second.plan.CallbackLeases, bytes.Equal(before, after), err)
		}
		rows := second.plan.LaunchInputMCP.SignIn.Callbacks
		if len(rows) != 1 || rows[0].Disposition != "held" || rows[0].Owner != lease.Launch {
			t.Fatal("contending launch did not report the existing owner", rows)
		}
		for _, arg := range second.plan.RunCmd {
			if arg == "127.0.0.1:3118:3118/tcp" {
				t.Fatal("contending launch received a publication")
			}
		}
	}
	first := f.Start("pending-owner", false, false, false)
	if !entered || first.rc != 0 {
		t.Fatal("pending owner did not continue after controlled window", entered, first.rc)
	}
}

// An unavailable callback row says why, so the user can fix it or paste the URL.
func TestManagedMCPCallbackUnavailableRowCarriesAReason(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	f.tune = func(_ *callbackRun, o *Options) { o.holdLaunchLock("not-this-launch") }
	run := f.Start("wrong-gate-reason", false, false, false)
	if !strings.Contains(run.errs.String(), "launch lock") {
		t.Fatalf("unavailable callback row has no reason: %s", run.errs.String())
	}
}

// Callback publications are disjoint from forward_host_ports: a forward already binds
// that jail-side port, so the callback is skipped with a line naming the next step.
func TestManagedMCPCallbackSkipsAPortThatForwardHostPortsUses(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	path := filepath.Join(f.home, ".config/yolo-jail/config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config = append(config, []byte("\n[[machine.forward]]\njail=3118\nhost=3118\n")...)
	if err := os.WriteFile(path, config, 0644); err != nil {
		t.Fatal(err)
	}
	run := f.Start("forward-overlap", false, false, false)
	out := run.errs.String()
	if run.rc != 0 || len(run.plan.CallbackLeases) != 0 || !strings.Contains(out, "forward") || !strings.Contains(out, "full callback URL") {
		t.Fatalf("callback port overlapping a forward was published or unexplained: rc=%d leases=%v %s", run.rc, run.plan.CallbackLeases, out)
	}
}

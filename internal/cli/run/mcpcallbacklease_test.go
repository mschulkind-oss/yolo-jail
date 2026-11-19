package run

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func callbackLeaseOptions(t *testing.T, root string, known bool, id string) *Options {
	t.Helper()
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.mcpCallbackRoot = root
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		if !known {
			return ExecResult{Ran: true, RC: 125}
		}
		return ExecResult{Ran: true, Stdout: id}
	}
	return o
}

func callbackHeldLock(t *testing.T, root string) *os.File {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(root, "port-3118.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestManagedMCPCallbackLeaseWaitReleaseAndCancellationEvents(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		root := filepath.Join(t.TempDir(), "private")
		lock := callbackHeldLock(t, root)
		o := callbackLeaseOptions(t, root, true, "")
		ctx, end := context.WithCancel(context.Background())
		defer end()
		o.mcpCallbackLockContext = ctx
		waits := 0
		o.mcpCallbackLockWait = func(context.Context, time.Duration) error {
			waits++
			if cancel {
				end()
				return context.Canceled
			}
			return syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		}
		lease := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "new", Runtime: "podman", Generation: "new-generation"}
		disposition, _ := o.acquireCallbackLease(lease)
		if waits != 1 {
			t.Fatal("no controlled lock wait event", waits)
		}
		if cancel {
			if disposition != "unavailable" {
				t.Fatal("canceled operation claimed ownership", disposition)
			}
			if _, err := os.Stat(mcpsignin.CallbackLeasePath(root, 3118)); !os.IsNotExist(err) {
				t.Fatal("cancellation mutated ownership")
			}
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			o.mcpCallbackLockContext = context.Background()
			if disposition, _ := o.acquireCallbackLease(lease); disposition != "publication pending" {
				t.Fatal("canceled descriptor retained lock", disposition)
			}
		} else if disposition != "publication pending" {
			t.Fatal("release event did not serialize", disposition)
		}
	}
}

func TestManagedMCPCallbackAttachNeverAcquiresOrMovesExistingLease(t *testing.T) {
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, nil, emptyEnv(), nil)
	o.mcpCallbackRoot = filepath.Join(t.TempDir(), "leases")
	owner := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "yolo-ws-abcd1234", Runtime: "podman", Generation: "running-generation"}
	if disposition, _ := o.acquireCallbackLease(owner); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	before, err := os.ReadFile(mcpsignin.CallbackLeasePath(o.callbackRoot(), 3118))
	if err != nil {
		t.Fatal(err)
	}
	o.mcpCallbackLeaseEvent = func(string, mcpsignin.CallbackLease) { t.Fatal("attach attempted lease operation") }
	o.mcpCallbackPortFree = func(int) (bool, bool) { t.Fatal("attach probed callback bind"); return false, false }
	rc, restarted, execed := attachToExec(t, o, cfg, nil, channel)
	after, err := os.ReadFile(mcpsignin.CallbackLeasePath(o.callbackRoot(), 3118))
	if rc != 0 || restarted || !execed || err != nil || !reflect.DeepEqual(before, after) || o.mcpCallbackGeneration != "" {
		t.Fatalf("actual attach moved lease: rc=%d restarted=%v execed=%v %v %s", rc, restarted, execed, err, stderr.String())
	}
}

func TestManagedMCPCallbackSimultaneousLeaseAcquisition(t *testing.T) {
	root := filepath.Join(t.TempDir(), "leases")
	first := callbackLeaseOptions(t, root, true, "live-id")
	second := callbackLeaseOptions(t, root, true, "live-id")
	leases := []mcpsignin.CallbackLease{{Version: 1, Port: 3118, Launch: "first", Runtime: "podman", Generation: "first-generation"}, {Version: 1, Port: 3118, Launch: "second", Runtime: "podman", Generation: "second-generation"}}
	begin := make(chan struct{})
	results := make(chan string, 2)
	var done sync.WaitGroup
	for i, options := range []*Options{first, second} {
		done.Add(1)
		go func(o *Options, lease mcpsignin.CallbackLease) {
			defer done.Done()
			<-begin
			disposition, _ := o.acquireCallbackLease(lease)
			results <- disposition
		}(options, leases[i])
	}
	close(begin)
	done.Wait()
	close(results)
	counts := map[string]int{}
	for disposition := range results {
		counts[disposition]++
	}
	if counts["publication pending"] != 1 || counts["held"] != 1 {
		t.Fatal("not exclusive", counts)
	}
	lease, err := mcpsignin.ReadCallbackLease(root, 3118)
	if err != nil || (lease != leases[0] && lease != leases[1]) {
		t.Fatal(lease, err)
	}
}

func TestManagedMCPCallbackReclaimOnlyAfterConfirmedAbsence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "leases")
	owner := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "first", Runtime: "podman", Generation: "old-generation"}
	o := callbackLeaseOptions(t, root, true, "")
	if disposition, _ := o.acquireCallbackLease(owner); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	replacement := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "second", Runtime: "podman", Generation: "new-generation"}
	unknown := callbackLeaseOptions(t, root, false, "")
	if disposition, held := unknown.acquireCallbackLease(replacement); disposition != "held" || held != "first" {
		t.Fatal("unknown stole lease", disposition, held)
	}
	if disposition, _ := o.acquireCallbackLease(replacement); disposition != "publication pending" {
		t.Fatal("confirmed absence did not reclaim", disposition)
	}
	o.releaseCallbackLease(owner)
	current, err := mcpsignin.ReadCallbackLease(root, 3118)
	if err != nil || current != replacement {
		t.Fatal("old teardown removed new generation", current, err)
	}
}

func TestManagedMCPCallbackForgetLifecycleExcludesPendingReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "leases")
	o := callbackLeaseOptions(t, root, true, "")
	old := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "same-cname", Runtime: "podman", Generation: "old-generation"}
	if disposition, _ := o.acquireCallbackLease(old); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	pending := callbackLeaseOptions(t, root, true, "")
	pending.holdLaunchLock(old.Launch)
	defer pending.releaseLaunchLock()
	newLease := old
	newLease.Generation = "new-generation"
	if disposition, _ := pending.acquireCallbackLease(newLease); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	reaper := callbackLeaseOptions(t, root, true, "")
	reaper.forgetGoneContainer(old.Launch, "podman", "")
	if lease, err := mcpsignin.ReadCallbackLease(root, 3118); err != nil || lease != newLease {
		t.Fatal("pending lifecycle exclusion lost", lease, err)
	}
	pending.releaseLaunchLock()
	o.mcpCallbackGeneration = old.Generation
	o.mcpCallbackLeases = []mcpsignin.CallbackLease{old}
	o.forgetGoneContainer(old.Launch, "podman", "")
	if lease, err := mcpsignin.ReadCallbackLease(root, 3118); err != nil || lease != newLease {
		t.Fatal("stale explicit generation discovered replacement", lease, err)
	}
	// The runtime becomes live before an independent retirement can own the
	// lifecycle gate: its new generation remains, even after that gate opens.
	reaper.Exec = func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, Stdout: "new-live-id"}
	}
	reaper.forgetGoneContainer(old.Launch, "podman", "")
	if lease, err := mcpsignin.ReadCallbackLease(root, 3118); err != nil || lease != newLease {
		t.Fatal("live replacement retired", lease, err)
	}
}

func TestManagedMCPCallbackForgetProbeHoldsLifecycleBeforeEnumeration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "leases")
	o := callbackLeaseOptions(t, root, true, "")
	lease := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "probe-cname", Runtime: "podman", Generation: "generation"}
	if disposition, _ := o.acquireCallbackLease(lease); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	probed := false
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		probed = true
		lock, acquired := tryWorkspaceLock(lease.Launch)
		if acquired {
			lock.Close()
			t.Error("probe/enumeration gap allowed pending lease registration")
		}
		return ExecResult{Ran: true}
	}
	o.forgetGoneContainer(lease.Launch, "podman", "")
	if !probed {
		t.Fatal("actual retirement did not probe")
	}
	if _, err := mcpsignin.ReadCallbackLease(root, 3118); !os.IsNotExist(err) {
		t.Fatal("known gone not retired", err)
	}
}

func TestManagedMCPCallbackReleaseRacesReplacementWithoutDeletingIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "leases")
	o := callbackLeaseOptions(t, root, true, "")
	old := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "first", Runtime: "podman", Generation: "old-generation"}
	newLease := old
	newLease.Generation = "new-generation"
	if disposition, _ := o.acquireCallbackLease(old); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	begin := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)
	go func() { defer done.Done(); <-begin; o.releaseCallbackLease(old) }()
	go func() { defer done.Done(); <-begin; o.acquireCallbackLease(newLease) }()
	close(begin)
	done.Wait()
	current, err := mcpsignin.ReadCallbackLease(root, 3118)
	if err != nil || current != newLease {
		t.Fatal("stale concurrent release removed replacement", current, err)
	}
}

func TestManagedMCPCallbackMalformedOrUnsafeLeaseIsUnavailableAndUnrepaired(t *testing.T) {
	for _, body := range []string{`{"version":2,"version":1}`, `{"version":2}`, `{"version":1,"port":3118,"launch":"first","runtime":"podman","generation":"g","extra":"do-not-print"}`, `{`} {
		root := filepath.Join(t.TempDir(), "leases")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		path := mcpsignin.CallbackLeasePath(root, 3118)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		o := callbackLeaseOptions(t, root, true, "")
		lease := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "new", Runtime: "podman", Generation: "new-generation"}
		if disposition, _ := o.acquireCallbackLease(lease); disposition != "unavailable" {
			t.Fatal(disposition)
		}
		after, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(after, []byte(body)) {
			t.Fatal("malformed lease repaired")
		}
	}
}

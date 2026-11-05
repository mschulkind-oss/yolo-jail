//go:build linux

package resourcegroups

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOpenPinnedIOChildrenRejectsOrdinaryFilesystemBeforeCreatingChildren supplies
// valid selected-unit and finite-policy fixtures but keeps the constructor's real
// cgroup-v2 filesystem guard; refusal must precede child creation or policy writes.
func TestOpenPinnedIOChildrenRejectsOrdinaryFilesystemBeforeCreatingChildren(t *testing.T) {
	root := t.TempDir()
	delegated := filepath.Join(root, "delegated")
	if err := os.Mkdir(delegated, 0700); err != nil {
		t.Fatal(err)
	}
	assigned := filepath.Join(delegated, "io.max")
	before := []byte("8:0 rbps=101 wbps=20 riops=max\n")
	if err := os.WriteFile(assigned, before, 0600); err != nil {
		t.Fatal(err)
	}
	paths := IOPaths{Assigned: assigned, Devices: []string{"8:0"}, UserManager: true}
	if plan := ReadAndPlanIO(paths, 5, os.ReadFile); !hasFiniteSplit(plan) {
		t.Fatalf("fixture must provide a valid finite selected policy: %+v", plan)
	}

	originalObserver := pinnedIOUnitObserver
	observerCalls := 0
	pinnedIOUnitObserver = func(ctx context.Context, unit string, selected IOPaths) error {
		observerCalls++
		if ctx.Err() != nil || unit != "fixture.service" || !selected.UserManager ||
			selected.Assigned != assigned || len(selected.Devices) != 1 || selected.Devices[0] != "8:0" {
			return errors.New("fixture selected-unit metadata was invalid")
		}
		return nil
	}
	t.Cleanup(func() { pinnedIOUnitObserver = originalObserver })

	children, err := openPinnedIOChildren(context.Background(), "fixture.service", paths, 5, filepath.Join(root, "locks"))
	if children != nil {
		children.close()
		t.Fatal("ordinary filesystem was admitted as a delegated cgroup group")
	}
	if err == nil || !strings.Contains(err.Error(), "not on cgroup v2") {
		t.Fatalf("constructor error = %v, want mandatory cgroup-v2 filesystem refusal", err)
	}
	if observerCalls != 1 {
		t.Fatalf("selected-unit observer calls = %d, want exactly one before filesystem refusal", observerCalls)
	}
	if got, err := os.ReadFile(assigned); err != nil || string(got) != string(before) {
		t.Fatalf("assigned fixture policy changed: %q err=%v", got, err)
	}
	for _, name := range []string{"yolo-agent", "yolo-batch", "locks"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("constructor created %s before refusing ordinary filesystem: %v", name, err)
		}
	}
}

// Ordinary-directory fixtures exercise real openat/write syscalls, not cgroup
// authority. Cgroup-v2 rejects rename; this substitution stresses an even
// stronger adversarial name change without granting a production filesystem bypass.
func TestPinnedIOWriterDoesNotWriteRecreatedName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"yolo-agent", "yolo-batch", "sibling"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "io.max"), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &pinnedIOChildren{parent: parent, path: root, children: map[string]*pinnedGroup{}}
	defer p.close()
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		p.children[name], err = parent.openChild(name, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.verifyChild(name); err != nil {
			t.Fatal(err)
		}
	}
	// Owner replaces the canonical name after identity verification. In a
	// cgroup-v2 hierarchy rename is forbidden, but empty removal/recreation
	// still demands that stale descriptors never target the replacement.
	if err := os.Rename(filepath.Join(root, "yolo-agent"), filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "yolo-agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "yolo-agent", "io.max"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	// Only the validation of regular files is test-specific. The very same
	// descriptor-relative openat/write function serves the native writer.
	if err := writePolicyAt(p.children["yolo-agent"].fd, []byte("written"), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"old": "written", "yolo-agent": "replacement", "yolo-batch": "before", "sibling": "before"} {
		got, err := os.ReadFile(filepath.Join(root, name, "io.max"))
		if err != nil || string(got) != want {
			t.Fatalf("%s: got %q, want %q: %v", name, got, want, err)
		}
	}
	if err := p.verifyChild("yolo-agent"); err == nil {
		t.Fatal("replaced name permitted a required start")
	}
	paths := IOPaths{Assigned: filepath.Join(root, "io.max"), UserManager: true}
	p.paths = paths
	if err := p.verifyPinnedStart(context.Background(), paths, "yolo-agent", 42, func(int) (string, error) {
		return filepath.Join(root, "yolo-agent"), nil
	}); err == nil {
		t.Fatal("replacement accepted after pinned write")
	}
}

func TestPinnedIOWriterRejectsNonCgroupPolicyAndUnsafeChildren(t *testing.T) {
	root := t.TempDir()
	assigned := filepath.Join(root, "io.max")
	if err := os.WriteFile(assigned, []byte("8:0 rbps=101\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := IOPaths{Assigned: assigned, Devices: []string{"8:0"}, UserManager: true}
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "io.max"), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &pinnedIOChildren{parent: parent, path: root, paths: paths, percent: 5, plan: ReadAndPlanIO(paths, 5, os.ReadFile), children: map[string]*pinnedGroup{}}
	defer p.close()
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		p.children[name], err = parent.openChild(name, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := p.writeChildPolicy(context.Background(), "yolo-agent", []byte("8:0 rbps=5\n")); err == nil || !strings.Contains(err.Error(), "not a cgroup v2 policy") {
		t.Fatalf("real writer must refuse ordinary file: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "yolo-agent", "io.max")); err != nil || string(data) != "before" {
		t.Fatalf("ordinary policy mutated: %q %v", data, err)
	}
	if err := p.writeChildPolicy(context.Background(), ".control", []byte("8:0 rbps=5\n")); err == nil {
		t.Fatal("manager-owned child accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.writeChildPolicy(ctx, "yolo-batch", []byte("8:0 rbps=5\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write accepted: %v", err)
	}
}

func TestPinnedIOPolicyUsesFiniteAssignedSplitAndRejectsDrift(t *testing.T) {
	root := t.TempDir()
	assigned := filepath.Join(root, "io.max")
	if err := os.WriteFile(assigned, []byte("8:0 rbps=101 wbps=20 riops=max\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := IOPaths{Assigned: assigned, Devices: []string{"8:0"}, UserManager: true}
	p := &pinnedIOChildren{paths: paths, percent: 5, plan: ReadAndPlanIO(paths, 5, os.ReadFile)}
	if err := p.allowPolicy("yolo-agent", []byte("8:0 rbps=5 wbps=1\n")); err != nil {
		t.Fatalf("finite agent split refused: %v", err)
	}
	if err := p.allowPolicy("yolo-batch", []byte("8:0 rbps=96 wbps=19\n")); err != nil {
		t.Fatalf("finite batch split refused: %v", err)
	}
	for _, data := range []string{"8:0 rbps=max wbps=1\n", "8:0 rbps=6 wbps=1\n", "8:0 rbps=5\n", "8:1 rbps=5 wbps=1\n"} {
		if err := p.allowPolicy("yolo-agent", []byte(data)); err == nil {
			t.Fatalf("unbounded, incorrect, incomplete or unrelated ceiling accepted: %q", data)
		}
	}
	if err := os.WriteFile(assigned, []byte("8:0 rbps=max wbps=20 riops=max\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.allowPolicy("yolo-agent", []byte("8:0 rbps=5 wbps=1\n")); err == nil {
		t.Fatal("finite-to-unlimited parent drift accepted")
	}
}

func TestPinnedIOSharedGroupLockSerializesLaunchers(t *testing.T) {
	group, err := openPinnedGroup(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	lockDir := privateLock(t)
	first, err := lockPinnedIOGroup(context.Background(), group, lockDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if second, err := lockPinnedIOGroup(ctx, group, lockDir); err == nil {
		second.Close()
		t.Fatal("second launcher entered locked group")
	}
	first.Close()
	third, err := lockPinnedIOGroup(context.Background(), group, lockDir)
	if err != nil {
		t.Fatalf("released shared group lock unavailable: %v", err)
	}
	third.Close()
}

func TestVerifyWrittenIOPolicyRequiresExactDeviceAndDimensions(t *testing.T) {
	requested := []byte("8:0 rbps=5 wbps=1\n")
	for _, tc := range []struct {
		name     string
		observed string
		wantErr  bool
	}{
		{name: "matching keyed row with unrelated max", observed: "8:0 rbps=5 wbps=1 riops=max\n18:0 rbps=max\n"},
		{name: "substring of another device", observed: "18:0 rbps=5 wbps=1\n", wantErr: true},
		{name: "correct device wrong value", observed: "8:0 rbps=6 wbps=1\n", wantErr: true},
		{name: "correct device wrong dimension", observed: "8:0 rbps=5 wiops=1\n", wantErr: true},
		{name: "correct device missing dimension", observed: "8:0 rbps=5\n", wantErr: true},
		{name: "finite became max", observed: "8:0 rbps=max wbps=1\n", wantErr: true},
		{name: "duplicate device", observed: "8:0 rbps=5 wbps=1\n8:0 rbps=6\n", wantErr: true},
		{name: "duplicate dimension", observed: "8:0 rbps=5 wbps=1 rbps=6\n", wantErr: true},
		{name: "malformed value", observed: "8:0 rbps=hi wbps=1\n", wantErr: true},
		{name: "unknown dimension", observed: "8:0 rbps=5 wbps=1 mystery=4\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyWrittenIOPolicy(requested, []byte(tc.observed))
			if (err != nil) != tc.wantErr {
				t.Fatalf("immediate write readback %q: err=%v wantErr=%t", tc.observed, err, tc.wantErr)
			}
		})
	}
	if err := verifyWrittenIOPolicy([]byte("8:0 riops=max\n"), []byte("8:0 riops=max\n")); err != nil {
		t.Fatalf("matching max is parseable in readback (allocation validation stays separate): %v", err)
	}
	if err := verifyWrittenIOPolicy([]byte("8:0 rbps=5 rbps=6\n"), []byte("8:0 rbps=5\n")); err == nil {
		t.Fatal("ambiguous requested policy accepted")
	}
}

func TestPinnedIOStartRejectsCancellationDuringObservedMembership(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &pinnedIOChildren{parent: parent, path: root, children: map[string]*pinnedGroup{}}
	defer p.close()
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		p.children[name], err = parent.openChild(name, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	paths := IOPaths{Assigned: filepath.Join(root, "io.max"), UserManager: true}
	p.paths = paths
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := false
	err = p.verifyPinnedStart(ctx, paths, "yolo-agent", 42, func(pid int) (string, error) {
		observed = true
		cancel()
		return filepath.Join(root, "yolo-agent"), nil
	})
	if !observed || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation after correct observed membership must refuse: callback=%t err=%v", observed, err)
	}
}

func TestPinnedIOStartChecksBothChildrenAndObservedPID(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &pinnedIOChildren{parent: parent, path: root, children: map[string]*pinnedGroup{}}
	defer p.close()
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		p.children[name], err = parent.openChild(name, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	paths := IOPaths{Assigned: filepath.Join(root, "io.max"), UserManager: true}
	p.paths = paths
	observed := filepath.Join(root, "yolo-batch")
	member := func(int) (string, error) { return observed, nil }
	if err := p.verifyPinnedStart(context.Background(), paths, "yolo-agent", 42, member); err == nil {
		t.Fatal("wrong child accepted")
	}
	observed = filepath.Join(root, "yolo-agent")
	if err := p.verifyPinnedStart(context.Background(), paths, "yolo-agent", 42, member); err != nil {
		t.Fatalf("matching observed child refused: %v", err)
	}
	if err := os.Rename(filepath.Join(root, "yolo-batch"), filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "yolo-batch"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.verifyPinnedStart(context.Background(), paths, "yolo-agent", 42, member); err == nil {
		t.Fatal("other managed child replacement accepted")
	}
}

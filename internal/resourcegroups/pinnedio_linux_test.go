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
	p, root, _ := pinnedSelectedFixture(t, 0)
	if err := os.Mkdir(filepath.Join(root, "sibling"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"yolo-agent", "yolo-batch", "sibling"} {
		if err := os.WriteFile(filepath.Join(root, name, "io.max"), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
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
	if err := writePolicyAt(p.children["yolo-agent"].fd, []byte("written"), regularPolicy); err != nil {
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
	if err := p.verifyPinnedStart(context.Background(), p.paths, "yolo-agent", 42, func(int) (string, error) {
		return filepath.Join(root, "yolo-agent"), nil
	}); err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("replacement accepted after pinned write: %v", err)
	}
}

func TestPinnedIOWriterRejectsNonCgroupPolicyAndUnsafeChildren(t *testing.T) {
	p, root, _ := pinnedSelectedFixture(t, 0)
	p.policyFS = nil // the production cgroup-v2 descriptor check
	if err := p.writeChildPolicy(context.Background(), "yolo-agent", []byte("8:0 rbps=5 wbps=1\n")); err == nil || !strings.Contains(err.Error(), "not a cgroup v2 policy") {
		t.Fatalf("real writer must refuse ordinary file: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "yolo-agent", "io.max")); err != nil || string(data) != "8:0 rbps=5 wbps=1\n" {
		t.Fatalf("ordinary policy mutated: %q %v", data, err)
	}
	if err := p.writeChildPolicy(context.Background(), ".control", []byte("8:0 rbps=5 wbps=1\n")); err == nil {
		t.Fatal("manager-owned child accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.writeChildPolicy(ctx, "yolo-batch", []byte("8:0 rbps=96 wbps=19\n")); !errors.Is(err, context.Canceled) {
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
	p, root, _ := pinnedSelectedFixture(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := false
	err := p.verifyPinnedStart(ctx, p.paths, "yolo-agent", 42, func(pid int) (string, error) {
		observed = true
		cancel()
		return filepath.Join(root, "yolo-agent"), nil
	})
	if !observed || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation after correct observed membership must refuse: callback=%t err=%v", observed, err)
	}
}

func TestPinnedIOStartChecksBothChildrenAndObservedPID(t *testing.T) {
	p, root, _ := pinnedSelectedFixture(t, 0)
	paths := p.paths
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

// regularPolicy admits ordinary-file fixtures in place of the production
// cgroup-v2 descriptor check; the descriptor-relative syscalls stay real.
func regularPolicy(fd int) error { return nil }

// pinnedSelectedFixture builds a selected-unit prototype over ordinary
// directories: a finite assigned split and both children holding it. The
// selected-unit observer is faked through the same seam the constructor uses,
// and fails from call failAt on (0 never fails).
func pinnedSelectedFixture(t *testing.T, failAt int) (*pinnedIOChildren, string, *int) {
	t.Helper()
	root := t.TempDir()
	assigned := filepath.Join(root, "io.max")
	if err := os.WriteFile(assigned, []byte("8:0 rbps=101 wbps=20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, policy := range map[string]string{"yolo-agent": "8:0 rbps=5 wbps=1\n", "yolo-batch": "8:0 rbps=96 wbps=19\n"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "io.max"), []byte(policy), 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths := IOPaths{Assigned: assigned, Devices: []string{"8:0"}, UserManager: true}
	calls := 0
	original := pinnedIOUnitObserver
	pinnedIOUnitObserver = func(ctx context.Context, unit string, selected IOPaths) error {
		calls++
		if unit != "fixture.service" || !sameAssignedGroup(selected, paths) {
			return errors.New("fixture selected-unit metadata was invalid")
		}
		if failAt > 0 && calls >= failAt {
			return errors.New("fixture unit stopped")
		}
		return nil
	}
	t.Cleanup(func() { pinnedIOUnitObserver = original })
	parent, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &pinnedIOChildren{parent: parent, path: root, unit: "fixture.service", paths: paths, percent: 5, plan: ReadAndPlanIO(paths, 5, os.ReadFile), children: map[string]*pinnedGroup{}, policyFS: regularPolicy}
	t.Cleanup(p.close)
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if p.children[name], err = parent.openChild(name, false); err != nil {
			t.Fatal(err)
		}
	}
	if !hasFiniteSplit(p.plan) {
		t.Fatalf("fixture must hold a finite split: %+v", p.plan)
	}
	return p, root, &calls
}

// A live member pins its cgroup against rmdir, so child identity must be
// re-verified after membership is observed: a name replaced while the PID was
// being observed must refuse the required start.
func TestPinnedIOStartRechecksChildIdentityAfterObservedMembership(t *testing.T) {
	p, root, _ := pinnedSelectedFixture(t, 0)
	observed := false
	err := p.verifyPinnedStart(context.Background(), p.paths, "yolo-agent", 42, func(int) (string, error) {
		observed = true
		if err := os.Rename(filepath.Join(root, "yolo-agent"), filepath.Join(root, "old")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, "yolo-agent"), 0700); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(root, "yolo-agent"), nil
	})
	if !observed {
		t.Fatalf("membership was never observed: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("replacement during membership observation admitted a start: %v", err)
	}
}

// The zero-value unit must not be a quieter mode: without a selected unit the
// prototype refuses before writing a child or observing membership.
func TestPinnedIOChildrenRefuseWithoutSelectedUnit(t *testing.T) {
	p, root, _ := pinnedSelectedFixture(t, 0)
	p.unit = ""
	policy := filepath.Join(root, "yolo-agent", "io.max")
	if err := os.WriteFile(policy, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.writeChildPolicy(context.Background(), "yolo-agent", []byte("8:0 rbps=5 wbps=1\n")); err == nil {
		t.Fatal("write admitted without a selected unit")
	}
	if got, err := os.ReadFile(policy); err != nil || len(got) != 0 {
		t.Fatalf("child policy written without a selected unit: %q %v", got, err)
	}
	observed := false
	if err := p.verifyPinnedStart(context.Background(), p.paths, "yolo-agent", 42, func(int) (string, error) {
		observed = true
		return filepath.Join(root, "yolo-agent"), nil
	}); err == nil || observed {
		t.Fatalf("start without a selected unit: err=%v membership observed=%t", err, observed)
	}
}

func TestPinnedIOStartReobservesUnitPolicyAndReadbackBeforeMembership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failAt  int
		mutate  func(t *testing.T, root string)
		wantErr string
	}{
		{name: "admits matching state"},
		{name: "unit stops before start", failAt: 1, wantErr: "fixture unit stopped"},
		{name: "unit stops after readback", failAt: 2, wantErr: "fixture unit stopped"},
		{name: "assigned policy drifts", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "io.max"), []byte("8:0 rbps=max wbps=20\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "assigned policy changed"},
		{name: "sibling child readback is stale", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "yolo-batch", "io.max"), []byte("8:0 rbps=97 wbps=19\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "stale or unexpected"},
		{name: "child ceiling missing", mutate: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "yolo-agent", "io.max"), []byte("8:0 rbps=5\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "ceiling missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, root, calls := pinnedSelectedFixture(t, tc.failAt)
			if tc.mutate != nil {
				tc.mutate(t, root)
			}
			observed := false
			err := p.verifyPinnedStart(context.Background(), p.paths, "yolo-agent", 42, func(int) (string, error) {
				observed = true
				return filepath.Join(root, "yolo-agent"), nil
			})
			if tc.wantErr == "" {
				if err != nil || !observed || *calls != 2 {
					t.Fatalf("matching state refused: err=%v observed=%t unit observations=%d", err, observed, *calls)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || observed {
				t.Fatalf("err=%v (want %q), membership observed=%t", err, tc.wantErr, observed)
			}
		})
	}
}

func TestPinnedIOWriteChecksUnitAndAllocationBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failAt  int
		data    string
		wantErr string
	}{
		{name: "writes and reads back the agent split", data: "8:0 rbps=5 wbps=1\n"},
		{name: "wrong ceiling", data: "8:0 rbps=6 wbps=1\n", wantErr: "differs from finite assigned split"},
		{name: "unit stopped", failAt: 1, data: "8:0 rbps=5 wbps=1\n", wantErr: "fixture unit stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, root, calls := pinnedSelectedFixture(t, tc.failAt)
			policy := filepath.Join(root, "yolo-agent", "io.max")
			if err := os.WriteFile(policy, nil, 0600); err != nil {
				t.Fatal(err)
			}
			err := p.writeChildPolicy(context.Background(), "yolo-agent", []byte(tc.data))
			got, readErr := os.ReadFile(policy)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.wantErr == "" {
				if err != nil || string(got) != tc.data || *calls != 2 {
					t.Fatalf("err=%v policy=%q unit observations=%d", err, got, *calls)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || len(got) != 0 {
				t.Fatalf("err=%v (want %q), policy written=%q", err, tc.wantErr, got)
			}
		})
	}
}

// The kernel may clamp or reject part of a write; the readback through the same
// pinned descriptor is what admits it. Here the fixture's validator rewrites
// the file between the write and the readback, standing in for a kernel that
// stored a different ceiling.
func TestPinnedIOWriteRefusesDifferingReadback(t *testing.T) {
	p, root, _ := pinnedSelectedFixture(t, 0)
	policy := filepath.Join(root, "yolo-agent", "io.max")
	if err := os.WriteFile(policy, nil, 0600); err != nil {
		t.Fatal(err)
	}
	opens := 0
	p.policyFS = func(int) error {
		opens++
		if opens == 2 {
			return os.WriteFile(policy, []byte("8:0 rbps=5 wbps=2\n"), 0600)
		}
		return nil
	}
	err := p.writeChildPolicy(context.Background(), "yolo-agent", []byte("8:0 rbps=5 wbps=1\n"))
	if opens != 2 || err == nil || !strings.Contains(err.Error(), "readback differs") {
		t.Fatalf("differing readback admitted: opens=%d err=%v", opens, err)
	}
}

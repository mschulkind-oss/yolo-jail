//go:build linux

package resourcegroups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type fakeDelegation struct {
	mu         sync.Mutex
	files      map[string]string
	dirs       map[string]bool
	writes     []string
	deny       string
	membership map[int]string
}

func (f *fakeDelegation) read(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(v), nil
}
func (f *fakeDelegation) mkdir(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path == f.deny {
		return os.ErrPermission
	}
	f.dirs[path] = true
	return nil
}
func (f *fakeDelegation) write(path string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path == f.deny {
		return os.ErrPermission
	}
	f.writes = append(f.writes, path+"="+string(data))
	if strings.HasSuffix(path, "/io.max") {
		device := strings.Fields(string(data))[0]
		lines := strings.Split(strings.TrimSpace(f.files[path]), "\n")
		replaced := false
		for i, line := range lines {
			if strings.HasPrefix(line, device+" ") {
				lines[i] = strings.TrimSpace(string(data))
				replaced = true
			}
		}
		if !replaced {
			lines = append(lines, strings.TrimSpace(string(data)))
		}
		f.files[path] = strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
	}
	return nil
}
func (f *fakeDelegation) member(pid int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.membership[pid], nil
}
func privateLock(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func fixture(t *testing.T) (*fakeDelegation, IOPaths, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "owner")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f := &fakeDelegation{files: map[string]string{filepath.Join(root, "cgroup.controllers"): "io cpu", filepath.Join(root, "cgroup.subtree_control"): "io", filepath.Join(root, "io.max"): "8:0 rbps=101 wbps=20\n"}, dirs: map[string]bool{}, membership: map[int]string{}}
	return f, IOPaths{Assigned: filepath.Join(root, "io.max"), Devices: []string{"8:0"}, UserManager: true}, root
}
func TestDelegatedIORefusesMissingRightsAndUnknownPolicyWithoutWrites(t *testing.T) {
	for _, name := range []string{"system manager", "missing io", "permission", "unknown", "tiny"} {
		t.Run(name, func(t *testing.T) {
			f, p, root := fixture(t)
			switch name {
			case "system manager":
				p.UserManager = false
			case "missing io":
				f.files[filepath.Join(root, "cgroup.subtree_control")] = "cpu"
			case "permission":
				f.deny = filepath.Join(root, "yolo-agent")
			case "unknown":
				f.files[p.Assigned] = "8:0 rbps=max wbps=20\n"
				f.files[filepath.Join(root, "yolo-agent", "io.max")] = "8:0 rbps=5 wbps=1\n"
			case "tiny":
				f.files[p.Assigned] = "8:0 rbps=1 wbps=20\n"
			}
			err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write)
			if err == nil {
				t.Fatal("accepted unsupported authority/policy")
			}
			for _, w := range f.writes {
				if name != "permission" {
					t.Fatalf("write after failed proof: %s", w)
				}
			}
		})
	}
}
func TestDelegatedIOSkipsUnconstrainedDimension(t *testing.T) {
	f, p, _ := fixture(t)
	f.files[p.Assigned] = "8:0 rbps=max wbps=20\n"
	if err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err != nil {
		t.Fatal(err)
	}
	for _, w := range f.writes {
		if strings.Contains(w, "rbps=") {
			t.Fatalf("invented read limit: %s", w)
		}
	}
}

func TestDelegatedIOReusesSharedChildrenAndRejectsParentDrift(t *testing.T) {
	f, p, root := fixture(t)
	lock := privateLock(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ReconcileDelegatedIO(context.Background(), p, 5, lock, func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range f.writes {
		if strings.Contains(w, root+"/io.max") || strings.Contains(w, "subtree_control") {
			t.Fatalf("owner write: %s", w)
		}
		if !strings.Contains(w, "yolo-agent/io.max") && !strings.Contains(w, "yolo-batch/io.max") {
			t.Fatalf("foreign write: %s", w)
		}
	}
	f.files[p.Assigned] = "8:0 rbps=max wbps=20\n"
	n := len(f.writes)
	if err := ReconcileDelegatedIO(context.Background(), p, 5, lock, func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err == nil {
		t.Fatal("accepted finite to unknown")
	}
	if len(f.writes) != n {
		t.Fatal("wrote after parent drift")
	}
}
func TestDelegatedIOAdmissionRefusesUnobservedMemberBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wrongPath bool
		stalePID  bool
	}{
		{name: "missing"},
		{name: "wrong child", wrongPath: true},
		{name: "stale PID", stalePID: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p, root := fixture(t)
			if tc.wrongPath {
				f.membership[42] = filepath.Join(root, "yolo-agent")
			}
			child := filepath.Join(root, "yolo-batch")
			observeMember := f.member
			if tc.stalePID {
				observeMember = func(int) (string, error) { return "", os.ErrNotExist }
			}
			starts, observations := 0, 0
			err := ReconcileDelegatedIOAndStart(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) {
				observations++
				return p, nil
			}, f.read, f.mkdir, f.write, child, 42, observeMember, func() error {
				starts++
				return nil
			})
			if err == nil || starts != 0 {
				t.Fatalf("unobserved/wrong member reached start: err=%v starts=%d", err, starts)
			}
			if observations != 4 {
				t.Fatalf("admission did not use a fresh manager observation: %d", observations)
			}
			assertOnlyDelegatedChildWrites(t, f.writes, root)
		})
	}
}

func TestDelegatedIOAdmissionRefusesMissingDelegationAndUnknownBudget(t *testing.T) {
	for _, tc := range []string{"missing delegation", "unknown budget"} {
		t.Run(tc, func(t *testing.T) {
			f, p, root := fixture(t)
			if tc == "missing delegation" {
				f.files[filepath.Join(root, "cgroup.subtree_control")] = "cpu"
			} else {
				f.files[p.Assigned] = "8:0 rbps=max wbps=max\n"
			}
			starts := 0
			err := ReconcileDelegatedIOAndStart(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) {
				return p, nil
			}, f.read, f.mkdir, f.write, filepath.Join(root, "yolo-agent"), 47, func(int) (string, error) {
				return filepath.Join(root, "yolo-agent"), nil
			}, func() error {
				starts++
				return nil
			})
			if err == nil || starts != 0 || len(f.writes) != 0 {
				t.Fatalf("unsupported authority/budget reached start or wrote: err=%v starts=%d writes=%v", err, starts, f.writes)
			}
		})
	}
}

func TestDelegatedIOAdmissionStartsOnlyAfterFreshMembershipAndPolicy(t *testing.T) {
	f, p, root := fixture(t)
	child := filepath.Join(root, "yolo-agent")
	f.membership[43] = child
	starts, observations := 0, 0
	err := ReconcileDelegatedIOAndStart(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) {
		observations++
		return p, nil
	}, f.read, f.mkdir, f.write, child, 43, f.member, func() error {
		starts++
		return nil
	})
	if err != nil || starts != 1 {
		t.Fatalf("verified membership did not reach start once: err=%v starts=%d", err, starts)
	}
	if observations != 4 {
		t.Fatalf("expected fresh pre-start manager observation, got %d", observations)
	}
	assertOnlyDelegatedChildWrites(t, f.writes, root)
}

func TestDelegatedIOAdmissionRefusesCurrentManagerOrPolicyDrift(t *testing.T) {
	for _, tc := range []string{"manager", "policy"} {
		t.Run(tc, func(t *testing.T) {
			f, p, root := fixture(t)
			starts, observations := 0, 0
			err := ReconcileDelegatedIOAndStart(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) {
				observations++
				if observations == 4 {
					if tc == "manager" {
						changed := p
						changed.Assigned = filepath.Join(root, "changed", "io.max")
						return changed, nil
					}
					f.mu.Lock()
					f.files[p.Assigned] = "8:0 rbps=max wbps=20\n"
					f.mu.Unlock()
				}
				return p, nil
			}, f.read, f.mkdir, f.write, filepath.Join(root, "yolo-agent"), 44, f.member, func() error {
				starts++
				return nil
			})
			if err == nil || starts != 0 {
				t.Fatalf("stale %s state reached start: err=%v starts=%d", tc, err, starts)
			}
			assertOnlyDelegatedChildWrites(t, f.writes, root)
		})
	}
}

func TestDelegatedIOAdmissionReusesChildPairAcrossConcurrentStarts(t *testing.T) {
	f, p, root := fixture(t)
	child := filepath.Join(root, "yolo-batch")
	lock := privateLock(t)
	f.membership[45] = child
	var wg sync.WaitGroup
	var mu sync.Mutex
	starts := 0
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ReconcileDelegatedIOAndStart(context.Background(), p, 5, lock, func() (IOPaths, error) {
				return p, nil
			}, f.read, f.mkdir, f.write, child, 45, f.member, func() error {
				mu.Lock()
				starts++
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if starts != 8 || len(f.dirs) != 2 || !f.dirs[filepath.Join(root, "yolo-agent")] || !f.dirs[filepath.Join(root, "yolo-batch")] {
		t.Fatalf("starts=%d child dirs=%v; reconciliation multiplied or lost shared children", starts, f.dirs)
	}
	assertOnlyDelegatedChildWrites(t, f.writes, root)
}

func TestDelegatedIOAdmissionCancellationNeverStarts(t *testing.T) {
	f, p, root := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	starts, observations, memberObservations := 0, 0, 0
	err := ReconcileDelegatedIOAndStart(ctx, p, 5, privateLock(t), func() (IOPaths, error) {
		observations++
		return p, nil
	}, f.read, f.mkdir, f.write, filepath.Join(root, "yolo-agent"), 46, func(int) (string, error) {
		memberObservations++
		cancel()
		return filepath.Join(root, "yolo-agent"), nil
	}, func() error {
		starts++
		return nil
	})
	if !errors.Is(err, context.Canceled) || starts != 0 || observations != 4 || memberObservations != 1 {
		t.Fatalf("canceled pre-start admission: err=%v starts=%d manager=%d member=%d", err, starts, observations, memberObservations)
	}
	assertOnlyDelegatedChildWrites(t, f.writes, root)
}

func assertOnlyDelegatedChildWrites(t *testing.T, writes []string, parent string) {
	t.Helper()
	for _, write := range writes {
		if strings.Contains(write, parent+"/io.max") || strings.Contains(write, "cgroup.subtree_control") || (!strings.HasPrefix(write, filepath.Join(parent, "yolo-agent")+"/io.max=") && !strings.HasPrefix(write, filepath.Join(parent, "yolo-batch")+"/io.max=")) {
			t.Fatalf("write escaped delegated child paths: %s", write)
		}
	}
}

func TestDelegatedIOMembershipIsObservedNotRequested(t *testing.T) {
	f, p, root := fixture(t)
	if err := VerifyDelegatedMember(p, filepath.Join(root, "yolo-batch"), 42, f.member); err == nil {
		t.Fatal("missing observed PID membership accepted")
	}
	f.membership[42] = filepath.Join(root, "yolo-agent")
	if err := VerifyDelegatedMember(p, filepath.Join(root, "yolo-batch"), 42, f.member); err == nil {
		t.Fatal("wrong observed PID membership accepted")
	}
	f.membership[42] = filepath.Join(root, "yolo-batch")
	if err := VerifyDelegatedMember(p, filepath.Join(root, "yolo-batch"), 42, f.member); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDelegatedMember(p, root, 42, f.member); err == nil {
		t.Fatal("owner group accepted as child")
	}
}
func TestDelegatedIORechecksManagerIdentityBeforeApply(t *testing.T) {
	for _, when := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(when), func(t *testing.T) {
			f, p, _ := fixture(t)
			calls := 0
			observe := func() (IOPaths, error) {
				calls++
				if calls >= when {
					changed := p
					changed.Assigned = filepath.Join(filepath.Dir(p.Assigned), "sibling", "io.max")
					return changed, nil
				}
				return p, nil
			}
			err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), observe, f.read, f.mkdir, f.write)
			if err == nil {
				t.Fatal("accepted manager identity drift")
			}
			if when <= 2 && len(f.writes) != 0 {
				t.Fatalf("wrote after manager drift: %v", f.writes)
			}
		})
	}
}

func TestDelegatedIORejectsUnobservedDeviceAndUncertainAncestor(t *testing.T) {
	for _, tc := range []string{"added device", "ancestor unreadable", "ancestor drift"} {
		t.Run(tc, func(t *testing.T) {
			f, p, root := fixture(t)
			if tc == "added device" {
				f.files[p.Assigned] += "8:16 rbps=100\n"
			}
			if tc != "added device" {
				p.Ancestors = []string{filepath.Join(root, "ancestor.io.max")}
				if tc == "ancestor drift" {
					f.files[p.Ancestors[0]] = "8:0 rbps=1000\n"
				}
			}
			reads := 0
			read := func(path string) ([]byte, error) {
				if path == filepath.Join(root, "ancestor.io.max") && tc == "ancestor drift" {
					reads++
					if reads >= 3 {
						return []byte("8:0 rbps=500\n"), nil
					}
				}
				return f.read(path)
			}
			if err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, read, f.mkdir, f.write); err == nil {
				t.Fatalf("accepted %s", tc)
			}
			if len(f.writes) != 0 {
				t.Fatalf("wrote with uncertain assigned policy: %v", f.writes)
			}
		})
	}
}

func TestDelegatedIOParentRaceAndReadbackFailClosed(t *testing.T) {
	for _, tc := range []string{"before write", "after write", "readback"} {
		t.Run(tc, func(t *testing.T) {
			f, p, _ := fixture(t)
			assignedReads := 0
			read := func(path string) ([]byte, error) {
				if path == p.Assigned {
					assignedReads++
					if tc == "before write" && assignedReads == 2 || tc == "after write" && assignedReads == 3 {
						return []byte("8:0 rbps=max wbps=20\n"), nil
					}
				}
				return f.read(path)
			}
			write := f.write
			if tc == "readback" {
				write = func(path string, data []byte) error {
					if strings.HasSuffix(path, "/io.max") && strings.Contains(path, "yolo-agent") {
						return nil
					}
					return f.write(path, data)
				}
			}
			err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, read, f.mkdir, write)
			if err == nil {
				t.Fatalf("accepted %s", tc)
			}
			if tc == "before write" && len(f.writes) != 0 {
				t.Fatalf("wrote after policy drift: %v", f.writes)
			}
		})
	}
}

func TestDelegatedIOLockCancellationRetainsExistingChildren(t *testing.T) {
	f, p, root := fixture(t)
	lockDir := privateLock(t)
	if err := ReconcileDelegatedIO(context.Background(), p, 5, lockDir, func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(lockDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("lock file: %v %v", entries, err)
	}
	held, err := os.OpenFile(filepath.Join(lockDir, entries[0].Name()), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := len(f.writes)
	if err := ReconcileDelegatedIO(ctx, p, 5, lockDir, func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reconcile: %v", err)
	}
	if len(f.writes) != before || !f.dirs[filepath.Join(root, "yolo-agent")] || !f.dirs[filepath.Join(root, "yolo-batch")] {
		t.Fatal("cancellation altered surviving children")
	}
}

func TestDelegatedIOCrashLeavesChildrenAndPermissionFailureFailsClosed(t *testing.T) {
	f, p, root := fixture(t)
	lock := privateLock(t)
	if err := ReconcileDelegatedIO(context.Background(), p, 5, lock, func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err != nil {
		t.Fatal(err)
	}
	f.membership[55] = filepath.Join(root, "yolo-agent")
	f.deny = filepath.Join(root, "yolo-agent", "io.max")
	if err := ReconcileDelegatedIO(context.Background(), p, 5, lock, func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("write denial: %v", err)
	}
	for _, child := range []string{"yolo-agent", "yolo-batch"} {
		if !f.dirs[filepath.Join(root, child)] {
			t.Fatal(fmt.Sprintf("crash removed %s", child))
		}
	}
}

func TestPinnedGroupRejectsIntermediateSwapAndOrdinaryPolicy(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := openPinnedGroup(parent, true); err == nil {
		t.Fatal("ordinary directory accepted as cgroupfs")
	}
	if err := os.Symlink(parent, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := openPinnedGroup(filepath.Join(root, "alias"), false); err == nil {
		t.Fatal("intermediate symlink accepted")
	}
	group, err := openPinnedGroup(parent, false)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	if err := os.Rename(parent, filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := group.verifyPath(parent); err == nil {
		t.Fatal("parent swap accepted")
	}
	if err := os.Mkdir(filepath.Join(root, "old", "yolo-agent"), 0700); err != nil {
		t.Fatal(err)
	}
	child, err := group.openChild("yolo-agent", false)
	if err != nil {
		t.Fatal(err)
	}
	defer child.close()
	if err := os.Rename(filepath.Join(root, "old", "yolo-agent"), filepath.Join(root, "old", "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "old", "yolo-agent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := group.verifyChild(child, "yolo-agent"); err == nil {
		t.Fatal("child swap accepted")
	}
	before := []byte("untouched")
	if err := os.WriteFile(filepath.Join(root, "old", "moved", "io.max"), before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := child.writePolicy([]byte("8:0 rbps=5\n"), false); err == nil || !strings.Contains(err.Error(), "not a cgroup v2 policy file") {
		t.Fatalf("ordinary policy gate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "old", "moved", "io.max"))
	if err != nil || string(got) != string(before) {
		t.Fatalf("ordinary policy mutated: %q: %v", got, err)
	}
}

func TestDelegatedIOAncestorOnlyDimensionDoesNotBlockAssignedSplit(t *testing.T) {
	f, p, root := fixture(t)
	f.files[p.Assigned] = "8:0 rbps=101\n"
	p.Ancestors = []string{filepath.Join(root, "ancestor.io.max")}
	f.files[p.Ancestors[0]] = "8:0 wbps=1000\n"
	if err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err != nil {
		t.Fatal(err)
	}
	for _, w := range f.writes {
		if strings.Contains(w, "wbps=") {
			t.Fatalf("invented cap: %s", w)
		}
	}
	f.files[filepath.Join(root, "yolo-agent", "io.max")] = "8:0 rbps=5 wbps=10\n"
	if err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err == nil {
		t.Fatal("stale finite child dimension accepted")
	}
}

func TestDelegatedIORejectsAdditionalFiniteReadback(t *testing.T) {
	f, p, _ := fixture(t)
	f.files[p.Assigned] = "8:0 rbps=101\n8:16 rbps=103\n"
	p.Devices = []string{"8:0", "8:16"}
	added := false
	write := func(path string, data []byte) error {
		if err := f.write(path, data); err != nil {
			return err
		}
		if strings.HasSuffix(path, "yolo-agent/io.max") && !added {
			added = true
			f.mu.Lock()
			f.files[path] += "8:32 rbps=1\n"
			f.mu.Unlock()
		}
		return nil
	}
	err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, write)
	if err == nil || !strings.Contains(err.Error(), "child policy readback differs") {
		t.Fatalf("extra finite readback must reach exact-set comparison: %v", err)
	}
}

func TestPinnedGroupIdentityIsSharedAcrossHardPathAliases(t *testing.T) {
	// Bind aliases cannot be manufactured without mount privilege; inode identity is
	// the same primitive for both names when the kernel maps them to one directory.
	root := t.TempDir()
	a, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	b, err := openPinnedGroup(root+"/.", false)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if a.identity() != b.identity() {
		t.Fatal("one group got two lock identities")
	}
}

func TestDelegatedIOMultiDeviceReadbackRetainsEveryFiniteRow(t *testing.T) {
	f, p, root := fixture(t)
	f.files[p.Assigned] = "8:0 rbps=101\n8:16 wbps=103\n"
	p.Devices = []string{"8:0", "8:16"}
	if err := ReconcileDelegatedIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil }, f.read, f.mkdir, f.write); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		data, err := f.read(filepath.Join(root, name, "io.max"))
		if err != nil {
			t.Fatal(err)
		}
		values, err := ParseIOMax(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(values) != 2 || !values["8:0"][ReadBytes].Finite || !values["8:16"][WriteBytes].Finite {
			t.Fatalf("per-device writes discarded a row: %q", data)
		}
	}
}

func TestReconcileUserIORejectsOrdinaryGroupBeforeCreation(t *testing.T) {
	root := t.TempDir()
	assigned := filepath.Join(root, "io.max")
	if err := os.WriteFile(assigned, []byte("8:0 rbps=101\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := IOPaths{Assigned: assigned, Devices: []string{"8:0"}, UserManager: true}
	err := ReconcileUserIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil })
	if err == nil || !strings.Contains(err.Error(), "cgroup v2") {
		t.Fatalf("ordinary group must fail cgroup-v2 filesystem gate: %v", err)
	}
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("child created before authority proof: %s: %v", name, err)
		}
	}
}

func TestReconcileUserIORefusesDirectMutationBeforeChildCreation(t *testing.T) {
	root := t.TempDir()
	assigned := filepath.Join(root, "io.max")
	original := []byte("8:0 rbps=101\n")
	if err := os.WriteFile(assigned, original, 0600); err != nil {
		t.Fatal(err)
	}
	p := IOPaths{Assigned: assigned, Devices: []string{"8:0"}, UserManager: true}
	err := ReconcileUserIO(context.Background(), p, 5, privateLock(t), func() (IOPaths, error) { return p, nil })
	if err == nil || !strings.Contains(err.Error(), "not on cgroup v2") {
		t.Fatalf("ordinary backing file must fail filesystem gate before child creation: %v", err)
	}
	got, err := os.ReadFile(assigned)
	if err != nil || string(got) != string(original) {
		t.Fatalf("parent changed: %q: %v", got, err)
	}
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("child created before stable-name exclusion: %s: %v", name, err)
		}
	}
}

// Reproduce the gap between a verified name and an fd-relative mutation without
// touching a real cgroup. Even with a pinned child, an authorized rename can move it.
func TestPinnedChildInterveningRenameRedirectsDescriptor(t *testing.T) {
	root := t.TempDir()
	parent, err := openPinnedGroup(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.close()
	if err := os.Mkdir(filepath.Join(root, "yolo-agent"), 0700); err != nil {
		t.Fatal(err)
	}
	child, err := parent.openChild("yolo-agent", false)
	if err != nil {
		t.Fatal(err)
	}
	defer child.close()
	if err := os.WriteFile(filepath.Join(root, "yolo-agent", "io.max"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := parent.verifyPath(root); err != nil {
		t.Fatal(err)
	}
	if err := parent.verifyChild(child, "yolo-agent"); err != nil {
		t.Fatal(err)
	}
	// Hook: another authorized actor renames after identity verification,
	// before the fd-relative operation, then supplies a new intended child.
	if err := os.Rename(filepath.Join(root, "yolo-agent"), filepath.Join(root, "sibling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "yolo-agent"), 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Openat(child.fd, "io.max", syscall.O_WRONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = syscall.Write(fd, []byte("changed!"))
	syscall.Close(fd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "sibling", "io.max"))
	if err != nil || string(got) != "changed!" {
		t.Fatalf("intervening rename did not demonstrate redirected fd write: %q: %v", got, err)
	}
}

func TestPinnedLockRootRejectsPathSwap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locks")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	pinned, err := pinLockRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.close()
	if err := os.Rename(path, filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := pinned.verifyPath(path); err == nil {
		t.Fatal("shared lock root swap accepted")
	}
}

//go:build linux

package resourcegroups

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// stubUnitObserver replaces only the selected-unit manager query, the seam the
// constructor itself calls; the descriptor-relative syscalls stay real.
func stubUnitObserver(t *testing.T) *int {
	t.Helper()
	calls := new(int)
	original := pinnedIOUnitObserver
	pinnedIOUnitObserver = func(ctx context.Context, unit string, selected IOPaths) error {
		*calls++
		if ctx.Err() != nil || unit == "" || !selected.UserManager {
			return errors.New("fixture selected-unit metadata was invalid")
		}
		return nil
	}
	t.Cleanup(func() { pinnedIOUnitObserver = original })
	return calls
}

// ordinaryFS lets a fixture of ordinary directories stand where a cgroup-v2
// mount would; it never changes which syscalls run.
func ordinaryFS(t *testing.T) {
	t.Helper()
	requireCgroupV2FS, pinnedIOPolicyFS = false, regularPolicy
	t.Cleanup(func() { requireCgroupV2FS, pinnedIOPolicyFS = true, nil })
}

// delegatedFixture is an assigned group directory on an ordinary filesystem:
// a finite io.max, both controller files, and (when withChildren) both children
// with empty io.max and cgroup.procs, as a cgroup creates them.
func delegatedFixture(t *testing.T, assigned string, withChildren bool) (string, IOPaths) {
	t.Helper()
	root := filepath.Join(canonicalDir(t), "batch.slice")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"io.max": assigned, "cgroup.controllers": "cpu io memory\n", "cgroup.subtree_control": "io\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if withChildren {
		for _, name := range []string{ChildAgent, ChildBatch} {
			makeChild(t, root, name, "")
		}
	}
	return root, IOPaths{Assigned: filepath.Join(root, "io.max"), Devices: []string{"8:0"}, UserManager: true}
}

func makeChild(t *testing.T, root, name, policy string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{"io.max": policy, "cgroup.procs": ""} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func canonicalDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOpenUserIOChildrenWritesAndReadsBackTheSplit(t *testing.T) {
	ordinaryFS(t)
	observed := stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 rbps=101 wbps=20\n", true)
	children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 5, privateLock(t))
	if err != nil {
		t.Fatalf("OpenUserIOChildren: %v", err)
	}
	defer children.Close()
	if got := readString(t, filepath.Join(root, ChildAgent, "io.max")); got != "8:0 rbps=5 wbps=1\n" {
		t.Fatalf("agent child io.max = %q, want the 5%% floor split", got)
	}
	if got := readString(t, filepath.Join(root, ChildBatch, "io.max")); got != "8:0 rbps=96 wbps=19\n" {
		t.Fatalf("batch child io.max = %q, want the remainder", got)
	}
	if got := readString(t, paths.Assigned); got != "8:0 rbps=101 wbps=20\n" {
		t.Fatalf("assigned io.max was written: %q", got)
	}
	if *observed < 2 {
		t.Fatalf("selected unit observed %d times; the writer must re-observe around its writes", *observed)
	}
	if children.ChildPath(ChildAgent) != filepath.Join(root, ChildAgent) {
		t.Fatalf("ChildPath = %q", children.ChildPath(ChildAgent))
	}
}

func TestReconcileUserIOCreatesMissingChildDirectories(t *testing.T) {
	ordinaryFS(t)
	stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 rbps=101\n", false)
	// A fixture directory has no io.max until a cgroup supplies it, so the writer
	// stops at the read; the point is that creation went through the pinned parent.
	err := ReconcileUserIO(context.Background(), "batch.slice", paths, 5, privateLock(t))
	if err == nil || !strings.Contains(err.Error(), "cgroup.subtree_control") {
		t.Fatalf("a child with no io.max must refuse naming the subtree_control step: %v", err)
	}
	for _, name := range []string{ChildAgent, ChildBatch} {
		if info, statErr := os.Stat(filepath.Join(root, name)); statErr != nil || !info.IsDir() {
			t.Fatalf("child %s was not created under the assigned group: %v", name, statErr)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 3+2 {
		t.Fatalf("assigned group holds %d entries, want its 3 files and the 2 Yolo children", len(entries))
	}
}

func TestOpenUserIOChildrenReusesTheSamePairWithoutASecondAllowance(t *testing.T) {
	ordinaryFS(t)
	stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 rbps=101 wbps=20\n", true)
	lock := privateLock(t)
	var identities [2][2]uint64
	for round := range identities {
		children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 5, lock)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		for i, name := range []string{ChildAgent, ChildBatch} {
			var stat syscall.Stat_t
			if err := syscall.Stat(children.ChildPath(name), &stat); err != nil {
				t.Fatal(err)
			}
			identities[round][i] = stat.Ino
		}
		children.Close()
		children.Close() // idempotent
	}
	if identities[0] != identities[1] {
		t.Fatalf("a second launch replaced the children: %v", identities)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 3+2 {
		t.Fatalf("a second launch multiplied the children: %d entries", len(entries))
	}
	if got := readString(t, filepath.Join(root, ChildAgent, "io.max")); got != "8:0 rbps=5 wbps=1\n" {
		t.Fatalf("reconciling twice changed the agent allowance: %q", got)
	}
}

func TestOpenUserIOChildrenRefusalsWriteNothing(t *testing.T) {
	cases := []struct {
		name     string
		assigned string
		agent    string
		noIO     bool
		want     string
	}{
		{name: "no finite allowance", assigned: "8:0 rbps=max wbps=max\n", want: "IOReadBandwidthMax"},
		{name: "ambiguous assigned policy", assigned: "8:0 rbps=101\n8:0 rbps=7\n", want: "IOReadBandwidthMax"},
		{name: "child keeps a ceiling the plan dropped", assigned: "8:0 rbps=101\n", agent: "8:0 riops=7\n", want: "no longer yields"},
		{name: "child policy is ambiguous", assigned: "8:0 rbps=101\n", agent: "8:0 rbps=1\n8:0 rbps=2\n", want: "ambiguous"},
		{name: "io is not delegated", assigned: "8:0 rbps=101\n", noIO: true, want: "user@.service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ordinaryFS(t)
			stubUnitObserver(t)
			root, paths := delegatedFixture(t, tc.assigned, true)
			if tc.agent != "" {
				if err := os.WriteFile(filepath.Join(root, ChildAgent, "io.max"), []byte(tc.agent), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.noIO {
				// The seam stands in for the manager query, so the controller check
				// is the real CheckDelegatedIO the observer ends in.
				pinnedIOUnitObserver = func(_ context.Context, _ string, selected IOPaths) error {
					return CheckDelegatedIO(selected, os.ReadFile)
				}
				if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := ReconcileUserIO(context.Background(), "batch.slice", paths, 5, privateLock(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "yolo check --no-build") && !strings.Contains(err.Error(), "user@.service") {
				t.Fatalf("refusal names no next step: %v", err)
			}
			for _, name := range []string{ChildBatch} {
				if got := readString(t, filepath.Join(root, name, "io.max")); got != "" {
					t.Fatalf("%s was written before the refusal: %q", name, got)
				}
			}
		})
	}
}

func TestOpenUserIOChildrenCallsReconcile(t *testing.T) {
	// OpenUserIOChildren must not return a writer whose children were never set:
	// deleting its reconcile call leaves empty children, which this catches.
	ordinaryFS(t)
	stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 wbps=20\n", true)
	children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 10, privateLock(t))
	if err != nil {
		t.Fatal(err)
	}
	defer children.Close()
	if readString(t, filepath.Join(root, ChildAgent, "io.max")) != "8:0 wbps=2\n" || readString(t, filepath.Join(root, ChildBatch, "io.max")) != "8:0 wbps=18\n" {
		t.Fatal("OpenUserIOChildren returned without writing the split")
	}
}

// fakeProc lays out /proc/<pid>/cgroup and a mount table that maps the fixture's
// assigned directory the way a cgroup-v2 mount would.
func fakeProc(t *testing.T, root string, pid string, cgroupLine string) (procRoot, mountInfo string) {
	t.Helper()
	base := canonicalDir(t)
	if err := os.MkdirAll(filepath.Join(base, "proc", pid), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "proc", pid, "cgroup"), []byte(cgroupLine), 0o600); err != nil {
		t.Fatal(err)
	}
	mountInfo = filepath.Join(base, "mountinfo")
	line := "30 25 0:26 / " + filepath.Dir(root) + " rw,nosuid - cgroup2 cgroup2 rw\n"
	if err := os.WriteFile(mountInfo, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "proc"), mountInfo
}

func TestPlaceProcessWritesProcsAndWitnessesMembership(t *testing.T) {
	ordinaryFS(t)
	stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 rbps=101 wbps=20\n", true)
	children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 5, privateLock(t))
	if err != nil {
		t.Fatal(err)
	}
	defer children.Close()
	children.p.procRoot, children.p.mountInfo = fakeProc(t, root, "4242", "0::/batch.slice/"+ChildAgent+"\n")
	if err := children.PlaceProcess(context.Background(), ChildAgent, 4242); err != nil {
		t.Fatalf("PlaceProcess: %v", err)
	}
	if got := readString(t, filepath.Join(root, ChildAgent, "cgroup.procs")); got != "4242\n" {
		t.Fatalf("cgroup.procs = %q, want the pid written through the pinned child", got)
	}
	if got := readString(t, filepath.Join(root, ChildBatch, "cgroup.procs")); got != "" {
		t.Fatalf("the batch child received the pid: %q", got)
	}
}

func TestPlaceProcessRefusesWhenObservedMembershipDiffers(t *testing.T) {
	for name, line := range map[string]string{
		"sibling child":   "0::/batch.slice/" + ChildBatch + "\n",
		"the group alone": "0::/batch.slice\n",
		"hybrid v1 lines": "12:cpu:/x\n0::/batch.slice/" + ChildAgent + "\n",
		"deleted cgroup":  "0::/batch.slice/" + ChildAgent + " (deleted)\n",
	} {
		t.Run(name, func(t *testing.T) {
			ordinaryFS(t)
			stubUnitObserver(t)
			root, paths := delegatedFixture(t, "8:0 rbps=101 wbps=20\n", true)
			children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 5, privateLock(t))
			if err != nil {
				t.Fatal(err)
			}
			defer children.Close()
			children.p.procRoot, children.p.mountInfo = fakeProc(t, root, "4242", line)
			err = children.PlaceProcess(context.Background(), ChildAgent, 4242)
			if err == nil {
				t.Fatal("a mismatched /proc/<pid>/cgroup was accepted as membership")
			}
		})
	}
}

func TestPlaceProcessRefusesUnmanagedChildAndBadPID(t *testing.T) {
	ordinaryFS(t)
	stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 rbps=101 wbps=20\n", true)
	children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 5, privateLock(t))
	if err != nil {
		t.Fatal(err)
	}
	defer children.Close()
	for _, name := range []string{".control", "sibling", "../x", ""} {
		if err := children.PlaceProcess(context.Background(), name, 4242); err == nil {
			t.Fatalf("placement into %q was accepted", name)
		}
	}
	if err := children.PlaceProcess(context.Background(), ChildAgent, 0); err == nil {
		t.Fatal("pid 0 was accepted")
	}
	if got := readString(t, filepath.Join(root, ChildAgent, "cgroup.procs")); got != "" {
		t.Fatalf("a refused placement wrote cgroup.procs: %q", got)
	}
}

func TestPlaceProcessReobservesTheUnitBeforeMoving(t *testing.T) {
	ordinaryFS(t)
	calls := stubUnitObserver(t)
	root, paths := delegatedFixture(t, "8:0 rbps=101 wbps=20\n", true)
	children, err := OpenUserIOChildren(context.Background(), "batch.slice", paths, 5, privateLock(t))
	if err != nil {
		t.Fatal(err)
	}
	defer children.Close()
	before := *calls
	pinnedIOUnitObserver = func(context.Context, string, IOPaths) error { return errors.New("unit stopped") }
	if err := children.PlaceProcess(context.Background(), ChildAgent, 4242); err == nil || !strings.Contains(err.Error(), "unit stopped") {
		t.Fatalf("a stopped unit did not refuse the move: %v", err)
	}
	if got := readString(t, filepath.Join(root, ChildAgent, "cgroup.procs")); got != "" {
		t.Fatalf("a pid was moved although the unit no longer observed: %q", got)
	}
	_ = before
}

func TestPlacementWriteErrorNamesTheNextStep(t *testing.T) {
	err := placementWriteError(ChildAgent, syscall.EACCES)
	for _, want := range []string{"common ancestor", "systemd-run --user --scope", ChildAgent} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("EACCES refusal lacks %q: %v", want, err)
		}
	}
	if !errors.Is(err, syscall.EACCES) {
		t.Fatal("the cause is lost")
	}
	if !strings.Contains(placementWriteError(ChildAgent, syscall.EBUSY).Error(), "yolo check --no-build") {
		t.Fatal("EBUSY refusal names no next step")
	}
}

// managerFixture is a systemd user manager that answers `show` for one unit.
type managerFixture struct {
	unit         string
	controlGroup string
	properties   map[string]string
}

func (m managerFixture) query(argv []string) (string, error) {
	if len(argv) < 2 || argv[0] != "systemctl" {
		return "", errors.New("unexpected command")
	}
	user := argv[1] == "--user"
	for _, arg := range argv {
		if prop, ok := strings.CutPrefix(arg, "--property="); ok {
			if prop == "ControlGroup" {
				if user {
					return m.controlGroup + "\n", nil
				}
				return "\n", nil // the system manager does not know the unit
			}
			if value, ok := m.properties[prop]; ok && user {
				return value + "\n", nil
			}
			return "", errors.New("no such property")
		}
	}
	return "", errors.New("no property")
}

func observerFixture(t *testing.T, unit string, subtree string, props map[string]string) (IOPaths, managerFixture, []byte) {
	t.Helper()
	root, _ := delegatedFixture(t, "8:0 rbps=101\n", true)
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte(subtree), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := filepath.Dir(root)
	mountInfo := []byte("30 25 0:26 / " + mount + " rw - cgroup2 cgroup2 rw\n")
	manager := managerFixture{unit: unit, controlGroup: "/" + filepath.Base(root), properties: props}
	paths, err := ResolveSelectedIOPaths(unit, manager.query, mountInfo, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	return paths, manager, mountInfo
}

func TestObserveDelegatedIOUnitAdmitsAnActiveUserSliceWithoutDelegate(t *testing.T) {
	paths, manager, mountInfo := observerFixture(t, "batch.slice", "io\n", map[string]string{"Id": "batch.slice", "ActiveState": "active"})
	if err := observeDelegatedIOUnitWith("batch.slice", paths, manager.query, mountInfo, os.ReadFile); err != nil {
		t.Fatalf("an active user slice with io delegated was refused: %v", err)
	}
}

func TestObserveDelegatedIOUnitRefusals(t *testing.T) {
	cases := []struct {
		name    string
		unit    string
		subtree string
		props   map[string]string
		want    string
	}{
		{"inactive slice", "batch.slice", "io\n", map[string]string{"Id": "batch.slice", "ActiveState": "inactive"}, "systemctl --user start batch.slice"},
		{"io not in subtree_control", "batch.slice", "cpu\n", map[string]string{"Id": "batch.slice", "ActiveState": "active"}, "user@.service"},
		{"service without Delegate", "w.service", "io\n", map[string]string{"Id": "w.service", "ActiveState": "active", "Delegate": "no"}, "Delegate"},
		{"service delegating other controllers", "w.service", "io\n", map[string]string{"Id": "w.service", "ActiveState": "active", "Delegate": "yes", "DelegateControllers": "cpu memory"}, "Delegate= list"},
		{"not a unit type", "w.timer", "io\n", map[string]string{}, "slice, scope or service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unit := tc.unit
			paths, manager, mountInfo := observerFixture(t, unit, tc.subtree, tc.props)
			err := observeDelegatedIOUnitWith(unit, paths, manager.query, mountInfo, os.ReadFile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestObserveDelegatedIOUnitAdmitsADelegatingService(t *testing.T) {
	paths, manager, mountInfo := observerFixture(t, "w.service", "io\n", map[string]string{"Id": "w.service", "ActiveState": "active", "Delegate": "yes", "DelegateControllers": "cpu io"})
	if err := observeDelegatedIOUnitWith("w.service", paths, manager.query, mountInfo, os.ReadFile); err != nil {
		t.Fatalf("a delegating service was refused: %v", err)
	}
}

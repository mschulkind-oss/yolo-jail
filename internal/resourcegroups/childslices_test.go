package resourcegroups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fakeUserManager is a user manager over a fake cgroup tree: systemctl is an
// injected function, and "starting" a slice makes its directory and io.max exist
// the way systemd realizes one. No real systemd or cgroup is touched; a nested
// jail cannot exercise real delegation, so the host witness owes that.
type fakeUserManager struct {
	t        *testing.T
	root     string
	parentCG string
	calls    [][]string
	props    map[string][]string // child unit -> property assignments from set-property
	realized map[string]string   // unit -> control group
	// ioMaxFor rewrites what a started child's io.max holds, to model systemd
	// realizing a different limit than asked, or a stale one.
	ioMaxFor    func(unit, derived string) string
	failSetProp error
	failStart   error
}

func newFakeUserManager(t *testing.T, assignedIOMax string) *fakeUserManager {
	t.Helper()
	root := t.TempDir()
	f := &fakeUserManager{t: t, root: root, parentCG: "/user.slice/user-1000.slice/batch.slice/batch-yolo.slice",
		props: map[string][]string{}, realized: map[string]string{}}
	dir := filepath.Join(root, f.parentCG)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"io.max": assignedIOMax, "cgroup.controllers": "io memory pids\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.realized["batch-yolo.slice"] = f.parentCG
	return f
}

func (f *fakeUserManager) mountInfo() []byte {
	return []byte(fmt.Sprintf("29 23 0:26 / %s rw,nosuid - cgroup2 cgroup rw\n", f.root))
}

func (f *fakeUserManager) paths() IOPaths {
	p, err := ResolveSelectedIOPaths("batch-yolo.slice", f.query, f.mountInfo(), os.ReadFile)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fakeUserManager) seams() ChildSeams {
	return ChildSeams{Query: f.query, MountInfo: f.mountInfo(), ReadFile: os.ReadFile}
}

func (f *fakeUserManager) query(argv []string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	user := len(argv) > 1 && argv[1] == "--user"
	switch {
	case len(argv) > 2 && argv[len(argv)-2] == "--" && strings.Contains(strings.Join(argv, " "), "ControlGroup"):
		if !user {
			return "\n", nil
		}
		return f.realized[argv[len(argv)-1]] + "\n", nil
	case user && argv[2] == "set-property":
		if f.failSetProp != nil {
			return "", f.failSetProp
		}
		f.props[argv[4]] = append([]string(nil), argv[5:]...)
		return "", nil
	case user && argv[2] == "start":
		if f.failStart != nil {
			return "", f.failStart
		}
		for _, unit := range argv[3:] {
			f.realize(unit)
		}
		return "", nil
	}
	return "", fmt.Errorf("unexpected systemctl call %v", argv)
}

// realize derives the io.max systemd would write for a unit's recorded properties.
func (f *fakeUserManager) realize(unit string) {
	cg := f.parentCG + "/" + unit
	f.realized[unit] = cg
	dir := filepath.Join(f.root, cg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	perDevice := map[string]map[string]string{}
	var devices []string
	for _, assignment := range f.props[unit] {
		name, value, _ := strings.Cut(assignment, "=")
		device, number, _ := strings.Cut(value, " ")
		device = strings.TrimPrefix(device, "/dev/block/")
		key := map[string]string{"IOReadBandwidthMax": "rbps", "IOWriteBandwidthMax": "wbps", "IOReadIOPSMax": "riops", "IOWriteIOPSMax": "wiops"}[name]
		if perDevice[device] == nil {
			perDevice[device] = map[string]string{"rbps": "max", "wbps": "max", "riops": "max", "wiops": "max"}
			devices = append(devices, device)
		}
		perDevice[device][key] = number
	}
	sort.Strings(devices)
	var body string
	for _, device := range devices {
		d := perDevice[device]
		body += fmt.Sprintf("%s rbps=%s wbps=%s riops=%s wiops=%s\n", device, d["rbps"], d["wbps"], d["riops"], d["wiops"])
	}
	if f.ioMaxFor != nil {
		body = f.ioMaxFor(unit, body)
	}
	if err := os.WriteFile(filepath.Join(dir, "io.max"), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeUserManager) mutating() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 2 && (c[2] == "set-property" || c[2] == "start") {
			out = append(out, c)
		}
	}
	return out
}

const assignedFinite = "259:0 rbps=2000000000 wbps=1000000000 riops=max wiops=max\n"

func TestChildUnitsNameTheSystemdDashNestedChildren(t *testing.T) {
	for unit, want := range map[string][2]string{
		"batch-yolo.slice": {"batch-yolo-agent.slice", "batch-yolo-batch.slice"},
		"work.slice":       {"work-agent.slice", "work-batch.slice"},
	} {
		agent, batch, err := ChildUnits(unit)
		if err != nil || agent != want[0] || batch != want[1] {
			t.Fatalf("ChildUnits(%q) = %q, %q, %v; want %v", unit, agent, batch, err, want)
		}
	}
	for _, unit := range []string{"app.service", "run.scope", "-.slice", ".slice", "batch yolo.slice"} {
		if _, _, err := ChildUnits(unit); err == nil || !strings.Contains(err.Error(), "yolo check --no-build") {
			t.Fatalf("ChildUnits(%q) err = %v; want a refusal naming the next step", unit, err)
		}
	}
}

func TestEnsureChildSlicesSetsTheSplitThroughTheUserManagerAndStartsThem(t *testing.T) {
	f := newFakeUserManager(t, "259:0 rbps=2000000000 wbps=1000000000 riops=1000 wiops=max\n")
	got, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"systemctl", "--user", "set-property", "--runtime", "batch-yolo-agent.slice", "IOReadBandwidthMax=/dev/block/259:0 100000000", "IOWriteBandwidthMax=/dev/block/259:0 50000000", "IOReadIOPSMax=/dev/block/259:0 50"},
		{"systemctl", "--user", "set-property", "--runtime", "batch-yolo-batch.slice", "IOReadBandwidthMax=/dev/block/259:0 1900000000", "IOWriteBandwidthMax=/dev/block/259:0 950000000", "IOReadIOPSMax=/dev/block/259:0 950"},
		{"systemctl", "--user", "start", "batch-yolo-agent.slice", "batch-yolo-batch.slice"},
	}
	if !reflect.DeepEqual(f.mutating(), want) {
		t.Fatalf("user-manager writes =\n%q\nwant\n%q", f.mutating(), want)
	}
	if got.Agent != "batch-yolo-agent.slice" || got.Batch != "batch-yolo-batch.slice" {
		t.Fatalf("children = %+v", got)
	}
	parent := filepath.Join(f.root, f.parentCG)
	if got.AgentDir != filepath.Join(parent, "batch-yolo-agent.slice") || got.BatchDir != filepath.Join(parent, "batch-yolo-batch.slice") {
		t.Fatalf("child directories = %q, %q", got.AgentDir, got.BatchDir)
	}
}

func TestEnsureChildSlicesNeverWritesACgroupFileItself(t *testing.T) {
	f := newFakeUserManager(t, assignedFinite)
	before := snapshotTree(t, f.root)
	if _, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams()); err != nil {
		t.Fatal(err)
	}
	// Only the fake manager's realization added files; the assigned group's own files are untouched.
	after := snapshotTree(t, filepath.Join(f.root, f.parentCG))
	for name, body := range before {
		if !strings.HasPrefix(name, f.parentCG) || strings.Count(strings.TrimPrefix(name, f.parentCG+"/"), "/") != 0 {
			continue
		}
		if after[strings.TrimPrefix(name, f.parentCG+"/")] != body {
			t.Fatalf("%s changed", name)
		}
	}
}

func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			out[e.Name()] = string(b)
		}
	}
	return out
}

func TestEnsureChildSlicesRefusesWhenTheReadBackDiffersFromWhatWasSet(t *testing.T) {
	for name, tc := range map[string]struct {
		rewrite func(unit, derived string) string
		want    string
	}{
		"systemd realized another value": {func(unit, d string) string {
			return strings.Replace(d, "rbps=100000000", "rbps=7", 1)
		}, "batch-yolo-agent.slice"},
		"the limit never reached the cgroup": {func(unit, d string) string { return "259:0 rbps=max wbps=max riops=max wiops=max\n" }, "rbps"},
		"nothing at all":                     {func(unit, d string) string { return "" }, "io.max"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeUserManager(t, assignedFinite)
			f.ioMaxFor = tc.rewrite
			_, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "yolo check --no-build") {
				t.Fatalf("err = %v; want a refusal naming %q and the next step", err, tc.want)
			}
		})
	}
}

func TestEnsureChildSlicesRefusesAStaleFiniteDimensionNamingRevert(t *testing.T) {
	f := newFakeUserManager(t, assignedFinite)
	f.ioMaxFor = func(unit, d string) string {
		if unit == "batch-yolo-batch.slice" {
			return strings.Replace(d, "riops=max", "riops=5", 1)
		}
		return d
	}
	_, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
	if err == nil || !strings.Contains(err.Error(), "systemctl --user revert batch-yolo-batch.slice") {
		t.Fatalf("err = %v; want the stale ceiling refused naming systemctl --user revert", err)
	}
}

func TestEnsureChildSlicesRefusalsNameTheirStep(t *testing.T) {
	t.Run("set-property fails", func(t *testing.T) {
		f := newFakeUserManager(t, assignedFinite)
		f.failSetProp = errors.New("Failed to connect to user scope bus")
		_, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
		if err == nil || !strings.Contains(err.Error(), "set-property") || !strings.Contains(err.Error(), "user manager") || !strings.Contains(err.Error(), "yolo check --no-build") {
			t.Fatalf("err = %v", err)
		}
		for _, c := range f.mutating() {
			if c[2] == "start" {
				t.Fatalf("a child was started after set-property failed: %v", c)
			}
		}
	})
	t.Run("start fails", func(t *testing.T) {
		f := newFakeUserManager(t, assignedFinite)
		f.failStart = errors.New("Unit not found")
		_, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
		if err == nil || !strings.Contains(err.Error(), "start") || !strings.Contains(err.Error(), "yolo check --no-build") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no finite allowance", func(t *testing.T) {
		f := newFakeUserManager(t, "259:0 rbps=max wbps=max riops=max wiops=max\n")
		_, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
		if err == nil || !strings.Contains(err.Error(), "finite") || len(f.mutating()) != 0 {
			t.Fatalf("err = %v, writes = %v; want a refusal before any write", err, f.mutating())
		}
	})
	t.Run("io not delegated", func(t *testing.T) {
		f := newFakeUserManager(t, assignedFinite)
		if err := os.WriteFile(filepath.Join(f.root, f.parentCG, "cgroup.controllers"), []byte("memory pids\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
		if err == nil || !strings.Contains(err.Error(), "Delegate=") || len(f.mutating()) != 0 {
			t.Fatalf("err = %v, writes = %v", err, f.mutating())
		}
	})
	t.Run("not a slice", func(t *testing.T) {
		f := newFakeUserManager(t, assignedFinite)
		_, err := EnsureChildSlices(context.Background(), "app.service", f.paths(), 5, f.seams())
		if err == nil || !strings.Contains(err.Error(), "slice") || len(f.mutating()) != 0 {
			t.Fatalf("err = %v, writes = %v", err, f.mutating())
		}
	})
	t.Run("canceled", func(t *testing.T) {
		f := newFakeUserManager(t, assignedFinite)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := EnsureChildSlices(ctx, "batch-yolo.slice", f.paths(), 5, f.seams()); err == nil {
			t.Fatal("a canceled context was accepted")
		}
	})
}

func TestEnsureChildSlicesIsReusedByASecondLaunch(t *testing.T) {
	f := newFakeUserManager(t, assignedFinite)
	first, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureChildSlices(context.Background(), "batch-yolo.slice", f.paths(), 5, f.seams())
	if err != nil {
		t.Fatal(err)
	}
	if *first != *second {
		t.Fatalf("a second launch resolved other children: %+v vs %+v", first, second)
	}
	units := 0
	for unit := range f.realized {
		if strings.HasPrefix(unit, "batch-yolo-") {
			units++
		}
	}
	if units != 2 {
		t.Fatalf("realized children = %d, want exactly the one pair", units)
	}
}

func TestObserveMemberDirMapsTheProcCgroupLine(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	if err := os.MkdirAll(filepath.Join(proc, "42"), 0o755); err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(root, "cg")
	mountInfo := filepath.Join(root, "mountinfo")
	if err := os.WriteFile(mountInfo, []byte(fmt.Sprintf("29 23 0:26 / %s rw - cgroup2 cgroup rw\n", mount)), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(proc, "42", "cgroup"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("0::/user.slice/batch.slice/batch-yolo.slice/batch-yolo-agent.slice/run-p42.scope\n")
	dir, err := ObserveMemberDir(proc, mountInfo, 42)
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(mount, "user.slice/batch.slice/batch-yolo.slice/batch-yolo-agent.slice")
	if !InsideGroup(dir, agent) || InsideGroup(dir, agent+"-x") || InsideGroup(agent, dir) {
		t.Fatalf("membership %q vs %q", dir, agent)
	}
	write("12:cpu:/x\n0::/y\n")
	if _, err := ObserveMemberDir(proc, mountInfo, 42); err == nil {
		t.Fatal("a hybrid hierarchy was accepted as witnessed membership")
	}
	if _, err := ObserveMemberDir(proc, mountInfo, 43); err == nil {
		t.Fatal("a missing process was accepted")
	}
	_ = strconv.Itoa
}

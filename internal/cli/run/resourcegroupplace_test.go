package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
)

// rgFakeManager is a user manager over a fake cgroup tree. It is the seam the placement
// reaches systemctl and busctl through; no real systemd or cgroup is touched, and a nested
// jail cannot exercise real delegation, so the owner's host witness owes that.
type rgFakeManager struct {
	t       *testing.T
	root    string
	cg      string
	calls   [][]string
	started map[string]string // unit -> control group
	// member is where the launcher is until a transient scope's start job completes. The job
	// is asynchronous: busctl only queues it. While jobRelease is open the launcher stays
	// outside the agent child; the test closes it to complete the job late.
	member     string
	failBusctl error
	jobRelease chan struct{}
	neverMoves bool // the job completes but the pid stays where it was
}

func newRGFakeManager(t *testing.T) *rgFakeManager {
	t.Helper()
	root := canonicalTempDir(t)
	f := &rgFakeManager{t: t, root: root, cg: "/user.slice/batch.slice/batch-yolo.slice", started: map[string]string{}}
	f.started["batch-yolo.slice"] = f.cg
	dir := filepath.Join(root, f.cg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"io.max": "259:0 rbps=2000000000 wbps=1000000000 riops=max wiops=max\n", "cgroup.controllers": "io memory pids\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.member = f.cg + "/session-3.scope"
	return f
}

func (f *rgFakeManager) mountInfo() []byte {
	return []byte(fmt.Sprintf("29 23 0:26 / %s rw - cgroup2 cgroup rw\n", strings.NewReplacer(`\`, `\134`, " ", `\040`).Replace(f.root)))
}

func (f *rgFakeManager) query(argv []string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	joined := strings.Join(argv, " ")
	switch {
	case strings.Contains(joined, "--property=ControlGroup"):
		if argv[1] != "--user" {
			return "\n", nil
		}
		return f.started[argv[len(argv)-1]] + "\n", nil
	case argv[0] == "systemctl" && argv[2] == "set-property":
		return "", nil
	case argv[0] == "systemctl" && argv[2] == "start":
		for _, unit := range argv[3:] {
			cg := f.cg + "/" + unit
			f.started[unit] = cg
			dir := filepath.Join(f.root, cg)
			_ = os.MkdirAll(dir, 0o755)
			body := "259:0 rbps=100000000 wbps=50000000 riops=max wiops=max\n"
			if strings.Contains(unit, "-batch") {
				body = "259:0 rbps=1900000000 wbps=950000000 riops=max wiops=max\n"
			}
			_ = os.WriteFile(filepath.Join(dir, "io.max"), []byte(body), 0o644)
		}
		return "", nil
	case argv[0] == "busctl":
		if f.failBusctl != nil {
			return "", f.failBusctl
		}
		f.member = "pending"
		return "o \"/org/freedesktop/systemd1/job/1\"\n", nil
	}
	return "", fmt.Errorf("unexpected call %v", argv)
}

func (f *rgFakeManager) memberOf(int) (string, error) {
	if f.member == "pending" {
		if f.neverMoves {
			return filepath.Join(f.root, f.cg, "session-3.scope"), nil
		}
		if f.jobRelease != nil {
			select {
			case <-f.jobRelease:
			default:
				return filepath.Join(f.root, f.cg, "session-3.scope"), nil
			}
		}
		return filepath.Join(f.root, f.cg, "batch-yolo-agent.slice", "run-p77.scope"), nil
	}
	return filepath.Join(f.root, f.member), nil
}

func (f *rgFakeManager) placement() resourceGroupPlacement {
	return resourceGroupPlacement{
		IsLinux: true,
		Pid:     77,
		Observe: func(sel config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
			return resourcegroups.ResolveSelectedIOPaths(sel.Unit, f.query, f.mountInfo(), os.ReadFile)
		},
		Query:     f.query,
		MountInfo: f.mountInfo(),
		Member:    f.memberOf,
	}
}

var rgSelection = config.ResourceGroupSelection{Unit: "batch-yolo.slice", AgentPercent: 5, Source: "default"}

func (f *rgFakeManager) call(name string) []string {
	for _, c := range f.calls {
		if (c[0] == "busctl" && name == "busctl") || (len(c) > 2 && c[0] == "systemctl" && c[2] == name) {
			return c
		}
	}
	return nil
}

func TestPlacementCreatesChildSlicesAndMovesTheLauncherIntoTheAgentChild(t *testing.T) {
	f := newRGFakeManager(t)
	d := f.placement()
	out := d.place(rgSelection)
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	if out.Placed == nil || out.Placed.Child != "batch-yolo-agent.slice" || out.Placed.Unit != "batch-yolo.slice" {
		t.Fatalf("placed = %+v", out.Placed)
	}
	set := f.call("set-property")
	if set == nil || set[4] != "batch-yolo-agent.slice" || set[5] != "IOReadBandwidthMax=/dev/block/259:0 100000000" {
		t.Fatalf("the agent child's limit was not set through the user manager: %v", f.calls)
	}
	bus := strings.Join(f.call("busctl"), " ")
	for _, want := range []string{"--user call org.freedesktop.systemd1", "StartTransientUnit", "PIDs au 1 77", "Slice s batch-yolo-agent.slice"} {
		if !strings.Contains(bus, want) {
			t.Errorf("the placement call lacks %q: %s", want, bus)
		}
	}
	if !strings.HasSuffix(out.Placed.ChildDir, "batch-yolo-agent.slice") {
		t.Fatalf("child dir = %q", out.Placed.ChildDir)
	}
}

// The scope's start job completes late, released by the test; the launch keeps waiting on the
// condition (membership in the agent child) and succeeds. The sleep seam is the test's event: it
// blocks the poll until the test has seen the launch waiting, so nothing here is timed.
func TestPlacementWaitsForTheLauncherToLandInTheAgentChild(t *testing.T) {
	f := newRGFakeManager(t)
	f.jobRelease = make(chan struct{})
	waiting := make(chan struct{}, 1)
	d := f.placement()
	d.Sleep = func(time.Duration) {
		select {
		case waiting <- struct{}{}:
		default:
		}
		<-f.jobRelease // the launch is held until the job completes
	}
	done := make(chan resourceGroupOutcome, 1)
	go func() { done <- d.place(rgSelection) }()
	<-waiting // the launch read the membership, found it outside, and is waiting
	select {
	case out := <-done:
		t.Fatalf("placement finished before the job completed: %+v", out)
	default:
	}
	close(f.jobRelease)
	out := <-done
	if out.Err != nil || out.Placed == nil {
		t.Fatalf("a late job completion refused a launch where nothing was wrong: %+v", out)
	}
}

// The one generous guard: a launcher that never lands refuses naming the status command, judged
// by an injected clock that the poll's sleep advances, so no real time passes.
func TestPlacementRefusesAtTheGuardWhenTheLauncherNeverLands(t *testing.T) {
	f := newRGFakeManager(t)
	f.neverMoves = true
	d := f.placement()
	clock := time.Unix(0, 0)
	d.Now = func() time.Time { return clock }
	sleeps := 0
	d.Sleep = func(dur time.Duration) { sleeps++; clock = clock.Add(dur) }
	out := d.place(rgSelection)
	if out.Err == nil || out.Placed != nil || !strings.Contains(out.Err.Error(), "systemctl --user status batch-yolo-agent.slice") {
		t.Fatalf("outcome = %+v", out)
	}
	if clock.Sub(time.Unix(0, 0)) < placementGuard || sleeps < 2 {
		t.Fatalf("gave up after %s and %d sleeps, before the %s guard", clock.Sub(time.Unix(0, 0)), sleeps, placementGuard)
	}
}

func TestPlacementOfAnAttachReusesThePairAndMovesNothingWhenAlreadyInsideIt(t *testing.T) {
	f := newRGFakeManager(t)
	d := f.placement()
	if out := d.place(rgSelection); out.Err != nil { // the first launch makes the pair
		t.Fatal(out.Err)
	}
	f.calls = nil
	f.member = f.cg + "/batch-yolo-agent.slice/run-p9.scope" // the attacher is already in the agent share
	d = f.placement()
	out := d.place(rgSelection)
	if out.Err != nil || out.Placed == nil {
		t.Fatalf("an attach inside the agent child refused: %+v", out)
	}
	if f.call("busctl") != nil {
		t.Fatalf("a launcher already inside the agent child was moved again: %v", f.calls)
	}
	if len(f.started) != 3 { // the mapped slice plus exactly one pair
		t.Fatalf("units = %v, want one pair", f.started)
	}
}

func TestPlacementRefusalsNameTheirStep(t *testing.T) {
	t.Run("the user manager refuses the move", func(t *testing.T) {
		f := newRGFakeManager(t)
		f.failBusctl = errors.New("Failed to connect to bus")
		d := f.placement()
		out := d.place(rgSelection)
		if out.Err == nil || out.Placed != nil || !strings.Contains(out.Err.Error(), "busctl --user status") || !strings.Contains(out.Err.Error(), "yolo check --no-build") {
			t.Fatalf("outcome = %+v", out)
		}
	})
	t.Run("a non-slice unit", func(t *testing.T) {
		f := newRGFakeManager(t)
		d := f.placement()
		out := d.place(config.ResourceGroupSelection{Unit: "work.service", AgentPercent: 5})
		if out.Err == nil || !strings.Contains(out.Err.Error(), "not a slice") || len(f.calls) != 0 {
			t.Fatalf("outcome = %+v, calls = %v", out, f.calls)
		}
	})
	t.Run("io not delegated", func(t *testing.T) {
		f := newRGFakeManager(t)
		if err := os.WriteFile(filepath.Join(f.root, f.cg, "cgroup.controllers"), []byte("memory pids\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d := f.placement()
		out := d.place(rgSelection)
		if out.Err == nil || !strings.Contains(out.Err.Error(), "Delegate=") || f.call("set-property") != nil || f.call("busctl") != nil {
			t.Fatalf("outcome = %+v, calls = %v", out, f.calls)
		}
	})
	t.Run("the launcher cgroup cannot be read", func(t *testing.T) {
		f := newRGFakeManager(t)
		d := f.placement()
		d.Member = func(int) (string, error) { return "", errors.New("no such process") }
		out := d.place(rgSelection)
		if out.Err == nil || !strings.Contains(out.Err.Error(), "yolo check --no-build") {
			t.Fatalf("outcome = %+v", out)
		}
	})
	t.Run("in a jail", func(t *testing.T) {
		f := newRGFakeManager(t)
		d := f.placement()
		d.InJail = true
		if out := d.place(rgSelection); out.Err == nil || len(f.calls) != 0 {
			t.Fatalf("outcome = %+v, calls = %v", out, f.calls)
		}
	})
}

func TestUserManagerQueryCarriesStderrIntoTheError(t *testing.T) {
	q := userManagerQuery(func([]string, string, []string, time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 1, Stderr: "Failed to connect to bus: No medium found\x1b[2J"}
	})
	_, err := q([]string{"systemctl", "--user", "start", "x.slice"})
	if err == nil || !strings.Contains(err.Error(), "No medium found") || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("err = %v", err)
	}
}

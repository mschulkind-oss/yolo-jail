package run

// keeperscopeslice_test.go pins that a launch placed in a resource group's agent child slice puts
// the keeper's transient scope in that same slice, so the host services' I/O stays inside the
// group's io.max (docs/design/resource-groups-plan.md).

import (
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

// keeperScopeCall runs the keeper's scope move against a recording Exec and returns the busctl argv
// joined, and the returned log line.
func keeperScopeCall(t *testing.T, slice string) (call, line string) {
	t.Helper()
	var argv []string
	o := &Options{
		LookPath:   func(string) (string, bool) { return "/usr/bin/busctl", true },
		Getpid:     func() int { return 4242 },
		PathExists: func(string) bool { return false },
		Exec: func(a []string, _ string, _ []string, _ time.Duration) ExecResult {
			argv = a
			return ExecResult{Ran: true}
		},
	}
	_, line = o.moveKeeperIntoScope("yolo-rg", slice)
	return strings.Join(argv, " "), line
}

// TestAKeeperScopeNamesTheResourceGroupSliceOnlyWhenThePlanDoes: with the plan's slice the transient
// scope carries Slice s <agent slice> (three properties); without it the call is the two-property
// one it always was.
func TestAKeeperScopeNamesTheResourceGroupSliceOnlyWhenThePlanDoes(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("the scope move is Linux-only")
	}
	call, line := keeperScopeCall(t, "batch-yolo-agent.slice")
	if !strings.Contains(call, "fail 3 PIDs au 1 4242 Slice s batch-yolo-agent.slice Description s") {
		t.Errorf("the keeper scope does not carry the agent slice: %q", call)
	}
	if !strings.Contains(line, "batch-yolo-agent.slice") {
		t.Errorf("the log line does not name the slice: %q", line)
	}
	call, line = keeperScopeCall(t, "")
	if strings.Contains(call, "Slice") || !strings.Contains(call, "fail 2 PIDs au 1 4242 Description s") {
		t.Errorf("a launch in no resource group changed the keeper scope call: %q", call)
	}
	if strings.Contains(line, "slice") {
		t.Errorf("the log line names a slice with no resource group: %q", line)
	}
}

// TestTheLaunchPlanCarriesItsResourceGroupPlacement pins the call site: keeperPlanFor writes the
// slice the launch placed itself in, and none when it was placed in no group.
func TestTheLaunchPlanCarriesItsResourceGroupPlacement(t *testing.T) {
	for _, tc := range []struct {
		placed *placedResourceGroup
		want   string
	}{
		{&placedResourceGroup{Unit: "batch-yolo.slice", Child: "batch-yolo-agent.slice"}, "batch-yolo-agent.slice"},
		{nil, ""},
	} {
		o := &Options{Workspace: t.TempDir(), resourceGroupPlaced: tc.placed}
		fillDefaults(o)
		plan, err := o.keeperPlanFor(newConfig(), "podman", "jail", stagedPacks{}, nil, nil, nil, "", "", []string{"podman", "run"}, &assembleInput{})
		if err != nil {
			t.Fatal(err)
		}
		if plan.ResourceGroupSlice != tc.want {
			t.Errorf("plan slice %q, want %q", plan.ResourceGroupSlice, tc.want)
		}
	}
}

// TestAKeeperRunPassesItsPlansSliceToItsScopeMove pins keeper.run's call site: the slice the plan
// names reaches the busctl call the keeper makes first.
func TestAKeeperRunPassesItsPlansSliceToItsScopeMove(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("the scope move is Linux-only")
	}
	var session *sessionLock
	f := startKeeperFixtureWith(t, true, func(p *keeperPlan) {
		p.ResourceGroupSlice = "batch-yolo-agent.slice"
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	}, func(o *Options) {
		o.LookPath = func(name string) (string, bool) { return "/usr/bin/busctl", name == "busctl" }
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	f.jail.mu.Lock()
	calls := append([]string(nil), f.jail.calls...)
	f.jail.mu.Unlock()
	if len(calls) == 0 || !strings.Contains(calls[0], "Slice s batch-yolo-agent.slice") {
		t.Errorf("the keeper's scope move does not carry its plan's slice: %q", calls)
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
}

package cgd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeHost is a fake cgroup root and /proc under t.TempDir(), with the package
// roots pointed at it for the test's duration. cgd tests run serially.
type fakeHost struct {
	t         *testing.T
	root      string
	proc      string
	container string
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	root := t.TempDir()
	h := &fakeHost{t: t, root: root, proc: filepath.Join(root, "proc"), container: filepath.Join(root, "cg", "box.scope")}
	must(t, os.MkdirAll(h.container, 0o755))
	must(t, os.MkdirAll(h.proc, 0o755))
	for f, v := range map[string]string{"cgroup.controllers": "cpu memory pids", "cgroup.procs": "", "cgroup.subtree_control": ""} {
		must(t, os.WriteFile(filepath.Join(h.container, f), []byte(v), 0o644))
	}
	oldP, oldC := procRoot, cgroupRoot
	procRoot, cgroupRoot = h.proc, filepath.Join(root, "cg")
	t.Cleanup(func() { procRoot, cgroupRoot = oldP, oldC })
	return h
}

// process records pid as living in the host cgroup path cg.
func (h *fakeHost) process(pid int, cg string) {
	h.t.Helper()
	dir := filepath.Join(h.proc, strconv.Itoa(pid))
	must(h.t, os.MkdirAll(dir, 0o755))
	must(h.t, os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::"+cg+"\n"), 0o644))
}

func (h *fakeHost) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(h.container, rel))
	return string(b)
}

func (h *fakeHost) do(line string, caller Caller) (ok bool, errText string) {
	h.t.Helper()
	req, parsed := ParseRequest([]byte(line))
	if !parsed {
		h.t.Fatalf("request does not parse: %s", line)
	}
	resp := HandleCaller(req, h.container, caller)
	v, _ := resp.Get("ok")
	e, _ := resp.Get("error")
	s, _ := e.(string)
	return v == true, s
}

func TestWeightIsWrittenToTheJobGroupAndCPUIsDelegated(t *testing.T) {
	h := newFakeHost(t)
	if ok, e := h.do(`{"op":"create_and_join","name":"gates","weight":800}`, Caller{PID: 4242}); !ok {
		t.Fatalf("refused: %s", e)
	}
	if got := h.read("agent/gates/cpu.weight"); got != "800" {
		t.Errorf("cpu.weight = %q, want 800", got)
	}
	if got := h.read("agent/gates/cgroup.procs"); got != "4242" {
		t.Errorf("caller not moved in: %q", got)
	}
	if !strings.Contains(h.read("cgroup.subtree_control"), "+cpu") {
		t.Errorf("cpu controller not enabled in the parent's subtree_control: %q", h.read("cgroup.subtree_control"))
	}
	// A weight is not a limit: with no other flag nothing else is written.
	for _, f := range []string{"cpu.max", "memory.max", "pids.max"} {
		if _, err := os.Stat(filepath.Join(h.container, "agent/gates", f)); err == nil {
			t.Errorf("%s written for a weight-only request", f)
		}
	}
}

func TestWeightCombinesWithTheHardLimits(t *testing.T) {
	h := newFakeHost(t)
	if ok, e := h.do(`{"op":"create_and_join","name":"both","weight":10,"pids":50}`, Caller{PID: 9}); !ok {
		t.Fatalf("refused: %s", e)
	}
	if h.read("agent/both/cpu.weight") != "10" || h.read("agent/both/pids.max") != "50" {
		t.Errorf("weight %q pids %q", h.read("agent/both/cpu.weight"), h.read("agent/both/pids.max"))
	}
}

func TestWeightOutsideTheKernelRangeIsRefusedAndCreatesNothing(t *testing.T) {
	h := newFakeHost(t)
	for _, w := range []string{"0", "-5", "10001", `"abc"`} {
		ok, e := h.do(`{"op":"create_and_join","name":"w","weight":`+w+`}`, Caller{PID: 9})
		if ok || !strings.Contains(e, "1-10000") {
			t.Errorf("weight %s: ok=%v err=%q, want a refusal naming 1-10000", w, ok, e)
		}
	}
	if _, err := os.Stat(filepath.Join(h.container, "agent")); err == nil {
		t.Error("a refused weight still created the agent hierarchy")
	}
	for _, w := range []string{"1", "10000"} {
		if ok, e := h.do(`{"op":"create_and_join","name":"edge`+w+`","weight":`+w+`}`, Caller{PID: 9}); !ok {
			t.Errorf("weight %s is in range but refused: %s", w, e)
		}
	}
}

func TestASecondCallWithTheSameNameJoinsAndTheLaterWeightWins(t *testing.T) {
	h := newFakeHost(t)
	if ok, e := h.do(`{"op":"create_and_join","name":"g","weight":100}`, Caller{PID: 11}); !ok {
		t.Fatal(e)
	}
	// Same name, no weight: joins, leaves the weight alone.
	if ok, e := h.do(`{"op":"create_and_join","name":"g"}`, Caller{PID: 12}); !ok {
		t.Fatalf("rejoin of an existing name failed: %s", e)
	}
	if got := h.read("agent/g/cpu.weight"); got != "100" {
		t.Errorf("a rejoin without --weight changed the weight to %q", got)
	}
	if got := h.read("agent/g/cgroup.procs"); got != "12" {
		t.Errorf("second caller not moved in: %q", got)
	}
	// Same name, different weight: updates (documented: last writer wins).
	if ok, e := h.do(`{"op":"create_and_join","name":"g","weight":900}`, Caller{PID: 13}); !ok {
		t.Fatal(e)
	}
	if got := h.read("agent/g/cpu.weight"); got != "900" {
		t.Errorf("cpu.weight = %q, want the later 900", got)
	}
}

func TestJoinMovesAProcessInsideTheContainer(t *testing.T) {
	h := newFakeHost(t)
	h.process(700, "/box.scope")             // container root
	h.process(701, "/box.scope/agent/other") // a descendant group
	for _, pid := range []int{700, 701} {
		line := `{"op":"join","name":"gates","weight":300}`
		if ok, e := h.do(line, Caller{PID: 1, Target: &Target{PID: pid}}); !ok {
			t.Fatalf("pid %d inside the container was refused: %s", pid, e)
		}
		if got := h.read("agent/gates/cgroup.procs"); got != strconv.Itoa(pid) {
			t.Errorf("cgroup.procs = %q, want %d", got, pid)
		}
	}
	if got := h.read("agent/gates/cpu.weight"); got != "300" {
		t.Errorf("cpu.weight = %q, want 300", got)
	}
}

func TestJoinRefusesAProcessOutsideTheContainerAndTouchesNothing(t *testing.T) {
	h := newFakeHost(t)
	h.process(800, "/user.slice/session.scope")
	h.process(801, "/box.scope-evil")         // shares a string prefix, is not a descendant
	h.process(802, "/")                       // the host root
	h.process(803, "/box.scope/../elsewhere") // traversal in the cgroup path
	for _, pid := range []int{800, 801, 802, 803, 999 /* no /proc entry */} {
		ok, e := h.do(`{"op":"join","name":"gates","weight":300}`, Caller{PID: 1, Target: &Target{PID: pid}})
		if ok || !strings.Contains(e, "Refusing to join") {
			t.Errorf("pid %d: ok=%v err=%q, want a refusal", pid, ok, e)
		}
	}
	if _, err := os.Stat(filepath.Join(h.container, "agent")); err == nil {
		t.Error("a refused join still created cgroups")
	}
}

func TestJoinNeedsAPidfdTarget(t *testing.T) {
	h := newFakeHost(t)
	h.process(700, "/box.scope")
	// A pid smuggled in the request body is never believed.
	ok, e := h.do(`{"op":"join","name":"g","pid":700}`, Caller{PID: 1})
	if ok || !strings.Contains(e, "pidfd") {
		t.Errorf("ok=%v err=%q, want a refusal naming the pidfd", ok, e)
	}
}

func TestJoinRefusesAProcessThatWasReapedBeforeTheWrite(t *testing.T) {
	h := newFakeHost(t)
	h.process(700, "/box.scope")
	ok, e := h.do(`{"op":"join","name":"g"}`, Caller{PID: 1, Target: &Target{PID: 700, Alive: func() bool { return false }}})
	if ok || !strings.Contains(e, "exited") {
		t.Errorf("ok=%v err=%q, want an exited refusal", ok, e)
	}
	if got := h.read("agent/g/cgroup.procs"); got != "" {
		t.Errorf("a reaped target was written: %q", got)
	}
}

func TestJoinAuditEventNamesTheTarget(t *testing.T) {
	line := AuditEvent{PeerPID: 5, Op: "join", Cgroup: "c", Target: 700, OK: true}.line()
	if !strings.Contains(line, "target=700") {
		t.Errorf("audit line lacks the target: %q", line)
	}
	if strings.Contains((AuditEvent{PeerPID: 5, Op: "status"}).line(), "target=") {
		t.Error("a non-join event names a target")
	}
}

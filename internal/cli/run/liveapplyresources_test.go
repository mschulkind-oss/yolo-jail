package run

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// liveapplyresources_test.go pins LA-S4c (docs/design/live-config-apply.md): a changed
// resources.cpus, .memory or .pids_limit reaches a running podman jail through `podman update`,
// and everything else under resources keeps the restart, saying why.

// resourcesFixture is applyFixture with a fake `podman update` that records each argv and
// answers with fail (empty: success), and a jail whose cgroup files then show what the last
// update set, unless shows overrides a file's content.
func resourcesFixture(t *testing.T, boot, now, fail string) (*Options, *strings.Builder, *[][]string) {
	o, buf, updates, _ := resourcesFixtureShowing(t, boot, now, fail)
	return o, buf, updates
}

func resourcesFixtureShowing(t *testing.T, boot, now, fail string) (*Options, *strings.Builder, *[][]string, map[string]string) {
	t.Helper()
	o, buf := applyFixture(t, boot, now, applyJail{running: true})
	o.PathExists = func(string) bool { return false } // the host, not a container
	var updates [][]string
	shows := map[string]string{}
	cgroup := map[string]string{}
	inner := o.Exec
	o.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if len(argv) > 1 && argv[0] == "podman" && argv[1] == "update" {
			updates = append(updates, argv)
			if fail != "" {
				return ExecResult{Ran: true, RC: 125, Stderr: fail}
			}
			switch argv[2] {
			case "--cpus":
				cgroup["/sys/fs/cgroup/cpu.max"] = argv[3] + "00000 100000"
			case "--memory":
				b, _ := memoryBytes(argv[3])
				cgroup["/sys/fs/cgroup/memory.max"] = strconv.FormatInt(b, 10)
			case "--pids-limit":
				cgroup["/sys/fs/cgroup/pids.max"] = argv[3]
			}
			return ExecResult{Ran: true}
		}
		if len(argv) == 6 && argv[0] == "podman" && argv[1] == "exec" && argv[2] == "--detach-keys=" && argv[4] == "cat" {
			argv = argv[1:]
			if v, ok := shows[argv[4]]; ok {
				return ExecResult{Ran: true, Stdout: v + "\n"}
			}
			if v, ok := cgroup[argv[4]]; ok {
				return ExecResult{Ran: true, Stdout: v + "\n"}
			}
			return ExecResult{Ran: true, RC: 1, Stderr: "No such file or directory"}
		}
		return inner(argv, dir, env, timeout)
	}
	answer(o, "y")
	return o, buf, &updates, shows
}

// The changed limits go to `podman update` with the values the launch would pass now, each is
// applied now and folded alone, and nothing is left pending.
func TestConfigApplyUpdatesResourceLimitsLive(t *testing.T) {
	o, buf, updates := resourcesFixture(t, `{"resources":{"cpus":2,"pids_limit":100}}`,
		`{"resources":{"cpus":4,"memory":"8g","pids_limit":100}}`, "")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, line := range []string{"+ machine.resources.memory: applied now", "~ machine.resources.cpus: applied now"} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q:\n%s", line, out)
		}
	}
	cname := o.jailName()
	want := [][]string{{"podman", "update", "--cpus", "4", cname}, {"podman", "update", "--memory", "8g", cname}}
	if !slices.EqualFunc(*updates, want, slices.Equal) {
		t.Errorf("podman update calls = %v, want %v", *updates, want)
	}
	if _, drifted, ok, err := config.WorkspaceConfigDrift(o.Workspace, "podman-self"); err != nil || !ok || drifted {
		t.Errorf("an applied limit was not folded: drifted=%v ok=%v err=%v", drifted, ok, err)
	}
}

// io, a dropped cpus or memory limit, and a failed update each wait for the restart with their
// reason; a live change beside them is not held back, and the pending ones keep drift reporting.
func TestConfigApplyResourcesThatCannotChangeLiveWait(t *testing.T) {
	o, buf, updates := resourcesFixture(t, `{"resources":{"cpus":2,"memory":"4g","io":"normal"}}`,
		`{"resources":{"cpus":3,"io":"low"}}`, "")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, line := range []string{
		"~ machine.resources.cpus: applied now",
		"~ machine.resources.io: at next restart (yolo stop, then yolo) — the disk I/O priority is set",
		"- machine.resources.memory: at next restart (yolo stop, then yolo) — a removed limit is lifted when the jail starts",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q:\n%s", line, out)
		}
	}
	if len(*updates) != 1 {
		t.Errorf("want only the cpus update, got %v", *updates)
	}
	boot, _, _ := config.ReadWorkspaceBootBaseline(o.Workspace, "podman-self")
	if v, _, _ := config.ConfigValueAtPath(boot, "machine.resources.cpus"); jsonNum(v) != "3" {
		t.Errorf("the applied cpus change was not folded: %v", v)
	}
	if v, _, _ := config.ConfigValueAtPath(boot, "machine.resources.io"); v != "normal" {
		t.Errorf("the restart-owed io change was folded: %v", v)
	}

	o, buf, _ = resourcesFixture(t, `{"resources":{"cpus":2}}`, `{"resources":{"cpus":4}}`,
		"Error: OCI runtime error: cpu controller not delegated")
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("a failed update: rc = %d, want 3\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "machine.resources.cpus: at next restart (yolo stop, then yolo) — podman update failed: Error: OCI runtime error: cpu controller not delegated") {
		t.Errorf("a failed update must wait and carry podman's words:\n%s", buf.String())
	}
	if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace, "podman-self"); !drifted {
		t.Error("a failed update was folded into the effective-config record")
	}
}

// THE EXIT IS NOT THE WITNESS: an update podman reports as done that the jail's cgroup does
// not show (measured in a nested jail: memory.max unwritten, the recorded limit moved anyway)
// waits for the restart and says what the cgroup reads.
func TestConfigApplyResourcesTheCgroupDoesNotShowWait(t *testing.T) {
	o, buf, _, shows := resourcesFixtureShowing(t, `{"resources":{"memory":"4g"}}`, `{"resources":{"memory":"8g"}}`, "")
	shows["/sys/fs/cgroup/memory.max"] = "max"
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "machine.resources.memory: at next restart (yolo stop, then yolo) — podman update ran, but the jail's /sys/fs/cgroup/memory.max reads max, not 8g") {
		t.Errorf("an unshown limit must wait and say what the cgroup reads:\n%s", buf.String())
	}
	if _, drifted, _, _ := config.WorkspaceConfigDrift(o.Workspace, "podman-self"); !drifted {
		t.Error("an unconfirmed update was folded")
	}
}

func TestCgroupShowsLimit(t *testing.T) {
	for _, c := range []struct {
		flag, value, content string
		want                 bool
	}{
		{"--cpus", "1.5", "150000 100000", true},
		{"--cpus", "2", "max 100000", false},
		{"--memory", "512m", "536870912", true},
		{"--memory", "1g", "1073737728", true}, // rounded down to a page
		{"--memory", "1g", "max", false},
		{"--pids-limit", "100", "100", true},
		{"--pids-limit", "100", "max", false},
	} {
		if got := cgroupShowsLimit(c.flag, c.value, c.content); got != c.want {
			t.Errorf("cgroupShowsLimit(%s %s, %q) = %v, want %v", c.flag, c.value, c.content, got, c.want)
		}
	}
}

// A jail started from inside a container (podman-in-podman, cgroups disabled) runs no update,
// and says why.
func TestConfigApplyResourcesInANestedJailWait(t *testing.T) {
	o, buf, updates := resourcesFixture(t, `{"resources":{"cpus":2}}`, `{"resources":{"cpus":4}}`, "")
	o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if len(*updates) != 0 {
		t.Errorf("a nested jail ran podman update: %v", *updates)
	}
	if !strings.Contains(buf.String(), "cgroups disabled") {
		t.Errorf("the line must say why:\n%s", buf.String())
	}
}

// The plan half, per backend: Apple Container and an unprovable podman wait, macos-user reaches
// the next session, and only a live podman plans an update step.
func TestTheResourcesPlanPerBackend(t *testing.T) {
	boot := mapOf(t, `{"machine":{"resources":{"cpus":2}}}`)
	cur := mapOf(t, `{"machine":{"resources":{"cpus":4}}}`)
	limits := []resourceLimit{{flag: "--cpus", key: "cpus", value: "4"}}
	for _, c := range []struct {
		rt   string
		live bool
		want applyOutcome
		kind changeKind
	}{
		{"podman", true, outcomeAppliedNow, changeResources},
		{"podman", false, outcomeNextRestart, changeKey},
		{"container", false, outcomeNextRestart, changeKey},
		{"macos-user", false, outcomeNextSession, changeKey},
	} {
		plan := buildApplyPlan(applyPlanInput{rt: c.rt, running: true, bootOK: true, boot: boot, current: cur,
			resourcesLive: c.live, resourceLimits: limits})
		if len(plan) != 1 || plan[0].outcome != c.want || plan[0].kind != c.kind || plan[0].key != "machine.resources.cpus" {
			t.Errorf("%s live=%v: plan = %+v", c.rt, c.live, plan)
		}
	}
}

func jsonNum(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(pyStrCoerce(v))
}

package macosuser

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// resourcesLine drives the real buildPlan and returns its "resources are NOT enforced"
// line, or "" when it printed none.
func resourcesLine(t *testing.T, res *jsonx.OrderedMap) string {
	t.Helper()
	return planOutputLine(t, res, "resources are NOT enforced on macos-user")
}

// planOutputLine drives the real buildPlan over a config whose resources block is res and
// returns the first line it printed containing marker, or "".
func planOutputLine(t *testing.T, res *jsonx.OrderedMap, marker string) string {
	t.Helper()
	var buf bytes.Buffer
	deps := mockDeps(nil)
	deps.Out = &buf
	opts := newOpts("/Users/Shared/proj")
	if res != nil {
		opts.Config.Set("resources", res)
	}
	buildPlan(deps, opts, nil)
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

// resourcesOf builds a resources block from key/value pairs. A Go number is round-tripped
// through the config decoder, so it arrives as the type a real yolo-jail.jsonc gives it.
func resourcesOf(pairs ...any) *jsonx.OrderedMap {
	res := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		v := pairs[i+1]
		switch v.(type) {
		case int, float64:
			d, err := jsonx.Decode([]byte(fmt.Sprintf(`{"v": %v}`, v)))
			if err != nil {
				panic(err)
			}
			v, _ = d.(*jsonx.OrderedMap).Get("v")
		}
		res.Set(pairs[i].(string), v)
	}
	return res
}

// TestMacosUserResourcesLineNamesOnlyWhatIsIgnored: since build step 5 and the memory guard,
// the "NOT enforced" line names only what this backend reads and does nothing with. `io` is
// applied (or makes no call), `cpus` is honored cooperatively and `memory` by the sampled
// guard, each with a line of its own; pids_limit is the one left, and a block holding nothing
// else prints no line at all (IO-D8's rule for a normal io, extended to the three).
func TestMacosUserResourcesLineNamesOnlyWhatIsIgnored(t *testing.T) {
	for _, io := range []any{"normal", nil, jsonx.NewOrderedMap(), "low", "idle"} {
		if line := resourcesLine(t, resourcesOf("io", io)); line != "" {
			t.Errorf("io=%v alone printed %q, want no line", io, line)
		}
	}
	if line := resourcesLine(t, resourcesOf("io", "low", "cpus", 2, "memory", "8g")); line != "" {
		t.Errorf("io, cpus and memory are all acted on, yet the line says %q", line)
	}
	line := resourcesLine(t, resourcesOf("io", "idle", "cpus", 2, "memory", "8g", "pids_limit", 100))
	if !strings.Contains(line, "— pids_limit.") {
		t.Errorf("pids_limit must still be named, as one key: %q", line)
	}
	for _, k := range []string{"io", "cpus", "memory"} {
		if strings.Contains(line, k+",") || strings.Contains(line, ", "+k) {
			t.Errorf("the line names %s, which this backend acts on: %q", k, line)
		}
	}
	// Two keys take the plural: a cpus neither reader can use is named beside pids_limit.
	if line := resourcesLine(t, resourcesOf("cpus", "many", "pids_limit", 100)); !strings.Contains(line, "— cpus, pids_limit.") {
		t.Errorf("two ignored keys: %q", line)
	}
}

// TestTheCPUDefaultsAreCappedAtTheHostsCPUs: each CooperativeCPUVars variable defaults, unset,
// to this Mac's CPU count, so a declared count above it would RAISE parallelism (GOMAXPROCS=16
// on a 4-CPU Mac runs 16 Ps), which the one rule that picks them forbids. Above the count the
// four are the count, and the disclosure says why; at or below it they are the declaration; a
// count the seam cannot give caps nothing.
func TestTheCPUDefaultsAreCappedAtTheHostsCPUs(t *testing.T) {
	run := func(cpus any, host func() int) (RunPlan, string) {
		var buf bytes.Buffer
		deps := mockDeps(nil)
		deps.Out = &buf
		deps.HostCPUs = host
		opts := newOpts("/Users/Shared/proj")
		opts.Config.Set("resources", resourcesOf("cpus", cpus))
		return buildPlan(deps, opts, nil), buf.String()
	}
	four := func() int { return 4 }
	plan, out := run(16, four)
	for _, k := range CooperativeCPUVars {
		if v, _ := sandboxEnvFileValue(plan.EnvFileContent, k); v != "4" {
			t.Errorf("cpus 16 on a 4-CPU host: %s = %q, want 4", k, v)
		}
	}
	if !strings.Contains(out, "resources.cpus (16) is honored cooperatively") ||
		!strings.Contains(out, "GOMAXPROCS=4") || !strings.Contains(out, "capped at this Mac's 4 CPUs") {
		t.Errorf("a capped count must be disclosed with the cap:\n%s", out)
	}
	for _, tc := range []struct {
		cpus any
		host func() int
		want string
	}{{3, four, "3"}, {4, four, "4"}, {16, nil, "16"}, {16, func() int { return 0 }, "16"}} {
		plan, out := run(tc.cpus, tc.host)
		if v, _ := sandboxEnvFileValue(plan.EnvFileContent, "GOMAXPROCS"); v != tc.want {
			t.Errorf("cpus %v: GOMAXPROCS = %q, want %s", tc.cpus, v, tc.want)
		}
		if strings.Contains(out, "capped at") {
			t.Errorf("cpus %v: an uncapped count says it was capped:\n%s", tc.cpus, out)
		}
	}
}

// TestMacosUserDisclosesTheCooperativeCPUsAndTheSampledMemory: each acted-on key says how far
// it goes, at launch and in a dry run — and only when declared.
func TestMacosUserDisclosesTheCooperativeCPUsAndTheSampledMemory(t *testing.T) {
	cpus := planOutputLine(t, resourcesOf("cpus", 2.5), "resources.cpus")
	for _, want := range []string{"(3) is honored cooperatively", "GOMAXPROCS=3", "CARGO_BUILD_JOBS=3",
		"RAYON_NUM_THREADS=3", "OMP_NUM_THREADS=3", "a program that ignores them is not limited"} {
		if !strings.Contains(cpus, want) {
			t.Errorf("the cpus line lacks %q: %q", want, cpus)
		}
	}
	mem := planOutputLine(t, resourcesOf("memory", "256m"), "resources.memory")
	for _, want := range []string{"(256m) is guarded by sampling", "not enforced by the kernel",
		"every 2s", "stops the largest process", "can kill that process", "leaves the session's process tree"} {
		if !strings.Contains(mem, want) {
			t.Errorf("the memory line lacks %q: %q", want, mem)
		}
	}
	if l := planOutputLine(t, resourcesOf("pids_limit", 10), "resources.cpus"); l != "" {
		t.Errorf("undeclared cpus printed %q", l)
	}
	if l := planOutputLine(t, resourcesOf("pids_limit", 10), "resources.memory"); l != "" {
		t.Errorf("undeclared memory printed %q", l)
	}
}

// TestCooperativeCPUsRoundsUp: every variable takes a whole count, and rounding a fractional
// cap down would be a stricter cap than declared.
func TestCooperativeCPUsRoundsUp(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int
		ok   bool
	}{
		{2, 2, true}, {2.5, 3, true}, {0.5, 1, true}, {"4", 4, true}, {" 1.2 ", 2, true}, {true, 1, true},
		{nil, 0, false}, {false, 0, false}, {0, 0, false}, {-1, 0, false}, {"many", 0, false},
	} {
		n, ok := CooperativeCPUs(resourcesOf("cpus", tc.v))
		if n != tc.want || ok != tc.ok {
			t.Errorf("cpus %v = %d %v, want %d %v", tc.v, n, ok, tc.want, tc.ok)
		}
	}
}

// TestTheCPUDefaultsReachTheEnvFileAndYieldToTheUser: the four variables are in the session
// env file at ceil(cpus), none of the ones that would RAISE a serial build is, and a value the
// user's own layers set wins — the channel (which carries env_sources last) and sandbox_env.
func TestTheCPUDefaultsReachTheEnvFileAndYieldToTheUser(t *testing.T) {
	opts := newOpts("/Users/Shared/proj")
	opts.Config.Set("resources", resourcesOf("cpus", 1.5))
	plan := buildPlan(mockDeps(nil), opts, nil)
	for _, k := range CooperativeCPUVars {
		if v, ok := sandboxEnvFileValue(plan.EnvFileContent, k); !ok || v != "2" {
			t.Errorf("%s = %q (%v) in the env file, want 2", k, v, ok)
		}
	}
	for _, k := range []string{"MAKEFLAGS", "CMAKE_BUILD_PARALLEL_LEVEL"} {
		if _, ok := sandboxEnvFileValue(plan.EnvFileContent, k); ok {
			t.Errorf("%s is set; make's default is one job, so it would raise a serial build", k)
		}
	}
	if plan.CooperativeCPUs != 2 {
		t.Errorf("plan.CooperativeCPUs = %d, want 2", plan.CooperativeCPUs)
	}

	opts.PackEnv = resourcesOf("CARGO_BUILD_JOBS", "1")
	opts.SandboxEnv = resourcesOf("GOMAXPROCS", "8")
	var buf bytes.Buffer
	deps := mockDeps(nil)
	deps.Out = &buf
	plan = buildPlan(deps, opts, nil)
	if v, _ := sandboxEnvFileValue(plan.EnvFileContent, "CARGO_BUILD_JOBS"); v != "1" {
		t.Errorf("the channel's CARGO_BUILD_JOBS lost to the default: %q", v)
	}
	if v, _ := sandboxEnvFileValue(plan.EnvFileContent, "GOMAXPROCS"); v != "8" {
		t.Errorf("sandbox_env's GOMAXPROCS lost to the default: %q", v)
	}
	if !strings.Contains(buf.String(), "GOMAXPROCS=8, CARGO_BUILD_JOBS=1, RAYON_NUM_THREADS=2") {
		t.Errorf("the disclosure does not report the values that won:\n%s", buf.String())
	}

	// Undeclared, nothing is set.
	plan = buildPlan(mockDeps(nil), newOpts("/Users/Shared/proj"), nil)
	for _, k := range CooperativeCPUVars {
		if _, ok := sandboxEnvFileValue(plan.EnvFileContent, k); ok {
			t.Errorf("%s set with no resources.cpus", k)
		}
	}
}

// launchPlan builds a real plan with resources res and returns it.
func launchPlan(t *testing.T, res *jsonx.OrderedMap) RunPlan {
	t.Helper()
	opts := newOpts("/Users/Shared/proj")
	if res != nil {
		opts.Config.Set("resources", res)
	}
	return buildPlan(mockDeps(nil), opts, nil)
}

// TestTheMemoryGuardRunsInsideTheSessionAndOnlyWhenDeclared: undeclared, the launch argv is
// the argv of a launch that never heard of a guard, byte for byte; declared, the staged yolo
// runs the guard after the env-file reader and before the shell that execs the agent, and the
// plan's invariants hold — and fire when the guard is lost.
func TestTheMemoryGuardRunsInsideTheSessionAndOnlyWhenDeclared(t *testing.T) {
	plain := launchPlan(t, resourcesOf("cpus", 2))
	direct := LaunchArgv([]string{"claude"}, plain.ProfilePath, plain.EnvFile, plain.Workspace, "", "", plain.DarwinPathPrefix)
	if !slices.Equal(plain.LaunchArgv, direct) {
		t.Errorf("with no resources.memory the launch argv moved:\n got %q\nwant %q", plain.LaunchArgv, direct)
	}
	if slices.Contains(plain.LaunchArgv, SessionGuardVerb) {
		t.Errorf("an undeclared guard reached the argv: %q", plain.LaunchArgv)
	}

	guarded := launchPlan(t, resourcesOf("memory", "256m"))
	argv := guarded.LaunchArgv
	words := append(SessionGuard{MemoryBytes: 256 << 20}.Argv(guarded.StagedYolo), "/bin/zsh", "-c")
	if !containsArgRun(argv, words) {
		t.Fatalf("the guard is not right before the inner shell: %q", argv)
	}
	reader, guard, shell := slices.Index(argv, sandboxEnvReader), slices.Index(argv, guarded.StagedYolo),
		slices.Index(argv, "/bin/zsh")
	sandbox := slices.Index(argv, "/usr/bin/sandbox-exec")
	if !(sandbox < reader && reader < guard && guard < shell) {
		t.Errorf("order sandbox-exec %d < reader %d < guard %d < shell %d does not hold: %q",
			sandbox, reader, guard, shell, argv)
	}
	if !strings.HasPrefix(argv[len(argv)-1], "cd '/Users/Shared/proj' && exec 'claude'") {
		t.Errorf("the inner shell changed: %q", argv[len(argv)-1])
	}
	if problems := PlanInvariants(guarded); len(problems) > 0 {
		t.Errorf("a guarded plan violates its invariants: %v", problems)
	}

	// The invariant is what fails when the call site loses the guard.
	broken := guarded
	broken.LaunchArgv = plain.LaunchArgv
	if p := strings.Join(PlanInvariants(broken), "\n"); !strings.Contains(p, "resources.memory is declared") {
		t.Errorf("a declared guard missing from the argv passed the invariants: %q", p)
	}
	stray := plain
	stray.LaunchArgv = argv
	if p := strings.Join(PlanInvariants(stray), "\n"); !strings.Contains(p, "not declared") {
		t.Errorf("a guard with no declaration passed the invariants: %q", p)
	}
}

// diskPolicyRun drives the real RunMacosUser over a config whose resources block is res,
// returning the recorded calls and the output.
func diskPolicyRun(t *testing.T, res *jsonx.OrderedMap, mutate func(*Deps)) ([]string, string, int) {
	t.Helper()
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if mutate != nil {
		mutate(&d)
	}
	opts := newOpts("/Users/Shared/yolo/proj")
	if res != nil {
		opts.Config.Set("resources", res)
	}
	rc := RunMacosUser(d, opts)
	return rec, buf.String(), rc
}

func recIndex(rec []string, pred func(string) bool) int {
	for i, r := range rec {
		if pred(r) {
			return i
		}
	}
	return -1
}

// TestTheLauncherSetsTheDiskPolicyBeforeTheBootstrap is build step 5's call-site pin
// (io-priority.md §5.5, IO-D7): a declared priority is set on the launcher as its macOS policy
// BEFORE the bootstrap and the agent, which inherit it, and silently when it holds.
func TestTheLauncherSetsTheDiskPolicyBeforeTheBootstrap(t *testing.T) {
	for _, tc := range []struct {
		io   any
		want string
	}{{"low", "iopol:4"}, {"idle", "iopol:3"}, {resourcesOf("priority", "idle"), "iopol:3"}} {
		rec, out, rc := diskPolicyRun(t, resourcesOf("io", tc.io), nil)
		if rc != 42 {
			t.Fatalf("io=%v: rc = %d, want the agent's 42\n%s", tc.io, rc, out)
		}
		set := recIndex(rec, func(r string) bool { return strings.HasPrefix(r, "iopol:") })
		boot := recIndex(rec, func(r string) bool { return strings.Contains(r, "internal darwin-bootstrap") })
		proxy := recIndex(rec, func(r string) bool { return strings.HasPrefix(r, "proxy:") })
		if set < 0 || rec[set] != tc.want {
			t.Errorf("io=%v: the launcher set %v, want %s:\n%s", tc.io, rec, tc.want, strings.Join(rec, "\n"))
			continue
		}
		if boot < 0 || proxy < 0 || set > boot || set > proxy {
			t.Errorf("io=%v: the policy is set at %d, after the bootstrap (%d) or the agent (%d)",
				tc.io, set, boot, proxy)
		}
		if strings.Contains(out, "resources.io") {
			t.Errorf("io=%v: a policy that held printed a line:\n%s", tc.io, out)
		}
	}
}

// TestNoDiskPolicyIsSetForAnUndeclaredPriority: "normal", null, {} and an absent key make no
// call, and neither does a dry run, which executes nothing.
func TestNoDiskPolicyIsSetForAnUndeclaredPriority(t *testing.T) {
	for _, res := range []*jsonx.OrderedMap{nil, resourcesOf("io", "normal"), resourcesOf("io", nil),
		resourcesOf("io", jsonx.NewOrderedMap()), resourcesOf("memory", "8g")} {
		rec, _, _ := diskPolicyRun(t, res, nil)
		if i := recIndex(rec, func(r string) bool { return strings.HasPrefix(r, "iopol:") }); i >= 0 {
			t.Errorf("resources %v set a policy: %s", res, rec[i])
		}
	}
	var rec []string
	d := mockDeps(&rec)
	d.IsMacOS = func() bool { return false }
	d.Out = &bytes.Buffer{}
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.Config.Set("resources", resourcesOf("io", "idle"))
	opts.DryRun = true
	if rc := RunMacosUser(d, opts); rc != 0 || len(rec) != 0 {
		t.Errorf("a dry run rc %d made calls: %v", rc, rec)
	}
}

// TestAFailedDiskPolicyWarnsAndLaunches: IO-D4 on this backend — a set that fails, a read-back
// that disagrees, or a build with no call wired each print ONE warning naming resources.io,
// and the launch goes on. A read-back that itself fails is a dim note: the set succeeded.
func TestAFailedDiskPolicyWarnsAndLaunches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Deps)
		want   string
	}{
		{"set fails", func(d *Deps) {
			d.SetDiskIOPolicy = func(int) error { return errors.New("operation not permitted") }
		}, "setiopolicy_np(IOPOL_UTILITY) failed: operation not permitted"},
		{"reads back another", func(d *Deps) {
			d.DiskIOPolicy = func() (int, error) { return 0, nil }
		}, "reads back as IOPOL_DEFAULT"},
		{"no call wired", func(d *Deps) { d.SetDiskIOPolicy = nil }, "no setiopolicy_np call wired"},
	} {
		_, out, rc := diskPolicyRun(t, resourcesOf("io", "low"), tc.mutate)
		if rc != 42 {
			t.Errorf("%s: rc = %d, want the launch to go on to the agent's 42", tc.name, rc)
		}
		if strings.Count(out, `Warning: resources.io "low" was not applied on macos-user`) != 1 ||
			!strings.Contains(out, tc.want) || !strings.Contains(out, "Remove resources.io") {
			t.Errorf("%s: want one warning saying %q and the next step:\n%s", tc.name, tc.want, out)
		}
	}
	_, out, rc := diskPolicyRun(t, resourcesOf("io", "idle"), func(d *Deps) {
		d.DiskIOPolicy = func() (int, error) { return 0, errors.New("EPERM") }
	})
	if rc != 42 || strings.Contains(out, "Warning: resources.io") || !strings.Contains(out, "reading it back failed (EPERM)") {
		t.Errorf("a failed read-back after a good set: rc %d\n%s", rc, out)
	}
}

// TestThePlanNamesTheDeclaredResources: the dry run says what each declared key becomes, and
// says nothing for an undeclared one.
func TestThePlanNamesTheDeclaredResources(t *testing.T) {
	render := func(res *jsonx.OrderedMap) string {
		var buf bytes.Buffer
		plan := launchPlan(t, res)
		PrintPlan(&buf, plan, nil)
		return buf.String()
	}
	out := render(resourcesOf("io", "idle", "cpus", 3, "memory", "1g"))
	for _, want := range []string{
		`disk I/O:    IOPOL_THROTTLE (resources.io "idle"; set on the launcher by setiopolicy_np`,
		"cpus:        3, cooperatively (GOMAXPROCS=3, CARGO_BUILD_JOBS=3, RAYON_NUM_THREADS=3, OMP_NUM_THREADS=3",
		"memory:      1g, sampled every 2s by `" + StagedYoloPath("") + " internal session-guard`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
	plain := render(resourcesOf("io", "normal"))
	for _, never := range []string{"disk I/O:", "cpus:  ", "memory:  "} {
		if strings.Contains(plain, never) {
			t.Errorf("an undeclared key got a plan line %q:\n%s", never, plain)
		}
	}
}

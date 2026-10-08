package entrypoint

// bootsteps_test.go pins the boot step table (bootsteps.go): that it is well-formed, that
// both boots run it, and the order each boot runs it in. The order pins are goldens on
// purpose: the table replaced two hand-kept lists, and the pure-merge rule for that change
// (docs/plans/notch-convergence.md item 22) is that each boot runs what it ran before, in
// the order it ran it, apart from the omissions the plan named. A change to either list is a
// change to a boot, and should show up here as one.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// bootStepNames is the ordered list of step names target runs.
func bootStepNames(target bootTarget) []string {
	var out []string
	for _, s := range bootSteps() {
		if s.excludedFrom(target) == "" {
			out = append(out, s.name)
		}
	}
	return out
}

// mustBootStep returns the table's step of that name.
func mustBootStep(t *testing.T, name string) bootStep {
	t.Helper()
	for _, s := range bootSteps() {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("the boot step table has no step %q", name)
	return bootStep{}
}

// assertStepBefore fails unless target runs both steps, first before second.
func assertStepBefore(t *testing.T, target bootTarget, first, second, why string) {
	t.Helper()
	names := bootStepNames(target)
	i, j := slices.Index(names, first), slices.Index(names, second)
	if i < 0 || j < 0 {
		t.Errorf("the %s boot does not run both %s (at %d) and %s (at %d): %v",
			target, first, i, second, j, names)
		return
	}
	if i > j {
		t.Errorf("the %s boot runs %s AFTER %s: %s", target, first, second, why)
	}
}

// isGen reports whether the step is the generator fn.
func isGen(s bootStep, fn func(*Env) error) bool {
	return s.gen != nil && reflect.ValueOf(s.gen).Pointer() == reflect.ValueOf(fn).Pointer()
}

// isRun reports whether the step's body is fn.
func isRun(s bootStep, fn func(*bootRun)) bool {
	return s.run != nil && reflect.ValueOf(s.run).Pointer() == reflect.ValueOf(fn).Pointer()
}

// THE TABLE'S SHAPE: every step has a unique name and exactly one body, and runs on at least
// one boot. An exclusion IS its reason, so "excluded without a reason" cannot be written; a
// step excluded from both boots is a step nothing runs, which the table must not hold.
func TestEveryBootStepRunsOrSaysWhy(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range bootSteps() {
		if s.name == "" {
			t.Error("a boot step has no name")
		}
		if seen[s.name] {
			t.Errorf("two boot steps are named %q", s.name)
		}
		seen[s.name] = true
		if (s.gen == nil) == (s.run == nil) {
			t.Errorf("step %q must set exactly one of gen and run", s.name)
		}
		if s.notContainer != "" && s.notDarwin != "" {
			t.Errorf("step %q is excluded from both boots, so nothing runs it", s.name)
		}
		if s.notSessionPass != "" && s.notContainer != "" {
			t.Errorf("step %q skips a session's pass of a boot that never runs it", s.name)
		}
		for _, reason := range []string{s.notContainer, s.notDarwin, s.notSessionPass} {
			if reason != "" && len(strings.Fields(reason)) < 4 {
				t.Errorf("step %q's exclusion %q does not say why", s.name, reason)
			}
		}
	}
}

// The container boot's order, which is Main's order before the table, step for step.
func TestTheContainerBootRunsItsStepsInOrder(t *testing.T) {
	want := []string{
		"hydrate_user_env", "configure_timezone", "scratch_permissions",
		"generate_store_packages", "generate_ld_cache",
		"generate_shims", "generate_agent_launchers", "generate_package_manager_launchers",
		"deliver_launch_flags", "assert_required_bins",
		"catalog_installed_orphans", "reconcile_installed_programs", "report_durable_dir",
		"generate_ca_bundle", "generate_bashrc", "generate_bootstrap_script",
		"generate_venv_precreate_script", "generate_mise_config", "mise_uninstall_retired",
		"nvim_config", "generate_mcp_wrappers", "skills_skipped",
		"configure_pack_surfaces", "configure_host_files",
		"cgroup_delegation", "cleanup_stale_wrappers",
		"published_port_localnet", "port_forwarding",
		"note_service_caller_auth", "write_caller_token_files", "start_jail_daemon_supervisor",
		"start_nix_root_watcher", "probe_service_reachability",
	}
	if got := bootStepNames(bootContainer); !slices.Equal(got, want) {
		t.Errorf("container boot steps:\n got %v\nwant %v", got, want)
	}
}

// The macos-user bootstrap's order: RunDarwinBootstrap's before the table, plus the program
// reconcile, which it skipped on a premise that stopped being true when this backend began
// staging a pack tree (plan row D10). Since then: the session env file is read first, in the
// slot the container reads its user env file in, so the requires_env gate sees what the agent
// will have; and the orphan catalog runs between the two other informational steps, as it
// does in a container, now that every pointer its one line gives works here (NC-D26).
func TestTheMacosUserBootRunsItsStepsInOrder(t *testing.T) {
	want := []string{
		"hydrate_session_env", "darwin_home_layout",
		"generate_shims", "generate_agent_launchers", "generate_package_manager_launchers",
		"deliver_launch_flags", "assert_required_bins",
		"catalog_installed_orphans", "reconcile_installed_programs", "report_durable_dir",
		"generate_bashrc", "generate_mise_config", "mcp_presets_declined", "configure_git",
		"configure_pack_surfaces", "configure_host_files", "install_home_overlay",
		"generate_darwin_bootstrap_script", "install_yolo_log", "write_login_rc",
	}
	if got := bootStepNames(bootDarwin); !slices.Equal(got, want) {
		t.Errorf("macos-user boot steps:\n got %v\nwant %v", got, want)
	}
}

// THE PERF LOG keeps the labels it had before the table, in order: a mark per step under the
// step's name unless the step names another label or marks nothing.
func TestTheContainerPerfLogKeepsItsLabels(t *testing.T) {
	var got []string
	for _, s := range bootSteps() {
		if s.excludedFrom(bootContainer) != "" || s.noMark {
			continue
		}
		if s.perf != "" {
			got = append(got, s.perf)
		} else {
			got = append(got, s.name)
		}
	}
	want := []string{
		"hydrate_user_env", "configure_timezone", "scratch_permissions",
		"generate_store_packages", "generate_ld_cache", "generate_shims",
		"generate_agent_launchers", "generate_package_manager_launchers",
		"deliver_launch_flags", "assert_required_bins", "catalog_installed_orphans",
		"reconcile_installed_programs", "report_durable_dir", "generate_ca_bundle", "generate_bashrc",
		"generate_bootstrap_script", "generate_venv_precreate_script", "generate_mise_config",
		"nvim_config", "generate_mcp_wrappers", "skills_skipped", "configure_pack_surfaces",
		"configure_host_files", "cgroup_delegation", "cleanup_stale_wrappers",
		"published_port_localnet", "port_forwarding", "jail_daemon_supervisor",
		"probe_service_reachability",
	}
	if !slices.Equal(got, want) {
		t.Errorf("container perf labels:\n got %v\nwant %v", got, want)
	}
}

// The runner's rules, over a synthetic table: a boot skips the steps it excludes and runs the
// rest in order; a generator's failure is collected under its label (the name, or label when
// set) and the next step still runs; the perf log gets a mark per step, under perf when set,
// and none for noMark; and a boot with no perf log marks nothing.
func TestRunStepsHonorsExclusionsLabelsAndMarks(t *testing.T) {
	var ran []string
	record := func(name string) func(*bootRun) { return func(*bootRun) { ran = append(ran, name) } }
	steps := []bootStep{
		{name: "both", run: record("both")},
		{name: "container_only", run: record("container_only"), notDarwin: "a reason given here"},
		{name: "darwin_only", run: record("darwin_only"), notContainer: "a reason given here"},
		{name: "failing", label: "old_label", gen: func(*Env) error {
			ran = append(ran, "failing")
			return os.ErrPermission
		}, perf: "failing_mark"},
		{name: "quiet", run: record("quiet"), noMark: true},
	}

	e := &Env{Home: t.TempDir(), Vars: map[string]string{}, Stderr: &strings.Builder{}}
	p := newPerfLog()
	runSteps(&bootRun{e: e, target: bootContainer, perf: p}, steps)
	if want := []string{"both", "container_only", "failing", "quiet"}; !slices.Equal(ran, want) {
		t.Errorf("container ran %v, want %v", ran, want)
	}
	var marks []string
	for _, m := range p.entries {
		marks = append(marks, m.label)
	}
	if want := []string{"both", "container_only", "failing_mark"}; !slices.Equal(marks, want) {
		t.Errorf("perf marks %v, want %v", marks, want)
	}
	if fails := e.GenFailures(); len(fails) != 1 || !strings.HasPrefix(fails[0], "old_label: ") {
		t.Errorf("gen failures %v, want one under old_label", fails)
	}

	ran = nil
	runSteps(&bootRun{e: &Env{Home: t.TempDir(), Vars: map[string]string{}, Stderr: &strings.Builder{}},
		target: bootDarwin}, steps)
	if want := []string{"both", "darwin_only", "failing", "quiet"}; !slices.Equal(ran, want) {
		t.Errorf("macos-user ran %v, want %v", ran, want)
	}
}

// Both boots call the table, each as itself: Main as the container boot, before the refusal
// gate and the exec; RunDarwinBootstrap as the macos-user one. Fails if either call site is
// deleted or names the other boot.
func TestBothBootsRunTheTable(t *testing.T) {
	for _, c := range []struct{ file, fn, target string }{
		{"boot.go", "Main", "bootContainer"},
		{"darwin.go", "RunDarwinBootstrap", "bootDarwin"},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, c.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var calls int
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Name.Name != c.fn || fn.Recv != nil {
				return true
			}
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				call, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "runBootSteps" || len(call.Args) != 1 {
					return true
				}
				ast.Inspect(call.Args[0], func(k ast.Node) bool {
					kv, ok := k.(*ast.KeyValueExpr)
					if !ok {
						return true
					}
					key, _ := kv.Key.(*ast.Ident)
					val, _ := kv.Value.(*ast.Ident)
					if key != nil && val != nil && key.Name == "target" && val.Name == c.target {
						calls++
					}
					return true
				})
				return true
			})
			return false
		})
		if calls != 1 {
			t.Errorf("%s (%s) calls runBootSteps as %s %d times, want once", c.fn, c.file, c.target, calls)
		}
	}
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	run := callIndex(string(src), "runBootSteps(&bootRun{e: e, target: bootContainer")
	gate := callIndex(string(src), "if err := genFailuresError(e); err != nil {")
	execCall := callIndex(string(src), "return execBash(e, command, ")
	if run < 0 || gate < 0 || execCall < 0 || run > gate || gate > execCall {
		t.Errorf("Main must run the table, then the refusal gate, then the exec "+
			"(table=%d, gate=%d, exec=%d)", run, gate, execCall)
	}
}

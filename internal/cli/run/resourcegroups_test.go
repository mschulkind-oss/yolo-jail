package run

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
)

// hermeticHostPlacement replaces the host launch's real placement dependencies, which read
// YOLO_VERSION (set in the jail this suite often runs in), systemd and the cgroup mount.
func hermeticHostPlacement(t *testing.T, p resourceGroupPlacement) {
	t.Helper()
	saved := hostResourceGroupPlacement
	hostResourceGroupPlacement = func() resourceGroupPlacement { return p }
	t.Cleanup(func() { hostResourceGroupPlacement = saved })
}

func TestHostResourceGroupRefusalEscapesDynamicTerminalText(t *testing.T) {
	root := filepath.Join(canonicalTempDir(t), "[bold red]project\x1b[31m[/bold red]")
	workspace := filepath.Join(root, "child")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	hermeticHostPlacement(t, resourceGroupPlacement{IsLinux: true, Observe: func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		return resourcegroups.IOPaths{}, fmt.Errorf("observer failed")
	}})
	projects := jsonx.NewOrderedMap()
	projects.Set(root, "safe.slice")
	group := jsonx.NewOrderedMap()
	group.Set("projects", projects)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("resource_groups", group)
	var stderr bytes.Buffer
	if !RefuseHostResourceGroup(&stderr, cfg) {
		t.Fatal("host resource-group selection did not refuse unsupported placement")
	}
	got := stderr.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("host refusal contains raw terminal control: %q", got)
	}
	visible := strings.ReplaceAll(got, "\u2060", "")
	for _, want := range []string{"[bold red]project\\x1b[31m[/bold red]", "safe.slice", "could not be placed", "yolo config unset --user resource_groups"} {
		if !strings.Contains(visible, want) {
			t.Errorf("host refusal lost visible dynamic text %q: %q", want, got)
		}
	}
}

func TestHostResourceGroupGetwdFailureHasNextStep(t *testing.T) {
	// Linux's getcwd fails ENOENT for a removed working directory; darwin's answers the
	// directory's former path. Both must reach the same refusal and next step.
	for _, tc := range []struct {
		name         string
		formerAnswer bool
	}{
		{name: "getcwd fails"},
		{name: "getcwd answers the former path", formerAnswer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := canonicalTempDir(t)
			workspace := filepath.Join(parent, "removed")
			if err := os.Mkdir(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(workspace)
			if err := os.Remove(workspace); err != nil {
				t.Fatal(err)
			}
			if tc.formerAnswer {
				saved := hostGetwd
				hostGetwd = func() (string, error) { return workspace, nil }
				t.Cleanup(func() { hostGetwd = saved })
			}
			group := jsonx.NewOrderedMap()
			group.Set("default", "work.slice")
			cfg := jsonx.NewOrderedMap()
			cfg.Set("resource_groups", group)
			var stderr bytes.Buffer
			if !RefuseHostResourceGroup(&stderr, cfg) {
				t.Fatal("selected resource group did not refuse after cwd was removed")
			}
			if !strings.Contains(stderr.String(), "Change to an existing workspace directory and retry") {
				t.Fatalf("host getwd refusal has no actionable next step: %q", stderr.String())
			}
		})
	}
}

func TestMappedLaunchInJailDoesNotProbeHostPolicy(t *testing.T) {
	workspace := canonicalTempDir(t)
	t.Setenv("HOME", canonicalTempDir(t))
	userConfig, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[resource_groups.workspaces]\n" + fmt.Sprintf("%q = %q\n", workspace, "forms.slice")
	if err := os.WriteFile(userConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := &Options{Workspace: workspace, IsLinux: true, Stdout: &stdout, Stderr: &stderr}
	fillDefaults(o)
	o.Stdout, o.Stderr = &stdout, &stderr
	o.Getenv = func(key string) string {
		if key == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.LookPath = func(string) (string, bool) { return "", false }
	assigned := filepath.Join(canonicalTempDir(t), "io.max")
	if err := os.WriteFile(assigned, []byte("8:0 rbps=101\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	observerCalls, managerCalls := 0, 0
	o.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		observerCalls++
		return resourcegroups.IOPaths{Devices: []string{"8:0"}, Assigned: assigned}, nil
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 0 && argv[0] == "systemctl" {
			managerCalls++
		}
		return ExecResult{Ran: false}
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want required-placement refusal\nstderr:\n%s", rc, stderr.String())
	}
	got := stderr.String()
	if observerCalls != 0 || managerCalls != 0 {
		t.Errorf("in-jail Run made host policy probes: observer=%d systemctl=%d", observerCalls, managerCalls)
	}
	for _, forbidden := range []string{"I/O child plan", "finite assigned"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("in-jail Run presented host policy output %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "inside a jail") || !strings.Contains(got, "could not be placed") || !strings.Contains(got, "yolo config unset --user resource_groups") {
		t.Fatalf("in-jail Run did not refuse with a next step:\n%s", got)
	}
}

func TestRunResourceGroupDiagnosticsEscapeDynamicTerminalText(t *testing.T) {
	workspace := filepath.Join(canonicalTempDir(t), "[bold red]project\x1b[31m[/bold red]", "child")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Dir(workspace)
	projects := jsonx.NewOrderedMap()
	projects.Set(project, "safe.slice")
	resourceGroup := jsonx.NewOrderedMap()
	resourceGroup.Set("projects", projects)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("resource_groups", resourceGroup)
	var stderr bytes.Buffer
	o := &Options{Workspace: workspace, Stderr: &stderr, IsLinux: true}
	fillDefaults(o)
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		return resourcegroups.IOPaths{}, fmt.Errorf("observer [bold yellow]failure[/bold yellow]\x1b[2J")
	}
	if !o.placeOrRefuseResourceGroup(cfg) {
		t.Fatal("selected resource group did not retain the placement refusal")
	}
	got := stderr.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("run output contains raw terminal control: %q", got)
	}
	visible := strings.ReplaceAll(got, "\u2060", "")
	for _, want := range []string{"[bold red]project\\x1b[31m[/bold red]", "safe.slice", "[bold yellow]failure[/bold yellow]", `\x1b[2J`} {
		if !strings.Contains(visible, want) {
			t.Errorf("run output lost or interpreted safe diagnostic text %q:\n%s", want, got)
		}
	}
}

func TestRunResourceGroupsOffInJailDoesNotProbeOrRefuse(t *testing.T) {
	var stderr bytes.Buffer
	o := &Options{Workspace: canonicalTempDir(t), Stderr: &stderr}
	fillDefaults(o)
	o.Stderr = &stderr
	o.Getenv = func(key string) string {
		if key == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
	observerCalls := 0
	o.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		observerCalls++
		return resourcegroups.IOPaths{}, fmt.Errorf("must not be called")
	}
	if o.placeOrRefuseResourceGroup(jsonx.NewOrderedMap()) {
		t.Fatal("disabled in-jail selection unexpectedly refused")
	}
	if observerCalls != 0 || stderr.Len() != 0 {
		t.Fatalf("disabled in-jail selection had effects: observer calls=%d output=%q", observerCalls, stderr.String())
	}
}

func TestMappedLaunchUsesResourceGroupPlanBeforePrewarm(t *testing.T) {
	workspace := canonicalTempDir(t)
	t.Setenv("HOME", canonicalTempDir(t))
	userConfig, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[resource_groups.workspaces]\n" + fmt.Sprintf("%q = %q\n", workspace, "forms.slice")
	if err := os.WriteFile(userConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := &Options{Workspace: workspace, IsLinux: true, Stdout: &stdout, Stderr: &stderr}
	fillDefaults(o)
	o.Stdout, o.Stderr = &stdout, &stderr
	// This synthetic observer fixture explicitly models the host, not a jail-side cgroup namespace.
	o.Getenv = func(string) string { return "" }
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.LookPath = func(string) (string, bool) { return "", false }
	planFile := filepath.Join(canonicalTempDir(t), "io.max")
	if err := os.WriteFile(planFile, []byte("8:0 rbps=101\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"cgroup.controllers": "io cpu", "cgroup.subtree_control": "io"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(planFile), name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var planObserved int
	o.ResourceGroupIOPaths = func(selection config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		planObserved++
		if selection.Unit != "forms.slice" {
			t.Fatalf("launch plan selection = %+v, want forms.slice", selection)
		}
		return resourcegroups.IOPaths{Devices: []string{"8:0"}, Assigned: planFile, UserManager: true}, nil
	}
	if runtime.GOOS != "linux" {
		t.Skip("the delegation preflight is linux-only; authority_other.go answers unsupported elsewhere")
	}
	execCount, placeCalls := 0, 0
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		execCount++
		return ExecResult{Ran: false}
	}
	o.ResourceGroupChildren = func(_ context.Context, sel config.ResourceGroupSelection, iopaths resourcegroups.IOPaths) (*resourcegroups.ChildSlices, error) {
		placeCalls++
		if sel.Unit != "forms.slice" || sel.AgentPercent != 5 || iopaths.Assigned != planFile {
			t.Errorf("child creation got unit=%q percent=%d assigned=%q", sel.Unit, sel.AgentPercent, iopaths.Assigned)
		}
		return nil, fmt.Errorf("fixture writer refuses; run yolo check --no-build")
	}
	o.Getenv = func(string) string { return "" }
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want refusal\nstderr:\n%s", rc, stderr.String())
	}
	got := stderr.String()
	for _, want := range []string{`forms.slice`, `could not be placed`, `fixture writer refuses`, `yolo config unset --user resource_groups`, `I/O child plan for the selected group:`, `8:0 rbps: finite assigned 101`} {
		if !strings.Contains(got, want) {
			t.Errorf("mapped Run output lacks %q:\n%s", want, got)
		}
	}
	if planObserved != 1 || placeCalls != 1 {
		t.Fatalf("observer calls = %d, writer calls = %d, want one production gate call each", planObserved, placeCalls)
	}
	if execCount != 0 {
		t.Fatalf("a refused placement reached an external execution hook %d times", execCount)
	}
}

// A placed launch records what the placement established, so the container argv asks the
// runtime for the same group; deleting the gate's call leaves it nil and this fails.
func TestPlacedResourceGroupIsRecordedForTheLaunch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("placement is linux-only; authority_other.go answers unsupported elsewhere")
	}
	workspace := canonicalTempDir(t)
	t.Setenv("HOME", canonicalTempDir(t))
	group := jsonx.NewOrderedMap()
	group.Set("default", "batch.slice")
	cfg := jsonx.NewOrderedMap()
	cfg.Set("resource_groups", group)
	root := canonicalTempDir(t)
	assigned := filepath.Join(root, "io.max")
	for name, data := range map[string]string{"io.max": "8:0 rbps=101 wbps=20\n", "cgroup.controllers": "io\n", "cgroup.subtree_control": "io\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	o := &Options{Workspace: workspace, Stderr: &stderr, IsLinux: true}
	fillDefaults(o)
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		return resourcegroups.IOPaths{Devices: []string{"8:0"}, Assigned: assigned, UserManager: true}, nil
	}
	o.ResourceGroupChildren = func(context.Context, config.ResourceGroupSelection, resourcegroups.IOPaths) (*resourcegroups.ChildSlices, error) {
		return &resourcegroups.ChildSlices{Unit: "batch.slice", Agent: "batch-agent.slice", AgentDir: "/sys/fs/cgroup/batch.slice/batch-agent.slice"}, nil
	}
	o.ResourceGroupMember = func(int) (string, error) { return "/sys/fs/cgroup/batch.slice/batch-agent.slice/run-p1.scope", nil }
	o.ResourceGroupMove = func(context.Context, string, int) error {
		t.Error("a launcher already inside the agent child was moved again")
		return nil
	}
	if o.placeOrRefuseResourceGroup(cfg) {
		t.Fatalf("a placeable mapping refused:\n%s", stderr.String())
	}
	if o.resourceGroupPlaced == nil || o.resourceGroupPlaced.Unit != "batch.slice" || o.resourceGroupPlaced.Child != "batch-agent.slice" {
		t.Fatalf("the launch did not record its placement: %+v", o.resourceGroupPlaced)
	}
	if strings.Contains(stderr.String(), "enforced.") || !strings.Contains(stderr.String(), "The ceiling is set and read back, and the kernel enforces it") {
		t.Fatalf("placement output claims more than it witnessed:\n%s", stderr.String())
	}
}

func TestResourceGroupContainerArgsAskTheRuntimeForTheSameGroup(t *testing.T) {
	rootless, rootful := true, false
	facts := func(manager string, rootless *bool) *podmanFacts {
		f := &podmanFacts{parsed: true}
		f.info.Host.CgroupManager = manager
		f.info.Host.Security.Rootless = rootless
		return f
	}
	cases := []struct {
		name  string
		rt    string
		unit  string
		facts *podmanFacts
		want  string // "" is success with --cgroup-parent=<agent child>
	}{
		{"rootless podman systemd slice", "podman", "batch.slice", facts("systemd", &rootless), ""},
		{"no facts", "podman", "batch.slice", nil, "cgroup manager could not be read"},
		{"rootful podman", "podman", "batch.slice", facts("systemd", &rootful), "not rootless"},
		{"cgroupfs manager", "podman", "batch.slice", facts("cgroupfs", &rootless), "cgroup_manager"},
		{"no agent child slice", "podman", "work.service", facts("systemd", &rootless), "no agent child slice"},
		{"other backend", "container", "batch.slice", nil, "no supported way"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			child := strings.TrimSuffix(tc.unit, ".slice") + "-agent.slice"
			if !strings.HasSuffix(tc.unit, ".slice") {
				child = tc.unit
			}
			o := &Options{podmanFacts: tc.facts, resourceGroupPlaced: &placedResourceGroup{Unit: tc.unit, Child: child}}
			args, err := o.resourceGroupContainerArgs(tc.rt)
			if tc.want == "" {
				if err != nil || len(args) != 1 || args[0] != "--cgroup-parent=batch-agent.slice" {
					t.Fatalf("args = %v, err = %v", args, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "yolo config unset --user resource_groups") && !strings.Contains(err.Error(), "retry") {
				t.Fatalf("error = %v, want one naming %q and a next step", err, tc.want)
			}
		})
	}
	// Default off: no placement means no argument and no probe of podman's facts.
	if args, err := (&Options{}).resourceGroupContainerArgs("podman"); args != nil || err != nil {
		t.Fatalf("an unmapped launch got %v, %v", args, err)
	}
}

// The assembled podman argv carries the request, and a refused request stops the launch
// through assemblyErr; deleting the call in assembleRunCmd fails both halves.
func TestAssembleRunCmdPassesTheResourceGroupParent(t *testing.T) {
	rootless := true
	f := &podmanFacts{parsed: true}
	f.info.Host.CgroupManager = "systemd"
	f.info.Host.Security.Rootless = &rootless
	for _, tc := range []struct {
		name    string
		unit    string
		wantArg bool
		wantErr bool
	}{{"slice", "batch.slice", true, false}, {"service", "work.service", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)
			in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
			o.podmanFacts = f
			child := strings.TrimSuffix(tc.unit, ".slice") + "-agent.slice"
			if !strings.HasSuffix(tc.unit, ".slice") {
				child = tc.unit
			}
			o.resourceGroupPlaced = &placedResourceGroup{Unit: tc.unit, Child: child}
			argv := o.assembleRunCmd(in)
			if got := slices.Contains(argv, "--cgroup-parent="+child); got != tc.wantArg {
				t.Fatalf("--cgroup-parent present = %v, want %v", got, tc.wantArg)
			}
			if (in.assemblyErr != nil) != tc.wantErr {
				t.Fatalf("assemblyErr = %v, want error=%v", in.assemblyErr, tc.wantErr)
			}
		})
	}
}

func TestRunRefusesTypoedOwnerJSONCResourceGroupsBeforeExecution(t *testing.T) {
	workspace := canonicalTempDir(t)
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	userConfig := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
	if err := os.MkdirAll(filepath.Dir(userConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userConfig, []byte(`{"resource_groups":{"defualt":"work.slice"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := &Options{Workspace: workspace, IsLinux: true, Stdout: &stdout, Stderr: &stderr}
	fillDefaults(o)
	o.Stdout, o.Stderr = &stdout, &stderr
	o.PathExists = func(string) bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStdin = func() bool { return false }
	o.Now = func() time.Time { return time.Unix(0, 0) }
	o.Getpid = func() int { return 1 }
	o.Getenv = func(key string) string {
		if key == "YOLO_SETUP" {
			return "podman-self"
		}
		return ""
	}
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	answeringPodman(o, minimalPodmanInfo)
	execCalls := 0
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		execCalls++
		return ExecResult{Ran: false}
	}
	if rc := Run(*o); rc != 1 || !strings.Contains(stderr.String(), "resource_groups.defualt: unknown key") {
		t.Fatalf("Run() = %d, want owner JSONC typo refusal; stderr:\n%s", rc, stderr.String())
	}
	if execCalls != 0 {
		t.Fatalf("invalid resource-group JSONC reached external execution hook %d times", execCalls)
	}
}

func TestRunRejectsWorkspaceResourceGroupsFromJSONCFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		scope string
	}{
		{name: "project", files: map[string]string{"yolo-jail.jsonc": `{"resource_groups":{"default":"work.slice"}}`}, scope: "this project file"},
		{name: "local", files: map[string]string{"yolo-jail.local.jsonc": `{"resource_groups":{"default":"work.slice"}}`}, scope: "this local file"},
		{name: "include", files: map[string]string{
			"yolo-jail.jsonc":   `{"include_if_found":["parts/owner.jsonc"]}`,
			"parts/owner.jsonc": `{"resource_groups":{"default":"work.slice"}}`,
		}, scope: "this included file"},
		{name: "TOML parity", files: map[string]string{"yolo-jail.toml": "[resource_groups]\ndefault = 'work.slice'\n"}, scope: "this project file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := canonicalTempDir(t)
			t.Setenv("HOME", canonicalTempDir(t))
			for name, body := range tc.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			o := &Options{Workspace: workspace, IsLinux: true, Stdout: &stdout, Stderr: &stderr}
			fillDefaults(o)
			o.Stdout, o.Stderr = &stdout, &stderr
			o.PathExists = func(string) bool { return false }
			o.IsTTYStdout = func() bool { return false }
			o.IsTTYStdin = func() bool { return false }
			o.Now = func() time.Time { return time.Unix(0, 0) }
			o.Getpid = func() int { return 1 }
			o.LookPath = func(string) (string, bool) { return "", false }
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{} }
			o.Getenv = func(string) string { return "" }
			if rc := Run(*o); rc != 1 || !strings.Contains(stderr.String(), "config.resource_groups") || !strings.Contains(stderr.String(), "user-scope only") {
				t.Fatalf("Run() = %d, want file-backed owner-scope refusal; stderr:\n%s", rc, stderr.String())
			}
			if tc.scope != "" {
				wantFile := "yolo-jail.jsonc"
				if tc.name == "local" {
					wantFile = "yolo-jail.local.jsonc"
				} else if tc.name == "TOML parity" {
					wantFile = "yolo-jail.toml"
				} else if tc.name == "include" {
					wantFile = filepath.Join(workspace, "parts/owner.jsonc")
				}
				for _, want := range []string{wantFile, "first remove the `resource_groups` table from " + tc.scope, "then retry the command", "yolo config set resource_groups.default", "example only; replace it with your independently chosen unit; do not copy a project value", "After the workspace config graph is repaired", "on the host", "independently choose an owner-level mapping"} {
					if !strings.Contains(stderr.String(), want) {
						t.Errorf("Run() stderr %q lacks corrective advice %q", stderr.String(), want)
					}
				}
				if strings.Contains(stderr.String(), "yolo config unset resource_groups") {
					t.Errorf("Run() stderr contains an unsupported repair command: %q", stderr.String())
				}
			}
		})
	}
}

func TestResourceGroupsOffSelectionIsEmptyWithoutPathResolution(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	selection, err := config.SelectResourceGroup(cfg, filepath.Join(canonicalTempDir(t), "missing"))
	if err != nil || selection.Unit != "" {
		t.Fatalf("off selection = %#v, %v", selection, err)
	}
}

// canonicalTempDir mints a temp dir by its resolved path. resource_groups
// mappings must name canonical paths, and on macOS t.TempDir() sits under a
// symlink (/var -> /private/var), so an unresolved fixture would be refused
// for the wrong reason.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// A host launch the placement accepts proceeds, and says what it established and what it did
// not; deleting the host gate's placement call leaves the writer unvisited and this fails.
func TestHostResourceGroupPlacesTheHostCommandBeforeItRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the delegation preflight is linux-only")
	}
	workspace := canonicalTempDir(t)
	t.Chdir(workspace)
	root := canonicalTempDir(t)
	for name, data := range map[string]string{"io.max": "8:0 rbps=101 wbps=20\n", "cgroup.controllers": "io\n", "cgroup.subtree_control": "io\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var placed []int
	hermeticHostPlacement(t, resourceGroupPlacement{
		IsLinux: true,
		Pid:     4242,
		Observe: func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
			return resourcegroups.IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(root, "io.max"), UserManager: true}, nil
		},
		Children: func(_ context.Context, sel config.ResourceGroupSelection, _ resourcegroups.IOPaths) (*resourcegroups.ChildSlices, error) {
			return &resourcegroups.ChildSlices{Unit: sel.Unit, Agent: "batch-agent.slice", AgentDir: "/cg/batch-agent.slice"}, nil
		},
		Member: func(int) (string, error) {
			if len(placed) == 0 {
				return "/cg/batch.slice", nil
			}
			return "/cg/batch-agent.slice/run-p4242.scope", nil
		},
		Move: func(_ context.Context, agent string, pid int) error {
			placed = append(placed, pid)
			if agent != "batch-agent.slice" {
				t.Errorf("host launcher moved into %q", agent)
			}
			return nil
		},
	})
	group := jsonx.NewOrderedMap()
	group.Set("default", "batch.slice")
	cfg := jsonx.NewOrderedMap()
	cfg.Set("resource_groups", group)
	var stderr bytes.Buffer
	if RefuseHostResourceGroup(&stderr, cfg) {
		t.Fatalf("a placeable host mapping refused:\n%s", stderr.String())
	}
	if len(placed) != 1 || placed[0] != 4242 {
		t.Fatalf("the host launcher was placed %v, want exactly pid 4242 once", placed)
	}
	if !strings.Contains(stderr.String(), "The ceiling is set and read back, and the kernel enforces it") {
		t.Fatalf("host placement output omits the enforcement statement:\n%s", stderr.String())
	}
}

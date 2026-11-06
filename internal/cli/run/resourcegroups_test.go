package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
)

func TestHostResourceGroupRefusalEscapesDynamicTerminalText(t *testing.T) {
	root := filepath.Join(canonicalTempDir(t), "[bold red]project\x1b[31m[/bold red]")
	workspace := filepath.Join(root, "child")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
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
	for _, want := range []string{"[bold red]project\\x1b[31m[/bold red]", "safe.slice"} {
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
	for _, forbidden := range []string{"Read-only I/O child plan", "finite assigned"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("in-jail Run presented host policy output %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "unknown/unavailable") || !strings.Contains(got, "unsupported/unknown") {
		t.Fatalf("in-jail Run did not preserve explicit unknown/refusal status:\n%s", got)
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
	o := &Options{Workspace: workspace, Stderr: &stderr}
	fillDefaults(o)
	o.Stderr = &stderr
	o.Getenv = func(string) string { return "" }
	o.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		return resourcegroups.IOPaths{}, fmt.Errorf("observer [bold yellow]failure[/bold yellow]\x1b[2J")
	}
	if !o.refuseUnsupportedResourceGroup(cfg) {
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
	if o.refuseUnsupportedResourceGroup(jsonx.NewOrderedMap()) {
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
	execCount := 0
	o.Exec = func([]string, string, []string, time.Duration) ExecResult {
		execCount++
		return ExecResult{Ran: false}
	}
	o.Getenv = func(string) string { return "" }
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want refusal\nstderr:\n%s", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), `forms.slice`) || !strings.Contains(stderr.String(), `yolo check --no-build`) || !strings.Contains(stderr.String(), `unsupported/unknown`) {
		t.Fatalf("resource-group refusal lacks selected identity, unsupported state, or next step:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), `Read-only I/O child plan (not applied or enforced)`) || !strings.Contains(stderr.String(), `8:0 rbps: finite assigned 101`) {
		t.Fatalf("selected launch did not consume its finite read-only plan:\n%s", stderr.String())
	}
	// The delegation preflight is the build's own: only linux compiles the cgroup-v2 checker
	// (internal/resourcegroups/authority_other.go answers unsupported everywhere else), so a
	// macOS runner's host reports that rather than the Linux answer the fixture describes.
	wantPreflight := `controller visible; child write authority and placement unverified`
	if runtime.GOOS != "linux" {
		wantPreflight = `Resource-group I/O delegation: unsupported/unknown: delegated cgroup-v2 I/O is unsupported on this platform`
	}
	if !strings.Contains(stderr.String(), wantPreflight) {
		t.Fatalf("mapped Run omitted read-only delegation preflight while refusing startup:\n%s", stderr.String())
	}
	if planObserved != 1 {
		t.Fatalf("resource-group plan observer calls = %d, want one production gate call", planObserved)
	}
	if execCount != 0 {
		t.Fatalf("injected launch observer reached an external execution hook %d times", execCount)
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

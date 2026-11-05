package check

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
)

func TestCheckRejectsWorkspaceResourceGroupsFromJSONCFiles(t *testing.T) {
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
			t.Setenv("HOME", t.TempDir())
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			for name, body := range tc.files {
				path := filepath.Join(opts.Workspace, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if rc := Check(opts); rc != 1 || !strings.Contains(out.String(), "config.resource_groups") || !strings.Contains(out.String(), "user-scope only") {
				t.Fatalf("Check() = %d, want file-backed owner-scope refusal; output:\n%s", rc, out.String())
			}
			if tc.scope != "" {
				wantFile := "yolo-jail.jsonc"
				if tc.name == "local" {
					wantFile = "yolo-jail.local.jsonc"
				} else if tc.name == "TOML parity" {
					wantFile = "yolo-jail.toml"
				} else if tc.name == "include" || tc.name == "included TOML" {
					if tc.name == "include" {
						wantFile = filepath.Join(opts.Workspace, "parts/owner.jsonc")
					} else {
						wantFile = filepath.Join(opts.Workspace, "parts/owner.toml")
					}
				}
				for _, want := range []string{wantFile, "first remove the `resource_groups` table from " + tc.scope, "then retry the command", "yolo config set resource_groups.default", "example only; replace it with your independently chosen unit; do not copy a project value", "After the workspace config graph is repaired", "on the host", "independently choose an owner-level mapping"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("Check() output %q lacks corrective advice %q", out.String(), want)
					}
				}
				if strings.Contains(out.String(), "yolo config unset resource_groups") {
					t.Errorf("Check() output contains an unsupported repair command: %q", out.String())
				}
			}
		})
	}
}

func TestCheckResourceGroupsInJailDoesNotProbeHostPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Getenv = func(key string) string {
		if key == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
	workspace := opts.Workspace
	userFile, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte("[resource_groups.workspaces]\n"+fmt.Sprintf("%q = %q\n", workspace, "work.slice")), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := t.TempDir()
	assigned := filepath.Join(mount, "owner.slice", "work.slice")
	if err := os.MkdirAll(assigned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assigned, "io.max"), []byte("8:0 rbps=101 wbps=20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mountInfo := fmt.Sprintf("29 23 0:26 / %s rw,nosuid - cgroup2 cgroup rw\n", mount)
	managerCalls, mountInfoCalls := 0, 0
	opts.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 0 && argv[0] == "systemctl" {
			managerCalls++
			return ExecResult{Ran: true, RC: 0, Stdout: "/owner.slice/work.slice\n"}
		}
		return ExecResult{Ran: false}
	}
	opts.readFile = func(path string) ([]byte, error) {
		if path == "/proc/self/mountinfo" {
			mountInfoCalls++
			return []byte(mountInfo), nil
		}
		return os.ReadFile(path)
	}

	if rc := Check(opts); rc != 1 {
		t.Fatalf("selected in-jail Check() = %d, want required-placement refusal", rc)
	}
	got := out.String()
	if managerCalls != 0 || mountInfoCalls != 0 {
		t.Errorf("in-jail Check made host policy probes: systemctl=%d mountinfo=%d", managerCalls, mountInfoCalls)
	}
	for _, forbidden := range []string{"I/O policy plan (read-only", "finite assigned", "agent=", "batch="} {
		if strings.Contains(got, forbidden) {
			t.Errorf("in-jail Check presented host policy output %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "unknown/unavailable") || !strings.Contains(got, "placement unsupported/unknown") {
		t.Fatalf("in-jail Check did not preserve explicit unknown/refusal status:\n%s", got)
	}
}

func TestCheckResourceGroupDiagnosticsEscapeDynamicTerminalText(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "[bold red]project\x1b[31m[/bold red]", "child")
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
	var out bytes.Buffer
	r := newReporter(&out, false)
	sectionResourceGroups(r, cfg, workspace, func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		return resourcegroups.IOPaths{}, fmt.Errorf("observer [bold red]failure[/bold red]\x1b[2J")
	}, os.ReadFile)
	got := out.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("check output contains raw terminal control: %q", got)
	}
	visibleProject := strings.ReplaceAll(project, "\x1b", `\x1b`)
	for _, want := range []string{"project " + visibleProject, "[bold red]failure[/bold red]", `\x1b[2J`, "safe.slice"} {
		if !strings.Contains(got, want) {
			t.Errorf("check output lost or misrendered safe diagnostic text %q:\n%s", want, got)
		}
	}
}

func TestCheckResourceGroupsOffInJailDoesNotProbeOrRefuse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Getenv = func(key string) string {
		if key == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
	opts.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		t.Fatal("disabled in-jail selection must not observe resource-group policy")
		return resourcegroups.IOPaths{}, nil
	}
	Check(opts)
	got := out.String()
	if !strings.Contains(got, "Resource groups: disabled") || strings.Contains(got, "Resource group selected:") {
		t.Fatalf("disabled in-jail policy changed resource-group status:\n%s", got)
	}
}

func TestCheckResourceGroupsFiniteAssignedPlan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	// This synthetic observer fixture explicitly models the host, not a jail-side cgroup namespace.
	opts.Getenv = func(string) string { return "" }
	workspace := opts.Workspace
	userFile, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte("[resource_groups.workspaces]\n"+fmt.Sprintf("%q = %q\n", workspace, "work.slice")), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := t.TempDir()
	assigned := filepath.Join(mount, "owner.slice", "work.slice")
	if err := os.MkdirAll(assigned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assigned, "io.max"), []byte("8:0 rbps=101 wbps=20 riops=max wiops=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mountInfo := fmt.Sprintf("29 23 0:26 / %s rw,nosuid - cgroup2 cgroup rw\n", mount)
	var queried []string
	opts.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 0 && argv[0] == "systemctl" {
			queried = append(queried, strings.Join(argv, " "))
			if strings.Contains(strings.Join(argv, " "), " --user ") {
				return ExecResult{Ran: true, RC: 1}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: "/owner.slice/work.slice\n"}
		}
		return ExecResult{Ran: false}
	}
	opts.readFile = func(path string) ([]byte, error) {
		if path == "/proc/self/mountinfo" {
			return []byte(mountInfo), nil
		}
		return os.ReadFile(path)
	}
	if rc := Check(opts); rc != 1 {
		t.Fatalf("selected unsupported Check() = %d, want 1", rc)
	}
	got := out.String()
	for _, want := range []string{"rbps: finite assigned 101 agent=5 batch=96", "wbps: finite assigned 20 agent=1 batch=19", "placement unsupported/unknown"} {
		if !strings.Contains(got, want) {
			t.Errorf("production resource-group check missing %q:\n%s", want, got)
		}
	}
	if len(queried) != 2 || !strings.Contains(queried[0], "work.slice") || !strings.Contains(queried[1], "work.slice") {
		t.Fatalf("selected-only manager queries = %v, want system and user lookups for work.slice only", queried)
	}
}

func TestCheckResourceGroupsDisabledAndSelectedUnknown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		t.Fatal("disabled resource-group selection must not resolve policy file paths")
		return resourcegroups.IOPaths{}, nil
	}
	Check(opts)
	if !strings.Contains(out.String(), "Resource groups: disabled") {
		t.Fatalf("check did not report disabled resource groups:\n%s", out.String())
	}

	workspace := opts.Workspace
	userFile, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte("[resource_groups.workspaces]\n"+fmt.Sprintf("%q = %q\n", workspace, "work.slice")), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	assigned := filepath.Join(fixture, "assigned.io.max")
	if err := os.WriteFile(assigned, []byte("8:0 rbps=101 wbps=20 riops=max wiops=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.readFile = os.ReadFile
	opts.ResourceGroupIOPaths = func(selection config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		if selection.Unit != "work.slice" || selection.AgentPercent != 5 || selection.AgentPercentExplicit {
			t.Fatalf("selected policy request = %+v, want work.slice at engineer default 5%%", selection)
		}
		return resourcegroups.IOPaths{Devices: []string{"8:0"}, Assigned: assigned}, nil
	}
	out.Reset()
	if rc := Check(opts); rc != 1 {
		t.Fatalf("selected unknown resource-group Check() = %d, want 1", rc)
	}
	got := out.String()
	for _, want := range []string{"Resource group selected: work.slice", "placement unsupported/unknown", "rbps: finite assigned 101 agent=5 batch=96", "wbps: finite assigned 20 agent=1 batch=19", "riops", "wiops", "I/O policy plan (read-only; child ceilings are not applied or enforced)", "5% agent / 95% batch is an engineering default, not measured throughput"} {
		if !strings.Contains(got, want) {
			t.Errorf("selected check output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[PASS] Resource group") || !strings.Contains(got, "placement unsupported/unknown") {
		t.Fatalf("check must keep selected placement unsupported/unknown:\n%s", got)
	}
}

package check

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
)

// A selected group whose io is delegated is reported as one a launch will place, never as
// enforced or passing: this check writes nothing and the host witness has not run.
func TestCheckResourceGroupsDelegatedGroupSaysALaunchWillPlaceIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the delegation check is linux-only")
	}
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	userFile, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte("[resource_groups.workspaces]\n"+fmt.Sprintf("%q = %q\n", opts.Workspace, "batch.slice")), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	for name, data := range map[string]string{"io.max": "8:0 rbps=101 wbps=20\n", "cgroup.controllers": "io\n"} {
		if err := os.WriteFile(filepath.Join(fixture, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts.readFile = os.ReadFile
	opts.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		return resourcegroups.IOPaths{Devices: []string{"8:0"}, Assigned: filepath.Join(fixture, "io.max"), UserManager: true}, nil
	}
	Check(opts)
	got := out.String()
	for _, want := range []string{"Resource group selected: batch.slice", "io reaches the group, so a launch will create the two child slices and place itself", "batch-agent.slice and batch-batch.slice as systemd child slices of batch.slice", "--cgroup-parent=batch-agent.slice", "Placement and enforcement are not verified by this check"} {
		if !strings.Contains(got, want) {
			t.Errorf("delegated-group check lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[PASS] Resource group") || strings.Contains(got, "a launch cannot place it") {
		t.Fatalf("a delegated group was reported as passing or as unplaceable:\n%s", got)
	}
}

// A mapped unit that is not a slice has no child slices to create, and the check says so
// before observing anything.
func TestCheckResourceGroupsNonSliceUnitFailsNamingTheSliceRequirement(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the delegation check is linux-only")
	}
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	userFile, _ := config.UserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte("[resource_groups.workspaces]\n"+fmt.Sprintf("%q = %q\n", opts.Workspace, "work.service")), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.ResourceGroupIOPaths = func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
		t.Error("a non-slice unit was observed")
		return resourcegroups.IOPaths{}, nil
	}
	Check(opts)
	got := out.String()
	if !strings.Contains(got, "is not a slice") || !strings.Contains(got, "map a .slice unit") {
		t.Fatalf("a non-slice mapping was not refused naming the slice requirement:\n%s", got)
	}
}

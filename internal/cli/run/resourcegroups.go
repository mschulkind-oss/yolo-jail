package run

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// hostGetwd is os.Getwd, a variable so a test can give the answer darwin gives for a removed
// working directory on a Linux runner.
var hostGetwd = os.Getwd

// resourceGroupIOPaths is called only for a selected mapping. It observes the named unit
// and its mapped cgroup-v2 ancestors; it never discovers devices outside that group.
func (o *Options) resourceGroupIOPaths(selection config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
	// The jail's cgroup namespace describes the jail, not the host-selected target.
	if o.inJail() {
		return resourcegroups.IOPaths{}, fmt.Errorf("host cgroup-v2 policy observation is unsupported from inside a jail")
	}
	if o.ResourceGroupIOPaths != nil {
		return o.ResourceGroupIOPaths(selection)
	}
	if !o.IsLinux || runtime.GOOS != "linux" {
		return resourcegroups.IOPaths{}, fmt.Errorf("read-only cgroup-v2 policy observation is unsupported on this platform")
	}
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return resourcegroups.IOPaths{}, fmt.Errorf("cannot read cgroup mount table: %w", err)
	}
	return resourcegroups.ResolveSelectedIOPaths(selection.Unit, func(argv []string) (string, error) {
		result := o.Exec(argv, "", nil, 2*time.Second)
		if !result.Ran || result.Timeout || result.RC != 0 {
			return "", fmt.Errorf("%v failed or unavailable", argv)
		}
		return result.Stdout, nil
	}, mountInfo, os.ReadFile)
}

// placeOrRefuseResourceGroup is the mapped launch's required placement, reported as whether the
// launch must stop. The unselected path returns without manager, controller, device, or path
// probes (OQ-RG3). A selected mapping on a Linux host observes the group, creates or reuses its two
// child slices through the user manager, moves this launcher into the agent child (by re-running
// under systemd-run) and reads the membership back, all
// before the launch's expensive work. Every condition it cannot observe refuses naming the next
// step; there is no unplaced fallback. It claims placement and a policy readback, never
// enforcement: only the owner's host witness establishes that.
func (o *Options) placeOrRefuseResourceGroup(cfg *jsonx.OrderedMap) bool {
	selection, err := config.SelectResourceGroup(cfg, o.Workspace)
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Cannot resolve resource-group selection: %s[/bold red]", richtext.Escape(termsafe.Visible(err.Error())))
		o.pr(o.Stderr).print("Fix the owner-scope `resource_groups` mapping, then run `yolo check --no-build` before retrying.")
		return true
	}
	if selection.Unit == "" {
		return false
	}
	outcome := resourceGroupPlacement{
		InJail:   o.inJail(),
		IsLinux:  o.IsLinux,
		Observe:  o.resourceGroupIOPaths,
		Children: o.ResourceGroupChildren,
		Query:    userManagerQuery(o.Exec),
		Member:   o.ResourceGroupMember,
		Move:     o.ResourceGroupMove,
		Pid:      os.Getpid(),
	}.place(selection)
	if len(outcome.PlanLines) > 0 {
		o.pr(o.Stderr).print("I/O child plan for the selected group:")
		for _, line := range outcome.PlanLines {
			o.pr(o.Stderr).print(richtext.Escape(termsafe.Visible(line)))
		}
	}
	if outcome.Err != nil {
		o.pr(o.Stderr).printf("[bold red]Resource group %q selected via %s could not be placed; this launch was not started: %s[/bold red]", richtext.Escape(termsafe.Visible(selection.Unit)), richtext.Escape(termsafe.Visible(selection.Source)), richtext.Escape(termsafe.Visible(outcome.Err.Error())))
		o.pr(o.Stderr).print("To launch without a resource group, remove the mapping with `yolo config unset --user resource_groups`.")
		return true
	}
	o.resourceGroupPlaced = outcome.Placed
	o.pr(o.Stderr).printf("Resource group %q selected via %s: this launch is in %s, a systemd child slice whose I/O ceiling was set through your user manager and read back. Enforcement is unverified until the host witness passes.",
		richtext.Escape(termsafe.Visible(selection.Unit)), richtext.Escape(termsafe.Visible(selection.Source)), richtext.Escape(termsafe.Visible(outcome.Placed.Child)))
	return false
}

// hostResourceGroupRefusal applies the same required-placement contract to host launches.
// Pack refresh precedes this gate in the existing host path and is intentionally not claimed
// as covered by resource-group placement.
func RefuseHostResourceGroup(errw io.Writer, cfg *jsonx.OrderedMap) bool {
	if problems := config.ValidateResourceGroups(cfg); len(problems) > 0 {
		fmt.Fprintf(errw, "yolo host: refusing resource-group config: %s\n", termsafe.Visible(strings.Join(problems, "; ")))
		fmt.Fprintln(errw, "Fix or remove the owner-scope `resource_groups` setting, then run `yolo check --no-build` before retrying.")
		return true
	}
	if !config.ResourceGroupMaySelect(cfg) {
		return false
	}
	workspace, err := hostGetwd()
	if err == nil {
		// darwin's getcwd answers a removed directory's former path where Linux fails ENOENT, so
		// a path that no longer exists is the same "no workspace" answer on both.
		_, err = os.Stat(workspace)
	}
	if err != nil {
		fmt.Fprintf(errw, "yolo host: cannot resolve workspace for resource-group selection: %s\n", termsafe.Visible(err.Error()))
		fmt.Fprintln(errw, "Change to an existing workspace directory and retry, or run `yolo check --no-build` to inspect the selection.")
		return true
	}
	selection, err := config.SelectResourceGroup(cfg, workspace)
	if err != nil {
		fmt.Fprintf(errw, "yolo host: cannot resolve resource-group selection: %s\n", termsafe.Visible(err.Error()))
		fmt.Fprintln(errw, "Fix the owner-scope `resource_groups` mapping, then run `yolo check --no-build` before retrying.")
		return true
	}
	if selection.Unit == "" {
		return false
	}
	outcome := hostResourceGroupPlacement().place(selection)
	for _, line := range outcome.PlanLines {
		fmt.Fprintln(errw, "yolo host: I/O child plan: "+termsafe.Visible(line))
	}
	if outcome.Err != nil {
		fmt.Fprintf(errw, "yolo host: resource group %q selected via %s could not be placed; command was not started: %s\n", termsafe.Visible(selection.Unit), termsafe.Visible(selection.Source), termsafe.Visible(outcome.Err.Error()))
		fmt.Fprintln(errw, "To run without a resource group, remove the mapping with `yolo config unset --user resource_groups`.")
		return true
	}
	fmt.Fprintf(errw, "yolo host: resource group %q selected via %s: this command runs in %s, a systemd child slice whose I/O ceiling was set through your user manager and read back. Enforcement is unverified until the host witness passes.\n", termsafe.Visible(selection.Unit), termsafe.Visible(selection.Source), termsafe.Visible(outcome.Placed.Child))
	return false
}

// hostResourceGroupIOPaths observes the selected unit from the host with the same bounded
// systemd and cgroup-v2 reads a launch uses.
func hostResourceGroupIOPaths(selection config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return resourcegroups.IOPaths{}, fmt.Errorf("cannot read cgroup mount table: %w", err)
	}
	return resourcegroups.ResolveSelectedIOPaths(selection.Unit, func(argv []string) (string, error) {
		result := realExec(argv, "", nil, 2*time.Second)
		if !result.Ran || result.Timeout || result.RC != 0 {
			return "", fmt.Errorf("%v failed or unavailable", argv)
		}
		return result.Stdout, nil
	}, mountInfo, os.ReadFile)
}

// hostResourceGroupPlacement is the host launch's real placement dependencies. It is a
// variable so a host-launch test can supply the manager, the filesystem and the writer; the
// jail this suite often runs in sets YOLO_VERSION, which a hermetic test must not depend on.
var hostResourceGroupPlacement = func() resourceGroupPlacement {
	return resourceGroupPlacement{
		InJail:  os.Getenv("YOLO_VERSION") != "",
		IsLinux: runtime.GOOS == "linux",
		Observe: hostResourceGroupIOPaths,
		Query:   userManagerQuery(realExec),
		Pid:     os.Getpid(),
	}
}

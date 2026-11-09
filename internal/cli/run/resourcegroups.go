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

// refuseUnsupportedResourceGroup observes a finite policy plan for a selected mapping,
// then retains the required-placement refusal until actual placement can be verified.
// The unselected path returns without manager, controller, device, or path probes.
func (o *Options) refuseUnsupportedResourceGroup(cfg *jsonx.OrderedMap) bool {
	selection, err := config.SelectResourceGroup(cfg, o.Workspace)
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Cannot resolve resource-group selection: %s[/bold red]", richtext.Escape(termsafe.Visible(err.Error())))
		o.pr(o.Stderr).print("Fix the owner-scope `resource_groups` mapping, then run `yolo check --no-build` before retrying.")
		return true
	}
	if selection.Unit == "" {
		return false
	}
	paths, observeErr := o.resourceGroupIOPaths(selection)
	if observeErr != nil {
		o.pr(o.Stderr).printf("[bold yellow]Resource-group I/O policy: unknown/unavailable: %s[/bold yellow]", richtext.Escape(termsafe.Visible(observeErr.Error())))
	} else if len(paths.Devices) == 0 {
		o.pr(o.Stderr).print("Resource-group I/O policy: unknown/unavailable; the selected group supplied no device identities and no host-wide device scan was performed.")
	} else {
		plan := resourcegroups.ReadAndPlanIO(paths, selection.AgentPercent, os.ReadFile)
		o.pr(o.Stderr).print("Read-only I/O child plan (not applied or enforced):")
		for _, row := range plan.Dimensions {
			o.pr(o.Stderr).print(resourcegroups.FormatIODimension(row))
		}
		// This read-only preflight does not create children or establish membership.
		// Even a delegated group cannot start until Brief 4 proves every execution boundary.
		if err := resourcegroups.CheckDelegatedIO(paths, os.ReadFile); err != nil {
			o.pr(o.Stderr).printf("Resource-group I/O delegation: unsupported/unknown: %s", richtext.Escape(termsafe.Visible(err.Error())))
		} else {
			o.pr(o.Stderr).print("Resource-group I/O delegation: controller visible; child write authority and placement unverified.")
		}
	}
	o.pr(o.Stderr).printf("[bold red]Resource group %q selected via %s, but placement is unsupported/unknown; this launch was not started.[/bold red]", richtext.Escape(termsafe.Visible(selection.Unit)), richtext.Escape(termsafe.Visible(selection.Source)))
	o.pr(o.Stderr).print("This build cannot place a launch in a resource group, so every launch this mapping selects refuses. Remove the mapping with `yolo config unset --user resource_groups`.")
	return true
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
	fmt.Fprintf(errw, "yolo host: resource group %q selected via %s, but host placement is unsupported/unknown; command was not started.\n", termsafe.Visible(selection.Unit), termsafe.Visible(selection.Source))
	fmt.Fprintln(errw, "This build cannot place a launch in a resource group, so every launch this mapping selects refuses. Remove the mapping with `yolo config unset --user resource_groups`.")
	return true
}

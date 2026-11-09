package check

import (
	"fmt"
	"runtime"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// resourceGroupIOPaths is called only for a selected mapping. The default observer
// queries only that unit and then reads its mapped cgroup files; tests may supply exact files.
func (o *Options) resourceGroupIOPaths(selection config.ResourceGroupSelection) (resourcegroups.IOPaths, error) {
	// The jail's cgroup namespace describes the jail, not the host-selected target.
	if o.inJail() {
		return resourcegroups.IOPaths{}, fmt.Errorf("host cgroup-v2 policy observation is unsupported from inside a jail")
	}
	if o.ResourceGroupIOPaths != nil {
		return o.ResourceGroupIOPaths(selection)
	}
	if runtime.GOOS != "linux" {
		return resourcegroups.IOPaths{}, fmt.Errorf("read-only cgroup-v2 policy observation is unsupported on %s", runtime.GOOS)
	}
	mountInfo, err := o.readFile("/proc/self/mountinfo")
	if err != nil {
		return resourcegroups.IOPaths{}, fmt.Errorf("cannot read cgroup mount table: %w", err)
	}
	return resourcegroups.ResolveSelectedIOPaths(selection.Unit, func(argv []string) (string, error) {
		result := o.Exec(argv, "", nil, 2*time.Second)
		if !result.Ran || result.Timeout || result.RC != 0 {
			return "", fmt.Errorf("%v failed or unavailable", argv)
		}
		return result.Stdout, nil
	}, mountInfo, o.readFile)
}

// sectionResourceGroups reports local selection and, only for selected work, a bounded
// read-only io.max plan. It never applies policy or treats planned children as enforced.
func sectionResourceGroups(r *reporter, cfg *jsonx.OrderedMap, workspace string, pathsForSelection func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error), readFile resourcegroups.IOReadFile) {
	r.sectionHeader("Resource Groups")
	defer r.blank()
	selection, err := config.SelectResourceGroup(cfg, workspace)
	if err != nil {
		r.fail("Could not resolve resource-group selection: "+termsafe.Visible(err.Error()),
			"Fix the owner-scope `resource_groups` mapping, then rerun `yolo check --no-build`.")
		return
	}
	if selection.Unit == "" {
		r.line("  Resource groups: disabled (no workspace mapping)")
		return
	}
	if pathsForSelection != nil {
		paths, resolveErr := pathsForSelection(selection)
		if resolveErr != nil {
			r.line("  I/O policy observation: unknown/unavailable: " + termsafe.Visible(resolveErr.Error()))
		} else if len(paths.Devices) == 0 {
			r.line("  I/O policy observation: unknown/unavailable (the selected group supplied no device identities; no host-wide device scan was performed).")
		} else {
			plan := resourcegroups.ReadAndPlanIO(paths, selection.AgentPercent, readFile)
			r.line("  I/O policy plan (read-only; child ceilings are not applied or enforced):")
			if !selection.AgentPercentExplicit {
				r.line("  I/O child split: 5% agent / 95% batch is an engineering default, not measured throughput.")
			} else {
				r.line(fmt.Sprintf("  I/O child split: %d%% agent / %d%% batch (owner override; not measured throughput).", selection.AgentPercent, 100-selection.AgentPercent))
			}
			for _, row := range plan.Dimensions {
				r.line("  I/O policy: " + resourcegroups.FormatIODimension(row))
			}
		}
	}
	r.fail("Resource group selected: "+termsafe.Visible(selection.Unit)+" via "+termsafe.Visible(selection.Source)+"; placement unsupported/unknown",
		"This build cannot verify host resource-group placement or policy. Every launch this mapping selects refuses. Remove the mapping with `yolo config unset --user resource_groups`; do not treat selection as enforcement.")
}

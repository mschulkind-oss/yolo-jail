package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
)

// resourceGroupPlacement is the one place a mapped launch is put into its
// assigned group. Run and the host launch both call it, only after the owner's
// mapping selected a unit: an empty config never reaches it, so the off path
// makes no manager, controller, device or cgroup call (OQ-RG3).
//
// It places the LAUNCHER process itself in the shared agent child before any
// expensive work, and reads /proc/<pid>/cgroup back before returning. Every
// descendant inherits the cgroup, including a host-mode command the launcher
// replaces itself with (hostSyscallExec keeps the pid). The jail's container is
// a separate boundary, placed by --cgroup-parent (resourceGroupContainerArgs).
type resourceGroupPlacement struct {
	// InJail and IsLinux gate the host-only Linux path (OQ-RG1).
	InJail  bool
	IsLinux bool
	// Observe resolves the selected unit to its cgroup-v2 policy paths, read-only.
	Observe func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error)
	// Place reconciles the children and moves pid into the agent child, returning
	// the child's cgroup path. nil is the real writer.
	Place func(ctx context.Context, sel config.ResourceGroupSelection, iopaths resourcegroups.IOPaths, lockDir string, pid int) (string, error)
	// LockDir is the private directory holding the shared per-group lock.
	LockDir func() (string, error)
	Pid     int
}

// placedResourceGroup records what a successful placement established.
type placedResourceGroup struct {
	Unit  string
	Child string
}

type resourceGroupOutcome struct {
	// PlanLines is the finite child plan the writer applied (or would have).
	PlanLines []string
	Placed    *placedResourceGroup
	// Err is the refusal, each ending in the next step.
	Err error
}

func defaultResourceGroupLockDir() (string, error) {
	dir := filepath.Join(paths.GlobalStorage(), "resource-groups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create the resource-group lock directory %s: %w; fix its permissions, then run yolo check --no-build", dir, err)
	}
	return dir, nil
}

func placeResourceGroupNative(ctx context.Context, sel config.ResourceGroupSelection, iopaths resourcegroups.IOPaths, lockDir string, pid int) (string, error) {
	children, err := resourcegroups.OpenUserIOChildren(ctx, sel.Unit, iopaths, sel.AgentPercent, lockDir)
	if err != nil {
		return "", err
	}
	defer children.Close()
	if err := children.PlaceProcess(ctx, resourcegroups.ChildAgent, pid); err != nil {
		return "", err
	}
	return children.ChildPath(resourcegroups.ChildAgent), nil
}

func (d resourceGroupPlacement) place(sel config.ResourceGroupSelection) resourceGroupOutcome {
	switch {
	case d.InJail:
		return resourceGroupOutcome{Err: errors.New("host cgroup-v2 policy cannot be observed or written from inside a jail; launch from the host")}
	case !d.IsLinux:
		return resourceGroupOutcome{Err: errors.New("resource-group placement is Linux-only")}
	case d.Observe == nil:
		return resourceGroupOutcome{Err: errors.New("the selected resource group cannot be observed; run yolo check --no-build")}
	}
	iopaths, err := d.Observe(sel)
	if err != nil {
		hint := "run yolo check --no-build"
		if strings.Contains(err.Error(), "no verified ControlGroup") || strings.Contains(err.Error(), "io.max is unavailable") {
			hint = "start the unit with `systemctl --user start " + sel.Unit + "` (and give it a finite IOReadBandwidthMax or IOWriteBandwidthMax), then run yolo check --no-build"
		}
		return resourceGroupOutcome{Err: fmt.Errorf("the selected group cannot be observed in your user manager: %w; %s", err, hint)}
	}
	if len(iopaths.Devices) == 0 {
		return resourceGroupOutcome{Err: errors.New("the selected group's io.max names no device, so there is no finite allowance to split: give the unit a finite IOReadBandwidthMax or IOWriteBandwidthMax, then run yolo check --no-build")}
	}
	var out resourceGroupOutcome
	for _, row := range resourcegroups.ReadAndPlanIO(iopaths, sel.AgentPercent, os.ReadFile).Dimensions {
		out.PlanLines = append(out.PlanLines, resourcegroups.FormatIODimension(row))
	}
	if err := resourcegroups.CheckDelegatedIO(iopaths, os.ReadFile); err != nil {
		out.Err = err
		return out
	}
	lockDirFn := d.LockDir
	if lockDirFn == nil {
		lockDirFn = defaultResourceGroupLockDir
	}
	lockDir, err := lockDirFn()
	if err != nil {
		out.Err = err
		return out
	}
	placer := d.Place
	if placer == nil {
		placer = placeResourceGroupNative
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child, err := placer(ctx, sel, iopaths, lockDir, d.Pid)
	if err != nil {
		out.Err = err
		return out
	}
	out.Placed = &placedResourceGroup{Unit: sel.Unit, Child: child}
	return out
}

// resourceGroupContainerArgs is the argv that asks the jail's runtime to create
// the container under the assigned group. The request is not proof: the launcher
// placement above is what is witnessed; the container's own membership is
// requested here and left to the host witness. Only rootless podman with the
// systemd cgroup manager has a supported route: that manager creates the
// container's scope as a child of a named SLICE, so a unit that is not a slice,
// another manager, or a rootful engine is refused with the missing step.
func (o *Options) resourceGroupContainerArgs(rt string) ([]string, error) {
	placed := o.resourceGroupPlaced
	if placed == nil {
		return nil, nil
	}
	remove := "or remove the mapping with `yolo config unset --user resource_groups`"
	if rt != "podman" { // parity: Refused — only rootless podman on Linux has a supported container route (OQ-RG1); other backends refuse the mapped launch
		return nil, fmt.Errorf("resource group %q is selected, but the %s backend has no supported way to place its container; use a podman setup, %s", placed.Unit, rt, remove)
	}
	if o.podmanFacts == nil || !o.podmanFacts.parsed {
		return nil, fmt.Errorf("resource group %q is selected, but podman's cgroup manager could not be read; run `podman info`, then retry", placed.Unit)
	}
	host := o.podmanFacts.info.Host
	if host.Security.Rootless == nil || !*host.Security.Rootless {
		return nil, fmt.Errorf("resource group %q is a user-manager group, but podman is not rootless, so its containers are not in your user manager; use rootless podman, %s", placed.Unit, remove)
	}
	if host.CgroupManager != "systemd" {
		return nil, fmt.Errorf("resource group %q is selected, but podman's cgroup manager is %q: only the systemd manager can create the container under a slice; set cgroup_manager = \"systemd\" in containers.conf, %s", placed.Unit, host.CgroupManager, remove)
	}
	if !strings.HasSuffix(placed.Unit, ".slice") {
		return nil, fmt.Errorf("resource group %q is not a slice, and podman's systemd manager can only create a container under a slice; map a .slice unit, %s", placed.Unit, remove)
	}
	return []string{"--cgroup-parent=" + placed.Unit}, nil
}

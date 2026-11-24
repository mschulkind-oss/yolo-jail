package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/resourcegroups"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// resourceGroupPlacement is the one place a mapped launch is put into its
// assigned group. Run and the host launch both call it, only after the owner's
// mapping selected a unit: an empty config never reaches it, so the off path
// makes no manager, controller, device or cgroup call (OQ-RG3).
//
// RG-R6: the two shared children are systemd child slices of the mapped slice,
// created and limited through the user manager. The LAUNCHER process is placed in
// the agent child by asking the user manager, over D-Bus (busctl, the call the
// keeper already makes for its own scope), to start a transient scope in the agent
// child slice with this pid in it: the pid and the terminal are kept, and everything
// the launcher then starts inherits the cgroup, including a host-mode command the
// launcher replaces itself with. The scope start is asynchronous, so /proc/<pid>/cgroup
// is read back until it names the agent child (or a generous deadline guard expires)
// before the launch goes on. The jail's container is a separate boundary,
// placed by --cgroup-parent=<agent child> (resourceGroupContainerArgs).
type resourceGroupPlacement struct {
	// InJail and IsLinux gate the host-only Linux path (OQ-RG1).
	InJail  bool
	IsLinux bool
	// Observe resolves the selected unit to its cgroup-v2 policy paths, read-only.
	Observe func(config.ResourceGroupSelection) (resourcegroups.IOPaths, error)
	// Children creates or reuses the two child slices, limits them and reads the
	// limits back. nil is resourcegroups.EnsureChildSlices over Query.
	Children func(ctx context.Context, sel config.ResourceGroupSelection, iopaths resourcegroups.IOPaths) (*resourcegroups.ChildSlices, error)
	// Query runs one systemctl argv for the real Children; nil is no user manager.
	Query resourcegroups.ManagerQuery
	// Member reports the cgroup directory pid is in, read from /proc. nil is the real read.
	Member func(pid int) (string, error)
	// Move asks the user manager to put pid into a transient scope inside the named agent
	// child slice. nil is the real busctl StartTransientUnit call over Query.
	Move func(ctx context.Context, agentUnit string, pid int) error
	// MountInfo is the cgroup mount table; nil reads /proc/self/mountinfo.
	MountInfo []byte
	Pid       int
}

// placedResourceGroup records what a successful placement established.
type placedResourceGroup struct {
	Unit string
	// Child is the agent child slice's unit name (batch-yolo-agent.slice), the value
	// podman's --cgroup-parent takes. ChildDir is its cgroup directory.
	Child    string
	ChildDir string
}

type resourceGroupOutcome struct {
	// PlanLines is the finite child plan the writer applied (or would have).
	PlanLines []string
	Placed    *placedResourceGroup
	// Err is the refusal, each ending in the next step.
	Err error
}

// userManagerQuery adapts a bounded subprocess runner to a manager query whose error
// carries the command's stderr, so a refusal can say what systemd said.
func userManagerQuery(run func(argv []string, dir string, env []string, timeout time.Duration) ExecResult) resourcegroups.ManagerQuery {
	return func(argv []string) (string, error) {
		result := run(argv, "", nil, 60*time.Second)
		switch {
		case !result.Ran:
			return "", fmt.Errorf("%s could not be run", argv[0])
		case result.Timeout:
			return "", fmt.Errorf("%s timed out", strings.Join(argv, " "))
		case result.RC != 0:
			return "", fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), result.RC, strings.TrimSpace(termsafe.Visible(result.Stderr)))
		}
		return result.Stdout, nil
	}
}

func (d resourceGroupPlacement) children(ctx context.Context, sel config.ResourceGroupSelection, iopaths resourcegroups.IOPaths) (*resourcegroups.ChildSlices, error) {
	if d.Children != nil {
		return d.Children(ctx, sel, iopaths)
	}
	mountInfo := d.MountInfo
	if mountInfo == nil {
		var err error
		if mountInfo, err = os.ReadFile("/proc/self/mountinfo"); err != nil {
			return nil, fmt.Errorf("cannot read cgroup mount table: %w", err)
		}
	}
	return resourcegroups.EnsureChildSlices(ctx, sel.Unit, iopaths, sel.AgentPercent, resourcegroups.ChildSeams{Query: d.Query, MountInfo: mountInfo, ReadFile: os.ReadFile})
}

// moveIntoAgentSlice is the real Move: busctl's StartTransientUnit with this pid in the new
// scope's PIDs and the agent child as its Slice. The call returns when the job is queued, not
// when the pid has moved. It then waits for that job's completion with `systemctl --user
// start` on the scope, which joins the pending start job and blocks until the manager reports
// it done (systemctl waits on the manager's own JobRemoved signal), so no loop or timer decides
// success; the caller reads /proc/<pid>/cgroup once afterwards. Query's timeout is only the
// generous failure guard.
func (d resourceGroupPlacement) moveIntoAgentSlice(ctx context.Context, agentUnit string, pid int) error {
	if d.Move != nil {
		return d.Move(ctx, agentUnit, pid)
	}
	if d.Query == nil {
		return errors.New("the user manager cannot be queried")
	}
	p := strconv.Itoa(pid)
	scope := "yolo-jail-launcher-" + p + ".scope"
	_, err := d.Query([]string{"busctl", "--user", "call", "org.freedesktop.systemd1",
		"/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "StartTransientUnit",
		"ssa(sv)a(sa(sv))", scope, "fail", "3",
		"PIDs", "au", "1", p,
		"Slice", "s", agentUnit,
		"Description", "s", "yolo jail launcher in " + agentUnit,
		"0"})
	if err != nil {
		return err
	}
	_, err = d.Query([]string{"systemctl", "--user", "start", scope})
	return err
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
	if _, _, err := resourcegroups.ChildUnits(sel.Unit); err != nil {
		return resourceGroupOutcome{Err: err}
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	children, err := d.children(ctx, sel, iopaths)
	if err != nil {
		out.Err = err
		return out
	}
	member := d.Member
	if member == nil {
		member = func(pid int) (string, error) { return resourcegroups.ObserveMemberDir("", "", pid) }
	}
	dir, err := member(d.Pid)
	if err != nil {
		out.Err = fmt.Errorf("cannot read which cgroup this launcher is in: %w; run yolo check --no-build", err)
		return out
	}
	if !resourcegroups.InsideGroup(dir, children.AgentDir) {
		if err := d.moveIntoAgentSlice(ctx, children.Agent, d.Pid); err != nil {
			out.Err = fmt.Errorf("your user manager would not move this launcher into %s: %w; check that `busctl --user status` works in this terminal (start yolo from a terminal inside your user manager), then run yolo check --no-build", children.Agent, err)
			return out
		}
		// The move is finished when the scope's start job is, which moveIntoAgentSlice waited
		// for, so one read decides: a launcher still outside the agent child is a refusal.
		dir, err = member(d.Pid)
		if err != nil {
			out.Err = fmt.Errorf("cannot read which cgroup this launcher is in after asking for the move: %w; run yolo check --no-build", err)
			return out
		}
		if !resourcegroups.InsideGroup(dir, children.AgentDir) {
			out.Err = fmt.Errorf("this launcher is in %s after your user manager moved it, not inside %s; check `systemctl --user status %s` and that this terminal belongs to your user manager, then run yolo check --no-build", dir, children.Agent, children.Agent)
			return out
		}
	}
	out.Placed = &placedResourceGroup{Unit: sel.Unit, Child: children.Agent, ChildDir: children.AgentDir}
	return out
}

// resourceGroupContainerArgs is the argv that asks the jail's runtime to create
// the container under the AGENT child slice (RG-R6), so the container's work is
// charged to the agent share rather than landing beside it. The request is not
// proof: the launcher placement above is what is witnessed; the container's own
// membership is requested here and left to the host witness. Only rootless podman
// with the systemd cgroup manager has a supported route: that manager creates the
// container's scope as a child of a named SLICE, so another manager or a rootful
// engine is refused with the missing step.
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
	if !strings.HasSuffix(placed.Child, ".slice") {
		return nil, fmt.Errorf("resource group %q has no agent child slice, and podman's systemd manager can only create a container under a slice; map a .slice unit, %s", placed.Unit, remove)
	}
	return []string{"--cgroup-parent=" + placed.Child}, nil
}

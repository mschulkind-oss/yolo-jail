//go:build linux

package resourcegroups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ChildAgent and ChildBatch name the two shared children Yolo manages under an
// assigned group. They are the only names the pinned writer will open.
const (
	ChildAgent = "yolo-agent"
	ChildBatch = "yolo-batch"
)

var userIOChildNames = []string{ChildAgent, ChildBatch}

// UserIOChildren is the production child writer: the two shared children of one
// assigned group, pinned by descriptor and held under the assigned-group lock
// until Close. It never writes the assigned group, an ancestor or a sibling, and
// it holds no privilege: every write is the delegated user's own.
type UserIOChildren struct {
	p    *pinnedIOChildren
	once sync.Once
}

// OpenUserIOChildren observes the selected user-manager unit, creates or reuses
// the two children under the assigned group, writes each child's io.max from the
// finite split of the assigned allowance (OQ-RG2) and reads both back. It refuses,
// naming the next step, for every unobserved or unsupported condition: a group
// that is not in the user manager, io not delegated, no finite allowance, an
// existing child holding a ceiling the plan no longer yields, or a readback that
// differs from what was written. The lock is released by Close, not before.
func OpenUserIOChildren(ctx context.Context, unit string, paths IOPaths, percent int, lockDir string) (*UserIOChildren, error) {
	p, err := openPinnedIOChildren(ctx, unit, paths, percent, lockDir)
	if err != nil {
		return nil, err
	}
	if err := p.reconcile(ctx); err != nil {
		p.close()
		return nil, err
	}
	return &UserIOChildren{p: p}, nil
}

// Close releases the descriptors and the assigned-group lock. It is idempotent.
func (c *UserIOChildren) Close() {
	c.once.Do(c.p.close)
}

// ChildPath is the filesystem path of a managed child, the value an observed
// membership must equal.
func (c *UserIOChildren) ChildPath(name string) string {
	return filepath.Join(c.p.path, name)
}

// PlaceProcess moves pid into the named child through the pinned descriptor and
// returns nil only after /proc/<pid>/cgroup names that child and the unit, the
// assigned policy and both children's identity and readback were checked again.
// A write that the kernel accepted proves nothing by itself.
func (c *UserIOChildren) PlaceProcess(ctx context.Context, name string, pid int) error {
	return c.p.place(ctx, name, pid, c.p.observeMemberDir)
}

// ReconcileUserIO creates or reuses the two children and sets their io.max, then
// releases the lock; see OpenUserIOChildren.
func ReconcileUserIO(ctx context.Context, unit string, paths IOPaths, percent int, lockDir string) error {
	children, err := OpenUserIOChildren(ctx, unit, paths, percent, lockDir)
	if err != nil {
		return err
	}
	children.Close()
	return nil
}

// reconcile writes both children's policy from the observed finite split and
// reads it back. The assigned group is never written.
func (p *pinnedIOChildren) reconcile(ctx context.Context) error {
	var order []string
	records := map[string]map[string][]string{} // child -> device -> dim=value
	for _, name := range userIOChildNames {
		records[name] = map[string][]string{}
	}
	finite := map[string]bool{}
	for _, row := range p.plan.Dimensions {
		if row.State != StateFinite {
			continue // an unconstrained dimension gets no invented cap
		}
		if _, seen := records[ChildAgent][row.Device]; !seen {
			order = append(order, row.Device)
		}
		records[ChildAgent][row.Device] = append(records[ChildAgent][row.Device], fmt.Sprintf("%s=%d", row.Dimension, row.Agent))
		records[ChildBatch][row.Device] = append(records[ChildBatch][row.Device], fmt.Sprintf("%s=%d", row.Dimension, row.Batch))
		finite[row.Device+" "+string(row.Dimension)] = true
	}
	if len(order) == 0 {
		return errors.New("assigned group has no finite I/O allowance to split: give the selected slice a finite IOReadBandwidthMax or IOWriteBandwidthMax, then run yolo check --no-build")
	}
	// A child holding a finite ceiling the plan no longer yields would keep it: a
	// formerly finite dimension that went missing is refused, never left stale.
	for _, name := range userIOChildNames {
		data, err := p.readChildPolicy(name)
		if err != nil {
			return fmt.Errorf("cannot read %s io.max (is io listed in the selected group's cgroup.subtree_control?): %w; run yolo check --no-build", name, err)
		}
		old, err := ParseIOMax(data)
		if err != nil {
			return fmt.Errorf("existing %s policy is ambiguous: %w; run yolo check --no-build", name, err)
		}
		for device, dims := range old {
			for dim, limit := range dims {
				if limit.Finite && !finite[device+" "+string(dim)] {
					return fmt.Errorf("existing %s has finite %s %s that the assigned policy no longer yields; run yolo check --no-build", name, device, dim)
				}
			}
		}
	}
	for _, name := range userIOChildNames {
		for _, device := range order {
			line := device + " " + strings.Join(records[name][device], " ") + "\n"
			if err := p.writeChildPolicy(ctx, name, []byte(line)); err != nil {
				return fmt.Errorf("cannot set %s policy: %w", name, err)
			}
		}
	}
	if err := p.observeUnit(ctx); err != nil {
		return err
	}
	for _, name := range userIOChildNames {
		if err := p.verifyChild(name); err != nil {
			return err
		}
		if err := p.verifyReadback(name); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// place writes pid into the named child's cgroup.procs through the pinned child
// descriptor, then runs the required-start admission: unit, policy, both
// children's readback, observed membership, and both identities again.
func (p *pinnedIOChildren) place(ctx context.Context, name string, pid int, observeMember func(int) (string, error)) error {
	if ctx == nil {
		return errors.New("delegated I/O context missing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if pid <= 0 || observeMember == nil {
		return errors.New("placement target unavailable; run yolo check --no-build")
	}
	if err := p.verifyChild(name); err != nil {
		return err
	}
	if err := p.observeUnit(ctx); err != nil {
		return err
	}
	if err := writeNamedAt(p.children[name].fd, "cgroup.procs", []byte(strconv.Itoa(pid)+"\n"), p.policyValidator()); err != nil {
		return placementWriteError(name, err)
	}
	return p.verifyPinnedStart(ctx, p.paths, name, pid, observeMember)
}

// placementWriteError names the next step for the refusals a move can meet. The
// kernel needs write access to the child's cgroup.procs and to the cgroup.procs
// of the nearest common ancestor of the process's current cgroup and the child.
func placementWriteError(name string, err error) error {
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("cannot move this process into %s: %w; the kernel also needs write access to the common ancestor's cgroup.procs, so start yolo from your user manager (a desktop terminal, or systemd-run --user --scope) rather than a login-session scope, then retry", name, err)
	case errors.Is(err, syscall.EBUSY):
		return fmt.Errorf("the kernel refused to move this process into %s: %w; the group's controllers or subtree make it an invalid target, run yolo check --no-build", name, err)
	}
	return fmt.Errorf("cannot move this process into %s: %w; run yolo check --no-build", name, err)
}

// observeMemberDir reads /proc/<pid>/cgroup and maps its cgroup-v2 path to the
// filesystem path through the mount table, the form VerifyDelegatedMember compares.
// Anything but exactly one unified-hierarchy line is not membership it can witness.
func (p *pinnedIOChildren) observeMemberDir(pid int) (string, error) {
	root := p.procRoot
	if root == "" {
		root = "/proc"
	}
	data, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "0::") {
		return "", errors.New("process is not on a pure cgroup-v2 hierarchy, so its membership cannot be witnessed")
	}
	controlGroup := strings.TrimPrefix(lines[0], "0::")
	if !validControlGroup(controlGroup) {
		return "", fmt.Errorf("process cgroup %q is not a usable path", controlGroup)
	}
	mountPath := p.mountInfo
	if mountPath == "" {
		mountPath = "/proc/self/mountinfo"
	}
	mountInfo, err := os.ReadFile(mountPath)
	if err != nil {
		return "", err
	}
	mount, err := cgroup2MountFor(mountInfo, controlGroup)
	if err != nil {
		return "", err
	}
	return mapControlGroup(mount, controlGroup)
}

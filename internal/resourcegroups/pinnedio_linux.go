//go:build linux

package resourcegroups

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// pinnedIOChildren is the descriptor-relative child writer behind
// OpenUserIOChildren. It observes the selected user-manager unit before
// allocating a child; the manager owns the unit and may own .control. Only the
// two named children are ours.
type pinnedIOChildren struct {
	parent   *pinnedGroup
	path     string
	unit     string
	paths    IOPaths
	percent  int
	plan     IOPlan
	children map[string]*pinnedGroup
	lock     *os.File
	// policyFS validates each child policy descriptor before a write or
	// readback. Nil is the production cgroup-v2 check; only same-package
	// regular-file fixtures set it, and openPinnedIOChildren never does.
	policyFS func(int) error
	// procRoot and mountInfo are where a member's cgroup is read from; "" is
	// /proc and /proc/self/mountinfo. Only fixtures set them.
	procRoot  string
	mountInfo string
}

func (p *pinnedIOChildren) policyValidator() func(int) error {
	if p.policyFS == nil {
		return requireCgroup2Policy
	}
	return p.policyFS
}

func (p *pinnedIOChildren) readChildPolicy(name string) ([]byte, error) {
	return readPolicyAt(p.children[name].fd, "io.max", p.policyValidator())
}

// pinnedIOUnitObserver lets fixture tests provide an already-verified manager
// identity while exercising openPinnedGroup's real cgroup-v2 filesystem guard.
var pinnedIOUnitObserver = observeDelegatedIOUnit

// pinnedIOPolicyFS is the policy-file descriptor check a constructed writer
// uses. Nil is the production cgroup-v2 check; only fixtures over ordinary
// files set it, alongside requireCgroupV2FS.
var pinnedIOPolicyFS func(int) error

func openPinnedIOChildren(ctx context.Context, unit string, paths IOPaths, percent int, lockDir string) (*pinnedIOChildren, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errors.New("delegated I/O context missing or canceled")
	}
	if percent < 1 || percent > 99 || lockDir == "" {
		return nil, errors.New("delegated I/O split or shared lock unavailable")
	}
	if err := pinnedIOUnitObserver(ctx, unit, paths); err != nil {
		return nil, err
	}
	plan := ReadAndPlanIO(IOPaths{Assigned: paths.Assigned, Devices: paths.Devices, Ancestors: paths.Ancestors}, percent, os.ReadFile)
	if !hasFiniteSplit(plan) {
		return nil, errors.New("assigned group has no safe finite I/O allowance to split: give the selected slice a finite IOReadBandwidthMax or IOWriteBandwidthMax (and nothing ambiguous above it), then run yolo check --no-build")
	}
	parentPath := filepath.Dir(paths.Assigned)
	parent, err := openPinnedGroup(parentPath, true)
	if err != nil {
		return nil, err
	}
	p := &pinnedIOChildren{parent: parent, path: parentPath, unit: unit, paths: paths, percent: percent, plan: plan, children: map[string]*pinnedGroup{}, policyFS: pinnedIOPolicyFS}
	completed := false
	defer func() {
		if !completed {
			p.close()
		}
	}()
	p.lock, err = lockPinnedIOGroup(ctx, parent, lockDir)
	if err != nil {
		return nil, err
	}
	if err := pinnedIOUnitObserver(ctx, unit, paths); err != nil {
		return nil, err
	}
	current := ReadAndPlanIO(IOPaths{Assigned: paths.Assigned, Devices: paths.Devices, Ancestors: paths.Ancestors}, percent, os.ReadFile)
	if !hasFiniteSplit(current) || !equalFinitePlans(plan, current) {
		return nil, errors.New("assigned I/O split changed under lock; run yolo check --no-build")
	}
	for _, name := range []string{"yolo-agent", "yolo-batch"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := p.parent.verifyPath(parentPath); err != nil {
			return nil, err
		}
		if err := pinnedIOUnitObserver(ctx, unit, paths); err != nil {
			return nil, err
		}
		child, openErr := parent.openChild(name, true)
		if openErr != nil {
			return nil, fmt.Errorf("cannot create or pin %s: %w", name, openErr)
		}
		p.children[name] = child
		if err := p.verifyChild(name); err != nil {
			return nil, err
		}
	}
	if err := pinnedIOUnitObserver(ctx, unit, paths); err != nil {
		return nil, err
	}
	completed = true
	return p, nil
}

// observeDelegatedIOUnit is a real, selected-only read-only manager query, not
// an injected claim of permission. An active scope/service may stop immediately
// after observation; the prototype never treats this as a durable lease.
func observeDelegatedIOUnit(ctx context.Context, unit string, paths IOPaths) error {
	if ctx == nil || ctx.Err() != nil {
		return errors.New("delegated I/O context missing or canceled")
	}
	query := func(argv []string) (string, error) {
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		output, err := exec.CommandContext(deadline, argv[0], argv[1:]...).Output()
		return string(output), err
	}
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	return observeDelegatedIOUnitWith(unit, paths, query, mountInfo, os.ReadFile)
}

// observeDelegatedIOUnitWith is observeDelegatedIOUnit over injected seams. A
// scope or service must declare Delegate=yes with io among its controllers. A
// slice cannot carry Delegate=, so a slice is admitted on the user manager's
// own identity (ResolveSelectedIOPaths finds it in the user manager only) and
// the cgroup-v2 check CheckDelegatedIO makes: io must be listed in the
// group's cgroup.controllers and cgroup.subtree_control.
func observeDelegatedIOUnitWith(unit string, paths IOPaths, query ManagerQuery, mountInfo []byte, read IOReadFile) error {
	slice := strings.HasSuffix(unit, ".slice")
	if !slice && !strings.HasSuffix(unit, ".service") && !strings.HasSuffix(unit, ".scope") {
		return errors.New("selected unit is not a slice, scope or service; name one in resource_groups, then run yolo check --no-build")
	}
	if !paths.UserManager || filepath.Base(paths.Assigned) != "io.max" || !filepath.IsAbs(paths.Assigned) {
		return errors.New("selected unit is not in your user manager; map a unit of systemctl --user, then run yolo check --no-build")
	}
	fresh, err := ResolveSelectedIOPaths(unit, query, mountInfo, read)
	if err != nil || !sameAssignedGroup(paths, fresh) {
		return errors.New("selected delegated user-manager identity or device policy changed; run yolo check --no-build")
	}
	properties := map[string]string{"Id": unit, "ActiveState": "active"}
	if !slice {
		properties["Delegate"] = "yes"
	}
	for property, expected := range properties {
		value, err := query([]string{"systemctl", "--user", "show", "--property=" + property, "--value", "--", unit})
		if err != nil || strings.TrimSpace(value) != expected {
			return fmt.Errorf("selected user-manager %s is not %q; start the unit with systemctl --user start %s, then run yolo check --no-build", property, expected, unit)
		}
	}
	if !slice {
		controllers, err := query([]string{"systemctl", "--user", "show", "--property=DelegateControllers", "--value", "--", unit})
		if err != nil || !containsController(controllers, "io") {
			return errors.New("selected unit does not delegate io: add io to its Delegate= list, then run yolo check --no-build")
		}
	}
	return CheckDelegatedIO(paths, read)
}

func containsController(value, controller string) bool {
	for _, word := range strings.Fields(value) {
		if word == controller {
			return true
		}
	}
	return false
}

func hasFiniteSplit(plan IOPlan) bool {
	found := false
	for _, row := range plan.Dimensions {
		if row.State == StateMissing || row.State == StateMax || row.State == StateAncestorOnly {
			continue
		}
		if row.State != StateFinite || row.SplitError != "" || row.AncestorUncertainty != "" || row.AncestorState == StateUnreadable || row.AncestorState == StateUnavailable || row.AncestorState == StateAmbiguous {
			return false
		}
		found = true
	}
	return found
}

func lockPinnedIOGroup(ctx context.Context, group *pinnedGroup, lockDir string) (*os.File, error) {
	root, err := pinLockRoot(lockDir)
	if err != nil {
		return nil, err
	}
	defer root.close()
	if err := root.verifyPath(lockDir); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(group.identity()))
	name := fmt.Sprintf("resource-io-%x.lock", hash)
	fd, err := syscall.Openat(root.fd, name, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Uid != uint32(os.Geteuid()) {
		file.Close()
		return nil, errors.New("unverified assigned-group lock file")
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			if err := root.verifyPath(lockDir); err != nil {
				file.Close()
				return nil, err
			}
			return file, nil
		} else if err != syscall.EWOULDBLOCK {
			file.Close()
			return nil, err
		}
		select {
		case <-deadline.Done():
			file.Close()
			return nil, fmt.Errorf("assigned-group lock unavailable: %w", deadline.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (p *pinnedIOChildren) close() {
	for _, child := range p.children {
		child.close()
	}
	p.parent.close()
	if p.lock != nil {
		p.lock.Close()
	}
}

// observeUnit re-observes the selected unit through the constructor's seam. An
// empty unit is refused, never treated as a mode that skips manager checks.
func (p *pinnedIOChildren) observeUnit(ctx context.Context) error {
	if p.unit == "" {
		return errors.New("selected delegated unit unavailable; run yolo check --no-build")
	}
	return pinnedIOUnitObserver(ctx, p.unit, p.paths)
}

func (p *pinnedIOChildren) verifyChild(name string) error {
	child := p.children[name]
	if child == nil || (name != "yolo-agent" && name != "yolo-batch") {
		return errors.New("not a Yolo-managed I/O child")
	}
	if err := p.parent.verifyPath(p.path); err != nil {
		return err
	}
	if err := p.parent.verifyChild(child, name); err != nil {
		return err
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(child.fd, &stat); err != nil {
		return err
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return errors.New("I/O child is not owned by this user")
	}
	return nil
}

// allowPolicy binds each single-device write to the already observed finite
// split. Parent limits are never rewritten; a changed allocation refuses.
func (p *pinnedIOChildren) allowPolicy(name string, data []byte) error {
	if p.percent < 1 || p.percent > 99 || !hasFiniteSplit(p.plan) {
		return errors.New("finite assigned I/O allocation unavailable")
	}
	current := ReadAndPlanIO(IOPaths{Assigned: p.paths.Assigned, Devices: p.paths.Devices, Ancestors: p.paths.Ancestors}, p.percent, os.ReadFile)
	if !equalFinitePlans(p.plan, current) {
		return errors.New("assigned I/O allocation changed; run yolo check --no-build")
	}
	parsed, err := ParseIOMax(data)
	if err != nil || len(parsed) != 1 {
		return errors.New("child I/O policy must contain exactly one finite device record")
	}
	for device, actual := range parsed {
		if !slices.Contains(p.paths.Devices, device) {
			return errors.New("child I/O device not in assigned group")
		}
		expected := map[Dimension]uint64{}
		for _, row := range p.plan.Dimensions {
			if row.Device != device || row.State != StateFinite {
				continue
			}
			if name == "yolo-agent" {
				expected[row.Dimension] = row.Agent
			} else {
				expected[row.Dimension] = row.Batch
			}
		}
		if len(expected) == 0 || len(actual) != len(expected) {
			return errors.New("child I/O dimensions do not match assigned split")
		}
		for dim, want := range expected {
			got, ok := actual[dim]
			if !ok || !got.Finite || got.Value != want {
				return errors.New("child I/O ceiling differs from finite assigned split")
			}
		}
	}
	return nil
}

// writeChildPolicy never reopens the child by its name to write. The checked
// descriptor still references the old cgroup if its name is deleted/recreated;
// the following identity/readback check forbids a required start on stale state.
// It does not defend against owner revocation after the final observation.
func (p *pinnedIOChildren) writeChildPolicy(ctx context.Context, name string, data []byte) error {
	if ctx == nil {
		return errors.New("delegated I/O context missing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.verifyChild(name); err != nil {
		return err
	}
	if err := p.allowPolicy(name, data); err != nil {
		return err
	}
	if err := p.observeUnit(ctx); err != nil {
		return err
	}
	child := p.children[name]
	if err := writeNamedAt(child.fd, "io.max", data, p.policyValidator()); err != nil {
		return fmt.Errorf("cannot write pinned child: %w", err)
	}
	if err := p.verifyChild(name); err != nil {
		return err
	}
	if err := p.observeUnit(ctx); err != nil {
		return err
	}
	got, err := p.readChildPolicy(name)
	if err != nil {
		return err
	}
	if err := verifyWrittenIOPolicy(data, got); err != nil {
		return err
	}
	return p.verifyChild(name)
}

// verifyWrittenIOPolicy checks the exact device and every written dimension,
// while permitting other well-formed rows and dimensions in the kernel readback.
// Allocation authority is checked separately by allowPolicy.
func verifyWrittenIOPolicy(requested, observed []byte) error {
	want, err := ParseIOMax(requested)
	if err != nil || len(want) != 1 {
		return errors.New("invalid written child I/O policy; run yolo check --no-build")
	}
	have, err := ParseIOMax(observed)
	if err != nil {
		return fmt.Errorf("ambiguous child I/O policy readback: %w; run yolo check --no-build", err)
	}
	for device, dims := range want {
		actual, ok := have[device]
		if !ok || len(dims) == 0 {
			return errors.New("pinned child policy readback lost the written device; run yolo check --no-build")
		}
		for dim, value := range dims {
			if got, ok := actual[dim]; !ok || got != value {
				return fmt.Errorf("pinned child policy readback differs for %s %s; run yolo check --no-build", device, dim)
			}
		}
	}
	return nil
}

// verifyPinnedStart rechecks both child identities and actual membership before
// the caller's start callback. It cannot itself authorize a placement or start.
func (p *pinnedIOChildren) verifyPinnedStart(ctx context.Context, paths IOPaths, name string, pid int, observeMember func(int) (string, error)) error {
	if ctx == nil {
		return errors.New("delegated I/O context missing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !sameAssignedGroup(paths, p.paths) {
		return errors.New("requested group differs from pinned assigned group")
	}
	if err := p.observeUnit(ctx); err != nil {
		return err
	}
	if !equalFinitePlans(p.plan, ReadAndPlanIO(IOPaths{Assigned: p.paths.Assigned, Devices: p.paths.Devices, Ancestors: p.paths.Ancestors}, p.percent, os.ReadFile)) {
		return errors.New("assigned policy changed before required start")
	}
	for _, child := range []string{"yolo-agent", "yolo-batch"} {
		if err := p.verifyChild(child); err != nil {
			return err
		}
		if err := p.verifyReadback(child); err != nil {
			return err
		}
	}
	if err := p.verifyChild(name); err != nil {
		return err
	}
	if err := p.observeUnit(ctx); err != nil {
		return err
	}
	if err := VerifyDelegatedMember(paths, filepath.Join(p.path, name), pid, observeMember); err != nil {
		return err
	}
	// Membership is observed by name. A cgroup holding a live member cannot be
	// removed, so re-verifying both pinned identities after the observation
	// binds the observed PID to the pinned child rather than to a replacement.
	for _, child := range []string{"yolo-agent", "yolo-batch"} {
		if err := p.verifyChild(child); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (p *pinnedIOChildren) verifyReadback(name string) error {
	data, err := p.readChildPolicy(name)
	if err != nil {
		return err
	}
	actual, err := ParseIOMax(data)
	if err != nil {
		return err
	}
	expected := map[string]map[Dimension]uint64{}
	for _, row := range p.plan.Dimensions {
		if row.State != StateFinite {
			continue
		}
		if expected[row.Device] == nil {
			expected[row.Device] = map[Dimension]uint64{}
		}
		if name == "yolo-agent" {
			expected[row.Device][row.Dimension] = row.Agent
		} else {
			expected[row.Device][row.Dimension] = row.Batch
		}
	}
	for device, dims := range actual {
		for dim, got := range dims {
			if !got.Finite {
				continue
			}
			want, ok := expected[device][dim]
			if !ok || got.Value != want {
				return errors.New("child has stale or unexpected finite I/O ceiling")
			}
			delete(expected[device], dim)
		}
	}
	for _, dims := range expected {
		if len(dims) != 0 {
			return errors.New("child finite I/O ceiling missing at required start")
		}
	}
	return nil
}

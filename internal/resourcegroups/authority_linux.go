//go:build linux

package resourcegroups

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ReconcileUserIO refuses real cgroup mutation: a verified parent/child name and
// pinned fd cannot exclude an authorized rename between verification and mutation.
// No existing user-manager right or cooperative lock provides that exclusion.
// Even the read-only filesystem gate runs before the unsupported refusal, so an
// ordinary directory cannot be mistaken for a cgroup delegation.
func ReconcileUserIO(ctx context.Context, paths IOPaths, percent int, lockDir string, observe func() (IOPaths, error)) error {
	group, err := openPinnedGroup(filepath.Dir(paths.Assigned), true)
	if err != nil {
		return fmt.Errorf("cannot pin assigned cgroup: %w", err)
	}
	group.close()
	return errors.New("direct cgroup I/O writer unsupported: stable-name exclusion is unavailable; inspect selection with yolo check --no-build")
}

// ReconcileDelegatedIO is a conditional adapter for fake/injected I/O, not a
// real cgroup writer. It requires a resolved USER-manager identity and a shared
// private lock, but those cannot exclude authorized parent/child renames.
// Callers must not supply a real filesystem writer until stable-name ownership
// is established independently; current mapped launches always refuse.
func ReconcileDelegatedIO(ctx context.Context, paths IOPaths, percent int, lockDir string, observe func() (IOPaths, error), read IOReadFile, mkdir func(string) error, write func(string, []byte) error) error {
	if ctx == nil {
		return errors.New("missing reconciliation context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !paths.UserManager || paths.Assigned == "" || filepath.Base(paths.Assigned) != "io.max" || !filepath.IsAbs(paths.Assigned) || lockDir == "" || observe == nil || read == nil || mkdir == nil || write == nil {
		return errors.New("verified user-manager delegated I/O authority is unavailable; inspect the selected group with yolo check --no-build")
	}
	parent := filepath.Dir(paths.Assigned)
	lockRoot, err := pinLockRoot(lockDir)
	if err != nil {
		return fmt.Errorf("unverified shared lock root: %w", err)
	}
	defer lockRoot.close()
	group, err := openPinnedGroup(parent, false)
	if err != nil {
		return fmt.Errorf("cannot identify selected group: %w", err)
	}
	defer group.close()
	hash := sha256.Sum256([]byte(group.identity()))
	// The lock is per ASSIGNED group rather than workspace or session. Never remove a lock
	// file: unlinking it can split concurrent launchers into independent writers.
	if err := lockRoot.verifyPath(lockDir); err != nil {
		return fmt.Errorf("lock root changed: %w", err)
	}
	lockName := fmt.Sprintf("resource-io-%x.lock", hash)
	lockFD, err := syscall.Openat(lockRoot.fd, lockName, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("cannot open assigned-group lock: %w", err)
	}
	file := os.NewFile(uintptr(lockFD), lockName)
	var lockStat syscall.Stat_t
	if err := syscall.Fstat(lockFD, &lockStat); err != nil {
		file.Close()
		return err
	}
	if lockStat.Mode&syscall.S_IFMT != syscall.S_IFREG || lockStat.Uid != uint32(os.Geteuid()) {
		file.Close()
		return errors.New("assigned-group lock file is not owner-owned regular storage")
	}
	defer file.Close()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("cannot lock assigned group: %w", err)
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("assigned-group lock unavailable: %w; retry after yolo check --no-build", deadline.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if err := group.verifyPath(parent); err != nil {
		return err
	}
	if err := lockRoot.verifyPath(lockDir); err != nil {
		return fmt.Errorf("shared lock root changed: %w", err)
	}
	// A resolver must repeat manager identity and hierarchy observation while this
	// group's writer lock is held. A prior unit name/path snapshot is not authority.
	if err = deadline.Err(); err != nil {
		return err
	}
	fresh, err := observe()
	if err != nil {
		return fmt.Errorf("cannot reverify selected user-manager group: %w", err)
	}
	if !sameAssignedGroup(paths, fresh) {
		return errors.New("selected manager group or hierarchy changed; retry after yolo check --no-build")
	}
	paths = fresh
	if err := CheckDelegatedIO(paths, read); err != nil {
		return err
	}
	// The parent's exact bytes and every selected ancestor are reread under the
	// group lock; unknown/new devices cannot disappear behind the old device list.
	initial, err := read(paths.Assigned)
	if err != nil {
		return fmt.Errorf("assigned policy unavailable: %w", err)
	}
	observed, err := ParseIOMax(initial)
	if err != nil {
		return fmt.Errorf("assigned policy ambiguous: %w", err)
	}
	devices, duplicates := normalizeDevices(paths.Devices)
	if len(duplicates) != 0 || len(devices) != len(observed) {
		return errors.New("assigned device identities changed or are ambiguous; retry after yolo check --no-build")
	}
	for _, device := range devices {
		if _, ok := observed[device]; !ok {
			return errors.New("assigned device identities changed; retry after yolo check --no-build")
		}
	}
	ancestorSnapshot, err := readAncestors(paths.Ancestors, read)
	if err != nil {
		return err
	}
	paths.AssignedData = initial
	paths.AssignedDataObserved = true
	plan := ReadAndPlanIO(paths, percent, read)
	if len(plan.Dimensions) == 0 {
		return errors.New("assigned group has no finite device policy; inspect it with yolo check --no-build")
	}
	lines := map[string][2][]string{}
	for _, row := range plan.Dimensions {
		if row.State == StateMissing || row.State == StateMax || row.State == StateAncestorOnly {
			continue
		} // no invented limit for an unconstrained dimension
		if row.State != StateFinite || row.SplitError != "" || row.AncestorUncertainty != "" || row.AncestorState == StateUnreadable || row.AncestorState == StateUnavailable || row.AncestorState == StateAmbiguous {
			return fmt.Errorf("%s %s policy is %s or cannot be safely split; inspect the assigned and ancestor policy with yolo check --no-build", row.Device, row.Dimension, row.State)
		}
		parts := lines[row.Device]
		parts[0] = append(parts[0], fmt.Sprintf("%s=%d", row.Dimension, row.Agent))
		parts[1] = append(parts[1], fmt.Sprintf("%s=%d", row.Dimension, row.Batch))
		lines[row.Device] = parts
	}
	if len(lines) == 0 {
		return errors.New("assigned group lacks finite I/O policy; inspect it with yolo check --no-build")
	}
	// A formerly finite dimension becoming absent is not safe: stale child values could
	// persist. Refuse instead of silently treating missing as an unconstrained dimension.
	for _, child := range []string{"yolo-agent", "yolo-batch"} {
		data, err := read(filepath.Join(parent, child, "io.max"))
		if err == nil {
			old, parseErr := ParseIOMax(data)
			if parseErr != nil {
				return fmt.Errorf("existing child policy ambiguous: %w", parseErr)
			}
			for device, dims := range old {
				for dim, value := range dims {
					if value.Finite {
						found := false
						for _, row := range plan.Dimensions {
							if row.Device == device && row.Dimension == dim && row.State == StateFinite && row.SplitError == "" {
								found = true
							}
						}
						if !found {
							return fmt.Errorf("existing child has finite %s %s absent from assigned policy; inspect with yolo check --no-build", device, dim)
						}
					}
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("child policy unreadable: %w", err)
		}
	}
	// Recheck the parent before touching children. A later owner race is caught at
	// readback and must prevent the caller's required start (never erase live children).
	before, err := read(paths.Assigned)
	if err != nil {
		return fmt.Errorf("assigned policy changed: %w", err)
	}
	if !bytes.Equal(initial, before) {
		return errors.New("assigned policy changed during reconciliation; retry after yolo check --no-build")
	}
	if err := CheckDelegatedIO(paths, read); err != nil {
		return err
	}
	if err := checkAncestors(paths.Ancestors, ancestorSnapshot, read); err != nil {
		return err
	}
	current := ReadAndPlanIO(IOPaths{Assigned: paths.Assigned, AssignedData: before, AssignedDataObserved: true, Devices: paths.Devices, Ancestors: paths.Ancestors}, percent, read)
	if !equalFinitePlans(plan, current) {
		return errors.New("assigned/ancestor policy changed during reconciliation; retry after yolo check --no-build")
	}
	if fresh, err = observe(); err != nil || !sameAssignedGroup(paths, fresh) {
		return errors.New("selected group identity changed before child writes; retry after yolo check --no-build")
	}
	for i, child := range []string{"yolo-agent", "yolo-batch"} {
		if err := deadline.Err(); err != nil {
			return err
		}
		dir := filepath.Join(parent, child)
		if err := mkdir(dir); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("cannot create or reuse delegated child: %w", err)
		}
		for _, device := range sortedDevices(lines) {
			if err := deadline.Err(); err != nil {
				return err
			}
			line := device + " " + strings.Join(lines[device][i], " ") + "\n"
			if err := write(filepath.Join(dir, "io.max"), []byte(line)); err != nil {
				return fmt.Errorf("cannot set delegated child policy: %w", err)
			}
		}
	}
	if fresh, err = observe(); err != nil || !sameAssignedGroup(paths, fresh) {
		return errors.New("selected group identity changed after child writes; no required work may start; run yolo check --no-build")
	}
	if err := CheckDelegatedIO(paths, read); err != nil {
		return err
	}
	afterData, err := read(paths.Assigned)
	if err != nil || !bytes.Equal(initial, afterData) {
		return errors.New("assigned policy changed after child writes; no required work may start; run yolo check --no-build")
	}
	if err := checkAncestors(paths.Ancestors, ancestorSnapshot, read); err != nil {
		return err
	}
	after := ReadAndPlanIO(IOPaths{Assigned: paths.Assigned, Devices: paths.Devices, Ancestors: paths.Ancestors}, percent, read)
	if !equalFinitePlans(plan, after) {
		return errors.New("assigned/ancestor policy changed after child writes; no required work may start; run yolo check --no-build")
	}
	for i, child := range []string{"yolo-agent", "yolo-batch"} {
		data, err := read(filepath.Join(parent, child, "io.max"))
		if err != nil {
			return fmt.Errorf("child readback unavailable: %w", err)
		}
		values, err := ParseIOMax(data)
		if err != nil {
			return fmt.Errorf("child readback ambiguous: %w", err)
		}
		expected := map[string]map[Dimension]IOLimit{}
		for device, parts := range lines {
			expected[device] = map[Dimension]IOLimit{}
			for _, part := range parts[i] {
				key, raw, ok := strings.Cut(part, "=")
				if !ok {
					return errors.New("invalid child allocation")
				}
				value, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					return err
				}
				expected[device][Dimension(key)] = IOLimit{Finite: true, Value: value}
			}
		}
		for device, dims := range values {
			for dim, actual := range dims {
				if !actual.Finite {
					continue
				}
				if wanted, ok := expected[device][dim]; !ok || wanted != actual {
					return fmt.Errorf("%s %s child policy readback differs; run yolo check --no-build", device, dim)
				}
				delete(expected[device], dim)
			}
		}
		for _, dims := range expected {
			if len(dims) != 0 {
				return errors.New("child policy readback missing finite allocation; run yolo check --no-build")
			}
		}
	}
	return nil
}

func sameAssignedGroup(a, b IOPaths) bool {
	return a.UserManager && b.UserManager && a.Assigned == b.Assigned && slices.Equal(a.Ancestors, b.Ancestors) && slices.Equal(a.Devices, b.Devices)
}

func readAncestors(paths []string, read IOReadFile) ([][]byte, error) {
	data := make([][]byte, 0, len(paths))
	for _, path := range paths {
		value, err := read(path)
		if err != nil {
			return nil, fmt.Errorf("assigned ancestor policy unavailable: %w", err)
		}
		data = append(data, value)
	}
	return data, nil
}
func checkAncestors(paths []string, before [][]byte, read IOReadFile) error {
	current, err := readAncestors(paths, read)
	if err != nil {
		return err
	}
	for i := range before {
		if !bytes.Equal(before[i], current[i]) {
			return errors.New("assigned ancestor policy changed during reconciliation; retry after yolo check --no-build")
		}
	}
	return nil
}

func sortedDevices(lines map[string][2][]string) []string {
	devices := make([]string, 0, len(lines))
	for device := range lines {
		devices = append(devices, device)
	}
	slices.Sort(devices)
	return devices
}
func equalFinitePlans(a, b IOPlan) bool {
	if len(a.Dimensions) != len(b.Dimensions) {
		return false
	}
	for i, x := range a.Dimensions {
		y := b.Dimensions[i]
		if x != y {
			return false
		}
	}
	return true
}

// CheckDelegatedIO is a selected-only, read-only authority preflight. It is not
// evidence of permission to write children or of execution placement.
func CheckDelegatedIO(paths IOPaths, read IOReadFile) error {
	if !paths.UserManager || read == nil || paths.Assigned == "" || filepath.Base(paths.Assigned) != "io.max" || !filepath.IsAbs(paths.Assigned) {
		return errors.New("selected unit is not verified in the user manager; run yolo check --no-build")
	}
	parent := filepath.Dir(paths.Assigned)
	controllers, err := read(filepath.Join(parent, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("assigned controllers unavailable: %w", err)
	}
	subtree, err := read(filepath.Join(parent, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("I/O delegation unavailable: %w", err)
	}
	if !slices.Contains(strings.Fields(string(controllers)), "io") || !slices.Contains(strings.Fields(string(subtree)), "io") {
		return errors.New("assigned group lacks delegated io controller; ask the owner to enable user-manager delegation, then run yolo check --no-build")
	}
	return nil
}

// VerifyDelegatedMember accepts only an observed PID in exactly one managed child;
// a requested runtime parent, successful argv, or an ancestor membership is insufficient.
func VerifyDelegatedMember(paths IOPaths, child string, pid int, observe func(int) (string, error)) error {
	parent := filepath.Dir(paths.Assigned)
	if !paths.UserManager || pid <= 0 || observe == nil || (child != filepath.Join(parent, "yolo-agent") && child != filepath.Join(parent, "yolo-batch")) {
		return errors.New("delegated member target unavailable; inspect placement with yolo check --no-build")
	}
	observed, err := observe(pid)
	if err != nil {
		return fmt.Errorf("member observation unavailable: %w", err)
	}
	if observed != child {
		return fmt.Errorf("PID %d membership differs from requested child; inspect placement with yolo check --no-build", pid)
	}
	return nil
}

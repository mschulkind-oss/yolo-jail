package configedit

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"golang.org/x/sys/unix"
)

const createdConfigMode os.FileMode = 0o600

func applyProspectiveTargeted(workspace string, target config.ResolvedEditTarget, mutation Mutation, options ApplyOptions) (*Plan, error) {
	if !target.IsProspective() || !options.Target.IsProspective() || target.Format != configdoc.TOML {
		return nil, fmt.Errorf("config target does not carry a supported prospective TOML binding")
	}
	plan, err := PlanEdit(nil, configdoc.TOML, mutation)
	if err != nil {
		return nil, fmt.Errorf("plan prospective TOML edit: %w", err)
	}
	_, _, err = validateProspectiveTarget(workspace, target, plan.Updated, true, options)
	if err != nil {
		return nil, err
	}
	if options.DryRun || plan.Noop {
		if err := target.VerifyProspectiveAbsence(); err != nil {
			return nil, err
		}
		return plan, nil
	}

	parentPath := filepath.Dir(target.PhysicalPath)
	parent, err := os.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open prospective config parent %s: %w", parentPath, err)
	}
	defer parent.Close()
	if err := lockParentDirectory(parent); err != nil {
		return nil, fmt.Errorf("lock prospective config parent %s: %w", parentPath, err)
	}
	defer unlockParentDirectory(parent)
	if options.onProspectiveLocked != nil {
		options.onProspectiveLocked()
	}
	if err := verifyHeldParent(parent, parentPath, target); err != nil {
		return nil, err
	}
	if err := target.VerifyProspectiveAbsence(); err != nil {
		return nil, err
	}
	validatedSnapshot, preexisting, err := validateProspectiveTarget(workspace, target, plan.Updated, false, options)
	if err != nil {
		return nil, err
	}
	if err := target.VerifyProspectiveAbsence(); err != nil {
		return nil, err
	}
	if err := verifyHeldParent(parent, parentPath, target); err != nil {
		return nil, err
	}

	leaf := filepath.Base(target.PhysicalPath)
	temp, tempInfo, err := writeCreationTemp(parent, leaf, plan.Updated)
	if err != nil {
		return nil, fmt.Errorf("prepare new config target %s: %w", target.PhysicalPath, err)
	}
	defer func() { _ = unix.Unlinkat(int(parent.Fd()), temp, 0) }()
	if err := verifyHeldParent(parent, parentPath, target); err != nil {
		return nil, err
	}
	if err := target.VerifyProspectiveAbsence(); err != nil {
		return nil, err
	}
	if options.onProspectiveBeforePublish != nil {
		options.onProspectiveBeforePublish()
	}
	if err := unix.Linkat(int(parent.Fd()), temp, int(parent.Fd()), leaf, 0); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("config target %s appeared before publication; preserve it and rerun the edit", target.PhysicalPath)
		}
		return nil, fmt.Errorf("publish new config target %s without replacement: %w", target.PhysicalPath, err)
	}
	if err := parent.Sync(); err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("sync published config parent: %w", err))
	}
	publishedInfo, err := statRegularAt(parent, leaf)
	if err != nil || !os.SameFile(tempInfo, publishedInfo) {
		if err == nil {
			err = fmt.Errorf("published target identity changed before verification")
		}
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	if options.onProspectivePublished != nil {
		options.onProspectivePublished()
	}
	if err := verifyHeldParent(parent, parentPath, target); err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	actualBytes, err := readOwnedRegularAt(parent, leaf, tempInfo)
	if err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("read published config for actual validation without following replacements: %w", err))
	}
	if !bytes.Equal(actualBytes, plan.Updated) {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("published config bytes differ from the validated candidate"))
	}
	actualTarget, err := config.NewExplicitEditTarget(workspace, target.PhysicalPath, target.Role, target.Format)
	if err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("bind published config as an actual file: %w", err))
	}
	input, err := config.NewConfigReadInput(target.PhysicalPath, actualBytes)
	if err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	// The published file is checked against the same pre-existing problems its plan tolerated.
	actual, err := config.ValidateExplicitEditTolerating(workspace, actualTarget, input, preexisting)
	if err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("actual published config validation failed: %w", err))
	}
	actualSnapshot, ok := actual.SnapshotJSON()
	if !ok || actualSnapshot != validatedSnapshot {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("actual published config does not match the validated prospective graph"))
	}
	if err := verifyHeldParent(parent, parentPath, target); err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	publishedAfter, err := statRegularAt(parent, leaf)
	if err != nil || !os.SameFile(tempInfo, publishedAfter) {
		if err == nil {
			err = fmt.Errorf("published config was replaced during actual verification")
		}
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	if options.Verify != nil {
		if err := options.Verify(nil, actualBytes); err != nil {
			return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, fmt.Errorf("caller verification failed: %w", err))
		}
	}
	if err := verifyHeldParent(parent, parentPath, target); err != nil {
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	finalInfo, err := statRegularAt(parent, leaf)
	if err != nil || !os.SameFile(tempInfo, finalInfo) {
		if err == nil {
			err = fmt.Errorf("published config was replaced after actual verification")
		}
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	finalBytes, err := readOwnedRegularAt(parent, leaf, tempInfo)
	if err != nil || !bytes.Equal(finalBytes, plan.Updated) {
		if err == nil {
			err = fmt.Errorf("published config bytes changed after actual verification")
		}
		return nil, rollbackCreation(parent, parentPath, leaf, tempInfo, err)
	}
	if err := unix.Unlinkat(int(parent.Fd()), temp, 0); err != nil {
		return nil, fmt.Errorf("config target %s was created and verified, but temporary sibling %s remains; inspect it before removing it: %w", target.PhysicalPath, filepath.Join(parentPath, temp), err)
	}
	if err := parent.Sync(); err != nil {
		return nil, fmt.Errorf("config target %s was created and verified, but parent sync failed; inspect the target before retrying: %w", target.PhysicalPath, err)
	}
	return plan, nil
}

// validateProspectiveTarget validates the new file's candidate bytes and returns the validated
// snapshot and the problems the config already had without the file, which it tolerated.
func validateProspectiveTarget(workspace string, target config.ResolvedEditTarget, candidate []byte, notify bool, options ApplyOptions) (string, []string, error) {
	if err := target.VerifyProspectiveAbsence(); err != nil {
		return "", nil, err
	}
	var preexisting []string
	result, err := target.ValidateProspective(workspace, candidate)
	if err != nil && config.EditProblems(err) != nil {
		// The config without this file is the one before the edit; what it already fails
		// validation for does not refuse creating the file (config.EditProblems).
		_, absentErr := target.ValidateProspective(workspace, []byte{})
		if preexisting = config.EditProblems(absentErr); preexisting != nil {
			result, err = target.ValidateProspectiveTolerating(workspace, candidate, preexisting)
		}
	}
	if err != nil {
		return "", nil, fmt.Errorf("validate prospective config: %w", err)
	}
	snapshot, ok := result.SnapshotJSON()
	if !ok {
		return "", nil, fmt.Errorf("prospective validation returned no validated snapshot")
	}
	if notify && options.OnValidated != nil {
		options.OnValidated(result)
	}
	return snapshot, preexisting, nil
}

func verifyHeldParent(parent *os.File, parentPath string, target config.ResolvedEditTarget) error {
	if err := verifyParentDirectory(parent, parentPath); err != nil {
		return err
	}
	return target.VerifyProspectiveParent()
}

func writeCreationTemp(parent *os.File, leaf string, data []byte) (string, os.FileInfo, error) {
	for range 16 {
		token := make([]byte, 12)
		if _, err := rand.Read(token); err != nil {
			return "", nil, err
		}
		name := "." + leaf + ".yolo-create-" + hex.EncodeToString(token)
		fd, err := unix.Openat(int(parent.Fd()), name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(createdConfigMode.Perm()))
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		file := os.NewFile(uintptr(fd), name)
		if err := file.Chmod(createdConfigMode); err != nil {
			_ = file.Close()
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
			return "", nil, err
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
			return "", nil, err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
			return "", nil, err
		}
		info, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil {
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
			return "", nil, statErr
		}
		if closeErr != nil {
			_ = unix.Unlinkat(int(parent.Fd()), name, 0)
			return "", nil, closeErr
		}
		return name, info, nil
	}
	return "", nil, fmt.Errorf("could not allocate a unique temporary config name")
}

// readOwnedRegularAt rejects links, special files, and replacement inodes before reading bytes.
func readOwnedRegularAt(parent *os.File, name string, own os.FileInfo) ([]byte, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("published config path is not a regular file")
	}
	if !os.SameFile(own, info) {
		return nil, fmt.Errorf("published config path no longer names the owned inode")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func statRegularAt(parent *os.File, name string) (os.FileInfo, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("published config path is not a regular file")
	}
	return info, nil
}

func rollbackCreation(parent *os.File, parentPath, leaf string, own os.FileInfo, cause error) error {
	fd := int(parent.Fd())
	targetPath := filepath.Join(parentPath, leaf)
	for range 16 {
		token := make([]byte, 12)
		if _, err := rand.Read(token); err != nil {
			return fmt.Errorf("%w; rollback could not allocate a quarantine name: %v; inspect %s before retrying", cause, err, targetPath)
		}
		quarantine := "." + leaf + ".yolo-rollback-" + hex.EncodeToString(token)
		err := renameNoReplaceAt(fd, leaf, quarantine)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("%w; published target was already removed, so the destination is absent", cause)
		}
		if err != nil {
			return fmt.Errorf("%w; could not safely quarantine the published path; inspect %s and preserve it before retrying: %v", cause, targetPath, err)
		}
		movedInfo, statErr := statRegularAt(parent, quarantine)
		if statErr == nil && os.SameFile(own, movedInfo) {
			quarantinePath := filepath.Join(parentPath, quarantine)
			if err := unix.Unlinkat(fd, quarantine, 0); err != nil {
				return fmt.Errorf("%w; owned published inode remains at %s after rollback cleanup failed; inspect it before retrying: %v", cause, quarantinePath, err)
			}
			_ = parent.Sync()
			return fmt.Errorf("%w; newly created target removed and absence restored", cause)
		}
		restoreErr := renameNoReplaceAt(fd, quarantine, leaf)
		if restoreErr != nil {
			return fmt.Errorf("%w; a replacement inode was preserved at %s because restoring it to %s would overwrite a competing entry: %v; inspect both paths before retrying", cause, filepath.Join(parentPath, quarantine), targetPath, restoreErr)
		}
		return fmt.Errorf("%w; target identity changed, so the external replacement at %s was restored and not deleted; inspect it before retrying", cause, targetPath)
	}
	return fmt.Errorf("%w; rollback quarantine names were occupied; inspect %s before retrying", cause, targetPath)
}

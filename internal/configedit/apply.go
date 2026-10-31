package configedit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"golang.org/x/sys/unix"
)

type ApplyOptions struct {
	DryRun bool
	// Target binds an effective edit to its caller-declared reader scope and format.
	Target config.ExplicitEditTarget
	// OnValidated receives detached diagnostics from the validated planned graph. It is
	// observational only; its result cannot authorize or alter the transaction.
	OnValidated func(config.ValidatedExplicitEditResult)
	// ExpectedBytes, when non-nil, refuses to apply over a preimage that changed since planning.
	ExpectedBytes []byte
	// Verify compares caller-defined effective meaning after the write. An error restores the
	// exact preimage while the same physical-target lock is held.
	Verify func(before, after []byte) error

	validateTarget func(physical string, before, planned []byte) error
	verifyTarget   func(physical string, before, after []byte) error

	// onProspective* fields are package-local creation race-test seams.
	onProspectivePublished     func()
	onProspectiveBeforePublish func()
	onProspectiveLocked        func()
}

// Apply serializes edits on the held parent directory of the symlink-resolved target, re-reads
// under that lock, and atomically replaces only the named physical file. Dry runs and semantic
// no-ops never write. Every writer of a config file (this, creation, `yolo config migrate`) takes
// that directory lock first, so it is the one lock: no lock file is made beside the config, where
// it would litter the user's work tree and a planted symlink could redirect it (RC-D34).
func Apply(path string, format configdoc.Format, mutation Mutation, options ApplyOptions) (*Plan, error) {
	return applyWithOps(path, format, mutation, options, systemFileOps)
}

type fileOps struct {
	ReadFile    func(string) ([]byte, error)
	Stat        func(string) (os.FileInfo, error)
	WriteAtomic func(string, []byte, os.FileMode) error
}

var systemFileOps = fileOps{ReadFile: os.ReadFile, Stat: os.Stat, WriteAtomic: writeAtomic}

// lockParentDirectory serializes same-parent transactions without a pathname lock artifact.
func lockParentDirectory(parent *os.File) error {
	return unix.Flock(int(parent.Fd()), unix.LOCK_EX)
}

func unlockParentDirectory(parent *os.File) {
	_ = unix.Flock(int(parent.Fd()), unix.LOCK_UN)
}

func verifyParentDirectory(parent *os.File, parentPath string) error {
	fdInfo, err := parent.Stat()
	if err != nil {
		return fmt.Errorf("stat held config parent: %w", err)
	}
	pathInfo, err := os.Stat(parentPath)
	if err != nil || !pathInfo.IsDir() || !os.SameFile(fdInfo, pathInfo) {
		return fmt.Errorf("config parent %s changed while waiting for its lock; preserve it and retry", parentPath)
	}
	return nil
}

func applyWithOps(path string, format configdoc.Format, mutation Mutation, options ApplyOptions, ops fileOps) (*Plan, error) {
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config target %s: %w", path, err)
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return nil, fmt.Errorf("resolve config target %s: %w", path, err)
	}
	parentPath := filepath.Dir(physical)
	parent, err := os.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open config target parent %s: %w", parentPath, err)
	}
	defer parent.Close()
	if err := lockParentDirectory(parent); err != nil {
		return nil, fmt.Errorf("lock config target parent %s: %w", parentPath, err)
	}
	defer unlockParentDirectory(parent)
	if err := verifyParentDirectory(parent, parentPath); err != nil {
		return nil, err
	}
	before, err := ops.ReadFile(physical)
	if err != nil {
		return nil, fmt.Errorf("read config target %s: %w", physical, err)
	}
	if options.ExpectedBytes != nil && !bytes.Equal(before, options.ExpectedBytes) {
		return nil, fmt.Errorf("config target %s changed since it was planned; reread and retry", physical)
	}
	plan, err := PlanEdit(before, format, mutation)
	if err != nil {
		return nil, fmt.Errorf("plan config edit: %w", err)
	}
	if options.validateTarget != nil {
		if err := options.validateTarget(physical, before, plan.Updated); err != nil {
			return nil, fmt.Errorf("planned effective-config validation failed: %w", err)
		}
	}
	if options.DryRun || plan.Noop {
		return plan, nil
	}
	info, err := ops.Stat(physical)
	if err != nil {
		return nil, fmt.Errorf("stat config target %s: %w", physical, err)
	}
	mode := info.Mode().Perm()
	if err := ops.WriteAtomic(physical, plan.Updated, mode); err != nil {
		return nil, fmt.Errorf("write config target %s: %w", physical, err)
	}
	actual, readErr := ops.ReadFile(physical)
	if readErr != nil {
		return nil, restoreFailure(physical, before, fmt.Errorf("reread after write: %w", readErr), ops, mode)
	}
	parsed, parseErr := configdoc.Parse(actual, format)
	if parseErr != nil {
		return nil, restoreFailure(physical, before, fmt.Errorf("written document failed to parse: %w", parseErr), ops, mode)
	}
	if !semanticEqual(parsed.Values, plan.Expected) {
		return nil, restoreFailure(physical, before, fmt.Errorf("written document has unexpected effective values (expected %s; got %s)", describe(plan.Expected), describe(parsed.Values)), ops, mode)
	}
	if options.verifyTarget != nil {
		if err := options.verifyTarget(physical, before, actual); err != nil {
			return nil, restoreFailure(physical, before, fmt.Errorf("effective-config verification failed (before: %s; after: %s): %w", describe(before), describe(actual), err), ops, mode)
		}
	}
	if options.Verify != nil {
		if err := options.Verify(before, actual); err != nil {
			return nil, restoreFailure(physical, before, fmt.Errorf("effective-config verification failed (before: %s; after: %s): %w", describe(before), describe(actual), err), ops, mode)
		}
	}
	return plan, nil
}

func restoreFailure(path string, before []byte, cause error, ops fileOps, mode os.FileMode) error {
	if err := ops.WriteAtomic(path, before, mode); err != nil {
		return fmt.Errorf("%w; restoring the original bytes also failed: %v", cause, err)
	}
	return fmt.Errorf("%w; original bytes restored", cause)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".edit-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func describe(v any) string {
	if b, ok := v.([]byte); ok {
		return strings.TrimSpace(string(b))
	}
	return fmt.Sprintf("%#v", v)
}

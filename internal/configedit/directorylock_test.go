package configedit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"golang.org/x/sys/unix"
)

func probeDirectoryLocked(t *testing.T, path string) {
	t.Helper()
	probe, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	err = unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		unix.Flock(int(probe.Fd()), unix.LOCK_UN)
		t.Fatal("borrowed lock was accidentally unlocked")
	}
	if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		t.Fatal(err)
	}
}
func TestDirectoryLockHandoffRetainsOwnershipAcrossApplyAndFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("enabled = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "enabled"}}, Value: true}
	var retained *DirectoryLock
	locked, err := WithDirectoryLock(dir, false, func(held *DirectoryLock) error {
		retained = held
		plan, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{HeldParentLock: held})
		if err != nil || plan.Noop {
			t.Fatalf("borrowed apply: %+v %v", plan, err)
		}
		probeDirectoryLocked(t, dir)
		mutation.Value = false
		_, err = Apply(path, configdoc.TOML, mutation, ApplyOptions{HeldParentLock: held, Verify: func(_, _ []byte) error { return errors.New("injected failure") }})
		if err == nil {
			t.Fatal("expected verification failure")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "enabled = true\n" {
			t.Fatalf("rollback=%q %v", data, err)
		}
		probeDirectoryLocked(t, dir)
		// A semantic no-op and dry run still leave ownership with the caller.
		mutation.Value = true
		if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{HeldParentLock: held}); err != nil {
			t.Fatal(err)
		}
		mutation.Value = false
		if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{HeldParentLock: held, DryRun: true}); err != nil {
			t.Fatal(err)
		}
		probeDirectoryLocked(t, dir)
		return nil
	})
	if err != nil || !locked {
		t.Fatalf("outer lock: %t %v", locked, err)
	}
	if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{HeldParentLock: retained}); err == nil {
		t.Fatal("expired borrowed lock accepted")
	}
	locked, err = WithDirectoryLock(dir, true, func(*DirectoryLock) error { return nil })
	if err != nil || !locked {
		t.Fatalf("outer descriptor retained after return: %t %v", locked, err)
	}
}
func TestDirectoryLockHandoffLocksDistinctTargetIndependently(t *testing.T) {
	heldDir, targetDir := t.TempDir(), t.TempDir()
	path := filepath.Join(targetDir, "config.toml")
	if err := os.WriteFile(path, []byte("enabled = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	locked, err := WithDirectoryLock(heldDir, false, func(held *DirectoryLock) error {
		_, err := Apply(path, configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "enabled"}}, Value: true}, ApplyOptions{HeldParentLock: held, Verify: func(_, _ []byte) error {
			probeDirectoryLocked(t, targetDir)
			probeDirectoryLocked(t, heldDir)
			return nil
		}})
		probeDirectoryLocked(t, heldDir)
		available, probeErr := WithDirectoryLock(targetDir, true, func(*DirectoryLock) error { return nil })
		if probeErr != nil || !available {
			t.Fatalf("distinct target lock retained: %t %v", available, probeErr)
		}
		return err
	})
	if err != nil || !locked {
		t.Fatalf("distinct apply: %t %v", locked, err)
	}
}
func TestDirectoryLockHandoffRejectsReplacedParent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "parent")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("enabled = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := WithDirectoryLock(dir, false, func(held *DirectoryLock) error {
		if err := os.Rename(dir, dir+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("enabled = false\n"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Apply(path, configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "enabled"}}, Value: true}, ApplyOptions{HeldParentLock: held})
		if err == nil {
			t.Fatal("replaced caller-held parent accepted")
		}
		probeDirectoryLocked(t, dir+"-old")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDirectoryLockHandoffProspectiveTargetedCreation(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	mutation := Mutation{Action: Add, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}
	target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetProject})
	if err != nil {
		t.Fatal(err)
	}
	_, err = WithDirectoryLock(workspace, false, func(held *DirectoryLock) error {
		result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{HeldParentLock: held})
		if err != nil || result.Plan == nil {
			t.Fatalf("creation handoff: %+v %v", result, err)
		}
		probeDirectoryLocked(t, workspace)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryLockHandoffErrorAndCancellationReleaseDescriptor(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			injected := errors.New("injected callback error")
			var token *DirectoryLock
			locked, err := WithDirectoryLock(dir, false, func(held *DirectoryLock) error {
				token = held
				if cancelled {
					cancel()
					return ctx.Err()
				}
				return injected
			})
			want := injected
			if cancelled {
				want = context.Canceled
			}
			if !locked || !errors.Is(err, want) {
				t.Fatalf("callback result: %t %v", locked, err)
			}
			if token.parent != nil {
				t.Fatal("completed callback retained live descriptor")
			}
			locked, err = WithDirectoryLock(dir, true, func(*DirectoryLock) error { return nil })
			if err != nil || !locked {
				t.Fatalf("callback failure retained lock: %t %v", locked, err)
			}
		})
	}
}

func TestDirectoryLockHandoffProspectiveRollbackKeepsBorrowedLock(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	mutation := Mutation{Action: Add, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}
	target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetProject})
	if err != nil {
		t.Fatal(err)
	}
	_, err = WithDirectoryLock(workspace, false, func(held *DirectoryLock) error {
		_, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{HeldParentLock: held, Verify: func(_, _ []byte) error { return errors.New("injected prospective failure") }})
		if err == nil {
			t.Fatal("prospective failure ignored")
		}
		if _, err := os.Stat(target.PhysicalPath); !os.IsNotExist(err) {
			t.Fatalf("failed creation remained: %v", err)
		}
		probeDirectoryLocked(t, workspace)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

package loopholes

// mountsentinel.go is the MOUNT SENTINEL (a term this file coins): the inert file a manifest
// names as its whole `state_files` list when NOTHING of its state dir may cross into a jail.
//
// WHY A FILE AT ALL. An absent or empty `state_files` mounts the loophole's entire state
// directory read-only (runtimeArgsWith, OQ-T6 in docs/reference/loophole-transport.md), and the
// state dirs of aws-auth and openai-auth hold credentials.json — every credential the host
// service minted. So those manifests keep the list nonempty, and its one entry names a file
// that carries nothing: a jail that reads it learns only that the marker exists.
//
// WHO WRITES IT. The launcher, before it assembles the mounts, for EVERY loophole the argv would
// mount state files for whose list names the marker — keyed on that declaration, never on a
// loophole name (AGENTS.md: core knows no tool by name). It was openai-auth's alone until
// 2026-10-05, written by a function gated on that loophole's name, so aws-auth, which declares
// the same marker, warned "skipping state file, host source missing" on every launch while its
// manifest and internal/awsauth both said a launcher created it.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// MountSentinelName is the mount sentinel's file name, relative to the loophole's state dir.
const MountSentinelName = ".mount-sentinel"

// mountSentinelContent is the marker's whole body: fixed and token-free, so a jail reading it
// learns nothing.
const mountSentinelContent = "yolo-loophole-mount-sentinel-v1\n"

// DeclaresMountSentinel reports whether the loophole's `state_files` names the mount sentinel.
func (l *Loophole) DeclaresMountSentinel() bool {
	return slices.Contains(l.StateFiles, MountSentinelName)
}

// PrepareMountSentinels writes the mount sentinel for every record in `from` whose state files
// this runtime's argv would mount (the same skip the mounts loop makes: admitsJailSideEffects
// under this Set's gate, and a jail daemon) and whose `state_files` names it. Each error names
// its loophole; the caller reports them, and a record it could not write for is then reported
// again by the argv's own missing-state-file warning.
//
// Call it BEFORE RuntimeArgsFor and its siblings, which stay side-effect free: this is the one
// write the launch makes into yolo's own state tree on the way to the argv.
func (s Set) PrepareMountSentinels(from []*Loophole, runtime string) []error {
	var errs []error
	for _, m := range from {
		if m.JailDaemon == nil || !m.DeclaresMountSentinel() {
			continue
		}
		if !admitsJailSideEffects(m, runtime, &s, "PrepareMountSentinels") {
			continue
		}
		if err := writeMountSentinel(m.StateDir()); err != nil {
			errs = append(errs, fmt.Errorf("loophole %s: could not write its mount sentinel in %s: %w",
				m.Name, m.StateDir(), err))
		}
	}
	return errs
}

// writeMountSentinel replaces dir's sentinel atomically, creating dir 0700 if it is absent.
//
// REPLACED ON EVERY LAUNCH, by rename. Besides never exposing torn content, the rename swaps out
// a pre-existing symlink rather than following it into some other host file the bind mount
// would then hand the jail.
func writeMountSentinel(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure the state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, MountSentinelName+".*")
	if err != nil {
		return fmt.Errorf("create a temporary marker: %w", err)
	}
	tmpPath := tmp.Name()
	// Gone after the rename below; on any earlier return this removes the half-written file.
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure the temporary marker: %w", err)
	}
	if _, err := tmp.WriteString(mountSentinelContent); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the temporary marker: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync the temporary marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the temporary marker: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, MountSentinelName)); err != nil {
		return fmt.Errorf("replace the marker: %w", err)
	}
	return nil
}

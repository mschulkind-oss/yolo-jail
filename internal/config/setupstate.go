package config

// setupstate.go places the launch records that belong to one setup's jail under that setup's
// state directory, <workspace>/.yolo/setups/<name>/ (named-setups-plan.md slice 6a-iii,
// SUP-D37): the boot baseline (config-boot.json, drift.go) and the assembled copy with its
// origins (config-assembled.json, config-assembled-origins.json, assembled.go). Two setups of one
// project each run a jail of their own, so each has its own record of the config it started
// from; one file under .yolo would be the last launch's, whichever setup it was.
//
// Every function here takes the setup's name. "" means THIS SESSION'S setup: the one its launch
// exported as YOLO_SETUP (SUP-D4), which is how an in-jail reader (`yolo config drift`, the
// config load that reads the assembled copy) finds its own jail's records. A session that names
// none is an error that says so (ErrNoSessionSetup), never a silent read of another file.
//
// Writes go through paths.WriteSetupStateFile, reads through paths.ReadSetupStateFile: no link
// at `.yolo`, `setups`, the setup's directory or the file is followed, since the jail can write
// all four.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// The leaf names of the per-setup launch records.
const (
	bootBaselineLeaf     = "config-boot.json"
	assembledConfigLeaf  = "config-assembled.json"
	assembledOriginsLeaf = "config-assembled-origins.json"
)

// ErrNoSessionSetup is a reader's answer, in a session whose launch named no setup, about a record
// it would read from this session's setup directory: there is no telling which setup's it is.
var ErrNoSessionSetup = errors.New("this session names no setup (YOLO_SETUP is unset), so its " +
	"launch records under .yolo/setups cannot be found; launch the jail again with this yolo")

// SessionSetupName is the setup this session's launch exported (YOLO_SETUP, SUP-D4):
// ErrNoSessionSetup when it is unset, and an error naming the value when it is not a setup name.
func SessionSetupName() (string, error) {
	name := os.Getenv(setup.EnvVar)
	if name == "" {
		return "", ErrNoSessionSetup
	}
	if !setup.ValidName(name) {
		return "", fmt.Errorf("%s=%q is not a setup name, so this session's launch records cannot be found", setup.EnvVar, name)
	}
	return name, nil
}

// recordSetup is name, or this session's setup when name is "" (SessionSetupName).
func recordSetup(name string) (string, error) {
	if name != "" {
		if !setup.ValidName(name) {
			return "", fmt.Errorf("%q is not a setup name", name)
		}
		return name, nil
	}
	return SessionSetupName()
}

// setupRecordPath is <workspace>/.yolo/setups/<setup>/<leaf>, for a path a message names. An
// unresolvable setup names the directory every setup's state is under.
func setupRecordPath(workspace, setupName, leaf string) string {
	ws := workspaceOrCwd(workspace)
	name, err := recordSetup(setupName)
	if err != nil {
		return filepath.Join(paths.SetupsDir(ws), "<setup>", leaf)
	}
	return filepath.Join(paths.SetupStateDir(ws, name), leaf)
}

// readSetupRecord reads leaf of setupName's state in workspace. A record that is not there is an
// error os.IsNotExist answers true for.
func readSetupRecord(workspace, setupName, leaf string) ([]byte, error) {
	name, err := recordSetup(setupName)
	if err != nil {
		return nil, err
	}
	return paths.ReadSetupStateFile(workspaceOrCwd(workspace), name, leaf)
}

// writeSetupRecord writes canonical, newline-terminated, as leaf of setupName's state in
// workspace, creating the setup's directory.
func writeSetupRecord(workspace, setupName, leaf, canonical string) error {
	name, err := recordSetup(setupName)
	if err != nil {
		return err
	}
	return paths.WriteSetupStateFile(workspaceOrCwd(workspace), name, leaf, []byte(canonical+"\n"), 0o644)
}

// isNotExist is os.IsNotExist through wrapping.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// LegacyConfigBootPath is <workspace>/.yolo/config-boot.json, where a launch of an older yolo,
// which keyed every jail of a project by its workspace alone, wrote the boot baseline. Only `yolo
// config migrate` and the workspace marker read it now.
func LegacyConfigBootPath(workspace string) string {
	return filepath.Join(paths.WorkspaceStateDir(workspaceOrCwd(workspace)), bootBaselineLeaf)
}

// BootBaselineMarked reports whether workspace holds a boot baseline any launch wrote, a
// setup's or the legacy one: a regular file, followed through no link.
func BootBaselineMarked(workspace string) bool {
	ws := workspaceOrCwd(workspace)
	if r, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(ws)); err == nil {
		fi, err := r.Lstat(bootBaselineLeaf)
		r.Close()
		if err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	for _, name := range paths.SetupStateNames(ws) {
		if _, err := paths.ReadSetupStateFile(ws, name, bootBaselineLeaf); err == nil {
			return true
		}
	}
	return false
}

// BootBaselineSetups lists, sorted, the setups of workspace whose state holds a boot baseline.
func BootBaselineSetups(workspace string) []string {
	ws := workspaceOrCwd(workspace)
	var out []string
	for _, name := range paths.SetupStateNames(ws) {
		if _, err := paths.ReadSetupStateFile(ws, name, bootBaselineLeaf); err == nil {
			out = append(out, name)
		}
	}
	return out
}

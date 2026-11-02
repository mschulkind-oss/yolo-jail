package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// migraterecord.go holds the seams `yolo config migrate` (internal/configmigrate) needs into this
// package: one authored-graph read, and read/compute/write access to the two records only yolo
// reads, the host-side approval record and the workspace's boot baseline
// (docs/design/config-migration.md §4 items 1 and 5).
//
// They add no behavior of their own. Each one calls the same reader, projection and writer the
// launch uses, so a record respelled through them holds exactly the bytes the next launch's gate
// computes, and the equality proof compares what the readers produce rather than what a second
// copy of their rules would say.

// MigrationGraphRead is one migration-time read of the authored config: the user graph, the
// workspace graph, and the workspace graph merged over the user graph. A half that was not asked
// for is an empty map.
type MigrationGraphRead struct {
	User, Workspace, Composed *jsonx.OrderedMap
}

// ReadMigrationGraph reads the authored user graph whose top-level file is userPath (its selected
// sibling, its includes and, for a TOML file, its config.d fragments) and the workspace's project
// and local graphs, strictly, through the format-aware readers a launch composes. It folds in no
// inherited launch file, no --user-layer and no per-workspace host file, and never substitutes a
// jail's assembled copy: a migration compares only what the authored files say. An empty userPath
// or workspace skips that half.
func ReadMigrationGraph(userPath, workspace string) (*MigrationGraphRead, error) {
	var problems []string
	warn := func(msg string) { problems = append(problems, msg) }
	out := &MigrationGraphRead{User: jsonx.NewOrderedMap(), Workspace: jsonx.NewOrderedMap()}
	if userPath != "" {
		selected, label, readable, err := resolveUserConfigPath(userPath)
		if err != nil {
			return nil, err
		}
		seen := map[string]struct{}{}
		cfg, node, err := loadWithIncludesScope(selected, label, true, warn, seen, false, SchemaScopeUserOnly, readAt{role: editRoleUser})
		if err != nil {
			return nil, err
		}
		if readable {
			cfg, _, err = mergeUserConfigFragmentsWithInputUse(cfg, node, filepath.Dir(selected), true, warn, seen, false, nil, nil)
			if err != nil {
				return nil, err
			}
		}
		if cfg != nil {
			out.User = cfg
		}
	}
	if workspace != "" {
		ws, _, err := loadReadableWorkspaceConfigOverWithInputUse(workspace, true, warn, false, jsonx.NewOrderedMap(), nil, nil, nil)
		if err != nil {
			return nil, err
		}
		out.Workspace = ws
		composed, _, err := loadReadableWorkspaceConfigOverWithInputUse(workspace, true, warn, false, jsonx.DeepCopyMap(out.User), nil, nil, nil)
		if err != nil {
			return nil, err
		}
		out.Composed = composed
	} else {
		out.Composed = jsonx.DeepCopyMap(out.User)
	}
	if len(problems) > 0 {
		return nil, configErr("%s", strings.Join(problems, "; "))
	}
	return out, nil
}

// UserConfigIsTOML is whether the user config the reader selects beside userPath (config.toml,
// else config.jsonc or config.json) is the TOML one: the format that makes `packs` the only
// switch and `[allow.<pack>]` a pack's settings. False when the directory cannot be read.
func UserConfigIsTOML(userPath string) bool {
	if userPath == "" {
		return false
	}
	_, _, readable, err := resolveUserConfigPath(userPath)
	return err == nil && readable
}

// ReadMigrationLayer is ReadMigrationGraph for a --user-layer file a migration converts on its
// own (RC-D46): the user graph whose top-level file is userPath, with the layer at layerPath
// merged over it exactly as a launch passed `--user-layer layerPath` merges it, strictly. No
// workspace is read: a layer is user scope. User and Composed are the merged read; Workspace is
// empty.
func ReadMigrationLayer(userPath, layerPath string) (*MigrationGraphRead, error) {
	read, err := ReadMigrationGraph(userPath, "")
	if err != nil {
		return nil, err
	}
	var problems []string
	warn := func(msg string) { problems = append(problems, msg) }
	layer, _, err := loadWithIncludesScope(layerPath, "--user-layer "+layerPath, true, warn, nil, false, SchemaScopeUserOnly, readAt{role: editRoleUser})
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, configErr("%s", strings.Join(problems, "; "))
	}
	if layer != nil && layer.Len() > 0 {
		// As the launch merges it (applyReadableUserLayerWithInputUse): a legacy layer over a TOML
		// user config is read in the TOML spelling, so the old side of the proof is what the
		// launch reads and not a map holding both spellings of one key.
		layerIsTOML := strings.EqualFold(filepath.Ext(layerPath), ".toml")
		if _, _, readableBase, err := resolveUserConfigPath(userPath); userPath != "" && err == nil && readableBase && !layerIsTOML && read.User.Len() > 0 {
			layer, _ = projectLegacyLayerOverReadable(layer, nil, read.User)
		}
		read.User, _ = mergeReadableConfigMode(read.User, layer, nil, nil, nil, layerIsTOML)
	}
	read.Composed = jsonx.DeepCopyMap(read.User)
	return read, nil
}

// ApprovalRecordPart is the approval record's config part for workspace as the launch's gate
// computes it now: ReadWorkspaceForGate, the record projection with each mount's resolved
// destination, and SnapshotJSON. It is the exact text the next launch compares the record with.
func ApprovalRecordPart(workspace string) (string, error) {
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		return "", err
	}
	return SnapshotJSON(approvalConfigPartWith(read.Config, resolvedHosts(read.Mounts())))
}

// ReadApprovalRecord returns the host-side approval record's config part for workspace as the
// gate reads it (trailing whitespace stripped), and whether one exists.
func ReadApprovalRecord(workspace string) (string, bool, error) {
	data, err := os.ReadFile(ApprovalSnapshotPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return pyRstrip(string(data)), true, nil
}

// WriteApprovalRecord replaces the approval record's config part with part, through the writer
// the gate uses. Respelling is not approval: the caller writes only a part it proved holds the
// same meaning as the record it replaces.
func WriteApprovalRecord(workspace, part string) error {
	return writeSnapshot(ApprovalSnapshotPath(workspace), part)
}

// BootBaselinePart is the boot baseline a fresh launch of workspace would write now:
// SnapshotJSON of LoadWorkspaceConfig, as the launch reads it (preflight's
// writeLaunchConfigArtifacts).
func BootBaselinePart(workspace string) (string, error) {
	cfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil {
		return "", err
	}
	return SnapshotJSON(cfg)
}

// ReadBootBaseline returns the workspace's boot baseline as WorkspaceConfigDrift compares it
// (trailing whitespace stripped), and whether one exists.
func ReadBootBaseline(workspace string) (string, bool, error) {
	data, err := os.ReadFile(WorkspaceConfigBootPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return pyRstrip(string(data)), true, nil
}

// WriteBootBaseline replaces the workspace's boot baseline with part, beneath the root on its
// `.yolo` exactly as a launch writes it (writeWorkspaceSnapshot).
func WriteBootBaseline(workspace, part string) error {
	return writeWorkspaceSnapshot(workspace, WorkspaceConfigBootPath(workspace), part)
}

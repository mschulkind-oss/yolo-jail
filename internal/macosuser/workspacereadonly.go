package macosuser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// workspaceReadonlyRels is config.workspace_readonly as this backend's profile renders it:
// the declared entries and, whenever ANY entry is declared, the workspace config file the
// loader reads (config.ResolveWorkspaceConfigPath: yolo-jail.jsonc, or yolo-jail.json where
// that is the file). The second half is the lock the container backends perform beside the
// declared entries (internal/cli/run's workspaceReadonlyMountArgs), so a session cannot switch
// its own protection off; this backend rendered only the declared entries until 2026-10-04.
//
// The container's trigger, exactly: a non-empty list, whatever its entries' validity, and a
// config file that exists. Neither yolo-jail.local.jsonc nor an included file is locked, on
// either backend.
//
// A SYMLINKED CONFIG LOCKS ITS TARGET TOO, when the target sits in the workspace: the kernel
// resolves a write through the link before the policy is consulted, so a deny on the link's
// name alone stops only an unlink or a rename of the link. A target outside the workspace is
// outside its write allow already. ⚠ A HARD LINK is the residual: a link the session makes to
// the config, under a name of its own, is a path no rule here names (recorded, not asserted,
// by integration/macosuserworkspacereadonly_test.go).
func workspaceReadonlyRels(workspace string, cfg *jsonx.OrderedMap) []string {
	rels := cfgStrList(cfg, "workspace_readonly")
	if len(rels) == 0 {
		return rels
	}
	p, name := config.ResolveWorkspaceConfigPath(workspace, config.WorkspaceConfigName)
	if _, err := os.Stat(p); err != nil {
		return rels
	}
	out := append([]string(nil), rels...)
	add := func(rel string) {
		if !slices.Contains(out, rel) {
			out = append(out, rel)
		}
	}
	add(name)
	if target, err := filepath.EvalSymlinks(p); err == nil && target != p {
		if rel, err := filepath.Rel(workspace, target); err == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			add(filepath.ToSlash(rel))
		}
	}
	return out
}

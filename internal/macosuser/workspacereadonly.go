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
// declared entries (internal/cli/run's workspaceReadonlyMountArgs), so a session cannot edit its
// own protection out of that file; this backend rendered only the declared entries until
// 2026-10-04.
//
// The container's trigger, exactly: a non-empty list, whatever its entries' validity, and a
// config file that exists.
//
// ⚠ IT LOCKS THAT ONE FILE, and the protection can be switched off without touching it, on
// either backend. yolo-jail.local.jsonc is merged over it and is not locked (a
// `"workspace_readonly": null` there turns the key off); nor is a file include_if_found pulls in,
// which wins over the file naming it; and a new yolo-jail.jsonc beside a locked yolo-jail.json
// is read instead of it, since ResolveWorkspaceConfigPath tries the `.jsonc` name first. What
// catches each is the config-change approval at the next fresh launch
// (docs/reference/config-safety.md), which diffs the MERGED workspace config.
//
// A SYMLINKED CONFIG LOCKS ITS TARGET TOO, wherever the target sits: the kernel resolves a
// write through the link before the policy is consulted, so a deny on the link's name alone
// stops only an unlink or a rename of the link. A target in the workspace joins rels under its
// workspace-relative path. One outside it is returned in targets, by its physical path, because
// outside the workspace is not outside the write allow: the fixed writable roots
// (profileWritableRoots: /tmp and /var/folders with their /private twins, and /dev), the
// sandbox home and every read-write context mount's source are allowed as well. The container
// backends bind the config `:ro`, and a bind resolves its source through the link, which locks
// the content wherever it lives. A deny can only narrow the profile, so this one is emitted
// whether or not the target is under a writable root.
// targets is a list of its own because readonlyDenies drops every absolute USER entry, and that
// refusal stays.
//
// ⚠ Two more gaps are recorded for this backend. A HARD LINK the session makes to the config,
// under a name of its own, is a path no rule here names (recorded, not asserted, by
// integration/macosuserworkspacereadonly_test.go). So is a link in the MIDDLE of a chain of
// links: only the config's own name and the chain's final target are named. The approval above
// catches both as well, since each changes the config the next launch reads.
func workspaceReadonlyRels(workspace string, cfg *jsonx.OrderedMap) (rels, targets []string) {
	declared := cfgStrList(cfg, "workspace_readonly")
	if len(declared) == 0 {
		return declared, nil
	}
	p, name := config.ResolveWorkspaceConfigPath(workspace, config.WorkspaceConfigName)
	if _, err := os.Stat(p); err != nil {
		return declared, nil
	}
	rels = append([]string(nil), declared...)
	add := func(rel string) {
		if !slices.Contains(rels, rel) {
			rels = append(rels, rel)
		}
	}
	add(name)
	if target, err := filepath.EvalSymlinks(p); err == nil && target != p {
		rel, err := filepath.Rel(workspace, target)
		switch {
		case err == nil && rel == ".":
			// The link names the workspace itself: no file for the loader to read, and the
			// workspace is not this list's to lock.
		case err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)):
			add(filepath.ToSlash(rel))
		case filepath.IsAbs(target):
			targets = append(targets, filepath.Clean(target))
		}
	}
	return rels, targets
}

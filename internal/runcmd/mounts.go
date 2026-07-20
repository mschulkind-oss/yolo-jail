package runcmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// workspaceReadonlyMountArgs ports _workspace_readonly_mount_args: the
// `-v …:ro` overlays for config.workspace_readonly. Each configured sub-path is
// overlaid onto the writable /workspace mount; when any entry is active the
// yolo-jail.jsonc config file itself is also locked. Entries that escape the
// workspace or don't exist are skipped with a warning. On Apple Container the
// :ro suffix is silently ignored, so a loud warning is printed (the protection
// can't be enforced, and the paths can't be skipped since they're inside the
// writable /workspace).
func (o *Options) workspaceReadonlyMountArgs(cfg *jsonx.OrderedMap, rt string) []string {
	entries := cfgStrList(cfg, "workspace_readonly")
	if len(entries) == 0 {
		return nil
	}
	out := o.pr(o.Stdout)
	if rt == "container" {
		out.print("[bold yellow]Warning: workspace_readonly is NOT enforced on Apple " +
			"Container[/bold yellow] — it ignores read-only bind mounts " +
			"(apple/container#889), so these paths stay writable inside the " +
			"jail. Use `YOLO_RUNTIME=podman` to actually protect host-executed " +
			"source: " + strings.Join(entries, ", "))
	}

	var args []string
	wsConfigFile := filepath.Join(o.Workspace, "yolo-jail.jsonc")
	if fileExists(wsConfigFile) {
		args = append(args, "-v", wsConfigFile+":/workspace/yolo-jail.jsonc:ro")
	}
	workspaceRoot := resolvePath(o.Workspace)
	for _, rel := range entries {
		hostSubpath := resolvePath(filepath.Join(o.Workspace, rel))
		if !isUnderOrEqual(hostSubpath, workspaceRoot) {
			out.print("[yellow]Warning: workspace_readonly path escapes workspace, skipping: " + rel + "[/yellow]")
			continue
		}
		if !fileExists(hostSubpath) {
			out.print("[yellow]Warning: workspace_readonly path does not exist, skipping: " + rel + "[/yellow]")
			continue
		}
		args = append(args, "-v", hostSubpath+":/workspace/"+rel+":ro")
	}
	return args
}

// venvShadowMountArgs ports _venv_shadow_mount_args: per-side shadow mounts over
// /workspace so derived state (venvs) never crosses the host↔jail boundary. The
// shadow set is `.venv` ∪ the mise-config venv path ∪ config per_side_paths.
// Backing dirs live under wsState/venv-shadows/ ("/" → "__"). Entries must be
// relative, template-free workspace sub-paths (offenders skipped with a
// warning); a host path that is a file or symlink is also skipped (a dir mount
// over a non-dir aborts container creation). Directory mounts only.
func (o *Options) venvShadowMountArgs(cfg *jsonx.OrderedMap, wsState string) []string {
	rels := map[string]struct{}{".venv": {}}
	if miseVenv, ok := MiseConfigVenvPathFromDir(o.Workspace); ok && miseVenv != "" {
		rels[miseVenv] = struct{}{}
	}
	for _, e := range cfgStrList(cfg, "per_side_paths") {
		rels[e] = struct{}{}
	}

	sorted := make([]string, 0, len(rels))
	for r := range rels {
		sorted = append(sorted, r)
	}
	sort.Strings(sorted)

	out := o.pr(o.Stdout)
	var args []string
	for _, rel := range sorted {
		if !ValidPerSideRel(rel) {
			out.print("[yellow]Warning: invalid per-side path, skipping: " + pyReprStr(rel) + "[/yellow]")
			continue
		}
		hostPath := filepath.Join(o.Workspace, rel)
		if isSymlink(hostPath) || (fileExists(hostPath) && !isDir(hostPath)) {
			out.print("[yellow]Warning: workspace path " + pyReprStr(rel) + " is a file or symlink " +
				"— cannot shadow it per-side; the jail will see the host's " +
				"entry[/yellow]")
			continue
		}
		backing := filepath.Join(wsState, "venv-shadows", strings.ReplaceAll(rel, "/", "__"))
		_ = os.MkdirAll(backing, 0o755)
		args = append(args, "-v", backing+":/workspace/"+rel)
	}
	return args
}

// --- fs helpers used by the mount builders ---

func resolvePath(p string) string {
	if r, err := filepath.Abs(p); err == nil {
		if evaled, err := filepath.EvalSymlinks(r); err == nil {
			return evaled
		}
		return r
	}
	return p
}

// isUnderOrEqual reports whether child is base or a descendant of base (Python's
// Path.relative_to not raising ValueError).
func isUnderOrEqual(child, base string) bool {
	if child == base {
		return true
	}
	rel, err := filepath.Rel(base, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// pyReprStr renders a Python repr() of a string ({rel!r}) — single-quoted with
// backslash escapes. Matches the {rel!r} in the skip warnings.
func pyReprStr(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

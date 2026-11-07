package configmigrate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/gitexclude"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Ignore is one name a migration makes that git would not keep out of commits the way it kept
// the old name out (config-migration.md §4). The migration never writes an ignore file for the
// user — not the tracked .gitignore, not the clone's .git/info/exclude (RC-D71): it says the
// one line to add, and the user decides where.
type Ignore struct {
	// Old and New are the old name and its converted twin.
	Old, New string
	// Hint is the one line the plan and the closing report say.
	Hint string
}

// planIgnores decides an Ignore for each name a migration makes from an old one: the converted
// file and the <name>.pre-migrate backup of every file it replaces (RC-D34: a backup holds the
// same private bytes its original did; for a linked name, the moved link). The rule is git's own
// answer for the old and new name, so it covers every such file, not a list of names.
func planIgnores(g *Graph, targets []Target) []Ignore {
	var out []Ignore
	type pair struct {
		oldPath, newPath string
		// hint says this name gets the no-git hint when its file is the workspace's local file:
		// the name the workspace reads, and the backup.
		hint bool
	}
	for _, t := range targets {
		var pairs []pair
		if t.New != t.Old {
			pairs = append(pairs, pair{t.Old, t.New, true})
		}
		pairs = append(pairs, pair{t.Old, t.Backup, true})
		for _, pr := range pairs {
			oldPath, newPath := pr.oldPath, pr.newPath
			switch gitexclude.Check(oldPath) {
			case gitexclude.Ignored:
				if gitexclude.Check(newPath) != gitexclude.NotIgnored {
					continue
				}
				out = append(out, Ignore{Old: oldPath, New: newPath, Hint: ignoredHint(g.Request.Workspace, oldPath, newPath)})
			case gitexclude.Unknown:
				// Without git there is no rule to compare, so only a file private by definition —
				// the workspace's local file, under the name the workspace reads, and its backup —
				// gets the hint.
				if pr.hint && isLocalFile(t.File) {
					out = append(out, Ignore{Old: oldPath, New: newPath, Hint: ignoreHint(g.Request.Workspace, newPath)})
				}
			}
		}
	}
	return out
}

func isLocalFile(f *File) bool {
	return f != nil && f.Scope == ScopeProject &&
		(f.topName == paths.WorkspaceLocalConfigLegacyJSONCName || f.topName == paths.WorkspaceLocalConfigLegacyJSONName ||
			f.topName == paths.WorkspaceLocalConfigTOMLName)
}

// ignoredHint is the line for a new name git does not ignore although it ignored the old one. The
// pattern is the new name anchored at its work tree's top when git can say where that is.
func ignoredHint(workspace, oldPath, newPath string) string {
	line, ok := gitexclude.Line(newPath)
	if !ok {
		line = filepath.ToSlash(shownPath(workspace, newPath))
	}
	return fmt.Sprintf("%s is not ignored by git as %s was; nothing was added to any ignore file. To keep it out of commits, add the line `%s` to .gitignore or .git/info/exclude",
		shownPath(workspace, newPath), shownPath(workspace, oldPath), line)
}

func ignoreHint(workspace, newPath string) string {
	name := shownPath(workspace, newPath)
	return fmt.Sprintf("%s is not ignored by git (git could not be asked here); to keep it out of commits, add the line `%s` to .gitignore", name, filepath.ToSlash(name))
}

// shownPath is path relative to dir when it lies inside it, else path itself.
func shownPath(dir, path string) string {
	if dir != "" {
		if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return path
}

// Plan is the transcript's line for one Ignore.
func (ig Ignore) Plan(string) string { return ig.Hint }

// Report is the closing report's line for one Ignore, ending in a full stop.
func (ig Ignore) Report(string) string { return ig.Hint + "." }

package run

// hostexports.go hands `yolo host --` (internal/cli) three of the launcher's own answers, so the
// host notch asks the jail's question rather than writing a second copy of it. Each is a thin
// wrapper and nothing more: the logic stays where the jail launch reads it, and a change there
// reaches the host by construction.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// WorkspaceSkillDirs is the workspace skills layer's SOURCE SET, in the order a jail reads it
// (workspaceSkillDirs: the selected packs' project_dirs in config order, then every shipped
// pack's by name). `yolo host -- <agent>`'s in-workspace link picks its source from this list
// (docs/design/workspace-skills.md WS-D20).
func WorkspaceSkillDirs(selected []*packload.Pack) []string {
	return workspaceSkillDirs(selected)
}

// DisplaySafe is displaySafe: a workspace-supplied path or name rendered for a disclosure line,
// verbatim when it is plain printable text and Go-quoted (with `[` escaped) otherwise, so a
// directory a clone names cannot forge or restyle a line. The host's workspace skills lines go
// through it as the jail's do.
func DisplaySafe(s string) string { return displaySafe(s) }

// ClaudeSharedCredentialDir is claudeSharedCredentialDir: the home-relative machine-scope
// directory a pack in packs links Claude's credential into through its `shared_credentials` hook,
// or "" when none does. The host notch asks it of the launched program's own pack, to know the
// program is the Claude the credential view is for (CL-D27).
func ClaudeSharedCredentialDir(packs []*packload.Pack) string {
	return claudeSharedCredentialDir(packs)
}

// HostScopedEndpoints is hostScopedEndpoints: the host-scoped loopholes that are active and whose
// pack may run host code, for runtime rt over cfg's `loopholes` block. The host notch asks it
// whether the claude-oauth-broker may run for a `yolo host -- claude` launch (CL-D27), the same
// predicate a jail launch's brokerLoopholeActive is.
func HostScopedEndpoints(rt string, cfg *jsonx.OrderedMap) []string {
	return hostScopedEndpoints(rt, cfg)
}

package run

// workspacescopeguard.go refuses a launch whose WORKSPACE would put the host side of the
// credential boundary inside the jail: the home directory itself, or either of yolo's own
// two host directories.
//
// WHICH directories, and the containment rule that decides it, are paths.WorkspaceScopeBreach's
// (internal/paths/workspacescope.go) — one authority, because the same three directories must
// also never acquire a stray `.yolo`, and two copies of a boundary list is one copy that gets
// out of step. What lives HERE is the MOUNT half: why a launch in particular cannot proceed,
// and what the jail would have got.
//
// WHY THIS IS THE BOUNDARY AND NOT A TIDINESS RULE. The workspace is the one host
// directory a jail can read and write by design — every backend binds it, read-write by
// default, and `workspace_readonly` removes only the write half. Everything else yolo
// promises rests on that directory being a PROJECT: "host credentials are not propagated
// into the jail" (AGENTS.md, Limitations) is true because ~/.ssh, ~/.gitconfig and the
// cloud tokens sit OUTSIDE the mount. A workspace of $HOME puts them inside it, and the
// read half alone is enough — this refusal is therefore not conditional on writability.
//
// The two yolo-owned directories are worse than the files they hold, because a jail that
// can write them rewrites the terms of the NEXT launch:
//
//   - ~/.config/yolo-jail/ carries the user scope — `packs`, `host_files`, the providers
//     and profiles. Those keys are user-scope ONLY, which is precisely why config.LoadPacks
//     and config.LoadHostFiles read this file directly rather than the merged config: it is
//     their security boundary. A jail that edits it grants itself a host-file read, or a
//     pack of its own choosing, on the launch after this one.
//   - ~/.local/share/yolo-jail/ holds every OTHER workspace's home overlay, the fetched
//     pack trees with the lockfile recording their host-read approvals, the approval
//     snapshots (config.ApprovalSnapshotPath — moved host-side by OQ-D1 for exactly this
//     "whatever can edit the config can rewrite the record of what was approved" reason),
//     and the flake bundle every launch binds as pid1.
//
// It needs no mistake beyond a cd: there is no --workspace flag, the cwd is the workspace,
// so a bare `yolo` typed in the home is a launch on the home. Measured on the maintainer's
// host 2026-09-17 — a stray ~/.yolo holding config-snapshot.json, startup.log and a full
// home/ overlay, from a launch old enough that the approval record still lived in the mount
// (2ecbe0f5, 2026-08-18).
//
// NO HATCH, where the two guards beside it have one (YOLO_ALLOW_LIVE_WORKSPACE,
// YOLO_ALLOW_SOURCE_SKEW). A YOLO_ALLOW_* dial is for a case yolo got wrong about a
// machine it cannot see; this test is structural and has no false positive to escape. An
// env var that voids the credential boundary would be a documented way to void it, and the
// deliberate case it would serve — jailing your own dotfiles — is served better by naming
// the subdirectory that holds them.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workspaceScopeRefusal is one refusal: `what` names the paths and the direction of the
// containment, `why` says what the jail would have got. Two fields rather than one
// sentence so the caller owns the frame and the hint, as the live-overlay guard's caller
// does.
type workspaceScopeRefusal struct {
	what string
	why  string
}

// jailWouldGet is the MOUNT consequence of each boundary root — this file's half of the
// message, keyed off the kind the shared predicate reports. A kind with no entry here
// still refuses; it just says less, which is the right direction for a root added in
// paths and not yet given its sentence.
var jailWouldGet = map[paths.ScopeRootKind]string{
	paths.RootHome: "every credential the jail is walled off from — ~/.ssh, ~/.gitconfig, " +
		"cloud and agent tokens — would be inside the mount",
	paths.RootStateDir: "the jail could rewrite every other workspace's home overlay, the " +
		"fetched pack trees and the lockfile recording their host-read approvals, the " +
		"approval snapshots, and the flake bundle every launch binds as pid1",
	paths.RootUserConfigDir: "the jail could grant itself host_files reads, packs and " +
		"providers for the NEXT launch, by editing the user-scope config that governs it",
}

// refuseWorkspaceScope returns the refusal for this launch, or nil to allow it.
func refuseWorkspaceScope(o *Options) *workspaceScopeRefusal {
	breach := paths.WorkspaceScopeBreach(o.Workspace)
	if breach == nil {
		return nil
	}
	why, ok := jailWouldGet[breach.Kind]
	if !ok {
		why = "the jail would have host access yolo's boundary exists to withhold"
	}
	return &workspaceScopeRefusal{what: breach.What(), why: why}
}

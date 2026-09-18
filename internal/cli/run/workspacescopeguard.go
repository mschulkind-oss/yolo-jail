package run

// workspacescopeguard.go refuses a launch whose WORKSPACE would put the host side of the
// credential boundary inside the jail: the home directory itself, or either of yolo's own
// two host directories.
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
// So the rule is CONTAINMENT, checked in both directions on RESOLVED paths, because a home
// is often itself a symlink:
//
//   - a workspace that IS, or CONTAINS, any of the three → refused. This is the accident,
//     and it needs no mistake beyond a cd: there is no --workspace flag, the cwd is the
//     workspace, so a bare `yolo` typed in the home is a launch on the home. Measured on
//     the maintainer's host 2026-09-17 — a stray ~/.yolo holding config-snapshot.json,
//     startup.log and a full home/ overlay, from a launch old enough that the approval
//     record still lived in the mount (2ecbe0f5, 2026-08-18).
//   - a workspace INSIDE either yolo-owned directory → refused too: the same write access
//     to the same state, one level in, and no project belongs there.
//
// A workspace inside the HOME is the ordinary case and is never refused — ~/code/x is where
// projects live, and ~/.dotfiles is the right way to put a dotfiles tree in a jail.
//
// NO HATCH, where the two guards beside it have one (YOLO_ALLOW_LIVE_WORKSPACE,
// YOLO_ALLOW_SOURCE_SKEW). A YOLO_ALLOW_* dial is for a case yolo got wrong about a
// machine it cannot see; this test is structural and has no false positive to escape. An
// env var that voids the credential boundary would be a documented way to void it, and the
// deliberate case it would serve — jailing your own dotfiles — is served better by naming
// the subdirectory that holds them.

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workspaceScopeRefusal is one refusal: `what` names the path and the direction of the
// containment, `why` says what the jail would have got. Two fields rather than one
// sentence so the caller owns the frame and the hint, as the live-overlay guard's caller
// does.
type workspaceScopeRefusal struct {
	what string
	why  string
}

// boundaryRoot is a host directory that must never be inside a jail's workspace mount.
type boundaryRoot struct {
	path string
	name string
	why  string
}

// refuseWorkspaceScope returns the refusal for this launch, or nil to allow it.
//
// The three roots are derived from ONE home — paths.Home(), which every paths helper
// resolves through — so they cannot disagree about which home this is.
func refuseWorkspaceScope(o *Options) *workspaceScopeRefusal {
	ws := resolvePath(o.Workspace)
	home := resolvePath(paths.Home())
	state := boundaryRoot{
		path: resolvePath(paths.GlobalStorage()),
		name: "yolo's own state directory",
		why: "the jail could rewrite every other workspace's home overlay, the fetched " +
			"pack trees and the lockfile recording their host-read approvals, the " +
			"approval snapshots, and the flake bundle every launch binds as pid1",
	}
	userConfig := boundaryRoot{
		path: resolvePath(filepath.Dir(paths.UserConfigPath())),
		name: "yolo's user config directory",
		why: "the jail could grant itself host_files reads, packs and providers for the " +
			"NEXT launch, by editing the user-scope config that governs it",
	}

	// Direction 1 — the workspace IS or CONTAINS a boundary root. Home first: it is the
	// broadest breach and the one an accidental cd produces.
	for _, r := range []boundaryRoot{
		{
			path: home,
			name: "your home directory",
			why: "every credential the jail is walled off from — ~/.ssh, ~/.gitconfig, " +
				"cloud and agent tokens — would be inside the mount",
		},
		state, userConfig,
	} {
		if !isUnderOrEqual(r.path, ws) {
			continue
		}
		what := "the workspace IS " + r.name + " (" + ws + ")"
		if r.path != ws {
			what = "the workspace " + ws + " CONTAINS " + r.name + " (" + r.path + ")"
		}
		return &workspaceScopeRefusal{what: what, why: r.why}
	}

	// Direction 2 — the workspace is INSIDE one of yolo's own directories. Not covered by
	// direction 1, and the same state is reachable from in there.
	for _, r := range []boundaryRoot{state, userConfig} {
		if isUnderOrEqual(ws, r.path) {
			return &workspaceScopeRefusal{
				what: "the workspace " + ws + " is INSIDE " + r.name + " (" + r.path + ")",
				why:  r.why,
			}
		}
	}
	return nil
}

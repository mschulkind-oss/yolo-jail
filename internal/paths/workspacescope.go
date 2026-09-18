package paths

// workspacescope.go answers one question for every caller that has a workspace in hand:
// may this directory BE a workspace at all?
//
// Three host directories must never be inside a jail's workspace mount, and the same three
// must never acquire a `.yolo` of their own:
//
//   - the HOME itself. The workspace is the one host directory a jail reads and writes by
//     design, so a workspace of $HOME puts ~/.ssh, ~/.gitconfig and every cloud and agent
//     token inside the mount — the credential boundary, gone in one cd.
//   - ~/.config/yolo-jail, which carries the user scope: `packs`, `host_files`, the
//     providers and profiles. A jail that can write it sets the NEXT launch's terms.
//   - ~/.local/share/yolo-jail, which holds every other workspace's home overlay, the
//     fetched pack trees and their approval lockfile, the approval snapshots, and the flake
//     bundle each launch binds as pid1.
//
// THERE ARE TWO HAZARDS HERE AND THEY NEED THE SAME PREDICATE, which is why it lives in
// this package rather than in the launcher that first needed it:
//
//  1. the MOUNT — a launch on such a workspace hands the jail the boundary
//     (internal/cli/run/workspacescopeguard.go refuses it);
//  2. the STRAY DIRECTORY — any command that writes `<cwd>/.yolo` in one of these
//     directories leaves a marker that `workspaceRoot()`'s upward walk then treats as a
//     workspace root, so every later `yolo config` verb run anywhere below it resolves the
//     HOME as its workspace, reads that directory's sidecars, and `reset` deletes them.
//     The second hazard outlives the first: a stray dir keeps working after the launch that
//     made it is forgotten, and machines already carry them (measured on the maintainer's
//     host 2026-09-17, from a launch predating the approval record leaving the mount).
//
// So the rule is CONTAINMENT, in both directions, on RESOLVED paths — a home is often a
// symlink, and comparing one resolved path against one unresolved path is the darwin
// t.TempDir class of bug (AGENTS.md, Testing):
//
//   - a workspace that IS or CONTAINS any of the three → refused;
//   - a workspace INSIDE either yolo-owned directory → refused too, because the same state
//     is reachable from in there and no project belongs in either.
//
// A workspace UNDER the home is the ordinary case and is never refused: ~/code/x is where
// projects live, and ~/.dotfiles is the right way to put a dotfiles tree in a jail.

import (
	"path/filepath"
	"strings"
)

// ScopeRootKind names the boundary root a workspace collided with. Callers key their own
// "and here is what that would have cost you" prose off this — the launcher's reason (what
// the jail would read) and a stray directory's reason (what the workspace-root walk would
// do) are different sentences about the same collision, so neither belongs here.
type ScopeRootKind int

const (
	// RootHome is the user's home directory.
	RootHome ScopeRootKind = iota + 1
	// RootStateDir is ~/.local/share/yolo-jail.
	RootStateDir
	// RootUserConfigDir is ~/.config/yolo-jail.
	RootUserConfigDir
)

// Name is the short human name for this root, used in messages.
func (k ScopeRootKind) Name() string {
	switch k {
	case RootHome:
		return "your home directory"
	case RootStateDir:
		return "yolo's own state directory"
	case RootUserConfigDir:
		return "yolo's user config directory"
	}
	return "a reserved directory"
}

// ScopeRelation is how the workspace and the root are related. All three are refusals; they
// differ only in what the reader has to do about it.
type ScopeRelation int

const (
	// ScopeIsRoot — the workspace IS the root.
	ScopeIsRoot ScopeRelation = iota + 1
	// ScopeContainsRoot — the workspace is an ancestor of the root.
	ScopeContainsRoot
	// ScopeInsideRoot — the workspace sits inside the root.
	ScopeInsideRoot
)

// ScopeBreach is one collision: a resolved workspace, the resolved root it hit, and the
// direction. Nil means the workspace is fine.
type ScopeBreach struct {
	Workspace string
	Root      string
	Kind      ScopeRootKind
	Relation  ScopeRelation
}

// What renders the collision as a clause naming both paths and the direction — the half of
// a refusal that is the same wherever it is reported. Callers add their own frame and their
// own consequence.
func (b *ScopeBreach) What() string {
	switch b.Relation {
	case ScopeIsRoot:
		return "the workspace IS " + b.Kind.Name() + " (" + b.Workspace + ")"
	case ScopeContainsRoot:
		return "the workspace " + b.Workspace + " CONTAINS " + b.Kind.Name() + " (" + b.Root + ")"
	default:
		return "the workspace " + b.Workspace + " is INSIDE " + b.Kind.Name() + " (" + b.Root + ")"
	}
}

// Error lets a breach be returned as one by the creating chokepoint
// (EnsureWorkspaceStateDir), so a caller that only checks `err != nil` is still protected.
// It states the DIRECTORY hazard, because that is the one a creation attempt is about.
func (b *ScopeBreach) Error() string {
	return "refusing to create " + WorkspaceStateDir(b.Workspace) + ": " + b.What() +
		", and a .yolo there would make every command below it resolve that directory as " +
		"the workspace root"
}

// WorkspaceScopeBreach reports why this directory may not be a workspace, or nil.
//
// The three roots are derived from ONE home — home(), which every path helper in this
// package resolves through — so they cannot disagree about which home this is.
func WorkspaceScopeBreach(workspace string) *ScopeBreach {
	ws := resolveScopePath(workspace)
	roots := []struct {
		path string
		kind ScopeRootKind
	}{
		// Home first: the broadest breach, and the one an accidental cd produces.
		{resolveScopePath(home()), RootHome},
		{resolveScopePath(GlobalStorage()), RootStateDir},
		{resolveScopePath(filepath.Dir(UserConfigPath())), RootUserConfigDir},
	}

	// Direction 1 — the workspace IS or CONTAINS a root.
	for _, r := range roots {
		if !scopeUnderOrEqual(r.path, ws) {
			continue
		}
		rel := ScopeContainsRoot
		if r.path == ws {
			rel = ScopeIsRoot
		}
		return &ScopeBreach{Workspace: ws, Root: r.path, Kind: r.kind, Relation: rel}
	}

	// Direction 2 — the workspace is INSIDE one of yolo's own two directories. Not covered
	// by direction 1, and the same state is reachable from in there. The HOME is
	// deliberately absent from this half: ~/code/x is the ordinary case.
	for _, r := range roots {
		if r.kind == RootHome || !scopeUnderOrEqual(ws, r.path) {
			continue
		}
		return &ScopeBreach{Workspace: ws, Root: r.path, Kind: r.kind, Relation: ScopeInsideRoot}
	}
	return nil
}

// WorkspaceStateDirAllowed is the boolean spelling, for a caller that wants to SKIP writing
// rather than report a refusal.
func WorkspaceStateDirAllowed(workspace string) bool {
	return WorkspaceScopeBreach(workspace) == nil
}

// resolveScopePath makes a path absolute and resolves its symlinks, falling back to the
// absolute form for a path that does not exist yet (the common case for a root under a
// fresh home). Both sides of every comparison go through it.
func resolveScopePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if evaled, err := filepath.EvalSymlinks(abs); err == nil {
		return evaled
	}
	return abs
}

// scopeUnderOrEqual reports whether child is base or a descendant of base.
func scopeUnderOrEqual(child, base string) bool {
	if child == base {
		return true
	}
	rel, err := filepath.Rel(base, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

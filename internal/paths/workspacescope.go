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
	return b.WhatFor("the workspace")
}

// WhatFor is What with the subject named by the caller: the same collision said about a
// path that is not a workspace. Its second caller is a read-write `mounts` source
// (config.rwMountRefusal, docs/design/context-mounts.md §2.3 clause 1), which runs THIS
// rule over the source rather than a copy of it (WritableSourceScopeBreach) — one spelling
// of the boundary, with the workspace-only capture-store exemption the one parameter.
func (b *ScopeBreach) WhatFor(subject string) string {
	switch b.Relation {
	case ScopeIsRoot:
		return subject + " IS " + b.Kind.Name() + " (" + b.Workspace + ")"
	case ScopeContainsRoot:
		return subject + " " + b.Workspace + " CONTAINS " + b.Kind.Name() + " (" + b.Root + ")"
	default:
		return subject + " " + b.Workspace + " is INSIDE " + b.Kind.Name() + " (" + b.Root + ")"
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
	return scopeBreach(workspace, true)
}

// WritableSourceScopeBreach is the SAME rule for a host path a jail would WRITE through a
// mount rather than work in as its workspace — a read-write `mounts` source
// (config.rwMountRefusal, docs/design/context-mounts.md §2.3 clause 1) — with one difference:
// no capture-store exemption. That exemption is a WORKSPACE's (scopeExempt: `yolo capture`
// launches a jail on its own scratch tree there); a writable mount of the store is every
// other workspace's captured installers, rewritable from one jail, which is the cross-jail
// injection the capture mount itself is refused to prevent. One rule body, a parameter for
// the one exemption, so the two callers cannot drift apart on anything else.
func WritableSourceScopeBreach(source string) *ScopeBreach {
	return scopeBreach(source, false)
}

// WorkspaceScopeBreachUnder is the same rule against the roots of an EXPLICIT home, for the
// one caller whose boundary is not the process HOME's: `yolo internal darwin-bootstrap`, which
// guards the macos-user SANDBOX account's home (internal/cli/internal.go says why it cannot
// guard the human's).
//
// THE HOME IS RESOLVED, ITS TWO yolo DIRECTORIES ARE NOT. They are joined onto the resolved home
// lexically, because on that backend the home layout links ~/.config and ~/.local into the
// launching workspace's own sidecar, <ws>/.yolo/home (entrypoint.DeriveDarwinHomeLayout). Followed,
// those links put the sandbox's config dir inside every workspace that has launched before, as soon
// as its sidecar holds a config/yolo-jail (a missing one fails to resolve and falls back to the
// lexical path, which is why the refusal waited for one to appear). The sidecar is the workspace's own overlay, the same tier a container binds at
// /home/agent/.config, not a boundary the workspace could breach. No capture exemption: that one
// is for a workspace inside the process HOME's state dir, which this caller does not check.
func WorkspaceScopeBreachUnder(workspace, home string) *ScopeBreach {
	h := resolveScopePath(home)
	return scopeBreachAgainst(workspace, false, []scopeRoot{
		{h, RootHome},
		{GlobalStorageUnder(h), RootStateDir},
		{filepath.Join(h, filepath.Dir(filepath.FromSlash(userConfigSuffix))), RootUserConfigDir},
	})
}

// scopeRoot is one boundary root, already resolved.
type scopeRoot struct {
	path string
	kind ScopeRootKind
}

// scopeBreach is WorkspaceScopeBreach's rule; exemptCaptures applies scopeExempt.
func scopeBreach(workspace string, exemptCaptures bool) *ScopeBreach {
	return scopeBreachAgainst(workspace, exemptCaptures, []scopeRoot{
		// Home first: the broadest breach, and the one an accidental cd produces.
		{resolveScopePath(home()), RootHome},
		{resolveScopePath(GlobalStorage()), RootStateDir},
		{resolveScopePath(filepath.Dir(UserConfigPath())), RootUserConfigDir},
	})
}

// scopeBreachAgainst is the containment rule over a given set of roots, home first.
func scopeBreachAgainst(workspace string, exemptCaptures bool, roots []scopeRoot) *ScopeBreach {
	ws := resolveScopePath(workspace)

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
	// by direction 1, and no project belongs in either. The HOME is deliberately absent
	// from this half: ~/code/x is the ordinary case.
	for _, r := range roots {
		if r.kind == RootHome || !scopeUnderOrEqual(ws, r.path) {
			continue
		}
		if exemptCaptures && scopeExempt(ws) {
			continue
		}
		return &ScopeBreach{Workspace: ws, Root: r.path, Kind: r.kind, Relation: ScopeInsideRoot}
	}
	return nil
}

// scopeExempt reports whether this workspace is one YOLO ITSELF puts inside its own state
// directory, and is therefore not the mistake direction 2 is looking for.
//
// There is exactly one, and it is load-bearing: `yolo capture` stages a throwaway workspace
// at <CapturesDir>/staging/<id> and launches the ordinary run pipeline against it.
// CapturesDir's docstring states why that tree cannot live in /tmp — admission into the
// store is an os.Rename, and a scratch tree on another filesystem silently turns it into a
// full copy of the gigabytes the capture subsystem exists to stop copying. The first cut of
// the launch guard shipped without this exemption and refused every `yolo capture`
// (internal/capture/scopeexemption_test.go is the regression pin, against the real
// Store.StagingDir rather than a hand-joined literal).
//
// It is narrow on purpose: the CAPTURE STORE only, never the state dir at large. A bind of a
// subtree exposes that subtree and not its parents, so what this allows a jail to reach is
// its own scratch tree — while a workspace at `packs/`, `approvals/` or the state dir itself
// stays refused.
func scopeExempt(ws string) bool {
	return scopeUnderOrEqual(ws, resolveScopePath(CapturesDir()))
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

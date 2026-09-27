package macosuser

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// homereadonly.go is G14's macos-user half (docs/plans/setup-support-gaps.md): the staged
// skills and briefings the bootstrap copies into the sandbox home are WRITE-PROTECTED by the
// session's Seatbelt profile, the way every container backend's `:ro` bind protects them.
//
// WHY THE PROFILE AND NOT THE FILE MODE. The bootstrap copies as the sandbox user, so the
// agent OWNS every file it was given, and an owner can chmod its way back to a writable
// file. A Seatbelt deny is evaluated by the kernel whoever owns the file. What it is
// evaluated AGAINST is a path, and that is where both of this file's jobs come from.
//
// JOB ONE: THE PATH THE KERNEL WILL SEE, NOT THE PATH THE PACK DECLARED. `.claude/skills`
// is declared home-relative, and the account home is /Users/_yolojail — but on a launch
// with the home-tier layout (entrypoint.DeriveDarwinHomeLayout) `~/.claude` is a SYMLINK
// into <workspace>/.yolo/home/claude. The kernel resolves symlinks before the policy is
// consulted, and an SBPL rule naming an unresolved spelling matches NOTHING (measured on
// hardware 2026-09-13: `(subpath "/tmp")` does not stop `touch /tmp/canary`,
// `(subpath "/private/tmp")` does). So a deny on /Users/_yolojail/.claude/skills would be
// in the profile and inert on every shipped pack. The deny is therefore emitted on the
// PHYSICAL path, derived from the same deriver the bootstrap lays the layout with and the
// same pack list it reads, so the two cannot disagree about where a destination lands.
//
// ⚠ THAT DERIVATION IS A TEXT JOIN, and it is sound only because the bootstrap makes it so.
// Only the two bases — the account home and the workspace — are resolved here; everything
// below them is joined through the layout's links as written, because on a first launch none
// of it exists yet and this runs before the bootstrap. The kernel reports the same string only
// while nothing below the bases is a symbolic link the layout did not lay, and the sidecar is
// inside the workspace, which an earlier session could write with a profile that did not cover
// these paths. So the bootstrap holds the other end: a link in the sidecar refuses both its
// layout and its overlay step (entrypoint.LinkedSidecarError), and the overlay install follows
// no link but the layout's own, replacing one planted at a destination in the sidecar and
// refusing one above a destination or in the account home (entrypoint.InstallHomeOverlay). Pinned together, over a planted link at each
// position, by TestTheBootstrapDeliversOnlyWhereTheHostsRulesPoint.
//
// JOB TWO: THE CHAIN ABOVE THE DESTINATION. A path deny protects a path, not an inode. With
// only the destinations denied, `mv ~/.claude ~/.claude-old && ln -s /tmp/x ~/.claude`
// would leave every staged file untouched and point the agent's own reader at a tree it
// wrote itself. So every directory and layout link between a writable root and a
// destination is an ANCHOR: it may not be unlinked or renamed away, and nothing may be
// created at its path. Anchors deny only those two operations, never a whole file-write*,
// so the agent can still chmod, touch and extend its own state directories — which is
// what "leaves every other agent state path writable" means for the directories that
// happen to hold a destination.
//
// ⚠ WHAT LINUX CANNOT SETTLE. Everything here is a pure function of paths and is pinned
// on Linux, including behind a symlinked TMPDIR. Whether the kernel refuses the operations
// is a Mac question: integration/macosuserseatbelt_test.go asks it under the generated
// profile, and integration/macosusercontent_test.go through a real launch.

// HomeOverlay is the composed CONTENT a launch delivers into the sandbox home — the host
// side's whole answer to "what did yolo put there, and where".
//
// ONE VALUE, because the three fields are one fact: the tree, the destinations it holds,
// and the layout that decides where those destinations land. Carried separately they
// could disagree, and the one disagreement that matters — a destination the tree holds
// and the profile does not protect — would be silent.
type HomeOverlay struct {
	// Tree is the host-side tree, laid out at the home-relative paths it belongs at, or ""
	// when there is nothing to deliver (internal/cli/run/macoshomeoverlay.go builds it,
	// and says why it crosses as a tree rather than as a mapping).
	Tree string
	// Dests are the home-relative destinations Tree holds: one per staged skills dir and
	// per briefing file the builder WROTE, deduplicated. Not the declared list — a
	// destination nothing was staged for is not delivered, so it is not protected either,
	// and the two stay one statement.
	Dests []string
	// WorkspaceDirs are the selected packs' scope:workspace state dirs
	// (packload.WritableDirs), which the bootstrap links from the account home into the
	// workspace sidecar. They are what decides where each Dest physically lands.
	WorkspaceDirs []string
}

// HomeReadonly is the sandbox-home half of the profile's write carve-outs: the rendered
// input to homeReadonlyDenies, a SIBLING of workspace_readonly's readonlyDenies.
//
// A sibling and not an extra argument to readonlyDenies, because that function drops every
// ABSOLUTE entry on purpose — defence in depth for a workspace-relative config key — and
// every entry here is absolute by construction.
type HomeReadonly struct {
	// Paths are the delivered destinations, each in every spelling the kernel can resolve
	// it to, rendered as `(subpath …)` under `(deny file-write* …)`. A subpath covers the
	// path itself, so a briefing FILE is denied by the same form as a skills DIRECTORY.
	Paths []string
	// Anchors are every directory and layout link between a writable root and a
	// destination, rendered as `(literal …)` under
	// `(deny file-write-create file-write-unlink …)` — see the file header, job two.
	Anchors []string
}

// Empty reports whether there is nothing to render.
func (h HomeReadonly) Empty() bool { return len(h.Paths) == 0 && len(h.Anchors) == 0 }

// ResolveHomeReadonly turns an overlay's home-relative destinations into the absolute paths
// the kernel will evaluate a write against, and the anchors above them.
//
// `home` is the account home and `workspace` the (already resolved) workspace; the sidecar
// is derived from it with the one spelling both backends use (paths.WorkspaceHomeState).
// Both are resolved through their longest EXISTING prefix before anything is joined onto
// them: SandboxHome() reaches here as a constant that nothing has EvalSymlinks'd, and a
// fixture home under a symlinked TMPDIR is exactly the darwin /var → /private/var shape.
// Nothing below them is resolved, and the file header says why that is sound.
//
// For each destination the walk is the one the kernel does. Starting at the home, each
// component is a directory in the account home until one of them is a LAYOUT LINK
// (entrypoint.DeriveDarwinHomeLayout's Links — the same deriver, list and order the
// bootstrap applies); from there on the path continues physically in the sidecar, under
// the link's target. The FIRST link wins because the kernel resolves the first component
// it meets, whatever a later entry declares.
//
// A destination that is absolute, escapes the home, or is empty is dropped: pack
// declarations are validated upstream, and a deny on a path outside the home would widen
// the profile's refusals to somewhere nobody asked for rather than narrow the agent's
// writes.
func ResolveHomeReadonly(home, workspace string, workspaceDirs, dests []string) HomeReadonly {
	home = resolveLongestExisting(home)
	workspace = resolveLongestExisting(workspace)
	sidecar := paths.WorkspaceHomeState(workspace)

	links := map[string]string{} // home-relative link path → physical target
	for _, l := range entrypoint.DeriveDarwinHomeLayout(home, sidecar, workspaceDirs, nil).Links {
		rel, err := filepath.Rel(home, l.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		links[filepath.ToSlash(rel)] = l.Target
	}

	pathSet := map[string]struct{}{}
	anchorSet := map[string]struct{}{}
	for _, d := range dests {
		rel, ok := cleanHomeRel(d)
		if !ok {
			continue
		}
		parts := strings.Split(rel, "/")
		// The ACCOUNT-HOME spelling is always a path: it is what the kernel sees whenever
		// the layout link is not there — a dest no pack's state dir covers, or a link the
		// anchors below failed to hold. Where a link stands it matches nothing, harmlessly.
		pathSet[filepath.Join(home, filepath.FromSlash(rel))] = struct{}{}

		cur, linked := home, false
		for i, part := range parts {
			node := filepath.Join(cur, part)
			last := i == len(parts)-1
			target, isLink := "", false
			if !linked {
				target, isLink = links[strings.Join(parts[:i+1], "/")]
			}
			if last {
				pathSet[node] = struct{}{}
				if isLink {
					// A destination that IS a layout link (a pack delivering into `.claude`
					// itself — none ships one) is written THROUGH the link, so its physical
					// spelling is the link's target, and the directories above that are the
					// chain. The container binds such a dest `:ro` over the whole state dir;
					// this denies the same thing.
					pathSet[target] = struct{}{}
					anchorSet[node] = struct{}{}
					chain := chainBelow(workspace, target)
					for _, a := range chain[:len(chain)-1] {
						anchorSet[a] = struct{}{}
					}
				}
				break
			}
			anchorSet[node] = struct{}{}
			if isLink {
				for _, a := range chainBelow(workspace, target) {
					anchorSet[a] = struct{}{}
				}
				cur, linked = target, true
				continue
			}
			cur = node
		}
	}
	return HomeReadonly{Paths: sortedKeys(pathSet), Anchors: sortedKeys(anchorSet)}
}

// chainBelow returns target and each of its ancestors strictly below root, deepest last —
// the physical directories a layout link's target hangs from inside the workspace
// (<ws>/.yolo, <ws>/.yolo/home, <ws>/.yolo/home/claude). The workspace itself is not an
// anchor: its parent is outside the writable set, so the profile's base deny already
// refuses creating anything at its path. A target that is not under root contributes
// itself alone.
func chainBelow(root, target string) []string {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return []string{target}
	}
	var out []string
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, part)
		out = append(out, cur)
	}
	return out
}

// cleanHomeRel normalizes a pack's home-relative destination, or reports it unusable.
func cleanHomeRel(d string) (string, bool) {
	d = strings.TrimSpace(filepath.ToSlash(d))
	if d == "" || strings.HasPrefix(d, "/") {
		return "", false
	}
	rel := filepath.ToSlash(filepath.Clean(d))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// resolveLongestExisting is resolvePathAbs for a path that may not exist YET: it resolves
// the deepest existing ancestor and joins the rest back on lexically. A first launch has no
// <workspace>/.yolo/home/claude/skills, and filepath.EvalSymlinks on the whole path would
// fail and fall back to a spelling whose symlinked PREFIX the kernel would never report.
func resolveLongestExisting(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	var rest []string
	for cur := abs; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

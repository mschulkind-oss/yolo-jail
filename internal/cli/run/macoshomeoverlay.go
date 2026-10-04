package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// macoshomeoverlay.go builds the HOME OVERLAY: the staged skills, briefings and pack
// `files` trees laid out at their home-relative destinations, ready to be copied over a
// jail home.
//
// WHY A TREE AND NOT A MAPPING. On the container backends each staged dir is bind-
// mounted at its destination, so the mapping "staging dir → home path" lives in the
// mount list and never crosses into the jail. macos-user has no mounts, so the
// mapping has to reach the sandbox somehow. Sending it as data (a JSON table from
// staging names to home paths that the bootstrap interprets) would put the same
// mapping in two implementations — the mount assembler's and the bootstrap's — which
// is the drift the transport unification exists to end. Laying the tree out by
// DESTINATION host-side instead means the paths in the tree ARE the home paths.
//
// ⚠ BUT THE TREE CANNOT SAY WHERE A DESTINATION STARTS, and that is why a LIST of the
// destinations rides beside it (entrypoint.HomeOverlayManifestName). `.pi/agent/skills/x`
// in the tree does not say whether `.pi/agent/skills` or `.pi/agent` is what a bind would
// have covered, and the install that guessed — replacing the first real directory below
// the home's layout links — deleted pi's whole state dir on every launch (G36). The list
// carries only the roots the tree already spells, written by the same loops that lay the
// tree out, so it maps nothing and cannot disagree with the tree about a path.
//
// It also survives the change that should replace it. The per-workspace sandbox home
// (docs/design/macos-user-home-tiers.md) moves only the DESTINATION home; the overlay
// itself, and everything below, is unchanged.
//
// WHAT THIS IS NOT: it is not a merge. The overlay holds only what yolo composes, and
// the install that applies it replaces exactly the listed destinations and leaves the
// rest of the home alone — the same semantics a bind mount has. The read-only part is
// the session's Seatbelt profile, which denies writes to every destination this returns.

// buildMacosHomeOverlay lays the staged skills, briefings and pack `files` trees out under
// one root at the home-relative paths they belong at, and returns that root with the
// destinations it holds (a zero HomeOverlay when there is nothing to deliver).
//
// It reads the SAME three declaration lists the container path mounts from —
// packSkillTargets, briefingDestinations and packFilesTargets — so a pack that declares a
// destination gets it on both backends or on neither. A fourth list here would be the shape
// that lets them disagree silently. `files` was the list this used to leave out, so pi's two
// extensions and every agent footer script were absent on this backend and nothing said so
// (docs/plans/notch-convergence.md row D8). A `files` tree whose source is missing is
// skipped with the jail's own warning (packFilesSkipWarning), passed to warn.
//
// THE DESTINATIONS TRAVEL WITH THE TREE because the container's `:ro` is part of the
// delivery, not an extra: the Seatbelt profile write-protects exactly what this wrote
// (macosuser.ResolveHomeReadonly, G14). ⚠ AND THEY ARE ONE LIST WITH THE ONE THE SANDBOX
// INSTALL READS: Dests is what entrypoint.WriteHomeOverlayManifest returned when it wrote
// the tree's destination list, so the destinations the profile protects and the ones the
// bootstrap replaces are the same list rather than two derivations of it. They are the
// destinations the loop below WROTE rather than a second walk of the declarations, so a
// destination nothing was staged for is neither delivered nor protected. WorkspaceDirs is
// the selection's scope:workspace state list, which is what decides where each
// destination physically lands once the bootstrap lays the home-tier layout.
func buildMacosHomeOverlay(staging string, packs []*packload.Pack, warn func(string)) (macosuser.HomeOverlay, error) {
	tree, dests, err := buildMacosHomeOverlayFor(staging, packSkillTargets(packs),
		briefingDestinations(packs), packFilesTargets(packs, nil), warn)
	if err != nil || tree == "" {
		return macosuser.HomeOverlay{}, err
	}
	return macosuser.HomeOverlay{Tree: tree, Dests: dests, WorkspaceDirs: packload.WritableDirs(packs)}, nil
}

// buildMacosHomeOverlayFor is the body, taking the three declaration lists directly, and
// returning the tree and the destination list it wrote beside the tree
// (entrypoint.WriteHomeOverlayManifest's result: cleaned, once each, sorted, with a
// destination inside another dropped).
//
// Split from the wrapper so a test can state the destinations rather than construct
// packs that produce them: the property under test is "staged name → home path", and
// routing it through pack parsing would test the parser instead.
func buildMacosHomeOverlayFor(staging string, skills []jailcontent.SkillTarget,
	briefings []briefingDest, files []packFilesTarget, warn func(string)) (string, []string, error) {
	overlay := filepath.Join(staging, "home-overlay")
	// Rebuilt from scratch every launch: a destination that LEAVES the config must
	// stop being delivered, and an overlay that only ever accumulated would keep
	// handing the agent skills from a pack the user removed. Contents-only would be
	// enough today; RemoveAll is fine because nothing binds this path (unlike the
	// jail's ~/.yolo/bin anchor, which is why that one is cleared contents-only).
	if err := os.RemoveAll(overlay); err != nil {
		return "", nil, fmt.Errorf("clearing the macos-user home overlay: %w", err)
	}

	// Every destination laid out below, in the order it was written — the input to the
	// list the sandbox install walks (entrypoint.HomeOverlayManifestName). Collected in the
	// SAME loops that write the tree, so a destination is listed exactly when its content
	// is there.
	var written []string
	for _, t := range skills {
		src := filepath.Join(staging, t.Staging)
		if _, err := os.Stat(src); err != nil {
			continue // a pack that declared a skills dest but staged nothing
		}
		dst := filepath.Join(overlay, filepath.FromSlash(t.Dest))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", nil, err
		}
		if err := copyTree(src, dst); err != nil {
			return "", nil, fmt.Errorf("staging skills for %s: %w", t.Dest, err)
		}
		written = append(written, t.Dest)
	}

	for _, d := range briefings {
		src := filepath.Join(staging, briefingStagingName(d.Into))
		body, err := os.ReadFile(src)
		if err != nil {
			continue // no briefing was written for this destination
		}
		dst := filepath.Join(overlay, filepath.FromSlash(d.Into))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", nil, err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return "", nil, err
		}
		written = append(written, d.Into)
	}

	// PACK `files` TREES, from the staged pack tree, the same source the container binds
	// read-only. A directory or a single file, copied at its destination; the Seatbelt
	// profile then denies the agent writes to it, which is this backend's `:ro`.
	for _, t := range files {
		if !isDir(t.Src) && !isFile(t.Src) {
			if warn != nil {
				warn(packFilesSkipWarning(t))
			}
			continue
		}
		dst := filepath.Join(overlay, filepath.FromSlash(t.Dest))
		if err := copyTree(t.Src, dst); err != nil {
			return "", nil, fmt.Errorf("staging pack %s's files for %s: %w", t.Pack, t.Dest, err)
		}
		written = append(written, t.Dest)
	}

	if len(written) == 0 {
		// Nothing to deliver — no packs, or none declaring skills, briefings or files.
		// Returning "" rather than an empty dir keeps the staging and the bootstrap
		// step off the launch entirely, so a bare `yolo -- bash` pays nothing.
		_ = os.RemoveAll(overlay)
		return "", nil, nil
	}
	// THE DESTINATION LIST, beside the tree it describes (G36). Without it the install can
	// only guess where a destination starts, and its guess — the first real directory below
	// the home's links — was pi's whole state dir. What it wrote is what this returns, so
	// the profile (Dests) and the install read one list.
	dests, err := entrypoint.WriteHomeOverlayManifest(overlay, written)
	if err != nil {
		return "", nil, fmt.Errorf("listing the macos-user home overlay's destinations: %w", err)
	}
	return overlay, dests, nil
}

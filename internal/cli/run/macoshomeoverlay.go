package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// macoshomeoverlay.go builds the HOME OVERLAY: the staged skills and briefings laid
// out at their home-relative destinations, ready to be copied over a jail home.
//
// WHY A TREE AND NOT A MANIFEST. On the container backends each staged dir is bind-
// mounted at its destination, so the mapping "staging dir → home path" lives in the
// mount list and never crosses into the jail. macos-user has no mounts, so the
// mapping has to reach the sandbox somehow. Sending it as data (a JSON table the
// bootstrap reads and acts on) would put the same mapping in two implementations —
// the mount assembler's and the bootstrap's — which is the drift the transport
// unification exists to end. Laying the tree out by DESTINATION host-side instead
// makes delivery a single `cp -R overlay/. $HOME/` with no schema at all: the paths
// ARE the manifest.
//
// It also survives the change that should replace it. The per-workspace sandbox home
// (docs/design/macos-user-home-tiers.md) moves only the DESTINATION home; the overlay
// itself, and everything below, is unchanged.
//
// WHAT THIS IS NOT: it is not a merge. The overlay holds only what yolo composes, and
// the copy that applies it overwrites those paths and leaves the rest of the home
// alone — the same semantics a bind mount has, minus the read-only part.

// buildMacosHomeOverlay lays the staged skills + briefings out under one root at the
// home-relative paths they belong at, and returns that root ("" when there is nothing
// to deliver).
//
// It reads the SAME two declaration lists the container path mounts from —
// packSkillTargets and briefingDestinations — so a pack that declares a destination
// gets it on both backends or on neither. A third list here would be the shape that
// lets them disagree silently.
func buildMacosHomeOverlay(staging string, packs []*packload.Pack) (string, error) {
	return buildMacosHomeOverlayFor(staging, packSkillTargets(packs), briefingDestinations(packs))
}

// buildMacosHomeOverlayFor is the body, taking the two declaration lists directly.
//
// Split from the wrapper so a test can state the destinations rather than construct
// packs that produce them: the property under test is "staged name → home path", and
// routing it through pack parsing would test the parser instead.
func buildMacosHomeOverlayFor(staging string, skills []jailcontent.SkillTarget,
	briefings []briefingDest) (string, error) {
	overlay := filepath.Join(staging, "home-overlay")
	// Rebuilt from scratch every launch: a destination that LEAVES the config must
	// stop being delivered, and an overlay that only ever accumulated would keep
	// handing the agent skills from a pack the user removed. Contents-only would be
	// enough today; RemoveAll is fine because nothing binds this path (unlike the
	// jail's ~/.yolo/bin anchor, which is why that one is cleared contents-only).
	if err := os.RemoveAll(overlay); err != nil {
		return "", fmt.Errorf("clearing the macos-user home overlay: %w", err)
	}

	wrote := false
	for _, t := range skills {
		src := filepath.Join(staging, t.Staging)
		if _, err := os.Stat(src); err != nil {
			continue // a pack that declared a skills dest but staged nothing
		}
		dst := filepath.Join(overlay, filepath.FromSlash(t.Dest))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := copyTree(src, dst); err != nil {
			return "", fmt.Errorf("staging skills for %s: %w", t.Dest, err)
		}
		wrote = true
	}

	for _, d := range briefings {
		src := filepath.Join(staging, briefingStagingName(d.Into))
		body, err := os.ReadFile(src)
		if err != nil {
			continue // no briefing was written for this destination
		}
		dst := filepath.Join(overlay, filepath.FromSlash(d.Into))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return "", err
		}
		wrote = true
	}

	if !wrote {
		// Nothing to deliver — no packs, or none declaring skills or briefings.
		// Returning "" rather than an empty dir keeps the staging and the bootstrap
		// step off the launch entirely, so a bare `yolo -- bash` pays nothing.
		_ = os.RemoveAll(overlay)
		return "", nil
	}
	return overlay, nil
}

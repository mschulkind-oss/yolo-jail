package entrypoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// darwinoverlay.go installs the macos-user HOME OVERLAY: the skills and briefings the host
// composed and staged for the sandbox account (run.buildMacosHomeOverlay), copied over the
// home because this backend has no bind mounts.
//
// ⚠ IT REPLACES EXACTLY THE DESTINATIONS THE HOST STAGED, AND NOTHING ELSE (G36). The
// container binds each staged tree AT its destination — <staging>/skills-pi over
// /home/agent/.pi/agent/skills — and leaves every directory above and beside it alone. This
// copy has to have the same granularity, and the tree alone cannot give it: an overlay
// holding `.pi/agent/skills/x/SKILL.md` does not say whether the destination is
// `.pi/agent/skills` or `.pi/agent`. The previous install guessed — it walked down through
// the home's symlinks and replaced the first REAL directory it met — and under the
// workspace-tier layout (macos-user-home-tiers.md) every agent state dir is a symlink into
// the workspace sidecar, so for pi, omp, agy and opencode that first real directory was the
// agent's own state dir ABOVE the destination. Every launch deleted pi's sign-in, sessions
// and freshly generated settings, and put back only the skills and the briefing.
//
// So the host writes the destination list beside the tree (HomeOverlayManifestName), from
// the same loop that lays the tree out, and this installs each listed destination on its
// own. The list names only the ROOTS the tree already spells; it is not a mapping from
// staging names to home paths, so it is not the second implementation of the mount
// assembler that macoshomeoverlay.go's header argues against.

// HomeOverlayManifestName is the file at the root of a home overlay that lists its
// destinations. It is read from the overlay root and never copied into the home: the
// install walks the list, not the tree, so nothing outside a listed destination moves.
const HomeOverlayManifestName = ".yolo-home-overlay.json"

// homeOverlayManifest is the manifest's wire shape. One field, and an object rather than a
// bare array so a later field does not need a new file.
type homeOverlayManifest struct {
	// Destinations are home-relative, slash-separated paths, each a directory or a file
	// the overlay carries at that same relative path.
	Destinations []string `json:"destinations"`
}

// overlayStagedSuffix and overlayAsideSuffix name the two siblings an install of one
// destination uses in the destination's own parent: the replacement is built completely
// under the first, and the previous copy is moved to the second for the instant between
// the two renames. Deterministic rather than random, so the next install removes whatever
// a crash left behind without having to guess which leftovers are its own.
const (
	overlayStagedSuffix = ".yolo-overlay-new"
	overlayAsideSuffix  = ".yolo-overlay-old"
)

// WriteHomeOverlayManifest records dests as the overlay's destination list and returns the
// list it wrote. The host calls it once, after laying every destination out under overlay.
// Duplicates collapse (two packs may declare one skills destination), a destination inside
// another is dropped (normalizeOverlayDests), and the list is sorted so the file is
// byte-stable across launches.
//
// THE RETURNED LIST IS THE ONE THE PROFILE PROTECTS: run.buildMacosHomeOverlay hands it on as
// macosuser.HomeOverlay.Dests, so the destinations Seatbelt write-protects and the ones this
// package's install replaces are one list, written once.
func WriteHomeOverlayManifest(overlay string, dests []string) ([]string, error) {
	out, err := normalizeOverlayDests(dests)
	if err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(homeOverlayManifest{Destinations: out}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(overlay, HomeOverlayManifestName), append(body, '\n'), 0o644); err != nil {
		return nil, err
	}
	return out, nil
}

// readHomeOverlayManifest returns the overlay's destinations, normalized the way
// WriteHomeOverlayManifest wrote them. The read normalizes again rather than trusting the
// file, so a list this builder did not write still installs each destination once and
// nothing outside the home.
//
// A MISSING LIST IS AN ERROR, not "install the whole tree": the host and this bootstrap are
// one binary on this backend (the launch stages its own executable and runs that), so an
// overlay without a list was not written by the builder, and guessing its granularity is
// exactly the defect this file exists to remove.
func readHomeOverlayManifest(overlay string) ([]string, error) {
	path := filepath.Join(overlay, HomeOverlayManifestName)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("the home overlay %s carries no destination list (%s), so "+
				"nothing says which paths it replaces; refusing to guess, because replacing "+
				"a parent of a destination deletes the agent state beside it", overlay,
				HomeOverlayManifestName)
		}
		return nil, err
	}
	var m homeOverlayManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	out, err := normalizeOverlayDests(m.Destinations)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// normalizeOverlayDests cleans each destination (cleanOverlayDest), sorts them, and drops
// duplicates and every destination that sits inside another one (the outer one's tree
// already carries it, because the host lays both out under one root). The one
// normalization both sides of the list use, so what the host writes and what the install
// reads cannot differ by a rule.
func normalizeOverlayDests(dests []string) ([]string, error) {
	var clean []string
	for _, d := range dests {
		c, err := cleanOverlayDest(d)
		if err != nil {
			return nil, err
		}
		clean = append(clean, c)
	}
	sort.Strings(clean)
	var out []string
	for _, d := range clean {
		if len(out) > 0 && (d == out[len(out)-1] || strings.HasPrefix(d, out[len(out)-1]+"/")) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// cleanOverlayDest validates one destination and returns its clean slash form. A
// destination must name a path strictly below the home: not the home itself, not an
// absolute path, nothing that climbs out with `..`, and not the manifest's own name.
func cleanOverlayDest(d string) (string, error) {
	native := filepath.Clean(filepath.FromSlash(d))
	if d == "" || native == "." || !filepath.IsLocal(native) {
		return "", fmt.Errorf("home overlay destination %q is not a path below the home", d)
	}
	clean := filepath.ToSlash(native)
	if strings.SplitN(clean, "/", 2)[0] == HomeOverlayManifestName {
		return "", fmt.Errorf("home overlay destination %q collides with the overlay's own "+
			"destination list, %s", d, HomeOverlayManifestName)
	}
	return clean, nil
}

// overlayLinks is what the install needs to know about the layout this launch laid: its
// directory links, each mapped from its account-home path to the target it was laid at
// (overlayLinksOf). Only the directory Links: a FileRedirect is a link too, and is refused like
// any other link in the account home, because a regular file written in its place would be a
// real file where the next launch's layout needs its link, which OQ-HT2 then refuses forever.
type overlayLinks struct {
	follow map[string]string
}

func overlayLinksOf(l DarwinHomeLayout) overlayLinks {
	o := overlayLinks{follow: map[string]string{}}
	for _, ln := range l.Links {
		o.follow[ln.Path] = ln.Target
	}
	return o
}

// route checks the path from the home down to dest, one component at a time, against the
// layout this launch laid (G14), and reports whether dest lies PAST one of its links — in the
// workspace sidecar rather than the account home.
//
// ⚠ THE PROFILE PROTECTS THE PATH THE LAYOUT LAID, SO NOTHING IS DELIVERED THROUGH A LINK IT
// DID NOT LAY. The content rules are the destination joined as text onto the home through the
// layout's links (macosuser.ResolveHomeReadonly); a delivery that followed any other link would
// land where no rule names it, the agent free to rewrite what it then follows. So, walking down:
//
//   - A LAYOUT LINK is the one way through, and only while it is the link this launch laid, to
//     the target it laid. A layout path that is not yet that link — a real directory the layout
//     step refused to replace (OQ-HT2: never deleted), or a path it failed to lay — delivers
//     nothing below it; replacing it would delete the very directory the refusal told the
//     reader to move their transcripts out of. A destination that IS a layout link is refused
//     too: it would be replaced whole, leaving a real directory where the link belongs.
//   - IN THE ACCOUNT HOME, any other link is refused, naming it. It is typically ANOTHER
//     launch's layout link — one a different pack selection declared, pointing into that
//     workspace — or one of the layout's file redirects, and following it would deliver into
//     that workspace's sidecar while replacing it would leave a real path its next launch
//     refuses forever.
//   - PAST A LAYOUT LINK, in the sidecar, the layout lays no links, so a link on the way to a
//     destination is refused, naming it: following it lands where no rule names, and replacing
//     it would touch a directory above a destination, which this install never does (G36). A
//     link AT the destination is an occupant like any other, and installOverlayDestination
//     replaces it — the link, never what it points at.
//
// Every path below an absent one is absent too, so the walk stops there: the install creates
// what is missing beneath the root that holds the last existing directory.
func (l overlayLinks) route(home, dest string) (pastLink bool, err error) {
	parts := strings.Split(dest, "/")
	cur := home
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		last := i == len(parts)-1
		fi, lerr := os.Lstat(cur)
		if target, ok := l.follow[cur]; ok && !pastLink {
			got, rerr := os.Readlink(cur)
			if rerr != nil || got != target {
				return false, fmt.Errorf("nothing was delivered under %s: it is not the layout's "+
					"link to %s yet (the darwin_home_layout step says why), and a path the "+
					"layout owns is never replaced", cur, target)
			}
			if last {
				return false, fmt.Errorf("not installing ~/%s: it is this launch's layout link "+
					"to %s, and a destination is replaced whole, which would put a real path "+
					"where the link belongs", dest, target)
			}
			// On from the link's TARGET, so what the walk names below it is the physical path
			// in the sidecar — the one a remedy can reach without resolving the layout's link.
			pastLink, cur = true, target
			continue
		}
		if lerr != nil {
			return pastLink, nil
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if !pastLink {
			return false, fmt.Errorf("nothing was delivered at %s: it is a symbolic link in the "+
				"account home that is not one of this launch's layout links to a directory — "+
				"typically another workspace's, for a pack this launch does not select — and "+
				"delivering through it would land where the session's sandbox profile does not "+
				"protect. Remove the link (what it points at is left alone):\n  sudo rm %s", cur, cur)
		}
		if last {
			return true, nil
		}
		return true, fmt.Errorf("nothing was delivered at ~/%s: %s, on its way through the "+
			"workspace sidecar, is a symbolic link the layout did not lay, and delivering "+
			"through it would land where the session's sandbox profile does not protect. "+
			"Remove the link (what it points at is left alone):\n  sudo rm %s", dest, cur, cur)
	}
	return pastLink, nil
}

// installHomeOverlayDestinations installs every listed destination of overlay into home.
// Each destination is installed on its own, and a failure in one does not stop the others
// — the same "report every problem in one boot" rule genStep follows.
//
// `roots` are the directories an install may write under once symlinks are resolved: the
// home and, when the layout laid one, the workspace sidecar its links point into. `links` are
// the layout's own links, the only ones a destination's route may pass through
// (overlayLinks.route).
func installHomeOverlayDestinations(overlay, home string, roots []string, links overlayLinks) error {
	dests, err := readHomeOverlayManifest(overlay)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range dests {
		if err := installOverlayDestination(overlay, home, roots, links, d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// overlayInstallRoots resolves the directories an overlay install may write under: the home
// and the workspace sidecar ("" when the launch laid no layout). Resolved here, once, so a
// home or sidecar behind a symlink (macOS's /var → /private/var) compares equal to the
// resolved parents it is checked against.
func overlayInstallRoots(home, sidecar string) ([]string, error) {
	h, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolving the home %s: %w", home, err)
	}
	roots := []string{h}
	if sidecar != "" {
		if s, err := filepath.EvalSymlinks(sidecar); err == nil {
			roots = append(roots, s)
		}
	}
	return roots, nil
}

// installOverlayDestination replaces ONE destination with the overlay's copy of it.
//
// What it may touch is the destination path itself and the two named siblings beside it
// (overlayStagedSuffix, overlayAsideSuffix) — never the destination's parent or anything
// else in it. That is what keeps an agent's state dir intact when the destination is a
// child of it, and it is also what makes a crash survivable: the replacement is built
// COMPLETELY beside the destination before the destination is touched, so a failure or a
// crash at any point leaves either the old copy, the new one, or (between two renames of a
// directory) the old one under the aside name — and in every case only yolo's own content
// is at stake, never the agent's.
//
// Symlinks: a directory ABOVE the destination may be a link only when it is one of the
// layout's own (overlayLinks.route refuses any other, G14), and the parent they resolve to
// must also lie under one of `roots`, because this runs outside Seatbelt with the sandbox
// account's full reach and a link the agent planted would otherwise aim the write at a
// directory the agent itself cannot reach. The destination ITSELF is never followed.
// In the account home a link there is refused: replacing it would put a real directory where
// a layout link may belong (which the next boot refuses, OQ-HT2). Past a layout link, in the
// sidecar, where the layout lays no links, it is an occupant like any other and is replaced:
// the rename moves the LINK aside and the removal unlinks it, so what it points at is never
// touched.
func installOverlayDestination(overlay, home string, roots []string, links overlayLinks, dest string) error {
	rel := filepath.FromSlash(dest)
	src := filepath.Join(overlay, rel)
	dst := filepath.Join(home, rel)
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("the home overlay lists ~/%s but carries nothing there: %w", dest, err)
	}
	if !srcInfo.IsDir() && !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("the home overlay's ~/%s is a %s; only files and directories are "+
			"delivered", dest, srcInfo.Mode().Type())
	}

	pastLink, err := links.route(home, dest)
	if err != nil {
		return err
	}
	parent := filepath.Dir(dst)
	if err := ensureOverlayParent(parent, roots, dest); err != nil {
		return err
	}
	cur, curErr := os.Lstat(dst)
	if curErr == nil && cur.Mode()&os.ModeSymlink != 0 && !pastLink {
		target, _ := os.Readlink(dst)
		return fmt.Errorf("not installing ~/%s: it is a symbolic link (to %s) in the account "+
			"home, and the overlay neither follows a link at a destination nor replaces one "+
			"there. Remove the link and launch again", dest, target)
	}
	if curErr != nil && !os.IsNotExist(curErr) {
		return curErr
	}

	base := filepath.Base(dst)
	staged := filepath.Join(parent, "."+base+overlayStagedSuffix)
	aside := filepath.Join(parent, "."+base+overlayAsideSuffix)
	// Leftovers of an install that crashed. Both hold only overlay content — the staged copy
	// a half-built replacement, the aside copy the previous delivery — so removing them
	// loses nothing of the agent's.
	for _, leftover := range []string{staged, aside} {
		if err := os.RemoveAll(leftover); err != nil {
			return fmt.Errorf("clearing a previous install's %s: %w", leftover, err)
		}
	}

	// Build the whole replacement before touching the destination. Strict: a partial copy
	// renamed into place would look like a complete delivery.
	if srcInfo.IsDir() {
		err = os.Mkdir(staged, 0o755)
		if err == nil {
			err = copyTreeStrict(src, staged)
		}
	} else {
		err = copyFileStrict(src, staged, srcInfo.Mode().Perm())
	}
	if err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("staging ~/%s: %w", dest, err)
	}

	// Swap. A file over a file (or over nothing) is one atomic rename. A directory cannot be
	// renamed over a non-empty one, so the old copy steps aside first and is removed after.
	if os.IsNotExist(curErr) || (!cur.IsDir() && !srcInfo.IsDir()) {
		if err := os.Rename(staged, dst); err != nil {
			_ = os.RemoveAll(staged)
			return fmt.Errorf("installing ~/%s: %w", dest, err)
		}
		return nil
	}
	if err := os.Rename(dst, aside); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("moving the previous ~/%s aside: %w", dest, err)
	}
	if err := os.Rename(staged, dst); err != nil {
		// Put the previous delivery back rather than leave the destination empty.
		_ = os.Rename(aside, dst)
		_ = os.RemoveAll(staged)
		return fmt.Errorf("installing ~/%s: %w", dest, err)
	}
	// The delivery is in place. A failure to remove the previous copy leaves it under the
	// aside name, where the next install removes it before building its own.
	_ = os.RemoveAll(aside)
	return nil
}

// ensureOverlayParent creates a destination's parent directory, refusing when the parent
// resolves — through any symlink on the way — outside every root. The check runs on the
// nearest EXISTING ancestor before anything is created, so a link aimed elsewhere cannot
// even have directories made under its target, and again on the created parent.
func ensureOverlayParent(parent string, roots []string, dest string) error {
	existing := parent
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		up := filepath.Dir(existing)
		if up == existing {
			break
		}
		existing = up
	}
	if err := requireWithinRoots(existing, roots, dest); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("creating the directory for ~/%s: %w", dest, err)
	}
	return requireWithinRoots(parent, roots, dest)
}

// requireWithinRoots resolves path and refuses it unless it lies at or below one of roots.
func requireWithinRoots(path string, roots []string, dest string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolving the directory for ~/%s: %w", dest, err)
	}
	for _, r := range roots {
		if resolved == r || strings.HasPrefix(resolved, r+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("not installing ~/%s: its directory %s resolves to %s, outside the "+
		"sandbox home and the workspace's own sidecar (%s). A symbolic link on the way there "+
		"points out of the jail's own directories; remove it and launch again",
		dest, path, resolved, strings.Join(roots, ", "))
}

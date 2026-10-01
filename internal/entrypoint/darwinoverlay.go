package entrypoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
//
// ⚠ AND IT REACHES A DESTINATION THROUGH NO LINK THE LAYOUT DID NOT LAY (G14). The session's
// Seatbelt profile write-protects each destination at the path the layout lays, so a delivery
// that followed any other link would land where no rule names it (overlayLinks.route). The
// list is the same one the profile protects: WriteHomeOverlayManifest returns what it wrote,
// and the host hands exactly that to the profile as macosuser.HomeOverlay.Dests.

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
	// Sorted, a destination's every ancestor comes before it (a prefix sorts first), so one
	// pass that checks each candidate's ANCESTORS against what it kept drops every nested
	// one. Comparing with the last kept entry alone is not enough: `-` and `.` sort before
	// `/`, so `.a/skills-extra` sorts between `.a/skills` and `.a/skills/sub`.
	sort.Strings(clean)
	kept := map[string]bool{}
	var out []string
	for _, d := range clean {
		if kept[d] || hasKeptAncestor(d, kept) {
			continue
		}
		kept[d] = true
		out = append(out, d)
	}
	return out, nil
}

// hasKeptAncestor reports whether any directory above the slash path d is in kept.
func hasKeptAncestor(d string, kept map[string]bool) bool {
	for up := path.Dir(d); up != "." && up != "/"; up = path.Dir(up) {
		if kept[up] {
			return true
		}
	}
	return false
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
				"protect. Remove the link (what it points at is left alone):\n  sudo rm %s", cur,
				shquote.Quote(cur))
		}
		if last {
			return true, nil
		}
		return true, fmt.Errorf("nothing was delivered at ~/%s: %s, on its way through the "+
			"workspace sidecar, is a symbolic link the layout did not lay, and delivering "+
			"through it would land where the session's sandbox profile does not protect. "+
			"Remove the link (what it points at is left alone):\n  sudo rm %s", dest, cur,
			shquote.Quote(cur))
	}
	return pastLink, nil
}

// overlayInstallHook runs at four moments of each destination's install — "routed", once its
// path has passed the layout check (overlayLinks.route) and before its directory is resolved;
// "checked", once the directory that holds it has been resolved and found under a root and
// before it is opened; "opened", once it is open and before anything in it is touched; and
// "staged", once the replacement is built and before the swap — nil in production. They are
// the windows in which a concurrent session could swap that directory for a link, and a test
// swaps one there.
var overlayInstallHook func(dest, window string)

// overlayRoot is one directory an install may write beneath: path is its resolved spelling,
// used only to find which root holds a destination's directory, and root is the handle every
// read and write below it goes through. sidecar marks the workspace sidecar, where the layout
// lays no links, so a link found AT a destination there is an occupant to replace rather than
// one to refuse (installOverlayDestination).
type overlayRoot struct {
	path    string
	root    *os.Root
	sidecar bool
}

// installHomeOverlayDestinations installs every listed destination of overlay into home.
// Each destination is installed on its own, and a failure in one does not stop the others
// — the same "report every problem in one boot" rule genStep follows.
//
// `roots` are the directories an install may write beneath (openOverlayInstallRoots): the
// home and, when the layout laid one, the workspace sidecar its links point into. `links` are
// the layout's own links, the only ones a destination's route may pass through
// (overlayLinks.route).
func installHomeOverlayDestinations(overlay, home string, roots []overlayRoot, links overlayLinks) error {
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

// openOverlayInstallRoots opens the directories an overlay install may write beneath: the
// home, and the workspace sidecar when the launch named one and it exists. Each path is
// resolved once, so a home or sidecar behind a symlink (macOS's /var → /private/var)
// compares equal to the resolved directories it is matched against. The caller closes the
// handles (closeOverlayRoots).
//
// The sidecar is opened refusing a link AT it or at the `.yolo` above it: both are in the
// workspace, which the sandbox account can write, and the launcher refuses a launch with a
// link at either (run.linkedWorkspaceState), so one here was put there after that check.
func openOverlayInstallRoots(home, sidecar string) ([]overlayRoot, error) {
	h, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolving the home %s: %w", home, err)
	}
	hr, err := os.OpenRoot(h)
	if err != nil {
		return nil, fmt.Errorf("opening the home %s: %w", home, err)
	}
	roots := []overlayRoot{{path: h, root: hr}}
	if sidecar == "" {
		return roots, nil
	}
	if _, err := os.Lstat(sidecar); os.IsNotExist(err) {
		return roots, nil
	}
	sr, err := openSidecarRoot(sidecar)
	if err != nil {
		closeOverlayRoots(roots)
		return nil, fmt.Errorf("opening the workspace sidecar %s: %w", sidecar, err)
	}
	s, err := filepath.EvalSymlinks(sidecar)
	if err != nil {
		sr.Close()
		closeOverlayRoots(roots)
		return nil, fmt.Errorf("resolving the workspace sidecar %s: %w", sidecar, err)
	}
	return append(roots, overlayRoot{path: s, root: sr, sidecar: true}), nil
}

// openSidecarRoot opens sidecar as an os.Root beneath a root on its parent, refusing a link
// at either (paths.OpenStateDirRoot, paths.OpenStateSubdirRoot).
func openSidecarRoot(sidecar string) (*os.Root, error) {
	sidecar = filepath.Clean(sidecar)
	parent, err := paths.OpenStateDirRoot(filepath.Dir(sidecar))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return paths.OpenStateSubdirRoot(parent, filepath.Base(sidecar), sidecar)
}

func closeOverlayRoots(roots []overlayRoot) {
	for _, r := range roots {
		r.root.Close()
	}
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
// layout's own (overlayLinks.route refuses any other, G14), and the directory they resolve to
// must also lie under one of `roots`, because this runs outside Seatbelt with the sandbox
// account's full reach and a link the agent planted would otherwise aim the write at a
// directory the agent itself cannot reach. The destination ITSELF is never followed, which
// would replace whatever it points at. In the account home a link there is refused:
// replacing it would put a real directory where a layout link may belong (which the next boot
// refuses, OQ-HT2). Past a layout link, in the sidecar, where the layout lays no links, it is
// an occupant like any other and is replaced — the rename moves the LINK aside and the removal
// unlinks it, so what it points at is never touched.
//
// ⚠ EVERY OPERATION AFTER THE CHECK GOES THROUGH A HANDLE ON THE CHECKED DIRECTORY, never
// through its path. The agent may be running while this does (the launch lock is released
// before the agent, and the account home is shared by every workspace), so it can swap that
// directory, or any directory above it, for a link between two calls. A path would be
// resolved again by each call and follow the new link; the handle names the directory that
// was checked, and os.Root refuses a component swapped for a link that leaves it. A swap
// can make the install fail or land in the directory the agent moved, never outside.
func installOverlayDestination(overlay, home string, roots []overlayRoot, links overlayLinks, dest string) error {
	rel := filepath.FromSlash(dest)
	src := filepath.Join(overlay, rel)
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
	if overlayInstallHook != nil {
		overlayInstallHook(dest, "routed")
	}
	dir, inSidecar, err := openOverlayParent(filepath.Dir(filepath.Join(home, rel)), roots, dest)
	if err != nil {
		return err
	}
	defer dir.Close()
	if overlayInstallHook != nil {
		overlayInstallHook(dest, "opened")
	}

	base := filepath.Base(rel)
	cur, curErr := dir.Lstat(base)
	// Replaced only when BOTH say sidecar: the route, by path, and the root the handle was
	// opened beneath. A concurrent swap can make them disagree, and then the link is refused.
	if curErr == nil && cur.Mode()&os.ModeSymlink != 0 && !(pastLink && inSidecar) {
		target, _ := dir.Readlink(base)
		return fmt.Errorf("not installing ~/%s: it is a symbolic link (to %s) in the account "+
			"home, and the overlay neither follows a link at a destination nor replaces one "+
			"there. Remove the link and launch again", dest, target)
	}
	if curErr != nil && !os.IsNotExist(curErr) {
		return curErr
	}

	staged := "." + base + overlayStagedSuffix
	aside := "." + base + overlayAsideSuffix
	// Leftovers of an install that crashed. Both hold only overlay content — the staged copy
	// a half-built replacement, the aside copy the previous delivery — so removing them
	// loses nothing of the agent's.
	for _, leftover := range []string{staged, aside} {
		if err := dir.RemoveAll(leftover); err != nil {
			return fmt.Errorf("clearing a previous install's %s beside ~/%s: %w", leftover, dest, err)
		}
	}

	// Build the whole replacement before touching the destination. Strict: a partial copy
	// renamed into place would look like a complete delivery.
	if srcInfo.IsDir() {
		err = dir.Mkdir(staged, 0o755)
		if err == nil {
			err = copyTreeStrictBeneath(src, dir, staged)
		}
	} else {
		err = copyFileStrictBeneath(src, dir, staged, srcInfo.Mode().Perm())
	}
	if err != nil {
		_ = dir.RemoveAll(staged)
		return fmt.Errorf("staging ~/%s: %w", dest, err)
	}
	if overlayInstallHook != nil {
		overlayInstallHook(dest, "staged")
	}

	// Swap. A file over a file (or over nothing) is one atomic rename. A directory cannot be
	// renamed over a non-empty one, so the old copy steps aside first and is removed after.
	if os.IsNotExist(curErr) || (!cur.IsDir() && !srcInfo.IsDir()) {
		if err := dir.Rename(staged, base); err != nil {
			_ = dir.RemoveAll(staged)
			return fmt.Errorf("installing ~/%s: %w", dest, err)
		}
		return nil
	}
	if err := dir.Rename(base, aside); err != nil {
		_ = dir.RemoveAll(staged)
		return fmt.Errorf("moving the previous ~/%s aside: %w", dest, err)
	}
	if err := dir.Rename(staged, base); err != nil {
		// Put the previous delivery back rather than leave the destination empty.
		_ = dir.Rename(aside, base)
		_ = dir.RemoveAll(staged)
		return fmt.Errorf("installing ~/%s: %w", dest, err)
	}
	// The delivery is in place. A failure to remove the previous copy leaves it under the
	// aside name, where the next install removes it before building its own.
	_ = dir.RemoveAll(aside)
	return nil
}

// openOverlayParent returns a handle on parent, the directory that holds dest, creating it
// and any missing directory above it, and whether that handle is beneath the workspace
// sidecar's root. It refuses when parent resolves — through any symlink on the way — outside
// every root.
//
// The nearest EXISTING ancestor is resolved and checked before anything is created, so a
// link aimed elsewhere cannot even have directories made under its target. The missing rest
// is then created, and the handle opened, BENEATH THE ROOT that holds that ancestor, by its
// resolved path relative to the root: os.Root resolves every component itself and refuses
// one that leaves the root, including a component swapped for a link after the check.
func openOverlayParent(parent string, roots []overlayRoot, dest string) (*os.Root, bool, error) {
	existing, missing := parent, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		up := filepath.Dir(existing)
		if up == existing {
			break
		}
		missing = filepath.Join(filepath.Base(existing), missing)
		existing = up
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return nil, false, fmt.Errorf("resolving the directory for ~/%s: %w", dest, err)
	}
	r, relToRoot, ok := containingOverlayRoot(roots, resolved)
	if !ok {
		var names []string
		for _, r := range roots {
			names = append(names, r.path)
		}
		return nil, false, fmt.Errorf("not installing ~/%s: its directory %s resolves to %s, outside "+
			"the sandbox home and the workspace's own sidecar (%s). A symbolic link on the way "+
			"there points out of the jail's own directories; remove it and launch again",
			dest, parent, filepath.Join(resolved, missing), strings.Join(names, ", "))
	}
	if overlayInstallHook != nil {
		overlayInstallHook(dest, "checked")
	}
	target := filepath.Join(relToRoot, missing)
	if missing != "" {
		if err := r.root.MkdirAll(target, 0o755); err != nil {
			return nil, false, fmt.Errorf("creating the directory for ~/%s: %w", dest, err)
		}
	}
	dir, err := r.root.OpenRoot(target)
	if err != nil {
		return nil, false, fmt.Errorf("opening the directory for ~/%s: %w", dest, err)
	}
	return dir, r.sidecar, nil
}

// containingOverlayRoot returns the root whose path holds resolved, and resolved relative to
// it. The DEEPEST such root wins, so a sidecar that happened to lie below the home would still
// be recognized as the sidecar.
func containingOverlayRoot(roots []overlayRoot, resolved string) (overlayRoot, string, bool) {
	var best overlayRoot
	var bestRel string
	found := false
	for _, r := range roots {
		if resolved != r.path && !strings.HasPrefix(resolved, r.path+string(filepath.Separator)) {
			continue
		}
		if found && len(r.path) <= len(best.path) {
			continue
		}
		if rel, err := filepath.Rel(r.path, resolved); err == nil {
			best, bestRel, found = r, rel, true
		}
	}
	return best, bestRel, found
}

// copyTreeStrictBeneath is copyTreeStrict with the destination beneath an os.Root: it copies
// the tree src into rel below dst, which must exist and be empty, and returns the FIRST error
// it meets. Symlinks are recreated, not followed; a directory is created with Mkdir and a
// file with O_EXCL, so nothing already there — a link swapped in, above all — is written
// through. The source is read by path: it is the root-owned staged overlay, which the sandbox
// account cannot write.
func copyTreeStrictBeneath(src string, dst *os.Root, rel string) error {
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range ents {
		from := filepath.Join(src, ent.Name())
		to := filepath.Join(rel, ent.Name())
		fi, err := os.Lstat(from)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(from)
			if err != nil {
				return err
			}
			if err := dst.Symlink(target, to); err != nil {
				return err
			}
		case fi.IsDir():
			if err := dst.Mkdir(to, fi.Mode().Perm()); err != nil {
				return err
			}
			if err := copyTreeStrictBeneath(from, dst, to); err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := copyFileStrictBeneath(from, dst, to, fi.Mode().Perm()); err != nil {
				return err
			}
		default:
			// A socket, fifo or device node: skipping it would be a partial copy reported as
			// a whole one (copyTreeStrict's rule).
			return fmt.Errorf("%s is a %s, which this copy cannot reproduce", from, fi.Mode().Type())
		}
	}
	return nil
}

// copyFileStrictBeneath is copyFileStrict with the destination beneath an os.Root, created
// O_EXCL: rel must not exist.
func copyFileStrictBeneath(src string, dst *os.Root, rel string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := dst.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

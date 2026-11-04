package entrypoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

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
	// Digest is the tree's content identity, computed by the host from the inputs it laid the
	// tree out from (run.homeOverlayDigest): two overlays with one digest are one tree. The host
	// keeps its own overlay, and skips re-staging the sealed copy, when the digest it would write
	// is the one already there (HomeOverlayDigest). Empty matches nothing.
	Digest string `json:"digest,omitempty"`
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

// inheritedCoreConfigDestination matches only the four core files whose staged source is sealed
// and whose owner destination must not be refreshed by the general home overlay.
func inheritedCoreConfigDestination(dest string) bool {
	switch dest {
	case ".config/yolo-jail/config.toml", ".config/yolo-jail/config.jsonc",
		".config/yolo-jail/inherited-launch.toml", ".config/yolo-jail/inherited-launch.jsonc":
		return true
	default:
		return false
	}
}

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
	return WriteHomeOverlayManifestDigest(overlay, dests, "")
}

// WriteHomeOverlayManifestDigest is WriteHomeOverlayManifest recording digest, the tree's
// content identity, in the list. The host writes the list LAST, after every destination is laid
// out, so a list carrying a digest marks a complete tree: a build that died part way left none.
func WriteHomeOverlayManifestDigest(overlay string, dests []string, digest string) ([]string, error) {
	out, err := normalizeOverlayDests(dests)
	if err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(homeOverlayManifest{Destinations: out, Digest: digest}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(overlay, HomeOverlayManifestName), append(body, '\n'), 0o644); err != nil {
		return nil, err
	}
	return out, nil
}

// HomeOverlayDigest returns the digest an overlay's destination list carries and the list itself,
// normalized as the install reads it. ok is false for an overlay with no list, an unreadable or
// invalid one, or one written with no digest — none of which is a complete tree this host can
// vouch for. The list is opened without following a link at it.
func HomeOverlayDigest(overlay string) (digest string, dests []string, ok bool) {
	f, err := os.OpenFile(filepath.Join(overlay, HomeOverlayManifestName), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", nil, false
	}
	var m homeOverlayManifest
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&m); err != nil || m.Digest == "" {
		return "", nil, false
	}
	out, err := normalizeOverlayDests(m.Destinations)
	if err != nil {
		return "", nil, false
	}
	return m.Digest, out, true
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
	// yours is set when the home is the invoking user's own (the run-as-you layout, HOME the
	// workspace sidecar): a remedy that removes something there needs no sudo.
	yours bool
}

// rm is the removal command a remedy names: plain rm in a home that is yours, sudo rm in the
// sandbox account's.
func (l overlayLinks) rm() string {
	if l.yours {
		return "rm"
	}
	return "sudo rm"
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
				"protect. Remove the link (what it points at is left alone):\n  %s %s", cur,
				l.rm(), shquote.Quote(cur))
		}
		if last {
			return true, nil
		}
		return true, fmt.Errorf("nothing was delivered at ~/%s: %s, on its way through the "+
			"workspace sidecar, is a symbolic link the layout did not lay, and delivering "+
			"through it would land where the session's sandbox profile does not protect. "+
			"Remove the link (what it points at is left alone):\n  %s %s", dest, cur,
			l.rm(), shquote.Quote(cur))
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
//
// It then removes what an earlier install delivered and this overlay no longer lists
// (retireOverlayDestinations), and records what it delivered for the next install to do the
// same (HomeOverlayInstalledName). Neither ever fails the boot: what they could not do is a
// line through warn. With no sidecar root there is nowhere to keep the record, so neither runs.
func installHomeOverlayDestinations(overlay, home string, roots []overlayRoot, links overlayLinks, warn func(string)) error {
	dests, err := readHomeOverlayManifest(overlay)
	if err != nil {
		return err
	}
	sidecar := sidecarOverlayRoot(roots)
	var prev overlayInstallRecord
	if sidecar != nil {
		prev = readOverlayInstallRecord(sidecar.root)
	}
	src, srcErr := os.OpenRoot(overlay)
	if srcErr == nil {
		defer src.Close()
	}
	installed := map[string]string{}
	var errs []error
	for _, d := range dests {
		if err := installOverlayDestination(overlay, home, roots, links, d); err != nil {
			errs = append(errs, err)
			// The home still holds the previous delivery, if any: keep what proves it yolo's.
			if dg, ok := prev.Digests[d]; ok {
				installed[d] = dg
			}
			continue
		}
		if srcErr == nil {
			if dg, err := overlayTreeDigest(src, filepath.FromSlash(d)); err == nil {
				installed[d] = dg
			}
		}
	}
	if sidecar != nil {
		settleOverlayRecord(home, roots, links, *sidecar, dests, installed, prev, warn)
	}
	return errors.Join(errs...)
}

// retireUndeliveredOverlay is the install of a launch that delivers NOTHING — no pack it selects
// ships a skill, briefing or file, so the launcher staged no overlay — run for its other half:
// every destination an earlier launch recorded is no longer listed, and leaves the home as
// retireOverlayDestinations decides. With no sidecar root, or nothing recorded, it does nothing.
func retireUndeliveredOverlay(home string, roots []overlayRoot, links overlayLinks, warn func(string)) {
	sidecar := sidecarOverlayRoot(roots)
	if sidecar == nil {
		return
	}
	prev := readOverlayInstallRecord(sidecar.root)
	if len(prev.Destinations) == 0 {
		return
	}
	settleOverlayRecord(home, roots, links, *sidecar, nil, nil, prev, warn)
}

// settleOverlayRecord retires what prev records and dests no longer lists, then records dests
// with the digests in installed, plus whatever the retire left.
func settleOverlayRecord(home string, roots []overlayRoot, links overlayLinks, sidecar overlayRoot,
	dests []string, installed map[string]string, prev overlayInstallRecord, warn func(string)) {
	left := retireOverlayDestinations(home, roots, links, dests, prev, warn)
	if err := writeOverlayInstallRecord(sidecar.root, dests, installed, left); err != nil {
		warn(fmt.Sprintf("the home overlay's install record (%s in %s) was not saved: %v. A "+
			"destination a later launch stops delivering may then stay in the home unnamed; "+
			"launch again to retry", HomeOverlayInstalledName, sidecar.path, err))
	}
}

// HomeOverlayInstalledName is the INSTALL RECORD (MMP-D11): the file in the workspace sidecar
// listing the destinations the last overlay install delivered, each with the digest of the tree
// it delivered there (overlayTreeDigest). It is how a destination a later launch no longer lists
// leaves the home, the way an unmounted bind vanishes on podman: without it the install knew
// only what the overlay lists NOW, and every destination any pack ever shipped stayed.
//
// It lives in the sidecar because the account home is shared by every workspace. The sidecar is
// agent-writable, so nothing in the record is trusted: an entry that is not a path below the home
// is ignored, a removal goes through the same route and roots as an install, and only a tree
// whose digest still matches is removed — so a forged entry can at most remove a tree identical
// to one yolo delivered, or put a path in a line.
const HomeOverlayInstalledName = ".yolo-home-overlay-installed.json"

// overlayInstallRecord is the install record's wire shape. Destinations keeps the overlay
// list's field name; Digests maps a destination to the tree yolo delivered there.
type overlayInstallRecord struct {
	Destinations []string          `json:"destinations"`
	Digests      map[string]string `json:"digests,omitempty"`
}

// OverlayLeftAction prefixes the line for a dropped destination the install leaves because yolo
// cannot prove it is still what yolo delivered — the overlay's twin of HostFilesLeftAction.
const OverlayLeftAction = "left in place: "

func sidecarOverlayRoot(roots []overlayRoot) *overlayRoot {
	for i := range roots {
		if roots[i].sidecar {
			return &roots[i]
		}
	}
	return nil
}

// readOverlayInstallRecord reads the record from the sidecar root, or an empty one: a record
// that is absent, a link, not a regular file, over the size cap or not JSON is read as empty.
// Each entry is checked on its own (cleanOverlayDest), so one bad entry drops only itself.
func readOverlayInstallRecord(root *os.Root) overlayInstallRecord {
	f, err := root.OpenFile(HomeOverlayInstalledName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return overlayInstallRecord{}
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return overlayInstallRecord{}
	}
	var raw overlayInstallRecord
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&raw); err != nil {
		return overlayInstallRecord{}
	}
	var clean []string
	digests := map[string]string{}
	for _, d := range raw.Destinations {
		c, err := cleanOverlayDest(d)
		if err != nil {
			continue
		}
		clean = append(clean, c)
		if dg := raw.Digests[d]; dg != "" {
			digests[c] = dg
		}
	}
	out, _ := normalizeOverlayDests(clean) // cannot fail: every entry is clean already
	return overlayInstallRecord{Destinations: out, Digests: digests}
}

// writeOverlayInstallRecord records the destinations this install delivered, and the dropped
// ones it left so the next launch names them again, by rename over the old record — or removes
// the record when there is nothing to record.
func writeOverlayInstallRecord(root *os.Root, dests []string, installed, left map[string]string) error {
	if len(dests) == 0 && len(left) == 0 {
		if err := root.Remove(HomeOverlayInstalledName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	rec := overlayInstallRecord{Destinations: append([]string(nil), dests...), Digests: map[string]string{}}
	for d, dg := range installed {
		rec.Digests[d] = dg
	}
	for d, dg := range left {
		rec.Destinations = append(rec.Destinations, d)
		if dg != "" {
			rec.Digests[d] = dg
		}
	}
	sort.Strings(rec.Destinations)
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := HomeOverlayInstalledName + ".new"
	_ = root.Remove(tmp)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, HomeOverlayInstalledName)
}

// retireOverlayDestinations removes each destination the record names that dests no longer
// lists or covers, and returns the ones it left, with their recorded digests, for the record to
// keep naming.
//
// ONLY A TREE YOLO CAN PROVE IS ITS OWN DELIVERY IS REMOVED: one whose digest still matches the
// one recorded when it was delivered. Anything else — changed since, no digest recorded, or a
// path the install could not reach safely — is the user's or the agent's: it stays, and one line
// names it and the rm that removes it, on every launch until someone decides. A recorded path
// already gone is forgotten silently. A recorded destination that is now the PARENT of a listed
// one is forgotten without a removal, because removing it would delete the new delivery and the
// agent state beside it.
func retireOverlayDestinations(home string, roots []overlayRoot, links overlayLinks, dests []string, prev overlayInstallRecord, warn func(string)) map[string]string {
	left := map[string]string{}
	for _, r := range prev.Destinations {
		if overlayDestCovered(r, dests) {
			continue
		}
		recorded, hasDigest := prev.Digests[r]
		leave := func(why string) {
			left[r] = recorded
			warn(fmt.Sprintf("%s~/%s: no pack this launch selects delivers it any more, but %s — "+
				"remove it with `%s -r %s` if you do not want it", OverlayLeftAction, r, why,
				links.rm(), shquote.Quote(filepath.Join(home, filepath.FromSlash(r)))))
		}
		dir, base, gone, err := openOverlayDestination(home, roots, links, r)
		if gone {
			continue
		}
		if err != nil {
			leave("yolo could not reach it safely to remove it (" + err.Error() + ")")
			continue
		}
		now, derr := overlayTreeDigest(dir, base)
		switch {
		case derr != nil:
			leave("yolo could not read it to check it is what yolo delivered (" + derr.Error() + ")")
		case !hasDigest:
			leave("yolo has no record of what it delivered there")
		case now != recorded:
			leave("it changed since yolo delivered it")
		default:
			if rerr := dir.RemoveAll(base); rerr != nil {
				leave("removing it failed (" + rerr.Error() + ")")
			} else {
				warn(fmt.Sprintf("removed ~/%s: no pack this launch selects delivers it any more", r))
			}
		}
		dir.Close()
	}
	return left
}

// overlayDestCovered reports whether the recorded destination is still delivered by dests, or
// must not be removed for their sake: equal to a listed destination, inside one, or above one.
func overlayDestCovered(recorded string, dests []string) bool {
	for _, d := range dests {
		if recorded == d || strings.HasPrefix(recorded, d+"/") || strings.HasPrefix(d, recorded+"/") {
			return true
		}
	}
	return false
}

// openOverlayDestination returns a handle on the directory holding dest and dest's name in it,
// reached exactly as an install reaches it (overlayLinks.route, openOverlayParentIn) but
// creating nothing. gone reports that dest, or a directory above it, does not exist.
func openOverlayDestination(home string, roots []overlayRoot, links overlayLinks, dest string) (dir *os.Root, base string, gone bool, err error) {
	if _, err := links.route(home, dest); err != nil {
		return nil, "", false, err
	}
	rel := filepath.FromSlash(dest)
	dir, _, err = openOverlayParentIn(filepath.Dir(filepath.Join(home, rel)), roots, dest, false)
	if errors.Is(err, errOverlayParentMissing) {
		return nil, "", true, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	base = filepath.Base(rel)
	if _, err := dir.Lstat(base); err != nil {
		dir.Close()
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", true, nil
		}
		return nil, "", false, err
	}
	return dir, base, false, nil
}

// overlayTreeDigest is the content identity of the file or tree at name beneath r: every entry's
// path relative to name, its kind, a file's content digest and a link's target, walked without
// following a link. Modes are not part of it, so it is the same for the sealed staged tree and
// the copies an install made of it.
func overlayTreeDigest(r *os.Root, name string) (string, error) {
	h := sha256.New()
	var walk func(at, rel string) error
	walk = func(at, rel string) error {
		fi, err := r.Lstat(at)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := r.Readlink(at)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "l %q %q\n", rel, target)
		case fi.IsDir():
			fmt.Fprintf(h, "d %q\n", rel)
			f, err := r.OpenFile(at, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
			if err != nil {
				return err
			}
			names, err := f.Readdirnames(-1)
			f.Close()
			if err != nil {
				return err
			}
			sort.Strings(names)
			for _, n := range names {
				if err := walk(filepath.Join(at, n), path.Join(rel, n)); err != nil {
					return err
				}
			}
		case fi.Mode().IsRegular():
			f, err := r.OpenFile(at, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			fh := sha256.New()
			_, err = io.Copy(fh, f)
			f.Close()
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "f %q %x\n", rel, fh.Sum(nil))
		default:
			fmt.Fprintf(h, "o %q %v\n", rel, fi.Mode().Type())
		}
		return nil
	}
	if err := walk(name, "."); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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

	inheritedConfig := inheritedCoreConfigDestination(dest)
	// recorded is set when an existing inherited destination is a delivery the record lists
	// (admitRecordedInheritedDelivery): it is replaced below, through a move aside checked against
	// the very file that was hashed, rather than refused (issue #70).
	var recorded *recordedInheritedDelivery
	if inheritedConfig {
		if !srcInfo.Mode().IsRegular() || srcInfo.Mode().Perm() != 0o444 {
			return fmt.Errorf("refusing to install inherited core config ~/%s: its staged source is not a sealed 0444 regular file; repair the staged overlay and relaunch", dest)
		}
		if curErr == nil {
			adm, err := admitRecordedInheritedDelivery(overlay, dest, src, srcInfo, dir, base)
			if err != nil {
				return fmt.Errorf("refusing to install inherited core config ~/%s: an existing destination is neither a delivery yolo recorded nor the staged file an earlier yolo linked there; preserve it, inspect the path (move it aside if it is yours to keep), then relaunch", dest)
			}
			if adm.current {
				// Already this delivery's bytes, sealed, in a copy of its own: nothing to write, and
				// the inode stays (MMP-D35).
				return nil
			}
			recorded = &adm
		}
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

	// ALREADY THIS DELIVERY: every file at the destination is a copy with the staged file's mode
	// and bytes, and nothing else is there (overlayInstalledFrom, MMP-D35). An unchanged relaunch
	// rewrites nothing and the delivered inodes stay; a destination edited between sessions fails
	// the comparison and is replaced as before.
	if curErr == nil && overlayInstalledFrom(src, srcInfo, dir, base) {
		return nil
	}

	// Build the whole replacement before touching the destination. Strict: a partial copy
	// renamed into place would look like a complete delivery. Every file is a copy of the staged
	// one, never a hard link (copyTreeStrictBeneath says why); a copy is the design, so it is not
	// reported, and only a copy that FAILS is.
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
		return fmt.Errorf("staging ~/%s: %w; the previous delivery there, if any, is left as it was. "+
			"Fix what the error names (free space or permissions in %s, or the staged file), then relaunch",
			dest, err, filepath.Dir(filepath.Join(home, rel)))
	}
	if overlayInstallHook != nil {
		overlayInstallHook(dest, "staged")
	}

	if inheritedConfig {
		parent, err := dir.Open(".")
		if err != nil {
			_ = dir.RemoveAll(staged)
			return fmt.Errorf("opening checked parent of inherited core config ~/%s: %w; inspect the path and relaunch", dest, err)
		}
		defer parent.Close()
		pfd := int(parent.Fd())
		if recorded != nil {
			// THE RECORDED DELIVERY STEPS ASIDE FIRST, and only the file that was hashed may, with
			// the bytes that were hashed (recordedInheritedDelivery.stillAt): a destination the
			// agent swapped in or rewrote after the check is put back and refused, never replaced. Both renames refuse to replace, so nothing the agent laid at either name
			// is overwritten.
			if err := renameNoReplaceBetweenAt(pfd, base, pfd, aside); err != nil {
				_ = dir.RemoveAll(staged)
				return fmt.Errorf("moving aside the previous inherited core config ~/%s: %w; inspect the path and relaunch", dest, err)
			}
			if !recorded.stillAt(pfd, aside) {
				_ = renameNoReplaceBetweenAt(pfd, aside, pfd, base)
				_ = dir.RemoveAll(staged)
				return fmt.Errorf("refusing to replace inherited core config ~/%s: it changed during installation; preserve it, inspect the path, then relaunch", dest)
			}
		}
		if err := renameNoReplaceBetweenAt(pfd, staged, pfd, base); err != nil {
			if recorded != nil {
				if rerr := renameNoReplaceBetweenAt(pfd, aside, pfd, base); rerr != nil {
					_ = dir.RemoveAll(staged)
					return fmt.Errorf("refusing to replace inherited core config ~/%s: a file appeared during installation, and the previous delivery stays at %s beside it; preserve both, inspect the path, then relaunch", dest, aside)
				}
			}
			_ = dir.RemoveAll(staged)
			if errors.Is(err, os.ErrExist) {
				return fmt.Errorf("refusing to replace inherited core config ~/%s: a file appeared during installation; preserve it, inspect the path, then relaunch", dest)
			}
			return fmt.Errorf("installing inherited core config ~/%s without replacement: %w; inspect the path and relaunch", dest, err)
		}
		if recorded != nil {
			// Only yolo's own previous delivery is under the aside name; a failure here leaves it
			// for the next install's leftover sweep.
			_ = dir.Remove(aside)
		}
		return nil
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
	return openOverlayParentIn(parent, roots, dest, true)
}

// errOverlayParentMissing is openOverlayParentIn's answer, with create false, for a directory
// that does not exist.
var errOverlayParentMissing = errors.New("the directory does not exist")

// openOverlayParentIn is openOverlayParent, creating the missing directories only when create
// is set; otherwise a missing one is errOverlayParentMissing, and nothing is created.
func openOverlayParentIn(parent string, roots []overlayRoot, dest string, create bool) (*os.Root, bool, error) {
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
	if missing != "" && !create {
		return nil, false, errOverlayParentMissing
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

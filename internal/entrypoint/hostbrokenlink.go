package entrypoint

// hostbrokenlink.go is the BROKEN-LINK RULE for the destinations a host apply writes THROUGH — a
// config surface and a composed briefing: a destination that is a symlink into a directory that
// does not exist is refused by name, per destination, and never fails the pack it belongs to. The
// `files` kind is not a caller: it archives a destination it wrote before replacing it, a link
// included, refuses one it did not write, and turns any write error into that destination's own
// `refused:` line, so a broken link there already costs one destination and never the pack.
//
// A **broken link** *(coined here)* is a symlink on a destination's path — the destination itself
// or one of its parent directories — whose chain ends at a path whose DIRECTORY does not exist.
// A dotfiles manager (rcm, stow, a home-manager checkout) leaves exactly that when the directory
// it linked into is deleted or moved. Measured on the maintainer's host 2026-09-28:
// ~/.pi/agent/settings.json linked into ~/.dotfiles/pi, which had been deleted when that config
// moved into a pack; the write followed the link, open(2) failed ENOENT, RenderHostPack returned
// the error, and the whole pi pack — and with it the launch of an unrelated agent — failed.
//
// WHY A REFUSAL, and not "create the missing directory" or "replace the link": both of those
// decide something about the user's dotfiles layout that yolo cannot know. Creating the target's
// directory recreates a tree the user deleted, somewhere they are no longer looking; replacing the
// link silently detaches the destination from whatever manages it. The link is the user's, so the
// report names it and its target, and the remedy is theirs to pick.
//
// WHAT IS NOT BROKEN. A link whose target does not exist but whose target's directory DOES is a
// dotfiles checkout that has not created the file yet, and HC-D4 rules that it is written THROUGH
// (hostcreatedfile_test.go's dangling-link case, reported as a change). A plain missing
// destination, with or without its parent directories, is created. Only the case the writer cannot
// complete without inventing a directory is refused.
//
// THE READ SIDE IS FindDanglingLink, in this file because it takes the same walk up the path
// (unresolvedLinkOnPath) and along the same chain (linkChainEnd), and a second walker is what
// docs/design/agent-directory-map.md AM-D5 rules out. A file yolo
// READS has no write-through exemption: a link to a missing file reads nothing whether or not the
// target's directory exists.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// BrokenLink names a destination's broken link: Link is the symlink on the destination's path,
// Target the end of its chain — the path whose directory does not exist.
type BrokenLink struct {
	Link   string
	Target string
}

// Reason is the refusal sentence: what is wrong, and the remedy. The file is never touched, so
// the sentence says so.
func (b BrokenLink) Reason() string {
	return fmt.Sprintf("%s is a symlink to %s, whose directory does not exist — remove the link "+
		"(`rm %s`) or recreate %s, then apply again; nothing was written there",
		b.Link, b.Target, shquote.Quote(b.Link), filepath.Dir(b.Target))
}

// maxLinkHops bounds a chain walk, as the kernel's own 40 does; a longer chain (or a loop) is
// reported as broken at the destination's first link.
const maxLinkHops = 40

// FindBrokenLink reports the broken link on path's way to the filesystem, or nil when a write to
// path can complete without inventing a directory. See the file header for what counts.
//
// It walks UP from path until something exists (unresolvedLinkOnPath), because a missing path
// means either a plain missing file (created, parents included) or a missing parent whose own
// ancestor may be the link. The first existing entry decides: a regular file or directory means
// nothing on the way is a link to nowhere; a symlink that does not resolve is the candidate.
func FindBrokenLink(path string) *BrokenLink {
	p, serr, found := unresolvedLinkOnPath(path)
	if !found || !os.IsNotExist(serr) {
		// Nothing on the way is a link that does not resolve, or the one there fails for a
		// reason that is not absence, which the writer reports as itself: not this rule's case.
		return nil
	}
	end, ok := linkChainEnd(p)
	if !ok {
		return &BrokenLink{Link: p, Target: firstHop(p)}
	}
	if p == filepath.Clean(path) {
		// The destination itself: broken only when the chain's end has no directory.
		if dirExists(filepath.Dir(end)) {
			return nil
		}
	}
	// An ANCESTOR link that resolves to nothing is broken whatever its parent holds:
	// writing below it means creating the directory it names, which is the same
	// invented tree one level up.
	return &BrokenLink{Link: p, Target: end}
}

// unresolvedLinkOnPath is the WALK UP both predicates in this file share: from path toward the
// root until something exists (Lstat), because a missing path means either a plain missing file
// or a missing parent whose own ancestor may be the link. The first existing entry decides. found
// is true only when that entry is a symlink that os.Stat cannot follow, and serr is that Stat's
// error, so each caller rules on it: ENOENT, a loop (ELOOP), or something else such as EACCES.
// found is false when the first existing entry is not a link, when it resolves, when an Lstat on
// the way fails for a reason that is not absence, and when nothing on the way exists at all.
func unresolvedLinkOnPath(path string) (link string, serr error, found bool) {
	p := filepath.Clean(path)
	for {
		fi, err := os.Lstat(p)
		if err == nil {
			if fi.Mode()&os.ModeSymlink == 0 {
				return "", nil, false
			}
			if _, serr := os.Stat(p); serr != nil {
				return p, serr, true
			}
			return "", nil, false
		}
		if !os.IsNotExist(err) {
			return "", nil, false
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", nil, false
		}
		p = parent
	}
}

// FindDanglingLink reports the symlink that keeps a file yolo READS from being read: a link on
// path's way to the filesystem — path itself or one of its parent directories — whose chain ends
// at nothing, or loops. ok is false when path resolves, when it fails for a reason that is not a
// link to nowhere, and when nothing is there and no link explains it: a plain missing file, which
// is the user not having created it.
//
// It walks up from path with FindBrokenLink's own walk (unresolvedLinkOnPath), because a dotfiles
// manager that linked a whole directory leaves the file's own path absent and the link one or more
// levels up. Unlike FindBrokenLink it exempts nothing: the write-through case (a missing target in
// a directory that exists) is a write's, and a read of it fails all the same.
func FindDanglingLink(path string) (link, target string, ok bool) {
	p, serr, found := unresolvedLinkOnPath(path)
	if !found {
		return "", "", false
	}
	end, chained := linkChainEnd(p)
	switch {
	case !chained:
		// A loop, or a chain longer than the kernel follows: nothing at its end either.
		return p, firstHop(p), true
	case os.IsNotExist(serr):
		return p, end, true
	default:
		// Resolves to something that cannot be read for another reason (EACCES on the
		// way, say): the caller's to report as itself.
		return "", "", false
	}
}

// linkChainEnd follows the symlink chain starting at link to the first path that is not itself an
// existing symlink, resolving relative targets against each link's directory. ok is false for a
// loop or an over-long chain.
func linkChainEnd(link string) (string, bool) {
	cur := link
	for i := 0; i < maxLinkHops; i++ {
		fi, err := os.Lstat(cur)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return cur, true
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return cur, true
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = filepath.Clean(target)
	}
	return "", false
}

// firstHop is link's own raw target, resolved against its directory — what a loop is reported as.
func firstHop(link string) string {
	target, err := os.Readlink(link)
	if err != nil {
		return link
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	return filepath.Clean(target)
}

func dirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

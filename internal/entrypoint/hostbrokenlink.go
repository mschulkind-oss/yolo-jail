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
// THE THIRD SHAPE IS a symlink cycle. A jail writer repairs it only when a repeated link is
// proven within the jail-home boundary and the exact entry can be removed safely; uncertain and
// outside-home loops are refused untouched. The host notch still refuses rather than repairing.
//
// THE READ SIDE IS FindDanglingLink, in this file because it takes the same walk up the path
// (unresolvedLinkOnPath) and along the same chain (linkChainEnd), and a second walker is what
// docs/design/agent-directory-map.md AM-D5 rules out. A file yolo
// READS has no write-through exemption: a link to a missing file reads nothing whether or not the
// target's directory exists. UnreadHostSource builds the whole read-side question on it — a
// dangling link, a non-regular file, a read error — for both notches.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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

// maxLinkHops (linkhops_<goos>.go) bounds a chain walk, without confusing an exhausted finite
// chain with a cycle. It is the kernel's own symlink limit for one path resolution, so a chain
// the walk proves finite is one the write that follows can open: 40 on Linux, 32 on macOS.

type loopInspection struct {
	link         string
	target       string
	rawTarget    string
	info         os.FileInfo
	cycle        bool
	uncertain    bool
	inHome       bool
	recreateDirs []string
}

type seenSymlink struct {
	info   os.FileInfo
	target string
}

func inspectLoopingLink(home, path string) loopInspection {
	p, _, found, hadLoop, uncertain := firstLinkOnPath(path)
	if uncertain {
		return loopInspection{uncertain: true}
	}
	if !found {
		if hadLoop {
			return loopInspection{uncertain: true}
		}
		return loopInspection{}
	}
	result := linkChainWalk(p, home)
	if result.cycle {
		if hadLoop {
			result.recreateDirs = append(result.recreateDirs, filepath.Dir(filepath.Clean(path)))
		}
		return result
	}
	if result.uncertain || hadLoop {
		return loopInspection{uncertain: true}
	}
	return loopInspection{}
}

// firstLinkOnPath reports the nearest existing symlink on the way to path. ELOOP on a deeper
// component is remembered while walking upward so a caller can inspect the parent link rather
// than mistaking that error for a terminating target.
func firstLinkOnPath(path string) (link string, info os.FileInfo, found, hadLoop, uncertain bool) {
	p := filepath.Clean(path)
	for {
		fi, err := os.Lstat(p)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return p, fi, true, hadLoop, false
			}
			return "", nil, false, hadLoop, false
		}
		if errors.Is(err, syscall.ELOOP) {
			hadLoop = true
		} else if errors.Is(err, syscall.ENOTDIR) {
			return "", nil, false, hadLoop, false
		} else if !os.IsNotExist(err) {
			return "", nil, false, hadLoop, true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", nil, false, hadLoop, false
		}
		p = parent
	}
}

// linkChainEnd follows the symlink chain starting at link to the first path that is not itself an
// existing symlink, resolving relative targets against each link's directory. ok is false for a
// proven loop, an unreadable chain, or a chain longer than this bounded inspection permits.
func linkChainEnd(link string) (string, bool) {
	result := linkChainWalk(link, "")
	return result.link, !result.cycle && !result.uncertain && result.link != ""
}

// linkChainWalk is the one bounded walker for read and write-side symlink policy. It follows
// target components until a non-link entry, steps up only after Lstat reports ELOOP to find the
// actual parent link, and calls a cycle only when the same path still names the same symlink.
// home, when supplied, tracks whether any part of the chain leaves the repairable jail home.
func linkChainWalk(link, home string) loopInspection {
	seen := map[string]seenSymlink{}
	cur := filepath.Clean(link)
	steps, links := 0, 0
	inHome := home == ""
	if home != "" {
		inHome = pathUnderHome(home, cur)
	}
	var recreateDirs []string
	for steps < maxLinkHops*2+2 {
		steps++
		fi, err := os.Lstat(cur)
		if errors.Is(err, syscall.ELOOP) {
			parentLink, _, found, _, uncertain := firstLinkOnPath(cur)
			if uncertain || !found {
				return loopInspection{uncertain: true}
			}
			if len(recreateDirs) == 0 {
				recreateDirs = append(recreateDirs, filepath.Dir(cur))
			}
			cur = parentLink
			continue
		}
		if err != nil {
			if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
				return loopInspection{link: cur, inHome: inHome, recreateDirs: recreateDirs}
			}
			return loopInspection{uncertain: true}
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return loopInspection{link: cur, inHome: inHome, recreateDirs: recreateDirs}
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return loopInspection{uncertain: true}
		}
		after, err := os.Lstat(cur)
		if err != nil || !os.SameFile(fi, after) {
			return loopInspection{uncertain: true}
		}
		targetAgain, err := os.Readlink(cur)
		if err != nil || targetAgain != target {
			return loopInspection{uncertain: true}
		}
		if prior, ok := seen[cur]; ok {
			if os.SameFile(prior.info, fi) && prior.target == target {
				reportedTarget := target
				if !filepath.IsAbs(reportedTarget) {
					reportedTarget = filepath.Join(filepath.Dir(cur), reportedTarget)
				}
				return loopInspection{
					link:         cur,
					target:       filepath.Clean(reportedTarget),
					rawTarget:    target,
					info:         fi,
					cycle:        true,
					inHome:       inHome,
					recreateDirs: recreateDirs,
				}
			}
			return loopInspection{uncertain: true}
		}
		if links == maxLinkHops {
			return loopInspection{uncertain: true}
		}
		links++
		seen[cur] = seenSymlink{info: fi, target: target}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = filepath.Clean(target)
		if home != "" && !pathUnderHome(home, cur) {
			inHome = false
		}
	}
	return loopInspection{uncertain: true}
}

func pathUnderHome(root, path string) bool {
	rootAbs, rootErr := filepath.Abs(root)
	pathAbs, pathErr := filepath.Abs(path)
	if rootErr != nil || pathErr != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(pathAbs))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
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

// HostSourceSkip is why a host file yolo was declared to READ was not read: a pack's `reads-host`
// grant or `readsHost` surface, or a briefing's `after: "host:<path>"` file. Why is a clause ("it
// is a symlink to …, which does not exist"); Remedy is a sentence, "" when no one remedy fits.
type HostSourceSkip struct {
	Why    string
	Remedy string
}

// UnreadHostSource says why the host file at src (an absolute path) cannot be read, or nil when
// there is nothing to say: it is a regular file, or nothing is there and no link explains its
// absence — the user has not created it, the normal state.
//
// IT NEVER OPENS src, and that is the point of asking before reading. A dangling link reads as
// absent (ENOENT), so the read cannot tell it from a file the user never made, and a FIFO would
// block the read until something writes into it. Both notches ask this first: the jail launch
// (internal/cli/run's unreadHostSource, which prints the launch line) and the host briefing
// composition (hostBriefingOverlay). One predicate, so the two say the same thing about one file.
func UnreadHostSource(src string) *HostSourceSkip {
	if link, target, ok := FindDanglingLink(src); ok {
		subject := link + " is"
		if link == src {
			subject = "it is"
		}
		return &HostSourceSkip{
			Why:    subject + " a symlink to " + target + ", which does not exist",
			Remedy: "Restore the target, or remove the link",
		}
	}
	info, err := os.Stat(src)
	switch {
	case err == nil && info.Mode().IsRegular():
		return nil
	case err == nil:
		return &HostSourceSkip{Why: "it is not a regular file"}
	case os.IsNotExist(err):
		return nil
	default:
		return &HostSourceSkip{Why: "it could not be read (" + err.Error() + ")"}
	}
}

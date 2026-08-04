package hostskills

// dangling.go answers one question the ownership rules could not: is this destination name
// USER CONTENT, or a pointer to content that is gone?
//
// The ownership rule everything else here rests on — never touch what yolo cannot prove it
// wrote — is correct and unchanged. What it lacked was a way to tell a hand-written skill from
// a symlink whose target no longer exists, and by any reading a link to a missing file is not
// content. Any dotfile manager that deploys by symlinking into $HOME (rcm, stow, chezmoi)
// produces a home full of exactly that the moment the source files MOVE, which is the first
// thing a user does when adopting a pack: `git mv claude/skills packs/mine/skills` leaves the
// deployed links behind, pointing at a path that is no longer there.
//
// The consequence was worse than the "left untouched" report suggested, and worse in two ways
// the report only saw one of:
//
//   - at a LEAF skill name, the link read as the user's own skill, so the pack was reported
//     skipped and stayed permanently inert;
//   - at a DIRECTORY yolo has to create (the skills dir itself, or a pack's subtree under it),
//     MkdirAll refuses a name it can neither use nor create — "file exists" — which aborted
//     the whole delivery with a raw syscall error and NO per-entry report at all.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// clearedLink is one dangling symlink and what it pointed at. The target is not decoration: it
// is the one fact that tells the user WHICH stale deployment this was, and it is what they need
// to re-create the link if removing it was not what they wanted.
type clearedLink struct {
	Path   string
	Target string
}

// danglingLink returns the target of a DANGLING symlink at path, and "" for everything else.
//
// Both syscalls are load-bearing. Lstat establishes that the name is a symlink rather than a
// real entry, because a real directory or file must never be touched by this. Stat establishes
// that following it lands nowhere — and the rest of this package Stats deliberately so that a
// symlink to a REAL directory counts as a directory. That stays true here: a link whose target
// exists is the user's content reached by another route, and is still refused.
//
// ONLY ENOENT counts as dangling. A symlink LOOP reports ELOOP, and a loop is not proof of
// absence: verified 2026-08-04 that a chain of 60 links each pointing at a real file also
// reports "too many levels of symbolic links", so that error says the kernel stopped walking,
// not that there is nothing at the end. ENOTDIR (a link through a regular file) and a
// permission error are the same kind of not-proof. All of them fall through to the ownership
// rule and are treated as the user's, which costs an inert pack rather than someone's work.
func danglingLink(path string) string {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "" // unreadable is not provably absent
	}
	return target
}

// danglingInChain returns the dangling symlinks standing where a directory has to be, for every
// path component from root down to dir inclusive, outermost first.
//
// Clearing is CONFINED to root and its descendants, and root is always the destination skills
// dir. That bound is the point: a broken link further up (a home directory that is itself a
// stale link) is not something a skills delivery may quietly unlink, so it keeps failing loudly.
// Within the bound the rule is exactly "a name MkdirAll would have created had it been free",
// since a dangling link is neither a usable directory nor a name that can be created.
//
// Computed with no writes, so both postures see the identical set and the dry run cannot
// disagree with the real one.
func danglingInChain(dir, root string) []clearedLink {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	chain := []string{root}
	if rel != "." {
		cur := root
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, seg)
			chain = append(chain, cur)
		}
	}
	var out []clearedLink
	for _, p := range chain {
		if target := danglingLink(p); target != "" {
			out = append(out, clearedLink{Path: p, Target: target})
		}
	}
	return out
}

// clearLinks unlinks the given dangling symlinks.
//
// os.Remove, not os.RemoveAll: both stop at a symlink rather than following it, but Remove is
// the exact statement of intent and would fail rather than recurse if the path turned out to be
// a populated directory after all. Nothing is archived — a broken link holds no content, so an
// archive of it would be a file the user finds and cannot use. The report names the target
// instead, which is what makes the removal reversible with one `ln -s`.
func clearLinks(links []clearedLink) error {
	for _, l := range links {
		if err := os.Remove(l.Path); err != nil {
			return err
		}
	}
	return nil
}

// clearDanglingDirs clears every dangling symlink standing where a directory of the delivery
// has to be, from skillsDir down to dir, and returns one Result per link so the removal is never
// silent. Both postures compute the same set; only the unlink is gated.
//
// This is the half of F2 the field report did not see, because it never got a report to read:
// at a leaf skill name a dangling link merely looked like the user's own skill, but at a
// DIRECTORY it aborted the whole delivery with a bare `mkdir …: file exists` — MkdirAll cannot
// use the name and cannot create it either. Same cause, no line of output at all.
func clearDanglingDirs(skillsDir, dir string, observe bool) []Result {
	links := danglingInChain(dir, skillsDir)
	if len(links) == 0 {
		return nil
	}
	out := make([]Result, 0, len(links))
	for _, l := range links {
		r := Result{
			// The base name alone would print "skills" for a link at the skills dir, which
			// says nothing about WHICH dir; the detail carries the full path.
			Name: filepath.Base(l.Path), Path: l.Path, Action: clearedAction(observe),
			Detail: l.Path + " was → " + l.Target + ", which no longer exists — nothing to " +
				"archive, clearing it so the directory can be created",
		}
		if !observe {
			if err := clearLinks([]clearedLink{l}); err != nil {
				r.Action, r.Detail = ActionRefused, err.Error()
			}
		}
		out = append(out, r)
	}
	return out
}

// danglingAncestorNote explains a directory-creation failure caused by a dangling symlink ABOVE
// the skills dir — `~/.claude` itself deployed as a stale link, say.
//
// Those are deliberately NOT cleared (clearDanglingDirs stops at the skills dir): unlinking a
// whole agent's home directory is not a decision a skills delivery gets to make silently. But
// the resulting `mkdir: file exists` names neither the link nor its target, so the user has no
// way to tell this apart from a permission problem. Naming it costs one Lstat on the error path.
func danglingAncestorNote(dir string) string {
	for p := filepath.Dir(dir); ; p = filepath.Dir(p) {
		if target := danglingLink(p); target != "" {
			return p + " is a dangling symlink → " + target + " (which no longer exists); " +
				"remove or repair it — yolo will not unlink a directory above the skills dir"
		}
		if parent := filepath.Dir(p); parent == p {
			return ""
		}
	}
}

// mkdirError annotates a directory-creation failure with the dangling ancestor that caused it,
// when there is one. The raw error is kept and wrapped rather than replaced, because it is the
// right message for every other cause (a permission problem, a read-only filesystem).
func mkdirError(dir string, err error) error {
	if note := danglingAncestorNote(dir); note != "" {
		return fmt.Errorf("%w — %s", err, note)
	}
	return err
}

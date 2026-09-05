package hostskills

// delivered.go defines THE DELIVERED FORM — what a delivery actually leaves at the destination —
// and hashes it, so the predicate that decides whether to write can see its own output.
//
// THIS IS A DIFFERENT QUESTION FROM internal/treedigest'S, and the split is the whole point.
// treedigest answers "are these two trees the same TREE?", an identity question in which a
// symlink's target and a file's exact permission bits are part of what the tree IS. That is
// right for the installer-capture store, whose digest is a KEY: a captured install full of
// absolute self-references must not be hashed by whatever those references point at today, and
// one that lost 0o700 for 0o755 is not the same install. It is right for this package's
// MIGRATION union too (compose.go's plannedLocalPack), which compares two of the USER'S trees
// and never materializes either.
//
// A DELIVERY predicate cannot use it. `Changed` does not ask "is the source the same tree as the
// destination?" — it asks "would copying the source over the destination ALTER it?", and the
// answer is a fact about what the COPY PRODUCES:
//
//   - the copy MATERIALIZES a symlink (copyTreeExcept reads through it; the `files` kind's
//     os.ReadFile does the same), because the destination is a real agent home whose tools must
//     be able to READ the skill. A source deployed by a dotfile manager — rcm, stow, chezmoi:
//     a tree of links into a dotfiles repo — therefore lands as CONTENT, and a predicate that
//     measured it by its link TARGETS could never match. It reported a change it had already
//     made, on every apply, forever;
//   - the copy NORMALIZES the mode, keeping only the exec BIT (0o755/0o644 here, 0o555/0o444
//     for the `files` kind's read-only mirror of the jail's `:ro` bind). A 0o700 source — what
//     `git clone` leaves under `umask 077` — could never match its own 0o755 output.
//
// Both are the same defect: the predicate read a property the delivery does not preserve. So
// the form below records exactly what survives a delivery and nothing else.
//
// THE CANONICAL FORM, exactly — one line per entry, walked depth-first with each directory's
// children sorted by name, relative to the root (whose own line is `d .`):
//
//	d <rel>                  a directory
//	l <rel> <target>         a link that will NOT be materialized: see resolve below
//	f <rel> <x|->            a file header carrying only the exec bit, then its raw bytes
//
// The digest is hex(sha256(that stream)). The alphabet differs from treedigest's on purpose:
// the two are not interchangeable and a stray digest must not silently compare.
//
// BOTH SIDES OF A COMPARISON COME FROM THIS ONE FUNCTION. Reading one side with treedigest and
// the other with this would be two spellings of one form, which is the drift that put the bug
// here in the first place — they would have to keep agreeing about every entry kind forever, and
// nothing would fail when they stopped.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// deliveredDigest hashes root in the form a delivery would leave it.
//
// resolve says which side of the comparison this is, and the asymmetry is deliberate:
//
//   - the SOURCE is digested with resolve=true — as it WOULD BE delivered, links read through;
//   - the DESTINATION with resolve=false — as it IS, links recorded by readlink.
//
// So a destination that is itself a link compares unequal to the content that would replace it,
// which is the honest answer (delivering really would replace it) and converges after one apply
// rather than never.
//
// skip omits a set of RELATIVE paths and their subtrees entirely — not just their content, but
// their names — matching copyTreeExcept, which returns before it so much as creates an excluded
// directory. See changedExcept for the one caller that passes a set and why omitting content is
// honest there.
//
// A link that cannot be resolved is recorded as a link even under resolve=true, so a dangling
// entry never fails the walk. The delivery of such a source refuses at the copy and says why;
// making the digest fail too would only move the same report somewhere less specific.
func deliveredDigest(root string, skip map[string]bool, resolve bool) (string, error) {
	h := sha256.New()
	walked := followedDirs{}
	var walk func(rel string) error
	walk = func(rel string) error {
		if skip[rel] {
			return nil
		}
		path := filepath.Join(root, rel)
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		linked := fi.Mode()&os.ModeSymlink != 0
		if linked {
			resolved, serr := os.Stat(path)
			if !resolve || serr != nil {
				target, rerr := os.Readlink(path)
				if rerr != nil {
					return rerr
				}
				fmt.Fprintf(h, "l %s %s\n", rel, target)
				return nil
			}
			fi = resolved
		}
		if fi.IsDir() {
			if linked {
				leave, cerr := walked.enter(path)
				if cerr != nil {
					return cerr
				}
				defer leave()
			}
			entries, rerr := os.ReadDir(path)
			if rerr != nil {
				return rerr
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names) // ReadDir already sorts; stated so the digest cannot drift with it
			fmt.Fprintf(h, "d %s\n", rel)
			for _, name := range names {
				if werr := walk(filepath.Join(rel, name)); werr != nil {
					return werr
				}
			}
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer f.Close()
		fmt.Fprintf(h, "f %s %s\n", rel, execFlag(fi))
		_, cerr := io.Copy(h, f)
		return cerr
	}
	if err := walk("."); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// execFlag is the only part of a file's mode a delivery preserves: whether it runs.
//
// Neither writer can reproduce anything finer. copyTreeExcept writes 0o755 or 0o644 and the
// `files` kind writes 0o555 or 0o444, so a digest that recorded the source's exact bits would be
// comparing against a value the destination is structurally incapable of holding.
func execFlag(fi os.FileInfo) string {
	if fi.Mode().Perm()&0o111 != 0 {
		return "x"
	}
	return "-"
}

// followedDirs is the cycle guard for a walk that FOLLOWS directory symlinks — which both the
// copy and the digest above now do, and which a never-following walk never needed.
//
// A real directory cannot contain itself, so only a followed link can produce a descent with no
// bottom: `skills/self -> ..` yields skills/self/loopy/self/loopy/… forever. The kernel does stop
// it — its 40-traversal limit per path resolution — but only after the damage, and MEASURED
// without this guard (2026-09-05, copyTree over that source): 40 nested directories created at
// the destination, and an ELOOP naming a 700-character path instead of the cycle. The guard holds
// the RESOLVED path of every directory entered through a link on the current descent and refuses
// a second entry into one, which turns that into nothing written and one legible sentence.
type followedDirs map[string]bool

// enter records path's resolved identity and returns the function that releases it. The release
// is deferred by the caller, so the set holds the current descent rather than every directory
// ever seen: two SIBLING links to one directory are not a cycle and must both be walked.
func (f followedDirs) enter(path string) (func(), error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if f[resolved] {
		return nil, fmt.Errorf("symlink cycle: %s resolves to %s, which is already being walked",
			path, resolved)
	}
	f[resolved] = true
	return func() { delete(f, resolved) }, nil
}

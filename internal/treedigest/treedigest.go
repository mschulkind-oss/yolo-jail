// Package treedigest answers "are these two directory trees the same?" by CONTENT, in one
// canonical form that more than one subsystem needs to agree on.
//
// It was `hostskills.treeDigest` until 2026-09-04, where it answered "is this the same
// SKILL?" — and it moved here unchanged because the installer-capture store
// (internal/capture, program-delivery.md §6.3) asks the same question of a captured
// install tree and must get the same answer from the same bytes. Two spellings of one
// canonical form is the drift that would make a capture key and a skill digest disagree
// about what "identical" means; hostskills' own tests are the gate that this lift changed
// nothing (they call the package-local wrappers, which now delegate here).
//
// THE CANONICAL FORM, exactly — one line per entry, walked depth-first with each
// directory's children sorted by name, relative to the root (whose own line is `d .`):
//
//	d <rel>                  a directory
//	l <rel> <target>         a symlink, by READLINK — never followed
//	f <rel> <exec-octal>     a file header, immediately followed by its raw bytes
//
// The digest is hex(sha256(that stream)). Nothing else is in it: not mtimes, not owners,
// not the non-exec permission bits, not the root's own path.
package treedigest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Of hashes a whole subtree — relative paths, entry kinds, file bytes and SYMLINK
// TARGETS — so "is this the same tree?" is answered by content rather than by name.
//
// Symlink targets are part of the identity rather than followed, because a dotfile-manager
// deployment is a tree of links: two agents' copies of one skill that link to the SAME source are
// the same skill, and two that link to different sources are not. Following them would make every
// such pair compare equal to whatever they happen to point at today. The capture store inherits
// the same rule for a second reason: an installer's tree contains absolute self-references, and a
// digest that followed them would hash whatever else is installed on the machine.
func Of(root string) (string, error) { return OfSkipping(root, nil) }

// OfSkipping is Of omitting a set of RELATIVE paths (and their subtrees)
// entirely — not just their content, but their names, matching hostskills' copyTreeExcept,
// which returns before it so much as creates an excluded directory. See hostskills'
// changedExcept for the one caller that passes a set and why omitting content is honest there.
func OfSkipping(root string, skip map[string]bool) (string, error) {
	h := sha256.New()
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
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, rerr := os.Readlink(path)
			if rerr != nil {
				return rerr
			}
			fmt.Fprintf(h, "l %s %s\n", rel, target)
			return nil
		case fi.IsDir():
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
		// The exec bit is part of the identity: a skill that ships a script the user made
		// executable is not the same skill as one that did not — and a captured install
		// whose binary lost its exec bit is not the same install.
		fmt.Fprintf(h, "f %s %o\n", rel, fi.Mode().Perm()&0o111)
		_, cerr := io.Copy(h, f)
		return cerr
	}
	if err := walk("."); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

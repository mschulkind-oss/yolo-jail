package capture

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// confine.go is what Materialize checks before it writes, so that a manifest can put files only
// INSIDE the home it is materialized into.
//
// # Why a manifest has to be checked at all
//
// The manifest is written by the capture driver inside the capture jail (inner.go): it is the
// jail's own account of what its installer left, and nothing on the way into the store
// re-derives it from the tree. Materialize joins each entry's Path onto the destination home and
// creates what the entry names, walking through whatever the home holds at each parent — which
// is how it has to work, because the macos-user account home reaches `.local` through a link it
// must follow (dirExists). Taken as given, an entry spelled with `..`, or one listed beneath a
// symlink entry the same run creates, is written outside the home. Inside a container that is a
// write the jail could make anyway; the host agent floor materializes on the HOST
// (internal/hostfloor, host-tool-provisioning.md OQ-HP3), where it is a write with the user's
// full authority.
//
// # Two levels
//
//   - EVERY materialize refuses, before its first write, a manifest whose entries are not a tree:
//     a path that is empty, absolute, not already clean, or climbs with `..`; a path listed twice;
//     a kind it does not know; and a path beneath an entry that is not a directory. A file entry
//     whose source in the store is not a regular file is refused as it is reached. None of these
//     can come from describeTree, which walks a real tree without following links, so no capture
//     that was made honestly is affected.
//   - A CONFINED materialize (MaterializeOptions.Confined, the host agent floor's) also requires
//     an absent or empty home, keeps every entry inside the capture surfaces, and follows no
//     symlink beneath the home while it writes: each directory on the way to an entry is checked
//     with Lstat first. That last rule is the one the entry checks cannot make on their own — two
//     names that differ only in case are one directory on a case-insensitive filesystem.

// checkEntries is the every-materialize half: the manifest's entries form a tree under the home.
func checkEntries(entries []ManifestEntry) error {
	kinds := make(map[string]string, len(entries))
	for _, e := range entries {
		if err := checkEntryPath(e.Path); err != nil {
			return err
		}
		switch e.Kind {
		case KindDir, KindFile, KindSymlink:
		default:
			return fmt.Errorf("entry %q has kind %q, which is none of %s, %s and %s",
				e.Path, e.Kind, KindDir, KindFile, KindSymlink)
		}
		if _, dup := kinds[e.Path]; dup {
			return fmt.Errorf("entry %q is listed twice", e.Path)
		}
		kinds[e.Path] = e.Kind
	}
	for _, e := range entries {
		for dir := path.Dir(e.Path); dir != "."; dir = path.Dir(dir) {
			if k, ok := kinds[dir]; ok && k != KindDir {
				return fmt.Errorf("entry %q lies beneath %q, which the manifest records as a %s: "+
					"placing it would write through that %s", e.Path, dir, k, k)
			}
		}
	}
	return nil
}

// checkEntryPath refuses a Path that is not a clean, relative, slash-separated path inside the
// home.
func checkEntryPath(p string) error {
	switch {
	case p == "" || p == ".":
		return errors.New("an entry names the home itself, or nothing")
	case strings.ContainsRune(p, 0):
		return fmt.Errorf("entry %q contains a NUL byte", p)
	case path.IsAbs(p) || filepath.IsAbs(p):
		return fmt.Errorf("entry %q is absolute; every entry is relative to the home", p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "..":
			return fmt.Errorf("entry %q climbs out of the home", p)
		case "", ".":
			return fmt.Errorf("entry %q is not a clean path (it is %q once cleaned)", p, path.Clean(p))
		}
	}
	return nil
}

// checkConfinedManifest is the confined half's up-front part: the home is absent or an empty
// directory (so nothing in it was put there by anyone but this materialize), and every entry is
// inside a capture surface, or is a directory on the way to one.
func checkConfinedManifest(m *Manifest, home string) error {
	fi, err := os.Lstat(home)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("the home %s is not a directory", home)
	default:
		ents, err := os.ReadDir(home)
		if err != nil {
			return err
		}
		if len(ents) > 0 {
			return fmt.Errorf("the home %s is not empty; a confined materialize writes only into a "+
				"directory made for it", home)
		}
	}
	var surfaces []string
	for _, s := range paths.InstalledProgramSurfaces() {
		surfaces = append(surfaces, filepath.ToSlash(s.HomeRel))
	}
	for _, e := range m.Entries {
		if !inSurfaces(e, surfaces) {
			return fmt.Errorf("entry %q is outside the capture surfaces (%s)", e.Path,
				strings.Join(surfaces, ", "))
		}
	}
	return nil
}

// inSurfaces reports whether e is a surface, is beneath one, or is a directory a surface is
// beneath (`.codex` and `.codex/packages` on the way to `.codex/packages/standalone`).
func inSurfaces(e ManifestEntry, surfaces []string) bool {
	for _, s := range surfaces {
		if e.Path == s || strings.HasPrefix(e.Path, s+"/") ||
			(e.Kind == KindDir && strings.HasPrefix(s, e.Path+"/")) {
			return true
		}
	}
	return false
}

// confinedParents checks, with Lstat, every directory between home and the entry at rel —
// the entry itself too when self is set — and refuses a symlink or a non-directory among them.
// A component that does not exist yet ends the check: everything from it down is created by
// this materialize, as a real directory.
func confinedParents(home, rel string, self bool) error {
	parts := strings.Split(rel, "/")
	if !self {
		parts = parts[:len(parts)-1]
	}
	cur := home
	for _, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, and a confined materialize writes through none", cur)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory", cur)
		}
	}
	return nil
}

// regularSource refuses a file entry whose source in the store is not a regular file: a link
// there would be read through by the copy arms, and hardlinked as a LINK by link(2), which makes
// the home hold a symlink the manifest never declared.
func regularSource(src string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("the manifest records a file and the store holds a %s at %s",
			fileKind(fi.Mode()), src)
	}
	return nil
}

func fileKind(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "symlink"
	case m.IsDir():
		return "directory"
	}
	return "special file"
}

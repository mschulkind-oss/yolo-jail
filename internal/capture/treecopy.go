package capture

// treecopy.go puts ONE SUBTREE of an admitted entry at a directory of its own: a PATCHED
// EXTENSION's built tree (docs/design/patched-extensions.md §8, PPX-D7), which a fresh jail launch
// copies beside its pack tree and mounts read-only, and the Linux host copies into a versioned
// directory it links `~/<into>` to.
//
// REFLINK, ELSE COPY, NEVER A HARDLINK. Materialize tries a hardlink second (materialize.go), and
// here that arm would succeed: the copy and the store share a filesystem, and on the host a mount
// too. A hardlinked file is the store's inode, so a jail that can write its mount (Apple Container
// below its read-only floor ignores `:ro`) could change the store, and every other copy, through
// it. A reflinked or copied file is its own inode. So the chain here has two arms.
//
// DRIVEN BY THE MANIFEST, as Materialize is, so the copy gets the modes the build left rather than
// the store's frozen ones, and only the entries under the prefix are placed, with the prefix cut.
// Links are placed as links, verbatim: a tree's admit refused any link into its build's home or
// workspace (cli's treeAdmitProblem), so what is left is relative, or absolute outside both.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// CopyTreeOptions configures CopyTree.
type CopyTreeOptions struct {
	// Entry is the resolved store entry.
	Entry *Entry
	// Prefix is the home-relative, slash-separated directory of the entry's tree to copy: its
	// contents land directly in Dest.
	Prefix string
	// Dest is the directory to create. It must not exist: a copy is always made whole, into a
	// directory of its own, never over another.
	Dest string
	// Stderr receives the copy report, as Materialize's (reportCopy); nil discards it.
	Stderr io.Writer
}

// CopyTree copies the entry's subtree at Prefix into Dest, by reflink or by copy and never by
// hardlink. A failure leaves whatever it wrote, for the caller to remove: a copy is made into a
// directory nothing else reads until the caller hands it on.
func CopyTree(opts CopyTreeOptions) (*MaterializeResult, error) {
	if opts.Entry == nil {
		return nil, errors.New("capture copy: no entry")
	}
	if !filepath.IsAbs(opts.Dest) {
		return nil, fmt.Errorf("capture copy: destination %q must be an absolute path", opts.Dest)
	}
	prefix := strings.Trim(path.Clean("/"+opts.Prefix), "/")
	if prefix == "" {
		return nil, errors.New("capture copy: no prefix")
	}
	if _, err := os.Lstat(opts.Dest); err == nil {
		return nil, fmt.Errorf("capture copy: %s already exists", opts.Dest)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	m, err := ReadManifest(opts.Entry.Root)
	if err != nil {
		return nil, fmt.Errorf("capture copy: %w", err)
	}
	if err := checkEntries(m.Entries); err != nil {
		return nil, fmt.Errorf("capture copy: entry %s has a manifest no capture writes: %w", opts.Entry.Key, err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.Dest), 0o755); err != nil {
		return nil, err
	}
	res := &MaterializeResult{}
	found := false
	ch := &chain{reflink: true, link: false, linkWhy: "a tree is never hardlinked out of the store"}
	for _, e := range m.Entries {
		var rel string
		switch {
		case e.Path == prefix:
			if e.Kind != KindDir {
				return res, fmt.Errorf("capture copy: %s in entry %s is a %s, not a directory", prefix,
					opts.Entry.Key, e.Kind)
			}
			found = true
		case strings.HasPrefix(e.Path, prefix+"/"):
			rel = strings.TrimPrefix(e.Path, prefix+"/")
		default:
			continue
		}
		src := filepath.Join(opts.Entry.Tree, filepath.FromSlash(e.Path))
		dst := opts.Dest
		if rel != "" {
			dst = filepath.Join(opts.Dest, filepath.FromSlash(rel))
		}
		if err := placeTreeEntry(src, dst, e, ch, res); err != nil {
			return res, fmt.Errorf("capture copy %s: %w", e.Path, err)
		}
	}
	if !found {
		return res, fmt.Errorf("capture copy: entry %s holds no directory %s", opts.Entry.Key, prefix)
	}
	res.ReflinkRetired, res.LinkRetired = ch.reflinkWhy, ch.linkWhy
	if res.Copied > 0 {
		res.SourceFS, res.DestFS = fsName(opts.Entry.Tree), fsName(opts.Dest)
		reportCopy(opts.Stderr, opts.Entry, opts.Dest, res)
	}
	return res, nil
}

// placeTreeEntry realizes one manifest entry at dst, which nothing occupies yet.
func placeTreeEntry(src, dst string, e ManifestEntry, ch *chain, res *MaterializeResult) error {
	switch e.Kind {
	case KindDir:
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		// A directory keeps its owner's write bit whatever the manifest says, so the caller can
		// remove the copy again: the read-only mount, not the mode, is what keeps a jail out of it.
		if err := os.Chmod(dst, permOf(e, src, 0o755)|0o700); err != nil {
			return err
		}
		res.Dirs++
		return nil
	case KindSymlink:
		if err := os.Symlink(e.Target, dst); err != nil {
			return err
		}
		res.Symlinks++
		return nil
	default:
		if err := regularSource(src); err != nil {
			return err
		}
		if err := ch.placeFile(src, dst, permOf(e, src, 0o644)); err != nil {
			return err
		}
		res.Files++
		res.Bytes += e.Size
		if ch.last == mechReflink {
			res.Reflinked++
		} else {
			res.Copied++
		}
		return nil
	}
}

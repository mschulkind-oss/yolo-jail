// rewrite.go is the REWRITE half of relocation (docs/plans/install-capture.md slice 6,
// hand-off H2): materializing an entry into a home OTHER than the one it was captured under, by
// rewriting every absolute reference the record found from the capture home to the destination.
//
// relocate.go is the half that FINDS the references and decides whether the entry may move at
// all. This file is the half that CONSUMES them, and it is the third clause of the contract
// Manifest.Relocatable states:
//
//	a materialize whose destination home EQUALS Manifest.Home ignores the field; a materialize
//	into any OTHER home must REFUSE when it is false, and must rewrite every AbsoluteRefs entry
//	from Home to the destination when it is true.
//
// # The two rewrites
//
// A SYMLINK reference is rewritten by creating the link with the rewritten target in place of
// the recorded one. The link is never created and then edited: materialize makes it once, with
// the right target.
//
// A FILE-CONTENT reference is rewritten by streaming the store's bytes through a substitution of
// the capture home for the destination home, into a NEW file beside the destination, which is
// then renamed into place. Three consequences, each deliberate:
//
//   - It never writes through a placed file. On the hardlink arm a placed file IS the store's
//     inode, so an in-place edit would rewrite the entry every other workspace materializes from.
//     A rewritten file is its own inode, always.
//   - It is not the reflink/hardlink/copy chain. A rewritten file holds different bytes from the
//     store's, so no extent-sharing primitive can place it; it costs its own size. Those files are
//     launcher shims and scripts, so the cost is small, and MaterializeResult.Rewritten counts them
//     apart from the chain's three arms so the copy report stays about the chain.
//   - A partial write never lands at the destination path. The bytes go to a temp file in the
//     destination's own directory, so the rename is on one filesystem and replaces the old file
//     in one step. A write that fails leaves whatever was at the path before, and the temp file is
//     removed.
//
// # Everything that can refuse, refuses BEFORE the home is touched
//
// planRelocation checks the whole manifest before the first file is placed: the verdict, the
// scan it rests on, that every reference names an entry of the right kind with the value the
// record would have written, that every file carrying a reference is still text, and that no
// absolute link into the capture home is missing from the list. A refusal is therefore the same
// outcome the pre-rewrite refusal was: nothing in the home changed, and the launcher falls
// through to the vendor installer. The only failures left after that are I/O on the destination
// (a full disk, a permission), and materialize is not transactional about those
// (materialize.go, "Not transactional, and it must not be").
//
// # What is MEASURED here and what is not
//
// MEASURED, by the tests beside this file and by internal/cli's capture-materialize tests, on
// Linux: a capture recorded by the real driver under one temp home and materialized under
// another, with both kinds of reference rewritten and the program run from the new home. The
// macos-user homes those temp dirs stand in for, /Users/Shared/yolo-captures/<bin>/home and
// /Users/_yolojail, have NOT run this: no Mac has. And no macos-user LAUNCH reaches this code
// yet, because nothing on that backend tells a launcher where the store is
// (install-capture.md hand-off H4).
package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// relocation is a checked plan for materializing one entry into a home it was not captured
// under. Built by planRelocation before anything is written; consumed by placeEntry.
type relocation struct {
	// from and to are the cleaned capture home (Manifest.Home) and destination home.
	from, to string
	// links maps a symlink entry's manifest path to the target the link is created with.
	links map[string]string
	// content is the set of file entries whose bytes carry `from` and are substituted on the
	// way into the home.
	content map[string]bool
}

// planRelocation checks a manifest against the contract and returns the rewrite plan, or the
// reasons the entry cannot be moved.
//
// reasons non-empty means REFUSE: the entry may not be materialized into `to`, and the caller
// wraps ErrNotRelocatable. err is an I/O failure reading the store itself, which is a broken
// store rather than a relocation verdict.
//
// All reasons are collected rather than stopping at the first, for the reason
// Manifest.NotRelocatable carries a list: the refusal is printed on a machine that did not make
// the capture, and naming every obstacle at once saves a human one capture per obstacle.
func planRelocation(m *Manifest, tree, to string) (*relocation, []string, error) {
	from := filepath.Clean(m.Home)
	to = filepath.Clean(to)
	// CLAUSE TWO. The record says no, and says why.
	if !m.Relocatable {
		why := "the capture is not relocatable"
		if len(m.NotRelocatable) > 0 {
			why += ": " + strings.Join(m.NotRelocatable, "; ")
		}
		return nil, []string{why}, nil
	}
	var reasons []string
	// relocatable:true is licensed ONLY by the full scan (decideRelocatable). A manifest that
	// claims it over a symlink-only scan disagrees with itself, and the rewrite cannot know
	// which half is wrong, so it believes the half that says less.
	if m.RefScan != RefScanFull {
		reasons = append(reasons, fmt.Sprintf("the manifest says relocatable but its reference "+
			"scan is %s, and only %s licenses a move", scanLabel(m.RefScan), RefScanFull))
	}
	if !filepath.IsAbs(from) || from == string(filepath.Separator) {
		reasons = append(reasons, fmt.Sprintf("the capture home %q is not a prefix a rewrite "+
			"can substitute", m.Home))
	}
	if len(reasons) > 0 {
		return nil, reasons, nil
	}

	byPath := make(map[string]ManifestEntry, len(m.Entries))
	for _, e := range m.Entries {
		byPath[e.Path] = e
	}
	rel := &relocation{from: from, to: to, links: map[string]string{}, content: map[string]bool{}}
	for _, r := range m.AbsoluteRefs {
		e, ok := byPath[r.Path]
		if !ok {
			reasons = append(reasons, fmt.Sprintf("a %s reference names %s, which is not in the "+
				"capture", r.Kind, r.Path))
			continue
		}
		switch r.Kind {
		case RefSymlinkTarget:
			// The record copies the target VERBATIM (describeTree), so a value that differs
			// from the entry's own target means the reference and the link it describes
			// are not the same fact any more. Rewriting either one would be a guess.
			switch {
			case e.Kind != KindSymlink:
				reasons = append(reasons, fmt.Sprintf("%s is recorded as a symlink reference "+
					"but the capture holds a %s there", r.Path, e.Kind))
			case r.Value != e.Target:
				reasons = append(reasons, fmt.Sprintf("%s's reference %q is not the link's "+
					"recorded target %q", r.Path, r.Value, e.Target))
			case !underPrefix(from, r.Value):
				reasons = append(reasons, fmt.Sprintf("%s's target %q is not under the capture "+
					"home %s, so there is no prefix to rewrite", r.Path, r.Value, from))
			default:
				rel.links[r.Path] = rewriteTarget(r.Value, from, to)
			}
		case RefFileContent:
			// Value is the PREFIX found in the bytes (scanContentRefs records the home
			// itself). Anything else is a prefix the contract never said what to rewrite
			// TO: the destination is a replacement for Manifest.Home, not for a string
			// somewhere inside it.
			switch {
			case e.Kind != KindFile:
				reasons = append(reasons, fmt.Sprintf("%s is recorded as a file-content "+
					"reference but the capture holds a %s there", r.Path, e.Kind))
			case filepath.Clean(r.Value) != from:
				reasons = append(reasons, fmt.Sprintf("%s's recorded prefix %q is not the "+
					"capture home %s", r.Path, r.Value, from))
			default:
				// THE BYTES ARE THE FACT, the manifest is the claim. The recording half
				// already refuses a reference inside a non-text file; this checks the
				// same heuristic against the tree actually being materialized, because
				// a substitution into a binary is the one rewrite that corrupts a program
				// rather than failing to fix it.
				binary, err := sniffBinary(filepath.Join(tree, filepath.FromSlash(r.Path)))
				if err != nil {
					return nil, nil, fmt.Errorf("reading %s to rewrite it: %w", r.Path, err)
				}
				if binary {
					reasons = append(reasons, fmt.Sprintf("%s is not text and embeds %s — a "+
						"prefix substitution in a binary is not a string edit", r.Path, from))
					continue
				}
				rel.content[r.Path] = true
			}
		default:
			// A reference kind a newer yolo records and this one cannot rewrite. Skipping
			// it would materialize exactly the dead path the record exists to prevent.
			reasons = append(reasons, fmt.Sprintf("%s carries a %q reference, which this yolo "+
				"does not know how to rewrite", r.Path, r.Kind))
		}
	}
	// THE LIST MUST BE COMPLETE, and for symlinks that is checkable from the manifest alone. An
	// absolute link into the capture home with no reference would be created verbatim, pointing
	// at a staging directory that is deleted when the capture ends.
	for _, e := range m.Entries {
		if e.Kind != KindSymlink || !underPrefix(from, e.Target) {
			continue
		}
		if _, ok := rel.links[e.Path]; !ok && !hasRef(m, e.Path, RefSymlinkTarget) {
			reasons = append(reasons, fmt.Sprintf("%s links to %s inside the capture home and "+
				"the record lists no reference for it", e.Path, e.Target))
		}
	}
	if len(reasons) > 0 {
		return nil, reasons, nil
	}
	return rel, nil, nil
}

// hasRef reports whether the manifest lists a reference of kind at path.
func hasRef(m *Manifest, path, kind string) bool {
	for _, r := range m.AbsoluteRefs {
		if r.Path == path && r.Kind == kind {
			return true
		}
	}
	return false
}

// rewriteTarget replaces the capture-home prefix of an absolute link target with the
// destination home.
//
// The target is CLEANED first, the same way underPrefix compares it. A recorded target such as
// `<home>/.local/../x` was written against a flat staging home, where `..` climbs out of a real
// directory; on macos-user `.local` is a link into the workspace sidecar
// (docs/reference/macos-user-home-tiers.md), and darwin resolves `..` physically, so the verbatim
// suffix would land somewhere else. The cleaned form names what the installer meant.
func rewriteTarget(target, from, to string) string {
	return to + strings.TrimPrefix(filepath.Clean(target), from)
}

// sniffBinary applies the recording half's text test to a file: a NUL byte in its first
// binarySniff bytes (git's heuristic, relocate.go).
func sniffBinary(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, binarySniff)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	return bytes.IndexByte(head[:n], 0) >= 0, nil
}

// rewriteFile writes src's bytes to dst with every occurrence of from replaced by to, and
// returns the number of bytes written.
//
// A DIRECTORY at dst is refused, as replaceable refuses one. Anything else at dst is left alone
// until the rename replaces it, so a failure at any point before the rename leaves dst as it was.
func rewriteFile(src, dst string, perm fs.FileMode, from, to string) (int64, error) {
	if fi, err := os.Lstat(dst); err == nil && fi.IsDir() {
		return 0, fmt.Errorf("%s is a directory in this home and a file in the capture — "+
			"refusing to remove it", dst)
	} else if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	// Beside dst, so the rename is within one directory and cannot be EXDEV. Dot-prefixed so a
	// directory listing mid-write does not show a second copy of the program.
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".yolo-rewrite-*")
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmp.Name())
		}
	}()
	n, err := rewriteStream(tmp, in, []byte(from), []byte(to))
	if err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	// CreateTemp makes the file 0600, and the manifest's mode is the installer's.
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return 0, err
	}
	committed = true
	return n, nil
}

// rewriteStream copies r to w, replacing every occurrence of from with to, and returns the
// bytes written. Behind a package var so a test can fail a write halfway through and check that
// nothing partial reaches the destination path.
var rewriteStream = streamReplace

// streamReplace is a streaming bytes.ReplaceAll: non-overlapping, left to right, with no
// rescanning of replaced text.
//
// STREAMED, never read whole, for the reason fileHasPrefix streams: a captured file that carries
// a reference can be large, and holding it in memory is a cost the store exists to avoid. The
// tail kept between reads is len(from)-1 bytes. That is enough: a match starting earlier than
// that would already have been found in the buffer.
//
// It matches exactly what the recording scan matched. The scan is bytes.Contains of the same
// needle, not a path-component test, so the rewrite replaces exactly the occurrences the scan
// counted.
func streamReplace(w io.Writer, r io.Reader, from, to []byte) (int64, error) {
	if len(from) == 0 {
		return io.Copy(w, r)
	}
	keep := len(from) - 1
	chunk := make([]byte, scanChunk)
	var pending []byte
	var written int64
	emit := func(b []byte) error {
		n, err := w.Write(b)
		written += int64(n)
		return err
	}
	for {
		n, rerr := r.Read(chunk)
		pending = append(pending, chunk[:n]...)
		for {
			i := bytes.Index(pending, from)
			if i < 0 {
				break
			}
			if err := emit(pending[:i]); err != nil {
				return written, err
			}
			if err := emit(to); err != nil {
				return written, err
			}
			pending = pending[i+len(from):]
		}
		if rerr == io.EOF {
			return written, emit(pending)
		}
		if rerr != nil {
			return written, rerr
		}
		// Everything but the tail cannot begin a match any more.
		if len(pending) > keep {
			if err := emit(pending[:len(pending)-keep]); err != nil {
				return written, err
			}
			pending = append(pending[:0:0], pending[len(pending)-keep:]...)
		}
	}
}

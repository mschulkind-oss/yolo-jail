package packsrc

// series.go reads a patched fork's PATCH SERIES (docs/design/patched-forks.md §3.2, PF-D2): the
// ordered `git format-patch` files a patched fork replays onto its upstream.
//
//   - Its MEMBERS are the regular files in the `patches` directory whose names end in `.patch`,
//     in byte-wise lexical order of their names, which is `git format-patch`'s 0001-… numbering.
//     Nothing else is read: no series file, no subdirectory, no other extension.
//   - Each member is MAIL-FORMAT, as `git format-patch` writes it, because `git am` makes its
//     commits; a plain `diff -u` is refused, naming the file and the command that makes one.
//   - NO LINK ANYWHERE ON THE WAY: every component of the path from the pack root to each member
//     is checked with lstat, in a fetched pack and a local one alike. The host reads these bytes
//     with a pack's authority, and a link would make the series a read of whatever it names. The
//     reads themselves go through an os.Root of the pack, so a link swapped in after the check
//     still cannot reach outside it.
//   - At least one member, and the first names its BASE (`base-commit:`, which
//     `git format-patch --base` writes). An empty, missing or unreadable directory is each the
//     fork's reason, never read as "no patches".
//   - READ ONCE: a caller reads the series once per act and replays from that copy, so the digest
//     it records describes the bytes it applied.
//
// The SERIES DIGEST (a term coined in the design, §3.2) is the sha256 of the JSON array of
// [name, sha256 of the file's bytes] pairs in series order: JSON for the reason ForkRecipe uses
// it, since no separator can be spelled inside a value.

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
	"sort"
	"strings"
	"syscall"
)

// MaxSeriesBytes bounds the bytes one series read takes into memory. A series is source text;
// 64 MiB is two orders of magnitude past the largest the design measured (22 members, 17 of them
// carrying built output), and a bound keeps a stray multi-gigabyte file in the directory from
// becoming a host process's memory.
const MaxSeriesBytes = 64 << 20

// Series is a patch series as one read found it.
type Series struct {
	// Dir is the series directory as the manifest names it, relative to the pack's root.
	Dir string
	// Members are the series' patches, in series order.
	Members []SeriesMember
	// Base is the full commit id the first member's `base-commit:` names.
	Base string
	// Digest is the series digest.
	Digest string
}

// SeriesMember is one patch of a series, with the bytes the read took.
type SeriesMember struct {
	Name string
	Data []byte
	// Sum is the sha256 of Data, in hex.
	Sum string
}

// Len is the number of patches, for a line that counts them.
func (s *Series) Len() int { return len(s.Members) }

// ShortDigest is the digest as a line names it.
func (s *Series) ShortDigest() string { return shortCommit(s.Digest) }

// SeriesError is why a series cannot be read, as one line naming the file and the fix for its
// cause (§8.1's third row): the fork's reason, never a refused launch.
type SeriesError struct {
	// Path is the pack-relative path the problem is at: the directory, a component on the way to
	// it, or a member.
	Path string
	// Problem says what is wrong, Fix what repairs it.
	Problem, Fix string
	// Err is the underlying error, when there is one.
	Err error
}

func (e *SeriesError) Error() string {
	msg := "patch series " + e.Path + ": " + e.Problem
	if e.Fix != "" {
		msg += " — " + e.Fix
	}
	return msg
}

func (e *SeriesError) Unwrap() error { return e.Err }

// ReadSeries reads the series in the directory rel of the pack rooted at packRoot.
func ReadSeries(packRoot, rel string) (*Series, error) {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "/") || path.Clean(rel) != rel ||
		rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, &SeriesError{Path: rel, Problem: "is not a clean path inside the pack",
			Fix: "name a directory relative to the pack's root, e.g. \"patches\""}
	}
	root, err := os.OpenRoot(packRoot)
	if err != nil {
		return nil, &SeriesError{Path: rel, Problem: "the pack's root cannot be opened (" + err.Error() + ")",
			Fix: "check the pack directory's permissions", Err: err}
	}
	defer root.Close()

	// Every component on the way, lstat'd: a link anywhere is refused, as is a component that is
	// not a directory.
	segs := strings.Split(rel, "/")
	for i := range segs {
		at := strings.Join(segs[:i+1], "/")
		fi, err := root.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if at == rel {
				return nil, &SeriesError{Path: rel, Problem: "the directory does not exist",
					Fix: "correct the fork's \"patches\" or create the directory", Err: err}
			}
			return nil, &SeriesError{Path: at, Problem: "does not exist, so the series directory " +
				rel + " cannot either", Fix: "correct the fork's \"patches\" or create the directory", Err: err}
		case err != nil:
			return nil, &SeriesError{Path: at, Problem: "cannot be read (" + err.Error() + ")",
				Fix: "check the path's permissions", Err: err}
		case fi.Mode()&fs.ModeSymlink != 0:
			return nil, &SeriesError{Path: at, Problem: "is a symbolic link, and a series is read " +
				"from the pack itself, never through a link", Fix: "put a regular directory in its place"}
		case !fi.IsDir():
			return nil, &SeriesError{Path: at, Problem: "is not a directory",
				Fix: "correct the fork's \"patches\" to name the series directory"}
		}
	}
	dir, err := root.Open(rel)
	if err != nil {
		return nil, &SeriesError{Path: rel, Problem: "the directory cannot be read (" + err.Error() + ")",
			Fix: "check its permissions", Err: err}
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return nil, &SeriesError{Path: rel, Problem: "the directory cannot be listed (" + err.Error() + ")",
			Fix: "check its permissions", Err: err}
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".patch") {
			continue
		}
		at := rel + "/" + e.Name()
		switch t := e.Type(); {
		case t&fs.ModeSymlink != 0:
			return nil, &SeriesError{Path: at, Problem: "is a symbolic link, and a series is read from " +
				"the pack itself, never through a link", Fix: "put the patch file itself in its place"}
		case !t.IsRegular():
			return nil, &SeriesError{Path: at, Problem: "is not a regular file",
				Fix: "a series member is a patch file; rename or remove this one"}
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, &SeriesError{Path: rel, Problem: "holds no .patch file, and a patched fork applies " +
			"at least one", Fix: "export the series into it with `git format-patch --base=<upstream " +
			"commit> -o " + rel + " <upstream commit>..HEAD`; to build the upstream unpatched, declare " +
			"a plain fork instead (drop \"patches\")"}
	}
	sort.Strings(names) // byte-wise, which is format-patch's 0001-… order
	s := &Series{Dir: rel}
	total := 0
	for i, name := range names {
		at := rel + "/" + name
		data, err := readMember(root, at, MaxSeriesBytes-total)
		if err != nil {
			return nil, err
		}
		total += len(data)
		if !mailFormat(data) {
			return nil, &SeriesError{Path: at, Problem: "is not a mail-format patch (the first line of " +
				"`git format-patch` output is \"From <commit> Mon Sep 17 00:00:00 2001\"), and `git am` " +
				"makes the series' commits", Fix: "export the commit with `git format-patch` rather " +
				"than `diff -u` or `git diff`"}
		}
		if !strings.Contains(string(data), "\ndiff --git ") {
			return nil, &SeriesError{Path: at, Problem: "carries no diff (a cover letter, or an empty " +
				"commit), so it makes no commit to replay", Fix: "remove it from the series directory"}
		}
		if i == 0 {
			base, ok := baseCommit(data)
			if !ok {
				return nil, &SeriesError{Path: at, Problem: "names no base commit (the \"base-commit:\" " +
					"line), and the series is replayed from its base", Fix: "re-export the series with " +
					"`git format-patch --base=<upstream commit>`"}
			}
			s.Base = base
		}
		sum := sha256.Sum256(data)
		s.Members = append(s.Members, SeriesMember{Name: name, Data: data, Sum: hex.EncodeToString(sum[:])})
	}
	s.Digest = SeriesDigest(s.Members)
	return s, nil
}

// SeriesDigest is the series digest of members: the sha256 of the JSON array of [name, content
// sha256] pairs, in order.
func SeriesDigest(members []SeriesMember) string {
	pairs := make([][2]string, len(members))
	for i, m := range members {
		pairs[i] = [2]string{m.Name, m.Sum}
	}
	canonical, _ := json.Marshal(pairs)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// readMember reads one member through the pack's root, refusing a link that appeared since the
// listing (O_NOFOLLOW) and anything that is not a regular file once opened, and taking at most
// budget bytes.
func readMember(root *os.Root, at string, budget int) ([]byte, error) {
	f, err := root.OpenFile(at, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &SeriesError{Path: at, Problem: "cannot be read (" + err.Error() + ")",
			Fix: "check its permissions, and that it is a regular file rather than a link", Err: err}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, &SeriesError{Path: at, Problem: "is not a regular file",
			Fix: "a series member is a patch file; rename or remove this one", Err: err}
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(budget)+1))
	if err != nil {
		return nil, &SeriesError{Path: at, Problem: "cannot be read (" + err.Error() + ")",
			Fix: "check its permissions", Err: err}
	}
	if len(data) > budget {
		return nil, &SeriesError{Path: at, Problem: fmt.Sprintf("takes the series past %d MiB, the "+
			"most one read takes", MaxSeriesBytes>>20), Fix: "a series is source text: keep built " +
			"output out of it, and let the fork's `build` make it"}
	}
	return data, nil
}

// mailFormat reports whether data opens the way `git format-patch` output does: the mbox
// "From <commit id> " line.
func mailFormat(data []byte) bool {
	line, _, _ := strings.Cut(string(data[:min(len(data), 200)]), "\n")
	rest, ok := strings.CutPrefix(line, "From ")
	if !ok {
		return false
	}
	id, _, ok := strings.Cut(rest, " ")
	return ok && isHexID(id)
}

// baseCommit finds the `base-commit: <id>` line `git format-patch --base` writes.
func baseCommit(data []byte) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		if id, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "base-commit: "); ok {
			id = strings.TrimSpace(id)
			if isHexID(id) {
				return strings.ToLower(id), true
			}
		}
	}
	return "", false
}

// isHexID reports whether s is a full object id: 40 hex digits (SHA-1) or 64 (SHA-256).
func isHexID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

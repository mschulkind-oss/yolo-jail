package packsrc

// scratchread.go reads what `git am` just wrote into the scratch repository WITHOUT STARTING GIT:
// the detached HEAD it left and the parent of each commit it made, both plain files of the fresh
// repository (.git/HEAD and its loose objects, which nothing packs, since every replay run carries
// gc.auto=0). Starting git is most of what a replay costs where starting a process is slow (about
// 25 ms a start on a macOS host, measured in docs/plans/test-suite-speed.md), and these reads were
// two of a replay's starts: the `rev-parse HEAD` after each member's `git am`, and the
// `rev-list --reverse HEAD ^<base>` naming the commits one `git am` of the whole series made.
//
// EVERY READ HERE IS ALL OR NOTHING: anything it does not recognize — a HEAD that is a symbolic
// ref, an object that is not loose, a commit with other than one parent, a chain that does not end
// at the base — is "not read", and the caller asks git exactly as it did before, so the answer is
// git's own whenever these files are not the plain ones a fresh repository holds.

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// detachedHead is the commit the scratch repository's detached HEAD names, read from .git/HEAD,
// and false when HEAD is not a bare object id (a symbolic ref, or a file that cannot be read).
func (sc *scratch) detachedHead() (string, bool) {
	raw, err := os.ReadFile(filepath.Join(sc.repo, ".git", "HEAD"))
	if err != nil {
		return "", false
	}
	id := strings.TrimSuffix(string(raw), "\n")
	if !isHexID(id) {
		return "", false
	}
	return strings.ToLower(id), true
}

// looseParents is the parents of the commit id when it is a loose object of the scratch repository
// itself (not one it borrows from a mirror), read from the object's header; false when it is not
// loose there, not a commit, or not readable as one.
func (sc *scratch) looseParents(id string) ([]string, bool) {
	f, err := os.Open(filepath.Join(sc.repo, ".git", "objects", id[:2], id[2:]))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	z, err := zlib.NewReader(f)
	if err != nil {
		return nil, false
	}
	defer z.Close()
	r := bufio.NewReader(z)
	kind, err := r.ReadString(0)
	if err != nil || !strings.HasPrefix(kind, "commit ") {
		return nil, false
	}
	// The header runs to the first empty line: "tree", then each "parent", then the rest.
	var parents []string
	sawTree := false
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, false
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		if len(line) == 0 {
			return parents, sawTree
		}
		switch k, v, _ := bytes.Cut(line, []byte(" ")); string(k) {
		case "tree":
			sawTree = true
		case "parent":
			if !isHexID(string(v)) {
				return nil, false
			}
			parents = append(parents, strings.ToLower(string(v)))
		}
		if err == io.EOF {
			return nil, false
		}
	}
}

// amCommits is the n commits one `git am` made on top of base, in the order it made them, read
// from HEAD back through each commit's one parent: exactly `git rev-list --reverse HEAD ^<base>`
// for a chain of n single-parent commits from HEAD that reaches base, which is the only chain it
// answers for. false is every other shape, and the caller asks git.
func (sc *scratch) amCommits(base string, n int) ([]string, bool) {
	cur, ok := sc.detachedHead()
	if !ok || n < 1 {
		return nil, false
	}
	base = strings.ToLower(base)
	commits := make([]string, n)
	for i := n - 1; i >= 0; i-- {
		if cur == base {
			return nil, false // fewer than n commits on top of base
		}
		parents, ok := sc.looseParents(cur)
		if !ok || len(parents) != 1 {
			return nil, false
		}
		commits[i], cur = cur, parents[0]
	}
	if cur != base {
		return nil, false
	}
	return commits, true
}

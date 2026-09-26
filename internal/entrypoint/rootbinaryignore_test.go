package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A bare `go build ./cmd/<name>` (or `./tools/<name>`) drops the binary at the repository
// root, where one careless `git add` commits it: a stale root yolo-parity was committed once
// that way. The root .gitignore therefore names every such binary, anchored with a leading
// slash so the pattern cannot also hide a directory of the same name deeper in the tree.
//
// The list is a copy of the main packages under cmd/ and tools/, and a copy drifts in both
// directions. It had drifted both ways at once: binaries built today were missing from it, and
// binaries retired with the Python-era daemons were still on it. So both directions fail here:
// a buildable binary the root ignore does not name, and a root ignore entry naming a binary
// nothing builds any more.

// rootBinaryIgnoreRe matches a root-anchored, extension-less, single-segment ignore line — the
// shape the stray-binary entries take. Directory entries (`/bin/`) end in a slash and do not
// match.
var rootBinaryIgnoreRe = regexp.MustCompile(`^/[a-z][a-z0-9-]*$`)

// buildableBinaries is every directory under cmd/ and tools/, each of which is a main package
// whose `go build` output would be named after it.
func buildableBinaries(t *testing.T, root string) map[string]string {
	t.Helper()
	bins := map[string]string{}
	for _, name := range cmdBinaries(t, root) {
		bins[name] = "cmd/" + name
	}
	entries, err := os.ReadDir(filepath.Join(root, "tools"))
	if err != nil {
		t.Fatalf("read tools/: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			bins[e.Name()] = "tools/" + e.Name()
		}
	}
	return bins
}

func rootBinaryIgnores(t *testing.T, root string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read the root .gitignore: %v", err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if rootBinaryIgnoreRe.MatchString(line) {
			set[strings.TrimPrefix(line, "/")] = true
		}
	}
	return set
}

func TestEveryBuildableBinaryIsIgnoredAtTheRoot(t *testing.T) {
	root := repoRoot(t)
	ignored := rootBinaryIgnores(t, root)
	for name, pkg := range buildableBinaries(t, root) {
		if !ignored[name] {
			t.Errorf("`go build ./%s` writes ./%s at the repository root, and the root "+
				".gitignore has no /%s line to keep it out of a commit — add one", pkg, name, name)
		}
	}
}

func TestRootIgnoreNamesNoRetiredBinary(t *testing.T) {
	root := repoRoot(t)
	bins := buildableBinaries(t, root)
	for name := range rootBinaryIgnores(t, root) {
		if _, ok := bins[name]; !ok {
			t.Errorf("the root .gitignore ignores /%s, but no cmd/%s or tools/%s builds it — "+
				"a binary that was retired or renamed; delete the line", name, name, name)
		}
	}
}

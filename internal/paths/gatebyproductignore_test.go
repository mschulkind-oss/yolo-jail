package paths

import (
	"path/filepath"
	"testing"
)

// TestTheGatesPythonBytecodeIsIgnored covers a byproduct of this repository's own quality
// gate. `just check-ci` runs scripts/test-check-*.py, each of which imports the script it
// tests, and CPython caches that import as scripts/__pycache__/<name>.cpython-3XX.pyc. On a
// clone whose owner has no global `*.pyc` ignore, the gate therefore leaves an untracked
// directory behind, and `just done` reports the working tree dirty after a green run.
//
// It passed on the maintainer's machine for the wrong reason, which is why the check runs
// under gitEnv: that machine's ~/.config/git/ignore carries `*.pyc`, so without it this asks
// the maintainer's global file rather than the repository's.
func TestTheGatesPythonBytecodeIsIgnored(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range []string{
		"scripts/__pycache__/check-site-output-dir.cpython-314.pyc",
		"scripts/__pycache__/check-userguide-closed-tree.cpython-314.pyc",
	} {
		if !gitIgnores(t, root, rel) {
			t.Errorf("%s is not ignored by the repository's own .gitignore — `just check-ci` "+
				"writes it, so a contributor's tree is dirty after every green gate", rel)
		}
	}
}

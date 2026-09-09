package entrypoint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE SHIPPED BINARIES ARE BUILT TWICE, BY TWO LANGUAGES, and the flags are the
// third thing (after the ship set and the prefix layout) that both halves have
// to agree on with nothing making them:
//
//   - flake.nix's hermetic Go build, which fills the MOUNTED /opt/yolo-jail/bin
//     prefix a launch supplies — what actually runs as pid1 in a jail.
//
//   - scripts/build-go.sh, the cross-compile-for-shipping step `just build-go`
//     runs, which fills bin/linux-<arch>/ for a shipped bundle's prebuilt
//     short-circuit.
//
// They disagreed about -trimpath until 2026-09-09 (OQ-WP2), and the way that
// showed up is why this is a test and not a comment: without the flag the LINK
// output records the absolute directory the build ran in, and Go's build cache
// is position-independent enough to reuse a compile action recorded somewhere
// else — so a jail build could emit the HOST's paths. Measured on this repo:
// 5429 references to /home/matt/code/yolo-jail/... (1267 distinct files) in the
// dist-go/ binaries, and 694 /workspace/... paths in a fresh in-jail build of
// the same tree. Both are zero with the flag.
//
// This pins the FLAG in the two files that carry it, not the number above — the
// number is a measurement of one machine on one day and would drift. What must
// not drift is that neither build path loses the flag while the other keeps it.
var goBuildInvocationRe = regexp.MustCompile(`go build\b[^\n]*?-o\s`)

// joinContinuations collapses a shell backslash-newline so a multi-line
// `go build \` invocation is one line to match against. build-go.sh spells it
// that way; flake.nix spells the same command on one line.
func joinContinuations(body []byte) string {
	s := strings.ReplaceAll(string(body), "\\\n", " ")
	return s
}

// goBuildLines returns every line of path that is an actual `go build … -o …`
// invocation. The `-o` is what separates a real invocation from the several
// places both files MENTION `go build` in prose (a comment about the goSrc
// fileset, an echo of the command being run) — those never name an output.
func goBuildLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, line := range strings.Split(joinContinuations(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // a comment quoting the command is not the command
		}
		if goBuildInvocationRe.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
}

// TestBothGoBuildPathsTrimPaths is the cross-language pin, in the same shape as
// run.TestFlakeAndLauncherAgreeOnThePrefixLayout and for the same reason: the
// two spellings of one fact live in two languages, and neither compiler nor
// linter can see the other half.
//
// It fails if EITHER file loses -trimpath, and it fails if a file stops
// containing a recognisable `go build … -o …` at all — because a rewrite that
// this regex no longer matches would otherwise make the test pass by
// discovering nothing to check, which is the vacuous-assertion shape.
func TestBothGoBuildPathsTrimPaths(t *testing.T) {
	root := repoRoot(t)

	for _, path := range []string{
		filepath.Join(root, "flake.nix"),
		filepath.Join(root, "scripts", "build-go.sh"),
	} {
		lines := goBuildLines(t, path)
		if len(lines) == 0 {
			t.Errorf("%s no longer contains a `go build … -o …` invocation this test can "+
				"recognise. Either the Go build moved out of this file — in which case pin "+
				"its new home here — or it was restructured past the match, in which case "+
				"this test has silently stopped checking anything.", path)
			continue
		}
		for _, line := range lines {
			if !strings.Contains(line, "-trimpath") {
				t.Errorf("%s builds Go without -trimpath:\n\t%s\n"+
					"Both build paths must trim, or the binary records the directory it was "+
					"built in — and because Go's build cache reuses a compile action recorded "+
					"elsewhere, a jail build can bake the HOST's source paths into a shipped "+
					"artifact. See the header of this file.", path, strings.TrimSpace(line))
			}
		}
	}
}

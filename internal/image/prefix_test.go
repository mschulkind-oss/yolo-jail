package image

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The prefix build must name the attr the flake actually exposes, with the same
// flake flags every other nix build in this repo carries — a prefix built
// against a different substituter set than the image beside it is a launch whose
// two halves came from two evaluations.
func TestJailPrefixBuildArgv(t *testing.T) {
	argv := flakeBuildArgv(installPrefixAttr, "/tmp/link", nil)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, ".#installPrefix") {
		t.Errorf("prefix build argv does not name .#installPrefix: %v", argv)
	}
	for _, want := range NixFlakeFlags() {
		if !strings.Contains(joined, want) {
			t.Errorf("prefix build argv is missing the shared flake flag %q: %v", want, argv)
		}
	}
	if !strings.Contains(joined, "--out-link /tmp/link") {
		t.Errorf("prefix build argv does not out-link where it was told: %v", argv)
	}
}

// THE OUT-LINK IS THE PREFIX'S GC ROOT, and it has to survive both reapers that
// walk BUILD_DIR. Getting either wrong deletes the store path holding the
// binaries a running jail is executing:
//
//   - prune.SweepDanglingOutLinks scans for "run-result-*";
//   - prune.PruneOrphanImageRoots owns BUILD_DIR/roots and removes every entry
//     that is not a currently-loaded IMAGE — which the prefix never is.
func TestJailPrefixOutLinkIsOutOfBothReapersReach(t *testing.T) {
	link := JailPrefixOutLink("/some/repo")
	base := filepath.Base(link)
	if strings.HasPrefix(base, "run-result-") {
		t.Errorf("out-link %q is named like a per-build image out-link; "+
			"prune.SweepDanglingOutLinks would reap it", base)
	}
	if strings.HasPrefix(link, ImageRootsDir()+string(filepath.Separator)) {
		t.Errorf("out-link %q lives under the image-roots dir, whose reaper deletes "+
			"anything that is not a loaded image", link)
	}
	if filepath.Dir(link) != paths.BuildDir() {
		t.Errorf("out-link %q is not directly under BUILD_DIR %q", link, paths.BuildDir())
	}
}

// Keyed by the SOURCE TREE, so re-building the same checkout replaces one link
// instead of accumulating one root per build — at most one prefix closure is
// pinned per checkout, and it is always the current one.
func TestJailPrefixOutLinkIsPerSourceTree(t *testing.T) {
	a := JailPrefixOutLink("/repo/a")
	b := JailPrefixOutLink("/repo/b")
	if a == b {
		t.Errorf("two source trees share one out-link (%q): building either would unroot the other", a)
	}
	if a != JailPrefixOutLink("/repo/a") {
		t.Error("the out-link for one source tree is not stable across calls")
	}
}

// The mountable layout lives at <storePath>/opt/yolo-jail, mirroring the in-jail
// destination — that is what lets one derivation serve as a host install prefix
// and as the jail's mount source. A change here silently mounts an empty dir.
func TestJailPrefixSubdirMirrorsTheJailDestination(t *testing.T) {
	if JailPrefixSubdir != "opt/yolo-jail" {
		t.Errorf("JailPrefixSubdir = %q, want opt/yolo-jail (flake.nix installPrefix lays it down there)",
			JailPrefixSubdir)
	}
}

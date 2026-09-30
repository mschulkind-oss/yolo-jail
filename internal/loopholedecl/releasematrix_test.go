package loopholedecl_test

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// TestNoShippedManifestDeclaresABinaryYet is the RELEASE MATRIX's tripwire
// (docs/design/broker-as-a-pack.md §14).
//
// An official pack's `binaries` names files at URLs, and for a program yolo builds itself those
// files come from yolo's own release: BP-D7 says which platforms it builds, BP-D8 where it
// publishes them and BP-D9 how each digest gets into the tagged tree. None of that release
// machinery is built yet, so an embedded manifest declaring `binaries` today would pin URLs no
// release publishes, and `yolo pack install` would fail for every user who selects the pack.
// That is §9's named failure mode, "supported, missing".
//
// It iterates shippedManifestHome, which TestShippedManifestHomeIsTotal holds total over the
// embed, so a loophole added to any official pack is covered here without an edit.
//
// It is RETIRED, not relaxed: the plan in §14.4 lands a census test over the same manifests
// (every build on exactly BP-D7's platforms, with BP-D8's URL and a digest the tree reproduces),
// in the change that adds the first official `binaries` entry, and that test replaces this one.
func TestNoShippedManifestDeclaresABinaryYet(t *testing.T) {
	names := make([]string, 0, len(shippedManifestHome))
	for name := range shippedManifestHome {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m, _, err := loopholedecl.DecodeTolerant(shippedManifest(t, name), filepath.Join("/loopholes", name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, b := range m.Binaries {
			t.Errorf("packs/%s/loopholes/%s declares the binary %q (builds for %v), and yolo's "+
				"release builds no pack binary yet: those URLs name files no release publishes, "+
				"so `yolo pack install` fails for every user of the pack. Build the release steps "+
				"in docs/design/broker-as-a-pack.md §14.4 (BP-D7 to BP-D9) in the same change; "+
				"their census test replaces this one", shippedManifestHome[name], name, b.Name,
				b.BuildPlatforms())
		}
	}
}

package entrypoint

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestJailPathHomeDirsCoversBootPath pins paths.JailPathHomeDirs — the list a pack
// manifest's destination is refused against — to BootPath, which is the thing that
// actually builds the jail's PATH.
//
// THE LIST LIVES IN internal/paths BECAUSE packdecl CANNOT IMPORT THIS PACKAGE:
// entrypoint imports packdecl (packsurfaces.go), so the reverse is a cycle. paths is a
// leaf with no internal imports, which makes it the one place both sides can see. The
// cost of that split is a list that can drift from its authority, and this test is the
// payment: add a home directory to BootPath without adding it to JailPathHomeDirs and
// the manifest guard silently stops covering it — a pack could then deliver a tree onto
// PATH with no refusal and no footprint claim, which is the exact outcome the guard
// exists to prevent.
//
// It asserts COVERAGE, not equality: an extra entry in JailPathHomeDirs refuses a
// destination that is not on PATH today, which is over-strict rather than wrong (a
// retired PATH dir kept in the list while packs still name it is the case), and
// TestJailPathHomeDirsHasNoStrays below reports it as information instead.
func TestJailPathHomeDirsCoversBootPath(t *testing.T) {
	home := "/home/agent"
	e := &Env{
		Home:      home,
		NpmPrefix: home + "/.npm-global",
		GoPath:    home + "/go",
		MiseData:  "/mise",
	}

	covered := map[string]bool{}
	for _, d := range paths.JailPathHomeDirs {
		covered[d] = true
	}

	var homeSegments []string
	for _, seg := range strings.Split(BootPath(e), ":") {
		rel, under := strings.CutPrefix(seg, home+"/")
		if !under {
			continue // /bin, /usr/bin, /mise/shims — a manifest path cannot name them
		}
		homeSegments = append(homeSegments, rel)
		if !covered[rel] {
			t.Errorf("BootPath has %q under the jail home, and paths.JailPathHomeDirs "+
				"does not list it — a pack could deliver a tree onto PATH there with no "+
				"refusal. Add it to JailPathHomeDirs.", rel)
		}
	}
	if len(homeSegments) == 0 {
		t.Fatal("no home-relative segments parsed out of BootPath — the pin is not " +
			"pinning anything; fix this test rather than deleting it")
	}
}

// TestJailPathHomeDirsHasNoStrays reports an entry that BootPath no longer contains.
// Not a failure: over-refusing is safe, and a name kept through a rename deliberately
// (the way removeRetiredGeneratedDirs keeps sweeping the retired dirs) is legitimate.
// It is logged so the list cannot accumulate dead entries unnoticed.
func TestJailPathHomeDirsHasNoStrays(t *testing.T) {
	home := "/home/agent"
	e := &Env{
		Home:      home,
		NpmPrefix: home + "/.npm-global",
		GoPath:    home + "/go",
		MiseData:  "/mise",
	}
	onPath := map[string]bool{}
	for _, seg := range strings.Split(BootPath(e), ":") {
		if rel, under := strings.CutPrefix(seg, home+"/"); under {
			onPath[rel] = true
		}
	}
	for _, d := range paths.JailPathHomeDirs {
		if !onPath[d] {
			t.Logf("paths.JailPathHomeDirs lists %q, which BootPath no longer contains "+
				"— deliberate (a retired name kept reserved) or a leftover?", d)
		}
	}
}

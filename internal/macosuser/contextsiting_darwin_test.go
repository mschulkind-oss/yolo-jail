//go:build darwin

package macosuser

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// contextsiting_darwin_test.go is the half of the context-mount siting only a Mac can say:
// that DarwinContextSiting's facts are this machine's. The siting is lexical over RESOLVED
// sources (the run pipeline resolves each with filepath.EvalSymlinks before it is judged), so
// every place it guards must be spelled the way resolution spells it here — a writable place
// or a state dir listed only under a symlinked name would be one a resolved source walks past.
// It reads the filesystem and writes nothing but one temp dir.

// Every writable place, every state-dir spelling and the users root resolve to a spelling the
// siting also lists, so a resolved source is judged against the name the kernel reports.
func TestDarwinContextSitingListsTheResolvedSpellingOfEveryPlace(t *testing.T) {
	s := DarwinContextSiting()
	check := func(what string, list []string) {
		for _, p := range list {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				// /var/yolo-jail exists only once a launch staged something; its parent says.
				parent, perr := filepath.EvalSymlinks(filepath.Dir(p))
				if perr != nil {
					t.Errorf("%s %s: %v", what, p, err)
					continue
				}
				resolved = filepath.Join(parent, filepath.Base(p))
			}
			if !slices.Contains(list, resolved) {
				t.Errorf("%s %s resolves to %s on this Mac, and the siting does not list that "+
					"spelling: a resolved source there would not be recognised", what, p, resolved)
			}
		}
	}
	check("writable place", s.WritableRoots)
	check("state dir", s.StateDirs)
	if resolved, err := filepath.EvalSymlinks(s.UsersRoot); err != nil || resolved != s.UsersRoot {
		t.Errorf("the users root %s resolves to %q (%v); the siting and the profile both name it as is",
			s.UsersRoot, resolved, err)
	}
}

// THE TEMP DIR IS A WRITABLE PLACE HERE, so a read-only context mount of anything under it
// refuses — the case every macos-user unit test states explicitly rather than inherits, for
// exactly this reason.
func TestATempDirIsInTheSandboxWritableSetOnThisMac(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := SiteContextLinks(DarwinContextSiting(), "/Users/Shared/yolo/proj",
		[]ContextLink{{Dest: "/ctx/x", Source: dir, Dir: true}}, nil)
	if len(got) != 1 {
		t.Fatalf("a read-only source under this Mac's temp dir (%s) was admitted: %+v", dir, got)
	}
	t.Logf("refused as expected: %s", got[0].Reason)
}

// The boot volume's /Volumes entry, which the profile re-allows. Logged, not asserted: a CI
// runner does not always carry the link (integration/macosuserseatbelt_test.go's
// boot-volume-read-allow entry says why), and the siting admits a source under it only if
// resolution leaves it there.
func TestDarwinContextSitingBootVolumeOnThisMac(t *testing.T) {
	s := DarwinContextSiting()
	if _, err := os.Lstat(s.BootVolume); err != nil {
		t.Logf("%s is not on this Mac: %v", s.BootVolume, err)
		return
	}
	resolved, err := filepath.EvalSymlinks(s.BootVolume)
	t.Logf("%s resolves to %q (%v): a source spelled under it is judged at that path", s.BootVolume, resolved, err)
}

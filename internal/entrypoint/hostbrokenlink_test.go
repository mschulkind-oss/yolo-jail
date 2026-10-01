package entrypoint

// hostbrokenlink_test.go pins the broken-link rule (hostbrokenlink.go): a host destination that is
// a symlink into a directory that does not exist is REFUSED by name, per surface, and never fails
// the pack it belongs to.
//
// Measured on the maintainer's host 2026-09-28: ~/.pi/agent/settings.json was an rcm dotfile link
// into ~/.dotfiles/pi, which the user had deleted when that config moved into a pack. The write
// followed the link, open(2) failed ENOENT, and RenderHostPack returned the error — so the whole pi
// pack failed to render and `yolo host -- claude` refused to launch over it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// linkIntoMissingDir makes path a symlink to a file inside a directory that does not exist, and
// returns the link's target.
func linkIntoMissingDir(t *testing.T, path string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "dotfiles-gone", "pi", filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	return target
}

func renderPiResults(t *testing.T, home string, observe bool, own render.HostOwnership) []HostRenderResult {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatal(err)
	}
	results, err := RenderHostPack(pi, home, own, observe, nil, nil)
	if err != nil {
		t.Fatalf("RenderHostPack(pi) failed the whole pack over one destination: %v", err)
	}
	return results
}

// THE MAINTAINER'S CASE, in both postures and under both writing contracts: the surface is refused
// by name with its link and target, the pack still renders, the link is left exactly as it was, and
// nothing is created at the target.
func TestABrokenLinkDestinationIsRefusedByNameAndDoesNotFailThePack(t *testing.T) {
	for _, own := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		for _, observe := range []bool{true, false} {
			home := t.TempDir()
			path := filepath.Join(home, ".pi", "agent", "settings.json")
			target := linkIntoMissingDir(t, path)

			results := renderPiResults(t, home, observe, own)
			r := resultFor(t, results, "pi/settings")
			if r.BrokenLink == nil {
				t.Fatalf("own=%v observe=%v: pi/settings through a link into a missing directory: "+
					"Action=%q BrokenLink=nil; want a broken-link refusal", own, observe, r.Action)
			}
			if r.BrokenLink.Link != path || r.BrokenLink.Target != target {
				t.Errorf("own=%v observe=%v: BrokenLink=%+v, want link %s target %s",
					own, observe, *r.BrokenLink, path, target)
			}
			if !strings.HasPrefix(r.Action, "refused: ") || !strings.Contains(r.Action, target) {
				t.Errorf("own=%v observe=%v: Action=%q; want a refusal naming the target",
					own, observe, r.Action)
			}
			if r.WouldChange {
				t.Errorf("own=%v observe=%v: a refused destination reported WouldChange", own, observe)
			}
			if got, err := os.Readlink(path); err != nil || got != target {
				t.Errorf("own=%v observe=%v: the link was touched: readlink=%q err=%v", own, observe, got, err)
			}
			if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
				t.Errorf("own=%v observe=%v: the render created the link's missing directory "+
					"(stat: %v)", own, observe, err)
			}
			// The rest of the pack rendered: some other pi surface has an ordinary result.
			others := 0
			for _, o := range results {
				if o.Surface != "pi/settings" && o.BrokenLink == nil {
					others++
				}
			}
			if others == 0 {
				t.Errorf("own=%v observe=%v: no other pi surface rendered", own, observe)
			}
		}
	}
}

// A LINK WHOSE TARGET'S DIRECTORY EXISTS IS NOT BROKEN: a dotfiles checkout that has not created
// the file yet is written THROUGH (HC-D4's dangling-link case, hostcreatedfile_test.go). And a
// plain missing destination is created, parent directories included.
func TestOnlyALinkIntoAMissingDirectoryIsBroken(t *testing.T) {
	dir := t.TempDir()
	existingDir := filepath.Join(dir, "dotfiles")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	through := filepath.Join(dir, "through.json")
	if err := os.Symlink(filepath.Join(existingDir, "x.json"), through); err != nil {
		t.Fatal(err)
	}
	if b := FindBrokenLink(through); b != nil {
		t.Errorf("a link whose target's directory exists reported broken: %+v", *b)
	}
	if b := FindBrokenLink(filepath.Join(dir, "absent", "deeper", "file.json")); b != nil {
		t.Errorf("a plain missing destination reported broken: %+v", *b)
	}
	regular := filepath.Join(dir, "regular.json")
	if err := os.WriteFile(regular, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := FindBrokenLink(regular); b != nil {
		t.Errorf("a regular file reported broken: %+v", *b)
	}

	// A broken link one level UP — the destination's directory is itself a link to nowhere —
	// is the same class: MkdirAll would create the missing directory somewhere the user deleted.
	dirLink := filepath.Join(dir, "agent")
	if err := os.Symlink(filepath.Join(dir, "gone", "agent"), dirLink); err != nil {
		t.Fatal(err)
	}
	b := FindBrokenLink(filepath.Join(dirLink, "settings.json"))
	if b == nil || b.Link != dirLink {
		t.Errorf("a destination under a broken directory link: got %+v, want the link %s", b, dirLink)
	}

	// A chain that ends in a missing directory is broken too, and the TARGET named is the end of
	// the chain — the path whose directory is missing.
	end := filepath.Join(dir, "nowhere", "end.json")
	mid := filepath.Join(dir, "mid.json")
	if err := os.Symlink(end, mid); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(dir, "head.json")
	if err := os.Symlink("mid.json", head); err != nil { // relative, as rcm writes some
		t.Fatal(err)
	}
	b = FindBrokenLink(head)
	if b == nil || b.Link != head || b.Target != end {
		t.Errorf("a chain into a missing directory: got %+v, want link %s target %s", b, head, end)
	}
}

// THE BRIEFING KIND gets the same answer, in both postures, and one broken destination does not
// stop the others: before the rule, the ENOENT aborted every destination after it.
func TestABrokenLinkBriefingDestinationIsRefusedAndTheOthersStillWrite(t *testing.T) {
	for _, observe := range []bool{true, false} {
		home := t.TempDir()
		broken := filepath.Join(home, ".claude", "CLAUDE.md")
		target := linkIntoMissingDir(t, broken)
		ok := filepath.Join(home, ".pi", "agent", "AGENTS.md")
		packs := []*packload.Pack{
			briefingPack(t, "a-claude", ".claude/CLAUDE.md", "Claude prose.\n"),
			briefingPack(t, "b-pi", ".pi/agent/AGENTS.md", "Pi prose.\n"),
		}
		req, _ := briefingReq(t, home)
		results, err := RenderHostBriefings(packs, home, req, observe)
		if err != nil {
			t.Fatalf("observe=%v: RenderHostBriefings failed over one broken link: %v", observe, err)
		}
		var sawBroken, sawOK bool
		for _, r := range results {
			switch r.Path {
			case broken:
				sawBroken = true
				if r.BrokenLink == nil || r.BrokenLink.Target != target ||
					!strings.HasPrefix(r.Action, "refused: ") {
					t.Errorf("observe=%v: broken destination: Action=%q BrokenLink=%+v", observe,
						r.Action, r.BrokenLink)
				}
			case ok:
				sawOK = true
				if !r.WouldChange {
					t.Errorf("observe=%v: the ordinary destination reported no change: %+v", observe, r)
				}
			}
		}
		if !sawBroken || !sawOK {
			t.Fatalf("observe=%v: results missing a destination: %+v", observe, results)
		}
		if _, serr := os.Stat(filepath.Dir(target)); !os.IsNotExist(serr) {
			t.Errorf("observe=%v: the render created the link's missing directory (stat: %v)", observe, serr)
		}
		if !observe {
			if got := readFile(t, ok); got == "" {
				t.Errorf("the ordinary destination was not written")
			}
		}
	}
}

// THE READ SIDE (FindDanglingLink): the write-through exemption is gone, a loop counts, and the
// shapes that must never be reported — a file there through a link that resolves, a regular
// file, and a plain missing file — are not.
func TestADanglingLinkIsAnyLinkAReadCannotFollow(t *testing.T) {
	dir := t.TempDir()
	existingDir := filepath.Join(dir, "dotfiles")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The write-through case FindBrokenLink exempts: a read of it reads nothing.
	through := filepath.Join(dir, "through.json")
	missing := filepath.Join(existingDir, "x.json")
	if err := os.Symlink(missing, through); err != nil {
		t.Fatal(err)
	}
	if link, target, ok := FindDanglingLink(through); !ok || link != through || target != missing {
		t.Errorf("a link to a missing file in an existing directory: got (%q, %q, %v), want (%q, %q, true)",
			link, target, ok, through, missing)
	}

	// An ancestor link to nowhere is named as the link that explains the missing file.
	dirLink := filepath.Join(dir, "agent")
	gone := filepath.Join(dir, "gone", "agent")
	if err := os.Symlink(gone, dirLink); err != nil {
		t.Fatal(err)
	}
	if link, target, ok := FindDanglingLink(filepath.Join(dirLink, "settings.json")); !ok || link != dirLink || target != gone {
		t.Errorf("a file under a dangling directory link: got (%q, %q, %v), want (%q, %q, true)",
			link, target, ok, dirLink, gone)
	}

	// A loop resolves to nothing either.
	loopA, loopB := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}
	if link, _, ok := FindDanglingLink(loopA); !ok || link != loopA {
		t.Errorf("a link loop: got (%q, %v), want (%q, true)", link, ok, loopA)
	}

	// Never reported: a link that resolves, a regular file, and a plain missing file.
	real := filepath.Join(existingDir, "real.json")
	if err := os.WriteFile(real, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolves := filepath.Join(dir, "resolves.json")
	if err := os.Symlink(real, resolves); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{resolves, real, filepath.Join(dir, "absent", "deeper", "file.json")} {
		if link, target, ok := FindDanglingLink(p); ok {
			t.Errorf("%s reported dangling: (%q, %q)", p, link, target)
		}
	}
}

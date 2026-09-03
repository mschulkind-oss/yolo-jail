package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// A `packs` entry may name a local pack home-relative. The motivating case is a
// dotfiles tree shared between a Linux host and a Mac: "/home/matt" and
// "/Users/matt" are not the same string, so an absolute entry cannot name one
// directory on both machines, while "~/" can.
//
// A bare ABSOLUTE path is accepted for the same reason: checkPackName forbids "/"
// in a pack name, so an entry carrying a separator can never be an embedded name,
// and the file:// scheme was disambiguating something that needed no disambiguation.
//
// Normalization must happen in lowerPackSource — the ONE funnel every entry passes
// through — so that PackEntry.Source is a file:// URL for staging, Slug, provenance
// and packsrc.Parse alike. These cases go through lowerPackSource rather than
// localPackAddress for exactly that reason: a test on the helper alone would pass
// with the call site deleted.
func TestPackSourceExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"bare tilde path", "~/dotfiles/packs/mine", "file://" + filepath.Join(home, "dotfiles/packs/mine")},
		{"bare tilde alone", "~", "file://" + home},
		{"bare absolute path", "/opt/packs/mine", "file:///opt/packs/mine"},
		{"bare absolute path is cleaned", "/opt/./packs//mine/", "file:///opt/packs/mine"},
		// Absolute and remote addresses must pass through byte-identical: expansion
		// is an added spelling, not a rewrite of the ones that already worked.
		{"absolute file", "file:///opt/packs/mine", "file:///opt/packs/mine"},
		{"git address", "git+ssh://git@host/org/repo//sub?ref=main", "git+ssh://git@host/org/repo//sub?ref=main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, problem := lowerPackSource(tc.source, "", "config.packs[0]")
			if problem != "" {
				t.Fatalf("lowerPackSource(%q) refused: %s", tc.source, problem)
			}
			if entry.Source != tc.want {
				t.Errorf("Source = %q, want %q", entry.Source, tc.want)
			}
		})
	}
}

// A home-expanded entry must be LOCAL, so it takes the no-fetch path and may grant
// host files — the same treatment an absolute file:// entry gets. Getting this
// wrong would classify the user's own directory as fetched third-party content.
func TestPackSourceExpandedHomeIsLocal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entry, problem := lowerPackSource("~/dotfiles/packs/mine", "", "config.packs[0]")
	if problem != "" {
		t.Fatalf("refused: %s", problem)
	}
	if !entry.IsLocal() {
		t.Errorf("IsLocal() = false for %q — a home-relative path is a local pack", entry.Source)
	}
	if entry.Origin() != OriginLocal {
		t.Errorf("Origin() = %v, want OriginLocal", entry.Origin())
	}
	if entry.Name != "mine" {
		t.Errorf("Name = %q, want %q (derived from the expanded path)", entry.Name, "mine")
	}
}

// Relative entries stay REFUSED, and the refusal names what to write instead.
// This is the half of the 2026-09-03 question that was NOT implemented: the anchor
// a relative entry needs is "beside the declaring file", packs does not go through
// the loader funnel that anchoring lives in, and packsrc.Parse rejects ".."
// outright. Pinned so that adding "~" did not quietly admit "./" too.
func TestPackSourceRefusesRelative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, src := range []string{"./my-pack", "../my-pack", "packs/mine"} {
		_, problem := lowerPackSource(src, "", "config.packs[0]")
		if problem == "" {
			t.Errorf("lowerPackSource(%q) was accepted; relative entries have no anchor", src)
			continue
		}
		if !strings.Contains(problem, "~/") {
			t.Errorf("refusal for %q does not offer the home-relative spelling: %s", src, problem)
		}
	}
}

// `file://~/…` is REFUSED rather than expanded. It is a malformed URL (RFC 8089
// reads the "~" as the authority), and an earlier cut of this feature accepted it
// so that packsrc.parseFile would not force-absolutize it into the literal path
// "/~/x". Accepting it meant yolo read a string differently from every other URL
// parser; refusing it fixes the same silent mis-parse without that divergence, and
// the refusal must name the one-word fix.
func TestPackSourceRefusesTildeUnderFileScheme(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, src := range []string{"file://~/dotfiles/packs/mine", "file://~"} {
		entry, problem := lowerPackSource(src, "", "config.packs[0]")
		if problem == "" {
			t.Errorf("lowerPackSource(%q) = %+v, want a refusal (malformed URL)", src, entry)
			continue
		}
		// It must not merely reject: the fix is dropping seven characters, and a
		// refusal that does not say so sends the reader to the source.
		if !strings.Contains(problem, "on its own") {
			t.Errorf("refusal for %q does not name the bare spelling: %s", src, problem)
		}
	}
}

// The silent mis-parse this replaces, pinned so it cannot come back by another
// route: whatever lowerPackSource accepts must never reach packsrc.Parse as a path
// starting "/~", which is what parseFile's `"/" + TrimPrefix(rest, "/")` produced.
func TestPackSourceNeverYieldsLiteralTildePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, src := range []string{"~/x", "file://~/x", "/opt/x", "file:///opt/x"} {
		entry, problem := lowerPackSource(src, "", "config.packs[0]")
		if problem != "" {
			continue // refused entries never reach packsrc at all
		}
		if strings.Contains(entry.Source, "/~") {
			t.Errorf("lowerPackSource(%q) produced %q — a literal \"~\" directory", src, entry.Source)
		}
	}
}

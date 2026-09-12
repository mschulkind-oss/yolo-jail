package cli

// hostadoptionarchivetier_test.go is the ZERO-BYTES half of OQ-CO7's disclosure
// (docs/design/config-ownership-and-promotion.md §6.3.3, §11).
//
// hostadoptionarchive_test.go pins that `yolo host apply` NAMES the copy, on a fixture whose
// render visibly rewrites the file. That leaves the canonical case unmeasured, and it is the
// case the ruling exists for: §11's criterion says switching a home from `host_management:
// assert` to `own` changes ZERO BYTES, so the adoption that archives usually reports
// WouldChange=false — and a tier-2 destination's own line lives behind --verbose. The archive
// line does not (OQ-RO3 forbids hiding a disclosure), so the default view printed the indented
// disclosure with no heading of its own:
//
//	  autonomy   guarded posture — permission prompts stay ON; …
//	    archived your file as yolo found it: …/archive/config/claude-settings/settings.json
//	Nothing to apply — this home is up to date.
//
// Measured on the real binary, 2026-09-12: a disclosure attached to an unrelated line and
// contradicted by the verdict under it. configResultTier is the fix — an adopting render is a
// tier-3 destination whether or not the composition reproduced the bytes — and this is what
// says so.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ownHomeSeeded is ownApplyFixture with the seed as a parameter: a real home under
// `host_management: own` holding exactly `seed` at ~/.claude/settings.json, with the process
// pointed at it. The seed has to vary here because the case below is built by MEASURING what
// the owned render emits and then handing those bytes to a fresh home.
func ownHomeSeeded(t *testing.T, seed string) (home, settings string) {
	t.Helper()
	home = t.TempDir()
	selectPacks(t, home, `"claude"`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_management":"own"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	settings = filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, seed)
	return home, settings
}

// AN ADOPTION THAT CHANGES NOTHING STILL REPORTS ITS SURFACE, so the archive line has a heading.
//
// The fixture is built in two phases rather than from a hand-written literal, because the bytes
// that make adoption a no-op are whatever THIS pack's owned render emits — a literal would pin
// claude/settings' current output and go red on every unrelated layer change. Phase 1 measures
// them; phase 2 hands them to a fresh home, where the first owned render adopts a file it
// reproduces exactly.
func TestHostApplyReportsTheSurfaceWhenAdoptionChangesNoBytes(t *testing.T) {
	// Phase 1: what the owned render produces for this pack.
	_, primed := ownHomeSeeded(t, `{"apiKeyHelper":"/usr/local/bin/acme-key.sh"}`)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("priming apply rc=%d\n%s", rc, report)
	}
	canonical, err := os.ReadFile(primed)
	if err != nil {
		t.Fatal(err)
	}

	// Phase 2: a fresh home already holding them.
	home, settings := ownHomeSeeded(t, string(canonical))
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("`own` apply rc=%d\n%s", rc, report)
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(canonical) {
		t.Fatalf("the render moved the file, so this is not the zero-bytes case any more and "+
			"the surface line would print for the ordinary reason.\nbefore:\n%s\nafter:\n%s",
			canonical, after)
	}
	archive := filepath.Join(paths.GlobalStorageUnder(home),
		"archive", "config", "claude-settings", "settings.json")
	if _, serr := os.Stat(archive); serr != nil {
		t.Fatalf("a first owned render over an existing file archived nothing: %v\n%s\n\n"+
			"Adoption is gated on there being no trusted last_render and some bytes to copy — "+
			"NOT on the render changing anything (see archiveAdoption's fourth-condition ⚠).",
			serr, report)
	}
	if !strings.Contains(report, archive) {
		t.Fatalf("the apply archived the user's file and never named the copy:\n%s", report)
	}
	// THE ASSERTION. The archive line is indented under its surface; the surface's own line is
	// what makes it say WHICH file was archived and why the home was touched at all.
	if !strings.Contains(report, "claude/settings") {
		t.Errorf("the report names the archive but not the surface it belongs to:\n%s\n\n"+
			"An indented disclosure with no heading attaches itself to whatever line precedes "+
			"it — here `autonomy`, which has nothing to do with it — under a verdict reading "+
			"\"Nothing to apply\". configResultTier must count a non-empty Archived as tier 3: "+
			"an adopting render is a one-way door whether or not it reproduced the bytes.",
			report)
	}
}

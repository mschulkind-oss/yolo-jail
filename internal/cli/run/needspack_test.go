package run

// needspack_test.go pins the launch call site of the `needs` closure
// (docs/design/wire-bridge.md §3.1). packload.ResolveNeeds' own tests pin the
// closure; this file exists for the OTHER half of the rule — the call site in
// stagePacks. A test that pins the callee while the call site is unpinned is not
// a test (AGENTS.md, Testing): delete the ResolveNeeds call from packs.go and
// TestStagePacksResolvesNeeds goes red, which is the property that matters.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// needsFixturePack writes a local one-file pack whose manifest declares one
// need, loaded the way the launch path loads a pack. The dir basename is the
// pack name, which is what a bare-string file:// config entry names it after;
// callers use p.Root as the config address.
func needsFixturePack(t *testing.T, name, needsJSON string) *packload.Pack {
	t.Helper()
	return writePackManifest(t, name,
		`{"name":"`+name+`","needs":`+needsJSON+`}`)
}

// TestStagePacksResolvesNeeds: a configured pack whose live need names an
// embedded official pack gets that pack JOINED to the launch — staged under
// _official like any selected embedded pack (the mount is the filter: a need
// whose target never stages is a need that does nothing in the jail), added to
// the loaded set, and disclosed on stderr with the cause line (WB-D12 — the one
// forbidden behavior is the silent join).
func TestStagePacksResolvesNeeds(t *testing.T) {
	home := packHome(t)
	needy := needsFixturePack(t, "needy", `[{"pack":"zai","when_bins":["claude"]}]`)
	writeUserPacks(t, home, `["file://`+needy.Root+`", "claude"]`)

	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf}
	_, loaded, _, err := o.stagePacks("yolo-test-needs")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}

	var names []string
	var zai *packload.Pack
	for _, p := range loaded {
		names = append(names, p.Name)
		if p.Name == "zai" {
			zai = p
		}
	}
	if zai == nil {
		t.Fatalf("the live need did not join zai: loaded = %v", names)
	}
	if !strings.Contains(zai.Root, "_official") {
		t.Errorf("the added pack staged at %q — a pack the entrypoint never finds under "+
			"the staging root renders nothing (the mount is the filter)", zai.Root)
	}
	if got := errBuf.String(); !strings.Contains(got, "+ zai (needed by needy: claude selected)") {
		t.Errorf("the launch stderr must carry the cause line (WB-D12):\n%s", got)
	}
}

// TestStagePacksSkipsAConditionNothingSatisfies: the need's when_bins names a bin
// no selected pack installs, so nothing joins and nothing prints. The control for
// the test above — without it, "always join" would pass.
func TestStagePacksSkipsAConditionNothingSatisfies(t *testing.T) {
	home := packHome(t)
	needy := needsFixturePack(t, "needy", `[{"pack":"zai","when_bins":["codex"]}]`)
	writeUserPacks(t, home, `["file://`+needy.Root+`", "claude"]`)

	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf}
	_, loaded, _, err := o.stagePacks("yolo-test-needs-unmet")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	for _, p := range loaded {
		if p.Name == "zai" {
			t.Errorf("a condition nothing satisfies must not join zai")
		}
	}
	if got := errBuf.String(); strings.Contains(got, "+ zai") {
		t.Errorf("an unmet condition must print no cause line:\n%s", got)
	}
}

// TestStagePacksRefusesANonEmbeddedNeed: the closure's WB-D9 refusal refuses the
// LAUNCH, not just the closure call — pins that packs.go propagates the error
// rather than downgrading it to a warning the jail starts past.
func TestStagePacksRefusesANonEmbeddedNeed(t *testing.T) {
	home := packHome(t)
	needy := needsFixturePack(t, "needy", `[{"pack":"ghost"}]`)
	writeUserPacks(t, home, `["file://`+needy.Root+`", "claude"]`)

	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	_, _, _, err := o.stagePacks("yolo-test-needs-ghost")
	if err == nil {
		t.Fatal("a need naming a non-embedded pack must refuse the launch")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("the refusal must name the target: %v", err)
	}
}

// TestNeedsJoinedPackCarriesItsEffectsIntoTheArgv: the closure is worth nothing
// unless the ADDED pack's contributions compose exactly like a selected pack's.
// A fixture donor whose only content is one env var joins through ResolveNeeds
// beside a real embedded pack, and the composed argv must carry the var — the
// same channel (packEnv → -e) a hand-selected pack rides, no special casing.
func TestNeedsJoinedPackCarriesItsEffectsIntoTheArgv(t *testing.T) {
	donor := writePackManifest(t, "donor",
		`{"name":"donor","contributes":[{"kind":"env","vars":{"DONOR_PROBE":"joined"}}]}`)
	needy := writePackManifest(t, "needy",
		`{"name":"needy","needs":[{"pack":"donor","when_bins":["claude"]}]}`)

	selected := []*packload.Pack{officialPack(t, "claude"), needy}
	added, causes, err := packload.ResolveNeeds(selected, func(name string) (*packload.Pack, bool) {
		if name == "donor" {
			return donor, true
		}
		return nil, false
	})
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if len(added) != 1 || added[0].Name != "donor" {
		t.Fatalf("added = %v, want [donor]", added)
	}
	if len(causes) != 1 || causes[0] != "+ donor (needed by needy: claude selected)" {
		t.Fatalf("causes = %q, want the one disclosure line", causes)
	}

	argv := zaiLaunch(t, append(selected, added...), bareConfig(), emptyEnv(), nil)
	vals := envArgValues(argv, "DONOR_PROBE")
	if len(vals) != 1 || vals[0] != "DONOR_PROBE=joined" {
		t.Errorf("the joined pack's env block must reach the argv like a selected "+
			"pack's: %q", vals)
	}
}

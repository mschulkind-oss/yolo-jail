package entrypoint

// rmwprovenancecensus_test.go pins that the rmw writer's provenance decision comes from the
// TARGET'S MODE CENSUS (render.ModeSet) and not from a comparison against one Kind (plan §6b
// D2 / Q8).
//
// The branch it replaced was `if e.renderTarget().KindOf() == render.KindHost` — the
// codebase's only live KindHost special-case. For the notches that exist the two spellings
// agree, so the refactor is behavior-preserving BY DESIGN, and that is what makes it testable
// in exactly one way: assert the observable outcome on both sides of the branch, then assert
// that it tracks the census rather than the Kind. A test that only checked the host side would
// pass against a writer that recorded unconditionally.
//
// Every test renders into a t.TempDir() home, so a record can only land under that home's
// state dir (render.Target.ProvenanceDir) — never the invoking user's.

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// BOTH SIDES OF THE BRANCH, as the observable file. An rmw render writes a record at the
// notch whose census says rmw is its recording mode (host) and no record where another mode
// carries that duty (jail) — the asymmetry `config diff` reports and the one the anti-
// laundering pass depends on.
func TestRMWProvenanceFollowsTheTargetsModeCensus(t *testing.T) {
	surface := manifest.Surface{
		Agent: "census", Name: "settings", Codec: "json",
		Path: "~/.census/settings.json", Mode: manifest.ModeRMW,
		Managed: map[string]any{"telemetry": false},
	}

	// HOST: rmw is the only mode, so it records.
	eh := &Env{Home: t.TempDir(), Vars: map[string]string{}, hostTarget: true}
	if err := renderSurfaceRMWSurface(eh, surface, nil, nil); err != nil {
		t.Fatalf("host render: %v", err)
	}
	if _, found := hostProvenance(t, eh.Home, "census", "settings"); !found {
		t.Error("the HOST notch wrote no provenance record for an rmw render. Its census says " +
			"rmw IS its recording mode — rmw is the only mode there, so \"rmw records nothing\" " +
			"degenerates into \"the host records nothing\", and a key a dropped pack contributed " +
			"comes back attributed to the user")
	}

	// JAIL: `stateful` carries the recording duty, so an rmw surface keeps no sidecar and
	// `config diff` says exactly that (pack-config-collaboration.md §8).
	ej := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	if err := renderSurfaceRMWSurface(ej, surface, nil, nil); err != nil {
		t.Fatalf("jail render: %v", err)
	}
	if _, err := os.Stat(prismProvenancePath(ej, "census", "settings")); err == nil {
		t.Error("a JAIL rmw render wrote a provenance record. It must not: `config diff` " +
			"reports the absence as \"this surface's mode keeps no provenance sidecar\", and a " +
			"record here both falsifies that message and puts a new write on the A12-fatal boot " +
			"path")
	}

	// And the decision tracks the CENSUS, not the Kind. This is the part that would survive a
	// fourth notch: if the writer went back to comparing against render.KindHost these two
	// would still agree today and diverge the moment a notch's census said something its Kind
	// equality could not express.
	if !render.Host(eh.Home, nil).Modes().Records(manifest.ModeRMW) {
		t.Error("the host census no longer says rmw records — the writer's behavior above and " +
			"the census it reads have come apart")
	}
	if render.Jail(ej.Home, ej.Workspace, nil).Modes().Records(manifest.ModeRMW) {
		t.Error("the jail census now says rmw records, contradicting the observed absence")
	}
}

// THE ANTI-LAUNDERING PROPERTY, through the census. This is the same fact
// provenanceretire_test.go pins from the drop side, asserted here as what the branch is FOR:
// the host record is the only memory of a dropped pack's claim, so a writer that stopped
// recording (or recorded at the wrong notch) would silently convert yolo's own output into
// "the user set this" — with no failing test anywhere unless the record itself is checked.
func TestHostCensusIsWhatKeepsADroppedPacksKeyAttributable(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, manifest.ModeRMW)
	dropme := overlayContributorPack(t, "dropme", map[string]any{"fileSuggestion": "run-fzf"})

	overlays := packoverlay.Collect([]*packload.Pack{owner, dropme}, false, nil)
	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatal("no record after the first apply — nothing downstream can be attributed")
	}
	if got := first["fileSuggestion"]; got != "config-overlay:dropme" {
		t.Fatalf("precondition: the first apply must attribute the key to the contributing "+
			"pack, got %q\nrecord: %v", got, first)
	}
}

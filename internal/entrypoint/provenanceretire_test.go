package entrypoint

// provenanceretire_test.go pins the ANTI-LAUNDERING property of the host provenance record:
// a key yolo force-wrote for a layer that no longer claims it must NOT come back as `host`.
//
// THE DEFECT (docs/plans/host-pack-drop-cleanup.md, "The real defect: provenance laundering",
// ruling R3). rmwProvenance derives `host` for every key the existing file has, then upgrades
// the ones a live layer claims. While a pack is configured its overlay key records as
// `config-overlay:<pack>`; drop the pack and the very next apply rewrites that to `host`,
// because the key is still in the file and nothing claims it any more. yolo's own output
// becomes "the user set this" — and self-reinforcingly, since every mechanism that then asks
// "did yolo write this?" answers no, forever. The correct attribution existed one apply
// earlier, in the record.
//
// So these tests are all SECOND-RENDER tests: the shape of the bug is entirely about what the
// next apply does to the previous record, and a single render cannot exhibit it.
//
// Every test renders into a t.TempDir() home. The record lands under THAT home's state dir
// (render.Target.ProvenanceDir), which is what keeps a real $HOME out of reach.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// applyToHome renders the owner pack into home with the given contributors in view — one
// `yolo host apply --assert`. Called twice by every test here, with a different contributor set,
// which is what models a pack being dropped from `packs`.
func applyToHome(t *testing.T, home string, owner *packload.Pack, contributors ...*packload.Pack) {
	t.Helper()
	overlays := packoverlay.Collect(append([]*packload.Pack{owner}, contributors...), false)
	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
}

// THE DEFECT, directly: apply with the contributor, drop it, apply again. The key must read
// as retired against the pack that last claimed it — never as `host`.
func TestDroppedPackKeyIsRetiredNotLaunderedToHost(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	dropme := overlayContributorPack(t, "dropme", map[string]any{"fileSuggestion": "run-fzf"})

	applyToHome(t, home, owner, dropme)
	first, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatal("no record after the first apply")
	}
	if got := first["fileSuggestion"]; got != "config-overlay:dropme" {
		t.Fatalf("precondition: the first apply must attribute the key to the contributing "+
			"pack, got %q\nrecord: %v", got, first)
	}

	// The DROP: the same owner applied with the contributor gone from view.
	applyToHome(t, home, owner)

	second, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatal("no record after the second apply")
	}
	if got := second["fileSuggestion"]; got == agentcfg.LayerHost {
		t.Errorf("LAUNDERED: a key yolo wrote for the dropped pack `dropme` now reads as %q — "+
			"the record has converted yolo's own output into user content, and every mechanism "+
			"that asks \"did yolo write this?\" will answer no from here on\nrecord: %v",
			got, second)
	}
	want := agentcfg.RetiredLayer(agentcfg.OverlayLayer("dropme"))
	if got := second["fileSuggestion"]; got != want {
		t.Errorf("fileSuggestion = %q, want %q — a retired key must keep naming the layer that "+
			"last claimed it, since nothing else in the system remembers\nrecord: %v",
			got, want, second)
	}
	// The KEY IS STILL IN THE FILE. Retirement is a record-side fact; dropping the value is a
	// separate, confirmed action (ruling R1/R3). A record naming a key the file does not have
	// would be its own misreport.
	if got := readRenderedJSON(t, home, ".acme/settings.json"); got["fileSuggestion"] != "run-fzf" {
		t.Errorf("the retirement pass changed the FILE: %#v — provenance is bookkeeping, and "+
			"removing a host key is a confirmed action this is not", got)
	}
}

// THE USER'S OWN KEY IS NOT RETIRED. This is the laundering in reverse and the direction that
// COSTS something: a prune reading the record would delete a key the user wrote by hand.
func TestUserOwnedKeyIsNeverRetired(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	// A key the user wrote themselves, in the file before yolo ever ran here.
	seedSurfaceFile(t, home, ".acme/settings.json", map[string]any{"userOwned": "keep me"})

	applyToHome(t, home, owner)
	first, _ := hostProvenance(t, home, "acme", "settings")
	if got := first["userOwned"]; got != agentcfg.LayerHost {
		t.Fatalf("precondition: a key only the file has must record as host, got %q", got)
	}
	applyToHome(t, home, owner)

	second, _ := hostProvenance(t, home, "acme", "settings")
	if got := second["userOwned"]; got != agentcfg.LayerHost {
		t.Errorf("userOwned = %q, want host — retiring the user's own key is the same laundering "+
			"reversed, and the direction that loses data: it hands a hand-written key to yolo, "+
			"which any prune reading the record would then delete\nrecord: %v", got, second)
	}
}

// A DEFAULT IS NOT RETIRED EITHER, and the reason is not symmetry. `defaults` is
// fill-if-absent: yolo writes the value once and never touches the key again, so from that
// moment the value is the user's to change — which is why rmwProvenance's own `host` pass
// already overwrites a defaults attribution for any key the file has.
func TestDroppedDefaultIsNotRetired(t *testing.T) {
	home := t.TempDir()
	// The owner declares `theme` as a DEFAULT. First apply fills it.
	applyToHome(t, home, overlayOwnerPack(t, ""))
	if got := readRenderedJSON(t, home, ".acme/settings.json"); got["theme"] != "system" {
		t.Fatalf("precondition: the default must have been filled, got %#v", got)
	}
	// Now the pack stops declaring the default at all.
	applyToHome(t, home, bareAcmePack(t))

	rec, _ := hostProvenance(t, home, "acme", "settings")
	if got := rec["theme"]; got != agentcfg.LayerHost {
		t.Errorf("theme = %q, want host — a filled default is the user's value from the moment "+
			"it lands (yolo never re-asserts it), so a dropped default is not yolo's output to "+
			"retire\nrecord: %v", got, rec)
	}
}

// A RE-ADDED PACK UN-RETIRES ITS KEY. Retirement is not a one-way stamp on the key: the pass
// only ever rewrites an attribution this render derived as `host`, so a live claim wins
// outright. Without this the record would report a key as orphaned while the pack that owns
// it is asserting it on every apply.
func TestReAddingThePackRestoresTheLiveAttribution(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	dropme := overlayContributorPack(t, "dropme", map[string]any{"fileSuggestion": "run-fzf"})

	applyToHome(t, home, owner, dropme)
	applyToHome(t, home, owner) // dropped: now retired
	rec, _ := hostProvenance(t, home, "acme", "settings")
	if _, retired := agentcfg.RetiredOf(rec["fileSuggestion"]); !retired {
		t.Fatalf("precondition: the key must be retired after the drop, got %q", rec["fileSuggestion"])
	}

	applyToHome(t, home, owner, dropme) // re-added

	rec, _ = hostProvenance(t, home, "acme", "settings")
	if got := rec["fileSuggestion"]; got != agentcfg.OverlayLayer("dropme") {
		t.Errorf("fileSuggestion = %q, want config-overlay:dropme — the pack is claiming the key "+
			"again, so reporting it as retired describes a state that ended\nrecord: %v", got, rec)
	}
}

// RETIREMENT IS STICKY. Without this the fix merely DELAYS the laundering by one apply: the
// second render records `retired:…`, and a third would find that label unrecognized, fall
// through to `host`, and lose the attribution exactly as before.
func TestRetirementSurvivesFurtherApplies(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	applyToHome(t, home, owner, overlayContributorPack(t, "dropme",
		map[string]any{"fileSuggestion": "run-fzf"}))

	want := agentcfg.RetiredLayer(agentcfg.OverlayLayer("dropme"))
	for i := 2; i <= 4; i++ {
		applyToHome(t, home, owner)
		rec, _ := hostProvenance(t, home, "acme", "settings")
		if got := rec["fileSuggestion"]; got != want {
			t.Fatalf("apply #%d: fileSuggestion = %q, want %q — the label must not nest into "+
				"retired:retired:… nor decay back to host\nrecord: %v", i, got, want, rec)
		}
	}
}

// FAIL SAFE #1 — NO PREVIOUS RECORD. A first-ever apply has nothing to consult, and the
// existing code's answer for that ("the file's content is the user's") must be exactly what it
// was: proving nothing may never manufacture a claim.
func TestFirstApplyWithNoRecordAttributesEverythingToHost(t *testing.T) {
	home := t.TempDir()
	seedSurfaceFile(t, home, ".acme/settings.json", map[string]any{"mystery": 1})

	applyToHome(t, home, overlayOwnerPack(t, ""))

	rec, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatal("no record")
	}
	if got := rec["mystery"]; got != agentcfg.LayerHost {
		t.Errorf("mystery = %q, want host — with no previous record there is nothing to prove, "+
			"and an unproven key is the user's\nrecord: %v", got, rec)
	}
}

// FAIL SAFE #2 — A CORRUPT RECORD CANNOT CLAIM A KEY. The record is a plain file in a state
// dir; garbage in it (a truncated write, a hand edit) must be inert rather than a lever for
// relabelling the user's keys as yolo's.
//
// Table-driven over the shapes that could plausibly slip through: an unrecognized layer name,
// a truncated `config-overlay:` with no pack, a bare `retired:` with no layer, a line with no
// tab at all, and outright binary.
func TestCorruptPreviousRecordCannotClaimAKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
	}{
		{"an unrecognized layer name", "mine\tsomething-invented\n"},
		{"config-overlay with no pack behind it", "mine\tconfig-overlay:\n"},
		{"a bare retired: with no layer", "mine\tretired:\n"},
		{"a line with no tab separator", "mine managed\n"},
		{"an empty key", "\tmanaged\n"},
		{"binary garbage", "\x00\x01\x02\xff\xfe not a record at all"},
		{"a claim on `host` itself", "mine\thost\n"},
		{"a claim on `defaults`", "mine\tdefaults\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			seedSurfaceFile(t, home, ".acme/settings.json", map[string]any{"mine": "user value"})
			// Plant the corrupt record where the writer's own re-read will find it.
			path := render.Host(home, nil).ProvenancePath("acme", "settings")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.record), 0o644); err != nil {
				t.Fatal(err)
			}

			applyToHome(t, home, overlayOwnerPack(t, ""))

			rec, found := hostProvenance(t, home, "acme", "settings")
			if !found {
				t.Fatal("the apply left no record")
			}
			if got := rec["mine"]; got != agentcfg.LayerHost {
				t.Errorf("mine = %q, want host — a record that cannot be trusted must prove "+
					"NOTHING; letting it claim a key means a corrupt file can relabel the "+
					"user's own config as yolo's output\nrecord: %v", got, rec)
			}
		})
	}
}

// FAIL SAFE #3 — AN UNREADABLE RECORD. Not corrupt content but an I/O failure (here: the
// record path is a DIRECTORY, so the read fails outright). Same rule, different failure mode,
// and worth its own case because it exercises the nil-return path rather than the parser.
func TestUnreadablePreviousRecordProvesNothing(t *testing.T) {
	home := t.TempDir()
	seedSurfaceFile(t, home, ".acme/settings.json", map[string]any{"mine": "user value"})
	path := render.Host(home, nil).ProvenancePath("acme", "settings")
	if err := os.MkdirAll(path, 0o755); err != nil { // a DIR where the record file belongs
		t.Fatal(err)
	}

	// The render still succeeds — a provenance failure must not fail the apply.
	applyToHome(t, home, overlayOwnerPack(t, ""))

	if got := readRenderedJSON(t, home, ".acme/settings.json"); got["mine"] != "user value" {
		t.Errorf("the render lost the user's key: %#v", got)
	}
}

// A MANAGED key the owner stops declaring retires too. The mechanism is not overlay-specific:
// `managed` and `computed` are force-written on every apply exactly as an overlay's keys are,
// so a key the owner drops from its own managed layer is the same orphan with a different
// owner. Pinned because the defect report only named the overlay case, and a fix that only
// handled that one would launder the others.
func TestDroppedManagedKeyIsRetiredToo(t *testing.T) {
	home := t.TempDir()
	applyToHome(t, home, overlayOwnerPack(t, "")) // declares managed telemetry:false
	first, _ := hostProvenance(t, home, "acme", "settings")
	if got := first["telemetry"]; got != agentcfg.LayerManaged {
		t.Fatalf("precondition: telemetry must record as managed, got %q", got)
	}

	// The owner stops declaring `telemetry` at all — same surface, no managed layer.
	applyToHome(t, home, bareAcmePack(t))

	rec, _ := hostProvenance(t, home, "acme", "settings")
	want := agentcfg.RetiredLayer(agentcfg.LayerManaged)
	if got := rec["telemetry"]; got != want {
		t.Errorf("telemetry = %q, want %q — a key the owner stops managing is yolo's own output "+
			"just as an overlay's is, and laundering it to `host` is the same defect\nrecord: %v",
			got, want, rec)
	}
}

// bareAcmePack owns acme/settings and declares NO layers — the "the pack stopped declaring
// this key" half of retirement, as against the "the pack left `packs`" half.
func bareAcmePack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path": "~/.acme/settings.json",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// seedSurfaceFile writes a surface file into a home before any apply — the user's own version
// of the file, which at the RMW notch IS the `host` layer.
func seedSurfaceFile(t *testing.T, home, rel string, content map[string]any) {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

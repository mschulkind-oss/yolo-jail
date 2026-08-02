package entrypoint

// hostprovenance_test.go pins HOST-SIDE PROVENANCE: `yolo apply --host` records which layer
// won each key, so `yolo config diff` at the host reports a MEASURED winner instead of one
// inferred from pack declarations.
//
// The bug this closes was not a missing feature but a confident wrong answer
// (docs/design/pack-config-collaboration.md §8, final bullet). The host render wrote no
// provenance record at all, whatever the surface's mode, so `config diff` had nothing to
// annotate from and guessed from what the packs declare — printing
//
//	fileSuggestion  contributed by fzf-overlay but managed won
//
// for a key whose value in the real ~/.claude/settings.json was the OVERLAY's, and which the
// owning pack does not declare at all (so there was no `managed` value to win). That is worse
// than an honest "unknown": it tells a user their overlay lost when it won.
//
// Every test here writes into a t.TempDir() home. The record lands under THAT home's state
// dir (see render.Target.ProvenanceDir), which is what makes the isolation real rather than
// hoped for — a record path derived from the process $HOME would land in the invoking user's
// live state dir.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// bareSurfacePack declares a surface with NO layers — no defaults, no managed — so a render
// of it attributes nothing and exercises the empty-record path.
func bareSurfacePack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "bare", "name": "settings", "codec": "json",
		"path": "~/.bare/settings.json",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "bare", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// hostProvenance reads the host-notch provenance record for one surface under home, as
// key → winning layer. found=false means no record file exists at all.
func hostProvenance(t *testing.T, home, agent, name string) (map[string]string, bool) {
	t.Helper()
	data, err := os.ReadFile(render.Host(home, nil).ProvenancePath(agent, name))
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, layer, ok := strings.Cut(line, "\t")
		if !ok || key == "" {
			continue
		}
		out[key] = layer
	}
	return out, true
}

// THE MEASUREMENT: a host render writes a record, and it attributes the overlay's key to the
// contributing pack rather than to the owner's managed layer.
//
// This is the §8 case exactly: `fileSuggestion` is contributed by an overlay and declared by
// nobody else, so "managed won" is not merely unhelpful, it is false.
func TestHostRenderWritesProvenanceNamingTheContributingPack(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"})
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false)

	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}

	prov, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatalf("the host render wrote NO provenance record — `config diff` at the host has " +
			"nothing to measure and falls back to guessing, which is the whole defect")
	}
	if got := prov["fileSuggestion"]; got != "config-overlay:acme-fzf" {
		t.Errorf("fileSuggestion attributed to %q, want config-overlay:acme-fzf — the overlay's "+
			"value is what landed in the file, so any other winner is a misreport\nrecord: %v",
			got, prov)
	}
	// And the owner's own managed key is attributed to managed, so a genuine loss is still
	// reportable as one.
	if got := prov["telemetry"]; got != "managed" {
		t.Errorf("telemetry attributed to %q, want managed\nrecord: %v", got, prov)
	}
}

// The other half of the same fact: an overlay that GENUINELY loses to the owner's managed
// layer is recorded as `managed`. Without this the fix could be "always say the overlay won",
// which is the same defect with the sign flipped.
func TestHostRenderProvenanceRecordsAGenuineOverlayLoss(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	// `telemetry` IS the owner's managed key, so the overlay must lose it; `theme` is only
	// the owner's default, so the overlay must win that one.
	pushy := overlayContributorPack(t, "pushy", map[string]any{"telemetry": true, "theme": "dark"})
	overlays := packoverlay.Collect([]*packload.Pack{owner, pushy}, false)

	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	prov, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatal("no provenance record")
	}
	if got := prov["telemetry"]; got != "managed" {
		t.Errorf("a contested MANAGED key must record managed as the winner, got %q", got)
	}
	if got := prov["theme"]; got != "config-overlay:pushy" {
		t.Errorf("a key the overlay beats (owner's default only) must record the overlay, got %q", got)
	}
	// The record must agree with the FILE — a record that disagrees with what landed is
	// worse than none, since the whole point is to stop guessing.
	got := readRenderedJSON(t, home, ".acme/settings.json")
	if got["telemetry"] != false || got["theme"] != "dark" {
		t.Errorf("the record and the file disagree: file=%#v record=%v", got, prov)
	}
}

// THE RECORD IS NOT IN THE USER'S CONFIG DIR, and not in the CWD.
//
// Both alternatives were live options. Beside the rendered file (~/.acme/.yolo-provenance/)
// would put yolo bookkeeping inside a dir the agent reads as config; a workspace-relative
// path would scatter records into whatever directory `apply --host` was invoked from,
// because render.Host leaves Workspace empty by definition.
func TestHostProvenanceLivesInTheStateDirNotTheConfigDir(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd) // a host apply runs from anywhere; nothing may land here

	owner := overlayOwnerPack(t, "")
	overlays := packoverlay.Collect([]*packload.Pack{owner}, false)
	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}

	want := filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"acme-settings.provenance")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no record at the state-dir path %s: %v", want, err)
	}
	// Nothing under the surface's own config dir except the surface itself.
	entries, err := os.ReadDir(filepath.Join(home, ".acme"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" {
			t.Errorf("the render left %q in the user's config dir — a real $HOME is not a jail "+
				"home, and yolo bookkeeping there is indistinguishable from config", e.Name())
		}
	}
	// And nothing at all in the invocation directory.
	if cwdEntries, err := os.ReadDir(cwd); err == nil && len(cwdEntries) != 0 {
		names := make([]string, 0, len(cwdEntries))
		for _, e := range cwdEntries {
			names = append(names, e.Name())
		}
		t.Errorf("the render wrote into the CWD: %v — a host target has no workspace, so a "+
			"workspace-relative sidecar path resolves against wherever the user happened to be",
			names)
	}
}

// OBSERVE writes nothing, including no record. A provenance record in dry-run would document
// a write that never happened, which is a stale record manufactured on purpose.
func TestHostRenderObserveWritesNoProvenance(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "")
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{"fileSuggestion": "run-fzf"})
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false)

	if _, err := RenderHostPack(owner, home, true, overlays); err != nil {
		t.Fatalf("RenderHostPack observe: %v", err)
	}
	if prov, found := hostProvenance(t, home, "acme", "settings"); found {
		t.Errorf("observe posture wrote a provenance record (%v) — it would describe a render "+
			"that did not happen", prov)
	}
}

// A PROVENANCE WRITE FAILURE MUST NOT FAIL THE APPLY. The surface is already written by the
// time the record is attempted, so aborting would report a failure for a render that
// succeeded — and at the host notch there is no A12 fatal-boot equivalent to justify it.
// The failure is announced, not swallowed.
func TestHostProvenanceWriteFailureDoesNotFailTheApply(t *testing.T) {
	home := t.TempDir()
	// Make the record's own parent un-creatable by planting a FILE where the state dir's
	// leaf must be a directory. The surface path is unaffected, so the render still succeeds.
	blocked := filepath.Join(home, ".local", "share", "yolo-jail")
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	owner := overlayOwnerPack(t, "")
	results, err := RenderHostPack(owner, home, false, nil)
	if err != nil {
		t.Fatalf("a provenance write failure must NOT fail the apply: %v", err)
	}
	var rendered bool
	for _, r := range results {
		if r.Surface == "acme/settings" && r.Action == "rendered" {
			rendered = true
		}
	}
	if !rendered {
		t.Errorf("the surface must still be reported as rendered: %+v", results)
	}
	// The surface itself landed — the render is what matters, the record is bookkeeping.
	if got := readRenderedJSON(t, home, ".acme/settings.json"); got["telemetry"] != false {
		t.Errorf("the surface did not render: %#v", got)
	}
}

// The host record's GRANULARITY matches the jail's, because ONE reader serves both. The
// host path derives provenance by replaying rmw write order while the jail path reads it off
// Compose's fold — two implementations of "which layer won", which is exactly the setup where
// a per-key vs per-subtree disagreement hides. Pinned on the case that would expose it: an
// overlay contributing a SIBLING under a parent the owner also manages. Compose attributes
// per TOP-LEVEL key, so both must say `managed`.
func TestHostProvenanceGranularityMatchesTheJail(t *testing.T) {
	surface, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":    "~/.acme/settings.json",
		"managed": map[string]any{"prefs": map[string]any{"owned": true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: surface}}}}
	contributor := overlayContributorPack(t, "pushy", map[string]any{
		"prefs": map[string]any{"sibling": true}})

	// The JAIL path (stateful → Compose → Result.Provenance).
	e, _ := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{owner, contributor})
	jailData, err := os.ReadFile(prismProvenancePath(e, "acme", "settings"))
	if err != nil {
		t.Fatalf("read the jail record: %v", err)
	}
	// The HOST path (rmw → rmwProvenance).
	home := t.TempDir()
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false)
	if _, err := RenderHostPack(owner, home, false, overlays); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	hostProv, found := hostProvenance(t, home, "acme", "settings")
	if !found {
		t.Fatal("no host record")
	}

	if got := strings.TrimSpace(string(jailData)); got != "prefs\tmanaged" {
		t.Errorf("jail record = %q, want the top-level key attributed to managed", got)
	}
	if got := hostProv["prefs"]; got != "managed" {
		t.Errorf("host record attributes prefs to %q, want managed — the two notches must agree "+
			"on granularity, since one `config diff` reader annotates from both", got)
	}
}

// EMPTY IS DISTINGUISHABLE FROM NEVER-RENDERED. A surface that renders and attributes no
// keys writes an EMPTY record rather than skipping, so a reader can tell "rendered, nothing
// to attribute" from "no render has happened here". Conflating them is how an unrendered
// surface gets reported as one where every overlay lost.
func TestHostProvenanceEmptyRecordIsWrittenNotSkipped(t *testing.T) {
	home := t.TempDir()
	// A surface with NO layers at all: no defaults, no managed, no overlays, and an absent
	// file — so there is nothing whatever to attribute.
	bare := bareSurfacePack(t)
	if _, err := RenderHostPack(bare, home, false, nil); err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	path := render.Host(home, nil).ProvenancePath("bare", "settings")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("an empty record must still be WRITTEN, so a reader can tell it from "+
			"never-rendered: %v", err)
	}
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("want an empty record, got:\n%s", data)
	}
	// The two states read differently: this surface has a file, a never-rendered one has none.
	if _, found := hostProvenance(t, home, "bare", "nosuchsurface"); found {
		t.Error("a surface that never rendered must have NO record file")
	}
}

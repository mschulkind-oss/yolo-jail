package agentcfg

// retiredlayer_test.go pins the PROVENANCE VOCABULARY the anti-laundering fix rests on
// (docs/plans/host-pack-drop-cleanup.md ruling R3). Three properties, and each one is a way
// the fix could be silently wrong:
//
//   - ROUND-TRIP. RetiredLayer/RetiredOf must be inverses, because the label is the ONLY place
//     a dropped pack's name survives — nothing declares the key any more, so a label that
//     cannot be decoded loses the fact entirely.
//   - THE CLOSED SET. LayerAsserted is the whole filter a previous record passes through
//     before it may claim a key. If it accepted anything outside the force-written layers, a
//     corrupt record could relabel the user's own config as yolo's output.
//   - RECORD FORMAT. ParseProvenanceRecord is the inverse of ProvenanceLines and is shared by
//     the writer's re-read and the CLI reader, so a drift here makes the two disagree about
//     what is in a file they both read.

import "testing"

// The round trip, and the shape that makes it safe: a retired label is not equal to the layer
// it wraps, so no reader mistakes an orphan for a live claim.
func TestRetiredLayerRoundTrips(t *testing.T) {
	for _, layer := range []string{
		OverlayLayer("dropme"),
		LayerManaged,
		LayerComputed,
		// A pack name with a colon in it: the label splits on the FIRST separator only, so the
		// rest — colons and all — comes back verbatim.
		OverlayLayer("weird:name"),
	} {
		label := RetiredLayer(layer)
		if label == layer {
			t.Errorf("RetiredLayer(%q) == the layer itself — a retired key would then read as a "+
				"live claim by the very layer that stopped claiming it", layer)
		}
		if label == LayerHost {
			t.Errorf("RetiredLayer(%q) collides with `host` — the one label it must never be, "+
				"since `host` means the user wrote the key", layer)
		}
		got, retired := RetiredOf(label)
		if !retired || got != layer {
			t.Errorf("RetiredOf(RetiredLayer(%q)) = (%q, %v), want (%q, true) — the label carries "+
				"the only surviving record of whose key this was", layer, got, retired, layer)
		}
	}
}

// A non-retired label decodes as not-retired. Two cases matter beyond the obvious: `retired:`
// with nothing behind it (a truncated write) is NOT a retirement, and a plain layer name is not
// one either.
func TestRetiredOfRejectsNonRetiredLabels(t *testing.T) {
	for _, label := range []string{
		LayerHost, LayerManaged, LayerComputed, LayerDefaults,
		OverlayLayer("live"),
		"retired", // the bare word, no separator
		"retired:",
		"",
		"not-a-layer",
	} {
		if last, retired := RetiredOf(label); retired {
			t.Errorf("RetiredOf(%q) reported a retirement of %q — a label that is not a "+
				"retirement must not become one, or a corrupt record can announce orphans that "+
				"do not exist", label, last)
		}
	}
}

// THE CLOSED SET. What LayerAsserted accepts is exactly the force-written layers; everything
// else — including the two exclusions that are deliberate rather than incidental — is rejected.
func TestLayerAssertedIsTheForceWrittenLayersOnly(t *testing.T) {
	for _, layer := range []string{LayerManaged, LayerComputed, OverlayLayer("acme-fzf")} {
		if !LayerAsserted(layer) {
			t.Errorf("LayerAsserted(%q) = false — this layer is re-asserted on every render, so "+
				"a key it wrote is yolo's output and must be retirable rather than laundered", layer)
		}
	}
	for _, tc := range []struct{ layer, why string }{
		{LayerHost, "`host` is the USER's key; retiring it hands their config to yolo, which is " +
			"the laundering reversed and the direction that loses data"},
		{LayerDefaults, "`defaults` is fill-if-absent — yolo writes it once and never re-asserts " +
			"it, so the value is the user's from the moment it lands"},
		{"config-overlay:", "a truncated overlay label names no pack; honoring it would let a " +
			"partial write claim a key"},
		{"config-overlay", "the bare prefix with no separator is not a contribution"},
		{"retired:config-overlay:x", "a retired label is handled by RetiredOf, not by claiming " +
			"afresh — routing it here would nest into retired:retired:x"},
		{"", "an empty layer proves nothing"},
		{"managed ", "a token with stray whitespace is not the exact token; the vocabulary is " +
			"exact strings precisely so garbage cannot spell one by accident"},
		{"MANAGED", "layer tokens are lowercase; a case variant is not the token"},
		{"transform", "the transform layer exists only in the FOLD (Compose); rmw has no " +
			"transform step, so a record naming it did not come from the notch that retires"},
	} {
		if LayerAsserted(tc.layer) {
			t.Errorf("LayerAsserted(%q) = true, want false — %s", tc.layer, tc.why)
		}
	}
}

// ParseProvenanceRecord is the inverse of ProvenanceLines, and its two contract points are the
// ones a caller's fail-safe depends on: a parsable input yields a NON-NIL map (so "rendered and
// attributed nothing" stays distinguishable from "no record"), and a malformed line is SKIPPED
// rather than admitted as a key.
func TestParseProvenanceRecordRoundTripsAndSkipsGarbage(t *testing.T) {
	want := map[string]string{
		"telemetry":      LayerManaged,
		"fileSuggestion": OverlayLayer("acme-fzf"),
		"legacyKey":      RetiredLayer(OverlayLayer("dropme")),
	}
	lines := (&Result{Provenance: want}).ProvenanceLines()
	var data []byte
	for _, l := range lines {
		data = append(data, l...)
		data = append(data, '\n')
	}
	got := ParseProvenanceRecord(data)
	if len(got) != len(want) {
		t.Fatalf("round trip lost entries: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("round trip: %q = %q, want %q", k, got[k], v)
		}
	}

	// Garbage: no tab, an empty key, and a trailing partial line. None may become an entry, and
	// the intact line beside them must still parse — a record is repaired line by line rather
	// than rejected wholesale, so one bad byte cannot relaunder a whole surface.
	mixed := ParseProvenanceRecord([]byte("no-tab-here\n\tempty-key\ngood\tmanaged\npartial"))
	if len(mixed) != 1 || mixed["good"] != LayerManaged {
		t.Errorf("garbage lines were admitted or the good line was lost: %v", mixed)
	}

	// EMPTY INPUT IS A MEASUREMENT, NOT AN ABSENCE: non-nil, zero entries.
	if empty := ParseProvenanceRecord(nil); empty == nil {
		t.Error("ParseProvenanceRecord(nil) returned nil — callers distinguish an empty record " +
			"(rendered, nothing attributed) from an absent one by nil-ness, and the writer emits " +
			"an empty file precisely to keep those two apart")
	}
}

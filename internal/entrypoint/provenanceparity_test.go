package entrypoint

// provenanceparity_test.go is the SHARED-CORPUS PARITY TABLE for the two "which layer won"
// derivations (proposed-fixes-open-findings.md §8).
//
// There are two of them, on purpose, and unification is RULED-DEFERRED until a third exists:
//
//	rmwProvenance  (internal/entrypoint/prism.go)  — replays RMW WRITE ORDER
//	Compose        (internal/agentcfg/compose.go)  — folds the LAYER STACK
//
// An RMW render has no layer fold to read a winner off (precedence *is* write order), and a
// fold has no write order to replay — so neither can be expressed in the other without
// inventing a mechanism it does not have. What they DO owe each other is the same ANSWER,
// because one `yolo config diff` reader annotates from both records.
//
// Why a table and not more of the same one-off tests. Parity was previously pinned for
// GRANULARITY only (TestHostProvenanceGranularityMatchesTheJail, one fixture: an overlay
// sibling under a managed parent). That catches a per-key-vs-per-subtree disagreement in
// SHAPE. It cannot catch a disagreement in OUTCOME — the two derivations naming different
// winners for the same layer set — which is the failure that actually reaches a user, as a
// `config diff` that says the opposite thing at the two notches. The table runs ONE corpus of
// layer/key fixtures through BOTH implementations and compares the records key for key.
//
// THE CORRESPONDENCE THE TABLE RESTS ON: `host` means "the user's own version of this file"
// at both notches, but it ARRIVES differently — in the jail as a /ctx mount handed to Compose
// as HostBytes, at the host notch as the existing file RMW reads in place. So one fixture
// field (`host`) feeds the jail as host bytes and the host as pre-existing file content. Any
// other pairing would compare two different questions.
//
// DIVERGENCE IS RECORDED, NOT PAPERED OVER. Two fixtures below genuinely disagree, and in
// both the disagreement is in the RENDER, not in the derivation: each record is truthful
// about the file its own notch produced. A case that declares `wantHost` MUST also declare
// `why`, so a future divergence cannot be added silently — the test itself refuses an
// undocumented one.
//
// WHAT THE CORPUS DELIBERATELY CANNOT COVER: retirement (agentcfg.RetiredLayer, the
// anti-laundering pass in rmwProvenance). Every fixture here is a FIRST render into a fresh
// home, so there is no previous record and rmwProvenance's `previous` is nil — which is
// exactly the parameterization under which parity is claimable. Retirement is not a fixture
// this table is missing; it is a property of the SECOND render, and it has no counterpart in
// Compose to be at parity WITH: a fold renders the file from the layers it has, so a layer
// that stops claiming a key simply does not contribute it and the key is not in the rendered
// file at all. There is nothing to launder in a fold, and so nothing to retire.
//
// Which makes the boundary itself the thing worth pinning here, and it is asserted below
// (parityRecords: no host record may carry a retired label): the retirement pass must be
// unreachable on a first render, or the two derivations would diverge on the shared corpus
// through a mechanism only one of them has. Do NOT add a retirement fixture to this corpus —
// it belongs in provenanceretire_test.go, which renders twice.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// parityCase is one corpus entry: a surface's declared layers plus the outside layers, and
// the record BOTH derivations must produce from them.
type parityCase struct {
	// name identifies the fixture in failures.
	name string
	// defaults / managed are the surface's own declared layers.
	defaults map[string]any
	// managed is the surface's asserted layer.
	managed map[string]any
	// host is the user's own version of the file: the jail reads it as HostBytes (a /ctx
	// mount), the host notch as the file already on disk. See the file header.
	host map[string]any
	// computed is yolo's per-boot dynamic layer.
	computed map[string]any
	// overlays are other packs' config-overlay contributions, in fold order.
	overlays []agentcfg.Overlay
	// want is the record both derivations must produce.
	want map[string]string
	// wantHost overrides `want` for the HOST derivation when the two renders genuinely
	// differ. Requires `why`.
	wantHost map[string]string
	// why documents a declared divergence. Enforced non-empty whenever wantHost is set, so
	// "the two disagree" can never become a thing the table quietly tolerates.
	why string
}

// parityCorpus is the shared fixture set. Each entry is chosen because the two models could
// plausibly disagree on it — a case where both obviously agree proves nothing about a
// write-order/fold split.
func parityCorpus() []parityCase {
	return []parityCase{
		// ── The precedence ladder, one rung at a time ──────────────────────────────
		{
			name:    "managed alone",
			managed: map[string]any{"telemetry": false},
			want:    map[string]string{"telemetry": "managed"},
		},
		{
			// An overlay key the owner does not declare at all. This is the §8 defect
			// fixture: reporting `managed` here is not merely unhelpful, it is false —
			// there is no managed value to have won.
			name:     "overlay key the owner never declares",
			managed:  map[string]any{"telemetry": false},
			overlays: []agentcfg.Overlay{{Pack: "acme-fzf", Data: map[string]any{"fileSuggestion": "run-fzf"}}},
			want: map[string]string{
				"telemetry":      "managed",
				"fileSuggestion": "config-overlay:acme-fzf",
			},
		},
		{
			// CONTESTED: managed also sets the key, so managed wins at both notches. The
			// other half of the same fact — without it, "always say the overlay won" would
			// pass, which is the same defect with the sign flipped.
			name:     "overlay key managed ALSO sets",
			managed:  map[string]any{"telemetry": false},
			overlays: []agentcfg.Overlay{{Pack: "pushy", Data: map[string]any{"telemetry": true}}},
			want:     map[string]string{"telemetry": "managed"},
		},
		{
			name:     "overlay beats the owner's default",
			defaults: map[string]any{"theme": "system"},
			overlays: []agentcfg.Overlay{{Pack: "pushy", Data: map[string]any{"theme": "dark"}}},
			want:     map[string]string{"theme": "config-overlay:pushy"},
		},
		{
			// Later pack wins, the rule every other multi-pack kind already uses. A fold
			// gets this from list order; write order gets it from loop order — the same
			// answer by two routes, which is exactly the kind of thing that drifts.
			name: "two overlays, later wins",
			overlays: []agentcfg.Overlay{
				{Pack: "first", Data: map[string]any{"k": 1}},
				{Pack: "second", Data: map[string]any{"k": 2}},
			},
			want: map[string]string{"k": "config-overlay:second"},
		},
		{
			name:     "defaults survive when nothing above them sets the key",
			defaults: map[string]any{"theme": "system"},
			want:     map[string]string{"theme": "defaults"},
		},
		{
			// FILL-IF-ABSENT, corrected. The RMW writer visits defaults LAST but only fills
			// absent keys, so replaying write order naively would attribute a key the file
			// already had to `defaults`. The fold has no such hazard. This is the sharpest
			// write-order-specific trap in the corpus.
			name:     "the file's value beats a default it collides with",
			defaults: map[string]any{"theme": "system"},
			host:     map[string]any{"theme": "solarized"},
			want:     map[string]string{"theme": "host"},
		},
		{
			name:    "a key present in the file and in NO yolo layer",
			managed: map[string]any{"telemetry": false},
			host:    map[string]any{"userOwned": "keep me"},
			want: map[string]string{
				"telemetry": "managed",
				"userOwned": "host",
			},
		},
		{
			name:    "managed beats the file",
			managed: map[string]any{"telemetry": false},
			host:    map[string]any{"telemetry": true},
			want:    map[string]string{"telemetry": "managed"},
		},
		{
			name:     "overlay beats the file",
			host:     map[string]any{"k": "user"},
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"k": "ov"}}},
			want:     map[string]string{"k": "config-overlay:ov"},
		},

		// ── Nesting: the case a write-order model could most plausibly get wrong ────
		{
			// THE GRANULARITY CASE. Compose attributes per TOP-LEVEL key, so an overlay
			// contributing a SIBLING under a parent the owner manages reads as `managed` —
			// a per-subtree host record would say `config-overlay:pushy` and one `config
			// diff` reader would then print two different answers for one key.
			name:     "overlay sibling under a managed parent",
			managed:  map[string]any{"prefs": map[string]any{"owned": true}},
			overlays: []agentcfg.Overlay{{Pack: "pushy", Data: map[string]any{"prefs": map[string]any{"sibling": true}}}},
			want:     map[string]string{"prefs": "managed"},
		},
		{
			// Same shape with the FILE as the other contributor under the parent, plus an
			// untouched top-level sibling — so a model that over-claimed the parent would
			// also have to be caught not to under-claim `other`.
			name:    "managed nested under a parent the file also populates",
			managed: map[string]any{"prefs": map[string]any{"owned": true}},
			host:    map[string]any{"prefs": map[string]any{"mine": 1}, "other": 2},
			want: map[string]string{
				"prefs": "managed",
				"other": "host",
			},
		},
		{
			name:     "overlay sibling under a parent with NO managed layer",
			host:     map[string]any{"prefs": map[string]any{"mine": 2}},
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"prefs": map[string]any{"sib": 3}}}},
			want:     map[string]string{"prefs": "config-overlay:ov"},
		},
		{
			name:     "defaults nested under a parent the file populates",
			defaults: map[string]any{"prefs": map[string]any{"d": 1}},
			host:     map[string]any{"prefs": map[string]any{"mine": 2}},
			want:     map[string]string{"prefs": "host"},
		},

		// ── The computed layer ─────────────────────────────────────────────────────
		{
			// An OBJECT-valued computed key is a dynamic managed table at both notches:
			// regenerateManagedTables rewrites the block wholesale, and the fold merges it
			// above the file. Both must say `computed`.
			name:     "computed dynamic table",
			computed: map[string]any{"mcpServers": map[string]any{"srv": map[string]any{"command": "x"}}},
			want:     map[string]string{"mcpServers": "computed"},
		},
		{
			name:     "computed table beats a stale block in the file",
			host:     map[string]any{"mcpServers": map[string]any{"stale": map[string]any{"command": "old"}}},
			computed: map[string]any{"mcpServers": map[string]any{"srv": map[string]any{"command": "x"}}},
			want:     map[string]string{"mcpServers": "computed"},
		},
		{
			// Managed still wins the floor over the regenerated table.
			name:     "managed beats computed on the same key",
			managed:  map[string]any{"mcpServers": map[string]any{"pinned": 1}},
			computed: map[string]any{"mcpServers": map[string]any{"srv": map[string]any{"command": "x"}}},
			want:     map[string]string{"mcpServers": "managed"},
		},
		{
			// DECLARED DIVERGENCE #1 — a RENDER difference, faithfully recorded twice.
			name:     "computed SCALAR key",
			computed: map[string]any{"flag": true},
			want:     map[string]string{"flag": "computed"},
			wantHost: map[string]string{},
			why: "the two RENDERS differ, and each record is truthful about its own file. " +
				"regenerateManagedTables writes only OBJECT-valued computed keys (a dynamic " +
				"table), so at the host notch a scalar computed key is never written at all " +
				"and attributing it would document a write that did not happen — rmwProvenance " +
				"says so in as many words. Compose folds the whole computed layer, scalars " +
				"included, so the jail file HAS the key and records it. Fixing the parity means " +
				"fixing the render (should rmw write scalar computed keys?), not the record.",
		},

		// ── Tombstones ─────────────────────────────────────────────────────────────
		{
			// A managed null asserts no value, so neither derivation attributes the key to
			// managed — and the file's own value keeps the attribution.
			name:    "managed null tombstone leaves the file's attribution",
			managed: map[string]any{"gone": nil},
			host:    map[string]any{"gone": "was here"},
			want:    map[string]string{"gone": "host"},
		},
		{
			name:    "managed nested tombstone still claims its top-level parent",
			managed: map[string]any{"prefs": map[string]any{"gone": nil, "owned": true}},
			host:    map[string]any{"prefs": map[string]any{"gone": 1, "keep": 2}},
			want:    map[string]string{"prefs": "managed"},
		},
		{
			// DECLARED DIVERGENCE #2 — again a RENDER difference, recorded honestly.
			name:     "overlay null tombstone",
			host:     map[string]any{"k": "user"},
			overlays: []agentcfg.Overlay{{Pack: "ov", Data: map[string]any{"k": nil}}},
			want:     map[string]string{},
			wantHost: map[string]string{"k": "config-overlay:ov"},
			why: "the two RENDERS differ. Compose honors RFC-7386 in the fold: a null in a " +
				"pre-transform layer DELETES the key, and provenance deletes the entry with " +
				"it, so the jail attributes nothing because the jail file HAS nothing. " +
				"applyRMWLayer has no tombstone step — it force-writes the literal null — so " +
				"the host file holds `k: null` and the overlay genuinely is the layer that " +
				"put it there. Both records match their own file; the mechanisms differ.",
		},

		// ── The empty case: a measurement, not an absence ───────────────────────────
		{
			// A surface with no layers whatever attributes nothing at BOTH notches, and
			// (see the assertion below) the host still WRITES the empty record — "rendered,
			// nothing to attribute" has to stay distinguishable from "never rendered here".
			name: "no layers at all",
			want: map[string]string{},
		},
	}
}

// parityRecords runs ONE corpus fixture through BOTH derivations and returns the two records.
//
// Jail: renderSurfaceStatefulSurface → ComposeStateful → Compose → Result.Provenance, read
// back off the persisted sidecar path so the test measures what a reader would see rather
// than an in-memory value the writer might not have persisted.
//
// Host: renderSurfaceRMWSurface with hostTarget set → rmwProvenance → the state-dir record.
// The fixture's `host` map is written to the surface path FIRST, because at this notch the
// pre-existing file IS the host layer.
func parityRecords(t *testing.T, tc parityCase) (jail, host map[string]string) {
	t.Helper()

	surface := manifest.Surface{
		Agent: "parity", Name: "settings", Codec: "json",
		Path: "~/.parity/settings.json",
	}
	// Assigned conditionally: the fields are `any`, so a nil map[string]any would box a
	// TYPED nil and read as a present-but-empty layer rather than an absent one.
	if tc.defaults != nil {
		surface.Defaults = tc.defaults
	}
	if tc.managed != nil {
		surface.Managed = tc.managed
	}
	var hostBytes []byte
	if tc.host != nil {
		var err error
		if hostBytes, err = json.Marshal(tc.host); err != nil {
			t.Fatal(err)
		}
	}

	// ── The JAIL derivation: the layer fold.
	var jailErr bytes.Buffer
	ej := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &jailErr}
	if _, err := renderSurfaceStatefulSurface(ej, surface, hostBytes, tc.computed, tc.overlays); err != nil {
		t.Fatalf("jail render: %v", err)
	}
	jail = readProvenanceFile(t, prismProvenancePath(ej, "parity", "settings"))

	// ── The HOST derivation: replayed write order.
	eh := &Env{Home: t.TempDir(), Vars: map[string]string{}, hostTarget: true}
	surfacePath := filepath.Join(eh.Home, ".parity", "settings.json")
	if hostBytes != nil {
		if err := os.MkdirAll(filepath.Dir(surfacePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(surfacePath, hostBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := renderSurfaceRMWSurface(eh, surface, tc.computed, tc.overlays); err != nil {
		t.Fatalf("host render: %v", err)
	}
	// hostProvenance (hostprovenance_test.go) reads through render.Host's own path, which is
	// what makes "the record is where a reader looks for it" part of the measurement.
	rec, found := hostProvenance(t, eh.Home, "parity", "settings")
	if !found {
		t.Fatal("the host render wrote NO provenance record — an EMPTY record must still be " +
			"written, so a reader can tell it from never-rendered")
	}
	// THE PARITY PRECONDITION, asserted rather than assumed (see the file header). This home is
	// fresh, so there is no previous record, so the retirement pass must be a no-op. A retired
	// label appearing here would mean the pass fires on a FIRST render — which would make the
	// host derivation diverge from the fold through a mechanism the fold does not have, and
	// every comparison below would be measuring the wrong thing.
	for k, layer := range rec {
		if last, retired := agentcfg.RetiredOf(layer); retired {
			t.Fatalf("the host record retired %q to %q on a FIRST render into a fresh home. "+
				"Retirement is a second-render property with no counterpart in Compose; firing it "+
				"here breaks the premise this whole table rests on", k, last)
		}
	}
	return jail, rec
}

// readProvenanceFile parses a "key\tlayer" record. An absent file is a failure, not an empty
// map: both notches write even an empty record on purpose.
func readProvenanceFile(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provenance record %s: %v", path, err)
	}
	out := map[string]string{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		key, layer, ok := bytes.Cut(line, []byte("\t"))
		if !ok || len(key) == 0 {
			continue
		}
		out[string(key)] = string(layer)
	}
	return out
}

// TestProvenanceParityAcrossBothDerivations is the table: one corpus, both implementations,
// compared key for key. A divergence in OUTCOME fails here — the previous parity test could
// only catch a divergence in SHAPE.
func TestProvenanceParityAcrossBothDerivations(t *testing.T) {
	for _, tc := range parityCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			// An undocumented divergence is a test-authoring error, caught before the
			// comparison so nobody can quiet this table by widening it.
			if tc.wantHost != nil && tc.why == "" {
				t.Fatalf("fixture %q declares wantHost without `why` — a divergence between the "+
					"two derivations has to be explained at the fixture, not asserted silently", tc.name)
			}
			wantJail := tc.want
			wantHost := tc.want
			if tc.wantHost != nil {
				wantHost = tc.wantHost
			}

			jail, host := parityRecords(t, tc)

			assertProvenance(t, "jail (Compose fold)", jail, wantJail)
			assertProvenance(t, "host (rmwProvenance write-order replay)", host, wantHost)

			// The parity claim itself, stated separately from the two per-side checks: for
			// every fixture that does NOT declare a divergence, the two records must be
			// equal as maps. Asserted even though both already matched `want`, because it
			// is the property the two implementations owe each other — a future fixture
			// added with a wrong shared `want` would still be caught agreeing or not.
			if tc.wantHost == nil && !sameProvenance(jail, host) {
				t.Errorf("the two derivations disagree:\n  jail = %v\n  host = %v\n"+
					"one `yolo config diff` reader annotates from both records, so a "+
					"per-key disagreement makes it report opposite answers at the two notches",
					jail, host)
			}
			if tc.wantHost != nil && sameProvenance(jail, host) {
				t.Errorf("fixture %q declares a divergence (%s) but the two records now AGREE "+
					"(%v) — the divergence was closed; drop wantHost/why rather than leaving a "+
					"stale explanation in the corpus", tc.name, tc.why, jail)
			}
		})
	}
}

func assertProvenance(t *testing.T, side string, got, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: key %q attributed to %q, want %q\nrecord: %v", side, k, got[k], w, got)
		}
	}
	for k, g := range got {
		if _, expected := want[k]; !expected {
			t.Errorf("%s: unexpected attribution %q → %q\nrecord: %v", side, k, g, got)
		}
	}
}

func sameProvenance(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

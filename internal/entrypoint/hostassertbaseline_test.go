package entrypoint

// hostassertbaseline_test.go pins THE BYTES a pre-retirement `assert` apply left on a home —
// the starting state of §11's "switching to `own` keeps every key and value" criterion
// (docs/design/config-ownership-and-promotion.md §11), and, since the `assert` retirement
// (OQ-CO14), the state every home yolo asserted into is LEFT IN: the unset key is `none` now,
// and the ruling leaves such a file exactly as `assert` last rendered it.
//
// `assert` no longer renders, so the home is built by renderAsRetiredAssert
// (retiredassert_test.go): the rmw arm `assert` coerced every surface through, which an owned
// host still runs for a surface its pack declares `rmw`. This file is that helper's premise —
// the bytes it leaves are the `assert` baseline below, stated in full.
//
// It stays a BYTE golden even though the criterion asks for keys and values (OQ-CO12), and
// that is not an oversight: §11's LAST bullet was a separate, unrelaxed requirement — a host
// apply under `assert` leaves an undeclared key byte-identical — and this file is what measured
// it. The criterion that moved governs the SWITCH.
//
// Why a byte golden rather than key assertions. §11's criterion is a claim about the FILE, and
// the class it has to catch is the one §6.3.1 measured on the engine as it then stood: adoption
// (`ComposeStateful`'s first-migration branch) dropped every leaf under a top-level object the
// pure render holds, wholesale, while `rmw` DEEP-MERGES the same object and keeps it
// (`applyRMWLayer`). So a user's `permissions.ask` sitting beside yolo's managed
// `permissions.defaultMode` survives here and would not have survived an owned render built on
// that rule. A test asserting "permissions is present" passes in both worlds; only the VALUE
// tells them apart, and a byte golden states the value without having to enumerate which one.
//
// The blanket drop is GONE — adoption now narrows in two passes, `dropComputedTables` then the
// shared leaf-level `narrowOverlay` — so this golden and its `own` twin agree. What the golden
// does NOT establish is the criterion in general: it is one JSON fixture whose keys are already
// in the order a composing encoder emits, so it cannot see the axes on which a home that is not
// changes anyway. Those are hostownedkeysandvalues_test.go's, and §11 lists them.
//
// The fixture surface declares NO mode, i.e. `stateful` (manifest.Surface.ResolvedMode's
// default), because that was the coercion case: `assert` ran `rmw` alone and rendered a surface
// declaring `stateful` through it. It is also the case `own` composes whole, so the same pack
// reads both sides of the switch.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point this at a real one: it is an
// --assert, it writes the surface, and it leaves a provenance record under that home's state dir.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// adoptionBaselinePack owns one JSON surface whose `managed` layer is an OBJECT with a single
// leaf — the `permissions.defaultMode` shape §6.3.1 names — so the file can hold a sibling leaf
// under the same parent that yolo does not declare.
func adoptionBaselinePack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":     "~/.acme/settings.json",
		"defaults": map[string]any{"theme": "system"},
		"managed":  map[string]any{"permissions": map[string]any{"defaultMode": "default"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// assertBaselineHome seeds a home with a pre-existing agent-written file and applies once as the
// retired `assert` did, so what comes back is a home yolo ASSERTED INTO before the retirement —
// the state §11's criterion is about, and OQ-CO14's face 2. The seeded file carries both classes
// that have to survive: an undeclared top-level key (`apiKeyHelper`) and an undeclared LEAF under
// a declared object (`permissions.ask`).
func assertBaselineHome(t *testing.T) (home, path string) {
	t.Helper()
	home, path = seedAdoptionHome(t)
	renderAsRetiredAssert(t, adoptionBaselinePack(t), home, nil, nil)
	return home, path
}

// seedAdoptionHome is assertBaselineHome before its apply: a home holding only the agent's own
// file, which yolo has never written.
func seedAdoptionHome(t *testing.T) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, ".acme", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "apiKeyHelper": "/usr/local/bin/acme-key.sh",
  "permissions": {
    "ask": [
      "Bash(rm:*)"
    ]
  }
}
`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

// THE BASELINE. These exact bytes are what an `assert` home holds — and keeps, since the
// retirement leaves it alone; §11's criterion is that
// switching it to `own` keeps every key and value in them, and for THIS fixture — canonical on
// every axis the criterion leaves free — that is the same as keeping the bytes. Written out in
// full rather than computed, so
// the comparison is against a STATED expectation and not against whatever the engine happens
// to emit on both sides of a change.
const assertBaselineBytes = `{
  "apiKeyHelper": "/usr/local/bin/acme-key.sh",
  "permissions": {
    "ask": [
      "Bash(rm:*)"
    ],
    "defaultMode": "default"
  },
  "theme": "system"
}
`

// A host apply as `assert` ran it left the file at the baseline: yolo's declared keys asserted,
// and every key the agent owns — including a LEAF under a declared object — byte-identical. It is
// what every test seeding through assertBaselineHome, or renderAsRetiredAssert, starts from.
// It was TestHostAssertLeavesTheAdoptionBaseline, rendering under the contract itself.
func TestTheRetiredAssertLeftTheAdoptionBaseline(t *testing.T) {
	_, path := assertBaselineHome(t)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	if string(got) != assertBaselineBytes {
		t.Errorf("the `assert` baseline moved.\n got:\n%s\nwant:\n%s\n\nThis is the file "+
			"docs/design/config-ownership-and-promotion.md §11 says an `own` render must "+
			"reproduce — and since this fixture is already canonical, byte for byte. If it moved, "+
			"either the rmw arm changed (it still runs for every `rmw`-declared surface an owned "+
			"host renders) or renderAsRetiredAssert no longer renders what `assert` did — fix "+
			"that, not this golden.", got, assertBaselineBytes)
	}
}

// AND THE RMW ARM IS A FIXED POINT. A second render over the first's output changes nothing.
// It pinned this of `assert` (TestHostAssertIsAFixedPoint); the arm is the same one an owned
// host runs for an `rmw`-declared surface, so the property is still production's, and a
// mechanism that were not idempotent could not preserve keys and values across a switch either.
func TestTheHostRMWArmIsAFixedPoint(t *testing.T) {
	home, path := assertBaselineHome(t)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first apply: %v", err)
	}
	renderAsRetiredAssert(t, adoptionBaselinePack(t), home, nil, nil)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if string(second) != string(first) {
		t.Errorf("a second rmw apply changed the file:\nfirst:\n%s\nsecond:\n%s",
			first, second)
	}
}

// THE CENSUS'S ANSWER AND THE MECHANISM THAT RAN, measured in one test so they cannot drift
// apart silently. RenderHostPack resolves the mechanism through render.ModeSet.Mechanism; this
// asserts what the census answers for each declaration at each contract left, and that the
// render left that mechanism's own signature.
//
// It pinned until the `assert` retirement (OQ-CO14) that the host ran rmw for a `stateful`
// surface. Now: under `own` a `stateful` surface composes whole (a capture baseline is written)
// and an `rmw` one is read-modify-written (no baseline, the user's leaf deep-merged, a record);
// under `none` nothing is decided, so the entry refuses the surface and writes nothing. A
// dispatch that hardcoded either mechanism, or ignored the contract, fails one of the three.
func TestHostRenderRunsTheMechanismTheCensusNames(t *testing.T) {
	owned := render.Host("/home/x", nil, render.OwnershipOwn).Modes()
	if m, ok := owned.Mechanism(manifest.ModeStateful); !ok || m != manifest.ModeStateful {
		t.Fatalf("the owned census names %q (decided=%v) for a `stateful` surface, want "+
			"stateful — decide what RenderHostPack should run now, and add the arm for it at the "+
			"mechanism switch in hostrender.go", m, ok)
	}
	if m, ok := owned.Mechanism(manifest.ModeRMW); !ok || m != manifest.ModeRMW {
		t.Fatalf("the owned census names %q (decided=%v) for an `rmw` surface, want rmw", m, ok)
	}

	// STATEFUL under own: the capture baseline exists, which no rmw render writes.
	home, _ := seedAdoptionHome(t)
	if _, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil, nil); err != nil {
		t.Fatalf("owned apply: %v", err)
	}
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipOwn).LastRenderPath("acme", "settings")); err != nil {
		t.Errorf("an owned `stateful` render left no capture baseline, so it did not compose: %v", err)
	}

	// RMW under own: the deep merge kept a leaf under a declared object — `applyRMWLayer`
	// recurses so a sibling key the agent owns under the same parent survives — no baseline was
	// written, and the provenance record that is this mechanism's recording duty here is.
	home, path := seedAdoptionHome(t)
	if _, err := RenderHostPack(asRetiredAssert(t, adoptionBaselinePack(t)), home, render.OwnershipOwn,
		false, nil, nil); err != nil {
		t.Fatalf("owned rmw apply: %v", err)
	}
	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil || perms["ask"] == nil || perms["defaultMode"] != "default" {
		t.Errorf("not an rmw deep merge — the user's permissions.ask and yolo's defaultMode "+
			"must both be there:\n%s", data)
	}
	if _, found := hostProvenance(t, home, "acme", "settings"); !found {
		t.Error("no provenance record: an owned host RECORDS an rmw render")
	}
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipOwn).LastRenderPath("acme", "settings")); err == nil {
		t.Error("an rmw render wrote a capture baseline — that is stateful's signature")
	}

	// NONE: undecided, so the surface is refused and the file is untouched.
	home, path = seedAdoptionHome(t)
	before, _ := os.ReadFile(path)
	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipNone, false, nil, nil)
	if err != nil {
		t.Fatalf("apply under none: %v", err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "refused: ") {
		t.Errorf("under none the surface was not refused: %+v", results)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a render under none wrote the file:\n%s", after)
	}
}

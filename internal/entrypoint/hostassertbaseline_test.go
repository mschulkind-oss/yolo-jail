package entrypoint

// hostassertbaseline_test.go pins THE BYTES a host `--assert` apply leaves on a home it has
// already applied to — the measurable form of the "switching to `own` changes **zero bytes**"
// criterion (docs/design/config-ownership-and-promotion.md §11), written against the `assert`
// path FIRST so the baseline exists before anything renders `own`.
//
// Why a byte golden rather than key assertions. §11's criterion is a claim about the FILE, and
// the class it has to catch is the one §6.3.1 already measured on the shipped engine: adoption
// (`ComposeStateful`'s first-migration branch) drops every leaf under a top-level object the
// pure render holds — `dropYoloOwnedSubtrees` — while `rmw` DEEP-MERGES the same object and
// keeps it (`applyRMWLayer`). So a user's `permissions.ask` sitting beside yolo's managed
// `permissions.defaultMode` survives here and would not survive an owned render built on
// today's adoption rule. A test asserting "permissions is present" passes in both worlds; only
// the bytes tell them apart, and the bytes are what the criterion says.
//
// The fixture surface declares NO mode, i.e. `stateful` (manifest.Surface.ResolvedMode's
// default), because that is the coercion case: at the host notch the census runs `rmw` alone
// and a surface declaring `stateful` is rendered through it (render.HostModes). A fixture that
// declared `rmw` outright would pin the one path that needs no coercion.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point this at a real one: it is an
// --assert, it writes the surface, and it leaves a provenance record under that home's state dir.

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// assertBaselineHome seeds a home with a pre-existing agent-written file and applies once, so
// what comes back is a home ALREADY APPLYING UNDER `assert` — the state §11's criterion is
// about. The seeded file carries both classes that have to survive: an undeclared top-level key
// (`apiKeyHelper`) and an undeclared LEAF under a declared object (`permissions.ask`).
func assertBaselineHome(t *testing.T) (home, path string) {
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
	if _, err := RenderHostPack(adoptionBaselinePack(t), home, false, nil); err != nil {
		t.Fatalf("first --assert apply: %v", err)
	}
	return home, path
}

// THE BASELINE. These exact bytes are what an `assert` home holds; §11's criterion is that
// switching it to `own` produces them unchanged. Written out in full rather than computed, so
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

// A host apply under `assert` leaves the file at the baseline: yolo's declared keys asserted,
// and every key the agent owns — including a LEAF under a declared object — byte-identical.
func TestHostAssertLeavesTheAdoptionBaseline(t *testing.T) {
	_, path := assertBaselineHome(t)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	if string(got) != assertBaselineBytes {
		t.Errorf("the `assert` baseline moved.\n got:\n%s\nwant:\n%s\n\nThis is the file "+
			"docs/design/config-ownership-and-promotion.md §11 says an `own` render must "+
			"reproduce byte for byte. If the diff is `permissions.ask`, it is §6.3.1's measured "+
			"defect — adoption drops every leaf under a declared object while `rmw` deep-merges "+
			"it — and narrowing the drop to `rmw`'s granularity is the fix, not moving this "+
			"golden.", got, assertBaselineBytes)
	}
}

// AND IT IS A FIXED POINT. A second apply over the first's output changes nothing — the
// property "zero bytes" is measured against, one notch at a time: a mechanism that were not
// idempotent could not be byte-identical across a switch either.
func TestHostAssertIsAFixedPoint(t *testing.T) {
	home, path := assertBaselineHome(t)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first apply: %v", err)
	}
	if _, err := RenderHostPack(adoptionBaselinePack(t), home, false, nil); err != nil {
		t.Fatalf("second --assert apply: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if string(second) != string(first) {
		t.Errorf("a second --assert apply changed the file:\nfirst:\n%s\nsecond:\n%s",
			first, second)
	}
}

// THE CENSUS'S ANSWER AND THE MECHANISM THAT RAN, measured in one test so they cannot drift
// apart silently. RenderHostPack resolves the mechanism through render.ModeSet.Mechanism; this
// asserts what the census answers for a `stateful`-declaring surface at the host notch, and
// then that the render left rmw's own signature rather than stateful's.
//
// What makes it a pin rather than a restatement: if HostModes is ever changed to run
// `stateful` — which is what `own` does (docs/design/config-ownership-and-promotion.md §10's
// `own` step) — the first assertion fails, and its author has to come here and decide what the
// host entry should then do. That is the forcing function the census exists to be; a dispatch
// that hardcoded rmw would have gone on rendering rmw with the census saying otherwise and
// nothing failing anywhere.
func TestHostRenderRunsTheMechanismTheCensusNames(t *testing.T) {
	home, path := assertBaselineHome(t)

	// The fixture surface declares no mode, i.e. `stateful`.
	mechanism, decided := render.Host(home, nil).Modes().Mechanism(manifest.ModeStateful)
	if !decided || mechanism != manifest.ModeRMW {
		t.Fatalf("the host census names %q (decided=%v) for a `stateful` surface, not %q. The "+
			"render below is still doing rmw — decide what RenderHostPack should run now, and "+
			"add the arm for it at the mechanism switch in hostrender.go",
			mechanism, decided, manifest.ModeRMW)
	}

	// rmw's signature: the deep merge kept a leaf under a declared object. `applyRMWLayer`
	// recurses so a sibling key the agent owns under the same parent survives; a stateful
	// render of the same surface adopts through dropYoloOwnedSubtrees, which drops that whole
	// subtree (§6.3.1). So this key is where the two mechanisms visibly disagree.
	var got map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil || perms["ask"] == nil {
		t.Errorf("permissions.ask is gone — that is the stateful adoption drop, not an rmw "+
			"deep merge:\n%s", data)
	}
	// And the provenance record IS there, which is this mechanism's recording duty at this
	// notch (HostModes records rmw) — so the file above is a render that ran, not one that
	// was quietly skipped into leaving the seed behind.
	if _, found := hostProvenance(t, home, "acme", "settings"); !found {
		t.Error("no provenance record: the census says rmw RECORDS at the host notch")
	}
}

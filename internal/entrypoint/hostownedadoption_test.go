package entrypoint

// hostownedadoption_test.go is the BYTE-INVARIANT case of §11's criterion, measured: switching
// a home already applying under `host_management: assert` to `own` changes nothing in the file
// (docs/design/config-ownership-and-promotion.md §11, §6.3.1).
//
// ⚠ BYTE INVARIANCE IS NO LONGER THE CRITERION — it is a STRONGER property this one fixture
// happens to have, and keeping it asserted here is the point. OQ-CO12 relaxed §11 to
// keys-and-values, which hostownedkeysandvalues_test.go states over fixtures that are not
// canonical. This fixture IS canonical (key-sorted JSON, no comments), so the two contracts
// have nothing left to disagree about except a dropped key — which makes a byte comparison the
// sharpest available instrument here, not a stale one. Relaxing THIS file to the ruled
// comparator would trade that sharpness for nothing: every axis the ruling made conformant is
// absent from the fixture by construction.
//
// It is the SAME FIXTURE hostassertbaseline_test.go pins, and that is the whole method. That
// file states the bytes an `assert` home holds; this one renders the identical pack into the
// identical home under the other contract and compares. Two fixtures could each be right about
// their own notch while the switch between them lost a key — which is exactly the defect §6.3.1
// measured, so the criterion has to be one home crossing one boundary.
//
// ⚠ WHAT THE FIXTURE DELIBERATELY DOES AND DOES NOT COVER. It carries the case the criterion is
// about: an undeclared LEAF (`permissions.ask`) under a top-level object yolo's MANAGED layer
// declares, which `rmw` deep-merges and which a blanket adoption drop used to lose. It carries
// NO computed layer, and that is a choice rather than an omission — `dropComputedTables` still
// drops a top-level key the computed layer holds as an object WHOLESALE, so on claude/settings
// an adopting render loses the user's other `env` vars and `enabledPlugins` entries, which the
// derive returns as partly-filled objects. That residue is PRE-EXISTING (the blanket drop this
// replaced took both keys too), it is documented at dropComputedTables, and it is not this
// step's to close: closing it needs a leaf-level signal saying which leaves under a computed
// table the derive actually asserted. A fixture with a computed table would MIX that known loss
// into the measurement and make a red test ambiguous; one without isolates the question the
// criterion asks.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point one at a real home.

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

// THE BYTE-INVARIANT CASE. A home applying under `assert` is switched to `own`, and the file
// does not move — not "keeps its keys", not "still parses": the same bytes. §11 asks for less
// than this (keys and values, OQ-CO12); this fixture delivers more, and the surplus is what
// catches a dropped leaf without having to name the leaf.
//
// It works because ComposeStateful reads "no trusted last_render for this surface" as a FIRST
// MIGRATION and seeds the overlay from the file it finds, so the first owned render is
// `declared layers + everything the file holds that yolo does not declare` — which is the file.
// Nothing in this step adds a guard for that; the guard is that branch, and it exists because
// seeding an empty overlay there was a shipped data-loss bug (B1).
func TestSwitchingToOwnKeepsACanonicalFileByteIdentical(t *testing.T) {
	home, path := assertBaselineHome(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the assert baseline: %v", err)
	}
	// Sanity: the home really is at the pinned baseline, so a failure below is about the
	// SWITCH rather than about the baseline having moved underneath it.
	if string(before) != assertBaselineBytes {
		t.Fatalf("the assert baseline moved; fix TestHostAssertLeavesTheAdoptionBaseline "+
			"first:\n%s", before)
	}

	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("first `own` apply: %v", err)
	}
	if len(results) != 1 || results[0].Action != "rendered" {
		t.Fatalf("the owned render did not render: %+v", results)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after the owned render: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("switching to `own` changed the file.\nbefore:\n%s\nafter:\n%s\n\n"+
			"This fixture is canonical on every axis OQ-CO12 made conformant — already "+
			"key-sorted JSON, no comments — so a byte diff here is a KEY OR VALUE that moved, "+
			"which §11 still forbids. Do not answer it by weakening this comparison to the "+
			"ruled keys-and-values one: hostownedkeysandvalues_test.go already states that, "+
			"over fixtures built to exercise it. If the diff is a LEAF under a declared "+
			"object, adoption has gone back to dropping whole top-level subtrees "+
			"(dropComputedTables' second bullet says why that is wrong). If it is a whole "+
			"top-level key the file held and no layer declares, the first-migration adoption "+
			"branch is not running — check that the host capture store is resolving "+
			"(render.Target.SidecarDir) rather than leaving last_render present.",
			before, after)
	}
}

// AND THE OWNED RENDER IS A FIXED POINT. The second apply has a real last_render to diff
// against, so it takes the STEADY-STATE branch rather than adoption — a different code path
// reaching the same bytes. Without this, byte invariance could be true only of the one render
// that adopts, and the next apply could quietly drop what the first preserved.
func TestOwnedRenderIsAFixedPoint(t *testing.T) {
	home, path := assertBaselineHome(t)
	pack := adoptionBaselinePack(t)
	if _, err := RenderHostPack(pack, home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("first `own` apply: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderHostPack(pack, home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("second `own` apply: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("a second `own` apply changed the file — adoption and steady state disagree:"+
			"\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// THE OBSERVE POSTURE AGREES, and says `unchanged` rather than `would render`. It is the same
// claim as the criterion, computed by the code a user actually reads before switching: a dry
// run over a home about to be adopted must not list a pending write it will not make.
//
// It also pins that observe WRITES NOTHING on the stateful arm — no surface file, no capture
// store — which is what lets confirmHostLosses run a full observe pass for free.
func TestOwnedObserveReportsUnchangedAndWritesNothing(t *testing.T) {
	home, path := assertBaselineHome(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	results, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, true, nil)
	if err != nil {
		t.Fatalf("observe under `own`: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want one surface result, got %+v", results)
	}
	if results[0].WouldChange || results[0].Action != "unchanged" {
		t.Errorf("a dry run over a home `own` would adopt byte-for-byte reported %q "+
			"(WouldChange=%v), want \"unchanged\" — the predicate runs the render and compares "+
			"its bytes, so this disagreeing with TestSwitchingToOwnKeepsACanonicalFileByteIdentical "+
			"means one of the two is not running the writer's own fold", results[0].Action,
			results[0].WouldChange)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the observe posture wrote the surface file")
	}
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipOwn).SidecarDir()); err == nil {
		t.Error("the observe posture created the capture store — a dry run must leave no " +
			"trace, or the next real render would find a baseline no write ever produced")
	}
}

// THE CAPTURE STORE, after a real owned apply: the three capture files live in the host store
// and the provenance record stays where `assert` writes it (§6.2's two directories, two
// lifetimes). The selection record is absent because this surface writes no selection — the
// one of the three that is written only on demand.
func TestOwnedRenderWritesTheHostCaptureStore(t *testing.T) {
	home, _ := assertBaselineHome(t)
	if _, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("`own` apply: %v", err)
	}

	target := render.Host(home, nil, render.OwnershipOwn)
	store := target.SidecarDir()
	if want := filepath.Join(home, ".local", "share", "yolo-jail", "host-capture"); store != want {
		t.Fatalf("capture store at %q, want %q", store, want)
	}
	info, err := os.Stat(store)
	if err != nil {
		t.Fatalf("the owned render left no capture store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the capture store is %O, want 0700 — at 0600 the owner cannot open a "+
			"single file inside it, and at 0755 a real home exposes the overlay", perm)
	}
	for _, p := range []string{
		target.OverlayPath("acme", "settings"),
		target.LastRenderPath("acme", "settings"),
	} {
		st, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing capture sidecar %s: %v", filepath.Base(p), err)
			continue
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s is %O, want 0600 — the overlay holds whatever the user's file held "+
				"that yolo's layers do not", filepath.Base(p), perm)
		}
	}
	// The overlay is the ADOPTED residue, which is what makes the invariance a
	// consequence rather than a coincidence: the keys the file holds and no layer declares
	// are recorded, so the next render reproduces them from the store.
	data, err := os.ReadFile(target.OverlayPath("acme", "settings"))
	if err != nil {
		t.Fatalf("read the overlay: %v", err)
	}
	var overlay map[string]any
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatalf("decode the overlay: %v\n%s", err, data)
	}
	if overlay["apiKeyHelper"] == nil {
		t.Errorf("the adopted overlay does not hold the file's undeclared top-level key:\n%s",
			data)
	}
	perms, _ := overlay["permissions"].(map[string]any)
	if perms == nil || perms["ask"] == nil {
		t.Errorf("the adopted overlay does not hold the undeclared LEAF under a declared "+
			"object — that is §6.3.1's drop, and the file above only looks right until the "+
			"next render:\n%s", data)
	}

	// And the provenance record did NOT move into the store. `--revert` reads it for an
	// `assert` home too, so folding it in would make reverting depend on a dir only `own`
	// creates.
	if _, found := hostProvenance(t, home, "acme", "settings"); !found {
		t.Error("no provenance record under host-provenance/ — the `own` census records " +
			"`stateful`, and --revert consumes that record at every contract")
	}
}

// THE CENSUS IS WHAT PICKED THE MECHANISM, and this is the pin
// TestHostRenderRunsTheMechanismTheCensusNames is for `assert`: the contract answers
// `stateful`, and the render left stateful's own signature — a capture store, which rmw never
// writes.
func TestOwnedHostRenderRunsTheMechanismTheCensusNames(t *testing.T) {
	home, _ := assertBaselineHome(t)
	mechanism, decided := render.Host(home, nil, render.OwnershipOwn).
		Modes().Mechanism(manifest.ModeStateful)
	if !decided || mechanism != manifest.ModeStateful {
		t.Fatalf("the `own` census names %q (decided=%v) for a `stateful` surface, not %q — "+
			"decide what RenderHostPack should run now and add the arm for it at the mechanism "+
			"switch in hostrender.go", mechanism, decided, manifest.ModeStateful)
	}
	if _, err := RenderHostPack(adoptionBaselinePack(t), home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("`own` apply: %v", err)
	}
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipOwn).SidecarDir()); err != nil {
		t.Errorf("the owned render kept no capture store, so it did not run `stateful`: %v", err)
	}
}

// A KEYLESS SURFACE IS REFUSED UNDER `own` (OQ-CO9), and the file is untouched. Adoption cannot
// take a partial residue from a surface whose one "key" is the whole file, so the first owned
// render would replace it outright — and confirmHostLosses cannot see that, because EntryLosses
// is defined over named entries in a table and a keyless surface has none.
func TestOwnRefusesAKeylessSurface(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".acme", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const seed = "the user's own file\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	// No `defaults`: a pack-declared surface cannot carry a SCALAR defaults layer through the
	// manifest DTO (`defaults` is an object there), which makes the fixture sharper rather than
	// weaker — with no layer at all the pure render is EMPTY, so an owned render that did not
	// refuse would blank the user's file outright. That is the loss OQ-CO9 refuses.
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "notes", "codec": "raw",
		"path": "~/.acme/notes.txt",
	}})
	if err != nil {
		t.Fatal(err)
	}
	pack := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}

	results, err := RenderHostPack(pack, home, render.OwnershipOwn, false, nil)
	if err != nil {
		t.Fatalf("`own` apply: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want one surface result, got %+v", results)
	}
	if got := results[0].Action; !strings.HasPrefix(got, "refused:") {
		t.Errorf("a keyless surface under `own` reported %q, want a refusal", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != seed {
		t.Errorf("the refused keyless surface was written anyway:\n%s", after)
	}
}

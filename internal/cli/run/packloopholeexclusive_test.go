package run

// packloopholeexclusive_test.go is the FOURTH launch pre-flight
// (docs/design/loophole-packaging.md §3.1, landing item 5b): a loophole NAME claimed twice
// across pack declarations.
//
// IT HAD A SECOND HALF — pack-vs-RESERVED, against the names yolo answered to itself.
// `loopholes.ReservedLoopholeNames()` was deleted on 2026-08-19 when the last name in it,
// `claude-oauth-broker`, became a contribution of `packs/claude`
// (docs/design/broker-as-a-pack.md §10 step 5). The tests for that half are gone with it;
// what replaced them is TestEveryShippedLoopholePackSurvivesThePreflight below, which is
// the assertion that actually protects a user — a reservation left standing over a
// pack-shipped name refuses every launch that selects the pack.
//
// It is a pre-flight and not a row in packload.Collisions for two measured reasons, and
// this file pins both so a later "simplification" back into the generic pass fails here
// rather than in a jail:
//
//  1. packload.Collisions is never consulted at launch (TestCollisionsIsNotOnTheLaunchPath).
//  2. Its generic Exclusive loop skips single-pack groups, so ONE pack colliding with
//     itself is invisible there (TestOnePackCollidingWithItselfIsRefused).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// decl is a PackLoopholeDecl for a pack-relative `from`, with the name derived exactly as
// the projection derives it (the module dir's basename).
func decl(pack, from string) PackLoopholeDecl {
	dir := filepath.Join("/staged", pack, filepath.FromSlash(from))
	return PackLoopholeDecl{Pack: pack, From: from, Dir: dir, Name: filepath.Base(dir)}
}

// TestTwoPacksOneLoopholeNameIsRefused is the headline rule: exclusivity is per NAME, and
// a collision is fatal naming BOTH sources. A shadowed loophole name means a daemon nobody
// audited running under a name the user trusts — and, worse, the loser's manifest still
// contributes its binds/devices/jail_env to the argv while the winner's daemon runs.
func TestTwoPacksOneLoopholeNameIsRefused(t *testing.T) {
	got := PackLoopholeNameConflicts([]PackLoopholeDecl{
		decl("alpha", "loopholes/acme-proxy"),
		decl("beta", "vendor/acme-proxy"),
	})
	if len(got) != 1 {
		t.Fatalf("want exactly one conflict, got %d: %v", len(got), got)
	}
	msg := got[0]
	for _, want := range []string{
		"acme-proxy",               // the name
		"pack alpha",               // source one
		"pack beta",                // source two
		"loopholes/acme-proxy",     // the line alpha edits
		"vendor/acme-proxy",        // the line beta edits
		"sole-owned",               // the rule
		"jail_env",                 // what the loser still contributes
		"Rename one of the module", // the fix
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q — a user hitting this must be able to fix it without\n"+
				"reading a design doc; got:\n%s", want, msg)
		}
	}
}

// TestOnePackCollidingWithItselfIsRefused is measured hole #2, and the whole reason this is
// a per-DECLARATION pass rather than a row in the generic Exclusive loop: that loop groups
// by pack and skips a group of one (`if len(packSet) < 2 { continue }`), so a single pack
// declaring both `a/acme` and `vendor/acme` — same basename, both individually valid —
// collides with ITSELF and is not reported there at all. Identical to why
// ConfigSurfaceCollisions had to become its own exported pass.
func TestOnePackCollidingWithItselfIsRefused(t *testing.T) {
	got := PackLoopholeNameConflicts([]PackLoopholeDecl{
		decl("solo", "a/acme"),
		decl("solo", "vendor/acme"),
	})
	if len(got) != 1 {
		t.Fatalf("one pack declaring two modules with the same basename must be refused — "+
			"the generic Exclusive loop skips single-pack groups by design, which is the wrong "+
			"question here; got %d conflicts: %v", len(got), got)
	}
	if !strings.Contains(got[0], "twice") {
		t.Errorf("a self-collision must say so rather than reading as two packs fighting; got:\n%s", got[0])
	}
	for _, want := range []string{"a/acme", "vendor/acme"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("refusal missing %q — with one pack named twice, the two `from` values are\n"+
				"the ONLY thing distinguishing the declarations; got:\n%s", want, got[0])
		}
	}
}

// TWO PACKS CLAIMING ONE NAME is reported ONCE, naming both — which is the refusal that
// took over from the reserved one.
//
// The fixture is `claude-oauth-broker` deliberately, because that is now an ORDINARY
// pack-vs-pack clash and the name is the sharpest instance of it: `yolo broker status`,
// `yolo check`'s broker section, brokerEnsure and the in-jail terminator all reach it by
// literal, so two claimants would be half the broker from one manifest.
func TestTwoPacksClaimingTheBrokerNameIsReportedOnce(t *testing.T) {
	got := PackLoopholeNameConflicts([]PackLoopholeDecl{
		decl("alpha", "loopholes/"+broker.BrokerLoopholeName),
		decl("beta", "loopholes/"+broker.BrokerLoopholeName),
	})
	if len(got) != 1 {
		t.Fatalf("want one message for one mistake, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "sole-owned") {
		t.Errorf("the message must say what the rule IS, not just that it fired; got:\n%s", got[0])
	}
	for _, want := range []string{"pack alpha", "pack beta"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("both claimants must be named; missing %q in:\n%s", want, got[0])
		}
	}
}

// The happy path: distinct names across packs are ordinary, and a pack shipping THREE
// loopholes is ordinary too (exclusivity is per name, not per pack — the same rule
// `program` has per `bin`).
func TestDistinctLoopholeNamesAreAllowed(t *testing.T) {
	got := PackLoopholeNameConflicts([]PackLoopholeDecl{
		decl("alpha", "loopholes/one"),
		decl("alpha", "loopholes/two"),
		decl("alpha", "loopholes/three"),
		decl("beta", "loopholes/four"),
	})
	if len(got) != 0 {
		t.Errorf("a pack shipping three loopholes is ordinary; got refusals: %v", got)
	}
}

// Deterministic order, because the launch refusal is user-visible text and a set-iteration
// order would make it flap between launches for one unchanged config.
func TestConflictOrderIsDeterministic(t *testing.T) {
	in := []PackLoopholeDecl{
		decl("zeta", "loopholes/zulu"),
		decl("alpha", "loopholes/zulu"),
		decl("mike", "loopholes/alfa"),
		decl("bravo", "loopholes/alfa"),
	}
	first := PackLoopholeNameConflicts(in)
	if len(first) != 2 {
		t.Fatalf("want 2 conflicts, got %d: %v", len(first), first)
	}
	if !strings.Contains(first[0], "alfa") || !strings.Contains(first[1], "zulu") {
		t.Errorf("conflicts must be ordered by loophole name; got:\n%v", first)
	}
	// The packs within one conflict are ordered too.
	if strings.Index(first[1], "pack alpha") > strings.Index(first[1], "pack zeta") {
		t.Errorf("claimants within a conflict must be sorted; got:\n%s", first[1])
	}
	for i := 0; i < 5; i++ {
		if again := PackLoopholeNameConflicts(in); strings.Join(again, "\n") != strings.Join(first, "\n") {
			t.Fatalf("unstable output across runs:\n%v\nvs\n%v", first, again)
		}
	}
}

// TestCollisionsIsNotOnTheLaunchPath is measured hole #1, pinned as a NEGATIVE: the design
// priced this pre-flight at zero on the belief that packload.Collisions' generic Exclusive
// pass already refused it. It does not, because it is not called at launch — its callers
// are the `pack footprint` report and internal/cli/check/packs.go (which passes
// packload.Embedded(), embedded packs only).
//
// This asserts the property that makes the pre-flight necessary rather than the call graph:
// Collisions over two packs that BOTH declare the same loophole module reports nothing, so
// wiring it in would not have refused the launch.
func TestCollisionsIsNotOnTheLaunchPath(t *testing.T) {
	a := &packload.Pack{Name: "alpha", Root: t.TempDir(), Decl: &packdecl.Manifest{
		Name:        "alpha",
		Contributes: []packdecl.Contribution{{Kind: packLoopholeKind, From: "loopholes/acme"}},
	}}
	b := &packload.Pack{Name: "beta", Root: t.TempDir(), Decl: &packdecl.Manifest{
		Name:        "beta",
		Contributes: []packdecl.Contribution{{Kind: packLoopholeKind, From: "vendor/acme"}},
	}}
	for _, c := range packload.Collisions([]*packload.Pack{a, b}) {
		if c.Kind == packLoopholeKind {
			t.Fatalf("packload.Collisions now reports a loophole-name collision (%+v). That is "+
				"welcome, but it does NOT make this pre-flight redundant: Collisions is not "+
				"called at launch, skips single-pack groups, and cannot see reserved names. "+
				"Delete this test, keep the pre-flight.", c)
		}
	}
	// And the pre-flight over the same two packs DOES refuse.
	if got := PackLoopholeNameConflicts(packLoopholeDecls([]*packload.Pack{a, b})); len(got) != 1 {
		t.Errorf("the pre-flight must refuse what Collisions misses; got %d conflicts: %v", len(got), got)
	}
}

// packLoopholeDecls resolves `from` against the STAGED tree and derives the name from the
// basename — the two facts that let the pre-flight run before any manifest is loaded
// (loopholes' own loader enforces name == dir basename, so the name is knowable from the
// path alone).
func TestPackLoopholeDeclsResolveAgainstStagedRoot(t *testing.T) {
	root := t.TempDir()
	p := &packload.Pack{Name: "alpha", Root: root, Decl: &packdecl.Manifest{
		Name: "alpha",
		Contributes: []packdecl.Contribution{
			{Kind: packLoopholeKind, From: "loopholes/acme-proxy"},
			// An empty `from` contributes NOTHING here: `loophole` has no conventional
			// source dir to fall back to, and refusing it is the pack LAYER's job (a
			// pack.json error `yolo pack lint` decides with no loophole loaded).
			{Kind: packLoopholeKind, From: ""},
			// A different kind is not a loophole declaration.
			{Kind: packdecl.KindSkills, From: "skills"},
		},
	}}
	got := packLoopholeDecls([]*packload.Pack{p})
	if len(got) != 1 {
		t.Fatalf("want exactly the one honorable declaration, got %d: %+v", len(got), got)
	}
	if want := filepath.Join(root, "loopholes", "acme-proxy"); got[0].Dir != want {
		t.Errorf("Dir = %q, want the STAGED path %q — resolving against the pack SOURCE would "+
			"hide an only/exclude filter that removed the module dir", got[0].Dir, want)
	}
	if got[0].Name != "acme-proxy" {
		t.Errorf("Name = %q, want the dir basename %q", got[0].Name, "acme-proxy")
	}
	if got[0].From != "loopholes/acme-proxy" {
		t.Errorf("From = %q, want the declared value verbatim (it is what the user edits)", got[0].From)
	}
}

// The ORIGIN GATE carried into the converged set is the pack's own MayAccessHost — the same
// decision packMayAccessHost already made — not a second gate that could disagree with it.
// A loophole's doctor_cmd and host_daemon are host EXECUTION, strictly more than the host
// READS that gate governs, so a pack that may not read the host certainly may not run a
// daemon on it.
func TestPackLoopholeModulesCarryTheOriginGate(t *testing.T) {
	approved := &packload.Pack{Name: "trusted", Root: t.TempDir(), MayAccessHost: true,
		Decl: &packdecl.Manifest{Name: "trusted",
			Contributes: []packdecl.Contribution{{Kind: packLoopholeKind, From: "loopholes/ok"}}}}
	refused := &packload.Pack{Name: "unapproved", Root: t.TempDir(), MayAccessHost: false,
		Decl: &packdecl.Manifest{Name: "unapproved",
			Contributes: []packdecl.Contribution{{Kind: packLoopholeKind, From: "loopholes/nope"}}}}

	mods := packLoopholeModules([]*packload.Pack{approved, refused})
	if len(mods) != 2 {
		t.Fatalf("want one module per declaration, got %d", len(mods))
	}
	byDir := map[string]bool{}
	for _, m := range mods {
		byDir[filepath.Base(m.Dir)] = m.HostExecApproved
	}
	if !byDir["ok"] {
		t.Error("a pack whose origin permits host access must have its loophole approved for " +
			"host execution — that is the same gate, not a second one")
	}
	if byDir["nope"] {
		t.Error("an UNAPPROVED pack's loophole must not be approved for host execution: a " +
			"doctor_cmd is host execution, which is strictly more than the host reads the gate " +
			"governs")
	}
}

// TestStagePacksRefusesLoopholeNameCollision is the pre-flight at its REAL call site,
// beside the other three, covering the attach path too (a collision is a config error
// either way).
//
// It used to be skipped until the `loophole` kind was in the build (packdecl.Decode refuses an
// unknown kind before any pre-flight sees it, so the launch would have failed for the wrong
// reason). The kind landed and the guard went with it: a t.Skip on a condition that can no
// longer be true is not inert, it is a way for a real KnownKind regression to silently
// re-disable this refusal test instead of failing it.
func TestStagePacksRefusesLoopholeNameCollision(t *testing.T) {
	home := packHome(t)
	alpha := loopholePackDir(t, "alpha", "loopholes/acme")
	beta := loopholePackDir(t, "beta", "vendor/acme")
	writeUserPacks(t, home, `["file://`+alpha+`", "file://`+beta+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, _, _, err := o.stagePacks("yolo-test-loophole-collide")
	if err == nil {
		t.Fatal("two packs shipping one loophole name must fail the launch: a shadowed " +
			"loophole name means a daemon nobody audited running under a name the user trusts")
	}
	for _, want := range []string{"acme", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("launch refusal missing %q; got:\n%s", want, err.Error())
		}
	}
}

// The exclusivity half AT THE REAL CALL SITE. It used to be the RESERVED half — a single
// pack claiming a name yolo answered to — first with `journal`, then `cgroup-delegate`,
// then the broker's, each of which stopped being reserved as it became a pack's.
//
// Two packs claiming one name is what is left, and stagePacks is where it has to be fatal:
// the loser's manifest still contributes its binds, devices and jail_env to the argv while
// the winner's daemon runs, so a warning would leave the user with a mixture under one
// trusted name.
func TestStagePacksRefusesADuplicateLoopholeName(t *testing.T) {
	home := packHome(t)
	one := loopholePackDir(t, "grabby", "loopholes/"+broker.BrokerLoopholeName)
	two := loopholePackDir(t, "grabbier", "loopholes/"+broker.BrokerLoopholeName)
	writeUserPacks(t, home, `["file://`+one+`", "file://`+two+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, _, _, err := o.stagePacks("yolo-test-loophole-duplicate")
	if err == nil {
		t.Fatalf("two packs shipping loopholes/%s must fail the launch — otherwise both load, "+
			"one daemon runs and the other manifest's binds, devices and jail_env still cross",
			broker.BrokerLoopholeName)
	}
	if !strings.Contains(err.Error(), broker.BrokerLoopholeName) {
		t.Errorf("refusal must name the contested loophole; got:\n%s", err.Error())
	}
}

// loopholePackDir writes a local pack declaring one `loophole` contribution plus the module
// dir it points at, and returns the pack root.
func loopholePackDir(t *testing.T, name, from string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	moduleDir := filepath.Join(root, filepath.FromSlash(from))
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"loophole","from":"` + from + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// A minimal, valid loophole manifest: name MUST equal the dir basename (the loader
	// enforces it, which is what makes the pre-flight's basename shortcut sound).
	body := `{"name":"` + filepath.Base(moduleDir) + `","description":"test","default_enabled":true,` +
		`"transport":"none","lifecycle":"external"}`
	if err := os.WriteFile(filepath.Join(moduleDir, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestEveryShippedLoopholePackSurvivesThePreflight is the trap this sprint existed to
// avoid, asserted over the packs yolo actually ships.
//
// A name yolo reserves and a pack-shipped loophole of the same name is not a warning and
// not a degraded loophole: PackLoopholeNameConflicts is FATAL, so the jail does not start
// at all for anyone who selected the pack. Five loopholes made that trip and they failed
// three different ways, which is why this asserts over ALL of them rather than one
// standing for the set:
//
//	host-processes, audio       reserved as bundled DIRECTORY names, read off the embed
//	                            the loader materialized — `git mv` retired them for free.
//	journal, cgroup-delegate    reserved as CONSTANTS in paths.BuiltinLoopholeNames — each
//	                            had to be deleted by hand in the commit shipping its pack.
//	claude-oauth-broker         reserved BOTH ways, and it is a contribution of the
//	                            `claude` AGENT pack rather than a pack of its own, so it is
//	                            also the row that would be missed by anything iterating
//	                            "the loophole packs".
//
// The SHIPPED packs, materialized from the binary's embed and read through the same
// packLoopholeDecls the launch path uses — not hand-built decls, which would only assert
// that a string is absent from a set.
func TestEveryShippedLoopholePackSurvivesThePreflight(t *testing.T) {
	// pack name -> the loophole it contributes. `claude` is the one where they differ.
	shipped := map[string]string{
		"host-processes":  "host-processes",
		"audio":           "audio",
		"journal":         "journal",
		"cgroup-delegate": "cgroup-delegate",
		"claude":          broker.BrokerLoopholeName,
	}
	var all []PackLoopholeDecl
	for name, loophole := range shipped {
		t.Run(name, func(t *testing.T) {
			var pack *packload.Pack
			for _, p := range packload.Embedded() {
				if p.Name == name {
					pack = p
				}
			}
			if pack == nil {
				t.Skip("no embedded packs registered in this test binary")
			}
			decls := packLoopholeDecls([]*packload.Pack{pack})
			if len(decls) != 1 {
				t.Fatalf("the %s pack must declare exactly one loophole, got %d", name, len(decls))
			}
			if decls[0].Name != loophole {
				t.Errorf("packs/%s contributes loophole %q, want %q", name, decls[0].Name, loophole)
			}
			if got := PackLoopholeNameConflicts(decls); len(got) != 0 {
				t.Errorf("the official %s pack is refused by the loophole-name pre-flight, "+
					"so every jail selecting it fails to launch:\n%s", name, strings.Join(got, "\n"))
			}
			all = append(all, decls...)
		})
	}
	// AND ALL OF THEM TOGETHER, which is the configuration a user actually has: the five
	// names must be distinct across the five packs, or selecting two of them is fatal.
	if got := PackLoopholeNameConflicts(all); len(got) != 0 {
		t.Errorf("yolo's own packs collide with each other, so selecting them together "+
			"refuses the launch:\n%s", strings.Join(got, "\n"))
	}
}

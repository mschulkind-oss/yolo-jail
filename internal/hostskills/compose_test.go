package hostskills

// compose_test.go pins the DESTINATION-WIDE half of §6a-2: yolo composes a skills dir wholesale,
// so the questions are about the directory rather than about one pack's entries.
//
// The tests are organized around the four passes a composed destination goes through (adopt →
// migrate → render → retire), plus the one property that must never break: nothing the user wrote
// is absent from BOTH the destination and the local pack.
//
// Every home is a t.TempDir(). A test here must never touch a real $HOME — and this file's subject
// MOVES DIRECTORIES, so that is load-bearing beyond the usual.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// composeFixture builds a destination out of N layers, each a pack name plus a temp skills source,
// and returns the destination plus the sources in layer order. The layer ORDER is the composition
// order, which is what "later wins" means.
func composeFixture(t *testing.T, tier Tier, packs ...string) (Destination, []string) {
	t.Helper()
	home := t.TempDir()
	d := Destination{Dir: filepath.Join(home, ".codex", "skills")}
	var sources []string
	for _, name := range packs {
		src := filepath.Join(t.TempDir(), name, "skills")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, src)
		d.Layers = append(d.Layers, Layer{Pack: name, Tier: tier, Sources: []string{src}})
	}
	return d, sources
}

// composeReq builds a ComposeRequest with temp dirs for everything writable, including a local pack
// the migration can move into.
//
// Configured is left NIL and each test that needs it calls stillConfigured, because nil means "every
// composer has been dropped" and therefore "this package retires nothing" — the fail-closed reading.
// A helper that quietly filled it in would make every retire test pass without anything asserting
// that the R1 boundary is even consulted.
func composeReq(t *testing.T) ComposeRequest {
	t.Helper()
	return ComposeRequest{
		Composed:        &Manifest{Entries: map[string]string{}},
		Legacy:          &Manifest{Entries: map[string]string{}},
		ArchiveRoot:     ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:           "20260804-000000",
		LocalPackSkills: filepath.Join(t.TempDir(), "local", "skills"),
		PackSetComplete: true,
	}
}

// stillConfigured marks a destination's own layers as packs the user still has, which is the ordinary
// state and the one every retire test but TestPruneLeavesADroppedPacksContentToTheConfirmedRetire
// needs. Derived from the layers rather than taking names, so a fixture and its configured set cannot
// drift apart.
func stillConfigured(req *ComposeRequest, dests ...Destination) {
	req.Configured = map[string]bool{}
	for _, d := range dests {
		for _, l := range d.Layers {
			req.Configured[l.Pack] = true
		}
	}
}

// body reads a delivered skill's body, for asserting WHICH layer won a name.
func body(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
	if err != nil {
		t.Fatalf("no skill at %s/%s: %v", dir, name, err)
	}
	return string(data)
}

// §6a-5, THE DEFECT THIS CHANGE EXISTS TO DISSOLVE. At flat tier a LATER layer wins a same-named
// skill. Under the per-pack delivery the ownership rule refused any pack overwriting another pack's
// recorded entry whatever the order, so the local pack — appended last precisely because it
// outranks everything — LOST a flat-tier collision to a shared pack and the user's own copy was
// silently not the one their agent loaded.
//
// Flat tier specifically: at namespaced tier each pack gets its own subtree, so a collision cannot
// be represented and the test would prove nothing (which is how the defect survived its first
// probe). `codex`, `pi` and `agy` all ship flat, so a real user reaches this path.
func TestFlatLaterLayerWinsTheName(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "sflat", "local")
	writeSkill(t, sources[0], "mine", "SHARED BODY")
	writeSkill(t, sources[1], "mine", "LOCAL BODY")

	if _, err := RenderHostSkills([]Destination{d}, composeReq(t), false); err != nil {
		t.Fatal(err)
	}
	if got := body(t, d.Dir, "mine"); !strings.Contains(got, "LOCAL BODY") {
		t.Errorf("the LAST layer must win a same-named skill at flat tier — the local pack is "+
			"appended last exactly so a personal skill outranks a shared pack's (§6a-5):\n%s", got)
	}
}

// The same collision is IDEMPOTENT and archives nothing. The naive fix — let a later layer
// overwrite, and archive the previous copy first because the record owns it — produces one archive
// entry per apply forever for a destination that never changed.
func TestFlatCollisionIsIdempotentAndArchivesNothing(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "sflat", "local")
	writeSkill(t, sources[0], "mine", "SHARED BODY")
	writeSkill(t, sources[1], "mine", "LOCAL BODY")
	req := composeReq(t)

	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	first := treeSnapshot(t, d.Dir)
	res, err := RenderHostSkills([]Destination{d}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if second := treeSnapshot(t, d.Dir); first != second {
		t.Errorf("the second apply changed the tree:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	for _, r := range res {
		if r.Action == ActionArchived {
			t.Errorf("re-composing a name two layers both claim archived something — the previous "+
				"copy is only archivable when it survives from a previous APPLY: %+v", r)
		}
	}
	if got := body(t, d.Dir, "mine"); !strings.Contains(got, "LOCAL BODY") {
		t.Errorf("the last layer must still win on re-apply:\n%s", got)
	}
}

// A NAME THAT CHANGES HANDS between applies is an update, not a retire-then-write. The per-pack
// retire got this wrong in both directions once several packs merged into one dir: pack A retired
// the name and pack B was refused it.
func TestFlatNameChangingHandsIsAnUpdate(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "a", "b")
	writeSkill(t, sources[0], "shared", "FROM A")
	req := composeReq(t)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	// Pack A stops shipping it, pack B starts.
	if err := os.RemoveAll(filepath.Join(sources[0], "shared")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, sources[1], "shared", "FROM B")

	res, err := RenderHostSkills([]Destination{d}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := body(t, d.Dir, "shared"); !strings.Contains(got, "FROM B") {
		t.Errorf("the new owner's content must land:\n%s", got)
	}
	for _, r := range res {
		if r.Action == ActionArchived {
			t.Errorf("a name moving from one pack to another must not be archived and immediately "+
				"rewritten: %+v", r)
		}
	}
}

// THE RETIRE, at the destination level: a skill NO layer composes any more is archived, never
// deleted. This used to be deliverFlat's per-pack pass; it is a destination-wide pass now because
// only the destination knows the union.
func TestComposeRetiresWhatNoLayerShips(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "goes", "bye")
	writeSkill(t, sources[0], "stays", "here")
	req := composeReq(t)
	stillConfigured(&req, d)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(sources[0], "goes")); err != nil {
		t.Fatal(err)
	}

	res, err := RenderHostSkills([]Destination{d}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	r := find(t, res, "goes")
	if r.Action != ActionArchived {
		t.Fatalf("action = %q, want %q (%q)", r.Action, ActionArchived, r.Detail)
	}
	// RECOVERABLE at the reported path: an archive the user cannot find is a deletion from their
	// point of view.
	at := r.Detail[strings.LastIndex(r.Detail, "→ ")+len("→ "):]
	if _, serr := os.Stat(filepath.Join(at, "SKILL.md")); serr != nil {
		t.Errorf("archived content must be recoverable at the reported path %q: %v", at, serr)
	}
	if _, serr := os.Stat(filepath.Join(d.Dir, "goes")); !os.IsNotExist(serr) {
		t.Errorf("the retired skill should no longer be loadable: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(d.Dir, "stays", "SKILL.md")); serr != nil {
		t.Errorf("the still-composed skill must survive: %v", serr)
	}
}

// A retiring entry that has become a DANGLING LINK is cleared, not archived: renaming a broken link
// into the archive would report "moved to <path>" as though the content were recoverable there.
func TestComposeRetireClearsADanglingEntry(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "goes", "bye")
	writeSkill(t, sources[0], "stays", "here")
	req := composeReq(t)
	stillConfigured(&req, d)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	// The pack stops shipping it, AND the delivered copy has been replaced by a stale link (the
	// user re-ran their dotfile manager over the top).
	if err := os.RemoveAll(filepath.Join(sources[0], "goes")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(d.Dir, "goes")); err != nil {
		t.Fatal(err)
	}
	target := dangle(t, d.Dir, "goes")

	res, err := RenderHostSkills([]Destination{d}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	r := find(t, res, "goes")
	if r.Action != ActionCleared {
		t.Errorf("a dangling entry being retired must be CLEARED, not archived — there is no "+
			"content to recover: action = %q (%q)", r.Action, r.Detail)
	}
	if !strings.Contains(r.Detail, target) {
		t.Errorf("the report must name the stale target: %q", r.Detail)
	}
	if _, serr := os.Lstat(filepath.Join(d.Dir, "goes")); !os.IsNotExist(serr) {
		t.Errorf("the stale link should be gone: %v", serr)
	}
	if _, recorded := req.Composed.Owner(filepath.Join(d.Dir, "goes")); recorded {
		t.Error("the ownership record must forget a cleared entry")
	}
}

// The flat half of the observe-tense gate (dangling_test.go's TestObserveArchiveUsesTheFutureTense
// keeps the namespaced half): a dry run that says `archived` reads as though it moved the user's
// skill out of their home, which is precisely the fear a dry run exists to allay.
func TestComposeObserveRetireUsesTheFutureTense(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "goes", "bye")
	writeSkill(t, sources[0], "stays", "here")
	req := composeReq(t)
	stillConfigured(&req, d)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(sources[0], "goes")); err != nil {
		t.Fatal(err)
	}

	res, err := RenderHostSkills([]Destination{d}, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, res, "goes"); r.Action != ActionWouldArchive {
		t.Errorf("observe retire action = %q, want %q", r.Action, ActionWouldArchive)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "goes", "SKILL.md")); err != nil {
		t.Errorf("observe archived the entry: %v", err)
	}
	if _, recorded := req.Composed.Owner(filepath.Join(d.Dir, "goes")); !recorded {
		t.Error("observe forgot the record for a path it did not move — the next real apply would " +
			"then read the user's home as holding a skill nobody owns")
	}
}

// AN INCOMPLETE PACK SET stops the RENDER's own retire, not only the prune's — which is sharper than
// the briefing analogue and is the offline-apply data loss. A content pack whose remote is
// unreachable contributes no layer while the AGENT pack naming the destination still does, so the
// directory IS composed and a retire keyed only on "no layer ships this name" archives the
// unreachable pack's skills on every offline apply while reporting success.
//
// Found by mutation: disabling this guard left the package suite entirely green (one CLI test caught
// it), so the render-level half of PackSetComplete had no coverage at all here.
func TestComposeRenderRetiresNothingWhenThePackSetIsIncomplete(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "agentpack", "contentpack")
	writeSkill(t, sources[1], "fromremote", "REMOTE BODY")
	req := composeReq(t)
	stillConfigured(&req, d)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(d.Dir, "fromremote")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the first apply should have delivered it: %v", err)
	}

	// The content pack's remote is unreachable this run: it resolves to nothing, so it contributes
	// no layer — while the agent pack that names the destination still does.
	offline := Destination{Dir: d.Dir, Layers: d.Layers[:1]}
	req.PackSetComplete = false

	res, err := RenderHostSkills([]Destination{offline}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Action == ActionArchived || r.Action == ActionCleared {
			t.Errorf("an offline apply must not retire a merely-unreachable pack's skills: %+v", r)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("the unreachable pack's skill must survive an offline apply — archiving it costs "+
			"the user a trip to the state dir: %v", err)
	}
	if _, recorded := req.Composed.Owner(dest); !recorded {
		t.Error("the record must still name it, or the next apply reads it as the user's own")
	}
}

// An UNRESOLVED layer leaves the WHOLE destination alone. This is where wholesale composition costs
// more than the per-pack delivery did and has to be more careful: there, a pack whose `from` was
// missing merely delivered nothing of its own; here, composing from the remaining layers would
// archive every OTHER pack's skills because one pack's path was misspelled.
func TestComposeLeavesTheDestinationAloneWhenALayerIsUnresolved(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "good", "broken")
	writeSkill(t, sources[0], "kept", "GOOD")
	req := composeReq(t)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, d.Dir)

	// The second layer's declared source is now unreadable.
	d.Layers[1].Sources = nil
	d.Layers[1].Problem = "pack broken declares `skills` from \"nope\", which is not in its content"
	d.Layers[1].Unresolved = true

	res, err := RenderHostSkills([]Destination{d}, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if after := treeSnapshot(t, d.Dir); before != after {
		t.Errorf("an unresolved layer must leave the destination untouched:\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
	var refused bool
	for _, r := range res {
		if r.Action == ActionRefused && strings.Contains(r.Detail, "nope") {
			refused = true
		}
		if r.Action == ActionArchived {
			t.Errorf("an unresolved layer must not cause a retire: %+v", r)
		}
	}
	if !refused {
		t.Errorf("the unresolvable source must be refused BY NAME, never silently: %+v", res)
	}
}

// A destination whose layers all ship NOTHING gets no directory at all. The six shipped agent packs
// declare a `skills` contribution purely to NAME the destination other packs merge into, so
// creating it eagerly would put an empty ~/.codex/skills in every home that selects one.
func TestComposeLeavesNoTraceWhenNoLayerShipsSkills(t *testing.T) {
	d, _ := composeFixture(t, TierFlat, "namesonly")
	res, err := RenderHostSkills([]Destination{d}, composeReq(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("a destination no pack ships into should report nothing, got %+v", res)
	}
	if _, serr := os.Stat(d.Dir); !os.IsNotExist(serr) {
		t.Errorf("%s was created for packs that ship no skills", d.Dir)
	}
}

// OBSERVE WRITES NOTHING and resolves every name the same way the write does. The second half is
// the sharper claim: the claim set is populated in observe too, so a collision two layers both
// reach reports the same winner in both postures.
func TestComposeObserveWritesNothingAndAgreesWithTheWrite(t *testing.T) {
	for _, tier := range []Tier{TierFlat, TierNamespaced} {
		t.Run(tier.String(), func(t *testing.T) {
			d, sources := composeFixture(t, tier, "sflat", "local")
			writeSkill(t, sources[0], "mine", "SHARED BODY")
			writeSkill(t, sources[1], "mine", "LOCAL BODY")
			req := composeReq(t)

			observed, err := RenderHostSkills([]Destination{d}, req, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, serr := os.Stat(d.Dir); !os.IsNotExist(serr) {
				t.Errorf("observe created %s — it must write nothing", d.Dir)
			}
			if len(req.Composed.Entries) != 0 {
				t.Errorf("observe recorded ownership it never took: %v", req.Composed.Entries)
			}
			// Every action a dry run reports must be future-tense, or it reads as though the run
			// mutated the home.
			for _, r := range observed {
				switch r.Action {
				case ActionWrote, ActionArchived, ActionCleared, ActionMoved, ActionUnioned:
					t.Errorf("observe reported a PAST-tense action %q for %q", r.Action, r.Name)
				}
			}
			// The write agrees: same names, same order, same count.
			written, err := RenderHostSkills([]Destination{d}, req, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(observed) != len(written) {
				t.Fatalf("observe reported %d lines and the write %d — a dry run that disagrees "+
					"with the real one is worse than no dry run:\nobserve: %+v\nwrite: %+v",
					len(observed), len(written), observed, written)
			}
			for i := range observed {
				if observed[i].Name != written[i].Name || observed[i].Path != written[i].Path {
					t.Errorf("line %d: observe %q@%q, write %q@%q", i,
						observed[i].Name, observed[i].Path, written[i].Name, written[i].Path)
				}
			}
		})
	}
}

// ADOPTION: an entry yolo cannot prove it composed is the user's, and is named. This is the input to
// the CLI's one-way-door confirmation, and it is the only reason the render ever gets to overwrite
// a directory the user has been keeping skills in.
func TestAdoptionsFindTheUsersOwnSkills(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "packskill", "FROM PACK")
	req := composeReq(t)
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, d.Dir, "mine", "HAND WRITTEN")

	adoptions, _ := Adoptions([]Destination{d}, req)
	if len(adoptions) != 1 || adoptions[0].Name != "mine" {
		t.Fatalf("want exactly the user's own skill, got %+v", adoptions)
	}
}

// yolo's OWN output is never an adoption, from either record. The LEGACY half matters most: without
// it the first apply after an upgrade would offer to migrate every skill a previous yolo delivered
// into the user's local pack, which is the loudest possible way to get the migration wrong.
func TestAdoptionsExcludeYolosOwnOutput(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "packskill", "FROM PACK")
	req := composeReq(t)
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, d.Dir, "composed", "yolo composed this")
	writeSkill(t, d.Dir, "legacy", "an older yolo delivered this")
	// Recorded under a pack name that is NOT the layer being composed, deliberately: the adoption
	// scan must read "is this yolo's?" rather than "is this THIS pack's?", or the local pack's own
	// previously-composed entries would come back as adoptions every apply.
	req.Composed.Record(filepath.Join(d.Dir, "composed"), "someotherpack")
	req.Legacy.Record(filepath.Join(d.Dir, "legacy"), "one")

	if adoptions, _ := Adoptions([]Destination{d}, req); len(adoptions) != 0 {
		t.Errorf("yolo's own output must never be offered for migration, got %+v", adoptions)
	}
}

// A HAND-AUTHORED PLUGIN is not a skill and is not adopted — it is reported and left exactly as it
// is. Moving it into the local pack's skills/ would re-deliver it under a different namespace and
// break the component paths its own manifest declares.
func TestAdoptionsSpareAUserAuthoredPlugin(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "packskill", "FROM PACK")
	req := composeReq(t)
	mine := filepath.Join(d.Dir, "myplugin")
	if err := os.MkdirAll(filepath.Join(mine, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, ".claude-plugin", "plugin.json"),
		[]byte(`{"name":"myplugin","skills":["./"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	adoptions, plugins := Adoptions([]Destination{d}, req)
	if len(adoptions) != 0 {
		t.Errorf("a plugin the user authored must not be migrated as a skill, got %+v", adoptions)
	}
	if len(plugins) != 1 || plugins[0].Action != ActionSkippedUser {
		t.Fatalf("a user-authored plugin must be REPORTED, never silent: %+v", plugins)
	}
	// And the render leaves it alone.
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mine, ".claude-plugin", "plugin.json")); err != nil {
		t.Errorf("the user's plugin must survive the composition: %v", err)
	}
}

// THE MIGRATION, headline case: the user's skill MOVES into the local pack. Behavior-preserving
// rather than merely non-destructive — the point of the ruling.
func TestMigrationMovesTheUsersSkillIntoTheLocalPack(t *testing.T) {
	d, _ := composeFixture(t, TierFlat, "one")
	req := composeReq(t)
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, d.Dir, "mine", "HAND WRITTEN")
	adoptions, _ := Adoptions([]Destination{d}, req)

	res, err := MigrateHostSkills(adoptions, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Action != ActionMoved {
		t.Fatalf("want one MOVE, got %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(req.LocalPackSkills, "mine", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "HAND WRITTEN") {
		t.Fatalf("the skill did not reach the local pack: %v %q", err, data)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "mine")); !os.IsNotExist(err) {
		t.Error("a MOVE must leave nothing behind at the destination — a copy would drift per agent " +
			"again, which is the risk the ruling names")
	}
}

// IDENTICAL CONTENT UNDER ONE NAME IS NOT A COLLISION. Measured before designing (§6a-2): every
// name shared across this jail's four agent skills dirs was byte-identical, so the union resolves
// the common case silently and correctly by comparing CONTENT rather than names.
func TestMigrationUnionsIdenticalDuplicatesSilently(t *testing.T) {
	req := composeReq(t)
	home := t.TempDir()
	a := Destination{Dir: filepath.Join(home, ".codex", "skills")}
	b := Destination{Dir: filepath.Join(home, ".pi", "agent", "skills")}
	for _, d := range []Destination{a, b} {
		if err := os.MkdirAll(d.Dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeSkill(t, d.Dir, "mine", "IDENTICAL")
	}
	adoptions, _ := Adoptions([]Destination{a, b}, req)
	if len(adoptions) != 2 {
		t.Fatalf("both copies must be seen: %+v", adoptions)
	}

	res, err := MigrateHostSkills(adoptions, req, false)
	if err != nil {
		t.Fatal(err)
	}
	var moved, unioned int
	for _, r := range res {
		switch r.Action {
		case ActionMoved:
			moved++
		case ActionUnioned:
			unioned++
		case ActionRenamed:
			t.Errorf("byte-identical copies are not a conflict and must NOT be suffixed: %+v", r)
		}
	}
	if moved != 1 || unioned != 1 {
		t.Errorf("want one move and one union, got %d/%d: %+v", moved, unioned, res)
	}
	// ONE copy in the local pack, under the bare name.
	entries, err := os.ReadDir(req.LocalPackSkills)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "mine" {
		t.Errorf("the union must leave exactly one `mine`, got %v", dirNames(entries))
	}
	// And both destinations are empty of it.
	for _, d := range []Destination{a, b} {
		if _, serr := os.Stat(filepath.Join(d.Dir, "mine")); !os.IsNotExist(serr) {
			t.Errorf("%s still holds a copy: %v", d.Dir, serr)
		}
	}
}

// DIFFERING CONTENT UNDER ONE NAME IS A REAL CONFLICT: both survive, under distinct names, and the
// suffix names the destination the loser came from. Losing one of two hand-written skills silently
// is the failure this whole ruling exists to prevent.
func TestMigrationKeepsBothOnDifferingContent(t *testing.T) {
	req := composeReq(t)
	home := t.TempDir()
	a := Destination{Dir: filepath.Join(home, ".claude", "skills")}
	b := Destination{Dir: filepath.Join(home, ".codex", "skills")}
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, a.Dir, "mine", "CLAUDE VERSION")
	writeSkill(t, b.Dir, "mine", "CODEX VERSION")
	adoptions, _ := Adoptions([]Destination{a, b}, req)

	res, err := MigrateHostSkills(adoptions, req, false)
	if err != nil {
		t.Fatal(err)
	}
	var renamed *Result
	for i := range res {
		if res[i].Action == ActionRenamed {
			renamed = &res[i]
		}
	}
	if renamed == nil {
		t.Fatalf("differing content under one name must be suffixed, not silently resolved: %+v", res)
	}
	// The report names BOTH sources, which is the whole mitigation: the user has to be able to tell
	// which of their agents held which version.
	for _, want := range []string{"mine", "codex"} {
		if !strings.Contains(renamed.Detail, want) {
			t.Errorf("the conflict report must name %q so the user can tell the two apart: %q",
				want, renamed.Detail)
		}
	}
	// BOTH bodies survive in the local pack.
	found := map[string]bool{}
	entries, err := os.ReadDir(req.LocalPackSkills)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, rerr := os.ReadFile(filepath.Join(req.LocalPackSkills, e.Name(), "SKILL.md"))
		if rerr != nil {
			t.Fatal(rerr)
		}
		switch {
		case strings.Contains(string(data), "CLAUDE VERSION"):
			found["claude"] = true
		case strings.Contains(string(data), "CODEX VERSION"):
			found["codex"] = true
		}
	}
	if !found["claude"] || !found["codex"] {
		t.Errorf("BOTH hand-written skills must survive, got %v in %v", dirNames(entries), found)
	}
}

// THE PROPERTY THAT MUST NEVER BREAK: after a migration and a render, nothing the user wrote is
// absent from BOTH the destination and the local pack. Asserted over the mixed fixture — an
// identical duplicate, a differing pair, and a pack's own skill — because that is the shape a real
// home has.
func TestNothingTheUserWroteIsEverAbsentFromBoth(t *testing.T) {
	req := composeReq(t)
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "pack", "skills")
	writeSkill(t, src, "packskill", "FROM PACK")
	dests := []Destination{
		{Dir: filepath.Join(home, ".claude", "skills"),
			Layers: []Layer{{Pack: "p", Tier: TierFlat, Sources: []string{src}}}},
		{Dir: filepath.Join(home, ".codex", "skills"),
			Layers: []Layer{{Pack: "p", Tier: TierFlat, Sources: []string{src}}}},
		{Dir: filepath.Join(home, ".pi", "agent", "skills"),
			Layers: []Layer{{Pack: "p", Tier: TierFlat, Sources: []string{src}}}},
	}
	// Everything the user wrote, and where.
	userBodies := map[string]string{
		"claude/mine":    "CLAUDE VERSION OF MINE",
		"codex/mine":     "CODEX VERSION OF MINE",
		"pi/mine":        "CLAUDE VERSION OF MINE", // identical to claude's — the union case
		"claude/soleown": "ONLY IN CLAUDE",
	}
	for key, content := range userBodies {
		agent, name, _ := strings.Cut(key, "/")
		var dir string
		for _, d := range dests {
			if strings.Contains(d.Dir, "."+agent) {
				dir = d.Dir
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeSkill(t, dir, name, content)
	}

	adoptions, _ := Adoptions(dests, req)
	if _, err := MigrateHostSkills(adoptions, req, false); err != nil {
		t.Fatal(err)
	}
	// The migration created the local pack, which the composition then includes as a LAYER — the
	// re-resolve a real apply does (§6a-6 defect 2). Appended last, so a personal skill outranks a
	// shared pack's.
	for i := range dests {
		dests[i].Layers = append(dests[i].Layers,
			Layer{Pack: "local", Tier: TierFlat, Sources: []string{req.LocalPackSkills}})
	}
	if _, err := RenderHostSkills(dests, req, false); err != nil {
		t.Fatal(err)
	}

	// Every body the user wrote is readable SOMEWHERE — in the local pack, or still at a
	// destination. Nothing is allowed to be absent from both.
	live := readableBodies(t, req.LocalPackSkills)
	for _, d := range dests {
		for k, v := range readableBodies(t, d.Dir) {
			live[k] = v
		}
	}
	for key, content := range userBodies {
		if !live[content] {
			t.Errorf("%q (the body of %s) is absent from BOTH the destination and the local pack — "+
				"that is the one property this whole change may never break. Live bodies: %v",
				content, key, sortedKeysOf(live))
		}
	}
	// And each one reaches EVERY agent, which is the win the ruling is actually after.
	for _, d := range dests {
		got := readableBodies(t, d.Dir)
		for key, content := range userBodies {
			if !got[content] {
				t.Errorf("%s does not carry %s — one local pack composed into every destination is "+
					"the whole point (bodies present: %v)", d.Dir, key, sortedKeysOf(got))
			}
		}
	}
}

// OBSERVE never moves anything, and resolves the union the SAME way the write does. The second half
// is the trap: a dry run that consulted the filesystem would see every target absent (nothing
// having moved yet) and promise the bare name for two entries the write splits.
func TestMigrationObserveWritesNothingAndAgreesWithTheWrite(t *testing.T) {
	build := func(t *testing.T) (ComposeRequest, []Destination) {
		t.Helper()
		req := composeReq(t)
		home := t.TempDir()
		a := Destination{Dir: filepath.Join(home, ".claude", "skills")}
		b := Destination{Dir: filepath.Join(home, ".codex", "skills")}
		for _, d := range []Destination{a, b} {
			if err := os.MkdirAll(d.Dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		writeSkill(t, a.Dir, "mine", "CLAUDE VERSION")
		writeSkill(t, b.Dir, "mine", "CODEX VERSION")
		return req, []Destination{a, b}
	}

	req, dests := build(t)
	adoptions, _ := Adoptions(dests, req)
	before := linkSnapshot(t, filepath.Dir(dests[0].Dir))
	observed, err := MigrateHostSkills(adoptions, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if after := linkSnapshot(t, filepath.Dir(dests[0].Dir)); before != after {
		t.Errorf("observe moved something:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, serr := os.Stat(req.LocalPackSkills); !os.IsNotExist(serr) {
		t.Errorf("observe created the local pack: %v", serr)
	}
	if len(observed) != 2 {
		t.Fatalf("observe must report both adoptions: %+v", observed)
	}
	if observed[0].Action != ActionWouldMove || observed[1].Action != ActionWouldRename {
		t.Errorf("observe must resolve the conflict the way the write will — one move and one "+
			"rename, got %q and %q", observed[0].Action, observed[1].Action)
	}

	// The write, on an identical fresh fixture, agrees action-for-action.
	wreq, wdests := build(t)
	wadoptions, _ := Adoptions(wdests, wreq)
	written, err := MigrateHostSkills(wadoptions, wreq, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range written {
		if got, want := written[i].Name, observed[i].Name; got != want {
			t.Errorf("line %d: write says %q, observe said %q", i, got, want)
		}
	}
}

// With NO LOCAL PACK LOCATION the migration falls back to the ARCHIVE — losing nothing, preserving
// nothing. Named as the fallback so the difference is visible: an archived skill is recoverable, a
// moved one is still being composed into every destination.
func TestMigrationFallsBackToTheArchiveWithNoLocalPack(t *testing.T) {
	d, _ := composeFixture(t, TierFlat, "one")
	req := composeReq(t)
	req.LocalPackSkills = ""
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, d.Dir, "mine", "HAND WRITTEN")
	adoptions, _ := Adoptions([]Destination{d}, req)

	res, err := MigrateHostSkills(adoptions, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Action != ActionArchived {
		t.Fatalf("want one archive, got %+v", res)
	}
	if !strings.Contains(res[0].Detail, "no local pack") {
		t.Errorf("the fallback must say WHY it is the fallback: %q", res[0].Detail)
	}
	// Recoverable, which is the whole reason archiving remains an acceptable fallback.
	at := res[0].Detail[strings.LastIndex(res[0].Detail, "→ ")+len("→ "):]
	if _, serr := os.Stat(filepath.Join(at, "SKILL.md")); serr != nil {
		t.Errorf("the archived skill must be recoverable at %q: %v", at, serr)
	}
}

// THE ORPHAN. A destination NO active pack contributes to any more has its composed content
// archived, so dropping the last contributing pack does not leave content nobody regenerates.
func TestPruneRetiresAnOrphanedDestination(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "one")
	writeSkill(t, sources[0], "packskill", "FROM PACK")
	req := composeReq(t)
	stillConfigured(&req, d)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(d.Dir, "packskill")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the render should have delivered it: %v", err)
	}

	// The destination is gone from the config while its composer is still there: retireComposed
	// against an EMPTY want set is what the prune reduces to for a destination the render no longer
	// visits. (A DROPPED pack's content is ruling R1's, not this pass's — see
	// TestPruneLeavesADroppedPacksContentToTheConfirmedRetire.)
	res := retireComposed(d.Dir, nil, req, false)
	if len(res) != 1 || res[0].Action != ActionArchived {
		t.Fatalf("an orphaned destination's content must be archived: %+v", res)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("the orphaned skill should be gone from the home")
	}
}

// A DROPPED PACK's content is left for ruling R1's CONFIRMED retire, not archived here. The two
// passes reach the same paths, so without the boundary this one gets there first and R1's [y/N] never
// fires — the gate intact, the code simply no longer arriving at it. Found by running the lifecycle.
func TestPruneLeavesADroppedPacksContentToTheConfirmedRetire(t *testing.T) {
	d, sources := composeFixture(t, TierFlat, "gone")
	writeSkill(t, sources[0], "packskill", "FROM THE DROPPED PACK")
	req := composeReq(t)
	if _, err := RenderHostSkills([]Destination{d}, req, false); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(d.Dir, "packskill")

	// The pack has left `packs`, so it is not in the active set.
	res := retireComposed(d.Dir, nil, req, false)
	if len(res) != 0 {
		t.Errorf("a dropped pack's content must be left to the confirmed retire, got %+v", res)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("the content must still be there for the confirmed pass to ask about: %v", err)
	}
	if _, recorded := req.Composed.Owner(dest); !recorded {
		t.Error("the record must still name it — it is the evidence the confirmed retire runs on, " +
			"and dropping it for a path still in the home is the one state yolo cannot recover from")
	}
}

// A NIL ACTIVE SET is REFUSED, not read as "nothing is active": that reading would retire every
// composed destination on a caller bug, which is the one outcome this file exists to prevent.
func TestPruneRefusesAnUnknownActiveSet(t *testing.T) {
	if _, err := PruneHostSkills(nil, nil, t.TempDir(), composeReq(t), false); err == nil {
		t.Error("a nil active set must be refused, not treated as 'no pack is active'")
	}
}

// AN INCOMPLETE PACK SET retires NOTHING (§6a-6 defect 3, found in the sibling kind). A fetched pack
// whose remote is unreachable resolves to nothing, so a destination it was the sole contributor to
// looks orphaned the first time the user is offline — and archiving on that guess costs a trip to
// the state dir.
func TestPruneRetiresNothingWhenThePackSetIsIncomplete(t *testing.T) {
	req := composeReq(t)
	req.PackSetComplete = false
	res, err := PruneHostSkills(nil, map[string]bool{}, t.TempDir(), req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("an incomplete pack set must retire nothing, got %+v", res)
	}
}

// THE CONTENT DIGEST is what makes the union correct, and it must key on more than file bytes: a
// symlink's TARGET and a file's exec bit are part of a skill's identity. Two dotfile-managed copies
// pointing at different sources are different skills; following the links would make them compare
// equal to whatever they happen to point at today.
func TestTreeDigestDistinguishesLinksAndModes(t *testing.T) {
	root := t.TempDir()
	// Written by hand rather than through writeSkill, which embeds the skill's NAME in its
	// frontmatter — so two writeSkill calls never produce identical bytes and the equal-digest half
	// of this test would pass for the wrong reason.
	identical := func(name string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("same body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	base, same := identical("a"), identical("b")
	da, err := treeDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	db, err := treeDigest(same)
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Errorf("identical trees must digest equal, or the union never fires: %q vs %q", da, db)
	}
	// The exec bit changes the identity.
	if err := os.Chmod(filepath.Join(same, "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d, derr := treeDigest(same); derr != nil || d == da {
		t.Errorf("a mode change must change the digest (err=%v)", derr)
	}
	// So does a symlink target.
	l1 := writeSkill(t, root, "l1", "x")
	l2 := writeSkill(t, root, "l2", "x")
	if err := os.Symlink("/one/place", filepath.Join(l1, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/another/place", filepath.Join(l2, "link")); err != nil {
		t.Fatal(err)
	}
	d1, _ := treeDigest(l1)
	d2, _ := treeDigest(l2)
	if d1 == d2 {
		t.Error("two skills whose links point at DIFFERENT sources are different skills — the " +
			"digest must not follow the link")
	}
}

// The suffix names the DESTINATION, derived from the path rather than from the contributing packs:
// the pack list is in config order, so its first entry is whichever pack the user happened to list
// first, which would label a `.codex/skills` conflict after an unrelated pack.
func TestDestinationLabelNamesTheAgent(t *testing.T) {
	for dir, want := range map[string]string{
		"/home/u/.claude/skills":          "claude",
		"/home/u/.pi/agent/skills":        "pi",
		"/home/u/.config/opencode/skills": "opencode",
		// agy's destination, and the INNER segment is right: `antigravity-cli` is the tool, while
		// `.gemini` is the vendor dir it shares with anything else Google ships. Scanning inward from
		// the skills dir is what gets the more specific answer.
		"/home/u/.gemini/antigravity-cli/skills": "antigravity-cli",
		// No agent segment at all: the nearest real one, which is at least a name the user
		// recognizes from the path.
		"/home/u/skills": "u",
	} {
		if got := destinationLabel(dir); got != want {
			t.Errorf("destinationLabel(%q) = %q, want %q", dir, got, want)
		}
	}
}

// dirNames renders a ReadDir result for an error message.
func dirNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// readableBodies is the set of SKILL.md bodies readable anywhere under root, which is how "is the
// user's content still reachable?" is asked without knowing what name it ended up under.
func readableBodies(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi == nil || fi.IsDir() || filepath.Base(p) != "SKILL.md" {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out[line] = true
			}
		}
		return nil
	})
	return out
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

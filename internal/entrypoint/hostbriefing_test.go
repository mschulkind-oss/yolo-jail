package entrypoint

// hostbriefing_test.go pins the §6a contract: yolo COMPOSES a briefing destination wholesale,
// and the prose that was there MOVES into the local pack rather than being lost.
//
// The load-bearing tests are TestHostBriefingFirstApplyDoesNotDuplicateProse (finding F3 is
// dissolved, not fixed — with no append there is nothing to double) and
// TestHostBriefingMigrationMovesProseIntoTheLocalPack (the migration is behavior-PRESERVING, not
// merely non-destructive). Everything else guards a way the composition could lose content it
// does not own.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// briefingPack builds a pack whose root is a temp dir carrying `prose` as AGENTS.md and which
// declares a briefing into `into`. The `after: host:` half is declared too, because that is the
// shape the shipped packs use and the host render must ignore it (§6a: the host no longer
// preserves the user's file in place, so there is nothing to prepend).
func briefingPack(t *testing.T, name, into, prose string) *packload.Pack {
	t.Helper()
	return briefingPackFrom(t, name, into, "AGENTS.md", "AGENTS.md", prose)
}

// briefingPackFrom is briefingPack with the declared `from` and the file actually written split
// apart, so a test can build a pack whose prose lives somewhere non-conventional.
func briefingPackFrom(t *testing.T, name, into, from, file, prose string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	if prose != "" {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(prose), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &packload.Pack{
		Name: name,
		Root: root,
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind:  packdecl.KindBriefing,
			From:  from,
			Into:  into,
			After: "host:" + into,
		}}},
		MayAccessHost: true,
	}
}

// briefingReq builds a request whose record, archive and local pack all live under `home`, so no
// test can reach a real state dir or a real config dir.
func briefingReq(t *testing.T, home string) (HostBriefingRequest, *hostskills.Manifest) {
	t.Helper()
	man := &hostskills.Manifest{Entries: map[string]string{}}
	return HostBriefingRequest{
		Manifest:        man,
		ArchiveRoot:     hostskills.ArchiveRoot(filepath.Join(home, "archive")),
		Stamp:           "20260804-000000",
		LocalPackAGENTS: filepath.Join(home, ".config", "yolo-jail", "local", "AGENTS.md"),
		// The ordinary case: every configured pack resolved. The false path has its own test.
		PackSetComplete: true,
	}, man
}

// readFile lives in hostfiles_test.go — the package's shared read-or-fail helper.

// THE F3 ASSERTION, and the reason the finding is DISSOLVED rather than fixed. A first apply
// against a briefing that already contains the pack's prose verbatim — the overwhelmingly likely
// shape when migrating existing config — must not produce it twice. With wholesale composition
// there is no append, so this is a property of the mechanism rather than a case it handles.
func TestHostBriefingFirstApplyDoesNotDuplicateProse(t *testing.T) {
	home := t.TempDir()
	const prose = "Use rg, never grep -r.\n"
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	// The user's file ALREADY holds exactly the prose they just moved into the pack.
	if err := os.WriteFile(dest, []byte(prose), 0o644); err != nil {
		t.Fatal(err)
	}

	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", prose)
	req, _ := briefingReq(t, home)
	packs := []*packload.Pack{p}
	// The adoption gate fires (the file differs from the composition — it has no provenance
	// header), the prose moves, then the render composes.
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest)
	if len(adoptions) != 1 {
		t.Fatalf("want one adoption for a hand-written destination; got %+v", adoptions)
	}
	if _, err := MigrateHostBriefings(adoptions, req, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}

	got := readFile(t, dest)
	if n := strings.Count(got, "Use rg, never grep -r."); n != 1 {
		t.Errorf("the pack's prose appears %d times — a wholesale composition cannot double "+
			"anything (F3 is dissolved by the mechanism):\n%s", n, got)
	}
	if n := strings.Count(got, "<!-- from pack: matt-core -->"); n != 1 {
		t.Errorf("want exactly 1 provenance header, got %d:\n%s", n, got)
	}
}

// A second --assert is byte-identical, and reported as unchanged rather than as a fresh render.
func TestHostBriefingRenderTwiceIsByteIdentical(t *testing.T) {
	home := t.TempDir()
	packs := []*packload.Pack{briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Pack rule one.\n")}
	req, _ := briefingReq(t, home)
	dest := filepath.Join(home, ".claude", "CLAUDE.md")

	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	first := readFile(t, dest)

	results, err := RenderHostBriefings(packs, home, req, false)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if second := readFile(t, dest); first != second {
		t.Fatalf("second render is not byte-identical:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
	if len(results) != 1 || results[0].Action != "unchanged" {
		t.Errorf("a re-render should report unchanged; got %+v", results)
	}
}

// THE MIGRATION. A hand-written destination's prose lands in the local pack's AGENTS.md, where
// yolo composes it back into every destination — behavior-PRESERVING, not merely
// non-destructive. Nothing is archived on this path, because a move is not a loss.
func TestHostBriefingMigrationMovesProseIntoTheLocalPack(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const userProse = "# My rules\n\nAlways run the tests.\n"
	if err := os.WriteFile(dest, []byte(userProse), 0o644); err != nil {
		t.Fatal(err)
	}

	packs := []*packload.Pack{briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Pack rule one.\n")}
	req, _ := briefingReq(t, home)
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest)
	if len(adoptions) != 1 {
		t.Fatalf("want one adoption; got %+v", adoptions)
	}
	results, err := MigrateHostBriefings(adoptions, req, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Action, "moved your prose into") {
		t.Fatalf("the migration must MOVE, not archive; got %+v", results)
	}
	local := readFile(t, req.LocalPackAGENTS)
	if !strings.Contains(local, "Always run the tests.") {
		t.Errorf("the user's prose did not reach the local pack:\n%s", local)
	}
	if !strings.Contains(local, "<!-- migrated from "+dest+" -->") {
		t.Errorf("the migrated section is unattributed — the union caveat's whole mitigation "+
			"is knowing which file each section came from:\n%s", local)
	}
	// And the destination is then regenerated from the packs alone.
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := readFile(t, dest)
	if !strings.Contains(got, "Pack rule one.") {
		t.Errorf("the destination was not regenerated:\n%s", got)
	}
	if strings.Contains(got, "Always run the tests.") {
		t.Errorf("the user's prose is still in the destination — it MOVED, so this apply's "+
			"output should carry it only via the local pack:\n%s", got)
	}
}

// THE UNION CAVEAT. Several destinations migrating in one pass CONCATENATE into the one local
// AGENTS.md, each attributed, and nothing is dropped. No dedup-by-similarity is attempted, so
// two agents with the same rule yield two near-identical sections — deliberately.
func TestHostBriefingMigrationUnionsSeveralDestinations(t *testing.T) {
	home := t.TempDir()
	claudeDest := filepath.Join(home, ".claude", "CLAUDE.md")
	codexDest := filepath.Join(home, ".codex", "AGENTS.md")
	for path, body := range map[string]string{
		claudeDest: "# Claude rules\n\nShared rule.\n",
		codexDest:  "# Codex rules\n\nShared rule.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	packs := []*packload.Pack{
		briefingPack(t, "claude", ".claude/CLAUDE.md", "Claude pack prose.\n"),
		briefingPack(t, "codex", ".codex/AGENTS.md", "Codex pack prose.\n"),
	}
	req, _ := briefingReq(t, home)
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest)
	if len(adoptions) != 2 {
		t.Fatalf("want two adoptions; got %+v", adoptions)
	}
	if _, err := MigrateHostBriefings(adoptions, req, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	local := readFile(t, req.LocalPackAGENTS)
	for _, want := range []string{"# Claude rules", "# Codex rules",
		"<!-- migrated from " + claudeDest + " -->", "<!-- migrated from " + codexDest + " -->"} {
		if !strings.Contains(local, want) {
			t.Errorf("the union lost %q:\n%s", want, local)
		}
	}
	// NO dedup: the shared rule appears once per source, which is what "leave the editing to
	// the user" means concretely.
	if n := strings.Count(local, "Shared rule."); n != 2 {
		t.Errorf("dedup-by-similarity happened (%d copies of the shared rule) — prose has no "+
			"name to disambiguate, so both must survive:\n%s", n, local)
	}
}

// A migration into a local pack that ALREADY holds prose APPENDS. A user migrating a second
// agent months later must not have the first migration replaced.
func TestHostBriefingMigrationAppendsToAnExistingLocalPack(t *testing.T) {
	home := t.TempDir()
	req, _ := briefingReq(t, home)
	if err := os.MkdirAll(filepath.Dir(req.LocalPackAGENTS), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(req.LocalPackAGENTS, []byte("My earlier prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("Newly migrated prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Pack prose.\n")}
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest)
	if _, err := MigrateHostBriefings(adoptions, req, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	local := readFile(t, req.LocalPackAGENTS)
	for _, want := range []string{"My earlier prose.", "Newly migrated prose."} {
		if !strings.Contains(local, want) {
			t.Errorf("the migration replaced instead of appending (%q missing):\n%s", want, local)
		}
	}
}

// ARCHIVE IS THE FALLBACK, not the answer. With no local-pack location the prose is archived —
// nothing is deleted — and the report SAYS which path ran, because the two differ in whether the
// user's instructions still reach their agents.
func TestHostBriefingMigrationArchivesWhenThereIsNoLocalPack(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("Precious prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Pack prose.\n")}
	req, _ := briefingReq(t, home)
	req.LocalPackAGENTS = "" // no resolvable local pack

	adoptions := HostBriefingAdoptions(packs, home, req.Manifest)
	results, err := MigrateHostBriefings(adoptions, req, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Action, "archived") {
		t.Fatalf("want an archive fallback; got %+v", results)
	}
	if !strings.Contains(results[0].Action, "no local pack") {
		t.Errorf("the fallback must say WHY it archived rather than moved; got %q",
			results[0].Action)
	}
	// Recoverable: the bytes are under the archive root, not gone.
	found := false
	_ = filepath.Walk(filepath.Join(home, "archive"), func(p string, fi os.FileInfo, werr error) error {
		if werr != nil || fi == nil || fi.IsDir() {
			return nil
		}
		if data, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(data), "Precious prose.") {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("the archived prose is not recoverable — nothing may ever be deleted")
	}
}

// AN IDENTICAL FILE IS NOT AN ADOPTION, and neither is one yolo composed before. A confirmation
// that fires when nothing is at stake trains people to answer it blind.
func TestHostBriefingAdoptionsOnlyWhenSomethingIsAtStake(t *testing.T) {
	home := t.TempDir()
	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Pack prose.\n")}
	req, man := briefingReq(t, home)

	// (1) Destination absent — nothing to adopt.
	if got := HostBriefingAdoptions(packs, home, man); len(got) != 0 {
		t.Errorf("an absent destination must not prompt; got %+v", got)
	}

	// (2) After a render, the record proves ownership: regenerating is not an adoption.
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	packs2 := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Pack prose CHANGED.\n")}
	if got := HostBriefingAdoptions(packs2, home, man); len(got) != 0 {
		t.Errorf("a destination yolo composed before must not prompt again; got %+v", got)
	}

	// (3) A file whose content already MATCHES the composition, with no record at all: the user
	// moved their prose into a pack by hand, or the state dir was pruned. Nothing is lost.
	fresh := t.TempDir()
	freshReq, freshMan := briefingReq(t, fresh)
	composed := ComposeHostBriefings(packs, fresh)
	if len(composed) != 1 {
		t.Fatalf("want one destination; got %+v", composed)
	}
	if err := os.MkdirAll(filepath.Dir(composed[0].Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composed[0].Path, []byte(composed[0].Content), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = freshReq
	if got := HostBriefingAdoptions(packs, fresh, freshMan); len(got) != 0 {
		t.Errorf("an identical file must not prompt — nothing would be lost; got %+v", got)
	}
}

// Two packs sharing one destination COMPOSE into one file, in pack order, each attributed. This
// is the `briefing` kind's CombineConcat footprint at the host notch.
func TestHostBriefingTwoPacksComposeOneFile(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	packs := []*packload.Pack{
		briefingPack(t, "pack-a", ".claude/CLAUDE.md", "A prose.\n"),
		briefingPack(t, "pack-b", ".claude/CLAUDE.md", "B prose.\n"),
	}
	req, _ := briefingReq(t, home)
	results, err := RenderHostBriefings(packs, home, req, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("two packs at one destination is ONE file, not two renders; got %+v", results)
	}
	got := readFile(t, dest)
	for _, want := range []string{"A prose.", "B prose.",
		"<!-- from pack: pack-a -->", "<!-- from pack: pack-b -->"} {
		if !strings.Contains(got, want) {
			t.Errorf("the composition lost %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "A prose.") > strings.Index(got, "B prose.") {
		t.Errorf("packs must compose in pack order (a then b):\n%s", got)
	}
	// The surface id names both contributors, so a merged file says whose prose it holds.
	if !strings.Contains(results[0].Surface, "pack-a") || !strings.Contains(results[0].Surface, "pack-b") {
		t.Errorf("the report line must name both contributors; got %q", results[0].Surface)
	}
}

// A DESTINATION WITH NO CONTRIBUTED PROSE IS LEFT ALONE, not emptied. The six shipped agent
// packs are exactly this shape: their `briefing` names the destination and the content comes
// from the user's own packs.
func TestHostBriefingNoProseLeavesTheFileAlone(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# Mine\n\nUntouched.\n"
	if err := os.WriteFile(dest, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "")}
	req, _ := briefingReq(t, home)

	// No adoption either: there is nothing yolo would write, so nothing is at stake.
	if got := HostBriefingAdoptions(packs, home, req.Manifest); len(got) != 0 {
		t.Errorf("a pack that ships no prose must not prompt; got %+v", got)
	}
	results, err := RenderHostBriefings(packs, home, req, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "skipped:") {
		t.Fatalf("want a skip when no pack contributes prose; got %+v", results)
	}
	if got := readFile(t, dest); got != mine {
		t.Errorf("a destination with no contributed prose must not be truncated:\n%s", got)
	}
}

// OBSERVE writes NOTHING, on every path: the render, the migration, and the retire.
func TestHostBriefingObserveWritesNothing(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# Mine\n"
	if err := os.WriteFile(dest, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Pack prose.\n")}
	req, man := briefingReq(t, home)

	adoptions := HostBriefingAdoptions(packs, home, man)
	mres, err := MigrateHostBriefings(adoptions, req, true)
	if err != nil {
		t.Fatalf("observe migrate: %v", err)
	}
	if len(mres) != 1 || !strings.HasPrefix(mres[0].Action, "would move") {
		t.Fatalf("want a 'would move' preview; got %+v", mres)
	}
	results, err := RenderHostBriefings(packs, home, req, true)
	if err != nil {
		t.Fatalf("observe render: %v", err)
	}
	if len(results) != 1 || results[0].Action != "would render" {
		t.Fatalf("want one 'would render'; got %+v", results)
	}
	if got := readFile(t, dest); got != mine {
		t.Errorf("observe modified the destination:\n%s", got)
	}
	if _, err := os.Stat(req.LocalPackAGENTS); !os.IsNotExist(err) {
		t.Errorf("observe created the local pack's AGENTS.md (stat err=%v)", err)
	}
	if len(man.Entries) != 0 {
		t.Errorf("observe recorded ownership it never asserted: %v", man.Entries)
	}
}

// THE ORPHAN CASE. Dropping the last pack that contributes to a destination means yolo no longer
// owns that file, so it is ARCHIVED rather than left behind with nobody to regenerate it. A
// destination another pack still contributes to survives.
func TestHostBriefingRetireArchivesTheOrphanedDestination(t *testing.T) {
	home := t.TempDir()
	shared := ".claude/CLAUDE.md"
	a := briefingPack(t, "pack-a", shared, "A prose.\n")
	b := briefingPack(t, "pack-b", shared, "B prose.\n")
	solo := briefingPack(t, "pack-solo", ".codex/AGENTS.md", "Solo prose.\n")
	all := []*packload.Pack{a, b, solo}
	req, man := briefingReq(t, home)
	if _, err := RenderHostBriefings(all, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	sharedPath := filepath.Join(home, ".claude", "CLAUDE.md")
	soloPath := filepath.Join(home, ".codex", "AGENTS.md")

	// pack-b and pack-solo leave the config. The SHARED destination survives (pack-a still
	// contributes); pack-solo's is an orphan.
	active := map[string]bool{"pack-a": true}
	results, err := PruneHostBriefings(all, active, home, req, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "archived") {
		t.Fatalf("want exactly one archive (pack-solo's destination); got %+v", results)
	}
	if results[0].Path != soloPath {
		t.Errorf("the wrong destination was retired: %q", results[0].Path)
	}
	if _, err := os.Lstat(soloPath); err == nil {
		t.Error("the orphaned destination is still in the home — a generated file with no owner")
	}
	if _, err := os.Lstat(sharedPath); err != nil {
		t.Errorf("a destination another pack still contributes to must survive: %v", err)
	}
	// The record no longer claims a path that is gone, or the next apply reports it forever.
	if _, recorded := man.Owner(soloPath); recorded {
		t.Error("the record still names the archived destination")
	}
	// And the prune is idempotent.
	again, err := PruneHostBriefings(all, active, home, req, false)
	if err != nil {
		t.Fatalf("second prune: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a second prune found something to do: %+v", again)
	}
}

// A destination yolo NEVER COMPOSED is not retirable, whatever pack names it. A prune with no
// ownership evidence is a prune with no authority.
func TestHostBriefingRetireSparesAFileYoloNeverWrote(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# Mine alone\n"
	if err := os.WriteFile(dest, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	p := briefingPack(t, "pack-a", ".claude/CLAUDE.md", "A prose.\n")
	req, _ := briefingReq(t, home)

	results, err := PruneHostBriefings([]*packload.Pack{p}, map[string]bool{}, home, req, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("a file yolo never composed must not be retired; got %+v", results)
	}
	if got := readFile(t, dest); got != mine {
		t.Errorf("the user's file was modified:\n%s", got)
	}
	// And a nil active set is REFUSED rather than read as "nothing is active" — that reading
	// would archive every composed destination on a caller bug.
	if _, err := PruneHostBriefings([]*packload.Pack{p}, nil, home, req, false); err == nil {
		t.Error("a nil active set must be refused, not treated as 'no pack is active'")
	}
}

// A pack that stops shipping PROSE has its destination retired too — "the pack was dropped" and
// "the pack stopped shipping a briefing" must not leave different residue. The old mechanism had
// this in its empty-prose branch; wholesale composition gets it from the prune reading COMPOSED
// content rather than declared destinations.
func TestHostBriefingRetireArchivesADestinationWhoseProseWentAway(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	req, _ := briefingReq(t, home)
	if _, err := RenderHostBriefings(
		[]*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Prose.\n")},
		home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	// Same pack, still ACTIVE, now shipping nothing.
	silent := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "")}
	results, err := PruneHostBriefings(silent, map[string]bool{"claude": true}, home, req, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "archived") {
		t.Fatalf("want the destination retired when no prose composes into it; got %+v", results)
	}
	if _, err := os.Lstat(dest); err == nil {
		t.Error("a generated file with nothing left to generate it is an orphan")
	}
}

// AN UNRESOLVABLE PACK IS NOT A DROPPED PACK. A fetched pack whose remote is unreachable
// contributes nothing to the composition, so its destination looks orphaned — and archiving it
// would cost the user a trip to the state dir the first time they are offline. The old delimited
// block could afford that mistake (it re-rendered from prose inside the pack); a wholesale file
// cannot, so the threshold moved to match the skills one.
func TestHostBriefingRetireRefusesAnIncompletePackSet(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	req, _ := briefingReq(t, home)
	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Prose.\n")}
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	// The pack is still configured but did not resolve this run: absent from `active`, and the
	// set is not complete.
	req.PackSetComplete = false
	results, err := PruneHostBriefings(packs, map[string]bool{}, home, req, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("an incomplete pack set must retire nothing; got %+v", results)
	}
	if _, err := os.Lstat(dest); err != nil {
		t.Errorf("the destination of an unresolvable pack must survive: %v", err)
	}
}

// `from` IS HONORED at the host, including a non-conventional one. This is the half that always
// worked; §6a-4's fix is the jail half, and this pins the contract both notches now share.
func TestHostBriefingHonorsDeclaredFrom(t *testing.T) {
	home := t.TempDir()
	p := briefingPackFrom(t, "housed", ".claude/CLAUDE.md", "house-rules.md", "house-rules.md",
		"House rules prose.\n")
	req, _ := briefingReq(t, home)
	if _, err := RenderHostBriefings([]*packload.Pack{p}, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md")); !strings.Contains(
		got, "House rules prose.") {
		t.Errorf("a declared non-conventional `from` was not read:\n%s", got)
	}
}

// TWO CONTRIBUTIONS, TWO DESTINATIONS, one pack. The host render is per-destination, so a pack
// declaring two briefings with two different sources delivers both — a capability the jail's
// one-text-per-pack composition does not have (see packload.BriefingProse).
func TestHostBriefingRendersEveryContributionOfOnePack(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	for name, body := range map[string]string{
		"AGENTS.md":      "General prose.\n",
		"house-rules.md": "House rules prose.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &packload.Pack{Name: "two", Root: root, MayAccessHost: true,
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md"},
			{Kind: packdecl.KindBriefing, From: "house-rules.md", Into: ".codex/AGENTS.md"},
		}}}
	req, _ := briefingReq(t, home)
	results, err := RenderHostBriefings([]*packload.Pack{p}, home, req, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want one result per destination; got %+v", results)
	}
	if got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md")); !strings.Contains(
		got, "General prose.") {
		t.Errorf("the conventional contribution did not deliver:\n%s", got)
	}
	if got := readFile(t, filepath.Join(home, ".codex", "AGENTS.md")); !strings.Contains(
		got, "House rules prose.") {
		t.Errorf("the second contribution's declared `from` did not deliver:\n%s", got)
	}
}

// A shipped pack renders at the host through the same entry yolo host apply calls. The one test
// here that exercises real pack data, so a manifest change that breaks host briefings is caught.
func TestHostBriefingShippedClaudePack(t *testing.T) {
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	home := t.TempDir()
	req, _ := briefingReq(t, home)
	results, err := RenderHostBriefings([]*packload.Pack{claude}, home, req, false)
	if err != nil {
		t.Fatalf("RenderHostBriefings: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("claude declares one briefing destination; got %+v", results)
	}
	// The shipped claude pack has no AGENTS.md of its own today, so the honest outcome is a
	// skip — NOT a written file and not a silent absence. If a pack file is added, this flips
	// to "rendered" and the assertion below documents which it was.
	switch results[0].Action {
	case "rendered":
		if got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md")); !strings.Contains(
			got, "<!-- from pack: claude -->") {
			t.Errorf("the rendered file is missing claude's provenance header:\n%s", got)
		}
	default:
		if !strings.HasPrefix(results[0].Action, "skipped:") {
			t.Errorf("want rendered or skipped, got %q", results[0].Action)
		}
		if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
			t.Errorf("a pack with no prose must not create the user's briefing (stat err=%v)", err)
		}
	}
}

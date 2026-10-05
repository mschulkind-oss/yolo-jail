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
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// briefingPack builds a pack whose root is a temp dir carrying `prose` as briefing/prose.md — the
// conventional source (docs/reference/pack-system.md#briefing-directory; a root AGENTS.md is never read) — and which
// declares a briefing into `into`, `from` omitted. The `after: host:` half is declared too, naming
// the destination itself, because that is the shape the shipped packs use and the host render
// must not read it: that file is the one this composition writes (hostBriefingOverlay's skip rule,
// S3), so prepending it would read yolo's output back in.
func briefingPack(t *testing.T, name, into, prose string) *packload.Pack {
	t.Helper()
	return briefingPackFrom(t, name, into, "", "briefing/prose.md", prose)
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
	}
}

// briefingReq builds a request whose record, archive and local pack all live under `home`, so no
// test can reach a real state dir or a real config dir.
//
// ITS BASE IS EMPTY, which no production caller asks for: `yolo host apply` always composes the
// host base (hostBriefingReq). Empty composes the packs' sections alone, so a test of the section
// mechanics — order, labels, `from`, the migration — can compare exact bytes without restating
// the header. A test of what a destination with no prose does, or of what the prune retires,
// must use hostBriefingReq: those are the answers the base changes.
func briefingReq(t *testing.T, home string) (HostBriefingRequest, *hostskills.Manifest) {
	t.Helper()
	man := &hostskills.Manifest{Entries: map[string]string{}}
	return HostBriefingRequest{
		Manifest:          man,
		ArchiveRoot:       hostskills.ArchiveRoot(filepath.Join(home, "archive")),
		Stamp:             "20260804-000000",
		LocalPackBriefing: filepath.Join(localPackDir(home), filepath.FromSlash(LocalPackBriefingRel)),
		// The ordinary case: every configured pack resolved. The false path has its own test.
		PackSetComplete: true,
	}, man
}

// hostBriefingReq is briefingReq with the production base — the one `yolo host apply` composes
// when the user sets no agents_md_extra (applyHostBriefings).
func hostBriefingReq(t *testing.T, home string) (HostBriefingRequest, *hostskills.Manifest) {
	t.Helper()
	req, man := briefingReq(t, home)
	req.Base = jailcontent.HostBriefingBase("", false)
	return req, man
}

// localPackDir is the conventional local pack's root under a TEMP home.
func localPackDir(home string) string {
	return filepath.Join(home, ".config", "yolo-jail", "local")
}

// readFile lives in hostfiles_test.go — the package's shared read-or-fail helper.

// THE F3 ASSERTION, and the reason the finding is DISSOLVED rather than fixed. A first apply
// against a briefing that already contains the pack's prose verbatim — the overwhelmingly likely
// shape when migrating existing config — must not produce it twice. With wholesale composition
// there is no append, so this is a property of the mechanism rather than a case it handles.
//
// UNLABELLED (the default), the user's file IS the composition, so there is nothing to adopt:
// `HostBriefingAdoptions`' own rule that an identical file is not an adoption. This case used to
// reach the adoption gate only because the provenance label made the two differ.
func TestHostBriefingFirstApplyDoesNotDuplicateProse(t *testing.T) {
	home, dest, packs := f3Home(t)
	req, man := briefingReq(t, home)
	if adoptions := HostBriefingAdoptions(packs, home, req.Manifest, "", false, nil); len(adoptions) != 0 {
		t.Fatalf("a file identical to the composition is not an adoption; got %+v", adoptions)
	}
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := readFile(t, dest)
	if got != f3Prose {
		t.Errorf("the render must leave an identical file identical:\n got %q\nwant %q", got, f3Prose)
	}
	// Recorded as owned, so the NEXT apply regenerates it without a prompt.
	if owner, ok := man.Owner(dest); !ok || owner != HostBriefingOwner {
		t.Errorf("an identical file must still be recorded as yolo's after the render")
	}
}

// The same home with briefing_provenance ON: the label makes the user's file differ from the
// composition, so the adoption gate fires, the prose moves, and the render still writes it once.
func TestHostBriefingFirstApplyDoesNotDuplicateProseWhenLabelled(t *testing.T) {
	home, dest, packs := f3Home(t)
	req, _ := briefingReq(t, home)
	req.Provenance = true
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest, "", true, nil)
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
		t.Errorf("want exactly 1 provenance label, got %d:\n%s", n, got)
	}
}

const f3Prose = "Use rg, never grep -r.\n"

// f3Home is a home whose ~/.claude/CLAUDE.md already holds exactly the prose the user just moved
// into a pack.
func f3Home(t *testing.T) (home, dest string, packs []*packload.Pack) {
	t.Helper()
	home = t.TempDir()
	dest = filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(f3Prose), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, dest, []*packload.Pack{briefingPack(t, "matt-core", ".claude/CLAUDE.md", f3Prose)}
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

// THE MIGRATION. A hand-written destination's prose lands in the local pack's briefing/local.md,
// where yolo composes it back into every destination — behavior-PRESERVING, not merely
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
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest, "", false, nil)
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
	// VERBATIM and UNANNOTATED. The local pack is composed into every agent's briefing, so a
	// provenance marker written here (the old `<!-- migrated from … -->`) would reach the agent
	// as a label on the user's own rules. The apply report carries provenance instead.
	local := readFile(t, req.LocalPackBriefing)
	if local != userProse {
		t.Errorf("the local pack must hold the user's prose verbatim, with no marker:\n"+
			"got  %q\nwant %q", local, userProse)
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
	// And the LOCAL PACK DELIVERS IT: loaded as the convention loads it (no manifest), the file
	// the migration wrote is one the readers read. A migration into a file no reader reads — the
	// root AGENTS.md it used to write — would pass every assertion above and still take the
	// user's instructions away from their agents.
	localPack, problems := packload.LoadDir(localPackDir(home), "local")
	if len(problems) != 0 {
		t.Fatalf("the migrated local pack does not load clean: %v", problems)
	}
	resolved, _ := packload.ResolveDestinations(append(packs, localPack))
	if _, err := RenderHostBriefings(resolved, home, req, false); err != nil {
		t.Fatalf("render with the local pack: %v", err)
	}
	if got := readFile(t, dest); !strings.Contains(got, "Always run the tests.") ||
		!strings.Contains(got, "Pack rule one.") {
		t.Errorf("the local pack does not compose the migrated prose back into the "+
			"destination:\n%s", got)
	}
}

// THE UNION CAVEAT. Several destinations migrating in one pass CONCATENATE into the one local
// briefing file, in adoption order, separated by one blank line exactly as composed pack prose is,
// and nothing is dropped. No marker names the source of each section (the apply report does),
// and no dedup-by-similarity is attempted, so two agents with the same rule yield two
// near-identical sections — deliberately.
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
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest, "", false, nil)
	if len(adoptions) != 2 {
		t.Fatalf("want two adoptions; got %+v", adoptions)
	}
	if _, err := MigrateHostBriefings(adoptions, req, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	local := readFile(t, req.LocalPackBriefing)
	const want = "# Claude rules\n\nShared rule.\n\n# Codex rules\n\nShared rule.\n"
	if local != want {
		t.Errorf("the union is not the adopted sections verbatim, in order, one blank line "+
			"apart:\ngot  %q\nwant %q", local, want)
	}
	for _, need := range []string{"# Claude rules", "# Codex rules"} {
		if !strings.Contains(local, need) {
			t.Errorf("the union lost %q:\n%s", need, local)
		}
	}
	if strings.Contains(local, "<!--") {
		t.Errorf("the union carries a comment — the local pack must not annotate the user's "+
			"prose:\n%s", local)
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
	if err := os.MkdirAll(filepath.Dir(req.LocalPackBriefing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(req.LocalPackBriefing, []byte("My earlier prose.\n"), 0o644); err != nil {
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
	adoptions := HostBriefingAdoptions(packs, home, req.Manifest, "", false, nil)
	if _, err := MigrateHostBriefings(adoptions, req, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	local := readFile(t, req.LocalPackBriefing)
	for _, want := range []string{"My earlier prose.", "Newly migrated prose."} {
		if !strings.Contains(local, want) {
			t.Errorf("the migration replaced instead of appending (%q missing):\n%s", want, local)
		}
	}
	if want := "My earlier prose.\n\nNewly migrated prose.\n"; local != want {
		t.Errorf("the appended section is not the user's prose one blank line after the "+
			"existing content, unannotated:\ngot  %q\nwant %q", local, want)
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
	req.LocalPackBriefing = "" // no resolvable local pack

	adoptions := HostBriefingAdoptions(packs, home, req.Manifest, "", false, nil)
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
	if got := HostBriefingAdoptions(packs, home, man, "", false, nil); len(got) != 0 {
		t.Errorf("an absent destination must not prompt; got %+v", got)
	}

	// (2) After a render, the record proves ownership: regenerating is not an adoption.
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	packs2 := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "Pack prose CHANGED.\n")}
	if got := HostBriefingAdoptions(packs2, home, man, "", false, nil); len(got) != 0 {
		t.Errorf("a destination yolo composed before must not prompt again; got %+v", got)
	}

	// (3) A file whose content already MATCHES the composition, with no record at all: the user
	// moved their prose into a pack by hand, or the state dir was pruned. Nothing is lost.
	fresh := t.TempDir()
	freshReq, freshMan := briefingReq(t, fresh)
	composed := ComposeHostBriefings(packs, fresh, "", false)
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
	if got := HostBriefingAdoptions(packs, fresh, freshMan, "", false, nil); len(got) != 0 {
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
	// Exact bytes: two sections, one blank line between, and no label — briefing_provenance is
	// off by default, and the jail's ComposePackBriefings produces the same bytes.
	if got != "A prose.\n\nB prose.\n" {
		t.Errorf("two-pack composition:\n got %q\nwant %q", got, "A prose.\n\nB prose.\n")
	}
	if strings.Index(got, "A prose.") > strings.Index(got, "B prose.") {
		t.Errorf("packs must compose in pack order (a then b):\n%s", got)
	}
	// The surface id names both contributors, so a merged file says whose prose it holds.
	if !strings.Contains(results[0].Surface, "pack-a") || !strings.Contains(results[0].Surface, "pack-b") {
		t.Errorf("the report line must name both contributors; got %q", results[0].Surface)
	}
}

// A DESTINATION WITH NO CONTRIBUTED PROSE IS STILL YOLO'S. The shipped agent packs are this
// shape: their `briefing` names the destination and ships no prose. With the host base every
// such destination composes the base, so an absent file is created holding it, and a file the
// user wrote there is an ADOPTION the CLI confirms and migrates first — never left alone, which
// was the answer before the base existed and left a host agent unaware it was on a real machine.
func TestHostBriefingNoProseDestinationComposesTheBase(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	packs := []*packload.Pack{briefingPack(t, "claude", ".claude/CLAUDE.md", "")}
	req, man := hostBriefingReq(t, home)

	results, err := RenderHostBriefings(packs, home, req, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(results) != 1 || results[0].Action != "rendered" {
		t.Fatalf("a prose-less destination must be rendered from the base; got %+v", results)
	}
	if got := readFile(t, dest); got != req.Base {
		t.Errorf("the destination must hold the base alone:\n got %q\nwant %q", got, req.Base)
	}
	if owner, ok := man.Owner(dest); !ok || owner != HostBriefingOwner {
		t.Error("the composed destination must be recorded as yolo's")
	}

	// The user's own file at the same path is what the adoption gate names.
	fresh := t.TempDir()
	mine := filepath.Join(fresh, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("# Mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshReq, _ := hostBriefingReq(t, fresh)
	got := HostBriefingAdoptions(packs, fresh, freshReq.Manifest, freshReq.Base, false, nil)
	if len(got) != 1 || got[0].Path != mine || got[0].Existing != "# Mine\n" {
		t.Errorf("the user's file under a prose-less destination must be an adoption; got %+v", got)
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

	adoptions := HostBriefingAdoptions(packs, home, man, "", false, nil)
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
	if _, err := os.Stat(req.LocalPackBriefing); !os.IsNotExist(err) {
		t.Errorf("observe created the local pack's briefing file (stat err=%v)", err)
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

// A PACK THAT STOPS SHIPPING PROSE KEEPS ITS DESTINATION: the pack is still selected, the base
// still composes there, so the destination is recomposed rather than archived. Only dropping the
// pack retires it (TestHostBriefingRetireArchivesTheOrphanedDestination; the CLI half
// is TestApplyHostBriefingDroppingThePackLeavesNoOrphan). Before the base, an empty composition
// made "stopped shipping prose" retire the file, and a host agent lost its briefing with it.
func TestHostBriefingPruneKeepsADestinationWhosePackStoppedShippingProse(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	req, _ := hostBriefingReq(t, home)
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
	if len(results) != 0 {
		t.Fatalf("a still-selected pack's destination must not be retired; got %+v", results)
	}
	if _, err := RenderHostBriefings(silent, home, req, false); err != nil {
		t.Fatalf("re-render: %v", err)
	}
	if got := readFile(t, dest); got != req.Base {
		t.Errorf("the destination must be recomposed from the base, the prose gone:\n got %q\nwant %q",
			got, req.Base)
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
		"briefing/general.md": "General prose.\n",
		"house-rules.md":      "House rules prose.\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &packload.Pack{Name: "two", Root: root, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
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
	req, _ := hostBriefingReq(t, home)
	results, err := RenderHostBriefings([]*packload.Pack{claude}, home, req, false)
	if err != nil {
		t.Fatalf("RenderHostBriefings: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("claude declares one briefing destination; got %+v", results)
	}
	// With the production base, claude's destination is always written, prose or none, and
	// opens with the host header telling the agent it is on the real machine.
	if results[0].Action != "rendered" {
		t.Fatalf("want claude's destination rendered, got %q", results[0].Action)
	}
	if got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md")); !strings.HasPrefix(got, req.Base) {
		t.Errorf("claude's host briefing must open with the host base:\n%s", got)
	}
}

// THE ROUND TRIP THE TWO NOTCHES SHARE. `yolo host apply` records what it composed; the run
// pipeline asks GeneratedHostBriefings whether a `after: "host:<path>"` source is one of those,
// so a jail does not prepend yolo's own output to a briefing it is about to compose the same
// packs into (the briefing half of S3 — see GeneratedHostBriefings).
//
// Pinned as a ROUND TRIP rather than as two unit tests, because the failure mode is neither
// half being wrong: it is the writer and the reader keying the record differently, or reading
// two files. Both are invisible to a test that only exercises one side.
func TestGeneratedHostBriefingsSeesWhatTheHostRenderRecorded(t *testing.T) {
	home := t.TempDir()
	packs := []*packload.Pack{briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Prefer rg.\n")}
	req, man := briefingReq(t, home)
	if _, err := RenderHostBriefings(packs, home, req, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := man.Save(HostBriefingManifestPath(home)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if got := GeneratedHostBriefings(home); !got[dest] {
		t.Errorf("the run pipeline cannot tell yolo's own composition from the user's file — "+
			"%s is missing from %v; a jail will prepend it and deliver every pack twice",
			dest, got)
	}
}

// A destination yolo never composed is the user's, and must NOT be reported as generated —
// otherwise the fix for the doubling would silently drop a hand-written briefing instead.
func TestGeneratedHostBriefingsExcludesTheUsersOwnFile(t *testing.T) {
	home := t.TempDir()
	man := &hostskills.Manifest{Entries: map[string]string{}}
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := man.Save(HostBriefingManifestPath(home)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if got := GeneratedHostBriefings(home); got[dest] {
		t.Errorf("an unrecorded destination was reported as yolo's own output: %v", got)
	}
}

// An ABSENT record proves nothing and must fail OPEN — an empty set, so every destination
// reads as the user's and is prepended as before. Losing the user's instructions from their
// jail is a worse failure than repeating a pack's prose.
func TestGeneratedHostBriefingsFailsOpenWithNoRecord(t *testing.T) {
	if got := GeneratedHostBriefings(t.TempDir()); len(got) != 0 {
		t.Errorf("want an empty set with no record on disk, got %v", got)
	}
}

// writeLegacyLocalPack writes `prose` as the local pack's ROOT AGENTS.md — where an earlier yolo
// migrated the user's prose — and returns the pack dir and the two paths the move concerns.
func writeLegacyLocalPack(t *testing.T, prose string) (dir, legacy, target string) {
	t.Helper()
	dir = localPackDir(t.TempDir())
	legacy = filepath.Join(dir, "AGENTS.md")
	target = filepath.Join(dir, "briefing", "local.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(prose), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, legacy, target
}

// THE MOVE (docs/reference/pack-system.md#local-pack-briefing-move). The local pack's root AGENTS.md is no longer read as
// pack prose, so yolo moves the file it put there into briefing/ — verbatim — and the local pack
// then delivers it again. Reported, naming both files.
func TestMoveLegacyLocalPackBriefingMovesIntoBriefing(t *testing.T) {
	const prose = "# My rules\n\nAlways run the tests.\n"
	dir, legacy, target := writeLegacyLocalPack(t, prose)

	res, err := MoveLegacyLocalPackBriefing(dir, false)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if res == nil || !res.WouldChange || !strings.Contains(res.Action, "moved") ||
		!strings.Contains(res.Action, legacy) || !strings.Contains(res.Action, target) {
		t.Fatalf("the move must be reported, naming both files; got %+v", res)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Errorf("the root AGENTS.md is still there after the move (stat err=%v)", err)
	}
	if got := readFile(t, target); got != prose {
		t.Errorf("the moved prose is not verbatim:\ngot  %q\nwant %q", got, prose)
	}
	// The point of the move: the local pack's readers now read it.
	p, problems := packload.LoadDir(dir, "local")
	if len(problems) != 0 {
		t.Fatalf("the moved local pack does not load clean: %v", problems)
	}
	srcs, _ := p.GovernedSources(packdecl.KindBriefing)
	if len(srcs) != 1 || !srcs[0].Implicit || srcs[0].Rel != LocalPackBriefingRel {
		t.Errorf("the moved file is not the local pack's implicit broadcast; got %+v", srcs)
	}
	// And a second run has nothing left to do.
	if res, err := MoveLegacyLocalPackBriefing(dir, false); res != nil || err != nil {
		t.Errorf("a second move must be a no-op; got %+v, %v", res, err)
	}
}

// The migration writer and the move name ONE file, so a migration after the move appends to the
// moved prose rather than starting a second file whose join order nobody chose.
func TestLocalPackBriefingRelIsWhereTheMigrationWrites(t *testing.T) {
	home := t.TempDir()
	req, _ := briefingReq(t, home)
	want := filepath.Join(localPackDir(home), "briefing", "local.md")
	if req.LocalPackBriefing != want {
		t.Fatalf("fixture drift: %q", req.LocalPackBriefing)
	}
	if packdecl.RepositoryInstructionFile(LocalPackBriefingRel) {
		t.Errorf("%s is a reserved basename; LoadDir would refuse the local pack", LocalPackBriefingRel)
	}
	if !packdecl.ConventionalBriefingFile(LocalPackBriefingRel) {
		t.Errorf("%s is not in the conventional briefing source, so nothing reads it",
			LocalPackBriefingRel)
	}
}

// THE TARGET IS TAKEN: refused, naming both files, and NEITHER is touched — which text wins is
// the user's decision (pack-system.md#local-pack-briefing-move: "refuses, naming both files, rather than choosing one").
func TestMoveLegacyLocalPackBriefingRefusesWhenTheTargetExists(t *testing.T) {
	dir, legacy, target := writeLegacyLocalPack(t, "Old prose.\n")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("Newer prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, observe := range []bool{true, false} {
		res, err := MoveLegacyLocalPackBriefing(dir, observe)
		if err == nil {
			t.Fatalf("observe=%v: a taken target must refuse; got %+v", observe, res)
		}
		for _, name := range []string{legacy, target} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("observe=%v: the refusal must name %s: %v", observe, name, err)
			}
		}
		if res == nil || res.WouldChange || !strings.HasPrefix(res.Action, "refused") {
			t.Errorf("observe=%v: want a refused result that changes nothing; got %+v", observe, res)
		}
		if got := readFile(t, legacy); got != "Old prose.\n" {
			t.Errorf("observe=%v: the refusal touched %s: %q", observe, legacy, got)
		}
		if got := readFile(t, target); got != "Newer prose.\n" {
			t.Errorf("observe=%v: the refusal touched %s: %q", observe, target, got)
		}
	}
}

// OBSERVE writes nothing, and still says what the move would do.
func TestMoveLegacyLocalPackBriefingObserveWritesNothing(t *testing.T) {
	dir, legacy, target := writeLegacyLocalPack(t, "Prose.\n")
	res, err := MoveLegacyLocalPackBriefing(dir, true)
	if err != nil || res == nil || !res.WouldChange || !strings.HasPrefix(res.Action, "would move") {
		t.Fatalf("want a 'would move' preview; got %+v, %v", res, err)
	}
	if got := readFile(t, legacy); got != "Prose.\n" {
		t.Errorf("observe changed %s: %q", legacy, got)
	}
	if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Errorf("observe created %s (stat err=%v)", filepath.Dir(target), err)
	}
}

// NOTHING TO MOVE is silent: no local pack, no AGENTS.md, or an AGENTS.md that never delivered
// prose (a directory, a dangling link).
func TestMoveLegacyLocalPackBriefingNothingToMove(t *testing.T) {
	if res, err := MoveLegacyLocalPackBriefing("", false); res != nil || err != nil {
		t.Errorf("no local pack location: got %+v, %v", res, err)
	}
	if res, err := MoveLegacyLocalPackBriefing(filepath.Join(t.TempDir(), "absent"), false); res != nil || err != nil {
		t.Errorf("no local pack: got %+v, %v", res, err)
	}
	dir := localPackDir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res, err := MoveLegacyLocalPackBriefing(dir, false); res != nil || err != nil {
		t.Errorf("a directory named AGENTS.md: got %+v, %v", res, err)
	}
	dir2 := localPackDir(t.TempDir())
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir2, "nowhere.md"), filepath.Join(dir2, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if res, err := MoveLegacyLocalPackBriefing(dir2, false); res != nil || err != nil {
		t.Errorf("a dangling link: got %+v, %v", res, err)
	}
}

// A SYMLINK is refused rather than moved: renaming it one directory deeper would break a relative
// link, and rewriting the user's link is not yolo's to do.
func TestMoveLegacyLocalPackBriefingRefusesASymlink(t *testing.T) {
	dir := localPackDir(t.TempDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.txt"), []byte("Prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("rules.txt", filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	res, err := MoveLegacyLocalPackBriefing(dir, false)
	if err == nil || res == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want a symlink refusal; got %+v, %v", res, err)
	}
	if fi, lerr := os.Lstat(filepath.Join(dir, "AGENTS.md")); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the refusal touched the user's link: %v", lerr)
	}
}

// AN INTERRUPTED MOVE — both names one file, which a crash between the link and the remove
// leaves — is FINISHED, not refused: nothing is chosen between, so a refusal would only wedge
// every later apply.
func TestMoveLegacyLocalPackBriefingFinishesAnInterruptedMove(t *testing.T) {
	dir, legacy, target := writeLegacyLocalPack(t, "Prose.\n")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(legacy, target); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	res, err := MoveLegacyLocalPackBriefing(dir, false)
	if err != nil || res == nil || !strings.Contains(res.Action, "finished moving") {
		t.Fatalf("want the interrupted move finished; got %+v, %v", res, err)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Errorf("the root AGENTS.md survived (stat err=%v)", err)
	}
	if got := readFile(t, target); got != "Prose.\n" {
		t.Errorf("the target lost its prose: %q", got)
	}
}

// A LOCAL pack.json STILL NAMING THE LEGACY FILE REFUSES THE MOVE, naming the edit, and touches
// nothing. Moving it anyway would widen delivery: governance never reads the reserved `from`, so
// briefing/local.md would be named by nobody and broadcast to every agent — prose the user
// addressed to claude alone, sent to all of them by the apply that moved it.
func TestMoveLegacyLocalPackBriefingRefusesWhileAManifestNamesTheLegacyFile(t *testing.T) {
	for _, contrib := range []string{
		`{"kind":"briefing","from":"AGENTS.md","agents":["claude"]}`,
		`{"kind":"briefing","from":"./AGENTS.md","into":".claude/CLAUDE.md"}`,
	} {
		dir, legacy, target := writeLegacyLocalPack(t, "Claude only.\n")
		manifest := filepath.Join(dir, packdecl.ManifestName)
		if err := os.WriteFile(manifest, []byte(`{"name":"local","contributes":[`+contrib+`]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, observe := range []bool{true, false} {
			res, err := MoveLegacyLocalPackBriefing(dir, observe)
			if err == nil {
				t.Fatalf("%s observe=%v: want a refusal, got %+v", contrib, observe, res)
			}
			for _, want := range []string{manifest, `"briefing/local.md"`, "EVERY agent"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s observe=%v: the refusal must name %s: %v", contrib, observe, want, err)
				}
			}
			if res == nil || res.WouldChange {
				t.Errorf("%s observe=%v: a refusal changes nothing; got %+v", contrib, observe, res)
			}
			if got := readFile(t, legacy); got != "Claude only.\n" {
				t.Errorf("%s observe=%v: the refusal touched %s", contrib, observe, legacy)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Errorf("%s observe=%v: the refusal created %s", contrib, observe, target)
			}
		}
	}
	// A manifest pointing elsewhere — or at the new name — does not block the move.
	dir, _, target := writeLegacyLocalPack(t, "Prose.\n")
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName),
		[]byte(`{"name":"local","contributes":[{"kind":"briefing","from":"briefing/local.md","agents":["claude"]},`+
			`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveLegacyLocalPackBriefing(dir, false); err != nil {
		t.Fatalf("a manifest already naming the new file must not block the move: %v", err)
	}
	if got := readFile(t, target); got != "Prose.\n" {
		t.Errorf("the move did not happen: %q", got)
	}
}

// loadDiscardingProblems writes a pack tree and loads it through LoadDir with its problems
// DISCARDED — the way `yolo host apply` loaded a local pack (internal/cli's resolveConfiguredPack)
// until NS-D14 made that resolver refuse a pack with problems. The host-notch guards below stay
// for any caller that still discards them; the launch would have refused each of these manifests.
func loadDiscardingProblems(t *testing.T, name, manifest string, files map[string]string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	files[packdecl.ManifestName] = manifest
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := packload.LoadDir(root, name)
	if p == nil {
		t.Fatalf("LoadDir(%s) returned no pack", name)
	}
	return p
}

// composedAt is the composed content of the destination at home-relative `rel`, "" if none.
func composedAt(dests []HostBriefingDestination, home, rel string) string {
	for _, d := range dests {
		if d.Path == filepath.Join(home, filepath.FromSlash(rel)) {
			return d.Content
		}
	}
	return ""
}

// A DESTINATION SOURCES NOTHING AT THE HOST NOTCH (P5). An agent pack that addresses one of its
// briefing/ files to ANOTHER agent must not also compose it into its own destination: its
// `{agent, into}` line names where content lands, and read as an omitted-`from` contribution it
// would carry the pack's whole briefing/ remainder — the file addressed to pi alone.
func TestComposeHostBriefingsAnAgentPacksDestinationCarriesNothing(t *testing.T) {
	home := t.TempDir()
	claude := loadDiscardingProblems(t, "claude", `{"name":"claude","contributes":[`+
		`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"},`+
		`{"kind":"briefing","agents":["pi"]}]}`,
		map[string]string{"briefing/forpi.md": "PI ONLY PROSE\n"})
	pi := loadDiscardingProblems(t, "pi", `{"name":"pi","contributes":[`+
		`{"kind":"briefing","agent":"pi","into":".pi/agent/AGENTS.md"}]}`, map[string]string{})
	resolved, _ := packload.ResolveDestinations([]*packload.Pack{claude, pi})
	dests := ComposeHostBriefings(resolved, home, "", false)
	if got := composedAt(dests, home, ".claude/CLAUDE.md"); got != "" {
		t.Errorf("~/.claude/CLAUDE.md = %q, want nothing — prose addressed to pi reached claude "+
			"through claude's own destination line", got)
	}
	if got := composedAt(dests, home, ".pi/agent/AGENTS.md"); got != "PI ONLY PROSE\n" {
		t.Errorf("~/.pi/agent/AGENTS.md = %q, want the addressed prose", got)
	}
}

// A RESERVED `from` IS NEVER READ AT THE HOST NOTCH, even when its refusal was discarded (P1).
// `yolo host apply` loads a local pack with its problems dropped, so the validator's refusal of
// `from: "AGENTS.md"` does not stop it; governance must still not read the repository's file into
// the user's briefing.
func TestComposeHostBriefingsNeverReadsADiscardedReservedFrom(t *testing.T) {
	home := t.TempDir()
	claude := loadDiscardingProblems(t, "claude", `{"name":"claude","contributes":[`+
		`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"}]}`, map[string]string{})
	local := loadDiscardingProblems(t, "local", `{"name":"local","contributes":[`+
		`{"kind":"briefing","from":"AGENTS.md"}]}`, map[string]string{"AGENTS.md": "REPO GUIDE\n"})
	resolved, _ := packload.ResolveDestinations([]*packload.Pack{claude, local})
	for _, d := range ComposeHostBriefings(resolved, home, "", false) {
		if strings.Contains(d.Content, "REPO GUIDE") {
			t.Errorf("%s composed a reserved `from` whose refusal was discarded:\n%s", d.Path, d.Content)
		}
	}
}

// TWO CONTRIBUTIONS CARRYING ONE FILE TO ONE DESTINATION COMPOSE IT ONCE. OQ-PB5 refuses the
// manifest on the strict path, but the host reads a local pack with that refusal discarded; the
// governors fold, yet both declarations still reach the destination, so the per-destination dedup
// is what keeps the prose from appearing twice.
func TestComposeHostBriefingsADuplicateSourceComposesOnce(t *testing.T) {
	home := t.TempDir()
	dup := loadDiscardingProblems(t, "dup", `{"name":"dup","contributes":[`+
		`{"kind":"briefing","from":"a.md","into":".claude/CLAUDE.md"},`+
		`{"kind":"briefing","from":"./a.md","into":".claude/CLAUDE.md"}]}`,
		map[string]string{"a.md": "Once only.\n"})
	got := composedAt(ComposeHostBriefings([]*packload.Pack{dup}, home, "", false), home, ".claude/CLAUDE.md")
	if got != "Once only.\n" {
		t.Errorf("~/.claude/CLAUDE.md = %q, want the prose exactly once", got)
	}
}

// afterPack is a pack composing prose into `into` whose `after` names the user's own host file
// `after` — DP-B26's shape: a briefing that opens with a file the user keeps elsewhere.
func afterPack(t *testing.T, into, after string) *packload.Pack {
	t.Helper()
	p := briefingPack(t, "afterpack", into, "Pack prose.\n")
	p.Decl.Contributes[0].After = "host:" + after
	return p
}

// DP-B26: an `after` naming a file other than a destination OPENS the destination with that file,
// in the jail's bytes (jailcontent.PrependHostBriefing). It was silently ignored at the host.
func TestComposeHostBriefingsPrependsTheUsersAfterFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "mine.md"), []byte("MY RULES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{afterPack(t, ".foo/AGENTS.md", "mine.md")}
	dests := ComposeHostBriefings(packs, home, "", false)
	want, err := jailcontent.PrependHostBriefing(filepath.Join(home, "mine.md"), "Pack prose.\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := composedAt(dests, home, ".foo/AGENTS.md"); got != want {
		t.Errorf("~/.foo/AGENTS.md = %q, want the jail's prepend %q", got, want)
	}
	if len(dests) != 1 || dests[0].Overlay.Outcome != OverlayPrepended || dests[0].After != "mine.md" {
		t.Errorf("the destination does not say it prepended ~/mine.md: %+v", dests)
	}
}

// THE SKIP RULE (S3). An `after` naming a destination this composition writes — the destination
// itself, as every shipped agent pack declares, or another pack's — is never read, though the file
// is there: it is about to hold yolo's own output, and a hand-written one there is the adoption
// gate's to move into the local pack.
func TestComposeHostBriefingsNeverPrependsADestinationItComposes(t *testing.T) {
	home := t.TempDir()
	for _, rel := range []string{".claude/CLAUDE.md", ".foo/AGENTS.md"} {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ALREADY THERE\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	own := briefingPack(t, "claude", ".claude/CLAUDE.md", "Claude prose.\n")
	other := afterPack(t, ".foo/AGENTS.md", ".claude/CLAUDE.md")
	for _, d := range ComposeHostBriefings([]*packload.Pack{own, other}, home, "", false) {
		if strings.Contains(d.Content, "ALREADY THERE") {
			t.Errorf("%s prepended a destination this apply composes:\n%s", d.Path, d.Content)
		}
		if d.Overlay.Outcome != OverlayYoloOutput {
			t.Errorf("%s: outcome %q, want %q", d.Path, d.Overlay.Outcome, OverlayYoloOutput)
		}
	}
}

// THE RECORD HALF of the skip rule: a file the briefing record lists as yolo's (a destination an
// earlier apply composed for a pack no longer selected) is not the user's, and the record is what
// knows it — the bare composition, which carries none, prepends it.
func TestComposeHostBriefingsForSkipsAFileTheRecordSaysYoloComposed(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "old.md")
	if err := os.WriteFile(old, []byte("A DROPPED PACK'S PROSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{afterPack(t, ".foo/AGENTS.md", "old.md")}
	req, man := briefingReq(t, home)
	man.Record(old, HostBriefingOwner)

	dests := ComposeHostBriefingsFor(packs, home, req)
	if got := composedAt(dests, home, ".foo/AGENTS.md"); strings.Contains(got, "DROPPED") {
		t.Errorf("a file yolo composed was read back in as the user's:\n%s", got)
	}
	// The record's own clause, so the skip is pinned to the record by path and not only to the
	// identity check that also matches this existing file.
	if ov := dests[0].Overlay; ov.Outcome != OverlayYoloOutput || ov.Why != "an earlier apply composed it" {
		t.Errorf("outcome %q (%q), want %q (%q)", ov.Outcome, ov.Why, OverlayYoloOutput,
			"an earlier apply composed it")
	}
	if got := composedAt(ComposeHostBriefings(packs, home, "", false), home, ".foo/AGENTS.md"); !strings.Contains(got, "DROPPED") {
		t.Errorf("without the record the file is indistinguishable from the user's, so the bare "+
			"composition must prepend it — otherwise this test proves nothing about the record: %q", got)
	}
}

// THE FIRST CONTRIBUTION CARRYING AN `after` DECIDES a destination's — not the first pack at the
// path, nor the last contribution carrying one — within one pack and across packs. A broadcast's
// synthesized copy carries none (packload.ResolveDestinations), so whichever pack is listed first
// at a path is no guide; and two `after`s at one path is a choice the declaration order makes.
func TestComposeHostBriefingsTakesTheFirstContributionCarryingAnAfter(t *testing.T) {
	carrying := func(t *testing.T, name string, afters ...string) *packload.Pack {
		p := briefingPack(t, name, ".foo/AGENTS.md", name+" prose.\n")
		first := p.Decl.Contributes[0]
		p.Decl.Contributes = nil
		for _, a := range afters {
			c := first
			c.After = "" // briefingPack's own `after` names its destination
			if a != "" {
				c.After = "host:" + a
			}
			p.Decl.Contributes = append(p.Decl.Contributes, c)
		}
		return p
	}
	cases := map[string]func(t *testing.T) []*packload.Pack{
		"within one pack": func(t *testing.T) []*packload.Pack {
			return []*packload.Pack{carrying(t, "one", "", "first.md", "second.md")}
		},
		"across packs": func(t *testing.T) []*packload.Pack {
			return []*packload.Pack{carrying(t, "none", ""), carrying(t, "a", "first.md"),
				carrying(t, "b", "second.md")}
		},
	}
	for name, packs := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			for _, f := range []string{"first.md", "second.md"} {
				mustAfter(t, os.WriteFile(filepath.Join(home, f), []byte(strings.ToUpper(f)+"\n"), 0o644))
			}
			dests := ComposeHostBriefings(packs(t), home, "", false)
			if len(dests) != 1 || dests[0].After != "first.md" {
				t.Fatalf("want one destination whose after is first.md, got %+v", dests)
			}
			if got := dests[0].Content; !strings.HasPrefix(got, "FIRST.MD\n\n---\n\n") ||
				strings.Contains(got, "SECOND.MD") {
				t.Errorf("~/.foo/AGENTS.md does not open with first.md alone:\n%s", got)
			}
		})
	}
}

// A RECORDED PATH THAT IS NOW A DANGLING LINK is still yolo's output, and is skipped as one, not
// warned about as an unreadable file of the user's: the record is what proves ownership, and no
// identity check can match a link to nowhere. Restoring the target would only make the file yolo's
// skipped output again, so the unread warning's "Restore the target, or remove the link" would send
// the user to fix something that changes nothing.
func TestComposeHostBriefingsForSkipsARecordedPathThatIsADanglingLink(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "old.md")
	mustAfter(t, os.Symlink(filepath.Join(home, "gone.md"), old))
	packs := []*packload.Pack{afterPack(t, ".foo/AGENTS.md", "old.md")}
	req, man := briefingReq(t, home)
	man.Record(old, HostBriefingOwner)

	dests := ComposeHostBriefingsFor(packs, home, req)
	if ov := dests[0].Overlay; ov.Outcome != OverlayYoloOutput || ov.Unread != nil {
		t.Errorf("outcome %q (unread %v), want %q: a recorded path is yolo's whatever it holds now",
			ov.Outcome, ov.Unread, OverlayYoloOutput)
	}
	// Without the record it is an unreadable file of the user's, which is the case being told apart.
	if ov := ComposeHostBriefings(packs, home, "", false)[0].Overlay; ov.Outcome != OverlayUnread {
		t.Errorf("fixture: without the record a dangling link must read as unread, got %q", ov.Outcome)
	}
}

// NEVER OPEN AN UNREADABLE SOURCE. A dangling link (the file's or an ancestor's), a directory and a
// FIFO each leave the destination composed without the file and say why — and the FIFO, which a
// read would block on until something writes into it, must not stall the composition.
func TestComposeHostBriefingsNeverOpensAnUnreadableAfterFile(t *testing.T) {
	cases := map[string]func(t *testing.T, home string) string{
		"dangling link": func(t *testing.T, home string) string {
			mustAfter(t, os.Symlink(filepath.Join(home, "gone.md"), filepath.Join(home, "mine.md")))
			return "mine.md"
		},
		"dangling ancestor": func(t *testing.T, home string) string {
			mustAfter(t, os.Symlink(filepath.Join(home, "gone"), filepath.Join(home, "notes")))
			return "notes/mine.md"
		},
		"directory": func(t *testing.T, home string) string {
			mustAfter(t, os.MkdirAll(filepath.Join(home, "mine.md"), 0o755))
			return "mine.md"
		},
		"fifo": func(t *testing.T, home string) string {
			mustAfter(t, syscall.Mkfifo(filepath.Join(home, "mine.md"), 0o644))
			return "mine.md"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			after := setup(t, home)
			packs := []*packload.Pack{afterPack(t, ".foo/AGENTS.md", after)}
			done := make(chan []HostBriefingDestination, 1)
			go func() { done <- ComposeHostBriefings(packs, home, "", false) }()
			var dests []HostBriefingDestination
			select {
			case dests = <-done:
			case <-time.After(2 * time.Minute):
				t.Fatal("the composition blocked on the `after` file")
			}
			if got := composedAt(dests, home, ".foo/AGENTS.md"); got != "Pack prose.\n" {
				t.Errorf("want the destination composed without the file, got %q", got)
			}
			ov := dests[0].Overlay
			if ov.Outcome != OverlayUnread || ov.Unread == nil || ov.Unread.Why == "" {
				t.Errorf("the destination does not say why the file was not read: %+v", ov)
			}
		})
	}
}

// THE ADOPTION GATE COMPOSES WHAT THE RENDER WRITES. A destination already holding exactly the
// composition — the user's `after` file prepended — is not an adoption, and the render then leaves
// it unchanged; were the gate to compare against the composition WITHOUT the file, every home with
// an `after` file would be asked to adopt its own destination.
func TestHostBriefingAdoptionsComposeTheBytesTheRenderWrites(t *testing.T) {
	home := t.TempDir()
	mustAfter(t, os.WriteFile(filepath.Join(home, "mine.md"), []byte("MY RULES\n"), 0o644))
	packs := []*packload.Pack{afterPack(t, ".foo/AGENTS.md", "mine.md")}
	req, man := briefingReq(t, home)
	composed := composedAt(ComposeHostBriefingsFor(packs, home, req), home, ".foo/AGENTS.md")
	if !strings.HasPrefix(composed, "MY RULES\n") {
		t.Fatalf("the fixture's file was not prepended: %q", composed)
	}
	dest := filepath.Join(home, ".foo", "AGENTS.md")
	mustAfter(t, os.MkdirAll(filepath.Dir(dest), 0o755))
	mustAfter(t, os.WriteFile(dest, []byte(composed), 0o644))

	if got := HostBriefingAdoptions(packs, home, man, req.Base, req.Provenance, nil); len(got) != 0 {
		t.Errorf("a destination holding exactly the composition was offered for adoption: %+v", got)
	}
	results, err := RenderHostBriefings(packs, home, req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "unchanged" {
		t.Errorf("the render disagrees with the gate about the bytes: %+v", results)
	}
}

// mustAfter fails the test on a fixture error.
func mustAfter(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// THE SKIP RULE BY FILE IDENTITY, not only by path. A dotfiles manager links one canonical file
// to the place a tool reads it, so an `after` file and a composed destination can be ONE file under
// two names. Compared by path, that file was read back in as the user's on every apply: the render
// writes through the link, so the next composition prepended its own previous output and the file
// grew without bound. Each shape below must be skipped as yolo's output, and the destination must
// hold the composition alone.
func TestComposeHostBriefingsSkipsAnAfterFileThatIsYoloOutputUnderAnotherName(t *testing.T) {
	cases := map[string]struct {
		// setup builds the home and returns the `after` path and, for the record case, the file
		// the record lists.
		setup func(t *testing.T, home string) (after, recorded string)
		why   string
	}{
		"the destination links to the after file": {func(t *testing.T, home string) (string, string) {
			mustAfter(t, os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("YOLO WROTE THIS\n"), 0o644))
			mustAfter(t, os.MkdirAll(filepath.Join(home, ".foo"), 0o755))
			mustAfter(t, os.Symlink(filepath.Join(home, "AGENTS.md"), filepath.Join(home, ".foo", "AGENTS.md")))
			return "AGENTS.md", ""
		}, "it is the same file as this destination, which this apply composes"},
		"the after file links to the destination": {func(t *testing.T, home string) (string, string) {
			mustAfter(t, os.MkdirAll(filepath.Join(home, ".foo"), 0o755))
			mustAfter(t, os.WriteFile(filepath.Join(home, ".foo", "AGENTS.md"), []byte("YOLO WROTE THIS\n"), 0o644))
			mustAfter(t, os.Symlink(filepath.Join(home, ".foo", "AGENTS.md"), filepath.Join(home, "mine.md")))
			return "mine.md", ""
		}, "it is the same file as this destination, which this apply composes"},
		"the after file links to another destination": {func(t *testing.T, home string) (string, string) {
			mustAfter(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
			mustAfter(t, os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("YOLO WROTE THIS\n"), 0o644))
			mustAfter(t, os.Symlink(filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, "mine.md")))
			return "mine.md", ""
		}, "it is the same file as ~/.claude/CLAUDE.md, a briefing destination this apply composes"},
		"the after file links to a file the record lists": {func(t *testing.T, home string) (string, string) {
			old := filepath.Join(home, "old.md")
			mustAfter(t, os.WriteFile(old, []byte("YOLO WROTE THIS\n"), 0o644))
			mustAfter(t, os.Symlink(old, filepath.Join(home, "mine.md")))
			return "mine.md", old
		}, "it is the same file as ~/old.md, which an earlier apply composed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			home := evalTempDir(t)
			after, recorded := tc.setup(t, home)
			packs := []*packload.Pack{
				briefingPack(t, "claude", ".claude/CLAUDE.md", "Claude prose.\n"),
				afterPack(t, ".foo/AGENTS.md", after),
			}
			req, man := briefingReq(t, home)
			if recorded != "" {
				man.Record(recorded, HostBriefingOwner)
			}
			for _, d := range ComposeHostBriefingsFor(packs, home, req) {
				if d.Path != filepath.Join(home, ".foo", "AGENTS.md") {
					continue
				}
				if d.Content != "Pack prose.\n" {
					t.Errorf("~/.foo/AGENTS.md read yolo's own output back in: %q", d.Content)
				}
				if d.Overlay.Outcome != OverlayYoloOutput || d.Overlay.Why != tc.why {
					t.Errorf("overlay = %+v, want %q because %q", d.Overlay, OverlayYoloOutput, tc.why)
				}
			}
			// The adoption gate composes through the same rule, so it compares the same bytes.
			dest := filepath.Join(home, ".foo", "AGENTS.md")
			if _, err := os.Stat(dest); err == nil {
				for _, a := range HostBriefingAdoptions(packs, home, man, req.Base, req.Provenance, nil) {
					if strings.Contains(a.Existing, "Pack prose.") {
						t.Errorf("the gate composed different bytes than the render: %+v", a)
					}
				}
			}
		})
	}
}

// evalTempDir is t.TempDir() with its symlinks resolved, so a path compared against one the code
// built from a resolved file reads the same on darwin, where the temp root is a link.
func evalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// THE RECORD HALF AT THE ADOPTION GATE. The gate composes with the record, so a destination holding
// exactly the record-aware composition — the `after` file NOT prepended, because the record lists
// it as yolo's — is not offered for adoption. Composed without the record, the gate would prepend
// that file, see bytes that differ, and ask to adopt a destination yolo is about to write the same
// content into. The destination itself is not in the record, so nothing else exempts it.
func TestHostBriefingAdoptionsComposeWithTheRecord(t *testing.T) {
	home := evalTempDir(t)
	old := filepath.Join(home, "old.md")
	mustAfter(t, os.WriteFile(old, []byte("A DROPPED PACK'S PROSE\n"), 0o644))
	packs := []*packload.Pack{afterPack(t, ".foo/AGENTS.md", "old.md")}
	req, man := briefingReq(t, home)
	man.Record(old, HostBriefingOwner)
	dest := filepath.Join(home, ".foo", "AGENTS.md")
	mustAfter(t, os.MkdirAll(filepath.Dir(dest), 0o755))
	mustAfter(t, os.WriteFile(dest, []byte(composedAt(ComposeHostBriefingsFor(packs, home, req), home,
		".foo/AGENTS.md")), 0o644))
	if got, _ := os.ReadFile(dest); string(got) != "Pack prose.\n" {
		t.Fatalf("fixture: the record-aware composition prepended the recorded file: %q", got)
	}

	if got := HostBriefingAdoptions(packs, home, man, req.Base, req.Provenance, nil); len(got) != 0 {
		t.Errorf("a destination holding exactly the record-aware composition was offered for "+
			"adoption — the gate composed without the record: %+v", got)
	}
}

// loadValidPack writes a pack tree and loads it through LoadDir, failing on any problem, so a
// fixture here is a manifest the launch would accept.
func loadValidPack(t *testing.T, name, manifest string, files map[string]string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	files[packdecl.ManifestName] = manifest
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, probs := packload.LoadDir(root, name)
	if p == nil || len(probs) > 0 {
		t.Fatalf("LoadDir(%s): %v", name, probs)
	}
	return p
}

// describesDest is an agent pack declaring ~/.claude/CLAUDE.md, with prose of its own.
func describesDest(t *testing.T) *packload.Pack {
	t.Helper()
	return loadValidPack(t, "claude", `{"name":"claude","contributes":[`+
		`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"}]}`,
		map[string]string{"briefing/own.md": "Claude's own prose.\n"})
}

// destinationAt is the composed destination at home-relative rel.
func destinationAt(t *testing.T, dests []HostBriefingDestination, home, rel string) HostBriefingDestination {
	t.Helper()
	for _, d := range dests {
		if d.Path == filepath.Join(home, filepath.FromSlash(rel)) {
			return d
		}
	}
	t.Fatalf("no destination %s in %+v", rel, dests)
	return HostBriefingDestination{}
}

// PROSE ABOUT A KIND THE HOST DOES NOT DELIVER IS WITHHELD (docs/design/boundary-broker.md
// BB-D69), and only that prose: a file describing a kind the host delivers (`config`) and a file
// no contribution names (the implicit broadcast) still compose, beside the agent pack's own.
func TestComposeHostBriefingsWithholdsProseAboutAKindTheHostDoesNotDeliver(t *testing.T) {
	home := t.TempDir()
	gh := loadValidPack(t, "gh", `{"name":"gh","contributes":[`+
		`{"kind":"briefing","from":"briefing/forwarder.md","describes":["intercept"]},`+
		`{"kind":"briefing","from":"briefing/surface.md","describes":["config"]},`+
		`{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"]},`+
		`{"kind":"config","config":[{"agent":"gh","name":"own","codec":"json","path":"~/x.json"}]}]}`,
		map[string]string{
			"briefing/forwarder.md": "JAIL FORWARDER PROSE\n",
			"briefing/surface.md":   "Config prose.\n",
			"briefing/plain.md":     "Implicit prose.\n",
		})
	resolved, _ := packload.ResolveDestinations([]*packload.Pack{describesDest(t), gh})
	d := destinationAt(t, ComposeHostBriefings(resolved, home, "", false), home, ".claude/CLAUDE.md")
	if strings.Contains(d.Content, "JAIL FORWARDER PROSE") {
		t.Errorf("prose describing an intercept reached a host destination:\n%s", d.Content)
	}
	if want := "Claude's own prose.\n\nImplicit prose.\n\nConfig prose.\n"; d.Content != want {
		t.Errorf("~/.claude/CLAUDE.md = %q, want %q", d.Content, want)
	}
	if strings.Join(d.Packs, "+") != "claude+gh" {
		t.Errorf("Packs = %v, want claude and gh: gh still contributes prose here", d.Packs)
	}
}

// A PACK WHOSE EVERY FILE HERE IS WITHHELD IS NOT AN OWNER of a destination it only borrowed —
// the github pack's shape, briefing/gh.md alone into claude's file — so the report names the
// destination claude/briefing. A pack that DECLARES the destination stays its owner, as a pack
// shipping no prose there always has.
func TestComposeHostBriefingsAPackWithOnlyWithheldProseOwnsNothingItBorrowed(t *testing.T) {
	home := t.TempDir()
	borrower := loadValidPack(t, "borrower", `{"name":"borrower","contributes":[`+
		`{"kind":"briefing","from":"briefing/gh.md","describes":["intercept"]},`+
		`{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"]}]}`,
		map[string]string{"briefing/gh.md": "JAIL FORWARDER PROSE\n"})
	declarer := loadValidPack(t, "declarer", `{"name":"declarer","contributes":[`+
		`{"kind":"briefing","from":"briefing/x.md","into":".claude/CLAUDE.md","describes":["intercept"]},`+
		`{"kind":"intercept","bin":"gx","forward":["yolo","gx","--"]}]}`,
		map[string]string{"briefing/x.md": "DECLARED JAIL PROSE\n"})
	resolved, _ := packload.ResolveDestinations([]*packload.Pack{describesDest(t), borrower, declarer})
	d := destinationAt(t, ComposeHostBriefings(resolved, home, "", false), home, ".claude/CLAUDE.md")
	if strings.Join(d.Packs, "+") != "claude+declarer" {
		t.Errorf("Packs = %v, want claude and declarer: borrower contributes nothing here", d.Packs)
	}
	if hostBriefingSurfaceID(d) != "claude+declarer/briefing" {
		t.Errorf("surface = %q", hostBriefingSurfaceID(d))
	}
	if d.Content != "Claude's own prose.\n" {
		t.Errorf("~/.claude/CLAUDE.md = %q, want the agent pack's prose alone", d.Content)
	}
}

// With no census for a pack, the predicate is the census's per-kind answer: an at-launch kind
// (env, loophole) and a rendered one (skills) gate nothing at the host, and the jail-only kinds do.
func TestHostWithheldKindsAsksTheCensus(t *testing.T) {
	src := func(kinds ...packdecl.Kind) packload.GovernedSource {
		return packload.GovernedSource{By: packdecl.Contribution{Kind: packdecl.KindBriefing, Describes: kinds}}
	}
	if got := HostWithheldKinds(nil, "p", src()); got != nil {
		t.Errorf("no describes = %v, want nil", got)
	}
	if got := HostWithheldKinds(nil, "p", src(packdecl.KindEnv, packdecl.KindSkills, packdecl.KindLoophole)); got != nil {
		t.Errorf("env, skills, loophole = %v, want nil: each is delivered by some host verb", got)
	}
	got := HostWithheldKinds(nil, "p", src(packdecl.KindIntercept, packdecl.KindEnv, packdecl.KindState,
		packdecl.KindIntercept))
	if len(got) != 2 || got[0] != packdecl.KindIntercept || got[1] != packdecl.KindState {
		t.Errorf("intercept, env, state, intercept = %v, want [intercept state]", got)
	}
}

// A PACK THE CENSUS NAMES IS ANSWERED PER CONTRIBUTION (HostDelivery): its loophole is delivered
// only when the census says so, whatever the kind's per-kind answer, and a kind it declares no
// contribution of is delivered nowhere. Another pack in the same map is answered from its own
// entry, and a pack the map does not name falls back to the per-kind answer.
func TestHostWithheldKindsReadsThePacksOwnCensus(t *testing.T) {
	src := func(kinds ...packdecl.Kind) packload.GovernedSource {
		return packload.GovernedSource{By: packdecl.Contribution{Kind: packdecl.KindBriefing, Describes: kinds}}
	}
	d := HostDelivery{
		"nodoor": {packdecl.KindEnv: true},
		"door":   {packdecl.KindLoophole: true, packdecl.KindEnv: true},
	}
	if got := HostWithheldKinds(d, "nodoor", src(packdecl.KindLoophole, packdecl.KindEnv)); len(got) != 1 ||
		got[0] != packdecl.KindLoophole {
		t.Errorf("nodoor: loophole, env = %v, want [loophole]: its loophole has no doorway", got)
	}
	if got := HostWithheldKinds(d, "door", src(packdecl.KindLoophole, packdecl.KindEnv)); got != nil {
		t.Errorf("door: loophole, env = %v, want nil: the census delivers both", got)
	}
	if got := HostWithheldKinds(d, "door", src(packdecl.KindSkills)); len(got) != 1 {
		t.Errorf("door: skills = %v, want [skills]: the pack declares no skills to describe", got)
	}
	if got := HostWithheldKinds(d, "unsurveyed", src(packdecl.KindLoophole)); got != nil {
		t.Errorf("unsurveyed: loophole = %v, want nil: the per-kind answer delivers a loophole", got)
	}
}

// THE COMPOSER READS THE REQUEST'S CENSUS. A pack whose loophole the census does not deliver has
// the prose about it withheld, through every reader of the composition (the render, the adoption
// gate, the retire pass): the same pack set with no census delivers it, the per-kind answer
// counting a loophole as delivered.
func TestComposeHostBriefingsForWithholdsByTheRequestsCensus(t *testing.T) {
	home := t.TempDir()
	lh := loadValidPack(t, "lh", `{"name":"lh","contributes":[`+
		`{"kind":"briefing","from":"briefing/hole.md","describes":["loophole"]},`+
		`{"kind":"briefing","from":"briefing/env.md","describes":["env"]},`+
		`{"kind":"loophole","from":"loopholes/lhole"},`+
		`{"kind":"env","vars":{"LH":"1"}}]}`,
		map[string]string{
			"briefing/hole.md":               "LOOPHOLE PROSE\n",
			"briefing/env.md":                "ENV PROSE\n",
			"loopholes/lhole/manifest.jsonc": `{"name":"lhole","description":"d","version":1}`,
		})
	resolved, _ := packload.ResolveDestinations([]*packload.Pack{describesDest(t), lh})
	census := HostDelivery{"claude": {packdecl.KindBriefing: true},
		"lh": {packdecl.KindBriefing: true, packdecl.KindEnv: true}}
	req := HostBriefingRequest{Delivery: census}

	d := destinationAt(t, ComposeHostBriefingsFor(resolved, home, req), home, ".claude/CLAUDE.md")
	if want := "Claude's own prose.\n\nENV PROSE\n"; d.Content != want {
		t.Errorf("with the census, ~/.claude/CLAUDE.md = %q, want %q", d.Content, want)
	}
	if strings.Join(d.Packs, "+") != "claude+lh" {
		t.Errorf("Packs = %v, want claude and lh: lh's env prose still lands", d.Packs)
	}
	nocensus := destinationAt(t, ComposeHostBriefings(resolved, home, "", false), home, ".claude/CLAUDE.md")
	if !strings.Contains(nocensus.Content, "LOOPHOLE PROSE") {
		t.Errorf("with no census the per-kind answer delivers a loophole:\n%s", nocensus.Content)
	}

	// The adoption gate composes by the same census: a file identical to the census's
	// composition is not an adoption, while one identical only to the per-kind composition is.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.Path, []byte(d.Content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := HostBriefingAdoptions(resolved, home, nil, "", false, census); len(got) != 0 {
		t.Errorf("the gate asks to adopt a file identical to the census's composition: %+v", got)
	}
	if got := HostBriefingAdoptions(resolved, home, nil, "", false, nil); len(got) != 1 {
		t.Errorf("fixture bug: with no census the composition differs, so the gate must ask: %+v", got)
	}
}

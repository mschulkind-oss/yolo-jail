package entrypoint

// hostbriefing_test.go pins the Phase 5 contract: yolo owns a delimited block inside the
// user's own briefing file and nothing else in it.
//
// The load-bearing test is TestHostBriefingAssertTwiceIsByteIdentical. In a jail the
// briefing is composed into a SEPARATE staging file, so appending is safe; at the host the
// source and the destination are the same file and a plain append duplicates the user's
// prose on every apply, forever. Every other test here guards a way that block could eat
// content it does not own.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// briefingPack builds a pack whose root is a temp dir carrying `prose` as AGENTS.md and
// which declares a briefing into `into`. The `after: host:` half is declared too, because
// that is the shape the shipped packs use and the host render must ignore it (the user's
// file IS the destination).
func briefingPack(t *testing.T, name, into, prose string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	if prose != "" {
		if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(prose), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &packload.Pack{
		Name: name,
		Root: root,
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind:  packdecl.KindBriefing,
			From:  "AGENTS.md",
			Into:  into,
			After: "host:" + into,
		}}},
		MayAccessHost: true,
	}
}

// readFile lives in hostfiles_test.go — the package's shared read-or-fail helper.

// THE duplication test. Two asserts in a row must produce identical bytes — at the host
// the destination is also the source, so an appending renderer grows the file every apply.
func TestHostBriefingAssertTwiceIsByteIdentical(t *testing.T) {
	home := t.TempDir()
	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Use rg, never grep -r.\n")
	dest := filepath.Join(home, ".claude", "CLAUDE.md")

	if _, err := RenderHostBriefing(p, home, false); err != nil {
		t.Fatalf("first assert: %v", err)
	}
	first := readFile(t, dest)

	results, err := RenderHostBriefing(p, home, false)
	if err != nil {
		t.Fatalf("second assert: %v", err)
	}
	second := readFile(t, dest)
	if first != second {
		t.Fatalf("second --assert is not byte-identical:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	// An unchanged re-assert must SAY so rather than claim it rendered.
	if len(results) != 1 || results[0].Action != "unchanged" {
		t.Errorf("re-assert should report unchanged; got %+v", results)
	}
	// One block, one provenance header — not two of either.
	if n := strings.Count(second, hostBriefingBeginMarker("matt-core")); n != 1 {
		t.Errorf("want exactly 1 begin marker, got %d:\n%s", n, second)
	}
	if n := strings.Count(second, "Use rg, never grep -r."); n != 1 {
		t.Errorf("pack prose duplicated (%d copies):\n%s", n, second)
	}
	if n := strings.Count(second, "<!-- from pack: matt-core -->"); n != 1 {
		t.Errorf("want exactly 1 provenance header, got %d:\n%s", n, second)
	}
}

// The user's hand-written prose is outside the markers, so it survives an apply — and
// survives being edited BETWEEN applies, which is the case a "compose the whole file"
// renderer gets wrong.
func TestHostBriefingPreservesUserProse(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const userProse = "# My rules\n\nAlways run the tests.\n"
	if err := os.WriteFile(dest, []byte(userProse), 0o644); err != nil {
		t.Fatal(err)
	}

	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Pack rule one.\n")
	if _, err := RenderHostBriefing(p, home, false); err != nil {
		t.Fatalf("assert: %v", err)
	}
	got := readFile(t, dest)
	if !strings.HasPrefix(got, userProse) {
		t.Errorf("user prose must survive verbatim at the head of the file:\n%s", got)
	}

	// Now the user adds more prose AFTER the block, and the pack's prose changes.
	if err := os.WriteFile(dest, []byte(got+"\n## Added later\n\nAnd this too.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Pack rule TWO.\n")
	if _, err := RenderHostBriefing(p2, home, false); err != nil {
		t.Fatalf("second assert: %v", err)
	}
	got = readFile(t, dest)
	for _, want := range []string{"# My rules", "Always run the tests.", "## Added later", "And this too.", "Pack rule TWO."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q after re-assert:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Pack rule one.") {
		t.Errorf("stale pack prose survived the in-place rewrite:\n%s", got)
	}
	// Rewritten IN PLACE: the user's trailing section is still last, so the block was not
	// relocated to end-of-file.
	if strings.LastIndex(got, "And this too.") < strings.LastIndex(got, hostBriefingEndMarker("matt-core")) {
		t.Errorf("block was relocated instead of rewritten in place:\n%s", got)
	}
}

// Two packs sharing one destination get two independent blocks — that is what makes
// dropping one pack a bounded edit.
func TestHostBriefingTwoPacksTwoBlocks(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	a := briefingPack(t, "pack-a", ".claude/CLAUDE.md", "A prose.\n")
	b := briefingPack(t, "pack-b", ".claude/CLAUDE.md", "B prose.\n")
	for _, p := range []*packload.Pack{a, b} {
		if _, err := RenderHostBriefing(p, home, false); err != nil {
			t.Fatalf("assert %s: %v", p.Name, err)
		}
	}
	got := readFile(t, dest)
	for _, pack := range []string{"pack-a", "pack-b"} {
		if !strings.Contains(got, hostBriefingBeginMarker(pack)) {
			t.Errorf("missing block for %s:\n%s", pack, got)
		}
	}
	// Re-asserting A must not disturb B's block.
	if _, err := RenderHostBriefing(a, home, false); err != nil {
		t.Fatalf("re-assert a: %v", err)
	}
	again := readFile(t, dest)
	if again != got {
		t.Errorf("re-asserting pack-a changed the file:\n--- before ---\n%s\n--- after ---\n%s", got, again)
	}
}

// A dropped pack's block is removed; the surviving pack's block and the user's prose stay.
func TestHostBriefingPruneRemovesOnlyTheDroppedPack(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("# Mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := briefingPack(t, "pack-a", ".claude/CLAUDE.md", "A prose.\n")
	b := briefingPack(t, "pack-b", ".claude/CLAUDE.md", "B prose.\n")
	for _, p := range []*packload.Pack{a, b} {
		if _, err := RenderHostBriefing(p, home, false); err != nil {
			t.Fatalf("assert %s: %v", p.Name, err)
		}
	}

	// pack-b leaves the config: it is still a CANDIDATE (its destination must be visited)
	// but not ACTIVE.
	results, err := PruneHostBriefings([]*packload.Pack{a, b}, map[string]bool{"pack-a": true}, home, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "removed") {
		t.Fatalf("want one removal for pack-b; got %+v", results)
	}
	got := readFile(t, dest)
	// Match pack-b's MARKER, not the bare string "pack-b". The marker tag is
	// `yolo:pack-briefing`, and "pack-b" is a substring of "pack-briefing" — so the naive
	// check was true of every block, including pack-a's, and failed on correct output.
	if strings.Contains(got, hostBriefingBeginMarker("pack-b")) || strings.Contains(got, "B prose.") {
		t.Errorf("pack-b's block should be gone:\n%q", got)
	}
	for _, want := range []string{"# Mine", "A prose.", hostBriefingBeginMarker("pack-a")} {
		if !strings.Contains(got, want) {
			t.Errorf("prune removed content it does not own (%q missing):\n%s", want, got)
		}
	}

	// Prune is idempotent, and a nil active set is refused rather than read as "drop all".
	if _, err := PruneHostBriefings([]*packload.Pack{a, b}, map[string]bool{"pack-a": true}, home, false); err != nil {
		t.Fatalf("second prune: %v", err)
	}
	if after := readFile(t, dest); after != got {
		t.Errorf("second prune changed the file:\n%s", after)
	}
	if _, err := PruneHostBriefings([]*packload.Pack{a, b}, nil, home, false); err == nil {
		t.Error("a nil active set must be refused, not treated as 'no pack is active'")
	}
}

// A pack that stops shipping prose has its stale block removed — "dropped the pack" and
// "dropped the prose" must not leave different residue.
func TestHostBriefingEmptyProseRemovesStaleBlock(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if _, err := RenderHostBriefing(briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Prose.\n"), home, false); err != nil {
		t.Fatalf("assert: %v", err)
	}
	silent := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "")
	results, err := RenderHostBriefing(silent, home, false)
	if err != nil {
		t.Fatalf("assert with no prose: %v", err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "removed") {
		t.Fatalf("want a removal when the pack ships no prose; got %+v", results)
	}
	if got := readFile(t, dest); strings.Contains(got, hostBriefingMarkerTag) {
		t.Errorf("stale block survived:\n%s", got)
	}
}

// Fail-closed: an unterminated begin marker must REFUSE, not guess a boundary. Guessing is
// how a renderer swallows the prose between the marker and end-of-file.
func TestHostBriefingUnterminatedMarkerRefuses(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "# Mine\n\n" + hostBriefingBeginMarker("matt-core") + "\nhalf a block\n\n## Precious\n\nDo not eat this.\n"
	if err := os.WriteFile(dest, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "New prose.\n")
	results, err := RenderHostBriefing(p, home, false)
	if err == nil {
		t.Fatalf("an unterminated marker must be an error; got results=%+v", results)
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("the refusal should name the problem; got %v", err)
	}
	// It must be REPORTED as a refusal too, so apply --host prints a line rather than
	// silently omitting the pack.
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "refused:") {
		t.Errorf("want a refused result for the destination; got %+v", results)
	}
	if got := readFile(t, dest); got != broken {
		t.Errorf("a refusal must not modify the file:\n--- want ---\n%s\n--- got ---\n%s", broken, got)
	}
}

// The other malformed shapes: a stray end marker, a crossed pair, and two blocks for one
// pack. Each is a boundary a renderer would have to guess at.
func TestHostBriefingMalformedShapesRefuse(t *testing.T) {
	begin := hostBriefingBeginMarker
	end := hostBriefingEndMarker
	cases := map[string]string{
		"stray end":         "# Mine\n" + end("matt-core") + "\n",
		"crossed":           begin("a") + "\n" + end("b") + "\n",
		"nested":            begin("a") + "\n" + begin("b") + "\n" + end("b") + "\n" + end("a") + "\n",
		"duplicate for one": begin("matt-core") + "\nx\n" + end("matt-core") + "\n" + begin("matt-core") + "\ny\n" + end("matt-core") + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			dest := filepath.Join(home, ".claude", "CLAUDE.md")
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "New prose.\n")
			if _, err := RenderHostBriefing(p, home, false); err == nil {
				t.Fatalf("%s must refuse", name)
			}
			if got := readFile(t, dest); got != content {
				t.Errorf("a refusal must not modify the file:\n%s", got)
			}
		})
	}
}

// A missing briefing file (and a missing parent dir) is the NORMAL case: create it holding
// just the block.
func TestHostBriefingCreatesMissingFile(t *testing.T) {
	home := t.TempDir()
	p := briefingPack(t, "matt-core", ".config/opencode/AGENTS.md", "Only the pack's prose.\n")
	results, err := RenderHostBriefing(p, home, false)
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if len(results) != 1 || results[0].Action != "rendered" {
		t.Fatalf("want one rendered result; got %+v", results)
	}
	got := readFile(t, filepath.Join(home, ".config", "opencode", "AGENTS.md"))
	want := hostBriefingBeginMarker("matt-core") + "\n" +
		"<!-- from pack: matt-core -->\n" +
		"Only the pack's prose.\n" +
		hostBriefingEndMarker("matt-core") + "\n"
	if got != want {
		t.Errorf("a created file should hold exactly the block:\n--- want ---\n%q\n--- got ---\n%q", want, got)
	}
}

// Observe (dry-run) computes and reports, and writes NOTHING — the same contract
// RenderHostPack honors, including for the create and remove paths.
func TestHostBriefingObserveWritesNothing(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Prose.\n")

	results, err := RenderHostBriefing(p, home, true)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(results) != 1 || results[0].Action != "would render" {
		t.Fatalf("want one 'would render'; got %+v", results)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("observe must not create the file (stat err=%v)", err)
	}

	// With a block present, observe on a pack that ships no prose previews the removal
	// without performing it.
	if _, err := RenderHostBriefing(p, home, false); err != nil {
		t.Fatalf("assert: %v", err)
	}
	before := readFile(t, dest)
	results, err = RenderHostBriefing(briefingPack(t, "matt-core", ".claude/CLAUDE.md", ""), home, true)
	if err != nil {
		t.Fatalf("observe removal: %v", err)
	}
	if len(results) != 1 || results[0].Action != "would remove" {
		t.Fatalf("want one 'would remove'; got %+v", results)
	}
	if after := readFile(t, dest); after != before {
		t.Errorf("observe must not remove the block:\n%s", after)
	}
}

// An append-then-remove round trip restores the file's original bytes, so a pack added and
// dropped again leaves no growing run of blank lines behind.
func TestHostBriefingRoundTripRestoresBytes(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "# Mine\n\nSome prose.\n"
	if err := os.WriteFile(dest, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "Pack prose.\n")
	if _, err := RenderHostBriefing(p, home, false); err != nil {
		t.Fatalf("assert: %v", err)
	}
	if _, err := PruneHostBriefings([]*packload.Pack{p}, map[string]bool{}, home, false); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := readFile(t, dest); got != original {
		t.Errorf("round trip should restore the original bytes:\n--- want ---\n%q\n--- got ---\n%q", original, got)
	}
}

// A marker in the PACK's prose would let pack content decide the file's block structure.
// Refuse it by name.
func TestHostBriefingRefusesMarkerInProse(t *testing.T) {
	home := t.TempDir()
	p := briefingPack(t, "sneaky", ".claude/CLAUDE.md",
		"innocent\n"+hostBriefingEndMarker("sneaky")+"\nrogue trailing prose\n")
	if _, err := RenderHostBriefing(p, home, false); err == nil {
		t.Fatal("pack prose carrying a managed-block marker must be refused")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been written (stat err=%v)", err)
	}
}

// A reindented marker still parses (an editor may have touched it) and is re-emitted in
// canonical form — the block is found by MARKER, never by offset or exact whitespace.
func TestHostBriefingToleratesReindentedMarkers(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# Mine\n\n  " + hostBriefingBeginMarker("matt-core") + "\nold\n  " +
		hostBriefingEndMarker("matt-core") + "\n\n## Tail\n"
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	p := briefingPack(t, "matt-core", ".claude/CLAUDE.md", "new\n")
	if _, err := RenderHostBriefing(p, home, false); err != nil {
		t.Fatalf("assert: %v", err)
	}
	got := readFile(t, dest)
	if strings.Contains(got, "old") {
		t.Errorf("the indented block should have been rewritten:\n%s", got)
	}
	if !strings.Contains(got, "\n"+hostBriefingBeginMarker("matt-core")+"\n") {
		t.Errorf("the marker should be re-emitted canonically:\n%s", got)
	}
	for _, want := range []string{"# Mine", "## Tail", "new"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// A shipped pack renders at the host through the same entry apply --host will call. This
// is the one test that exercises real pack data rather than a fixture, so a manifest change
// that breaks host briefings is caught here.
func TestHostBriefingShippedClaudePack(t *testing.T) {
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	home := t.TempDir()
	results, err := RenderHostBriefing(claude, home, false)
	if err != nil {
		t.Fatalf("RenderHostBriefing: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("claude declares one briefing; got %+v", results)
	}
	// The shipped claude pack has no AGENTS.md of its own today, so the honest outcome is
	// a skip — NOT a written block and not a silent absence. If a pack file is added, this
	// flips to "rendered" and the assertion below documents which it was.
	switch results[0].Action {
	case "rendered":
		got := readFile(t, filepath.Join(home, ".claude", "CLAUDE.md"))
		if !strings.Contains(got, hostBriefingBeginMarker("claude")) {
			t.Errorf("rendered file is missing claude's block:\n%s", got)
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

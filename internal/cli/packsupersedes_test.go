package cli

// packsupersedes_test.go covers `supersedes` at the two SINGLE-PACK inspection commands,
// `pack lint` and `pack footprint`.
//
// One file for both verbs, following packloophole_test.go, and for the same reason: the
// invariant that binds them is that they must not DIVERGE. Both render through
// printClaimLines, so a claim that reaches one reaches the other — which is a property
// worth asserting rather than assuming, since the two inlined the same loop once already.
//
// # Why this file exists when the code did not change
//
// `supersedes` is a MANIFEST TOP-LEVEL key (packdecl.Manifest.Supersedes), not a
// contribution — docs/design/pack-capabilities.md §10 settled that on 2026-09-02 — and the
// accepted leaning's second half was "top-level, WITH footprint taught to print it
// explicitly". That half was recorded as unbuilt on the strength of a TEXTUAL negative:
// internal/cli/pack.go mentions supersession nowhere. It does not need to. The claim is
// produced by packload.FootprintOf (its `for _, s := range p.Supersessions()` loop) and
// rendered by printClaimLines, which formats string(c.Kind) generically — so the line has
// printed since packload gained the loop, and the grep that looked for the word in the CLI
// could not see it.
//
// What was genuinely missing is THIS: nothing pinned the behaviour at the surface the
// ruling names. packload's own TestSupersedesAppearsInTheFootprint asserts the Claim
// struct, one layer below the output, and would stay green if `pack footprint` stopped
// printing claims of a kind it does not recognise. So the residue was a test, and the
// test's job is to make the generic renderer's coverage of a non-kind claim an asserted
// property instead of a lucky one.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// supersedesManifest is the design's own example (docs/design/pack-capabilities.md §2, and
// packload's bedrockDecl): a pack that declares the OAuth-refresh job unnecessary, with the
// mandatory `because` that has to travel with it. The `env` contribution is there only so
// `pack lint` has something to call a contribution — a supersedes-only pack is a separate
// question, noted at the bottom of this file.
const supersedesManifest = `{"supersedes":[
	{"capability":"claude-oauth-refresh",
	 "because":"Bedrock overrides the OAuth path; no token is ever refreshed"}],
	"contributes":[{"kind":"env","vars":{"ACME_MODE":"fast"}}]}`

// noSupersedesManifest is the same pack with the key absent — the silence half.
const noSupersedesManifest = `{"contributes":[{"kind":"env","vars":{"ACME_MODE":"fast"}}]}`

// writeSupersedesPack scaffolds a pack with the given manifest, returning its dir. Built on
// `pack init` so the tree carries an AGENTS.md and a skills/ dir, which is what keeps
// `pack lint`'s "stages files nothing reads" rule quiet — this file is about the claim line,
// not about content classification.
func writeSupersedesPack(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if rc := packMain([]string{"init", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack init rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSupersedesPrintsAtBothInspectionCommands is the ruling's second half, asserted.
//
// The line must carry three things, because each is load-bearing somewhere else: the KIND
// (so a reader scanning a footprint can tell a supersession from a contribution), the
// CAPABILITY (the rendezvous key — never a loophole name, design §3), and the pack author's
// own `because` (mandatory precisely so it can be printed wherever the supersession takes
// effect — packdecl/supersedes.go's asymmetry with `serves`).
func TestSupersedesPrintsAtBothInspectionCommands(t *testing.T) {
	dir := writeSupersedesPack(t, supersedesManifest)
	for _, verb := range []string{"lint", "footprint"} {
		t.Run(verb, func(t *testing.T) {
			var out, errw bytes.Buffer
			if rc := packMain([]string{verb, dir}, &out, &errw, false); rc != 0 {
				t.Fatalf("%s rc = %d\n%s%s", verb, rc, out.String(), errw.String())
			}
			report := out.String()
			for _, want := range []string{
				"supersedes",
				"claude-oauth-refresh",
				"no token is ever refreshed",
			} {
				if !strings.Contains(report, want) {
					t.Errorf("`pack %s` output does not contain %q — a reader cannot learn, "+
						"before selecting this pack, that it retires a loophole they rely on:\n%s",
						verb, want, report)
				}
			}
		})
	}
}

// TestSupersedesIsNotFlaggedForReview: the claim NARROWS what the pack may do, so neither
// marker applies and the review tail must not mention it. Asserted at the OUTPUT rather
// than on the Claim struct (which packload already pins) because the markers are added by
// printClaimLines, which is where a future third marker would be added too.
func TestSupersedesIsNotFlaggedForReview(t *testing.T) {
	dir := writeSupersedesPack(t, supersedesManifest)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, "supersedes") {
			continue
		}
		if strings.Contains(line, "⚠") || strings.Contains(line, "review") {
			t.Errorf("the supersedes line carries a review marker; it grants nothing:\n%s", line)
		}
	}
	if strings.Contains(report, "worth review") {
		t.Errorf("a pack whose only unusual declaration is a supersession has a review "+
			"summary:\n%s", report)
	}
}

// TestNoSupersedesPrintsNothingExtra is the silence half: SILENCE IS NOT PARTICIPATION.
//
// It is the half a "does the line appear" test cannot supply. A renderer that printed a
// supersedes line unconditionally — with an empty capability, or a "none" — would pass the
// test above and would put a claim in front of every reader of every pack that makes none,
// which is how a footprint becomes wallpaper (ExecutablesClaimKind's own doc makes the
// same argument about the launch disclosure).
func TestNoSupersedesPrintsNothingExtra(t *testing.T) {
	dir := writeSupersedesPack(t, noSupersedesManifest)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if report := out.String(); strings.Contains(report, "supersedes") {
		t.Errorf("a pack that declares no supersession still shows one:\n%s", report)
	}
}

// TestSupersedesLineSurvivesTheGenericRenderer pins the SHAPE the three tests above depend
// on, from the other direction: the claim's kind is a DISPLAY LABEL
// (packload.SupersedesClaimKind), deliberately outside packdecl's closed kind set, and it
// prints only because printClaimLines formats every claim's kind generically rather than
// switching on the known kinds.
//
// So the line is one `switch c.Kind` away from vanishing with every per-kind test still
// green. This asserts the claim reaches the output beside a claim of a REAL kind — the env
// contribution declared in the same manifest — which is the property that would break.
func TestSupersedesLineSurvivesTheGenericRenderer(t *testing.T) {
	dir := writeSupersedesPack(t, supersedesManifest)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"footprint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("footprint rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String()
	if !strings.Contains(report, "ACME_MODE") {
		t.Fatalf("the env claim is missing, so this test is not measuring what it claims:\n%s", report)
	}
	if !strings.Contains(report, "supersedes") {
		t.Errorf("a registered kind prints and the supersedes display label does not — the "+
			"renderer has learned to switch on packdecl's kind set:\n%s", report)
	}
}

package packload

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// pack-execution-trust.md §6's worked example of what BAD looks like, as a test.
//
// §6 quotes four lines and says a new user cannot act on them:
//
//	reads-host .claude/settings.json
//	mount /home/you/notes -> /ctx/notes
//	installer https://example.com/install.sh
//	briefing .claude/CLAUDE.md
//
// and names the three things wrong with each: the kind is jargon, the path is relative to
// an unstated root, and nothing says whose machine it is on. This checks the three
// properties per claim rather than the exact sentence, so a wording change is free and a
// REGRESSION to the terse form is not.
func TestDisclosureSentenceNamesDirectionAndWhoseMachine(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
		{Kind: packdecl.KindMount, Host: "notes", Into: "notes"},
		{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://example.com/i.sh"},
		{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", After: "host:AGENTS.md"},
		{Kind: packdecl.KindEnv, Vars: map[string]string{"ACME_HOME": "/opt/acme"}},
	}}
	fp := FootprintOf(&Pack{Name: "acme", Decl: m})
	if len(fp.Claims) != 5 {
		t.Fatalf("fixture produced %d claims, want 5: %+v", len(fp.Claims), fp.Claims)
	}
	for _, c := range fp.Claims {
		got := c.DisclosureSentence()
		// A DIRECTION VERB, stated as a verb rather than implied by the kind name.
		if !strings.ContainsAny(got, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") ||
			!hasAnyPrefix(got, "READS", "SHOWS", "RUNS", "SETS") {
			t.Errorf("%s claim opens with no direction verb, so it reads as a label rather "+
				"than as a statement of what happens:\n  %s", c.Kind, got)
		}
		// WHOSE MACHINE. "host" means nothing to a new user (§6), so every line has to
		// place the crossing on one side or the other in words.
		if !strings.Contains(got, "YOUR HOME on this machine") &&
			!strings.Contains(got, "INSIDE THE JAIL") &&
			!strings.Contains(got, "inside the jail") {
			t.Errorf("%s claim never says whose machine it touches:\n  %s", c.Kind, got)
		}
		// THE ROOT the path is relative to. Every host-home path renders "~/…" because
		// packdecl refuses an absolute path and any "..", so the root is a fact.
		if (c.Kind == packdecl.KindReadsHost || c.Kind == packdecl.KindMount) &&
			!strings.Contains(got, "~/") {
			t.Errorf("%s claim leaves the path's root unstated:\n  %s", c.Kind, got)
		}
		// AND IT IS NOT THE TERSE LINE. This is the assertion that goes red if someone
		// reverts the rendering: the old form began with the bare kind token.
		if strings.HasPrefix(got, string(c.Kind)+" ") {
			t.Errorf("%s claim rendered as the terse token line §6 rejects:\n  %s", c.Kind, got)
		}
	}
}

// An installer says NOT PINNED in words, because §6 requires that "a pinned thing shows its
// pin; an unpinned thing must say so in words".
//
// The negative half is retired OQ-X1's surviving condition and is the reason this test is
// not just a substring check for "NOT PINNED": a digest shown beside an installer URL must
// not imply a depth an installer pin does not have (the script is pinned; what the script
// itself downloads at run time is not — §5). No manifest field carries a digest today, so
// the line must not claim one.
func TestDisclosureSentenceSaysAnInstallerIsUnpinned(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://example.com/i.sh"},
	}}
	got := FootprintOf(&Pack{Name: "acme", Decl: m}).Claims[0].DisclosureSentence()
	if !strings.Contains(got, "NOT PINNED") {
		t.Errorf("an installer URL is disclosed without saying it is unpinned:\n  %s", got)
	}
	if !strings.Contains(got, "https://example.com/i.sh") {
		t.Errorf("an installer sentence that does not name the URL is not actionable:\n  %s", got)
	}
	// No digest, by word or by shape. "pinned" itself is not forbidden — the line says NOT
	// PINNED — so this checks the two ways a digest could actually appear: a naming word, or a
	// raw hex run long enough to be one.
	for _, forbidden := range []string{"sha256", "sha512", "digest", "checksum"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Errorf("the installer line mentions %q, but no manifest field carries a digest — "+
				"a pin claimed here would imply a depth an installer pin does not have "+
				"(pack-execution-trust.md §5, retired OQ-X1's surviving condition):\n  %s",
				forbidden, got)
		}
	}
	if hexRun.MatchString(got) {
		t.Errorf("the installer line shows what looks like a digest, and an installer pin does "+
			"not cover what the script itself downloads (pack-execution-trust.md §5):\n  %s", got)
	}
}

// hexRun matches a hex string long enough to be a digest (or an abbreviated one).
var hexRun = regexp.MustCompile(`\b[0-9a-f]{8,}\b`)

// TWO CLAIMS THAT DIFFER MUST NEVER RENDER IDENTICALLY (§6: "The prose is a rendering, never
// the record").
//
// The banner is not the approval record, but a rendering that collapses two crossings onto
// one line shows a user one thing where two happened — which is the same failure mode as a
// non-injective claim string, arrived at from the display side. Asserted over claims
// FootprintOf actually produces, which is the set the guarantee is about.
func TestDisclosureSentenceDistinguishesClaimsThatDiffer(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		// Two host reads differing only in path.
		{Kind: packdecl.KindReadsHost, Host: ".netrc"},
		{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
		// Two mounts differing only in destination — the half that lives in Detail.
		{Kind: packdecl.KindMount, Host: "notes", Into: "notes"},
		{Kind: packdecl.KindMount, Host: "notes", Into: "notes2"},
		// Two installers differing only in URL, and one npm program that is not host access.
		{Kind: packdecl.KindProgram, Bin: "a", Via: "installer", URL: "https://x/1.sh"},
		{Kind: packdecl.KindProgram, Bin: "b", Via: "installer", URL: "https://x/2.sh"},
		{Kind: packdecl.KindProgram, Bin: "c", Via: "npm", Package: "c"},
		// Two env vars differing only in value.
		{Kind: packdecl.KindEnv, Vars: map[string]string{"A": "1"}},
		{Kind: packdecl.KindEnv, Vars: map[string]string{"B": "1"}},
	}}
	seen := map[string]Claim{}
	for _, c := range FootprintOf(&Pack{Name: "acme", Decl: m}).Claims {
		got := c.DisclosureSentence()
		if prev, dup := seen[got]; dup {
			t.Errorf("two different claims render to one line — %+v and %+v both produce:\n  %s",
				prev, c, got)
		}
		seen[got] = c
	}
}

// The BRIEFING branch reads a string FootprintOf produces elsewhere in the same file, so the
// two are coupled: this is the test that fails if the producer's wording moves and the
// renderer silently falls back to the terse line.
func TestDisclosureSentenceReadsTheProducersBriefingDetail(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", After: "host:AGENTS.md"},
	}}
	got := FootprintOf(&Pack{Name: "acme", Decl: m}).Claims[0].DisclosureSentence()
	if !strings.Contains(got, "~/AGENTS.md") {
		t.Errorf("the host briefing source is not rendered against its root — the renderer's "+
			"CutPrefix no longer matches FootprintOf's briefing Detail, so it fell back to "+
			"the terse line:\n  %s", got)
	}
	if !strings.Contains(got, ".claude/CLAUDE.md") {
		t.Errorf("the jail destination is dropped, so two briefings into different files "+
			"would render alike:\n  %s", got)
	}
}

// A LOOPHOLE claim is passed through, and its TARGET survives. Its Detail already reads as a
// §6 sentence, but the Detail alone does not say WHICH loophole made the crossing — and two
// loopholes can intercept one hostname or trust one CA path, so dropping the target would be
// the injectivity failure the doc comment forbids.
func TestDisclosureSentenceKeepsTheLoopholeTarget(t *testing.T) {
	c := Claim{
		Kind: packdecl.KindLoophole, Pack: "acme", Target: "broker:intercept:api.example.com",
		Detail: "INTERCEPTS api.example.com -> 127.0.0.1 — installs a CA trusted by every TLS client in the jail",
	}
	got := c.DisclosureSentence()
	if !strings.Contains(got, "broker:intercept:api.example.com") {
		t.Errorf("a loophole claim's target is dropped, so two loopholes crossing the same "+
			"host render alike:\n  %s", got)
	}
	if !strings.Contains(got, "INTERCEPTS") {
		t.Errorf("a loophole claim's own sentence was discarded rather than passed through:\n  %s", got)
	}
}

// An UNKNOWN kind falls back to the terse line rather than to nothing. run.disclosureClassOf
// fails closed by announcing an unclassified kind BEFORE the spawn, so this renderer must
// give that announcement something to print.
func TestDisclosureSentenceFallsBackForAnUnknownKind(t *testing.T) {
	c := Claim{Kind: packdecl.Kind("some-future-kind"), Target: "thing", Detail: "detail"}
	if got, want := c.DisclosureSentence(), "some-future-kind thing detail"; got != want {
		t.Errorf("unknown kind rendered %q, want the terse fallback %q", got, want)
	}
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

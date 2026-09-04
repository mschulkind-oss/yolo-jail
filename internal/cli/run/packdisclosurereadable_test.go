package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// THE BANNER ITSELF IS READABLE — pack-execution-trust.md §6, retargeted onto this surface by
// trust-paths.md OQ-TP9.
//
// AGAINST THE CALL SITE, NOT THE RENDERER. packload.Claim.DisclosureSentence has its own unit
// tests (packload/disclosuresentence_test.go); they all stay green if disclosedClaims never
// calls it, which is the shape AGENTS.md names as "not a test". This one drives the real
// printer — o.notePackHostAccess → disclosedClaims → the rendering — so deleting the call in
// disclosedClaims turns it red.
func TestLaunchBannerRendersClaimsAsSentences(t *testing.T) {
	p := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
			{Kind: packdecl.KindMount, Host: "notes", Into: "notes"},
			{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://example.com/i.sh"},
			{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", After: "host:AGENTS.md"},
			{Kind: packdecl.KindEnv, Vars: map[string]string{"ACME_HOME": "/opt/acme"}},
		},
	}}
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackHostAccess([]*packload.Pack{p})
	got := errBuf.String()

	// §6's own worked example of what BAD looks like: the terse token line, where the kind
	// leads and the path follows with no root and no direction. Its exact shape for this
	// fixture is asserted absent, because that is what a revert would print.
	for _, terse := range []string{
		"reads-host .claude/settings.json",
		"mount notes read-only",
		"program acme installer:",
		"briefing .claude/CLAUDE.md concat after host:",
		"env ACME_HOME =",
	} {
		if strings.Contains(got, terse) {
			t.Errorf("the banner still prints the terse token line §6 rejects (%q):\n%s", terse, got)
		}
	}

	// And what it must print instead, per claim: a direction, whose machine, and a path
	// against a stated root.
	for _, want := range []string{
		"READS a file from YOUR HOME on this machine (read-only): ~/.claude/settings.json",
		"SHOWS a path from YOUR HOME on this machine to the jail: ~/notes",
		"RUNS an installer downloaded from the internet, INSIDE THE JAIL",
		"NOT PINNED",
		"READS a file from YOUR HOME on this machine into the jail's briefing at .claude/CLAUDE.md: ~/AGENTS.md",
		"SETS an environment variable inside the jail: ACME_HOME=/opt/acme",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the banner does not say %q:\n%s", want, got)
		}
	}
}

// The KIND stays on the line as a trailing tag.
//
// §6 requires that the machine-comparable identity survive beside the prose — "the prose is a
// rendering, never the record" — and the tag is also what a reader matches a banner line to in
// `yolo pack footprint` and in `yolo config-ref`'s per-kind reference. Losing it would make the
// banner unmappable onto every other pack surface.
func TestLaunchBannerKeepsTheKindTag(t *testing.T) {
	p := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindReadsHost, Host: ".netrc"},
		},
	}}
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackHostAccess([]*packload.Pack{p})
	if got := errBuf.String(); !strings.Contains(got, "[reads-host]") {
		t.Errorf("the banner dropped the claim's kind tag:\n%s", got)
	}
}

// THE TRAP THE TRAILING TAG WALKS INTO, pinned so a future kind cannot spring it.
//
// The banner goes through richtext, whose tag regexp matches ANY `[word…]`. A bracketed token
// survives only because richtext.isStyleTag refuses it — every space-separated word must be a
// known STYLE word (bold, dim, red, green, yellow, blue, magenta, cyan). No kind collides
// today. A kind named after one would be silently ERASED from every banner line, in the one
// surface whose whole job is to be legible, with no error anywhere.
func TestEveryKindTagSurvivesRichtextRendering(t *testing.T) {
	for _, k := range packdecl.KnownKinds() {
		tag := "[" + string(k) + "]"
		if got := richtext.Strip(tag); got != tag {
			t.Errorf("kind %q renders as a richtext STYLE tag: Strip(%q) = %q — the banner's "+
				"trailing kind tag would vanish. Rename the kind, or stop bracketing the tag "+
				"in run.disclosedClaims", k, tag, got)
		}
		if got := richtext.ToANSI(tag); got != tag {
			t.Errorf("kind %q renders as a richtext STYLE tag under color: ToANSI(%q) = %q", k, tag, got)
		}
	}
}

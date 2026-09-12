package entrypoint

// hostownedformattingloss_test.go pins the ONE disclosure that survives OQ-CO12 for a TOML
// surface's comments, because the criterion no longer can.
//
// THE SITUATION, stated plainly. §11's criterion is KEYS AND VALUES since
// [OQ-CO12](../../docs/design/config-ownership-and-promotion.md) — and a comment is neither a
// key nor a value, so comment destruction is CONFORMANT and no criterion test will ever fail
// for it again. That is deliberate: `own` composes the whole file through the surface's codec
// and codec.TOML has no comment channel, while teaching it one would put rmw's formatting
// knowledge into the capture path. What §11 offers in exchange is that the switch "is never
// silent on any axis", and for comments the ONLY thing holding that up is
// HostRenderResult.Formatting:
//
//	WouldChange  true — but in the same undifferentiated word it uses for a re-sorted key,
//	             so it says a byte moved, never that prose was destroyed.
//	Archived     the file as yolo found it — but only on the WRITE. A dry run makes no copy
//	             (deliberately, HostRenderResult.Archived), so it is not a warning at all.
//	Formatting   the one field that names the loss BEFORE the one-way door.
//
// ⚠ WHICH IS WHY IT MUST NOT BE COMPUTED FROM THE RMW SIMULATION. hostFormattingLosses
// simulates an rmw write and reports the comments THAT would drop — and rmw REATTACHES
// comments, so the honest rmw answer is "almost none". Run against an `own` surface it
// reported approximately nothing on the one render that drops all of them, which is exactly
// how a conformant-by-ruling loss becomes an unreported one. Fixed by branching on mechanism
// the way hostMechanismWouldChange beside it already did.
//
// Every test here renders into a t.TempDir() home. ⚠ Never point one at a real home.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

func formattingPack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "toml",
		"path":     "~/.acme/settings.toml",
		"defaults": map[string]any{"theme": "system"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}}}}
}

// formattingHome seeds a home, applies once under `assert`, and returns the home and path.
func formattingHome(t *testing.T, seed string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, ".acme/settings.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderHostPack(formattingPack(t), home, render.OwnershipAssert, false, nil); err != nil {
		t.Fatalf("the `assert` apply: %v", err)
	}
	return home, path
}

// observeOwn runs the `own` render in OBSERVE posture — the dry-run preview, which is where
// this disclosure has to land, since after the write the comments are already gone.
func observeOwn(t *testing.T, home string) HostRenderResult {
	t.Helper()
	res, err := RenderHostPack(formattingPack(t), home, render.OwnershipOwn, true, nil)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want one surface, got %d", len(res))
	}
	return res[0]
}

// THE DISCLOSURE. A commented TOML file, switched to `own`, says so in observe.
func TestSwitchingToOwnDisclosesThatCommentsWillNotSurvive(t *testing.T) {
	home, _ := formattingHome(t, "# a long explanatory note about this file\n"+
		"apiKeyHelper = \"/x/y\"\n\n# and one about the section\n[permissions]\nask = [\"Bash(rm:*)\"]\n")

	r := observeOwn(t, home)
	if len(r.Formatting) == 0 {
		t.Fatalf("the owned render would destroy every comment in this file and said "+
			"nothing.\n\nHostRenderResult.Formatting is the LAST disclosure this loss has: "+
			"OQ-CO12 made comment destruction conformant under §11's keys-and-values "+
			"criterion, so no criterion test fails for it, and WouldChange cannot tell the "+
			"user a comment went rather than a key moving. If hostFormattingLosses has gone "+
			"back to running the rmw simulation for every mechanism, it is answering with "+
			"the comments RMW would drop — and rmw REATTACHES them, so that answer is "+
			"~none on the one render that drops all of them.\ngot: %+v", r)
	}
	if joined := strings.Join(r.Formatting, " "); !strings.Contains(joined, "NOT preserved") {
		t.Errorf("the disclosure does not say the comments are not preserved: %q", joined)
	}
	// AND IT IS A DRY RUN: the preview must not have written anything, or the "before the
	// one-way door" property this test exists for is gone.
	if r.Archived != "" {
		t.Errorf("observe took an archive: %q — a dry run makes no copy", r.Archived)
	}
}

// AND IT STOPS ONCE THERE IS NOTHING LEFT TO LOSE, which is the half a fix is most likely to
// get wrong: yolo's OWN generated header is comments, so a probe spelled "does this file hold
// a comment?" is true of every file an owned render has ever written. configResultTier reads
// this field to decide a destination loses something of the user's, so answering yes forever
// would park a steady-state owned home at tierLoss permanently and the signal would stop
// meaning anything.
func TestASteadyStateOwnedTOMLFileReportsNoCommentLoss(t *testing.T) {
	home, path := formattingHome(t, "# a note\napiKeyHelper = \"/x/y\"\n")

	if _, err := RenderHostPack(formattingPack(t), home, render.OwnershipOwn, false, nil); err != nil {
		t.Fatalf("the first `own` apply: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(written), "#") {
		t.Fatalf("this test is only meaningful while the owned render writes a comment "+
			"header of its own; it no longer does, so the trap it guards is gone:\n%s", written)
	}
	if strings.Contains(string(written), "a note") {
		t.Fatalf("the user's comment survived, so the disclosure above is now false and this "+
			"whole file needs rewriting:\n%s", written)
	}

	if r := observeOwn(t, home); len(r.Formatting) != 0 {
		t.Errorf("a steady-state owned file, holding only yolo's OWN header, still reports a "+
			"comment loss: %q\n\nThe probe has to ask about the file MINUS generatedHeader — "+
			"the render reproduces the header exactly, so it is never what is at risk.",
			r.Formatting)
	}
}

// AND AN UNCOMMENTED FILE NEVER REPORTS ONE, so the disclosure stays a signal rather than a
// line every TOML surface carries.
func TestAnUncommentedTOMLFileReportsNoCommentLoss(t *testing.T) {
	home, _ := formattingHome(t, "apiKeyHelper = \"/x/y\"\n\n[permissions]\nask = [\"Bash(rm:*)\"]\n")
	if r := observeOwn(t, home); len(r.Formatting) != 0 {
		t.Errorf("a file with no comments reported a comment loss: %q", r.Formatting)
	}
}

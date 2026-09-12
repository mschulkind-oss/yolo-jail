package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// A `launch` claim must name what the contribution actually carries, which since ab0d4a39 is
// usually its ALIASES and not its flags.
//
// The bare line is the failure being pinned, not a cosmetic one. `yolo pack footprint` and
// `yolo pack lint` are where a reader learns what a pack does — the launch banner skips this
// kind (packloopholes.go: disclosureSkip) — and a claim printed with an empty detail is read
// against the kind's documented meaning, "inject flags after a binary". So `launch  copilot`
// asserted an injection copilot's launch contribution does not make, while its real content
// (`-y` is the same switch as `--yolo`, so do not pass both) appeared nowhere in the report.
func TestLaunchClaimNamesItsAliasesAndNotOnlyItsFlags(t *testing.T) {
	detail := func(c packdecl.Contribution) string {
		cs := claimSet(FootprintOf(pk("p", &packdecl.Manifest{Contributes: []packdecl.Contribution{c}})))
		claim, ok := cs["launch tool"]
		if !ok {
			t.Fatalf("no launch claim for %+v", c)
		}
		return claim.Detail
	}

	// Flags only: unchanged from before the aliases were rendered.
	if got := detail(packdecl.Contribution{
		Kind: packdecl.KindLaunch, Bin: "tool", Flags: []string{"--yolo", "--fast"},
	}); got != "--yolo --fast" {
		t.Errorf("flags-only launch claim = %q, want %q", got, "--yolo --fast")
	}

	// Aliases only — the SHIPPED shape. An empty detail here is the defect.
	got := detail(packdecl.Contribution{
		Kind: packdecl.KindLaunch, Bin: "tool",
		Aliases: map[string][]string{"--yolo": {"-y"}},
	})
	if got == "" {
		t.Fatal("a launch contribution carrying only aliases renders an EMPTY claim, so the " +
			"report prints `launch  tool` — an injection it does not make — and says nothing " +
			"about the alias, which is its whole content")
	}
	if !strings.Contains(got, "-y") || !strings.Contains(got, "--yolo") {
		t.Errorf("aliases-only launch claim = %q, want both spellings of the switch named", got)
	}

	// Both halves, with the alias entries SORTED BY FLAG. Exact-matched, because the thing
	// that goes wrong here is map iteration order: an unsorted claim renders differently run
	// to run, so a reader diffing two footprints sees a change that is not one.
	both := detail(packdecl.Contribution{
		Kind: packdecl.KindLaunch, Bin: "tool", Flags: []string{"--fast"},
		Aliases: map[string][]string{"--yolo": {"-y"}, "--fast": {"-f"}},
	})
	if want := "--fast; alias -f → --fast; alias -y → --yolo"; both != want {
		t.Errorf("launch claim = %q, want %q", both, want)
	}
}

// AGAINST THE SHIPPED DECLARATION, not only the renderer: the case above would stay green if
// every pack stopped declaring aliases, and copilot's is the one the report is actually read
// for. This is the half that fails if the pack drops its flagless `launch` entry — the entry
// that exists ONLY to carry the alias, since AutonomyLaunch has nowhere to put one.
func TestTheCopilotPackDisclosesItsFlagAlias(t *testing.T) {
	packs, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	var copilot *Pack
	for _, p := range packs {
		if p.Name == "copilot" {
			copilot = p
		}
	}
	if copilot == nil {
		t.Fatal("no copilot pack in the embedded set")
	}
	for _, c := range FootprintOf(copilot).Claims {
		if c.Kind != packdecl.KindLaunch {
			continue
		}
		if !strings.Contains(c.Detail, "-y") {
			t.Errorf("copilot's launch claim = %q — the `-y` alias is the only thing this "+
				"contribution carries, and the report does not name it", c.Detail)
		}
		return
	}
	t.Error("the copilot pack declares no launch claim at all: `-y` no longer suppresses the " +
		"autonomy posture's `--yolo`, and a user who types it gets both spellings")
}

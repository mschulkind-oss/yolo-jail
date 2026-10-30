package packload

// packtreeprebuilt_test.go pins the resolver's build-or-prebuilt half for a pack-tree program
// (docs/design/pack-buildable-programs.md §4, §7, OQ-BG2 as ruled 2026-10-23): the build while the
// source is present, never the prebuilt in its place; the platform's prebuilt where the source is
// absent or the user opted into it (BG-D18); and a reason naming what is declared otherwise.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func prebuiltProgram(t *testing.T, withSource bool) Fork {
	t.Helper()
	c := packTreeContribution()
	c.Prebuilt = map[string]packdecl.PrebuiltBuild{
		"linux/amd64":  {URL: "https://example.invalid/board-linux-amd64", SHA256: strings.Repeat("a", 64)},
		"darwin/arm64": {URL: "https://example.invalid/board-darwin-arm64", SHA256: strings.Repeat("b", 64)},
	}
	p := claimPack(t, "fx", c)
	if withSource {
		writeTree(t, filepath.Join(p.Root, "tools", "board-cli"), map[string]string{"main.c": "int main(){}\n"})
	}
	return PackTreePrograms([]*Pack{p})[0]
}

func TestAPresentSourceBuildsAndNeverTakesThePrebuilt(t *testing.T) {
	f := prebuiltProgram(t, true)
	digest, _ := f.Digest()
	pin := PackTreeResolve(f, "linux/amd64", false)
	if pin.Commit != digest || pin.Fork.IsPrebuilt() || pin.Reason != "" {
		t.Fatalf("a present source resolved to %+v, want its build at %s", pin, digest)
	}
	if pin.Fork.PackTreeRecipe() != packdecl.PackTreeRecipe("tools/board-cli", "make", "dist/board.js") {
		t.Errorf("a build's recipe is not the build recipe")
	}
}

func TestAnAbsentSourceTakesThePlatformsPrebuilt(t *testing.T) {
	f := prebuiltProgram(t, false)
	pin := PackTreeResolve(f, "linux/amd64", false)
	if !pin.Fork.IsPrebuilt() || pin.Commit != strings.Repeat("a", 64) || pin.Reason != "" {
		t.Fatalf("an absent source resolved to %+v, want linux/amd64's prebuilt", pin)
	}
	if pin.Fork.Prebuilt.URL != "https://example.invalid/board-linux-amd64" || pin.Fork.ProgramRun() != "board" {
		t.Errorf("prebuilt pin = %+v (run %q)", pin.Fork.Prebuilt, pin.Fork.ProgramRun())
	}
	if !strings.Contains(pin.Fork.PrebuiltWhy, "no tools/board-cli in the pack") {
		t.Errorf("why = %q", pin.Fork.PrebuiltWhy)
	}
	// A prebuilt's recipe is its own: a hit on it never serves a build, nor the reverse.
	if r := pin.Fork.PackTreeRecipe(); r != packdecl.PackTreePrebuiltRecipe("board") || r == f.PackTreeRecipe() {
		t.Errorf("prebuilt recipe %q, build recipe %q", r, f.PackTreeRecipe())
	}
	if f.IsPrebuilt() {
		t.Errorf("resolving changed the Fork it was handed")
	}
}

func TestAnAbsentSourceWithNoPrebuiltForThePlatformNamesWhatIsDeclared(t *testing.T) {
	f := prebuiltProgram(t, false)
	pin := PackTreeResolve(f, "linux/arm64", false)
	if pin.Commit != "" || pin.Fork.IsPrebuilt() {
		t.Fatalf("resolved to %+v, want a reason", pin)
	}
	for _, want := range []string{"pack fx", "tools/board-cli", "linux/arm64", "darwin/arm64, linux/amd64"} {
		if !strings.Contains(pin.Reason, want) {
			t.Errorf("the reason does not name %q: %s", want, pin.Reason)
		}
	}
	f.Prebuilts = nil
	if pin := PackTreeResolve(f, "linux/amd64", false); !strings.Contains(pin.Reason, "(it declares: none)") {
		t.Errorf("no prebuilt at all: %q", pin.Reason)
	}
}

// OQ-BG2 AS RULED: a present source is never answered with the prebuilt on yolo's own — the resolver
// has no "unbuildable" input at all, so a builder that cannot run is the caller's refusal. The
// user's opt-in (programs.overrides.<bin>.prebuilt, BG-D18) takes the declared artifact even with
// the source present, and with none declared for the platform it is a reason naming the setting and
// the platforms the pack does declare.
func TestThePrebuiltOptInTakesTheArtifactOverAPresentSource(t *testing.T) {
	f := prebuiltProgram(t, true)
	pin := PackTreeResolve(f, "darwin/arm64", true)
	if !pin.Fork.IsPrebuilt() || pin.Commit != strings.Repeat("b", 64) || pin.Reason != "" {
		t.Fatalf("an opted-in program with its source present resolved to %+v, want darwin/arm64's prebuilt", pin)
	}
	if !strings.Contains(pin.Fork.PrebuiltWhy, "programs.overrides.board.prebuilt") {
		t.Errorf("why = %q, want the opt-in named", pin.Fork.PrebuiltWhy)
	}
	pin = PackTreeResolve(f, "darwin/amd64", true)
	if pin.Fork.IsPrebuilt() || pin.Commit != "" {
		t.Fatalf("an opt-in with no prebuilt for the platform resolved to %+v, want a reason", pin)
	}
	for _, want := range []string{"programs.overrides.board.prebuilt", "darwin/amd64", "darwin/arm64, linux/amd64"} {
		if !strings.Contains(pin.Reason, want) {
			t.Errorf("the opt-in's reason does not name %q: %s", want, pin.Reason)
		}
	}
}

// THE REFUSAL'S OPT-IN CLAUSE names the setting only where the pack declares an artifact for the
// platform, since otherwise there is nothing to opt into.
func TestThePrebuiltOptInStepIsNamedOnlyWhereOneIsDeclared(t *testing.T) {
	f := prebuiltProgram(t, true)
	if step := f.PrebuiltOptInStep("darwin/arm64"); !strings.Contains(step, "`yolo config set programs.overrides.board.prebuilt true --user`") ||
		!strings.Contains(step, "darwin/arm64") {
		t.Errorf("step = %q, want the opt-in for board on darwin/arm64", step)
	}
	if step := f.PrebuiltOptInStep("darwin/amd64"); step != "" {
		t.Errorf("a platform with no prebuilt named an opt-in: %q", step)
	}
	if c := f.PrebuiltOptInClause("darwin/arm64"); c != "; "+f.PrebuiltOptInStep("darwin/arm64") {
		t.Errorf("clause = %q, want \"; \" and the step", c)
	}
	// A DEV OVERRIDE'S CHECKOUT names none: its `path` cannot be combined with `prebuilt`, so following
	// the advice would be a validation error.
	co := f
	co.Checkout = "/home/u/src/board"
	if step, c := co.PrebuiltOptInStep("darwin/arm64"), co.PrebuiltOptInClause("darwin/arm64"); step != "" || c != "" {
		t.Errorf("a checkout build named the opt-in: %q, %q", step, c)
	}
}

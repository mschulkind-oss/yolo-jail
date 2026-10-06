package hostfloor

import (
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// lookupOnly is a launch PATH lookup that finds the named programs and nothing else.
func lookupOnly(have ...string) func(string) (string, error) {
	return func(bin string) (string, error) {
		for _, h := range have {
			if h == bin {
				return "/launch/" + bin, nil
			}
		}
		return "", errors.New("not found")
	}
}

// TestOutrankerGivesAProgramToTheManagerTheOrderRanksFirst (PS-D12): a program leaves the floor
// only when the user's order ranks a manager above the pack's recipe AND that manager is on the
// launch PATH with a recipe for it. No order, a manager this PATH lacks, a manager the pack hints
// nothing for, `pack` ranked first, and a fork's program all keep the floor's entry.
func TestOutrankerGivesAProgramToTheManagerTheOrderRanksFirst(t *testing.T) {
	npm := npmProgram("opencode", "opencode", "opencode-ai")
	npm.Install.InstallHints = map[string]string{"brew": "opencode", "pacman": "opencode"}
	fork := Program{Pack: "pi", Install: packdecl.Install{Kind: packdecl.InstallKindSource, Bin: "pi",
		InstallHints: map[string]string{"brew": "pi-coding-agent"}}}
	order := func(o ...string) func(string) []string { return func(string) []string { return o } }
	for _, c := range []struct {
		name  string
		order func(string) []string
		path  []string
		p     Program
		want  string
	}{
		{"brew first, on PATH", order("brew"), []string{"brew"}, npm, "ranks brew first for opencode"},
		{"brew first, absent; pacman next", order("brew", "pacman"), []string{"pacman"}, npm, "ranks pacman first"},
		{"no order", order(), []string{"brew"}, npm, ""},
		{"manager not on PATH", order("brew"), nil, npm, ""},
		{"manager with no recipe", order("apt"), []string{"apt"}, npm, ""},
		{"the pack's recipe first", order("pack", "brew"), []string{"brew"}, npm, ""},
		{"a fork's program", order("brew"), []string{"brew"}, fork, ""},
		// PS-D2's closure build is not written, so nix ranked first keeps the floor's entry: the
		// order's winner is nix, and the floor answers rather than `nix profile install`.
		{"nix first, on PATH", order("nix", "brew"), []string{"nix", "brew"},
			Program{Pack: "x", Install: packdecl.Install{Kind: "npm", Bin: "x",
				InstallHints: map[string]string{"nix": "x", "brew": "x"}}}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Outranker(c.order, lookupOnly(c.path...))(c.p)
			if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
				t.Errorf("Outranked = %q, want %q", got, c.want)
			}
		})
	}
}

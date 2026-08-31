package packdecl

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A pack destination may not land on the jail's PATH. The rule is guidance toward
// kind "program" — the kind that OWNS a name on PATH and says so in the footprint —
// not a containment boundary, and the tests below assert the shape of the rule rather
// than any security property.

// manifestWithDest builds a one-contribution manifest of `kind` whose destination
// field is `dest`, and returns the validation problems.
func destProblems(t *testing.T, kind, dest string) []string {
	t.Helper()
	c := Contribution{Kind: Kind(kind)}
	switch Kind(kind) {
	case KindState:
		c.At = dest
	case KindFiles:
		c.From = "tree"
		c.Into = dest
	default:
		c.Into = dest
	}
	return validateContribution("contributes[0]", c)
}

// TestJailPathDestinationRefused covers the three ways a destination reaches PATH.
// Only the exact match would be a rule an author routes around by accident.
func TestJailPathDestinationRefused(t *testing.T) {
	cases := []struct {
		name, kind, dest string
	}{
		{"exact PATH dir", "files", ".local/bin"},
		{"inside a PATH dir", "files", ".local/bin/tools"},
		{"parent of a PATH dir", "files", ".local"},
		{"the launcher dir", "files", ".yolo/bin/launch"},
		{"the blocker dir", "files", ".yolo/bin/block"},
		{"parent of both", "files", ".yolo/bin"},
		{"npm bin", "files", ".npm-global/bin"},
		{"go bin", "files", "go/bin"},
		{"trailing slash", "files", ".local/bin/"},
		{"dot-slash prefix", "files", "./.local/bin"},
		// The writable kind, which is the stronger case: a state dir on PATH lets
		// whatever runs in the jail add its own executable there later.
		{"state at a PATH dir", "state", ".local/bin"},
		{"state above a PATH dir", "state", ".local"},
		// Shared by omission-proofing: skills and briefing are not exempt.
		{"skills into a PATH dir", "skills", ".local/bin"},
		{"briefing into a PATH dir", "briefing", ".local/bin/AGENTS.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := destProblems(t, tc.kind, tc.dest)
			if len(problems) == 0 {
				t.Fatalf("%s -> %q was accepted, want refused", tc.kind, tc.dest)
			}
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, "on the jail's PATH") {
				t.Errorf("refused for the wrong reason:\n%s", joined)
			}
			// The message's whole job is to name the alternative.
			if !strings.Contains(joined, `"program"`) {
				t.Errorf("refusal does not point at kind \"program\":\n%s", joined)
			}
		})
	}
}

// TestNonPathDestinationsStillAccepted is the half that keeps the rule narrow. Every
// shipped pack's destination is here, plus ~/.claude/bin — the matt-fzf shape, a tree
// of scripts an agent invokes by an explicit configured path. It is NOT on PATH and
// must keep working, or the rule has quietly become "packs may not ship tools".
func TestNonPathDestinationsStillAccepted(t *testing.T) {
	for _, dest := range []string{
		".claude/bin",
		".claude/skills",
		".claude/CLAUDE.md",
		".codex/skills",
		".pi/agent/skills",
		".config/opencode/AGENTS.md",
		".gemini/config/skills",
		".localstuff", // shares a prefix with .local but is not it
		"golang/bin",  // shares a prefix with go/bin but is not it
		".yolo/state",
	} {
		t.Run(dest, func(t *testing.T) {
			for _, kind := range []string{"files", "skills", "state"} {
				if problems := destProblems(t, kind, dest); len(problems) != 0 {
					t.Errorf("%s -> %q refused, want accepted:\n%s",
						kind, dest, strings.Join(problems, "\n"))
				}
			}
		})
	}
}

// TestEveryJailPathDirIsRefused walks paths.JailPathHomeDirs itself, so an entry ADDED
// to that list without a matching guard call cannot pass unnoticed. The table above
// names cases a human chose; this one names every case the list claims.
func TestEveryJailPathDirIsRefused(t *testing.T) {
	if len(paths.JailPathHomeDirs) == 0 {
		t.Fatal("paths.JailPathHomeDirs is empty — the guard guards nothing")
	}
	for _, dir := range paths.JailPathHomeDirs {
		if problems := destProblems(t, "files", dir); len(problems) == 0 {
			t.Errorf("%q is in JailPathHomeDirs but a files destination there was accepted", dir)
		}
	}
}

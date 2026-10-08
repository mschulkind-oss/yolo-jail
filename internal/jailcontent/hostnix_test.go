package jailcontent

import (
	"strings"
	"testing"
)

// A jail whose nix goes through the host daemon is told that its links are not GC
// roots, and a jail without the daemon is told nothing about nix at all
// (docs/design/in-jail-nix-roots.md §8). The claim is measured, not inferred: a root the
// jail asks for is recorded under the jail's own spelling of the out-link, and the host
// daemon deletes it as stale at the next GC or root query (§3, M1).
func TestBriefingStatesThatInJailNixLinksAreNotRoots(t *testing.T) {
	with := BriefingContent(BriefingInput{Workspace: "/w", HostNix: true})
	for _, want := range []string{"NIX_REMOTE=daemon", "yolo nix-roots list", "rebuild it"} {
		if !strings.Contains(with, want) {
			t.Errorf("a host-nix briefing is missing %q:\n%s", want, with)
		}
	}
	// Inside the Environment section, which is where an agent looks for how its tools
	// reach the host — not appended after the capability sections.
	env := with[strings.Index(with, "## Environment"):]
	if next := strings.Index(env[1:], "\n## "); next >= 0 {
		env = env[:next+1]
	}
	if !strings.Contains(env, "**Nix**") {
		t.Errorf("the nix line is not in the Environment section:\n%s", env)
	}

	if without := BriefingContent(BriefingInput{Workspace: "/w"}); strings.Contains(without, "**Nix**") {
		t.Errorf("a jail with no host nix daemon was told about one:\n%s", without)
	}
}

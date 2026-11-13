package jailcontent

import (
	"strings"
	"testing"
)

// A jail whose nix goes through the host daemon is told that its links are not GC
// roots, and a jail without the daemon is told nothing about nix at all
// (docs/reference/in-jail-nix-roots.md "The command and the briefing"). The claim is measured, not inferred: a root the
// jail asks for is recorded under the jail's own spelling of the out-link, and the host
// daemon deletes it as stale at the next GC or root query (§3, M1).
func TestBriefingStatesThatInJailNixLinksAreNotRoots(t *testing.T) {
	// In the limits topic, beside how the jail's other tools reach the host.
	with := topicText(BriefingInput{Workspace: "/w", HostNix: true}, "limits.md")
	for _, want := range []string{"**Nix**", "NIX_REMOTE=daemon", "yolo nix-roots list", "rebuild it"} {
		if !strings.Contains(with, want) {
			t.Errorf("a host-nix limits topic is missing %q:\n%s", want, with)
		}
	}

	if without := reachable(BriefingInput{Workspace: "/w"}); strings.Contains(without, "**Nix**") {
		t.Errorf("a jail with no host nix daemon was told about one:\n%s", without)
	}
}

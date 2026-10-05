package jailcontent

import (
	"strings"
	"testing"
)

// A COPIED CONTEXT ENTRY SAYS IT IS A SNAPSHOT (docs/design/context-mounts.md CX-D23): macos-user
// copies a pack's single-file `mount` into the context dir at launch, so the agent is told a host
// edit arrives at the next launch — and the link sentence, which is about the other entries, does
// not describe it.
func TestABriefedCopyIsMarkedAndTheLinkSentenceLeavesItOut(t *testing.T) {
	const dir = "/var/yolo-jail/ctx/yolo-ws-abcd1234"
	copied := ContextMount{Path: dir + "/acme/notes.txt", Host: "/Users/me/notes.txt", Pack: "acme", Copied: true}
	linked := ContextMount{Path: dir + "/lib", Host: "/opt/lib"}

	both := BriefingContent(BriefingInput{Workspace: "/Users/Shared/yolo/proj", Mechanism: "macos-user",
		ContextDir: dir, ContextMounts: []ContextMount{linked, copied}})
	for _, want := range []string{
		"- `" + dir + "/acme/notes.txt` (read-only; copied at launch; host edits arrive at the next " +
			"launch; host `/Users/me/notes.txt`; from pack `acme`)",
		"- `" + dir + "/lib` (read-only; host `/opt/lib`)",
		"Each one not copied is a link to the host folder itself",
	} {
		if !strings.Contains(both, want) {
			t.Errorf("the briefing lacks %q:\n%s", want, both)
		}
	}

	onlyCopy := BriefingContent(BriefingInput{Workspace: "/Users/Shared/yolo/proj", Mechanism: "macos-user",
		ContextDir: dir, ContextMounts: []ContextMount{copied}})
	if strings.Contains(onlyCopy, "is a link to the host folder itself") {
		t.Errorf("a briefing whose one entry is a copy calls it a link:\n%s", onlyCopy)
	}

	onlyLink := BriefingContent(BriefingInput{Workspace: "/Users/Shared/yolo/proj", Mechanism: "macos-user",
		ContextDir: dir, ContextMounts: []ContextMount{linked}})
	if !strings.Contains(onlyLink, "Each is a link to the host folder itself") {
		t.Errorf("a briefing of links alone lost its sentence:\n%s", onlyLink)
	}
}

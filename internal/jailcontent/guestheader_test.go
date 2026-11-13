package jailcontent

import (
	"strings"
	"testing"
)

// The Seatbelt setup is the macos-user backend (env-manager plan Phase 7.1, EMP-D1), so its
// briefing states the setup name AND the one fact of that backend an agent most needs: what in
// its home is its own. Since slice 12a (macos-user-model-plan.md MMP-D25) the home is THIS
// workspace's own `.yolo/home` in both modes, and only its links to machine-wide state are shared
// by every workspace; the header said the whole account home was shared until the Mac run of
// 2026-11-03. The seatbelt-self setup says the same; reaching the same backend by the other setup
// must not drop it. A guest with a container mechanism (the Linux spelling, unbuilt) does not say
// it.
//
// Since the briefing diet (docs/reference/agent-briefings.md#the-core) the header names the setup, the
// core's "What survives a restart" line says the home persists, and the full sentences open the
// storage topic.
func TestTheSeatbeltSetupHeaderStatesWhatIsShared(t *testing.T) {
	const shared = "Your home is THIS workspace's own"
	native := func(notch string) BriefingInput {
		return BriefingInput{Workspace: "/Users/me/proj", Mechanism: "macos-user", IsMacOS: true,
			Home: "/Users/me/proj/.yolo/setups/" + notch + "/home", Setup: setupForHeaderTest(notch)}
	}
	for _, notch := range []string{"guest", "jail"} {
		storage := topicText(native(notch), "storage.md")
		for _, stale := range []string{"The account is shared by every workspace", "linked into THIS workspace's own"} {
			if strings.Contains(storage, stale) {
				t.Errorf("%s: the macos-user storage topic still describes the pre-12a shared account home (%q):\n%s", notch, stale, storage)
			}
		}
		for _, want := range []string{shared, "shared by every workspace on this machine"} {
			if !strings.Contains(storage, want) {
				t.Errorf("%s: the macos-user storage topic does not say %q:\n%s", notch, want, storage)
			}
		}
		if core := BriefingContent(native(notch)); !strings.Contains(core, "your home (this workspace's own; its machine-wide logins and caches are shared)") {
			t.Errorf("%s: the macos-user core does not say the home is the workspace's own:\n%s", notch, core)
		}
	}
	macGuest := strings.Join(setupHeaderForTest("guest", "macos-user", true), "\n")
	for _, want := range []string{"# YOLO Environment — setup guest", "setup **guest**: Seatbelt"} {
		if !strings.Contains(macGuest, want) {
			t.Errorf("the macOS guest header does not say %q:\n%s", want, macGuest)
		}
	}
	if strings.Contains(macGuest, "setup jail") {
		t.Errorf("the macOS guest header borrowed the jail notch's title:\n%s", macGuest)
	}
	linuxGuest := reachable(BriefingInput{Workspace: "/w", Mechanism: "podman", Setup: setupForHeaderTest("guest")})
	if strings.Contains(linuxGuest, shared) {
		t.Errorf("a guest with no macos-user mechanism claims the macos-user account:\n%s", linuxGuest)
	}
}

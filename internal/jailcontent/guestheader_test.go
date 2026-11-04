package jailcontent

import (
	"strings"
	"testing"
)

// The macOS guest notch is the macos-user backend (env-manager plan Phase 7.1, EMP-D1), so its
// briefing header states the guest notch AND the one fact of that backend an agent most needs:
// what in its home is its own. Since slice 12a (macos-user-model-plan.md MMP-D25) the home is
// THIS workspace's own `.yolo/home` in both modes, and only its links to machine-wide state are
// shared by every workspace; the header said the whole account home was shared until the Mac run
// of 2026-11-03. The jail-without-a-container header says the same; reaching the same backend by
// the other notch must not drop it. A guest with a container mechanism (the Linux spelling,
// unbuilt) does not say it.
func TestTheMacosGuestHeaderStatesWhatIsShared(t *testing.T) {
	const shared = "Your home is THIS workspace's own"
	for _, header := range []string{
		strings.Join(confinementHeader("guest", "macos-user", true), "\n"),
		strings.Join(confinementHeader("jail", "macos-user", true), "\n"),
	} {
		for _, stale := range []string{"The account is shared by every workspace", "linked into THIS workspace's own"} {
			if strings.Contains(header, stale) {
				t.Errorf("the macos-user header still describes the pre-12a shared account home (%q):\n%s", stale, header)
			}
		}
		if !strings.Contains(header, "shared by every workspace on this machine") {
			t.Errorf("the macos-user header no longer says what IS shared:\n%s", header)
		}
	}
	macGuest := strings.Join(confinementHeader("guest", "macos-user", true), "\n")
	for _, want := range []string{"# YOLO Environment — guest", "**guest** confinement level", shared,
		"Seatbelt"} {
		if !strings.Contains(macGuest, want) {
			t.Errorf("the macOS guest header does not say %q:\n%s", want, macGuest)
		}
	}
	if strings.Contains(macGuest, "jail (native, no container)") {
		t.Errorf("the macOS guest header borrowed the jail notch's title:\n%s", macGuest)
	}
	jailNative := strings.Join(confinementHeader("jail", "macos-user", true), "\n")
	if !strings.Contains(jailNative, shared) {
		t.Errorf("the jail-notch macos-user header lost its own-home sentence:\n%s", jailNative)
	}
	linuxGuest := strings.Join(confinementHeader("guest", "podman", false), "\n")
	if strings.Contains(linuxGuest, shared) {
		t.Errorf("a guest with no macos-user mechanism claims the macos-user account:\n%s", linuxGuest)
	}
}

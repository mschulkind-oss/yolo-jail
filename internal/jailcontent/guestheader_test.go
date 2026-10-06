package jailcontent

import (
	"strings"
	"testing"
)

// The macOS guest notch is the macos-user backend (env-manager plan Phase 7.1, EMP-D1), so its
// briefing header states the guest notch AND the one fact of that backend an agent most needs:
// the account is shared by every workspace on the machine. The jail-without-a-container header
// has said so since 2026-09-04; reaching the same backend by the other notch must not drop it.
// A guest with a container mechanism (the Linux spelling, unbuilt) does not say it.
func TestTheMacosGuestHeaderStatesTheSharedAccount(t *testing.T) {
	const shared = "The account is shared by every workspace on this machine"
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
	if !strings.Contains(jailNative, "is shared by every workspace on this machine") {
		t.Errorf("the jail-notch macos-user header lost its shared-account sentence:\n%s", jailNative)
	}
	linuxGuest := strings.Join(confinementHeader("guest", "podman", false), "\n")
	if strings.Contains(linuxGuest, shared) {
		t.Errorf("a guest with no macos-user mechanism claims the macos-user account:\n%s", linuxGuest)
	}
}

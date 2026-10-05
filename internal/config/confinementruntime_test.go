package config

import (
	"strings"
	"testing"
)

// confinementruntime_test.go pins the one resolver every runtime reader shares since the guest
// notch launches on macOS (env-manager plan Phase 7.1, EMP-D1): YOLO_RUNTIME, then a known
// `runtime` key, then the notch's own backend, which is macos-user for a macOS guest and
// nothing anywhere else.

func TestNotchRuntimeIsTheMacosGuestsBackendAlone(t *testing.T) {
	cases := []struct {
		notch   Confinement
		isMacOS bool
		want    string
	}{
		{ConfinementGuest, true, "macos-user"},
		// Linux's guest backend (Phase 7.2) is unwritten, so the notch names none there.
		{ConfinementGuest, false, ""},
		// The jail notch leaves the mechanism to the platform probe on both platforms.
		{ConfinementJail, true, ""},
		{ConfinementJail, false, ""},
		// The host notch launches nothing.
		{ConfinementHost, true, ""},
	}
	for _, tc := range cases {
		if got := NotchRuntime(tc.notch, tc.isMacOS); got != tc.want {
			t.Errorf("NotchRuntime(%s, macOS=%v) = %q, want %q", tc.notch, tc.isMacOS, got, tc.want)
		}
	}
	if GuestRuntime != "macos-user" {
		t.Errorf("GuestRuntime = %q; the macOS guest is the macos-user backend", GuestRuntime)
	}
}

func TestSelectedRuntimePrecedence(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		cfg     string
		notch   Confinement
		isMacOS bool
		want    string
		src     RuntimeSource
	}{
		{"nothing named", "", `{}`, ConfinementJail, true, "", RuntimeUnselected},
		{"the macOS guest's own backend", "", `{}`, ConfinementGuest, true, "macos-user", RuntimeFromNotch},
		{"a Linux guest names none", "", `{}`, ConfinementGuest, false, "", RuntimeUnselected},
		{"the config key outranks the notch", "", `{"runtime": "podman"}`, ConfinementGuest, true, "podman", RuntimeFromConfig},
		{"YOLO_RUNTIME outranks the key", "container", `{"runtime": "podman"}`, ConfinementGuest, true, "container", RuntimeFromEnv},
		// An unknown spelling counts for nothing, as every reader has always applied it.
		{"an unknown env value falls through", "docker", `{}`, ConfinementGuest, true, "macos-user", RuntimeFromNotch},
		{"an unknown key falls through", "", `{"runtime": "docker"}`, ConfinementJail, false, "", RuntimeUnselected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, src := SelectedRuntime(tc.env, decode(t, tc.cfg), tc.notch, tc.isMacOS)
			if rt != tc.want || src != tc.src {
				t.Errorf("SelectedRuntime = (%q, %d), want (%q, %d)", rt, src, tc.want, tc.src)
			}
			if got := ConfiguredRuntime(decode(t, tc.cfg), tc.notch, tc.isMacOS); tc.src != RuntimeFromEnv && got != tc.want {
				t.Errorf("ConfiguredRuntime = %q, want %q (SelectedRuntime without the env)", got, tc.want)
			}
		})
	}
	// A nil config is no config, not a panic: run's resolveRuntime is called with nil.
	if rt, src := SelectedRuntime("", nil, ResolveConfinement(nil), true); rt != "" || src != RuntimeUnselected {
		t.Errorf("SelectedRuntime over a nil config = (%q, %d), want nothing selected", rt, src)
	}
}

func TestNotchRuntimeConflict(t *testing.T) {
	cases := []struct {
		name     string
		env      string
		cfg      string
		notch    Confinement
		isMacOS  bool
		conflict bool
		rt       string
		src      RuntimeSource
	}{
		{"a container key at a macOS guest", "", `{"runtime": "podman"}`, ConfinementGuest, true, true, "podman", RuntimeFromConfig},
		{"a container env at a macOS guest", "container", `{}`, ConfinementGuest, true, true, "container", RuntimeFromEnv},
		// Agreement is one launch said twice.
		{"macos-user named at a macOS guest", "", `{"runtime": "macos-user"}`, ConfinementGuest, true, false, "", RuntimeUnselected},
		// YOLO_RUNTIME outranks the key, so a podman key under a macos-user env agrees.
		{"the env overrides a contradicting key", "macos-user", `{"runtime": "podman"}`, ConfinementGuest, true, false, "", RuntimeUnselected},
		{"nothing explicit", "", `{}`, ConfinementGuest, true, false, "", RuntimeUnselected},
		// No backend selected by the notch, so nothing to contradict: the Linux guest is
		// refused for its own reason, and the jail notch takes any runtime.
		{"a Linux guest", "", `{"runtime": "podman"}`, ConfinementGuest, false, false, "", RuntimeUnselected},
		{"the jail notch", "", `{"runtime": "podman"}`, ConfinementJail, true, false, "", RuntimeUnselected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, src, conflict := NotchRuntimeConflict(tc.env, decode(t, tc.cfg), tc.notch, tc.isMacOS)
			if conflict != tc.conflict || rt != tc.rt || src != tc.src {
				t.Errorf("NotchRuntimeConflict = (%q, %d, %v), want (%q, %d, %v)",
					rt, src, conflict, tc.rt, tc.src, tc.conflict)
			}
		})
	}
}

// TestSessionNotchNamesTheGuestFromItsMarker: NotchEnv names the guest notch with "guest" and
// nothing else; every other value, the empty one included, is the jail notch YOLO_VERSION alone
// implies (EMP-D4).
func TestSessionNotchNamesTheGuestFromItsMarker(t *testing.T) {
	for value, want := range map[string]Confinement{
		"guest": ConfinementGuest,
		"":      ConfinementJail,
		"jail":  ConfinementJail,
		"host":  ConfinementJail, // a launched session is never at the host notch
		"Guest": ConfinementJail,
	} {
		if got := SessionNotch(value); got != want {
			t.Errorf("SessionNotch(%q) = %s, want %s", value, got, want)
		}
	}
	if NotchEnv != "YOLO_CONFINEMENT" {
		t.Errorf("NotchEnv = %q; the footer and the macos-user backend read YOLO_CONFINEMENT", NotchEnv)
	}
}

// TestContainerStepClauseNamesTheJailNotchAtAGuestAlone: a next step naming a container runtime
// gains the jail notch at a guest, where the runtime alone is refused as a contradiction, and
// gains nothing anywhere else (EMP-D5). Each spelling it names is one the notch gate passes,
// which TestMacosGuestContainerStepsPassTheNotchGate in internal/cli/run measures on Run().
func TestContainerStepClauseNamesTheJailNotchAtAGuestAlone(t *testing.T) {
	got := ContainerStepClause(ConfinementGuest)
	for _, want := range []string{"; ", "`--at jail`", "`confinement` set to \"jail\"", GuestRuntime} {
		if !strings.Contains(got, want) {
			t.Errorf("the guest clause does not say %q: %q", want, got)
		}
	}
	for _, n := range []Confinement{ConfinementJail, ConfinementHost, ""} {
		if c := ContainerStepClause(n); c != "" {
			t.Errorf("ContainerStepClause(%q) = %q, want none: only a guest contradicts a "+
				"container runtime", n, c)
		}
	}
}

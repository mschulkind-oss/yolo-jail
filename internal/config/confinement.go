package config

// confinement.go is what is left of the retired `confinement` dial (the notch: jail, guest,
// host) once named setups replaced it (docs/design/named-setups.md; named-setups-plan.md slice 5).
// The config key itself is refused (setupsretired.go); a setup's notch is setup.Setup.Kind. What
// stays here is the notch VOCABULARY its last readers still speak, each of which slice 9 of that
// plan moves to the setup name: the briefing's Confinement input (internal/cli/run's notchConfig
// writes the setup's notch into a copy of the config, which ResolveConfinement reads back), the
// agent footer's session marker (NotchEnv), and the `--at` refusal's names.

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// Confinement is the resolved notch. The three values are presets over a composable
// primitive model (see internal/render/confinement.go) — a user selects a notch, not a
// hand-assembled policy vector (fill-the-matrix-principle.md).
type Confinement string

const (
	// ConfinementJail is the strongest notch and the default: a container (podman /
	// Apple Container), disposable home, none of the user's credentials.
	ConfinementJail Confinement = "jail"
	// ConfinementGuest is the middle notch: a real home on the real filesystem, no
	// image, an LSM boundary (Seatbelt on macOS, bwrap+Landlock on Linux). On macOS it is
	// the macos-user backend, a separate account under Seatbelt (GuestRuntime); on Linux
	// it has no backend yet, and a launch refuses it.
	ConfinementGuest Confinement = "guest"
	// ConfinementHost is the weakest notch: you, your machine, your dotfiles, your
	// credentials. `yolo host apply` renders into it. Never inferred, never a fallback.
	ConfinementHost Confinement = "host"
)

// KnownConfinements is the closed set, in dial order (strongest first).
var KnownConfinements = []Confinement{ConfinementJail, ConfinementGuest, ConfinementHost}

// ResolveConfinement reads the notch from a merged config's `confinement`, defaulting to jail.
// On the host the key is refused, so only internal/cli/run's notchConfig copy (the setup's
// notch, for the briefing) carries one; an unknown value is jail, so a resolver never fails.
func ResolveConfinement(config *jsonx.OrderedMap) Confinement {
	if config == nil {
		return ConfinementJail
	}
	v, ok := config.Get("confinement")
	if !ok || v == nil {
		return ConfinementJail
	}
	s, ok := v.(string)
	if !ok {
		return ConfinementJail
	}
	for _, c := range KnownConfinements {
		if Confinement(s) == c {
			return c
		}
	}
	return ConfinementJail
}

// GuestRuntime is the runtime the guest notch runs on where it is built: macos-user, a
// dedicated macOS account under Seatbelt with no VM (env-manager plan Phase 7.1). It is the
// guest notch's only backend; Linux's (Phase 7.2) is unwritten.
const GuestRuntime = "macos-user"

// NotchEnv is the variable a launch sets in the session it starts when YOLO_VERSION alone would
// name the wrong notch: "guest", on a macOS guest launch (the macos-user arm of internal/cli/run;
// env-manager plan EMP-D4). YOLO_VERSION says the process is in a launched session (InJail), and
// that alone reads as the jail notch; this names the guest. Its one reader is the agent footer,
// which prints the notch (docs/reference/agent-footer.md, "The notch").
//
// Unset, or any value but "guest", is the jail notch. A launch at the jail notch sets nothing.
const NotchEnv = "YOLO_CONFINEMENT"

// SessionNotch is the notch a launched session's NotchEnv value names: the guest notch for
// "guest", the jail notch for anything else. The caller has already established that the
// process is in a launched session; outside one there is no notch to read here.
func SessionNotch(value string) Confinement {
	if Confinement(value) == ConfinementGuest {
		return ConfinementGuest
	}
	return ConfinementJail
}

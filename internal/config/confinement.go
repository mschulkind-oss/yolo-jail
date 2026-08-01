package config

// confinement.go is the `confinement` config key — the dial that names how confined an
// agent's environment is, independent of the MECHANISM that enforces it (env-manager
// design §4). It splits the conflation `runtime` carries today, where "macos-user" means
// both "a weaker isolation level" and "by what backend."
//
//	confinement: jail | guest | host   (default: jail)
//	runtime:     podman | container | auto   — a mechanism hint INSIDE jail
//
// This phase (env-manager plan Phase 2) lands the key, its validation, and the resolver.
// It is intentionally behavior-neutral for the default: an absent key, or "jail", is
// exactly today's behavior. The guest/host notches are wired by later phases (host
// render, the guest backend); here they are accepted and resolvable so nothing downstream
// has to string-guess.

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// Confinement is the resolved notch. The three values are presets over a composable
// primitive model (see internal/render/confinement.go) — a user selects a notch, not a
// hand-assembled policy vector (happy-path-principle.md).
type Confinement string

const (
	// ConfinementJail is the strongest notch and the default: a container (podman /
	// Apple Container), disposable home, none of the user's credentials.
	ConfinementJail Confinement = "jail"
	// ConfinementGuest is the middle notch: a real home on the real filesystem, no
	// image, an LSM boundary (Seatbelt on macOS, bwrap+Landlock on Linux), its own
	// separate identity. Not yet enforced — later-phase work.
	ConfinementGuest Confinement = "guest"
	// ConfinementHost is the weakest notch: you, your machine, your dotfiles, your
	// credentials. `apply --host` renders into it. Never inferred, never a fallback.
	ConfinementHost Confinement = "host"
)

// KnownConfinements is the closed set, in dial order (strongest first).
var KnownConfinements = []Confinement{ConfinementJail, ConfinementGuest, ConfinementHost}

// ResolveConfinement reads the confinement notch from a merged config, defaulting to
// jail. An unknown value is treated as jail here (validateConfinement is what reports
// it as an error at check time) so a resolver never has to fail — the same shape
// ResolveRuntime and friends use.
func ResolveConfinement(config *jsonx.OrderedMap) Confinement {
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

// validateConfinement reports an unknown confinement value, and — the load-bearing
// safety rule (env-manager §7) — that `host` is EXPLICIT-only: it is a real-machine
// posture and must never be reachable by a typo that a lenient resolver would swallow.
func validateConfinement(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get("confinement")
	if !present || v == nil {
		return
	}
	s, ok := v.(string)
	if !ok {
		add(errs, "config.confinement: expected a string ('jail', 'guest', or 'host')")
		return
	}
	for _, c := range KnownConfinements {
		if Confinement(s) == c {
			return
		}
	}
	add(errs, "config.confinement: expected 'jail', 'guest', or 'host'")
}

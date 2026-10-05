package config

// confinement.go is the `confinement` config key — the dial that names how confined an
// agent's environment is, independent of the MECHANISM that enforces it (env-manager
// design §4). It splits the conflation `runtime` carries today, where "macos-user" means
// both "a weaker isolation level" and "by what backend."
//
//	confinement: jail | guest | host   (default: jail)
//	runtime:     podman | container | macos-user   — the mechanism; none is the platform probe
//
// Phase 2 of the env-manager plan landed the key, its validation, and the resolver,
// behavior-neutral for the default: an absent key, or "jail", is exactly what a launch did
// before the key existed. The host notch has its own verbs (`yolo host`, `yolo apply --at
// host`). The guest notch launches on macOS, where its backend is macos-user (plan Phase
// 7.1, decision EMP-D1 in docs/plans/environment-manager-plan.md), and nowhere else yet:
// Linux's bwrap + Landlock backend (7.2) is unwritten, and a Linux launch refuses it.

import (
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

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
	// credentials. `yolo apply --at host` renders into it. Never inferred, never a fallback.
	ConfinementHost Confinement = "host"
)

// KnownConfinements is the closed set, in dial order (strongest first).
var KnownConfinements = []Confinement{ConfinementJail, ConfinementGuest, ConfinementHost}

// ResolveConfinement reads the confinement notch from a merged config, defaulting to
// jail. An unknown value is treated as jail here (validateConfinement is what reports
// it as an error at check time) so a resolver never has to fail — the same shape
// ResolveRuntime and friends use. A nil config is the default too.
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

// GuestRuntime is the runtime the guest notch runs on where it is built: macos-user, a
// dedicated macOS account under Seatbelt with no VM (env-manager plan Phase 7.1). It is the
// guest notch's only backend; Linux's (Phase 7.2) is unwritten.
const GuestRuntime = "macos-user"

// NotchRuntime is the runtime a notch selects by itself on this platform, with no `runtime`
// key and no YOLO_RUNTIME: GuestRuntime for the guest notch on macOS, and "" otherwise.
// The jail notch leaves the mechanism to the platform probe, the host notch launches
// nothing, and a Linux guest has no backend to name.
//
// THE NOTCH IS THE CALLER'S. A launch's notch is the config's `confinement` key overridden by
// `--at`, which only the launch knows; every other reader passes ResolveConfinement(cfg).
func NotchRuntime(notch Confinement, isMacOS bool) string {
	if notch == ConfinementGuest && isMacOS {
		return GuestRuntime
	}
	return ""
}

// RuntimeSource names the input that selected a runtime, for a message that has to tell the
// reader which one to change.
type RuntimeSource int

const (
	// RuntimeUnselected: nothing names a runtime, and the caller probes the platform.
	RuntimeUnselected RuntimeSource = iota
	// RuntimeFromEnv: YOLO_RUNTIME named it.
	RuntimeFromEnv
	// RuntimeFromConfig: the config's `runtime` key named it.
	RuntimeFromConfig
	// RuntimeFromNotch: the notch selected its own backend (NotchRuntime).
	RuntimeFromNotch
)

// RuntimeKey is the config's `runtime` value as a string, "" when absent or not a string.
func RuntimeKey(cfg *jsonx.OrderedMap) string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg.Get("runtime")
	s, _ := v.(string)
	return s
}

// SelectedRuntime is the runtime a launch's own inputs select before any platform probe, and
// the input that selected it, in the precedence every runtime reader shares: YOLO_RUNTIME,
// then the config's `runtime` key, each counted only when it names a runtime yolo knows
// (paths.AllRuntimes), then the notch's own backend (NotchRuntime). ("", RuntimeUnselected)
// means nothing selects one.
//
// The explicit inputs outrank the notch so that this is the one place the two are weighed;
// whether they CONTRADICT is NotchRuntimeConflict's question, which a launch refuses on.
func SelectedRuntime(envRuntime string, cfg *jsonx.OrderedMap, notch Confinement, isMacOS bool) (string, RuntimeSource) {
	if rt, src := explicitRuntime(envRuntime, cfg); rt != "" {
		return rt, src
	}
	if rt := NotchRuntime(notch, isMacOS); rt != "" {
		return rt, RuntimeFromNotch
	}
	return "", RuntimeUnselected
}

// ConfiguredRuntime is SelectedRuntime without YOLO_RUNTIME, for the readers that weigh the
// environment variable themselves (runtime.ResolveRuntime's callers): a known `runtime` key,
// else the notch's own backend, else "".
func ConfiguredRuntime(cfg *jsonx.OrderedMap, notch Confinement, isMacOS bool) string {
	rt, _ := SelectedRuntime("", cfg, notch, isMacOS)
	return rt
}

// NotchRuntimeConflict reports an explicit runtime that contradicts the notch's own backend:
// on macOS `confinement: guest` (or `--at guest`) runs on macos-user, so a YOLO_RUNTIME or
// `runtime` key naming podman or Apple Container asks for two different launches at once. It
// returns the explicit runtime and the input that named it; conflict is false when the notch
// selects no backend, nothing explicit is named, or the two agree (`runtime: "macos-user"`
// with `confinement: "guest"` is one launch said twice).
func NotchRuntimeConflict(envRuntime string, cfg *jsonx.OrderedMap, notch Confinement, isMacOS bool) (rt string, src RuntimeSource, conflict bool) {
	want := NotchRuntime(notch, isMacOS)
	if want == "" {
		return "", RuntimeUnselected, false
	}
	rt, src = explicitRuntime(envRuntime, cfg)
	if rt == "" || rt == want {
		return "", RuntimeUnselected, false
	}
	return rt, src, true
}

// explicitRuntime is the runtime YOLO_RUNTIME or the `runtime` key names, in that order, each
// only when it is one yolo knows.
func explicitRuntime(envRuntime string, cfg *jsonx.OrderedMap) (string, RuntimeSource) {
	if envRuntime != "" && slices.Contains(paths.AllRuntimes, envRuntime) {
		return envRuntime, RuntimeFromEnv
	}
	if rt := RuntimeKey(cfg); rt != "" && slices.Contains(paths.AllRuntimes, rt) {
		return rt, RuntimeFromConfig
	}
	return "", RuntimeUnselected
}

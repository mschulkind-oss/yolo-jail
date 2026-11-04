// Package setup is the named-setup model (docs/design/named-setups.md): where an agent runs is
// chosen by naming a setup, a set of values for two independent fields, `confinement` (the
// technology that draws the walls) and `run_as` (the account the agent runs as).
//
// The package is stdlib-only by design (named-setups-plan.md SUP-D1): internal/config imports
// it to validate `[setups.*]`, and internal/render imports it for KindForSetup, so it may import
// neither. It bridges to the three values the launch pipeline consumes today — the backend
// string, the render notch and the macos-user mode — through Backend, Kind and the RunAs field,
// all derived from one field each, never from another field's value (design §4: "No code path
// reads one setup field to decide another's meaning or default").
package setup

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Confinement is the technology that draws the walls (design §4).
type Confinement string

// RunAs is the account the agent runs as (design §4).
type RunAs string

const (
	ConfinementPodman         Confinement = "podman"
	ConfinementAppleContainer Confinement = "apple-container"
	ConfinementSeatbelt       Confinement = "seatbelt"
	ConfinementLandlock       Confinement = "landlock"
	ConfinementHost           Confinement = "host"

	RunAsSelf        RunAs = "self"
	RunAsYoloAccount RunAs = "yolo-account"
)

// Each field's own default (design §4, OQ-SU1): the same whatever the other field says.
const (
	DefaultConfinement = ConfinementPodman
	DefaultRunAs       = RunAsSelf
)

// Confinements and RunAsValues are every value of each field, in the design's table order;
// refusals list them in this order.
func Confinements() []Confinement {
	return []Confinement{ConfinementPodman, ConfinementAppleContainer, ConfinementSeatbelt, ConfinementLandlock, ConfinementHost}
}

func RunAsValues() []RunAs { return []RunAs{RunAsSelf, RunAsYoloAccount} }

// Setup is one named setup, built-in or the user's.
type Setup struct {
	Name        string
	Confinement Confinement
	RunAs       RunAs
	BuiltIn     bool
}

// Kind is the setup's point on the render pipeline's three-value dial; internal/render maps
// it to render.Kind (KindForSetup). It is this package's own enum so the package imports
// nothing of render's.
type Kind int

const (
	KindUnset Kind = iota
	KindJail
	KindGuest
	KindHost
)

func (k Kind) String() string {
	switch k {
	case KindJail:
		return "jail"
	case KindGuest:
		return "guest"
	case KindHost:
		return "host"
	}
	return "unset"
}

// Kind derives the render notch from confinement alone. A container technology is the jail
// notch; a rule list over the real machine (Seatbelt, and Landlock when built) is the guest
// notch, as macos-user is today; host is host.
func (s Setup) Kind() Kind {
	switch s.Confinement {
	case ConfinementPodman, ConfinementAppleContainer:
		return KindJail
	case ConfinementSeatbelt, ConfinementLandlock:
		return KindGuest
	case ConfinementHost:
		return KindHost
	}
	return KindUnset
}

// Backend derives today's backend string (paths.AllRuntimes) from confinement alone: "" for
// host, which runs no backend, and for landlock, which has none built.
func (s Setup) Backend() string {
	switch s.Confinement {
	case ConfinementPodman:
		return "podman"
	case ConfinementAppleContainer:
		return "container"
	case ConfinementSeatbelt:
		return "macos-user"
	}
	return ""
}

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidName reports whether name is a setup name: lowercase letters, digits and inner
// hyphens, at most 32 characters (SUP-D3). The name joins a container name, a keeper key
// whose fields '.' separates, and a macos-user session key.
func ValidName(name string) bool { return nameRE.MatchString(name) }

func nameRefusal(name string) string {
	return fmt.Sprintf("%q is not a setup name: a setup name is lowercase letters, digits and inner hyphens, at most 32 characters", name)
}

// Decode reads one user setup, a `[setups.<name>]` table, from its fields. A field it does
// not write takes that field's own default. It refuses a malformed name, a built-in's name,
// an unknown field and an unknown value, each with its next step.
func Decode(name string, fields map[string]string) (Setup, error) {
	if !ValidName(name) {
		return Setup{}, fmt.Errorf("setups.%s: %s. Rename the table in your user config, for example:\n    [setups.%s]", name, nameRefusal(name), SuggestName(name))
	}
	if IsBuiltIn(name) {
		return Setup{}, fmt.Errorf("[setups.%s] redefines a built-in setup. Give yours its own name:\n    yolo setup new my-%s --from %s", name, string(MustBuiltIn(name).Confinement), name)
	}
	s := Setup{Name: name, Confinement: DefaultConfinement, RunAs: DefaultRunAs}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := fields[k]
		switch k {
		case "confinement":
			c := Confinement(v)
			if !knownConfinement(c) {
				return Setup{}, fmt.Errorf("setups.%s: confinement = %q is not a confinement. The values: %s. Set one:\n    yolo config set setups.%s.confinement %s --user",
					name, v, joinConfinements(), name, DefaultConfinement)
			}
			s.Confinement = c
		case "run_as":
			r := RunAs(v)
			if !knownRunAs(r) {
				return Setup{}, fmt.Errorf("setups.%s: run_as = %q is not a run_as. The values: %s. Set one:\n    yolo config set setups.%s.run_as %s --user",
					name, v, joinRunAs(), name, DefaultRunAs)
			}
			s.RunAs = r
		case "policy":
			// Design §6, verbatim: someone reaching for a looser rule list is pointed at the
			// one looser setup there is.
			return Setup{}, fmt.Errorf("setups.%s: `policy` is not a setup field; Seatbelt has one rule list. Remove it:\n    yolo config unset setups.%s.policy --user\nFor fewer refusals, run as the yolo account with no Seatbelt:\n    yolo host --setup host-yolo-account -- claude", name, name)
		default:
			return Setup{}, fmt.Errorf("setups.%s: `%s` is not a setup field; a setup has `confinement` and `run_as`. Remove it:\n    yolo config unset setups.%s.%s --user", name, k, name, k)
		}
	}
	return s, nil
}

// SuggestName turns a malformed name into the nearest one ValidName accepts, for a refusal's
// next step: lowercased, every other character a hyphen, runs and ends trimmed, cut to 32.
func SuggestName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if len(out) > 32 {
		out = strings.TrimRight(out[:32], "-")
	}
	if out == "" {
		return "my-setup"
	}
	return out
}

func knownConfinement(c Confinement) bool {
	for _, k := range Confinements() {
		if k == c {
			return true
		}
	}
	return false
}

func knownRunAs(r RunAs) bool {
	for _, k := range RunAsValues() {
		if k == r {
			return true
		}
	}
	return false
}

func joinConfinements() string {
	var out []string
	for _, c := range Confinements() {
		out = append(out, string(c))
	}
	return strings.Join(out, ", ")
}

func joinRunAs() string {
	var out []string
	for _, r := range RunAsValues() {
		out = append(out, string(r))
	}
	return strings.Join(out, ", ")
}

// LaunchDescription is the setup's fields for a launch disclosure, with the network the launch
// actually applied. A Podman launch reports its resolved bridge/shared choice rather than the
// setup's general rule; Apple Container's invariant network is simply "bridged".
func LaunchDescription(s Setup, network string) string {
	description := Describe(s)
	switch s.Confinement {
	case ConfinementPodman:
		description = strings.TrimSuffix(description, "; bridged unless network.mode is host")
		if network == "host" {
			description += "; shares the host's network"
		} else {
			description += "; bridged"
		}
	case ConfinementAppleContainer:
		description = strings.TrimSuffix(description, "; always bridged") + "; bridged"
	}
	return description
}

// LaunchDisclosure is the setup portion of the launch line, shared by container and native
// launch paths so both name the same selected setup, fields, applied network and source.
func LaunchDisclosure(s Setup, network string, source Source) string {
	return "yolo: setup " + s.Name + " (" + LaunchDescription(s, network) + ") - from " + source.LaunchLabel()
}

func technologyName(c Confinement) string {
	switch c {
	case ConfinementPodman:
		return "Podman"
	case ConfinementAppleContainer:
		return "Apple Container"
	case ConfinementSeatbelt:
		return "Seatbelt"
	case ConfinementLandlock:
		return "Landlock"
	case ConfinementHost:
		return "no walls"
	}
	return string(c)
}

// Describe spells a setup's fields for the launch line, the footer and the briefing:
// one phrase per confinement, one per run_as, then the technology's network (design §2:
// "Seatbelt, deny-by-default, as you; shares the Mac's network"). Landlock's network is its
// own design's, so it has no network phrase.
func Describe(s Setup) string {
	conf := technologyName(s.Confinement)
	switch s.Confinement {
	case ConfinementSeatbelt:
		conf = "Seatbelt, deny-by-default"
	case ConfinementLandlock:
		conf = "Landlock (not built)"
	}
	who := "as you"
	if s.RunAs == RunAsYoloAccount {
		who = "as the yolo account"
	}
	out := conf + ", " + who
	if net := networkPhrase(s.Confinement); net != "" {
		out += "; " + net
	}
	return out
}

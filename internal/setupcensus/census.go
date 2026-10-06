// Package setupcensus is the per-setup census: one table, in code, giving every live
// top-level config key and every pack contribution kind a disposition and a reason on each
// of the four setups a jail can run on (docs/design/backend-parity.md §4, built by the
// OQ-BP-1 ruling of 2026-10-05).
//
// THE WORDS. A CENSUS is backend-parity.md §4's term for this table. A SETUP is a backend
// paired with the host OS it runs on — the four columns of
// userguide/reference/settings-per-setup.md, which is where the word comes from. A
// DISPOSITION is one of backend-parity.md §3's six states (Honored, HonoredBy, Warned,
// Dropped, Refused, NotApplicable). An ASPECT is a term coined for this package: a
// sub-mechanism of one key or kind whose disposition differs from its parent's on at least
// one setup — `resources.pids_limit` on Apple Container is the worked example, and §4's
// residue 2 ("the vocabulary is per-key; these are per-sub-key") is why the shape needs it.
//
// THE SHAPE IS render.hostConfigKeys' (internal/render/configkeys.go), which answers the
// same question for the host notch alone: one entry per key, each with a disposition AND a
// reason, exhaustive over the schema by test, so a key added to the config fails the build
// until it is classified here. It is a SIBLING rather than an extension, because
// render.FieldSet is platform-blind on purpose (internal/render/confinement.go) and a setup
// is a platform. The two together answer every notch: this table for a jail on each of four
// setups, render's for `yolo host`.
//
// THE ENUMERATION IS NEVER A HAND LIST. The tests read config.TopLevelConfigKeys() and
// packdecl.KnownKinds(), the schema's and the manifest decoder's own sets, so the next key
// or kind is unclassified until someone decides it here (census_test.go). The user guide's
// page is checked against this table (guide_test.go), so a cell the code and the guide
// disagree on fails a test instead of waiting to be read.
//
// THE REASON IS FOR A MAINTAINER, NOT A USER. It names the code path that produces the
// disposition, so the next reader can ask whether that path still does — the §3 warning that
// a HonoredBy without its mechanism is how #39 hid. No terminal view prints it; the guide
// says the same thing in the user's words. Where the code could not settle a cell, the
// reason says "unverified" and says what would settle it, rather than guessing.
//
// THE NOTICE IS FOR THE USER, AND THE LAUNCH READS IT. A Warned cell asserts a launch line, and
// on macos-user, and for Apple Container's cache_relocations, the cell also holds that line
// (Notice): the printers that say it read their words from here at run time (Warning), so the
// table decides what a launch prints, the ruling's "the macos-user notice block reads it"
// (OQ-BP-1). The reason stays a maintainer's: the notice is a separate field because the two
// are written for different readers, which is why render's host census prints no reason
// either. A Notice names its printer (By), and each printing package's tests drive every
// notice it owns through its call site.
//
// WHAT IT CANNOT CATCH is §4's list, unchanged: a mechanism that emits an argv the backend
// then fails to run is a cell that reads Honored and is wrong. internal/cli/run's
// backendparity_test.go stays beside this table for the code sites the table cannot see.
package setupcensus

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Setup is one of the four ways a jail runs: a backend on a host OS.
type Setup int

const (
	// PodmanLinux is podman on a Linux host.
	PodmanLinux Setup = iota
	// PodmanMac is podman on macOS, inside the Podman Machine's Linux VM.
	PodmanMac
	// AppleContainer is Apple Container (`runtime: "container"`), macOS only.
	AppleContainer
	// MacosUser is the macos-user backend: a Seatbelt sandbox on the Mac, no container.
	MacosUser
)

// Setups returns the four setups in the guide's column order.
func Setups() []Setup {
	return []Setup{PodmanLinux, PodmanMac, AppleContainer, MacosUser}
}

// String names the setup as the guide's column header does, without the backticks.
func (s Setup) String() string {
	switch s {
	case PodmanLinux:
		return "podman/Linux"
	case PodmanMac:
		return "podman/macOS"
	case AppleContainer:
		return "container/macOS"
	case MacosUser:
		return "macos-user/macOS"
	}
	return "unknown setup"
}

// Disposition is what a setup does with a key or kind: backend-parity.md §3's six states,
// plus the zero value, which no entry may hold.
type Disposition int

const (
	// Unclassified is the zero value: nobody has decided this cell.
	Unclassified Disposition = iota
	// Honored: it works, by the same mechanism as podman on Linux.
	Honored
	// HonoredBy: it works, by a different mechanism, which the reason must name.
	HonoredBy
	// Warned: it is absent, and the launch says so.
	Warned
	// Dropped: it is absent, silently. §3 says "deliberately so"; where nobody ruled the
	// silence, the reason says so, because the census records what the code does today.
	Dropped
	// Refused: the launch stops rather than proceed without it.
	Refused
	// NotApplicable: the question does not arise on this setup.
	NotApplicable
)

// Dispositions returns the six states in §3's order. internal/cli/run's annotation census
// (backendparity_test.go) reads its vocabulary from here, so the two censuses cannot drift
// apart on what the words are.
func Dispositions() []Disposition {
	return []Disposition{Honored, HonoredBy, Warned, Dropped, Refused, NotApplicable}
}

// String is the disposition's §3 spelling, the word a `// parity:` marker carries.
func (d Disposition) String() string {
	switch d {
	case Honored:
		return "Honored"
	case HonoredBy:
		return "HonoredBy"
	case Warned:
		return "Warned"
	case Dropped:
		return "Dropped"
	case Refused:
		return "Refused"
	case NotApplicable:
		return "NotApplicable"
	}
	return "Unclassified"
}

// Works reports whether the setup delivers what was declared, by either mechanism.
func (d Disposition) Works() bool { return d == Honored || d == HonoredBy }

// Cell is one setup's answer for one key or kind.
type Cell struct {
	Disposition Disposition
	// Reason names the code path that produces the disposition, in one or two sentences.
	// Required for every cell, Honored ones included: a classification with no stated reason
	// cannot be re-decided when its code moves (internal/config/inherit.go's argument).
	Reason string
	// Notice is the line a Warned cell's launch prints, for a printer that reads it from here.
	// The zero Notice is none.
	Notice Notice
}

// Notice is a Warned cell's launch line in the words the launch prints: a headline, the
// entries the config declared (supplied by the printer, since they are the user's), then the
// rest. Written for the user, as Reason is for a maintainer.
type Notice struct {
	// Says is the headline, printed as the warning: "`devices` is not read on macos-user".
	Says string
	// Then is the rest of the line, after the entries when the printer names any.
	Then string
	// By is the printer that reads this notice, as package.Identifier: a function, or a
	// darwin boot step's name (`entrypoint.mcp_presets_declined`). The package's own tests
	// drive every notice it owns through that printer's call site.
	By string
}

// Line renders the notice for the entries a config declared ("" when the line names none), in
// the launch printers' rich markup (internal/richtext): the headline as a yellow warning, then
// the entries, then the rest.
func (n Notice) Line(entries string) string {
	return "[yellow]Warning: " + n.Says + "[/yellow]" + n.tail(entries)
}

// Plain is Line without markup, for a printer that writes raw text (the darwin bootstrap's).
func (n Notice) Plain(entries string) string {
	return "Warning: " + n.Says + n.tail(entries)
}

func (n Notice) tail(entries string) string {
	var parts []string
	if entries != "" {
		parts = append(parts, entries+".")
	}
	if n.Then != "" {
		parts = append(parts, n.Then)
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, " ")
}

// Warning is the notice a setup's Warned cell holds for a census path, the one call every
// printer that reads the census makes. A path or cell with no notice still yields a true line,
// naming the path and the setup, so a table edit cannot silence a printer at run time; the
// tests fail on it first (census_test.go's notice tests, and each printer's own).
func Warning(s Setup, path string) Notice {
	if e, ok := Find(path); ok {
		if n := e.Cell(s).Notice; n.Says != "" {
			return n
		}
	}
	return Notice{Says: "`" + path + "` is not honored on " + s.String()}
}

// Entry is one key's or kind's four cells.
type Entry struct {
	PodmanLinux, PodmanMac, AppleContainer, MacosUser Cell

	// Guide names the rows of userguide/reference/settings-per-setup.md this entry is checked
	// against: each is the start of one row's first cell, footnote references removed. Every
	// row whose first cell starts with a key or kind name is claimed by some entry, and a key
	// or kind no row names is a finding (guide_test.go), except the kinds the guide's
	// "delivered on all four setups" sentence covers, which must then be honored everywhere.
	Guide []string

	// Aspects are the sub-mechanisms whose disposition differs from this entry's on at least
	// one setup, keyed by a short name (`pids_limit`). A sub-mechanism that agrees with its
	// parent everywhere is not an aspect; the parent states it.
	Aspects map[string]Entry
}

// Cell returns the entry's cell for a setup.
func (e Entry) Cell(s Setup) Cell {
	switch s {
	case PodmanLinux:
		return e.PodmanLinux
	case PodmanMac:
		return e.PodmanMac
	case AppleContainer:
		return e.AppleContainer
	case MacosUser:
		return e.MacosUser
	}
	return Cell{}
}

// ConfigKey returns the census entry for a live top-level config key.
func ConfigKey(key string) (Entry, bool) {
	e, ok := configKeys[key]
	return e, ok
}

// Kind returns the census entry for a pack contribution kind.
func Kind(k packdecl.Kind) (Entry, bool) {
	e, ok := kinds[k]
	return e, ok
}

// ConfigKeys returns every key the census classifies, sorted.
func ConfigKeys() []string {
	out := make([]string, 0, len(configKeys))
	for k := range configKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Kinds returns every kind the census classifies, sorted.
func Kinds() []packdecl.Kind {
	out := make([]packdecl.Kind, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// KindPathPrefix marks a census path that names a pack kind rather than a config key, since a
// key and a kind can share a name (`profile` is both).
const KindPathPrefix = "kind:"

// Find returns the entry a census path names: a config key (`resources`), one of its aspects
// (`resources.pids_limit`), or either for a kind behind KindPathPrefix (`kind:program.patches`).
func Find(path string) (Entry, bool) {
	name, aspect, hasAspect := strings.Cut(path, ".")
	var e Entry
	var ok bool
	if k, isKind := strings.CutPrefix(name, KindPathPrefix); isKind {
		e, ok = Kind(packdecl.Kind(k))
	} else {
		e, ok = ConfigKey(name)
	}
	if !ok || !hasAspect {
		return e, ok
	}
	a, ok := e.Aspects[aspect]
	return a, ok
}

// Paths returns every path Find answers, entries and aspects, sorted: the keys first, then the
// kinds behind KindPathPrefix.
func Paths() []string {
	var out []string
	add := func(path string, e Entry) {
		out = append(out, path)
		names := make([]string, 0, len(e.Aspects))
		for n := range e.Aspects {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, path+"."+n)
		}
	}
	for _, k := range ConfigKeys() {
		add(k, configKeys[k])
	}
	for _, k := range Kinds() {
		add(KindPathPrefix+string(k), kinds[k])
	}
	return out
}

// The constructors keep the tables readable: one call per cell, the disposition first.

func honored(reason string) Cell   { return Cell{Disposition: Honored, Reason: reason} }
func honoredBy(reason string) Cell { return Cell{Disposition: HonoredBy, Reason: reason} }
func warned(reason string) Cell    { return Cell{Disposition: Warned, Reason: reason} }
func dropped(reason string) Cell   { return Cell{Disposition: Dropped, Reason: reason} }
func refused(reason string) Cell   { return Cell{Disposition: Refused, Reason: reason} }
func notApplicable(reason string) Cell {
	return Cell{Disposition: NotApplicable, Reason: reason}
}

// warnedSaying is a Warned cell whose launch line its printer reads from here.
func warnedSaying(reason string, n Notice) Cell {
	return Cell{Disposition: Warned, Reason: reason, Notice: n}
}

// everywhere is an entry whose four cells are one cell: a key read before the backend
// dispatch, or by a host-side command, where the setup is not the axis.
func everywhere(c Cell, guide ...string) Entry {
	return Entry{PodmanLinux: c, PodmanMac: c, AppleContainer: c, MacosUser: c, Guide: guide}
}

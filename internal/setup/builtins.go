package setup

import "fmt"

// builtIns is design §5's table, in its order: one built-in per combination of the two
// fields, named <confinement>-<run_as>. A built-in that is not built is still a name;
// Buildable refuses it naming the nearest built one.
var builtIns = func() []Setup {
	var out []Setup
	for _, row := range []struct {
		c Confinement
		r RunAs
	}{
		{ConfinementPodman, RunAsSelf},
		{ConfinementAppleContainer, RunAsSelf},
		{ConfinementSeatbelt, RunAsSelf},
		{ConfinementSeatbelt, RunAsYoloAccount},
		{ConfinementLandlock, RunAsSelf},
		{ConfinementLandlock, RunAsYoloAccount},
		{ConfinementHost, RunAsSelf},
		{ConfinementHost, RunAsYoloAccount},
	} {
		out = append(out, Setup{Name: string(row.c) + "-" + string(row.r), Confinement: row.c, RunAs: row.r, BuiltIn: true})
	}
	return out
}()

// BuiltIns returns a copy of the built-in setups, in design §5's order.
func BuiltIns() []Setup { return append([]Setup(nil), builtIns...) }

// IsBuiltIn reports whether name is a built-in setup's name.
func IsBuiltIn(name string) bool {
	_, ok := builtIn(name)
	return ok
}

func builtIn(name string) (Setup, bool) {
	for _, s := range builtIns {
		if s.Name == name {
			return s, true
		}
	}
	return Setup{}, false
}

// MustBuiltIn returns the named built-in and panics on any other name: for callers naming a
// built-in in code, where an unknown name is a programming error.
func MustBuiltIn(name string) Setup {
	s, ok := builtIn(name)
	if !ok {
		panic("setup: no built-in setup named " + name)
	}
	return s
}

// Default is the OS's one named default setup (design §2, step 4): podman-self on Linux,
// apple-container-self on macOS. It is chosen by name, never by probing; whether its
// technology is installed is Buildable's question. The Mac default moves to seatbelt-self
// only when the owner rules (OQ-SU4), not here.
func Default(goos string) Setup {
	if goos == "darwin" {
		return MustBuiltIn("apple-container-self")
	}
	return MustBuiltIn("podman-self")
}

// LaunchLine is the one command that runs s, the single spelling every refusal and listing
// names: `yolo host -- claude` for host-self, `yolo host --setup <name> -- claude` for any other
// host setup (only that verb runs one, design §8), and `yolo --setup <name> -- claude` for every
// setup with walls.
func LaunchLine(s Setup) string {
	switch {
	case s.Name == "host-self":
		return "yolo host -- claude"
	case s.Kind() == KindHost:
		return "yolo host --setup " + s.Name + " -- claude"
	}
	return "yolo --setup " + s.Name + " -- claude"
}

// launchLine is LaunchLine for a built-in named in code.
func launchLine(name string) string { return LaunchLine(MustBuiltIn(name)) }

// HostRefusal is design §6's refusal of a host setup reached outside `yolo host`: chosen by
// config, by YOLO_SETUP for a launch, or named to a launch's --setup (§8 item 3).
func HostRefusal(s Setup) string {
	return fmt.Sprintf("setup %q puts no walls around the agent, and only `yolo host` runs one:\n    %s", s.Name, LaunchLine(s))
}

func osName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	}
	return goos
}

// install is where each technology yolo checks for comes from, and the binary it checks.
var install = map[Confinement]struct{ binary, url string }{
	ConfinementPodman:         {"podman", "https://podman.io/docs/installation"},
	ConfinementAppleContainer: {"container", "https://github.com/apple/container"},
}

// MissingTechnologyError is Buildable's refusal for a setup whose technology is not installed
// here. Error is the refusal for a setup the user named; AsDefault is design §6's sentence
// for the OS default, which the user did not name.
type MissingTechnologyError struct {
	Setup      Setup
	GOOS       string
	Technology string // "Podman", "Apple Container"
	Binary     string // the binary looked for
	Install    string // where to install it from
	// Alternative is the first built-in, in design §5's order, that is built and installed
	// here, runs as you, and has walls; "" when there is none. Never a host setup: host is
	// never a fallback (design §8).
	Alternative string
}

func (e *MissingTechnologyError) next() string {
	if e.Alternative == "" {
		return fmt.Sprintf("Install it (%s):\n    yolo setup list          (shows the setups built here)", e.Install)
	}
	alt := fmt.Sprintf("Install it (%s), or choose an installed setup:\n    %s          (yolo setup list shows the rest)", e.Install, launchLine(e.Alternative))
	if b := MustBuiltIn(e.Alternative); b.Confinement == ConfinementSeatbelt && b.RunAs == RunAsSelf {
		// The one installed setup on a bare Mac. It is not the default's equal, and the reader
		// choosing it should know what it gives up.
		alt += "\n  " + e.Alternative + " runs the agent as you, under a Seatbelt profile alone: it confines less than " +
			e.Setup.Name + " would, with no VM and no separate account."
	}
	return alt
}

func (e *MissingTechnologyError) Error() string {
	return fmt.Sprintf("setup %q needs %s, which is not installed. %s", e.Setup.Name, e.Technology, e.next())
}

// AsDefault is the refusal when the setup was not named but is this OS's default.
func (e *MissingTechnologyError) AsDefault() string {
	where := "this machine"
	if e.GOOS == "darwin" {
		where = "this Mac"
	}
	return fmt.Sprintf("The default setup on %s, %s, needs %s, which is not installed. %s", where, e.Setup.Name, e.Technology, e.next())
}

// Buildable answers whether s can run on goos, and if not, the refusal naming the nearest
// built setup (design §5, §6, OQ-MT7). hasBinary reports whether a technology's binary is
// installed; nil skips that check (a caller asking only what yolo has built). The answer is a
// pure function of the setup's values, goos and hasBinary.
//
// Not decided here: whether the yolo account exists (its own refusal, naming
// `yolo yolo-account create`), Apple Container's Apple-silicon and macOS 26 floor, and that a
// host setup runs only under `yolo host` (design §8; the caller knows which verb it is).
func Buildable(s Setup, goos string, hasBinary func(string) bool) error {
	return buildable(s, goos, hasBinary, true)
}

func buildable(s Setup, goos string, hasBinary func(string) bool, findAlternative bool) error {
	mac := goos == "darwin"
	// A technology this OS does not have.
	switch s.Confinement {
	case ConfinementSeatbelt, ConfinementAppleContainer:
		if !mac {
			return fmt.Errorf("setup %q uses %s, which only macOS has. On this machine:\n    %s", s.Name, technologyName(s.Confinement), launchLine("podman-self"))
		}
	case ConfinementLandlock:
		if mac {
			return fmt.Errorf("setup %q uses Landlock, which only Linux has. On this machine:\n    %s", s.Name, launchLine("seatbelt-"+string(s.RunAs)))
		}
	}
	// A container run as the yolo account: not built on either OS, future work with no
	// built-in name (OQ-MT7). Only a user setup can ask for it.
	if s.RunAs == RunAsYoloAccount && (s.Confinement == ConfinementPodman || s.Confinement == ConfinementAppleContainer) {
		return fmt.Errorf("setups.%s: %s cannot run as the yolo account yet. Run it as you:\n    yolo config set setups.%s.run_as self --user\nOr use the built-in:\n    %s",
			s.Name, technologyName(s.Confinement), s.Name, launchLine(string(s.Confinement)+"-self"))
	}
	if s.Confinement == ConfinementLandlock {
		return fmt.Errorf("setup %q is not built yet. The nearest built setup on %s:\n    %s", s.Name, osName(goos), launchLine("podman-self"))
	}
	if s.RunAs == RunAsYoloAccount && !mac {
		return fmt.Errorf("setup %q is not built yet on %s: %s has no yolo account. The nearest built setup on %s:\n    %s",
			s.Name, osName(goos), osName(goos), osName(goos), launchLine(string(s.Confinement)+"-self"))
	}
	if inst, ok := install[s.Confinement]; ok && hasBinary != nil && !hasBinary(inst.binary) {
		e := &MissingTechnologyError{Setup: s, GOOS: goos, Technology: technologyName(s.Confinement), Binary: inst.binary, Install: inst.url}
		if findAlternative {
			e.Alternative = alternative(s, goos, hasBinary)
		}
		return e
	}
	return nil
}

func alternative(s Setup, goos string, hasBinary func(string) bool) string {
	for _, b := range builtIns {
		if b.Name == s.Name || b.Confinement == ConfinementHost || b.RunAs != RunAsSelf {
			continue
		}
		if buildable(b, goos, hasBinary, false) == nil {
			return b.Name
		}
	}
	return ""
}

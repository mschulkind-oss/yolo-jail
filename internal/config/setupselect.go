package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// setupselect.go is THE selection of a setup (docs/design/named-setups.md §2; plan slice 4a):
// the one function the launch, `yolo host`, `yolo setup list`, `yolo check` and the listing
// verbs (stop, ps, prune) call, so no two of them can name different setups for the same
// inputs. The order is the design's: --setup, then YOLO_SETUP, then the user config's `setup`,
// then this OS's default.
//
// ONE REMOVED INPUT IS WEIGHED HERE TOO: YOLO_RUNTIME is refused on the host, naming the
// YOLO_SETUP value the old one meant, and ignored with one warning in a launched session
// (SUP-D6): every jail an older launcher started exports YOLO_RUNTIME=podman, and refusing there
// would stop every nested launch. The removed config keys (`runtime`, `confinement`,
// `macos_user_mode`) choose nothing: config validation refuses them (setupsretired.go), so the
// selection never reads the merged config at all.

// RemovedRuntimeEnv is the variable setups replace (design §10).
const RemovedRuntimeEnv = "YOLO_RUNTIME"

// SetupVerb is which verb is asking: a launch never runs a host setup, and `yolo host` runs
// only one (design §8).
type SetupVerb int

const (
	// SetupForLaunch: `yolo`, `yolo run`, and every reader that answers for one (check, list).
	SetupForLaunch SetupVerb = iota
	// SetupForHost: `yolo host`. With no --setup it runs host-self, or the host setup
	// YOLO_SETUP names; a setup with walls is refused naming the launch that runs it.
	SetupForHost
)

// SetupInputs are a selection's inputs. Getenv nil reads the process environment.
type SetupInputs struct {
	Verb SetupVerb
	// Flag is --setup as typed, "" when not given.
	Flag   string
	Getenv func(string) string
	// GOOS is the OS the built-ins are judged for ("darwin", "linux").
	GOOS string
	// InSession: this process runs inside a launched session (YOLO_VERSION set), where a
	// removed YOLO_RUNTIME warns rather than refuses (SUP-D6).
	InSession bool
}

// SetupChoice is a selection's answer.
type SetupChoice struct {
	Setup  setup.Setup
	Source setup.Source
	// Warnings are lines the caller prints and goes on: a removed input that was ignored.
	Warnings []string
}

// From is where the choice came from, for a launch line's "- from <source>".
func (c SetupChoice) From() string { return c.Source.String() }

// SetupForRemovedRuntime is the built-in a removed YOLO_RUNTIME or `runtime` value meant, ""
// for a value yolo never knew. macos-user meant the yolo account by default, so it names
// seatbelt-yolo-account; every refusal that names it names seatbelt-self beside it.
func SetupForRemovedRuntime(rt string) string {
	switch rt {
	case "podman":
		return "podman-self"
	case "container":
		return "apple-container-self"
	case "macos-user":
		return "seatbelt-yolo-account"
	}
	return ""
}

// RemovedRuntimeEnvRefusal is design §10's refusal of a set YOLO_RUNTIME on the host.
func RemovedRuntimeEnvRefusal(value string) string {
	name := SetupForRemovedRuntime(value)
	if name == "" {
		return "YOLO_RUNTIME is removed: where an agent runs is now a setup. Use YOLO_SETUP=<name> (yolo setup list shows them), and unset it:\n    unset YOLO_RUNTIME"
	}
	also := ""
	if value == "macos-user" {
		other := "seatbelt-self"
		if name == other {
			other = "seatbelt-yolo-account"
		}
		also = " (or " + other + ")"
	}
	return fmt.Sprintf("YOLO_RUNTIME is removed: where an agent runs is now a setup. Use YOLO_SETUP=%s%s, and unset the old one:\n    unset YOLO_RUNTIME\n    YOLO_SETUP=%s yolo -- claude", name, also, name)
}

// removedRuntimeEnvWarning is the in-session half of SUP-D6: one line, and the launch goes on.
func removedRuntimeEnvWarning(value string) string {
	return fmt.Sprintf("yolo: YOLO_RUNTIME=%s is removed and ignored here; the setup decides (YOLO_SETUP, or --setup <name>).", value)
}

// SelectSetup resolves the selection for in.Verb, with every removed input weighed. An error
// is a refusal whose text names its next step; the caller prints it and stops.
func SelectSetup(in SetupInputs) (SetupChoice, error) {
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var choice SetupChoice
	if v := getenv(RemovedRuntimeEnv); v != "" {
		if !in.InSession {
			choice.Source = setup.FromEnv
			return choice, errors.New(RemovedRuntimeEnvRefusal(v))
		}
		choice.Warnings = append(choice.Warnings, removedRuntimeEnvWarning(v))
	}
	configName, user, _ := SelectedSetupFromConfig()
	env := getenv(setup.EnvVar)

	if in.Verb == SetupForHost {
		return selectHostSetup(in.Flag, env, user, in.GOOS, choice)
	}

	// AN INHERITED YOLO_SETUP THIS MACHINE CANNOT RUN is ignored in a session (SUP-D12): a session
	// is told its own setup through the variable, so a launch inside it inherits a value that
	// describes the outer session, not a choice made for this machine. Outside a session the
	// variable is the user's own, and a setup it names that cannot run here is refused.
	ignored, why := "", ""
	if in.InSession && in.Flag == "" && env != "" {
		if why = inheritedSetupUnusable(env, user, in.GOOS); why != "" {
			ignored, env = env, ""
		}
	}
	s, src, err := setup.Select(in.Flag, env, configName, user, in.GOOS)
	if err != nil {
		choice.Source = src
		return choice, err
	}
	choice.Setup, choice.Source = s, src
	if ignored != "" {
		choice.Warnings = append(choice.Warnings, inheritedSetupWarning(ignored, why, choice))
	}
	return choice, nil
}

// inheritedSetupUnusable is why the setup name names cannot run on goos, "" when it can: a name
// this machine does not define, or a setup yolo has not built for this OS (setup.Buildable,
// without the installed-binary check, which the launch makes itself).
func inheritedSetupUnusable(name string, user map[string]setup.Setup, goos string) string {
	if !setup.ValidName(name) {
		return "it is not a setup name"
	}
	s, err := setup.Lookup(name, user)
	if err != nil {
		return "this machine defines no setup by that name"
	}
	if err := setup.Buildable(s, goos, nil); err != nil {
		// The refusal's first sentence: what it says before the next step it names.
		line := strings.SplitN(err.Error(), "\n", 2)[0]
		if i := strings.LastIndex(line, ". "); i > 0 && strings.HasSuffix(line, ":") {
			line = line[:i]
		}
		return line
	}
	return ""
}

// inheritedSetupWarning is SUP-D12's one disclosure line: which inherited value was ignored, why,
// and what this launch selected instead.
func inheritedSetupWarning(name, why string, choice SetupChoice) string {
	return fmt.Sprintf("yolo: %s=%s came from the session this one runs in, and is ignored here: %s. "+
		"This launch uses %s, from %s; --setup <name> picks another.", setup.EnvVar, name, strings.TrimSuffix(why, "."),
		choice.Setup.Name, choice.From())
}

// selectHostSetup is `yolo host`'s selection: --setup when given, which must name a host
// setup; else YOLO_SETUP when it names one (a session's own marker names a setup with walls,
// and `yolo host` is the verb that leaves them); else host-self.
func selectHostSetup(flag, env string, user map[string]setup.Setup, goos string, choice SetupChoice) (SetupChoice, error) {
	if flag != "" {
		s, err := setup.Lookup(flag, user)
		if err != nil {
			return choice, fmt.Errorf("--setup: %w", err)
		}
		if s.Kind() != setup.KindHost {
			return choice, fmt.Errorf("setup %q has walls, and `yolo host` runs only a setup without them. Run that one as a launch:\n    %s\nOr on your own machine:\n    yolo host -- claude", s.Name, setup.LaunchLine(s))
		}
		choice.Setup, choice.Source = s, setup.FromFlag
		return choice, nil
	}
	if env != "" && setup.ValidName(env) {
		if s, err := setup.Lookup(env, user); err == nil && s.Kind() == setup.KindHost {
			choice.Setup, choice.Source = s, setup.FromEnv
			return choice, nil
		}
	}
	choice.Setup, choice.Source = setup.MustBuiltIn("host-self"), setup.FromDefault
	return choice, nil
}

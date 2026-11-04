package run

import (
	"errors"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// setupgate.go is THE SETUP GATE (docs/design/named-setups.md; plan slice 4a): a launch runs one
// named setup, chosen by config.SelectSetup — the selection `yolo host`, `yolo setup list` and
// `yolo check` call too — and refused here, before anything is built for it, when it cannot run:
// a name nothing defines, a technology this OS does not have or that is not installed, a setup
// not built yet, or a host setup, which only `yolo host` runs (design §8).
//
// It replaces the notch gate (refuseUnbuiltNotch) and the runtime probe: the backend is the
// setup's (Setup.Backend), never probed for, and the macos-user mode is its run_as.

// SetupRef carries the resolved setup across the by-value Options seam (Options.SetupRef).
type SetupRef struct {
	Setup setup.Setup
}

// setupGOOS is the OS this launch judges the built-ins for: setupOS when a test states it, else
// the platform's.
func (o *Options) setupGOOS() string {
	if o.setupOS != "" {
		return o.setupOS
	}
	if o.IsMacOS {
		return "darwin"
	}
	return "linux"
}

// resolveSetup selects and gates this launch's setup, and reports the exit code and whether it
// refused: rc 2 when `--setup` names a setup this launch cannot run (misuse, the code a bad
// `--at` had), rc 1 for every other refusal, a missing install included, since that is the
// machine's state and not what was typed. On a pass it records the setup and the three values
// derived from it: the notch, the macos-user mode and the SetupRef. The merged config is not
// read: the keys that once chose a backend are refused by config validation (named-setups-plan.md
// slice 5), and the selection's own inputs are the flag, the environment and the user config.
func (o *Options) resolveSetup(_ *jsonx.OrderedMap) (int, bool) {
	choice, err := config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Flag:      o.SetupName,
		Getenv:    o.Getenv,
		GOOS:      o.setupGOOS(),
		InSession: o.inJail(),
	})
	for _, w := range choice.Warnings {
		o.pr(o.Stderr).print("[yellow]" + richtext.Escape(w) + "[/yellow]")
	}
	if err != nil {
		o.printSetupRefusal(err.Error())
		return o.setupRefusalCode(choice, false), true
	}
	s := choice.Setup
	if s.Kind() == setup.KindHost {
		o.printSetupRefusal(setup.HostRefusal(s))
		return o.setupRefusalCode(choice, false), true
	}
	if err := setup.Buildable(s, o.setupGOOS(), func(bin string) bool {
		_, ok := o.LookPath(bin)
		return ok
	}); err != nil {
		text := err.Error()
		var missing *setup.MissingTechnologyError
		isMissing := errors.As(err, &missing)
		if isMissing && choice.Source == setup.FromDefault {
			text = missing.AsDefault()
		}
		o.printSetupRefusal(text)
		return o.setupRefusalCode(choice, isMissing), true
	}
	// A `container` on PATH that is not Apple's is refused here, before anything is built for
	// it, by the probe and with the next step `yolo check` gives (setup.CheckAppleContainer). It
	// is the machine's state, never what was typed: rc 1.
	if err := setup.CheckAppleContainer(s, o.setupGOOS(), o.LookPath, o.appleContainerVersionOutput); err != nil {
		o.printSetupRefusal(err.Error())
		return 1, true
	}
	o.Setup, o.setupChoice = s, choice
	o.atNotch = config.ConfinementJail
	if s.Kind() == setup.KindGuest {
		o.atNotch = config.ConfinementGuest
	}
	o.MacosUserMode = macosuser.ModeFor(s.RunAs)
	if o.SetupRef != nil {
		o.SetupRef.Setup = s
	}
	return 0, false
}

// setupRefusalCode is a refusal's exit code: 2 for what `--setup` typed, 1 otherwise (see
// resolveSetup).
func (o *Options) setupRefusalCode(choice config.SetupChoice, missingInstall bool) int {
	if o.SetupName != "" && choice.Source == setup.FromFlag && !missingInstall {
		return 2
	}
	return 1
}

// printSetupRefusal prints a setup refusal: its first line is the headline, and every line after
// it the next step, as internal/setup words them.
func (o *Options) printSetupRefusal(text string) {
	out := o.pr(o.Stderr)
	lines := strings.Split(text, "\n")
	out.print("[bold red]Refusing to launch: " + richtext.Escape(lines[0]) + "[/bold red]")
	for _, l := range lines[1:] {
		out.print(richtext.Escape(l))
	}
}

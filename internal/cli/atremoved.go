package cli

// atremoved.go is THE ONE REFUSAL of the removed `--at` flag (docs/design/named-setups.md §10,
// named-setups-plan.md slice 4a): where an agent runs is a named setup now, chosen with
// `--setup <name>`, so every parser that met `--at` (the launch, `yolo host`, `yolo apply`, the
// `yolo config` and `yolo surface` read verbs, `yolo pack build`) refuses it here, in one set of
// words, and each names the spelling that replaces the notch it typed.

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// atFlag is the removed flag's spelling, read only to refuse it.
const atFlag = "--at"

// atRemoved is the launch's refusal of `--at <notch>` (atRemovedFor with no verb).
func atRemoved(notch string) error { return atRemovedFor("", notch) }

// atRemovedFor is the refusal of `--at <notch>` on verb ("" for the launch, else the verb's
// words after `yolo`, such as "apply" or "config get"), naming its replacement: for the launch,
// `yolo host` at the host notch and the setup the notch launched for the others; for a verb,
// that verb with `--setup` and the built-in the notch meant.
func atRemovedFor(verb, notch string) error {
	jailName := setup.Default(setupGOOS).Name
	var name string
	switch config.Confinement(notch) {
	case config.ConfinementHost:
		if verb == "" {
			return errors.New("`--at` is removed. Use `--setup <name>`; the host notch is its own verb now:\n" +
				"    yolo host -- <cmd>")
		}
		name = "host-self"
	case config.ConfinementGuest:
		name = "seatbelt-yolo-account"
	case config.ConfinementJail:
		name = jailName
	}
	spell := func(n string) string {
		if verb == "" {
			return "yolo --setup " + n + " -- <cmd>"
		}
		return "yolo " + verb + " --setup " + n
	}
	switch name {
	case "host-self":
		return errors.New("`--at` is removed. Use `--setup <name>`; the host notch is the host-self setup:\n" +
			"    " + spell(name))
	case "seatbelt-yolo-account":
		return errors.New("`--at` is removed. Use `--setup <name>`; the guest notch is a Seatbelt setup on a Mac:\n" +
			"    " + spell(name))
	case jailName:
		return errors.New("`--at` is removed. Use `--setup <name>`; the jail notch is a container setup:\n" +
			"    " + spell(name))
	}
	if notch == "" {
		return fmt.Errorf("`--at` is removed. Use `--setup <name>` (`yolo setup list` names them):\n"+
			"    %s", spell(jailName))
	}
	return fmt.Errorf("`--at` is removed. Use `--setup <name>` (`yolo setup list` names them):\n"+
		"    %s\n  (%q was never a notch.)", spell(jailName), notch)
}

// lookupNamedSetup is the setup a verb's `--setup <name>` names, a built-in or one of the user
// config's `[setups.*]`, or the refusal naming what is defined (setup.Lookup).
func lookupNamedSetup(name string) (setup.Setup, error) {
	_, user, _ := config.SelectedSetupFromConfig()
	return setup.Lookup(name, user)
}

// setupNotch is the confinement notch a setup acts at (setup.Setup.Kind, named as the notch
// vocabulary the verbs still route on until slice 5 retires it).
func setupNotch(s setup.Setup) config.Confinement { return config.Confinement(s.Kind().String()) }

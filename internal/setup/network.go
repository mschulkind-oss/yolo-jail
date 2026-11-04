package setup

import "fmt"

// networkPhrase is the technology's network in Describe's words (design §4's network table).
func networkPhrase(c Confinement) string {
	switch c {
	case ConfinementPodman:
		return "bridged unless network.mode is host"
	case ConfinementAppleContainer:
		return "always bridged"
	case ConfinementSeatbelt:
		return "shares the Mac's network"
	case ConfinementHost:
		return "shares this machine's network"
	}
	return "" // landlock: its design's
}

// NetworkModeProblem is the refusal for a written `network.mode` the setup's technology
// cannot hold (SU-R3, SU-D8), or "" when it can. mode is the value written in the project's
// config, "" when unwritten: an unwritten value is the technology's own network and never a
// problem. Podman honors both values; Apple Container is always bridged; Seatbelt and host
// always share the machine's network; Landlock's network is its own design's, so nothing is
// refused for it yet. A value that is neither "bridge" nor "host" is not this check's to judge.
//
// The text has no `file:line:` prefix; the caller, which knows where the value was written,
// adds it (design §6 shows `yolo-jail.toml:7: ` before it).
func NetworkModeProblem(s Setup, mode string) string {
	return NetworkModeProblemIn(s, mode, "--project")
}

// NetworkModeProblemIn is NetworkModeProblem for a value written in the config scope whose
// `yolo config unset` flag is scope ("--project", "--local" or "--user"), so the second step
// removes the key from the file that holds it (SUP-D13).
func NetworkModeProblemIn(s Setup, mode, scope string) string {
	why := networkHeadline(s, mode)
	if why == "" {
		return ""
	}
	return fmt.Sprintf("network.mode = %q: ", mode) + why + networkSetupStep +
		"\nOr, if the project does not need it:  yolo config unset network.mode " + scope
}

// NetworkFlagProblem is the refusal for a typed `--network <mode>` the setup's technology cannot
// hold, or "" when it can: the same judgment as NetworkModeProblem, naming the flag, whose
// second step is to drop it, since no config file holds it (SUP-D13).
func NetworkFlagProblem(s Setup, mode string) string {
	why := networkHeadline(s, mode)
	if why == "" {
		return ""
	}
	return "--network " + mode + ": " + why + networkSetupStep +
		"\nOr, if the project does not need it:  launch without --network " + mode
}

// networkSetupStep is the first step both refusals name: a setup whose technology holds either
// mode.
const networkSetupStep = " For this launch, a setup that can:\n    yolo --setup podman-self -- claude"

// networkHeadline is why setup s cannot hold mode, "" when it can.
func networkHeadline(s Setup, mode string) string {
	switch {
	case s.Confinement == ConfinementAppleContainer && mode == "host":
		return fmt.Sprintf("setup %s always runs bridged, so the jail cannot share this machine's network.", s.Name)
	case s.Confinement == ConfinementSeatbelt && mode == "bridge":
		return fmt.Sprintf("setup %s always shares the Mac's network, so the agent cannot have a loopback of its own.", s.Name)
	case s.Confinement == ConfinementHost && mode == "bridge":
		return fmt.Sprintf("setup %s always shares this machine's network, so the agent cannot have a loopback of its own.", s.Name)
	}
	return ""
}

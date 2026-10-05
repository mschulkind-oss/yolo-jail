package cli

// hostcapabilities.go is OQ-CAP2's gate at the HOST notch (docs/design/agent-auth-modes.md §6.2):
// a user config declaring `required_capabilities` that nothing in the launch satisfies refuses
// `yolo host -- <cmd>` exactly as it refuses a jail launch, in the same words
// (config.UnmetCapabilityRefusal). Until 2026-10-04 the host launched without asking it, so the
// declaration "this environment does not work without X" was a refusal at one notch and nothing
// at the other (declaration-parity.md DP-B46).
//
// USER SCOPE ONLY, the boundary every host composition draws (config.UserScopeConfig's whole
// argument): the census reads the config this launch composes from and nothing a workspace wrote,
// so a cloned repository's yolo-jail.jsonc can neither refuse a host launch nor satisfy one.

import (
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostCapabilityLaunch is the launch the census counts at `yolo host --`: this launch's own
// selection (loadedHostPacks, the one its composition stages, `needs` applied), under the profile
// table this launch folds (hostProfileFold: the user scope's `profile` key with the typed -p for
// agent over it). complete is false when the selection is one the composition refuses, so the
// census warns and that refusal speaks in its own words further on.
//
// ANY AGENT'S ACTIVE SOURCE counts, as in a jail (capabilities.go's file doc): the selection can
// install agents other than the one this launch runs, and those run on the key's profile alone.
func hostCapabilityLaunch(cfg *jsonx.OrderedMap, agent, typed string) *config.CapabilityLaunch {
	return &config.CapabilityLaunch{
		Packs: func() ([]*packload.Pack, bool) {
			sel := loadedHostPacks(cfg, agent, typed)
			return sel.packs, sel.launchRefusal() == nil
		},
		ProfileSets: func(packs []*packload.Pack) map[string][]string {
			return packload.ProfileSets(hostProfileFold(cfg, packs, agent, typed).Table)
		},
	}
}

// refuseHostUnmetCapabilities is the gate itself: it reports whether the launch of agent (the
// command's base name) on typed (the -p as hostProfileFor resolved it, "" for none) must stop,
// having printed why. A malformed value is refused with the validator's own message, since the
// census reads a non-list as requiring nothing and the jail refuses that config at validation,
// and its next step names the file and line that wrote the value: any file of the user scope can
// (an include_if_found file, the inherited nested-launch file, a --user-layer), so config.jsonc
// is named only when the record cannot place it. The hatch is the jail's
// (config.AllowUnmetCapabilitiesEnv), read from this process's own environment.
func refuseHostUnmetCapabilities(errw io.Writer, agent, typed string) bool {
	cfg := config.UserScopeConfigOrEmpty()
	if probs := config.RequiredCapabilitiesProblems(cfg); len(probs) > 0 {
		at := "in " + paths.UserConfigPath()
		if where := capabilityKeyLocations(probs); len(where) > 0 {
			at = "at " + strings.Join(where, " and at ")
		}
		printHostLines(errw, []string{"refusing to launch: " + strings.Join(probs, "; "),
			"  Make it a list of capability names (`\"required_capabilities\": [\"web_search\"]`) " +
				at + ", or remove it. `yolo check` reports the same problem."})
		return true
	}
	missing, err := config.UnmetCapabilities(cfg, hostCapabilityLaunch(cfg, agent, typed))
	lines, refuse := config.UnmetCapabilityRefusal(missing, err,
		os.Getenv(config.AllowUnmetCapabilitiesEnv) != "",
		capabilityKeyLocations(missing))
	printHostLines(errw, lines)
	return refuse
}

// capabilityKeyLocations locates `required_capabilities` in the user scope's files, read only when
// there is something to name (a gap, or a malformed value): UserScopeSources reads the files
// again, so it belongs on the error path.
func capabilityKeyLocations(problems []string) []string {
	if len(problems) == 0 {
		return nil
	}
	return config.UserScopeSources().Locations("config.required_capabilities")
}

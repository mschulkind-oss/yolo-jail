package check

// profiledeclarations.go predicts the launch's undeclared-profile refusal.
//
// Every launch refuses a selected profile name that no selected pack and no user `profiles`
// entry declares (run's checkProfileDeclarations, OQ-CS6), and until this file `yolo check`
// passed the same config: the one thing `check` exists to prevent. It asks the launch's own
// check, config.UndeclaredProfileProblems, over the selection this config describes (the user
// scope's packs and the `profile` key, config.ConfigProfileFold), so the names and the next step
// are the launch's words. A `-p` is an argument to a launch that has not happened, the limit
// capabilities.go states for its gate too.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// profileDeclarationGap is the launch's undeclared-profile refusal for the config, one FAIL line
// per name, in the launch's "packs: " form. packs is the selection to read (the user scope's,
// config.UserScopeSelectedPacks); complete false means a selected pack could not be read, and
// then nothing is predicted, since that pack may be the declarer and the launch reports the
// unreadable pack itself. The same for a profiles file that does not load. userProfiles is the
// launch's selection namespace over the selection (config.SelectableProfiles), so a provider
// selected by its own name is declared here as it is at the launch.
func profileDeclarationGap(cfg *jsonx.OrderedMap, packs []*packload.Pack, complete bool,
	userProfiles func(selected []*packload.Pack) (map[string]packload.UserProfile, error),
	universe func() []*packload.Pack) []string {
	selected := packs
	if !complete {
		// An unreadable selected pack may declare the profile, so do not claim an undeclared
		// profile while the selection is incomplete.
		return nil
	}
	declared, err := userProfiles(selected)
	if err != nil {
		return nil
	}
	fold := config.ConfigProfileFold(cfg, selected)
	problems := config.UndeclaredProfileProblems(fold, 0, declared, config.ReplacedNameRefusals(cfg, selected, declared),
		selected, universe())
	out := make([]string, len(problems))
	for i, p := range problems {
		out[i] = "packs: " + p
	}
	return out
}

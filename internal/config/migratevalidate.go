package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// MigrationGate is the launch's config gate run over one layout of config files: what
// loadAndValidateConfig refuses a launch for, and what `yolo check` reports under Merged
// Configuration.
type MigrationGate struct {
	// Problems are the refusals as the validators word them, comparable between two layouts.
	Problems []string
	// Located are the same refusals as a launch prints them, each naming the file and line.
	Located []string
	// Unchecked says which checks could not run in full, and why: a selected pack this machine
	// has not fetched yet, which only the launch fetches.
	Unchecked []string
}

// RunMigrationGate composes the config a launch would compose with the user config read from
// userConfigDir ("" for the usual ~/.config/yolo-jail) and the workspace's files read from
// workspace, and runs the launch's config gate over it: the strict load, ValidateConfig, and the
// pack switch's refusals. A load that fails is the one problem. `yolo config migrate` runs it on
// the converted files, staged off to the side, before it touches any original (RC-D44), so a
// conversion the next launch would refuse is refused instead.
//
// It reads the authored files, never a jail's assembled copy, and a --user-layer applies as it
// does at a launch.
func RunMigrationGate(userConfigDir, workspace string, resolver LoopholeResolver) MigrationGate {
	if userConfigDir != "" {
		defer paths.ReadUserConfigFrom(userConfigDir)()
	}
	cfg, node, err := composeReadableAuthoredConfigWithInputUseAudit(workspace, true, func(string) {}, true, true, nil, nil, nil)
	if err != nil {
		return MigrationGate{Problems: []string{err.Error()}, Located: []string{err.Error()}}
	}
	src := sourcesOf(node)
	errs, _ := ValidateConfig(cfg, workspace, resolver)
	errs = append(errs, AllowPackSwitchErrors()...)
	var unchecked []string
	// The launch's capability gate and its undeclared-profile refusal, as `yolo check` predicts
	// them (RC-D44): over the selected packs that resolve with no fetch. A pack that needs one is
	// fetched by the launch first, so what it could add is said as unchecked, never guessed.
	selected, complete := UserScopeSelectedPacks()
	if !complete {
		unchecked = append(unchecked, "some selected packs are not fetched on this machine yet, so the "+
			"capability and profile checks ran over the packs that are; the next launch fetches the rest "+
			"and runs them in full")
	}
	missing, capErr := UnmetCapabilities(cfg, &CapabilityLaunch{
		Packs: func() ([]*packload.Pack, bool) { return selected, complete },
		ProfileSets: func(packs []*packload.Pack) map[string][]string {
			return packload.ProfileSets(ConfigProfileSets(cfg, packs))
		},
	})
	if len(missing) > 0 && capErr == nil {
		errs = append(errs, "config.required_capabilities declares '"+strings.Join(missing, "', '")+
			"', and nothing this config or its selected packs declare satisfies it")
	}
	if len(missing) > 0 && capErr != nil && complete {
		unchecked = append(unchecked, "the capability check could not run: "+capErr.Error())
	}
	declared, err := LoadSelectableProfiles(nil, cfg, selected)
	if err != nil {
		errs = append(errs, err.Error())
	} else {
		for _, p := range UndeclaredProfileProblems(ConfigProfileFold(cfg, selected), 0, declared, selected, packload.Embedded()) {
			errs = append(errs, "packs: "+p)
		}
	}
	return MigrationGate{Problems: errs, Located: src.Annotate(errs), Unchecked: unchecked}
}

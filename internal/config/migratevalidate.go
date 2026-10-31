package config

import (
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
	return MigrationGate{Problems: errs, Located: src.Annotate(errs)}
}

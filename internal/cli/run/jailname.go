package run

// jailname.go is the name a launch's RUNNING jail is keyed by (docs/design/named-setups-plan.md
// SUP-D2, slice 6a-iii): the project key runtime.FromWorkspace returns, with the setup's name
// appended (runtime.JailName). Everything that identifies a running jail or its per-run state
// reads it: the container name, the keeper key and its macos-user and guest keys, the host
// services' short hash, the staged pack tree, the tracking file, the launch lock. What stays per
// project (the approval and grant records) keeps runtime.FromWorkspace.
//
// A jail an older yolo started is keyed by the project key alone. Its name is the LEGACY jail
// name (legacyJailName); SUP-D10's refusal (legacyjail.go) is the one place a launch asks about it.

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// JailNameFor is the jail name of a launch of s in workspace. A setup with no name, which only a
// caller that resolved none builds (a unit test's bare Options), names the legacy jail.
func JailNameFor(workspace string, s setup.Setup) string {
	if s.Name == "" {
		return yoloruntime.FromWorkspace(workspace)
	}
	return yoloruntime.JailName(workspace, s.Name)
}

// jailName is this launch's jail name: a keeper's own jail (keeperJailName), its selected setup's
// (resolveSetup), or, read before the setup is resolved, the one the selection would make now
// (selectedJailName).
func (o *Options) jailName() string {
	if o.keeperJailName != "" {
		return o.keeperJailName
	}
	if o.Setup.Name != "" {
		return JailNameFor(o.Workspace, o.Setup)
	}
	return o.selectedJailName()
}

// selectedJailName is the jail name of the setup this launch would select, asked quietly: a
// reader that runs before resolveSetup (the config-migration offer's attach probe) must look where
// the launch will. A selection that refuses names the legacy jail, which the launch then refuses
// before anything else reads it.
func (o *Options) selectedJailName() string {
	s, ok := o.selectedSetup()
	if !ok {
		return yoloruntime.FromWorkspace(o.Workspace)
	}
	return JailNameFor(o.Workspace, s)
}

// selectedSetup is the setup this launch would select, asked quietly; false when the selection
// refuses.
func (o *Options) selectedSetup() (setup.Setup, bool) {
	// A caller that reads a launch record before setDefaults ran (a live apply's bare Options)
	// has no Getenv seam yet; the process environment is the one setDefaults would install.
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	choice, err := config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Flag:      o.SetupName,
		Getenv:    getenv,
		GOOS:      o.setupGOOS(),
		InSession: getenv("YOLO_VERSION") != "",
	})
	if err != nil {
		return setup.Setup{}, false
	}
	return choice.Setup, true
}

// setupName is the name of this launch's setup, which keys its launch records under
// .yolo/setups (config.WorkspaceConfigBootPath, SUP-D37): the resolved setup's, or the one the
// selection would make now. "" when the selection refuses, which the config package reads as
// this session's setup, an error on the host.
func (o *Options) setupName() string {
	if o.Setup.Name != "" {
		return o.Setup.Name
	}
	s, _ := o.selectedSetup()
	return s.Name
}

// legacyJailName is the name every jail of the workspace was keyed by before setup-qualified
// names (named-setups-plan.md SUP-D10): the project key.
func (o *Options) legacyJailName() string {
	return yoloruntime.FromWorkspace(o.Workspace)
}

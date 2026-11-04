package cli

// setupbackend.go is the backend the read-only verbs (`yolo ps`, `yolo stop`, `yolo prune`,
// `yolo describe`, `yolo capture`, the config target) act on: the backend of the setup a launch
// from the workspace would select, through the one selection every verb shares
// (config.SelectSetup; named-setups-plan.md slice 4a). A launch refuses what it cannot run; these
// verbs never do, so a selection that refuses (a removed YOLO_RUNTIME on the host, a name nothing
// defines) still answers: the setup the removed value meant, else the platform's probe. So does
// a removed config key whose setup this OS lacks (a `confinement: "guest"` on Linux means a
// Seatbelt setup): it names no backend here, so the answer is the platform's probe, never
// macos-user on Linux. The platform is paths.IsMacOS's, the one these verbs read everywhere else.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// selectedSetupBackend is the backend of the setup a launch with merged (nil for none) would
// select, tolerantly: see the file header.
func selectedSetupBackend(merged *jsonx.OrderedMap) string {
	goos := platformGOOS()
	choice, err := selectedSetup(merged)
	s := choice.Setup
	if err != nil {
		s = setup.Setup{}
		if name := config.SetupForRemovedRuntime(os.Getenv(config.RemovedRuntimeEnv)); name != "" {
			s = setup.MustBuiltIn(name)
		}
	}
	// A setup the removed `confinement` key chose (`"guest"`, written on a Mac) answers only where
	// this OS has its technology, as the guest notch did: nil skips the install check. Every other
	// choice is answered as made, a removed `runtime` key included, as that key was.
	bridged := err == nil && choice.Source == setup.FromRemovedKey && strings.HasPrefix(choice.RemovedKey, "confinement")
	if b := s.Backend(); b != "" && (!bridged || setup.Buildable(s, goos, nil) == nil) {
		return b
	}
	return runtime.ResolveRuntime("", "", paths.IsMacOS, func(bin string) bool {
		_, lerr := exec.LookPath(bin)
		return lerr == nil
	})
}

// selectedSetup is the selection a launch with merged (nil for none) would make on this
// platform, through config.SelectSetup: the setup, where it came from, and the lines a launch
// would warn. An error is the launch's refusal; a read-only verb reports it and goes on.
func selectedSetup(merged *jsonx.OrderedMap) (config.SetupChoice, error) {
	return selectedSetupFlag(merged, "")
}

// selectedSetupFlag is selectedSetup for a verb given `--setup flag` ("" for none), which wins
// over everything else, as it does for a launch.
func selectedSetupFlag(merged *jsonx.OrderedMap, flag string) (config.SetupChoice, error) {
	return config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Flag:      flag,
		Getenv:    os.Getenv,
		Merged:    merged,
		GOOS:      platformGOOS(),
		InSession: os.Getenv("YOLO_VERSION") != "",
	})
}

// hostYoloAccountRefusal is the refusal of a host setup run as the yolo account, which is not
// built yet (named-setups-plan.md slice 8 builds host-yolo-account). `yolo host`, `yolo apply`
// and the config verbs all say it, naming the same two next steps, so none of them quietly acts
// on the real home in its place.
func hostYoloAccountRefusal(name string) string {
	return fmt.Sprintf("setup %q is not built yet: running as the yolo account with no Seatbelt "+
		"profile arrives in a later release. Run it as you now:\n    yolo host -- claude\n"+
		"Or keep the yolo account and its sandbox:\n    yolo --setup seatbelt-yolo-account -- claude", name)
}

// verbSetupRefusal is why a verb acting for setup s (`yolo apply`, the config verbs) refuses
// it, "" when it may act: what setup.Buildable refuses on goos, and a host setup run as the
// yolo account (hostYoloAccountRefusal). The install check is left out: only a launch needs the
// technology installed, and the launch a jail-notch apply runs makes that check itself.
func verbSetupRefusal(s setup.Setup, goos string) string {
	if err := setup.Buildable(s, goos, nil); err != nil {
		return err.Error()
	}
	if s.Kind() == setup.KindHost && s.RunAs == setup.RunAsYoloAccount {
		return hostYoloAccountRefusal(s.Name)
	}
	return ""
}

// platformGOOS is the OS these verbs judge setups for: paths.IsMacOS, which a test states with
// onPlatform, rather than the build's GOOS.
func platformGOOS() string {
	if paths.IsMacOS {
		return "darwin"
	}
	return "linux"
}

// workspaceSetupBackend is selectedSetupBackend with the workspace's config, loaded loosely
// (non-strict, warnings dropped): a config that does not load is read as none, so a malformed
// file degrades to the selection without it rather than crashing a diagnostic command.
func workspaceSetupBackend(workspace string) string {
	var merged *jsonx.OrderedMap
	if workspace != "" {
		if cfg, err := config.LoadConfig(workspace, false, func(string) {}); err == nil {
			merged = cfg
		}
	}
	return selectedSetupBackend(merged)
}

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
	choice, err := config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Getenv:    os.Getenv,
		Merged:    merged,
		GOOS:      goos,
		InSession: os.Getenv("YOLO_VERSION") != "",
	})
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

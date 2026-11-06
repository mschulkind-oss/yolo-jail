package cli

// setupbackend.go is the backend the read-only verbs (`yolo ps`, `yolo stop`, `yolo prune`,
// `yolo describe`, `yolo capture`, the config target) act on: the backend of the setup a launch
// from the workspace would select, through the one selection every verb shares
// (config.SelectSetup; named-setups-plan.md slice 4a). A launch refuses what it cannot run; these
// verbs never do, so a selection that refuses (a removed YOLO_RUNTIME on the host, a name nothing
// defines) still answers: the setup the removed value meant, else the platform's probe. The
// platform is paths.IsMacOS's, the one these verbs read everywhere else.

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// selectedSetupBackend is the backend of the setup a launch would select, tolerantly: see the
// file header. The merged config is not read (the selection's inputs are the flag, the
// environment and the user config); the parameter keeps the callers' shape.
func selectedSetupBackend(_ *jsonx.OrderedMap) string {
	choice, err := selectedSetup(nil)
	s := choice.Setup
	if err != nil {
		s = setup.Setup{}
		if name := config.SetupForRemovedRuntime(os.Getenv(config.RemovedRuntimeEnv)); name != "" {
			s = setup.MustBuiltIn(name)
		}
	}
	if b := s.Backend(); b != "" {
		return b
	}
	return runtime.ResolveRuntime("", "", paths.IsMacOS, func(bin string) bool {
		_, lerr := exec.LookPath(bin)
		return lerr == nil
	})
}

// selectedSetup is the selection a launch would make on this platform, through
// config.SelectSetup: the setup, where it came from, and the lines a launch would warn. An error
// is the launch's refusal; a read-only verb reports it and goes on. The merged config is not
// read; the parameter keeps the callers' shape.
func selectedSetup(merged *jsonx.OrderedMap) (config.SetupChoice, error) {
	return selectedSetupFlag(merged, "")
}

// selectedSetupFlag is selectedSetup for a verb given `--setup flag` ("" for none), which wins
// over everything else, as it does for a launch.
func selectedSetupFlag(_ *jsonx.OrderedMap, flag string) (config.SetupChoice, error) {
	return config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Flag:      flag,
		Getenv:    os.Getenv,
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

// workspaceSetupBackend is selectedSetupBackend for a verb acting on workspace. The selection
// reads no workspace config (a project cannot choose a setup, named-setups.md §8), so it is the
// same answer for every workspace.
func workspaceSetupBackend(_ string) string { return selectedSetupBackend(nil) }

// actSetup is the setup a jail a host act boots on backend rt runs as: a capture jail, a fork's
// build jail, the host floor's (named-setups-plan.md SUP-D14). It is the setup a launch selects
// (config.SelectSetup, as the launch's own) when that setup runs on rt, so its run_as and its
// name carry over: `setup = "seatbelt-self"` captures as you, with no yolo account. Otherwise it
// is rt's built-in: podman-self, apple-container-self, or for macos-user seatbelt-yolo-account,
// the account mode, whose readers decline rather than guess where no account is. A selection
// that refuses (a removed YOLO_RUNTIME on the host) is answered by rt's built-in too; the act's
// own launch then refuses it naming the next step.
func actSetup(rt string) setup.Setup {
	if choice, err := selectedSetup(nil); err == nil && choice.Setup.Backend() == rt {
		return choice.Setup
	}
	switch rt {
	case "podman":
		return setup.MustBuiltIn("podman-self")
	case "container":
		return setup.MustBuiltIn("apple-container-self")
	case "macos-user":
		return setup.MustBuiltIn("seatbelt-yolo-account")
	}
	return setup.Setup{}
}

// workspaceJailName is the jail name a launch from workspace would key its jail by now
// (run.JailNameFor of the selected setup; named-setups-plan.md SUP-D2), tolerantly: a selection
// that refuses names the jail of the built-in its backend would act as (actSetup).
func workspaceJailName(workspace string) string {
	return run.JailNameFor(workspace, workspaceSetup())
}

// workspaceSetup is the setup a launch here would run as: the selected one, or, when the
// selection refuses, the built-in setup of the backend it would fall back to.
func workspaceSetup() setup.Setup {
	if choice, err := selectedSetup(nil); err == nil {
		return choice.Setup
	}
	return actSetup(selectedSetupBackend(nil))
}

// recordSetupName is the setup whose launch records (.yolo/setups/<setup>, config.WorkspaceConfigBootPath)
// a reader here reads: in a jail "", the session's own (YOLO_SETUP), which is a named error when
// the session names none; on the host the setup a launch here would run as (workspaceSetup).
func recordSetupName() string {
	if config.InJail() {
		return ""
	}
	return workspaceSetup().Name
}

// actJailName is the jail name of an act's jail on rt in staging: the name of the setup it runs
// as (actSetup, SUP-D14), which runCaptureJail hands its launch. rt "" is an act that names no
// runtime, whose launch selects as any launch does (workspaceJailName).
func actJailName(staging, rt string) string {
	if rt == "" {
		return workspaceJailName(staging)
	}
	return run.JailNameFor(staging, actSetup(rt))
}

// actMacosUserMode is who a macos-user act runs as (macosuser.ModeFor of actSetup's run_as): the
// capture, the host floor's probes and prune's guest pack-tree sweep read it, as the launch reads
// its own setup's.
func actMacosUserMode() macosuser.Mode { return macosuser.ModeFor(actSetup("macos-user").RunAs) }

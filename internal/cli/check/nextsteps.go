package check

// nextsteps.go holds the next steps several `yolo check` findings share: the re-check, the
// repo-root fix, the container runtime's install line for this host, and the note for a fault
// that is yolo's own. A next step is something the user can do at once with no research — a
// command, or exact instructions for this platform — and a finding without one is a dead end;
// both terms are coined in docs/reference/happy-path-principle.md, the rule this file applies.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// recheck ends a next step: after a fix, check again rather than take it on faith (rule 5).
const recheck = "then: yolo check"

// issuesURL is where a fault that is yolo's own is reported. TestIssuesURLIsThisModulesRepo
// pins it to go.mod's module path, so a moved repository fails a test instead of the link.
const issuesURL = "https://github.com/mschulkind-oss/yolo-jail/issues"

// yoloBugNote is rung 4 for a fault no setup change can fix: it says so and names who can act.
func yoloBugNote(what string) string {
	return what + " is a yolo bug, not something your setup can fix: report it at " + issuesURL +
		" with the output of `yolo --version`."
}

// repoRootFix is the fix for a repo root reporoot.Resolve cannot find, as the launch's refusal
// for the same fault words it (internal/cli/run/run.go, "Cannot find yolo-jail repo root"),
// ending on the re-check rather than a launch.
const repoRootFix = "The yolo CLI needs the repo (a flake) to build the jail image.\n" +
	"Fix: reinstall so the flake bundle ships with the binary (`just install`), or\n" +
	"point yolo at a checkout with YOLO_REPO_ROOT. The working directory is never\n" +
	"consulted, so standing in a checkout is not enough:\n" +
	"  YOLO_REPO_ROOT=~/code/yolo-jail yolo check"

// flakeMissingNote is the fix for a resolved root that holds no flake.nix. Only YOLO_REPO_ROOT
// can name one (reporoot accepts a go.mod there); a bundle missing its flake was damaged after
// it resolved.
func flakeMissingNote(rr reporoot.Resolution) string {
	if rr.Source == reporoot.FromEnv {
		return "YOLO_REPO_ROOT names " + rr.Root + ", which is not a yolo-jail checkout.\n" +
			"Point it at one, or unset it to use the flake bundle installed with yolo:\n" +
			"  YOLO_REPO_ROOT=~/code/yolo-jail yolo check"
	}
	return "The flake bundle at " + rr.Root + " is incomplete.\n" + repoRootFix
}

// probeNote is the next step for a probe that did not answer: run it yourself, where its own
// error is visible, then check again.
func probeNote(argv ...string) string {
	return "Run `" + shquote.Join(argv) + "` yourself to see why it does not answer, " + recheck
}

// podmanInstallHints is internal/depcheck's per-manager hint for installing Podman on Linux.
//
// Source: userguide/getting-started.md, "Linux: Podman" — each value is the package list that
// guide's line for the manager installs (TestPodmanInstallHintsMatchTheGuide reads the guide).
// No "nix" entry: a nix-profile podman on another distribution lacks the setuid newuidmap that
// rootless podman needs, and NixOS takes a system option instead (runtimeInstallNote). No "brew"
// entry: on Linux the guide installs Podman from the distribution.
var podmanInstallHints = map[string]string{
	"apt":    "podman passt slirp4netns uidmap",
	"dnf":    "podman",
	"pacman": "podman",
}

// macPodmanMachineFlags are the resources the getting-started guide's `podman machine init`
// gives a new machine ("macOS: Podman"). The shares it adds are computed (podmanMachineInit).
const macPodmanMachineFlags = "--cpus 4 --memory 8192 --disk-size 50"

// lookup adapts the check's LookPath seam to the depcheck.Lookup every remedy is found through,
// so the manager a note names is one on the PATH this check searched.
func (o *Options) lookup() depcheck.Lookup {
	return func(bin string) (string, error) {
		if p, ok := o.LookPath(bin); ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
}

// runtimeInstallNote is the next step when no container runtime is installed: THIS host's
// command, never a menu across platforms. On Linux it is depcheck's install line for the
// manager the lookup finds; on a Mac it is the runtime the getting-started guide names for this
// Mac's chip and macOS version.
func (o *Options) runtimeInstallNote() string {
	if o.IsMacOS {
		return o.macRuntimeInstallNote()
	}
	res := depcheck.Check([]depcheck.Requirement{{Bin: "podman", Hints: podmanInstallHints}}, o.lookup())
	switch {
	case len(res) == 1 && res[0].Remedy != "":
		return "Install Podman, the container runtime yolo uses on Linux:\n  " + res[0].Remedy + "\n" + recheck
	case o.PathExists("/etc/NIXOS"):
		return "On NixOS, Podman is a system option. Add this line to your NixOS configuration\n" +
			"(/etc/nixos/configuration.nix by default):\n" +
			"  virtualisation.podman.enable = true;\n" +
			"then: sudo nixos-rebuild switch && yolo check"
	}
	return "None of apt, dnf or pacman is on this PATH, so yolo has no install line to name.\n" +
		"Install Podman with your distribution's command, listed at https://podman.io/docs/installation\n" +
		recheck
}

// macRuntimeInstallNote is runtimeInstallNote on a Mac. Source: userguide/getting-started.md,
// "Step 2: Install a container runtime" — Apple Container on Apple silicon running macOS 26 or
// later, Podman from Homebrew on an older Apple silicon Mac, Podman's 5.8 installer package on
// an Intel Mac (Podman 6 and Homebrew dropped those).
func (o *Options) macRuntimeInstallNote() string {
	machine := "  " + o.podmanMachineInit() + "\n  podman machine start\n" + recheck
	if o.Machine != "arm64" {
		return "Podman 6 no longer supports Intel Macs, and Homebrew has no Intel build of it.\n" +
			"Install podman-installer-macos-amd64.pkg from the newest 5.8 release at\n" +
			"https://github.com/containers/podman/releases, then create and start its machine:\n" + machine
	}
	major, ok := o.macOSMajorVersion()
	switch {
	case ok && major >= 26:
		return "Install Apple Container, the recommended runtime on this Mac, and answer Y when\n" +
			"`container system start` offers to install a kernel:\n" +
			"  brew install container\n  container system start\n" + recheck
	case ok:
		return "Apple Container needs macOS 26 or later, and this Mac runs macOS " + itoa(major) +
			", so install Podman\nand create its machine:\n  brew install podman\n" + machine
	}
	return "yolo could not read this Mac's version (`sw_vers -productVersion`). Apple Container\n" +
		"needs macOS 26 or later; on an older Mac, install Podman and create its machine:\n" +
		"  brew install podman\n" + machine
}

// podmanMachineInit is the `podman machine init` for a new machine: the guide's resources, and
// the shares a jail launched from here binds — Podman's own defaults written out, since a
// `-v` list replaces them, plus whatever of the workspace and yolo's own files they do not
// reach (machineShareSources, the folders checkPodmanMachineShares grades).
func (o *Options) podmanMachineInit() string {
	defaults := runtime.MachineShares{Shares: []runtime.MachineShare{
		{Source: "/Users", Target: "/Users"},
		{Source: "/private", Target: "/private"},
		{Source: "/var/folders", Target: "/var/folders"},
	}}
	cmd := defaults.MachineInitCommand(defaults.Unreachable(o.machineShareSources(), runtime.ResolveThroughExisting))
	return strings.Replace(cmd, "podman machine init", "podman machine init "+macPodmanMachineFlags, 1)
}

// configNote is the next step for a config finding: where to edit, and the re-check. A message
// config validation located already leads with the file and line it was written at
// (config.Sources.Annotate); an unlocated one names only the key, so the note names the files.
func configNote(msg, workspace string) string {
	where := "Fix it where this says it was written"
	if strings.HasPrefix(msg, "config.") {
		where = "Fix it in " + filepath.Join(workspace, "yolo-jail.jsonc") + " or " + paths.UserConfigPath()
	}
	return where + " (`yolo config-ref` documents every key), " + recheck
}

// lockfileNote is the next step for a lockfile LoadLock refused: a newer yolo's file wants a
// newer yolo, a corrupt one is regenerated, and one this user cannot read needs its mode fixed.
func lockfileNote(path string, err error) string {
	if os.IsPermission(err) {
		return "Make " + path + " readable by you (`ls -l " + shquote.Quote(path) + "` shows its owner), " + recheck
	}
	if data, rerr := os.ReadFile(path); rerr == nil {
		var head struct {
			Schema int `json:"schema"`
		}
		if json.Unmarshal(data, &head) == nil && head.Schema > packsrc.LockSchema {
			return "A newer yolo wrote it. `yolo update` installs the newer yolo, " + recheck
		}
	}
	return "Remove it and let yolo write it again:\n  rm " + shquote.Quote(path) + " && yolo pack install\n" + recheck
}

// entrypointPreflightNote follows a generator failure in the Entrypoint Dry-Run: the jail's boot
// runs the same generators and refuses on the same error (A12), so the fix is the config it
// names, or, when it names none, a yolo bug.
var entrypointPreflightNote = "A jail booting this config refuses on the same error. Fix the config key or pack it\n" +
	"names (`yolo config-ref` documents every key), " + recheck + "\n" +
	"If it names neither, " + yoloBugNote("it")

// runtimeFindingNote is the next step for Merged Configuration's runtime finding: the runtime the
// config or YOLO_RUNTIME selects is not usable here. The fix is the selection's own source.
func (o *Options) runtimeFindingNote(merged *jsonx.OrderedMap) string {
	name := o.configuredRuntimeName(merged)
	if name == "" {
		return "Container Runtime, above, names the command that installs or starts one, " + recheck
	}
	unselect := "change `runtime` in the config that sets it"
	if o.Getenv("YOLO_RUNTIME") == name {
		unselect = "unset YOLO_RUNTIME"
	}
	if inStrSlice(paths.NativeRuntimes, name) {
		return name + " runs only on a Mac, and the runtime on this host is podman: " + unselect +
			", " + recheck
	}
	return "Install or start " + name + ", or " + unselect + " to use a runtime that answers, " + recheck
}

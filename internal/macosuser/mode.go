package macosuser

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// Mode is which user a macos-user launch runs the sandbox as (docs/design/macos-user-model.md
// §7, MMP-D2 in docs/design/macos-user-model-plan.md). The profile is the same in both; only
// the crossing into it differs.
type Mode int

const (
	// ModeAccount runs the sandbox as the second account, SandboxUser, through the one sudo rule
	// `yolo yolo-account create` installs. It is the zero value, so every plan built without a mode is
	// today's plan.
	ModeAccount Mode = iota
	// ModeSelf runs the sandbox as the invoking user: no account, no sudo.
	ModeSelf
)

// String is the mode's config spelling ("account" or "self").
func (m Mode) String() string {
	switch m {
	case ModeAccount:
		return "account"
	case ModeSelf:
		return "self"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// ModeFor is the mode a setup's run_as means (named-setups-plan.md SUP-D1): self runs the
// sandbox as you, and every other value as the yolo account.
func ModeFor(r setup.RunAs) Mode {
	if r == setup.RunAsSelf {
		return ModeSelf
	}
	return ModeAccount
}

// Homes are the homes one launch's sandbox runs with (MMP-D5 to MMP-D7 in
// docs/design/macos-user-model-plan.md):
//
//   - Home is HOME;
//   - Machine holds the machine tier — `.cache`, `Library/Caches` and the mise store
//     (SandboxMiseData(Machine));
//   - Shared is where `scope: machine` pack dirs live;
//   - Real is the invoking user's real home when it is NOT Home, or "" when HOME is already the
//     home the guest should judge workspace scope by (paths.DarwinRealHomeEnv).
type Homes struct {
	Home    string
	Machine string
	Shared  string
	Real    string
}

// LaunchHomes derives a launch's Homes from its mode, its workspace and the invoking user's
// resolved home. In BOTH modes HOME is <workspace>/.yolo/home itself (MMP-D5; MMP-D25 for the
// account mode), so two workspaces launch at once with no HOME in common.
//
// ModeAccount (slice 12a, MMP-D25): Machine, Shared and Real are the account's own home,
// SandboxHome(). The mise store, `.cache`, `Library/Caches` and the `scope: machine` dirs stay
// where account-mode launches have always kept them, shared by every workspace with no lock
// (MMP-D27), and the account's real home is the boundary its guest judges scope by. hostHome is
// not read.
//
// ModeSelf is the run-as-you geometry: Machine is the darwin-only machine root and Shared the
// store podman binds credentials from, both under hostHome's state dir (MMP-D6) — never the
// shared cache or mise store, which hold Linux files on a Mac that also runs podman; Real is
// hostHome (MMP-D7).
func LaunchHomes(mode Mode, workspace, hostHome string) Homes {
	if mode != ModeSelf {
		account := SandboxHome()
		return Homes{
			Home:    paths.WorkspaceHomeState(workspace),
			Machine: account,
			Shared:  account,
			Real:    account,
		}
	}
	return Homes{
		Home:    paths.WorkspaceHomeState(workspace),
		Machine: paths.MacosUserMachineRootUnder(hostHome),
		Shared:  paths.GlobalHomeUnder(hostHome),
		Real:    hostHome,
	}
}

// accountHomes is one home in every role but Real: what a builder handed a bare `home` (empty
// meaning SandboxHome()) renders with when its crossing carries no launch Homes — an install
// capture's staging home, and the `yolo macos-*` commands' probes.
func accountHomes(home string) Homes {
	if home == "" {
		home = SandboxHome()
	}
	return Homes{Home: home, Machine: home, Shared: home}
}

// RunAs is THE ONE CROSSING into the macos-user sandbox (MMP-D4). Every argv that starts a
// process as the sandbox's user is built by RunAs.Argv, so one value per launch decides whether
// that user is the yolo account or you, and no consumer can be left crossing into an account
// self mode does not have. TestEveryCrossingGoesThroughRunAs fails a file that builds the sudo
// crossing any other way.
type RunAs struct {
	Mode Mode
	// Homes are the homes LaunchHomes derived for the launch, which every argv builder renders in
	// BOTH modes (sandboxEnvPairs, MMP-D25); the zero value renders the bare home a builder was
	// handed. User is the invoking user's name, read only in self mode: the account mode's USER
	// is SandboxUser.
	Homes Homes
	User  string
}

// homes is the Homes an argv builder renders with: the launch's when the crossing carries them,
// in either mode, else one home, home ("" meaning SandboxHome()), in every role.
func (r RunAs) homes(home string) Homes {
	if r.Homes.Home != "" {
		return r.Homes
	}
	return accountHomes(home)
}

// user is the USER an argv builder renders: the invoking user in self mode, else user, which ""
// makes SandboxUser.
func (r RunAs) user(user string) string {
	if r.Mode == ModeSelf && r.User != "" {
		return r.User
	}
	if user == "" {
		return SandboxUser
	}
	return user
}

// Argv prefixes argv with the crossing. Account mode is RunAsSandboxArgv byte for byte —
// `/usr/bin/sudo -n [--set-home] --user=_yolojail /usr/bin/env <argv…>` — with setHome adding
// sudo's `--set-home`. Self mode is `/usr/bin/env <argv…>`: the same command the rule names, so
// every argv keeps its shape after the crossing, and no sudo; setHome means nothing there, since
// no user changes.
func (r RunAs) Argv(setHome bool, argv ...string) []string {
	if r.Mode == ModeSelf {
		return append([]string{envBin}, argv...)
	}
	var flags []string
	if setHome {
		flags = []string{"--set-home"}
	}
	return RunAsSandboxArgv(flags, argv...)
}

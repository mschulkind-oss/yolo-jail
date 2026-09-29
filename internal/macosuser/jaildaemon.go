package macosuser

// jaildaemon.go is the GUEST HALF of the loophole lifecycle on this backend: the in-jail
// binaries a declared `jail_daemon.cmd` names, staged into the sandbox's own prefix, and the
// one supervisor that runs the declared daemons inside the Seatbelt profile
// (docs/design/jail-daemon-on-macos-user-plan.md steps 3 and 4).
//
// # The two rulings this builds (docs/design/declaration-parity.md, 2026-09-28)
//
// The maintainer's principle, which both follow from: "if you would have run it on the host
// before, it runs on the host. If you would have run it in the jail container, you run it on
// the guest."
//
//   - OQ-DP8 — the in-jail binaries are built for darwin into the flake bundle as
//     `bin/darwin-<arch>` (flake.nix guestBinaries, scripts/stage-source-bundle.sh
//     GUEST_BINARIES) and staged here into GuestBinDir, beside the `yolo` this backend already
//     stages for its bootstrap. That directory is the guest's counterpart of a container's
//     /opt/yolo-jail/bin, and it is on no host PATH, so the host ship set stays {yolo}. A
//     declared `cmd` runs exactly as declared: the supervisor resolves `yolo-jaild` on the
//     sandbox PATH (SandboxPath carries GuestBinDir), with no argv[0] rewrite and no
//     generated shim.
//   - OQ-DP9 — the daemons run CONFINED: the supervisor's argv is sudo → env -i →
//     sandbox-exec -f <the session profile> → the env-file reader → `yolo-jaild supervise`,
//     the same four layers the agent and the provisioning stage run under, as the sandbox
//     account. Callers authenticate with the launch's caller tokens (NC-D2), which reach the
//     supervisor in a root-owned 0600 file only that account is granted read on — this
//     backend has no network isolation, so the file's ownership and ACE are the boundary.
//
// # What a guest does NOT run
//
// The launch decides which of the composed daemons run here (internal/loopholes'
// JailDaemonsRunIn); this package receives only the ones that do, as the payload inside
// JailDaemons.Env. It never classifies, so the prediction `yolo check` makes and the payload
// this runs cannot be two different selections.

import (
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

// guestBinLeaf is the state-dir subdir holding the guest's binaries — the sandbox's
// counterpart of the container's /opt/yolo-jail/bin.
const guestBinLeaf = "bin"

// jailDaemonsEnv is the supervisor's input: the YOLO_JAIL_DAEMONS payload, one JSON list
// internal/supervisor.ParseEnv reads (internal/loopholes.JailDaemonPayload is its one writer).
const jailDaemonsEnv = "YOLO_JAIL_DAEMONS"

// JaildName is the in-jail daemon dispatcher: the supervisor and every in-jail daemon.
const JaildName = "yolo-jaild"

// GuestBinaries is the guest's darwin in-jail set — only what a guest actually RUNS. It is a
// subset of flake.nix's shippedBinaries and one of three spellings of the same list
// (flake.nix guestBinaries, stage-source-bundle.sh GUEST_BINARIES), pinned together by
// guestbundle_test.go.
//
// yolo-jaild is the whole of it. `yolo` is staged separately (StageBinaryCommands, from the
// running host binary, which is already darwin); yolo-entrypoint is not needed (the guest
// bootstrap is `yolo internal darwin-bootstrap`); and yolo-ps, yolo-cglimit, yolo-journalctl
// and yolo-serial are clients of Linux-only loopholes (internal/cli/run's backendlimits.go).
var GuestBinaries = []string{JaildName}

// GuestBinDir is the root-owned directory holding every binary the sandbox runs of yolo's
// own: <stateDir>/bin. StagedYoloPath is in it too, so the one PATH entry SandboxPath derives
// from StagedYoloPath resolves both `yolo` and `yolo-jaild`.
func GuestBinDir(sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, guestBinLeaf)
}

// GuestBinaryPath is where one guest binary is staged.
func GuestBinaryPath(name, sd string) string { return filepath.Join(GuestBinDir(sd), name) }

// PrebuiltGuestBinDir is the per-arch guest directory a flake bundle ships, if the resolved
// flake source has one: <root>/bin/darwin-<GOARCH>. The same bundle-format spelling as
// flake.nix's guestPrebuiltDir and stage-source-bundle.sh's layout.
//
// goruntime.GOARCH rather than a probe: this is the binary the running host yolo stages for a
// sandbox on the same Mac, so the guest's arch is this process's.
func PrebuiltGuestBinDir(root string) string {
	return filepath.Join(root, "bin", "darwin-"+goruntime.GOARCH)
}

// StageGuestBinaryCommands returns the sudo argv that stage each GuestBinaries member from
// srcDir into GuestBinDir, the StageBinaryCommands shape per binary: copy to a temp name,
// `a+rX`, then an atomic rename — a FRESH INODE, because macOS caches Mach-O code signatures
// per vnode and an in-place overwrite gets the next exec SIGKILLed. Root-owned, so the
// sandbox can execute and cannot rewrite what its supervisor runs. nil for an empty srcDir,
// which is a launch with no jail daemon to run.
func StageGuestBinaryCommands(srcDir, sd string) [][]string {
	if srcDir == "" {
		return nil
	}
	cmds := [][]string{{mkdirBin, "-p", GuestBinDir(sd)}}
	for _, name := range GuestBinaries {
		dst := GuestBinaryPath(name, sd)
		tmp := dst + ".new"
		cmds = append(cmds,
			[]string{cpBin, "-f", filepath.Join(srcDir, name), tmp},
			[]string{chmodBin, "a+rX", tmp},
			[]string{mvBin, "-f", tmp, dst},
		)
	}
	return cmds
}

// JailDaemons is what the run pipeline hands this backend to run in the guest.
//
// The zero value runs nothing, which is every launch whose payload names no daemon the guest
// runs — so `yolo -- bash` in a workspace with no loophole pays nothing: no binary staged, no
// file written, no supervisor.
type JailDaemons struct {
	// Env is the supervisor's composed environment, rendered into the daemon env file: the
	// YOLO_JAIL_DAEMONS payload, every caller token those daemons demand (a SCOPED one
	// included — the daemon is its consumer, and this file is read by no agent), the
	// endpoint file each dials, and the shared channel values. Composed by the run pipeline
	// (internal/cli/run's jailDaemonEnv), never here.
	Env *jsonx.OrderedMap
	// GuestBinSource is the directory the darwin guest binaries are copied from: a bundle's
	// bin/darwin-<arch>, or a `.#guestPrefix` build's bin. The orchestrator resolves it
	// (Deps.GuestBinaries) before the plan; a plan render names the prebuilt spelling.
	GuestBinSource string
}

// Names is the daemons the payload names, sorted — what the supervisor will start.
func (j JailDaemons) Names() []string {
	if j.Env == nil {
		return nil
	}
	v, _ := j.Env.Get(jailDaemonsEnv)
	var out []string
	for _, s := range supervisor.ParseEnv(asStr(v)) {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

// SandboxDaemonEnvFile is the supervisor's env file: <stateDir>/env/<cname>.daemons.env.
//
// A SECOND FILE rather than the agent's session file, for the credential gate's reason: the
// agent's file carries what THAT program may see, and a scoped caller token is exported only
// to the agents whose profile selects its daemon (provider-credential-scope.md OQ-CN7 (c)).
// The supervisor needs every token its daemons demand, so it reads a file no agent is handed.
// Beside the session file, in the same 0700 directory with the same search ACE, and read-
// granted to the same one account.
func SandboxDaemonEnvFile(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	if cname == "" {
		return ""
	}
	return sd + "/" + sandboxEnvLeaf + "/" + cname + ".daemons.env"
}

// JailDaemonArgv builds the supervisor's argv: `sudo -n --set-home --user=<sb> /usr/bin/env -i
// <the closed identity list> /usr/bin/sandbox-exec -f <profile> -- <env-file reader>
// <GuestBinDir>/yolo-jaild supervise`.
//
// LaunchArgv's shape, deliberately — the plan's Reuse note: sandboxEnvPairs for the closed
// `env -i` list (so no composed value is on this argv either), ExecWithEnvFile for the
// composed environment, and the same sandbox-exec profile the agent runs under. What differs
// is `-n`: this starts in the background beside the agent's terminal, so a sudo whose
// credential cache had expired must FAIL rather than prompt on a tty the agent is about to
// own. The staging steps a moment earlier ran sudo, so the cache is warm on every launch
// that gets here.
//
// The supervisor is named by ABSOLUTE path, the one word of this argv yolo decides; the
// daemons it starts are the payload's argvs verbatim, resolved on the PATH this argv sets.
func JailDaemonArgv(profilePath, envFile, user, home string, pathPrefix []string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	out := []string{
		"sudo",
		"-n",
		"--set-home",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
	}
	out = append(out, sandboxEnvPairs(home, user, SandboxPath(home, pathPrefix), envFile)...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	out = append(out, ExecWithEnvFile(envFile, []string{GuestBinaryPath(JaildName, ""), "supervise"})...)
	return out
}

// daemonEnvPlan makes the daemon env file installable through installSandboxEnvFile, the one
// installer the session file and the capture's file already share: 0700 directory first,
// content on stdin through `sudo tee`, the sandbox's read ACE after.
type daemonEnvPlan struct {
	path, content string
	dir, grant    [][]string
}

func (d daemonEnvPlan) envFile() (string, string)                 { return d.path, d.content }
func (d daemonEnvPlan) envFileCommands() ([][]string, [][]string) { return d.dir, d.grant }

// daemonEnvFilePlan is the RunPlan's daemon env file as an installable plan. The directory
// commands are included only when the session file did not already prepare the same
// directory, so a launch with both never adds the search ACE twice.
func (p RunPlan) daemonEnvFilePlan() daemonEnvPlan {
	dir := SandboxEnvDirCommands(p.DaemonEnvFile, "")
	if p.EnvFile != "" && pathParent(p.EnvFile) == pathParent(p.DaemonEnvFile) {
		dir = nil
	}
	return daemonEnvPlan{
		path:    p.DaemonEnvFile,
		content: p.DaemonEnvFileContent,
		dir:     dir,
		grant:   SandboxEnvGrantCommands(p.DaemonEnvFile, ""),
	}
}

// startJailDaemons writes the daemon env file and starts the supervisor, returning the stop
// that ends it and sweeps the file. Every failure REFUSES the launch (ok false), for
// RunMacosUser's reason: the served set already pointed this launch's agents at these
// daemons' addresses.
func startJailDaemons(deps Deps, out printer, plan RunPlan) (func(), bool) {
	if deps.StartBackground == nil {
		out.print("[bold red]This build cannot start the sandbox's jail daemons[/bold red] " +
			"(no background launcher is wired).")
		return nil, false
	}
	if !installSandboxEnvFile(deps, out, plan.daemonEnvFilePlan()) {
		return nil, false
	}
	sweep := func() {
		for _, cmd := range plan.DaemonEnvRemoveCommands {
			_ = deps.Run(append([]string{"sudo"}, cmd...))
		}
	}
	stop, err := deps.StartBackground(plan.JailDaemonArgv)
	if err != nil || stop == nil {
		sweep()
		out.printf("[bold red]Could not start the sandbox's jail daemons (%s):[/bold red] %s",
			strings.Join(plan.JailDaemonNames, ", "), errStr(err))
		return nil, false
	}
	// A DISCLOSURE, not progress: pack-declared code is about to run for the whole session,
	// so it is said on every launch (report-tiers.md, no quiet mode).
	out.printf("Started %s inside the sandbox (confined by its Seatbelt profile, as %s) "+
		"under %s supervise, until the command exits. Logs: ~/.local/state/yolo-jail-daemons.",
		strings.Join(plan.JailDaemonNames, ", "), SandboxUser, JaildName)
	return func() {
		stop()
		sweep()
	}, true
}

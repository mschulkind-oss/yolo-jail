package macosuser

// jaildaemon.go is the GUEST HALF of the loophole lifecycle on this backend: the in-jail
// binaries a declared `jail_daemon.cmd` names, staged into the sandbox's own prefix, and the
// one supervisor that runs the declared daemons inside the Seatbelt profile
// (docs/reference/macos-user-nix-and-features.md, "The jail daemons run in the sandbox").
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
// # The loophole clients the agent runs (the same ruling, one step over)
//
// OQ-DP8's principle covers the CLIENTS too: `yolo-serial` and `yolo-ps` would have run in the
// jail container, so they run on the guest. They are staged into GuestBinDir with yolo-jaild,
// as one set (GuestBinaries), whenever the session env carries an endpoint one of them reads
// (GuestClients), and they resolve on the agent's PATH, which SandboxPath gives GuestBinDir.
// A launch with neither a daemon nor such an endpoint stages none of it, so a checkout launch
// never builds `.#guestPrefix` for nothing.
//
// # What a guest does NOT run
//
// The launch decides which of the composed daemons run here (internal/loopholes'
// JailDaemonsRunIn); this package receives only the ones that do, as the payload inside
// JailDaemons.Env. It never classifies, so the prediction `yolo check` makes and the payload
// this runs cannot be two different selections.

import (
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// GuestClient is one in-jail loophole CLIENT the guest runs: the binary, the loophole whose
// host daemon it dials, and the endpoint variable it reads that daemon's endpoint file from.
//
// The variable is the CLIENT'S OWN CONTRACT (paths.SerialEndpointEnv is what cmd/yolo-serial
// reads, paths.HostProcessesEndpointEnv what cmd/yolo-ps reads), and it is the key the launch
// stages the guest set on: a session env carrying it is a session whose agent may run the
// client. Keying on the variable rather than on the config means the launch stages a client
// exactly when its host daemon published, which is the only time the client has anything to
// dial (the run pipeline sets an endpoint variable only for a service that started).
type GuestClient struct {
	Binary      string
	Loophole    string
	EndpointEnv string
}

// GuestClients are the in-jail loophole clients a guest stages, one per loophole that runs on a
// Mac. yolo-cglimit and yolo-journalctl are not here: their loopholes declare
// `platforms: ["linux"]`, so no macos-user launch publishes an endpoint either could dial
// (internal/cli/run's backendlimits.go tells the agent so).
var GuestClients = []GuestClient{
	{Binary: "yolo-serial", Loophole: "serial", EndpointEnv: paths.SerialEndpointEnv},
	{Binary: "yolo-ps", Loophole: "host-processes", EndpointEnv: paths.HostProcessesEndpointEnv},
}

// GuestBinaries is the guest's darwin in-jail set — only what a guest actually RUNS. It is a
// subset of flake.nix's shippedBinaries and one of three spellings of the same list
// (flake.nix guestBinaries, stage-source-bundle.sh GUEST_BINARIES), pinned together by
// guestbundle_test.go.
//
// yolo-jaild, the supervisor and every in-jail daemon, then each GuestClients binary. `yolo` is
// staged separately (StageBinaryCommands, from the running host binary, which is already
// darwin), and yolo-entrypoint is not needed (the guest bootstrap is `yolo internal
// darwin-bootstrap`).
//
// ONE SET, STAGED WHOLE. A launch that needs any of it stages all of it (StageGuestBinaryCommands),
// because the members come from one source directory and cost one copy each; splitting the
// staging per member would buy a few kilobytes of copy for a second selector that could
// disagree with the first.
var GuestBinaries = func() []string {
	out := []string{JaildName}
	for _, c := range GuestClients {
		out = append(out, c.Binary)
	}
	return out
}()

// GuestClientsIn returns the GuestClients whose endpoint variable one of envs carries with a
// non-empty value, in GuestClients' order: the clients this launch's sandbox can use. A nil env
// carries nothing.
func GuestClientsIn(envs ...*jsonx.OrderedMap) []GuestClient {
	var out []GuestClient
	for _, c := range GuestClients {
		for _, env := range envs {
			if env == nil {
				continue
			}
			if v, ok := env.Get(c.EndpointEnv); ok && asStr(v) != "" {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// guestClientNames is the clients' binaries, for a plan field and a message.
func guestClientNames(clients []GuestClient) []string {
	var out []string
	for _, c := range clients {
		out = append(out, c.Binary)
	}
	return out
}

// guestClientPhrase names each client with its loophole, for a refusal or a disclosure:
// "yolo-serial (the serial loophole's client)".
func guestClientPhrase(clients []GuestClient) string {
	parts := make([]string, 0, len(clients))
	for _, c := range clients {
		parts = append(parts, c.Binary+" (the "+c.Loophole+" loophole's client)")
	}
	return strings.Join(parts, ", ")
}

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
// sandbox can execute and cannot rewrite what its supervisor or its agent runs. nil for an
// empty srcDir, which is a launch with no jail daemon to run and no guest client's endpoint.
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

// guestClientInvariants is PlanInvariants' rule for the guest clients: every GuestClients
// entry whose endpoint the session env file carries is staged, copy-then-rename, into
// GuestBinDir, and the agent's PATH carries that directory. Either half missing is a sandbox
// told where its service is with no program to dial it, which looks healthy until the agent
// types `yolo-serial` and gets `command not found`.
func guestClientInvariants(plan RunPlan) []string {
	var problems []string
	for _, c := range GuestClients {
		if v, ok := sandboxEnvFileValue(plan.EnvFileContent, c.EndpointEnv); !ok || v == "" {
			continue
		}
		dst := GuestBinaryPath(c.Binary, plan.StagedDir)
		staged := false
		for _, cmd := range plan.StageCommands {
			if len(cmd) == 4 && cmd[0] == mvBin && cmd[2] == dst+".new" && cmd[3] == dst {
				staged = true
			}
		}
		if !staged {
			problems = append(problems, "the session env carries "+c.EndpointEnv+" and the plan "+
				"stages no "+c.Binary+" into "+GuestBinDir(plan.StagedDir)+"; the agent would be "+
				"told where the "+c.Loophole+" loophole's daemon is and have no client to dial it")
		}
		if len(plan.LaunchArgv) > 0 {
			onPath := false
			if v, ok := argvEnvValue(plan.LaunchArgv, "PATH"); ok {
				for _, dir := range strings.Split(v, ":") {
					onPath = onPath || dir == GuestBinDir(plan.StagedDir)
				}
			}
			if !onPath {
				problems = append(problems, "the agent's PATH does not carry "+
					GuestBinDir(plan.StagedDir)+", so a staged "+c.Binary+" would not resolve")
			}
		}
	}
	return problems
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
	//
	// It is the source of the WHOLE guest set, the GuestClients included, so it is filled for a
	// launch that runs no daemon and carries a client's endpoint too, and the plan stages from
	// it when either is true. It stays on this struct, beside the payload it began with, because
	// both triggers are one directory resolved once per launch (resolveGuestBinSource).
	GuestBinSource string
	// OnLaunch, when set, runs once, immediately before the session's command starts: after every
	// step that can refuse the launch (the preconditions, the nix builds, the bootstrap, the
	// provisioning stage, this supervisor's start and the host-service witness) has passed and
	// the workspace lock is released. What it returns runs once the command has exited, before
	// the supervisor stops; nil returns nothing to run. Never on a dry run or a refusal. It must
	// return promptly: the session waits for it.
	//
	// The run pipeline's port relays open here (internal/cli/run's macosuserportrelay.go), so a
	// launch refused at any step, or still building its tools, publishes no port. It rides on
	// this value, which is about the guest's daemons, only because the value already crosses the
	// run pipeline's MacosUserRun seam whole, so the seam's signature does not change.
	OnLaunch func() (stop func())
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

// SandboxDaemonEnvFile is the supervisor's env file: <stateDir>/env/<key>.daemons.env, where key
// is the session's (SessionKey: <cname>.<session id>), so two sessions of one workspace never
// write, read or remove one file (sessionfiles.go).
//
// A SECOND FILE rather than the agent's session file, for the credential gate's reason: the
// agent's file carries what THAT program may see, and a scoped caller token is exported only
// to the agents whose profile selects its daemon (providers.md OQ-CN7 (c)).
// The supervisor needs every token its daemons demand, so it reads a file no agent is handed.
// Beside the session file, in the same 0700 directory with the same search ACE, and read-
// granted to the same one account.
func SandboxDaemonEnvFile(key, sd string) string {
	return sessionEnvDirFile(key, sd, ".daemons.env")
}

// SupervisorLogName is the supervisor's own log, beside the daemons' <name>.log files.
const SupervisorLogName = "supervisor.log"

// SupervisorLogPath is where the guest's supervisor sends its own stdout and stderr, as the
// HOST sees it: <workspace>/.yolo/home/local/state/yolo-jail-daemons/supervisor.log. That is
// the directory the daemons' own logs land in (supervisor.LogDir, ~/.local/state/
// yolo-jail-daemons in the sandbox, whose ~/.local is the workspace overlay's `local` subtree —
// paths.HomeSurfaces), spelled here from the workspace rather than through the sandbox home's
// symlink so the file the guest writes and the file the launch reads are one path.
//
// Readable by the user on the ACL model the daemons' logs already use: the guest creates it as
// the sandbox account, inside the workspace, whose inheriting `group:_yolojail` ACEs
// (WorkspaceACLAces) are applied to whatever is created there whoever creates it — and the host
// user is a member of that group. Nothing here grants anything.
func SupervisorLogPath(workspace string) string {
	return filepath.Join(paths.WorkspaceHomeState(workspace), "local", "state", "yolo-jail-daemons",
		SupervisorLogName)
}

// supervisorLogWrapper is the shell body that sends everything after it to the supervisor's
// log: make the log's directory, append stdout and stderr to $1, then exec the rest. Placed
// INSIDE sandbox-exec and in front of the env-file reader, so it runs confined, as the sandbox
// account, and the log catches the reader's failure, an exec failure and every line the
// supervisor writes (its readiness line included). What fails BEFORE it — sudo's own refusal,
// sandbox-exec's — is on the launcher's side of the pipe instead (Background.Output).
//
// APPEND, never truncate: the per-workspace launch lock is held across the supervisor's start,
// so the launch reads the lines THIS start added from the size it saw before starting; and a
// truncating open would put holes in a still-running earlier session's file. It grows by about
// one line per launch, so it is not rotated.
const supervisorLogWrapper = `/bin/mkdir -p "${1%/*}" && exec >>"$1" 2>&1 || exit 1; shift; exec "$@"`

// supervisorLogWrapperName is $0 for the wrapper, what a `ps` line and a dry run show.
const supervisorLogWrapperName = "yolo-supervisor-log"

// WithSupervisorLog wraps argv so its stdout and stderr go to logPath (supervisorLogWrapper).
func WithSupervisorLog(logPath string, argv []string) []string {
	if logPath == "" || len(argv) == 0 {
		return argv
	}
	return append([]string{sandboxEnvShell, "-c", supervisorLogWrapper, supervisorLogWrapperName, logPath}, argv...)
}

// JailDaemonArgv builds the supervisor's argv: `sudo -n --set-home --user=<sb> /usr/bin/env -i
// <the closed identity list> /usr/bin/sandbox-exec -f <profile> -- <the log wrapper> <logPath>
// <env-file reader> <GuestBinDir>/yolo-jaild supervise`.
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
func JailDaemonArgv(profilePath, envFile, logPath, user, home string, pathPrefix []string) []string {
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
	out = append(out, WithSupervisorLog(logPath,
		ExecWithEnvFile(envFile, []string{GuestBinaryPath(JaildName, ""), "supervise"}))...)
	return out
}

// sessionFilePlan is one more root-owned file a session writes beside its env file, installable
// through installSandboxEnvFile, the one installer the session file and the capture's file
// already share: 0700 directory first, content on stdin through `sudo tee`, the sandbox's read
// ACE after. The daemon env file is one, and the CA files are the others (cabundle.go).
type sessionFilePlan struct {
	path, content string
	dir, grant    [][]string
}

func (d sessionFilePlan) envFile() (string, string)                 { return d.path, d.content }
func (d sessionFilePlan) envFileCommands() ([][]string, [][]string) { return d.dir, d.grant }

// daemonEnvFilePlan is the RunPlan's daemon env file as an installable plan. The directory
// commands are included only when the session file did not already prepare the same
// directory, so a launch with both never adds the search ACE twice.
func (p RunPlan) daemonEnvFilePlan() sessionFilePlan {
	dir := SandboxEnvDirCommands(p.DaemonEnvFile, "")
	if p.EnvFile != "" && pathParent(p.EnvFile) == pathParent(p.DaemonEnvFile) {
		dir = nil
	}
	return sessionFilePlan{
		path:    p.DaemonEnvFile,
		content: p.DaemonEnvFileContent,
		dir:     dir,
		grant:   SandboxEnvGrantCommands(p.DaemonEnvFile, ""),
	}
}

// Background is one process Deps.StartBackground started.
type Background struct {
	// Stop ends it (idempotent).
	Stop func()
	// Exited is closed once the process has exited. nil means the starter cannot tell, which
	// reads as "still running".
	Exited <-chan struct{}
	// Output is what the process wrote on its OWN stdout and stderr, bounded: for the
	// supervisor, only what happened before supervisorLogWrapper took them over — sudo's
	// refusal, sandbox-exec's. nil means nothing was captured.
	Output func() string
}

// supervisorReadyBound is the longest the launch waits for the supervisor's readiness line
// before it stops waiting. The wait ends the moment the line appears or the process exits,
// so this costs time only when the supervisor is still alive and has not spoken — a slow or
// hung start — and never on the stop, which is untouched.
//
// WHY 1.5 s. The chain in front of the line is six execs, none of which does I/O worth the
// name: a sudo whose credential cache the staging steps warmed a moment earlier, env,
// sandbox-exec compiling a profile of a few dozen rules, two /bin/sh wrappers, and a Go binary
// that parses one env var and writes the line before it starts anything. Every failure it is
// meant to catch (a sudo -n refusal, a sandbox-exec denial, an exec or env-file failure) EXITS,
// in milliseconds, and ends the wait through Exited rather than this bound. So the bound is
// only the ceiling on a live-but-silent start, set well above that chain's cost (hundreds of
// milliseconds at worst — each exec of the freshly staged binary is a new inode, so its code
// signature is checked afresh) and low enough that a slow Mac costs a launch under two seconds
// rather than a refusal. How long the chain really takes is a Mac measurement
// (UNMEASURED in macos-user-nix-and-features.md's jail-daemon section; JD-8).
var supervisorReadyBound = 1500 * time.Millisecond

// supervisorReadyPoll is how often the wait re-reads the log.
const supervisorReadyPoll = 20 * time.Millisecond

// supervisorLogTail is how many of the log's last lines a failure disclosure quotes.
const supervisorLogTail = 10

type supervisorOutcome int

const (
	supervisorStarted supervisorOutcome = iota
	supervisorExited
	supervisorUnconfirmed
)

// awaitSupervisor waits, bounded by supervisorReadyBound, for the supervisor's readiness line
// (supervisor.StartedLinePrefix) to appear in the part of its log past offset, or for the
// process to exit. It returns the outcome and that fresh part of the log.
//
// EXITED IS CHECKED FIRST: a supervisor that wrote its line and died at once is not running,
// and "Started" would be the claim this function exists not to make.
func awaitSupervisor(deps Deps, bg Background, logPath string, offset int) (supervisorOutcome, string) {
	deadline := time.Now().Add(supervisorReadyBound)
	for {
		select {
		case <-bg.Exited:
			return supervisorExited, freshSupervisorLog(deps, logPath, offset)
		default:
		}
		fresh := freshSupervisorLog(deps, logPath, offset)
		for _, line := range strings.Split(fresh, "\n") {
			if strings.HasPrefix(line, supervisor.StartedLinePrefix) {
				return supervisorStarted, fresh
			}
		}
		if !time.Now().Before(deadline) {
			return supervisorUnconfirmed, fresh
		}
		select {
		case <-bg.Exited:
		case <-time.After(supervisorReadyPoll):
		}
	}
}

// freshSupervisorLog is the log's content past offset: what THIS start wrote. A file shorter
// than offset was replaced, and is read whole.
func freshSupervisorLog(deps Deps, logPath string, offset int) string {
	if deps.ReadFile == nil || logPath == "" {
		return ""
	}
	content, _ := deps.ReadFile(logPath)
	if len(content) < offset {
		return content
	}
	return content[offset:]
}

// quoteTail renders the last n non-empty lines of s, indented, or "" when there are none.
func quoteTail(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("\n    " + l)
	}
	return b.String()
}

// startJailDaemons writes the daemon env file and starts the supervisor, returning the stop
// that ends it and sweeps the file. Every failure REFUSES the launch (ok false), for
// RunMacosUser's reason: the served set already pointed this launch's agents at these
// daemons' addresses. Every sweep of the file goes through the session's teardown, so one that
// fails keeps the session's record for the next launch's sweep (sessionfiles.go).
//
// "Started" is printed only once the supervisor's readiness line is in its log
// (awaitSupervisor, JD-8). A supervisor that exits first is a refusal naming the log and its
// last lines; one still running but silent past the bound is said to be unconfirmed, and the
// launch goes on — a timer of ours is no reason to refuse a Mac that is merely slow.
func startJailDaemons(deps Deps, out printer, plan RunPlan, teardown *sessionTeardown) (func(), bool) {
	if deps.StartBackground == nil {
		out.print("[bold red]This build cannot start the sandbox's jail daemons[/bold red] " +
			"(no background launcher is wired).")
		return nil, false
	}
	// The sweep is defined BEFORE the install and runs on its failure too, as the env file's and
	// the profile's removals are deferred before theirs: an install that failed half-way may have
	// written the caller tokens (the tee ran, the chmod or the read ACE after it failed), and the
	// file's per-session name means no later launch rewrites it. Through the teardown, so a removal
	// that fails keeps the session's record for the next launch's sweep.
	sweep := func() { teardown.remove(plan.DaemonEnvRemoveCommands) }
	if !installSandboxEnvFile(deps, out, plan.daemonEnvFilePlan()) {
		sweep()
		return nil, false
	}
	names := strings.Join(plan.JailDaemonNames, ", ")
	logPath := plan.SupervisorLog
	// The size before the start, so the wait reads only what this start adds. Exact because the
	// per-workspace launch lock is held across this call: no other launch of this workspace is
	// starting a supervisor meanwhile.
	offset := 0
	if deps.ReadFile != nil && logPath != "" {
		before, _ := deps.ReadFile(logPath)
		offset = len(before)
	}
	bg, err := deps.StartBackground(plan.JailDaemonArgv)
	if err != nil || bg.Stop == nil {
		sweep()
		out.printf("[bold red]Could not start the sandbox's jail daemons (%s):[/bold red] %s",
			names, errStr(err))
		return nil, false
	}
	outcome, fresh := awaitSupervisor(deps, bg, logPath, offset)
	launcherSaw := ""
	if bg.Output != nil {
		launcherSaw = quoteTail(bg.Output(), supervisorLogTail)
	}
	switch outcome {
	case supervisorExited:
		bg.Stop()
		sweep()
		logLines := quoteTail(fresh, supervisorLogTail)
		if logLines == "" {
			logLines = "\n    (nothing — the supervisor never reached its log)"
		}
		msg := fmt.Sprintf("[bold red]The sandbox's jail-daemon supervisor exited before it started "+
			"%s.[/bold red] Its log, %s, gained these lines:%s", names, logPath, logLines)
		if launcherSaw != "" {
			msg += "\n  Before that log took over, sudo and sandbox-exec printed:" + launcherSaw
		}
		out.print(msg + "\n  The launch stops here: its agents were already pointed at these " +
			"daemons' addresses.")
		return nil, false
	case supervisorUnconfirmed:
		out.printf("[yellow]The sandbox's jail-daemon supervisor for %s is running but has not said "+
			"it is supervising after %s.[/yellow] The launch continues without that confirmation; "+
			"if a daemon's client fails, read %s and the daemons' logs beside it.",
			names, supervisorReadyBound, logPath)
	default:
		// A DISCLOSURE, not progress: pack-declared code is about to run for the whole session,
		// so it is said on every launch (report-tiers.md, no quiet mode).
		out.printf("Started %s inside the sandbox (confined by its Seatbelt profile, as %s) "+
			"under %s supervise, until the command exits. Logs: %s "+
			"(~/.local/state/yolo-jail-daemons in the sandbox).",
			names, SandboxUser, JaildName, pathParent(logPath))
	}
	return func() {
		bg.Stop()
		sweep()
	}, true
}

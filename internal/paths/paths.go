// Package paths provides the module-level constants used across the CLI.
// Socket names especially are cross-image contracts (CGD_SOCKET_NAME once
// caused a real regression from a re-typing error).
package paths

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// Platform predicates (IS_LINUX / IS_MACOS in Python).
var (
	IsLinux = runtime.GOOS == "linux"
	IsMacOS = runtime.GOOS == "darwin"
)

// Container runtimes that build an argv, load an image, and answer `<rt> ps`.
// Iterate this — never AllRuntimes — in container-side code.
var SupportedRuntimes = []string{"podman", "container"}

// Native (non-container) runtimes. macos-user runs under Seatbelt with no VM,
// no Linux image; explicit opt-in only, never auto-detected.
var NativeRuntimes = []string{"macos-user"}

// AllRuntimes is every value the `runtime` config key / YOLO_RUNTIME may take.
var AllRuntimes = append(append([]string{}, SupportedRuntimes...), NativeRuntimes...)

const (
	// JailImageRepo is the REPOSITORY the jail image is published under;
	// JailImageRepoShort drops the localhost/ prefix Apple Container's CLI
	// doesn't recognize.
	//
	// The repository — not the tag — is the stable half. Since C2 the loaded
	// image is addressed by CONTENT (`<repo>:<sha16-of-store-path>`, built by
	// image.JailImageRef), so anything that wants "the jail image" in general
	// must filter by REPOSITORY, the way internal/prune already does. Nothing
	// may depend on a particular tag: the container image tag is not a public
	// surface (docs/reference/image-staging-vs-baking.md, "The content-addressed image ref").
	JailImageRepo      = "localhost/yolo-jail"
	JailImageRepoShort = "yolo-jail"

	// JailImage is the fully-qualified LEGACY :latest ref; JailImageShort is its
	// unqualified twin. The nix flake bakes `tag = "latest"`, which is what these
	// spell — but since C2 the load OVERRIDES that name (image.StreamRepoTag is
	// written into the archive's RepoTags), so nothing lands here by default any
	// more. They survive as (a) the DESTINATION of the best-effort alias that
	// keeps :latest pointing at the newest load, and (b) the only name the
	// degraded fallback branch (no store path in hand) can honestly ask about.
	// They are NOT the name a jail runs.
	JailImage      = JailImageRepo + ":latest"
	JailImageShort = JailImageRepoShort + ":latest"

	// JailHostServicesDir is where each host service's published ENDPOINT FILE
	// appears in-jail: one <name>.endpoint per service, naming the loopback-TLS
	// listener to dial.
	//
	// THE DIRECTORY IS SECRET-BEARING. Every file in it carries that service's
	// per-jail bearer token alongside its address and public cert, so the
	// directory is per-jail and never shared, and each file is 0600. See
	// internal/svcendpoint and docs/reference/loophole-transport.md §3.2.
	JailHostServicesDir = "/run/yolo-services"

	// JailPrefixDir is where a launch mounts the install prefix inside a container jail
	// (the binaries and the flake bundle, read-only). The launcher's own spelling is
	// run.JailPrefixDir, which cannot be imported below internal/cli; this copy is for the
	// host render's jail-path check (entrypoint.JailPathsIn), and a test in internal/cli pins
	// the two equal.
	JailPrefixDir = "/opt/yolo-jail"

	// BuiltinCgroupLoopholeName is the cgroup-delegate service name.
	//
	// STILL A CONSTANT, AND NO LONGER A RESERVATION — the two used to be the same
	// fact and are now separate ones. The name is the loophole's, declared by the
	// official `cgroup-delegate` pack's manifest; this constant survives because
	// CgdSocketName and CgdEndpointName are COMPOSED from it and the entrypoint
	// (baked into the image) expects those exact filenames. See CgdSocketName below
	// for what a drift there costs.
	BuiltinCgroupLoopholeName = "cgroup-delegate"
)

// `BuiltinLoopholeNames`, `BuiltinJournalLoopholeName` and `JournalSocketName` were all
// deleted on 2026-08-18, and the deletion is the sprint's point rather than a tidy-up.
//
// The slice was the part of the reserved loophole namespace this package owned: the
// names yolo's OWN in-process daemons answered to, which no manifest anywhere could
// claim. It existed because both of its entries were reserved in fact and enforced
// nowhere — the config validator refused `loopholes.cgroup-delegate` by name and said
// nothing about `journal`, and internal/loopholes never mentioned either one. A pack or
// user manifest named `journal` therefore loaded, was discovered, had its daemon skipped
// without a word, and still contributed its --add-host / ca_cert / --device / bind
// mounts / jail_env to the argv — half a loophole, silently
// (docs/reference/loophole-system.md#selection-and-discovery).
//
// BOTH NAMES ARE PACKS' NOW (`journal`, `cgroup-delegate`), and a name a pack ships
// cannot also be a name yolo answers to itself: the pack pre-flight
// (PackLoopholeNameConflicts) is FATAL, so a reservation left standing over either
// manifest would refuse the whole launch for everyone who selects that pack.
//
// Neither came free the way `host-processes` and `audio` did. Those two were reserved
// only as BUNDLED DIRECTORY NAMES, read off the same embed.FS the loader materializes,
// so `git mv` retired them with no code change. These were constants here, so each had
// to be deleted by hand in the commit that shipped its manifest — which is the same
// shape `broker.BrokerLoopholeName` had, and the trap whoever converted the broker had
// to avoid rather than generalize past.
//
// THERE IS NO RESERVED LOOPHOLE NAMESPACE LEFT. `loopholes.ReservedLoopholeNames`, which
// composed what survived this slice's deletion, is itself deleted (2026-08-19): the
// broker was its last entry, and moving that manifest into `packs/claude` emptied both
// of its contributors at once (docs/design/broker-as-a-pack.md §13). What refuses a
// duplicate loophole name now is exclusivity ACROSS PACKS, plus the origin gate for what
// a claimed name is allowed to switch on.

// CgdSocketName MUST be "<BuiltinCgroupLoopholeName>.sock": the entrypoint
// (baked into the image) and YOLO_SERVICE_CGROUP_DELEGATE_SOCKET both expect
// /run/yolo-services/cgroup-delegate.sock. A refactor once kept the legacy
// "cgroup.sock" name here and every jail silently reported the delegate as
// unavailable.
const CgdSocketName = BuiltinCgroupLoopholeName + ".sock"

// ServiceEndpointExt is the extension of a host service's published endpoint
// file: /run/yolo-services/<name>.endpoint.
//
// THESE FILES ARE SECRET-BEARING. Each carries its service's per-jail bearer
// token next to the address and the public cert, which is why the mode (0600)
// and the per-jail directory are load-bearing rather than cosmetic.
const ServiceEndpointExt = ".endpoint"

// ServiceEnvVarPrefix and ServiceEnvVarSuffix compose YOLO_SERVICE_<NAME>_ENDPOINT,
// the variable naming a service's endpoint FILE in-jail. The value is always a
// path, never an address: the address lives inside the file so it can change
// without relaunching the jail, whose environment is frozen at container start.
//
// The producer (the run pipeline) and every consumer (yolo-ps, the OAuth
// terminator, the entrypoint's generated clients) must never drift — see
// CgdSocketName above for what a drifted name costs: it silently disabled the
// cgroup delegate in every jail.
//
// The _SOCKET spelling these replace is deliberately NOT also emitted. A stale
// baked client reading an ABSENT variable hits its own clear "not wired up in this
// jail" path, where one reading a same-named variable whose value is no longer a
// socket would dial a regular file and report something obscure.
const (
	ServiceEnvVarPrefix = "YOLO_SERVICE_"
	ServiceEnvVarSuffix = "_ENDPOINT"

	// JailDaemonReadyNamesEnv names endpoint-publishing jail daemons whose
	// readiness the entrypoint must receive before it probes their endpoint.
	// It is emitted only when the launch registered the matching endpoint above;
	// an idle, selection-lazy service therefore never holds boot open.
	JailDaemonReadyNamesEnv = "YOLO_JAIL_DAEMON_READY_NAMES"
	// JailDaemonReadyFDEnv is the inherited, one-shot readiness pipe. The
	// entrypoint gives its write end to yolo-jaild; the supervisor passes it to
	// children as fd 3, and a ready daemon writes its declared name after the
	// endpoint it registered is live.
	JailDaemonReadyFDEnv = "YOLO_JAIL_DAEMON_READY_FD"
)

// SerialEndpointEnv and HostProcessesEndpointEnv are the endpoint variables two in-jail
// loophole CLIENTS read: `yolo-serial` the serial loophole's, `yolo-ps` the host-processes
// loophole's. Each is YOLO_SERVICE_<NAME>_ENDPOINT for its loophole's name, the spelling the
// run pipeline's hostServiceEnvVar produces, composed from the two halves above so the three
// cannot drift.
//
// They are named here, and not inside each client, because a second binary keys on them: the
// macos-user launch stages a client into its guest exactly when the session env carries the
// variable that client reads (macosuser.GuestClients). Spelled twice, a renamed client would
// be staged for a variable it no longer reads, or not staged for the one it does, and either
// way the sandbox would hold an endpoint and no program that dials it.
const (
	SerialEndpointEnv        = ServiceEnvVarPrefix + "SERIAL" + ServiceEnvVarSuffix
	HostProcessesEndpointEnv = ServiceEnvVarPrefix + "HOST_PROCESSES" + ServiceEnvVarSuffix
)

// CgdEndpointName MUST be "<BuiltinCgroupLoopholeName>.endpoint" — composed, for
// exactly the reason recorded above CgdSocketName.
const CgdEndpointName = BuiltinCgroupLoopholeName + ServiceEndpointExt

// HostLoopbackEnvVar carries the LAUNCHER'S host-loopback decision into the jail,
// and it exists to keep apart two outcomes that are indistinguishable from inside:
// a jail-facing service is unreachable because this HOST cannot forward the host's
// loopback (an old passt, a stack yolo does not recognise) — a KNOWN LIMITATION —
// or because yolo DID ask for that forwarding and the service is unreachable
// anyway — a FAULT. Only the second is a broken jail, and only the second may ever
// fail a launch (docs/reference/loopback-tls-reachability.md, OQ-R2 as scoped by
// OQ-R3: "unsupported is not broken").
//
// The producer is internal/cli/run/hostloopback.go, which is the only place that
// knows whether the option reached the argv; the consumer is
// internal/entrypoint/reachability.go, the in-jail witness. The spelling lives
// here for the reason ServiceEnvVarPrefix does: a producer and a consumer in
// different binaries, one of them BAKED INTO THE IMAGE, must not be able to drift
// apart by a re-typing.
//
// EVERY STATE IS SPELLED (OQ-R6). This used to be positive-only — set for the two
// definite outcomes, omitted otherwise — and an ABSENT variable therefore stood for
// four unrelated launches at once: a jail that SHARES the launcher's network
// namespace (no forwarding hop exists), a launcher that reached no conclusion (a
// rootful podman, an unrecognised backend, an explicit network.mode, the
// YOLO_NO_HOST_LOOPBACK opt-out, Apple Container), a `podman info` that could not be
// read, and a launcher older than the variable itself. The first of those is the
// STRONGEST case in the set rather than the weakest — with one namespace there is no
// forwarding to get wrong, so an unreachable service has no host-stack excuse — and
// it was indistinguishable from the vaguest. So the launcher now emits one of the
// four values below on EVERY launch, and absent is left to mean only "this launcher
// predates the variable", which the consumer reads exactly like Unknown.
//
// Safety did not move with it, because it never lived in the omission: the consumer
// matches the escalating values EXACTLY and every other input — a spelling from a
// newer launcher, an empty value, an absent one — falls through to the
// never-escalate default. Adding states is therefore free in the one direction that
// costs a jail.
const HostLoopbackEnvVar = "YOLO_HOST_LOOPBACK"

const (
	// HostLoopbackRequested: yolo put the forwarding option on this container's
	// argv (--network=pasta:--map-host-loopback,… or the slirp4netns twin). An
	// unreachable jail-facing service on such a launch is a fault.
	HostLoopbackRequested = "requested"

	// HostLoopbackShared: this jail shares the LAUNCHER'S network namespace —
	// `network.mode: "host"`, or podman-in-podman, where the launcher forces
	// --net=host because netavark cannot create a netns without NET_ADMIN. There is
	// no forwarding hop to ask for and none to get wrong: the jail's 127.0.0.1 IS
	// the listener's, which is why internal/cli/run's advertiseHostFor publishes
	// that address for exactly these shapes and nothing else works there. So an
	// unreachable service on such a launch has no host-stack excuse either, and this
	// is the strongest of the four rather than the weakest (OQ-R5).
	HostLoopbackShared = "shared"

	// HostLoopbackUnsupported: yolo identified the rootless stack, could not get
	// it to forward the host's loopback (an old passt, a capability it could not
	// confirm), and launched anyway — OQ-R3's ruling that yolo degrades rather
	// than refusing on the host it is given. An unreachable service here is a
	// known limitation of the host, and the launch output said so.
	HostLoopbackUnsupported = "unsupported"

	// HostLoopbackUnknown: yolo reached NO conclusion. A rootful podman, a backend
	// it does not recognise, a `podman info` it could not read or parse, an explicit
	// network.mode it will not override (OQ-R1), the YOLO_NO_HOST_LOOPBACK opt-out,
	// Apple Container. Nothing was positively established, so nothing may be
	// escalated; the launch output carries whichever specific reason applied.
	//
	// It is deliberately DISTINCT from Unsupported even though neither escalates:
	// "yolo asked this host and it cannot forward" is a fact about the host with an
	// upgrade behind it, and "yolo never asked" is not — collapsing them would send
	// a reader to check their passt version over a rootful podman.
	HostLoopbackUnknown = "unknown"
)

// AllowUnreachableServicesEnv is the escape hatch out of the two boot refusals about
// services, mirroring YOLO_ALLOW_STALE_IMAGE (internal/image): any non-empty value
// keeps the jail launching, loudly, and says what it is suppressing. It reaches
//
//   - the in-jail reachability witness (internal/entrypoint/reachability.go), which
//     refuses a jail-facing service the jail cannot use where the launcher asked for
//     loopback forwarding (OQ-R2, OQ-R3), and
//   - the readiness refusal of a REQUIRED in-jail service that did not start, such as a
//     wire bridge that cannot bind its port (internal/entrypoint/requiredservice.go,
//     OQ-R8 in docs/reference/loopback-tls-reachability.md).
//
// It does not reach the orphan refusal beside the second, which names the pid to kill.
//
// It exists because both refusals stop the launch, and a hard fatal with no override leaves
// a user unable to open a shell to fix the very daemon that is failing. The user types
// it on the HOST, so the launcher forwards it into the container — an escape hatch
// nobody can reach is not one.
const AllowUnreachableServicesEnv = "YOLO_ALLOW_UNREACHABLE_SERVICES"

// AllowMissingProvidersEnv is the escape hatch out of the selected-pack credential
// pre-flight (docs/reference/providers.md#the-credential-preflight, #pv-oq-13) and out of the
// region pre-flight beside it (#the-region-preflight, OQ-BR6): any non-empty value keeps the
// launch going, loudly, and says what it is suppressing. The named-var
// const is the same convention AllowUnreachableServicesEnv is — the producer (this CLI)
// and every consumer read one spelling, so a hatch cannot drift out of reach by a
// re-typing. Both notches that refuse read it host-side (internal/cli/run and the
// `yolo host` exec half), so — unlike the reachability witness — nothing has to forward
// it into the container.
const AllowMissingProvidersEnv = "YOLO_ALLOW_MISSING_PROVIDERS"

// HoldOnRefusalEnv makes the ENTRYPOINT, on a boot it is about to refuse, print how
// to get in and then BLOCK instead of exiting — so the container stays up with the
// failed state intact. Any non-empty value. The user types it on the HOST, so the
// launcher forwards it into the container, for AllowUnreachableServicesEnv's reason:
// a container inherits nothing from the launcher's environment, and the user this
// exists for is by definition the one with no in-jail shell.
//
// It is NOT an `ALLOW_` hatch and is deliberately not spelled like one. The hatches
// suppress a refusal; this one keeps it — the failure is still reported and the exit
// code is still non-zero when the hold ends. It changes WHEN the jail dies, never
// whether the launch failed.
//
// # Why it has to be opt-in
//
// A jail that hangs instead of failing is a worse default than one that fails, and
// CI would acquire a hung container on the first red boot. So the default is
// unchanged and an absent variable is the whole of it.
//
// # What it is for
//
// A boot that refuses takes its evidence with it: the container runs with `--rm`
// (internal/cli/run/assemble.go's runFlags) and the entrypoint's refusal exits, so
// podman deletes the container — the process table, the listeners and the daemon
// logs with it. Measured 2026-09-19 on a `cannot bind 127.0.0.1:8214: address
// already in use` refusal that three separate attempts failed to catch, because by
// the time a human can type there is no container left to type at.
const HoldOnRefusalEnv = "YOLO_HOLD_ON_REFUSAL"

// HoldExecEnv carries the exact command a human should type to get into the held
// container — `<runtime> exec -it <container> bash` — composed by the LAUNCHER and
// printed verbatim by the entrypoint.
//
// It is composed host-side because neither half of it can be derived in the jail.
// The container NAME is the launcher's (`assembleInput.cname`; a container's own
// hostname is its ID, which is a different string from the one the launch banner
// named), and the RUNTIME is worse than absent in-jail: commonEnvBlock emits a
// hard-coded `YOLO_RUNTIME=podman` on every backend, so a jail under Apple
// Container reads `podman` and would print a command that does not exist.
//
// Emitted only alongside HoldOnRefusalEnv, and only by the container backends
// (macos-user returns above assembleRunCmd and has no container to exec into).
const HoldExecEnv = "YOLO_HOLD_EXEC"

// AllowMissingProgramsEnv is the escape hatch out of the jail's READINESS ACT
// (docs/design/jail-notch-readiness.md, OQ-JR1): the provisioning stage installs every program
// a selected pack declares before the command runs, and a program it cannot install STOPS the
// launch, offline included. Any non-empty value starts the jail instead, listing each program
// it could not install; each then installs from its launcher the first time it is run, as
// every program did before the readiness act.
//
// The user types it on the HOST, in front of `yolo`, and the launcher forwards it into the
// container (run.programReadinessArgs) for AllowUnreachableServicesEnv's reason: the stage
// that reads it runs in the jail, and the user it exists for is the one whose jail will not
// start. The entrypoint BAKES its value into the generated bootstrap rather than leaving the
// script to read it, the way every other value there is baked.
//
// The same variable keeps a launch going past its refusal of a missing patched build
// (docs/design/patched-extensions.md PPX-D40, internal/cli/run's missingbuilds.go), so one
// hatch covers every program a launch cannot deliver. That refusal reads it host-side,
// before any jail exists.
const AllowMissingProgramsEnv = "YOLO_ALLOW_MISSING_PROGRAMS"

// NoProgramReadinessEnv turns the jail's readiness act OFF for a launch: any non-empty value
// leaves every declared program to install from its launcher on first use, which is what every
// launch did before OQ-JR1 (docs/design/jail-notch-readiness.md, JR-D7).
//
// It is NOT a second spelling of AllowMissingProgramsEnv. That hatch still installs and starts
// the jail when an install fails; this one installs nothing ahead of time. It exists for the
// integration suite's every-push run, which must not install six vendors' current releases on
// every push (docs/reference/agent-install-in-ci.md, "The real-install gate"), the reason
// YOLO_NO_AUTO_CAPTURE exists beside it. Forwarded from the host like the hatch, and loud: the
// stage says it installed nothing ahead of time and names what it left.
const NoProgramReadinessEnv = "YOLO_NO_PROGRAM_READINESS"

// NoProgramReadinessCaptureJail is the value the launcher gives NoProgramReadinessEnv for a
// CAPTURE or BUILD jail (run.Options.NoProgramReadiness, set by cli.runCaptureJail): the
// readiness act is off there for the jail's own reason, not the user's, and the stage's notice
// says that instead of naming a variable nobody typed.
//
// Off there because that jail's command IS an install. `yolo capture` diffs the home across its
// installer, so a readiness act that installed the program first left the capture empty; and a
// fork's build jail runs the build that produces the program, so a readiness act that asked for
// that program first refused the jail before the build could run.
const NoProgramReadinessCaptureJail = "capture-jail"

// TimingEnv is the host-process opt-in to `--timing`'s span logging
// (docs/reference/perf-logging.md): any non-empty value enables the same surface
// the flag does, for wrappers and scripts that cannot add a flag.
//
// HOST-ONLY, deliberately (design D5): it is never forwarded into the jail.
// The in-container half of timing is keyed on a SEPARATE argv pair the launcher
// emits, YOLO_JAIL_TIMING=1 (renamed from YOLO_PROFILE by D13). The two names
// are deliberately unrelated: one variable serving both would make forwarding
// look intended, and D5's whole point is that it is not.
//
// The reason this comment used to give — that the halves redeploy on different
// cadences, so a second spelling crossing the boundary is a skew bug — was
// FALSE and is why the rename went unmade for so long. Nothing in the image or
// the entrypoint reads the jail-side pair; the launcher emits it and also
// generates the bash it belongs to, so both halves move in one commit.
const TimingEnv = "YOLO_TIMING"

// VerboseEnv is the published form of the global `--verbose`/`-v` flag
// (internal/cli/verbose.go) and may equally be typed directly. v1 enables the
// same span surface TimingEnv does; it exists so non-timing diagnostics have a
// gate to grow into (design D1). HOST-ONLY for the same reason TimingEnv is.
const VerboseEnv = "YOLO_VERBOSE"

// hostServicesDirPrefix names the per-jail host-side directory. The 8-hex suffix
// is JailShortHash(cname).
const hostServicesDirPrefix = "yolo-host-services-"

// JailShortHash is the 8-hex key derived from a container name. It identifies a
// jail's host-services directory and the upstream sockets of its fronted daemons,
// and `yolo prune`'s legacy sweep matches a leftover broker-relay pid file back to
// a live container name through it — so every producer and consumer must compute it
// identically. It lived in three packages before this, copied by hand.
//
// It does NOT key a host-wide singleton's paths. Those are derived from the
// LOOPHOLE NAME (HostSingletonSocket below), because one daemon serving every jail
// has no jail to be keyed by — which is also what stops a jail's teardown, which
// sweeps by this hash, from reaching it.
func JailShortHash(cname string) string {
	sum := sha1.Sum([]byte(cname))
	return hex.EncodeToString(sum[:])[:8]
}

// HostServicesDirName returns the per-jail directory's BASE NAME for a hash that
// is already known — the reap path's shape, which sweeps by hash without ever
// holding a container name.
func HostServicesDirName(shortHash string) string { return hostServicesDirPrefix + shortHash }

// HostSingletonSocket / HostSingletonPIDFile / HostSingletonLock are the fixed
// host-wide paths a `host_daemon.scope: "host"` loophole's ONE daemon owns
// (loopholedecl.ScopeHost). They are keyed by the LOOPHOLE NAME and nothing else:
// a singleton has no jail to be keyed by, and deriving them from the name is what
// makes two yolo processes launching two different jails at the same moment agree
// on which file to flock and which socket to ensure.
//
// THE FRAMEWORK OWNS THESE PATHS, not the manifest, for the reason
// startExternalService already states about `{socket}`: yolo decides what the path
// IS, which is the whole point of owning the transport. A manifest naming its own
// host-wide socket would be a host path claim yolo would then have to gate, and
// two manifests could name the same one.
//
// They live under /tmp so AF_UNIX sun_path limits are never a concern (108 bytes
// on Linux, 104 on darwin) and a host reboot leaves a clean slate — the same
// reasoning, and for claude-oauth-broker the same BYTES, as the broker singleton
// constants these replaced: internal/broker's BrokerSingleton* now derive from these
// functions, so `yolo broker status`, `yolo check` and the run pipeline's front reach
// one file by construction, and a test pins the production bytes an older yolo on the
// same host still spells.
func HostSingletonSocket(loopholeName string) string {
	return HostSingletonDir + "/yolo-" + loopholeName + ".sock"
}

// HostSingletonPIDFile returns the singleton's PID file — see HostSingletonSocket.
func HostSingletonPIDFile(loopholeName string) string {
	return HostSingletonDir + "/yolo-" + loopholeName + ".pid"
}

// HostSingletonLock returns the singleton's spawn lock — see HostSingletonSocket.
// It is the flock two concurrent launches contend for, so that the loser observes
// the winner's daemon instead of starting a second one.
func HostSingletonLock(loopholeName string) string {
	return HostSingletonDir + "/yolo-" + loopholeName + ".lock"
}

// HostSingletonGlob matches every host-wide daemon's spawn lock under HostSingletonDir
// (`yolo host-daemon` enumerates the singletons by it).
func HostSingletonGlob() string { return HostSingletonDir + "/yolo-*.lock" }

// DefaultHostSingletonDir is where the host-wide singleton rendezvous files live in
// production: /tmp, machine-wide by design (see HostSingletonSocket).
const DefaultHostSingletonDir = "/tmp"

// HostSingletonDir is the directory every host-wide singleton path above is built in.
// It is always DefaultHostSingletonDir in production. It is a variable for ONE reason:
// a test package that spawns real singletons redirects it to a private directory in its
// TestMain (testsupport.IsolateHostSingletons), because `go test ./...` runs packages in
// parallel and two packages' daemons on the one machine-wide path take each other's
// socket. The spawned daemon learns its socket from its argv (`--socket`), so the
// redirect reaches it without any environment variable. Nothing in production writes it.
//
// The per-jail host-services dirs (HostServicesDir) are built in it too, for the same
// reason: they sit at deterministic machine-wide /tmp paths, keyed by a container name a
// test chooses, and a test package that starts host services without tearing them down
// used to leave empty /tmp/yolo-host-services-<8hex> dirs behind on every run. Redirected,
// they land in the package's private directory and go with it.
var HostSingletonDir = DefaultHostSingletonDir

// HostServicesDir returns the per-jail host-side directory holding this jail's
// published endpoint files: /tmp/yolo-host-services-<8hex>. /tmp is HostSingletonDir,
// which is /tmp in production and a private directory in a test package that isolates
// its host singletons.
//
// THE DIRECTORY IS SECRET-BEARING (see JailHostServicesDir, its in-jail mount
// point) and it sits at a fully deterministic path under a world-writable /tmp,
// which is why it is created 0700 and why svcendpoint refuses to publish into one
// that is not.
//
// isMacOS is a parameter rather than the package's own IsMacOS so callers that
// inject the platform (the run pipeline's golden fixtures do) get the same answer
// they assert. On macOS /tmp is a symlink to /private/tmp and the resolved form is
// used, so a path here matches what the kernel reports.
//
// ⚠ ON macos-user NO LAUNCH PUBLISHES HERE. That backend runs one sandbox per invocation and
// no container, so two terminals in one workspace are two live sessions of one cname, and a
// dir keyed by the cname alone was one dir for both: the second session's front replaced the
// first's endpoint file, and the first teardown removed the dir under the survivor
// (docs/design/host-daemon-ownership.md#OQ-HD10). Each macos-user session creates its own
// dir instead, named by HostServicesSessionPrefix (internal/cli/run/servicessession.go).
func HostServicesDir(cname string, isMacOS bool) string {
	return filepath.Join(HostServicesBase(isMacOS), HostServicesDirName(JailShortHash(cname)))
}

// HostServicesBase is the directory every host-services dir is built in: HostSingletonDir,
// resolved on macOS, where /tmp is a symlink to /private/tmp, so a path built here matches
// what the kernel reports.
func HostServicesBase(isMacOS bool) string {
	base := HostSingletonDir
	if isMacOS {
		if r, err := filepath.EvalSymlinks(base); err == nil {
			base = r
		}
	}
	return base
}

// HostServicesSessionPrefix is the base-name prefix of a macos-user SESSION's own
// host-services dir: yolo-host-services-<8hex>-, the workspace's key and a dash, to which the
// session's creation (os.MkdirTemp) appends a random suffix. A SESSION, a term coined here, is
// one macos-user invocation of yolo: one sandbox and the host services started for it, from
// launch to teardown.
//
// The dash is what keeps the two families apart. A container jail's dir is exactly
// HostServicesDirName(<8hex>), with nothing after the hash, so HostServicesSessionGlob never
// matches one, and nothing that sweeps session dirs can reach a container's.
func HostServicesSessionPrefix(cname string) string {
	return HostServicesDirName(JailShortHash(cname)) + "-"
}

// HostServicesSessionGlob matches every macos-user session's host-services dir under base, of
// every workspace: the prefix, any 8-hex key, a dash, any suffix.
func HostServicesSessionGlob(base string) string {
	return filepath.Join(base, hostServicesDirPrefix+"*-*")
}

// HostServicesSessionLockName is the file inside a session's host-services dir that its yolo
// process holds an exclusive flock on for the session's whole life. The kernel drops the lock
// when that process exits, however it exits, so a lock nobody holds is the evidence that the
// session is gone. Host-only: the file is 0600 and the sandbox account's grant on the dir is
// search alone (macosuser.EndpointGrantCommands), so the sandbox can neither open nor remove it.
const HostServicesSessionLockName = ".session.lock"

// Home-relative storage layout. Python computes these from Path.home() at
// import time; Go exposes the fixed suffixes plus helpers that join with the
// caller's home dir, so the constant *strings* are what the golden tests pins
// (they don't vary by host) while the absolute paths resolve at runtime.
const (
	globalStorageSuffix = ".local/share/yolo-jail"
	userConfigSuffix    = ".config/yolo-jail/config.jsonc"
	// localPackLeaf is the CONVENTIONAL LOCAL PACK's directory name. It is deliberately
	// not a suffix of its own: the convention is "beside config.jsonc" (that is the whole
	// argument for the location — user-scope yolo config already lives there), so
	// LocalPackDir derives it from the user config's directory and the two cannot drift
	// apart the way two independently-spelled suffixes could.
	localPackLeaf = "local"
	// workspaceFilesLeaf is the per-workspace files' folder, beside config.jsonc for
	// localPackLeaf's reason: user-scope yolo config already lives there.
	workspaceFilesLeaf = "workspaces"
)

// GlobalStorage returns $HOME/.local/share/yolo-jail.
func GlobalStorage() string { return GlobalStorageUnder(home()) }

// GlobalStorageRel is the state dir as a HOME-RELATIVE, slash-separated path
// (".local/share/yolo-jail") — the same string GlobalStorageUnder joins onto a home.
//
// It exists for the one caller that needs the state dir WITHOUT a home to join it to:
// internal/capture excludes yolo's own state from an install capture, and it does so from
// inside a jail whose home it is describing rather than writing to. The exclusion has to be
// home-relative because a manifest's paths are (Manifest.Path is ".local/…", never absolute),
// and it has to be THIS string because the dir being excluded is the dir GlobalStorageUnder
// creates — a second spelling would be an exclusion that stopped matching the moment the
// suffix moved.
func GlobalStorageRel() string { return globalStorageSuffix }

// JailDaemonLogsRel is the in-jail supervisor's per-daemon log dir as a HOME-RELATIVE,
// slash-separated path (".local/state/yolo-jail-daemons"), the one spelling
// supervisor.LogDir joins onto a home and internal/capture excludes from an install capture,
// for GlobalStorageRel's reason: a capture jail's daemons log there while the installer runs,
// inside the `.local` capture surface.
func JailDaemonLogsRel() string { return ".local/state/yolo-jail-daemons" }

// GlobalStorageUnder returns the state dir under an EXPLICIT home, rather than the
// process $HOME. It exists because a caller that has ALREADY resolved which home it is
// writing into must not re-derive it from the environment: `yolo host apply` renders into a
// home it was handed (render.Target.Home), and a state path computed from $HOME instead
// would land in the invoking user's REAL state dir the moment the two differ — which is
// exactly what every test with a t.TempDir() home does. Keying on the passed home makes
// that class of mistake impossible rather than merely avoided.
func GlobalStorageUnder(home string) string { return filepath.Join(home, globalStorageSuffix) }

// WorkspaceStateDir returns <workspace>/.yolo — the per-workspace directory yolo owns
// inside a user's project (boot log, assembled config, the receipt log, the home overlay).
//
// CREATING it goes through EnsureWorkspaceStateDir, which is the only thing that also makes
// it un-committable. This function is path arithmetic and creates nothing.
func WorkspaceStateDir(workspace string) string { return filepath.Join(workspace, ".yolo") }

// WorkspaceStateIgnoreName is the file that makes <workspace>/.yolo un-committable.
const WorkspaceStateIgnoreName = ".gitignore"

// WorkspaceStateIgnore is that file's content, and the bare `*` is the whole mechanism: it
// ignores every path under this directory INCLUDING this file, so .yolo/ is invisible to git
// however the repo's own .gitignore is written.
//
// WHY IT IS NOT OPTIONAL. What lands under .yolo is not merely noise. `launch.log` tees
// everything the launcher printed, and a --dry-run's printed argv IS its env list, provider
// secrets and all (docs/plans/handoff-macos-user-open-threads.md §6). `archive/config/` holds
// VERBATIM copies of the user's own pre-yolo agent config files — exactly where credentials
// live — and render.Target.ArchivePath is shaped to keep them forever. `home/` is the jail's
// entire home overlay. This repo happens to ignore .yolo/ by a hand-written line in its own
// .gitignore, and until this function existed that accident was the only thing making
// docs/reference/storage-and-config.md's description of the directory as "gitignored" true.
//
// NOTHING under .yolo is meant to be committed, `handover.md` included — the one file worth
// asking about, since a human may want to hand one to a teammate. The handoff design already
// answers it: the durable context is filed in a committed file ELSEWHERE in the workspace and
// .yolo/ holds only the one-time POINTER, which the next launch consumes by renaming it
// (docs/reference/host-to-jail-handoff.md). A committed pointer would be a file that is stale
// by design.
const WorkspaceStateIgnore = `# yolo-jail's per-workspace state. None of it belongs in version control:
# home/ is the jail's home overlay, archive/ holds verbatim copies of your own
# agent config files, and the logs record a launch's full argv.
#
# The bare '*' below ignores this file too, so .yolo/ stays invisible to git
# however the repo's own .gitignore is written. yolo writes this file once and
# never overwrites it - edit it, or delete it, if you mean to commit any of this.
*
`

// EnsureWorkspaceStateDir creates <workspace>/.yolo and leaves it un-committable, returning
// the directory so a caller can join a filename onto it.
//
// THE FILE IS THE IDEMPOTENCY KEY, NOT THE DIRECTORY, and that is the whole reason this is a
// function rather than two lines at the one call site that creates the dir first. Gating the
// write on "did I just create .yolo?" would leave every workspace yolo has ever launched
// without the file forever — and those are precisely the ones already holding a launch.log.
// So: MkdirAll every time, then write when the file is absent, however old the directory is.
//
// A .gitignore that is already there is NEVER touched, whatever it says. A user who edited it,
// emptied it, or deliberately un-ignored something owns it from then on.
//
// The write is best-effort and its failure is NOT returned: no launch should die because it
// could not ignore itself, and a workspace yolo cannot write into is one where the launch is
// about to fail for a better reason. Only MkdirAll's error reaches the caller, because every
// caller already refuses on it.
//
// IT IS ALSO THE CHOKEPOINT that keeps a `.yolo` out of the three directories that may
// never hold one (workspacescope.go states them and both hazards). The refusal is returned
// as the error every caller already has a branch for, so a command that gains the ability
// to run in a home is protected without being taught about it — and the breach is checked
// BEFORE MkdirAll, because the directory is the artifact.
func EnsureWorkspaceStateDir(workspace string) (string, error) {
	dir := WorkspaceStateDir(workspace)
	if breach := WorkspaceScopeBreach(workspace); breach != nil {
		return dir, breach
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir, err
	}
	// Beneath a root on the directory, created O_EXCL: `.yolo` is jail-writable, so a link the
	// last jail left at `.yolo` itself, or at the ignore file (a DANGLING one included), would
	// otherwise have this write create or truncate the file it names. A linked `.yolo` skips
	// the write here, and the callers that write beneath it refuse it (OpenWorkspaceStateFile).
	// Anything already at the name is left alone: it is a file the user put there.
	r, err := OpenStateDirRoot(dir)
	if err != nil {
		return dir, nil
	}
	defer r.Close()
	if f, err := r.OpenFile(WorkspaceStateIgnoreName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		_, _ = f.WriteString(WorkspaceStateIgnore)
		_ = f.Close()
	}
	return dir, nil
}

// WorkspaceHomeState returns <workspace>/.yolo/home — the HOST side of the jail home's
// per-workspace binds. Its children are the HomeSurface.Subtree names, bound into the jail
// at the matching HomeSurface.HomeRel.
//
// It is spelled here rather than at its two call sites because those two are the halves of
// one fact and only agree by construction: the run pipeline binds `<this>/local` at
// `$HOME/.local` and also binds the WORKSPACE at /workspace, so inside a jail the same
// directory has two paths — and only the workspace-side one shares a MOUNT with anything
// else under /workspace. That is what lets `yolo capture` move a gigabyte-scale delta with
// rename(2) instead of copying it (program-delivery.md §6.3; MEASURED 2026-09-04, both
// directions). A second spelling of ".yolo/home" would be a capture that silently started
// copying the moment the layout moved.
func WorkspaceHomeState(workspace string) string {
	return filepath.Join(WorkspaceStateDir(workspace), "home")
}

// GlobalHome returns the shared container /home/agent backing dir.
func GlobalHome() string { return filepath.Join(GlobalStorage(), "home") }

// HomeSurface is one of the per-workspace writable dirs inside the jail home: the pair of
// names for ONE directory, which lives at <ws>/.yolo/home/<Subtree> on the host and at
// $HOME/<HomeRel> inside the jail.
//
// The two spellings differ (`npm-global` → `.npm-global`, `go` → `go`) and the mapping is
// not derivable, which is why it is written down once here instead of three times.
type HomeSurface struct {
	// Subtree is the directory's name under <ws>/.yolo/home — the host side of the bind.
	Subtree string
	// HomeRel is its path relative to the jail's HOME — the jail side of the bind.
	HomeRel string
}

// HomeSurfaces returns the three top-level per-workspace directories mounted separately into
// the jail home: the npm prefix, ~/.local, and $GOPATH. InstalledProgramSurfaces adds any
// install payload nested inside another mount; keeping that distinction prevents a nested
// surface from becoming a second overlapping bind.
//
// The jail's OTHER per-workspace
// binds — `yolo-bin`, `config`, `.cache` — are deliberately absent: they hold GENERATED or
// CACHED content that is rebuilt or refetched.
//
// The order is the bind order in the podman argv (`run/assemble_parts.go`).
func HomeSurfaces() []HomeSurface {
	return []HomeSurface{
		{Subtree: "npm-global", HomeRel: ".npm-global"},
		{Subtree: "local", HomeRel: ".local"},
		{Subtree: "go", HomeRel: "go"},
	}
}

// InstalledProgramSurfaces returns the exact home subtrees capture and prune may treat as
// installed program bytes. It starts with the separately-mounted HomeSurfaces and adds nested
// payloads that live inside a pack's state mount.
//
// Codex's vendor installer plants a symlink in ~/.local/bin whose target is under
// ~/.codex/packages/standalone. Capturing only ~/.local records a dangling symlink. Adding all
// of ~/.codex would capture and dedupe mutable auth, histories, sessions and SQLite databases,
// so the vendor's standalone payload is the exact boundary.
func InstalledProgramSurfaces() []HomeSurface {
	return append(HomeSurfaces(), HomeSurface{
		Subtree: filepath.Join("codex", "packages", "standalone"),
		HomeRel: filepath.Join(".codex", "packages", "standalone"),
	})
}

// HomeFileRedirect is a home-ROOT file that is a symlink into a per-workspace directory:
// the name in the home, and the target relative to it.
//
// The three exist because the file has to be at the home root (the tool looks for it there)
// while its BYTES are one workspace's — `~/.claude.json` carries a `projects.<workspace>`
// map, `~/.config/bashrc` this launch's PATH, `~/.config/git/config` the composed identity.
// A relative target, spelled from the home, so it resolves through whatever backs the
// per-workspace directory: a bind mount on the container backends, a symlink on macos-user.
type HomeFileRedirect struct {
	Name   string
	Target string
}

// HomeFileRedirects returns those three. ONE list, two consumers that must agree about it:
// the podman launch writes them into each jail's :ro home skeleton (buildHomeSkeleton, in
// internal/cli/run), and the macos-user home layout writes the same three into the sandbox
// account home (entrypoint.DeriveDarwinHomeLayout). A fourth file redirected on one backend and not the
// other is a per-backend answer to "where does my agent's state live", which is the drift
// docs/design/macos-user-home-tiers.md §5.0 rules out.
//
// These are CORE's. The CONFIG-driven redirects — one per home-root `host_files` file
// (`~/.npmrc`) — exist on both backends too, outside this list because the user's config
// decides them: each consumer above lays them from the same two calls,
// config.HostFileEntry.StagingFor and SymlinkTarget (on macos-user,
// entrypoint.DarwinHomeLayout.WithHostFileRedirects). macos-user lays none at a login rc
// file its bootstrap writes by path on every launch (entrypoint.DarwinLoginRCFiles).
func HomeFileRedirects() []HomeFileRedirect {
	return []HomeFileRedirect{
		{Name: ".claude.json", Target: filepath.Join(".claude", "claude.json")},
		{Name: ".gitconfig", Target: filepath.Join(".config", "git", "config")},
		{Name: ".bashrc", Target: filepath.Join(".config", "bashrc")},
	}
}

// GeneratedBinDir returns $HOME/.local/share/yolo-jail/bin — the parent of every
// directory of yolo-GENERATED executables. It is a gathering point in the FILESYSTEM
// only, and nothing may ever put this parent on PATH.
//
// ⚠ Its children are ADJACENT AT THE HEAD of PATH since B2 (2026-09-04), not at opposite
// ends as this comment said until 2026-09-09, and there are TWO of them (block, launch) —
// not the three it counted. The rule holds for a better reason than the span it used to
// protect: the parent holds both, so a launcher reached through it would be reachable from
// the blockers' position. entrypoint.Env.GeneratedBinDir carries the same note.
//
// See host-agent-environment.md OQ-6 for the naming ruling that created it.
func GeneratedBinDir() string { return GeneratedBinDirUnder(home()) }

// GeneratedBinDirUnder is GeneratedBinDir under an EXPLICIT home — see GlobalStorageUnder
// for why a caller that has already resolved a home must not re-derive it from $HOME.
func GeneratedBinDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), "bin")
}

// WrapDir returns $HOME/.local/share/yolo-jail/bin/wrap — the HOST launch-wrapper dir.
//
// This is the one generated dir a USER is asked to put on PATH, and it must be
// PREPENDED, ahead of ~/.local/bin: a wrapper only works if it is found before the real
// binary, and `claude`'s own installer writes ~/.local/bin/claude, so sharing that
// directory would be a file collision rather than a shadowing strategy
// (host-agent-environment.md §5.1).
//
// Because prepending it makes everything inside shadow the user's real tools, the
// directory holds ONLY generated wrappers and is reset CONTENTS-ONLY — never RemoveAll,
// which would unlink a directory a user's PATH (or a live jail's bind) has captured.
func WrapDir() string { return WrapDirUnder(home()) }

// WrapDirUnder is WrapDir under an EXPLICIT home. `apply` renders into a home it was
// handed (render.Target.Home), so deriving this from $HOME instead would write into the
// invoking user's REAL state dir the moment the two differ — exactly what every test
// with a t.TempDir() home does.
func WrapDirUnder(home string) string {
	return filepath.Join(GeneratedBinDirUnder(home), "wrap")
}

// HostBlockDir returns $HOME/.local/share/yolo-jail/bin/block — where `yolo host --` keeps the
// blocked-tool shims it puts first on the PATH of the program it starts
// (docs/design/host-launch-environment.md HE-D11): one CONTENT-ADDRESSED child per distinct set
// of scripts, named by their digest, written once and then reused, so two launches with the same
// blockers share one directory and one with different blockers never edits another's.
//
// Under GeneratedBinDir, and so inside the folders every host PATH lookup skips
// (hostpath.ManagedDirs): a lookup of the program to run, or of a blocker's replacement, never
// finds a shim. ⚠ NO JAIL MOUNTS IT, for HostFloorDir's reason: the host runs these scripts with
// the user's authority, so a copy a jail could write would be a file the host executes because of
// where it sits.
func HostBlockDir() string { return HostBlockDirUnder(home()) }

// HostBlockDirUnder is HostBlockDir under an EXPLICIT home.
func HostBlockDirUnder(home string) string {
	return filepath.Join(GeneratedBinDirUnder(home), "block")
}

// hostWorkspaceSkillsLeaf is the state-dir child holding the records of the workspace skills
// links `yolo host --` wrote.
const hostWorkspaceSkillsLeaf = "host-workspace-skills"

// HostWorkspaceSkillsDir returns $HOME/.local/share/yolo-jail/host-workspace-skills — the RECORD
// of each link `yolo host -- <agent>` put into a workspace for the workspace skills layer
// (docs/design/workspace-skills.md WS-D19 to WS-D23): one file per workspace and link, keyed by a
// digest of the workspace's resolved path and the link's path in it. The record is what makes a
// link yolo's to refresh or remove, so it is host-side and never in the workspace's own .yolo/,
// which the repository's agent can write. No jail mounts it.
func HostWorkspaceSkillsDir() string { return filepath.Join(GlobalStorage(), hostWorkspaceSkillsLeaf) }

// hostAgentsLeaf is the state-dir child holding the stores yolo manages for programs `yolo host
// --` starts.
const hostAgentsLeaf = "host-agents"

// HostAgentStoreDir returns $HOME/.local/share/yolo-jail/host-agents/<pack> — a directory yolo
// manages for the program a pack delivers when `yolo host --` starts it with something of the
// machine's in place of the user's own: today the Claude credential view behind
// YOLO_CLAUDE_CREDENTIAL_VIEW (docs/design/claude-login-without-interception.md CL-D27), which
// the host broker writes and Claude reads through CLAUDE_SECURESTORAGE_CONFIG_DIR. The user's
// own `~/.claude` is never written.
//
// ⚠ NO JAIL MOUNTS IT, and it is created 0700: what sits here is a login the host broker keeps
// current, and a jail that could write it could hand a host Claude a credential of its choosing.
func HostAgentStoreDir(pack string) string {
	return filepath.Join(GlobalStorage(), hostAgentsLeaf, pack)
}

// GlobalMise returns the shared mise data dir.
func GlobalMise() string { return filepath.Join(GlobalStorage(), "mise") }

// GlobalCache returns the shared cache dir.
func GlobalCache() string { return filepath.Join(GlobalStorage(), "cache") }

// embeddedPacksLeaf is the state-dir child holding the content-addressed copies of the packs
// compiled into the binary (internal/packload's embeddedcache.go).
const embeddedPacksLeaf = "embedded-packs"

// EmbeddedPacksDir returns $HOME/.local/share/yolo-jail/embedded-packs — the base under
// which each build keeps ONE immutable, content-addressed copy of its embedded packs — or ""
// when no real home resolves (home() fell back to "/"), so the caller takes its
// per-process fallback instead of creating state under the filesystem root.
//
// ⚠ DELIBERATELY NOT UNDER GlobalCache(). The cache dir is bind-mounted READ-WRITE into
// every jail at ~/.cache (internal/cli/run/assemble_parts.go), and host yolo loads this tree
// with the authority of a pack yolo SHIPS — host_files grants, loophole host exec. A tree
// there would be a jail-writable file the host executes BECAUSE OF WHERE IT SITS: the
// injection class the hostcas rule in AGENTS.md exists to keep out. The location is the
// boundary; the tree's read-only modes are not (root ignores them).
func EmbeddedPacksDir() string {
	h := home()
	if h == "/" {
		return ""
	}
	return EmbeddedPacksDirUnder(h)
}

// EmbeddedPacksDirUnder is EmbeddedPacksDir under an EXPLICIT home.
func EmbeddedPacksDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), embeddedPacksLeaf)
}

// packBinariesLeaf is the state-dir child holding the DOWNLOADED PACK BINARIES
// (internal/packbin).
const packBinariesLeaf = "pack-binaries"

// PackBinariesDir returns $HOME/.local/share/yolo-jail/pack-binaries — the cache of the
// executables loophole manifests declare under `binaries`, each build verified against the
// manifest's sha256 and kept at <dir>/<sha256>/<name> with its exec bit set
// (docs/design/broker-as-a-pack.md BP-D1). `yolo pack install` fills it by download, a
// from-source `just install` seeds it with the tree's own builds of the official programs
// (packbin.Seed, BP-D15), and a launch only reads it.
//
// ⚠ NO JAIL MOUNTS THE DIRECTORY, for HostFloorDir's reason: a host daemon's binary is run
// from here by absolute path, with the user's authority, so a copy a jail could write would
// be a file the host executes because of where it sits. A jail daemon's build reaches the
// jail as ONE read-only file bind (internal/loopholes' runtime args), never through this
// directory.
func PackBinariesDir() string { return PackBinariesDirUnder(home()) }

// PackBinariesDirUnder is PackBinariesDir under an EXPLICIT home — see GlobalStorageUnder
// for why a caller that has already resolved a home must not re-derive it from $HOME.
func PackBinariesDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), packBinariesLeaf)
}

// hostFloorLeaf is the state-dir child holding the HOST AGENT FLOOR (internal/hostfloor).
const hostFloorLeaf = "host-floor"

// HostFloorDir returns $HOME/.local/share/yolo-jail/host-floor — the HOST PREFIX
// (docs/design/host-tool-provisioning.md §3): the directory yolo installs the `program`
// binaries of the user-scope selected packs into, and that `yolo host -- <agent>` execs them
// from by absolute path.
//
// ⚠ NO JAIL MOUNTS IT, IN ANY MODE, and that is the whole security property. The host
// executes these files with the user's full authority, so a copy any jail could write would
// be the injection channel EmbeddedPacksDir names: a file the host runs BECAUSE OF WHERE IT
// SITS. So it is never under GlobalCache() or GlobalMise() (both mounted read-write in every
// jail) nor any workspace's .yolo/, and it is created 0700 so the macos-user guest account,
// another uid, can neither read nor replace anything in it. A test in internal/cli/run pins
// that no mount source a launch emits is at, under, or an ancestor of it.
//
// It is also on NO user PATH: the user's shell never sees it (OQ-HP2), and a host launch
// appends its bin/ LAST to the agent's PATH (HE-D1).
func HostFloorDir() string { return HostFloorDirUnder(home()) }

// HostFloorDirUnder is HostFloorDir under an EXPLICIT home — see GlobalStorageUnder for why a
// caller that has already resolved a home must not re-derive it from $HOME.
func HostFloorDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), hostFloorLeaf)
}

// hostTreesLeaf is the state-dir child holding the host's PATCHED EXTENSION trees.
const hostTreesLeaf = "host-trees"

// HostTreesDir returns $HOME/.local/share/yolo-jail/host-trees — where `yolo host apply` keeps its
// versioned copies of each patched extension's good build, one directory per build, that the
// owned link at `~/<into>` names (docs/design/patched-extensions.md §8.3, PPX-D11).
//
// ⚠ NO JAIL MOUNTS IT, IN ANY MODE, for HostFloorDir's reason in another form: the host's agent
// loads these trees and runs their code with the user's full authority, so a copy a jail could
// write would be a jail choosing the code an unconfined agent runs, read BECAUSE OF WHERE IT SITS.
// It is created 0700, and the test that pins the floor's mount rule in internal/cli/run pins it
// too.
func HostTreesDir() string { return HostTreesDirUnder(home()) }

// HostTreesDirUnder is HostTreesDir under an EXPLICIT home.
func HostTreesDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), hostTreesLeaf)
}

// hostModelMenusLeaf is the state-dir child holding the host's MODEL MENUS (internal/modelmenu).
const hostModelMenusLeaf = "model-menus"

// HostModelMenusDir returns $HOME/.local/share/yolo-jail/model-menus — where `yolo host --`
// keeps the model menus it hands the programs it runs (docs/design/model-lists-and-pickers.md
// MM-D27): one directory per pack and program, one menu per cache key, each kept while a running
// program holds it (modelmenu.Request.WriteIn). Never the user's own config dir of the program
// (`~/.codex`), which is theirs.
//
// ⚠ NO JAIL MOUNTS IT, IN ANY MODE, for HostFloorDir's reason in another form: a menu carries the
// prompt text the host program runs its model with (codex's catalog entries hold each model's
// instructions), so a copy a jail could write would be a jail choosing the instructions of an
// agent outside every sandbox, read BECAUSE OF WHERE IT SITS. So it is never under GlobalCache()
// or GlobalMise(), and it is created 0700. The test that pins the floor's mount rule in
// internal/cli/run pins this directory too.
func HostModelMenusDir() string { return HostModelMenusDirUnder(home()) }

// HostModelMenusDirUnder is HostModelMenusDir under an EXPLICIT home — see GlobalStorageUnder
// for why a caller that has already resolved a home must not re-derive it from $HOME.
func HostModelMenusDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), hostModelMenusLeaf)
}

// imageDeliveryLeaf is the state-dir child an archive delivery works in
// (internal/image's deltaarchive.go).
const imageDeliveryLeaf = "image-delivery"

// ImageDeliveryDir returns $HOME/.local/share/yolo-jail/image-delivery — where the
// two archive backends (podman on macOS, Apple Container) assemble the OCI layout
// and the archive they hand the runtime's loader, and where Apple Container's
// delivery records live.
//
// ⚠ DELIBERATELY NOT UNDER GlobalCache(), for the reason EmbeddedPacksDir gives.
// The cache dir is bind-mounted READ-WRITE into every jail at ~/.cache, and what
// sits here decides which image the NEXT launch runs under a content ref: the
// archive's index.json names the image the loader creates, and a delivery record
// decides which blobs the next archive leaves out. The host also reads every file
// in the layout while it tars it. In cache/ a running jail could swap in its own
// manifest, or a symlink to any host file, while a delivery is in flight. The
// location is the boundary.
func ImageDeliveryDir() string { return ImageDeliveryDirUnder(home()) }

// ImageDeliveryDirUnder is ImageDeliveryDir under an EXPLICIT home — see
// GlobalStorageUnder for why a caller with an injected root needs one.
func ImageDeliveryDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), imageDeliveryLeaf)
}

// ContainerDir returns the tracking-files dir.
func ContainerDir() string { return filepath.Join(GlobalStorage(), "containers") }

// AgentsDir returns the per-jail briefing staging dir.
func AgentsDir() string { return filepath.Join(GlobalStorage(), "agents") }

// ApprovalsDir returns $HOME/.local/share/yolo-jail/approvals — where the
// last-approved config snapshot for each workspace lives, one
// <container-name>.json per workspace.
//
// HOST-SIDE IS THE WHOLE POINT (docs/reference/config-safety.md, OQ-D1). The
// snapshot is the record of what a human approved, and it used to sit at
// <workspace>/.yolo/config-snapshot.json — inside the bind mount an agent has
// read-WRITE access to. Anything that can edit yolo-jail.jsonc could therefore
// also rewrite the only record of what was last approved, and the next launch
// would show nothing to approve. A record the subject can rewrite is not a
// record. This directory is never mounted into any jail, so the approval
// baseline is out of reach by construction rather than by convention.
//
// It is a SIBLING of ContainerDir/AgentsDir and keyed the same way they are —
// by the deterministic container name runtime.FromWorkspace derives from the
// resolved workspace path — because that is already this repo's answer to "one
// small piece of host state per workspace". A second keying scheme (a path
// hash, a slugged path) would be a second thing to keep in step with the
// container name, and the reap/prune paths already speak that name.
func ApprovalsDir() string { return filepath.Join(GlobalStorage(), "approvals") }

// BrokerDir is the brokered sources' host directory, $HOME/.local/share/yolo-jail/broker:
// each source's store, its launches' scope files and the audit log
// (docs/design/boundary-broker.md §7, §8). HOST-SIDE AND UNMOUNTED, like ApprovalsDir,
// and fenced besides: with a brokered loophole's pack selected, a workspace `mounts` entry
// reaching it is refused (BB-D26), because the audit log carries argv values from every
// workspace on the machine and the scope files decide what a running broker admits.
//
// Deliberately NOT under logs/, which a `mounts` entry commonly exposes.
func BrokerDir() string { return filepath.Join(GlobalStorage(), "broker") }

// BrokerSourceDir is one source's directory under BrokerDir, such as broker/github.
func BrokerSourceDir(source string) string { return filepath.Join(BrokerDir(), source) }

// BrokerScopeFile is one fresh launch's scope file for a source:
// broker/<source>/scope/<launch-id>.json (BB-D32). Keyed by the launch, never the
// workspace, so two macos-user sessions of one workspace never share one.
func BrokerScopeFile(source, launchID string) string {
	return filepath.Join(BrokerSourceDir(source), "scope", launchID+".json")
}

// BrokerAuditLog is the append-only audit log every broker writes (BB-D15).
func BrokerAuditLog() string { return filepath.Join(BrokerDir(), "audit.jsonl") }

// PacksDir returns the machine-wide pack store: $HOME/.local/share/yolo-jail/packs.
// Packs are USER-scope (config/packs.go), so their fetched content is per-machine —
// one pack serves every workspace. Their EFFECTS (staged trees, composed files) are
// per-workspace like every other agent artifact.
func PacksDir() string { return filepath.Join(GlobalStorage(), "packs") }

// CapturesDir returns the machine-wide INSTALL-CAPTURE store:
// $HOME/.local/share/yolo-jail/captures. A vendor installer is run once in a throwaway jail,
// its delta is content-addressed here, and every workspace then materializes it by hardlink
// (program-delivery.md §6.3). So it is a SIBLING of PacksDir for the same reason PacksDir is
// machine-wide: the fetched bytes are per-machine, and only their EFFECTS are per-workspace.
// That is the whole point — `~/.local` is a per-workspace bind, so today claude's five builds
// (1.2 GB, measured 2026-09-03) are re-downloaded per workspace.
//
// The scratch root for a capture in flight lives INSIDE this directory
// (<CapturesDir>/staging/<id>), never /tmp: admission into the store is an os.Rename, and a
// scratch tree on another filesystem silently turns that into a full copy of the very
// gigabytes this subsystem exists to stop copying.
func CapturesDir() string { return CapturesDirUnder(home()) }

// CapturesDirUnder is CapturesDir under an EXPLICIT home — see GlobalStorageUnder for why a
// caller that has already resolved which home it is writing into must not re-derive it from
// $HOME.
func CapturesDirUnder(home string) string {
	return filepath.Join(GlobalStorageUnder(home), "captures")
}

// BuildDir returns the nix build-root dir.
func BuildDir() string { return filepath.Join(GlobalStorage(), "build") }

// PackageRootsDir returns $HOME/.local/share/yolo-jail/build/package-roots — where the
// durable nix GC roots for NON-CONTAINER package profiles live (the buildEnv that
// `packages:` materializes for a notch with no baked image; see internal/darwinpkg).
//
// A SIBLING of the per-image roots dir (image.ImageRootsDir, build/roots) and deliberately
// NOT the same dir: prune.PruneOrphanImageRoots enumerates every symlink under build/roots
// and reaps the ones no recently-loaded IMAGE needs, so a package-profile root parked there
// would be swept away by a routine `yolo prune --apply` — unrooting the very closure it was
// created to pin. Different lifetime, different dir.
func PackageRootsDir() string { return filepath.Join(BuildDir(), "package-roots") }

// StoreSamplesDir returns $HOME/.local/share/yolo-jail/stores — the bounded
// sample ledger `yolo stores` writes, one <store-key>.samples file per store,
// one dated line per store per run (docs/design/disk-levers-and-backfill.md
// OQ-BF9).
//
// A DEDICATED LEAF, and a small one by construction: the ledger is capped at the
// last 30 samples per store, so the handle on growth cannot itself become a
// store that grows. `yolo stores` is its only writer — nothing on the launch
// path, and nothing in internal/prune, reads or writes here.
func StoreSamplesDir() string { return filepath.Join(GlobalStorage(), "stores") }

// FlakeBundleDir is the STABLE path at which a from-source `just install`
// publishes the self-contained flake bundle (flake.nix + flake.lock + prebuilt
// bin/linux-<arch>/) so an installed `yolo` builds the jail image with no source
// checkout — the "installs are self-contained" guarantee. reporoot.Resolve
// consults it.
//
// It is a SYMLINK into flake-bundles/<stamp>, not a directory an install
// rewrites: a launch mounts <bundle>/bin/linux-<arch> into the jail as its yolo
// binaries, and a bind mount pins an inode, so staging over this path deleted
// pid1 out from under every running jail. internal/flakebundle owns the
// generations and the swap; nothing that merely READS the bundle needs to know,
// which is why this stayed one path.
//
// It is a DEDICATED LEAF under GlobalStorage, deliberately NOT derived from the
// binary's install dir. The first cut computed it as $(dirname $GOBIN)/share/
// yolo-jail, which for the common GOBIN=~/.local/bin collapses onto
// GlobalStorage() itself ($HOME/.local/share/yolo-jail) — and the staging script
// leads with `rm -rf $DEST`, so `just install` deleted the whole state dir. A
// fixed leaf under GlobalStorage can never equal GlobalStorage, so that class of
// collision is structurally impossible.
func FlakeBundleDir() string { return filepath.Join(GlobalStorage(), "flake-bundle") }

// UpdateCheckDir returns $HOME/.local/share/yolo-jail/update-check: the cached
// answer of the last update check and the lock a background check holds
// (internal/selfupdate).
//
// A DEDICATED LEAF rather than a file under state/, because state/ is not a
// general state dir: it is the loophole state root, keyed by loophole NAME
// (loopholes.StateDirFor) and swept by loophole retirement, so anything else
// placed there squats in that namespace. Never under cache/, which every jail
// mounts read-write — a jail must not be able to write the notice the host
// prints, nor the "declined" flag that decides whether the host prompts.
func UpdateCheckDir() string { return filepath.Join(GlobalStorage(), "update-check") }

// UserConfigPath returns $HOME/.config/yolo-jail/config.jsonc (or config.json if config.jsonc is absent).
func UserConfigPath() string {
	p := filepath.Join(home(), userConfigSuffix)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	jsonP := filepath.Join(home(), ".config/yolo-jail/config.json")
	if _, err := os.Stat(jsonP); err == nil {
		return jsonP
	}
	return p
}

// LocalPackDir returns $HOME/.config/yolo-jail/local — the CONVENTIONAL LOCAL PACK: an
// implicitly-included pack for the user's own skills and briefing prose, needing no `packs`
// entry (roadmap.md §6a-2).
//
// Beside config.jsonc, and derived from it, because that is already where user-scope yolo
// config lives: the convention EXTENDS an existing one rather than inventing a second
// user-scope location to remember. A user with three personal skills should never have to
// author a manifest, and `packload.LoadDir` on a dir with no pack.json is already
// zero-ceremony — so the whole feature is a path plus a place in the pack order.
//
// ABSENT IS NORMAL. Most users will not have this directory, and its absence must cost
// nothing: the one caller (config.LoadPacks) stats it once per load and appends no entry
// when it is not a directory. That is why this returns a path and answers no question
// about existence — a helper that reported "present" would invite a second stat.
func LocalPackDir() string { return filepath.Join(filepath.Dir(UserConfigPath()), localPackLeaf) }

// WorkspaceFilesDir returns $HOME/.config/yolo-jail/workspaces: the folder of PER-WORKSPACE
// FILES, one per workspace, each holding switches that apply to that workspace alone
// (internal/config/workspacefile.go; docs/design/boundary-broker.md BB-D53). `yolo loopholes
// enable|disable` writes them, so a per-project switch never edits config.jsonc.
//
// Beside config.jsonc, and derived from it, for LocalPackDir's reason. No jail sees it: a launch
// binds the user scope it generates into a jail as single files, never this directory
// (internal/cli/run/inheritscope.go, R8).
func WorkspaceFilesDir() string {
	return filepath.Join(filepath.Dir(UserConfigPath()), workspaceFilesLeaf)
}

// Home returns the resolved home directory (see home() for the Python-parity
// resolution rules). Exported for callers that must expand a leading "~/" in a
// user-scope path, e.g. a surface manifest's Path.
func Home() string { return home() }

// home Path.home() / os.path.expanduser("~") resolution,
// which the paths constants depend on — NOT Go's os.UserHomeDir(), which reads
// only $HOME and errors when it is unset (audit finding: that made every path
// helper return a RELATIVE path in a stripped environment). Python's rules:
//
// - $HOME set and non-empty -> $HOME
// - $HOME set but empty -> "/" (expanduser: userhome="" then `or "/"`)
// - $HOME unset -> pwd.getpwuid(getuid()).pw_dir (the passwd
// database home), and if THAT is empty, "/"
//
// This keeps the paths absolute in cron/systemd/subprocess contexts where the
// CLI may run without $HOME, matching Python.
func home() string {
	h, ok := os.LookupEnv("HOME")
	if ok {
		if h == "" {
			return "/" // Python expanduser: empty HOME -> "/"
		}
		return h
	}
	// HOME unset: fall back to the passwd database (Python's pwd.getpwuid).
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return "/"
}

// JailPathHomeDirs are the directories UNDER THE JAIL'S HOME that sit on the jail's
// PATH, home-relative and slash-separated.
//
// It exists so a pack MANIFEST can be told it is naming one. `program` is the kind
// that puts a name on PATH — it owns the launcher filename, it is exclusive by that
// name, and it is disclosed at launch. A `files` tree or a `state` subtree landing on
// PATH reaches the same result by POSITION, which declares nothing, collides silently
// with whatever else provides the name, and leaves `yolo pack footprint` describing a
// pack that does something it never said. Refusing the destination is how a pack author
// finds `program` instead.
//
// IT IS NOT A SANDBOX AND MUST NOT BE DESCRIBED AS ONE. A pack has louder ways to run
// code — `program via installer` fetches and runs a remote script, a `loophole` runs a
// daemon on the HOST — and each is disclosed and approved rather than blocked. This is a
// naming rule: the mechanism exists, so use it. (It is also why the host notch, where
// `yolo host apply` renders into a real home whose PATH yolo does not know, gets only the
// overlap — `.local/bin` — and no pretence of completeness.)
//
// The authority for the CONTENTS is entrypoint.BootPath; the two are pinned together by
// TestJailPathHomeDirsCoversBootPath, so a PATH that gains a home directory cannot leave
// this list silently short. mise's shims, /bin and /usr/bin are absent because they are
// not under the home and a manifest path cannot name them (appendPathProblems already
// refuses an absolute destination).
var JailPathHomeDirs = []string{
	".local/bin",       // entrypoint.Env.LocalBin
	".npm-global/bin",  // entrypoint.Env.NpmBin (NPM_CONFIG_PREFIX/bin)
	".yolo/bin/block",  // entrypoint.Env.BlockDir — blockers, PATH head
	".yolo/bin/launch", // entrypoint.Env.LaunchDir — lazy installers, PATH head (B2)
	"go/bin",           // entrypoint.Env.GoBin (GOPATH/bin)
}

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
	// surface (docs/design/image-staging-vs-baking.md §4 C2).
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
	// internal/svcendpoint and docs/design/loophole-transport.md §3.2.
	JailHostServicesDir = "/run/yolo-services"

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
// (docs/design/loophole-packaging.md §3.1).
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
// fail a launch (docs/design/loopback-tls-reachability.md, OQ-R2 as scoped by
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

// AllowUnreachableServicesEnv is the escape hatch out of the in-jail reachability
// witness, mirroring YOLO_ALLOW_STALE_IMAGE (internal/image): any non-empty value
// keeps the jail launching, loudly, and says what it is suppressing.
//
// It exists because the witness is on its way to being FATAL (OQ-R2), and a hard
// fatal with no override leaves a user unable to open a shell to fix the very
// daemon that is failing. The user types it on the HOST, so the launcher forwards
// it into the container — an escape hatch nobody can reach is not one.
const AllowUnreachableServicesEnv = "YOLO_ALLOW_UNREACHABLE_SERVICES"

// AllowMissingProvidersEnv is the escape hatch out of the selected-pack credential
// pre-flight (profiles-as-pack-variants.md §6.2 as rescoped by OQ-13): any non-empty
// value keeps the launch going, loudly, and says what it is suppressing. The named-var
// const is the same convention AllowUnreachableServicesEnv is — the producer (this CLI)
// and every consumer read one spelling, so a hatch cannot drift out of reach by a
// re-typing. Both notches that refuse read it host-side (internal/cli/run and the
// `yolo host` exec half), so — unlike the reachability witness — nothing has to forward
// it into the container.
const AllowMissingProvidersEnv = "YOLO_ALLOW_MISSING_PROVIDERS"

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
// constants these generalize (internal/broker; a test pins the three pairs equal,
// because `yolo broker status`, `yolo check` and the run pipeline's front all have
// to reach one file).
func HostSingletonSocket(loopholeName string) string {
	return "/tmp/yolo-" + loopholeName + ".sock"
}

// HostSingletonPIDFile returns the singleton's PID file — see HostSingletonSocket.
func HostSingletonPIDFile(loopholeName string) string {
	return "/tmp/yolo-" + loopholeName + ".pid"
}

// HostSingletonLock returns the singleton's spawn lock — see HostSingletonSocket.
// It is the flock two concurrent launches contend for, so that the loser observes
// the winner's daemon instead of starting a second one.
func HostSingletonLock(loopholeName string) string {
	return "/tmp/yolo-" + loopholeName + ".lock"
}

// HostServicesDir returns the per-jail host-side directory holding this jail's
// published endpoint files: /tmp/yolo-host-services-<8hex>.
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
func HostServicesDir(cname string, isMacOS bool) string {
	base := "/tmp"
	if isMacOS {
		if r, err := filepath.EvalSymlinks(base); err == nil {
			base = r
		}
	}
	return filepath.Join(base, HostServicesDirName(JailShortHash(cname)))
}

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
)

// GlobalStorage returns $HOME/.local/share/yolo-jail.
func GlobalStorage() string { return GlobalStorageUnder(home()) }

// GlobalStorageUnder returns the state dir under an EXPLICIT home, rather than the
// process $HOME. It exists because a caller that has ALREADY resolved which home it is
// writing into must not re-derive it from the environment: `yolo host apply` renders into a
// home it was handed (render.Target.Home), and a state path computed from $HOME instead
// would land in the invoking user's REAL state dir the moment the two differ — which is
// exactly what every test with a t.TempDir() home does. Keying on the passed home makes
// that class of mistake impossible rather than merely avoided.
func GlobalStorageUnder(home string) string { return filepath.Join(home, globalStorageSuffix) }

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

// HomeSurfaces returns the three per-workspace home dirs that hold INSTALLED PROGRAMS —
// the npm prefix, `~/.local` (where `~/.local/bin` and the vendor installers' version dirs
// land) and `$GOPATH`.
//
// This is one list serving two subsystems that must agree about it or be wrong together:
//
//   - `prune` hardlink-dedups exactly these three per workspace (`WalkDedupableWorkspaces`),
//     because they are where the same bytes get downloaded again for every workspace;
//   - `capture` walks exactly these three for its baseline and its delta
//     (program-delivery.md §6.3), because they are where an installer's output lands.
//
// Those are the same set for the same reason, so a fourth spelling of it is a bug waiting
// for someone to add a surface to one and not the other. The jail's OTHER per-workspace
// binds — `yolo-bin`, `config`, `.cache` — are deliberately absent: they hold GENERATED or
// CACHED content that is rebuilt or refetched rather than installed, so deduping them buys
// little and capturing them would file yolo's own output as a vendor's.
//
// The order is the bind order in the podman argv (`run/assemble_parts.go`), kept so a
// human reading either list beside the other sees the same sequence.
func HomeSurfaces() []HomeSurface {
	return []HomeSurface{
		{Subtree: "npm-global", HomeRel: ".npm-global"},
		{Subtree: "local", HomeRel: ".local"},
		{Subtree: "go", HomeRel: "go"},
	}
}

// GeneratedBinDir returns $HOME/.local/share/yolo-jail/bin — the parent of every
// directory of yolo-GENERATED executables. It is a gathering point in the FILESYSTEM
// only: its children sit at opposite ends of PATH by design (blockers first, launchers
// last), so nothing may ever put this parent on PATH and pick up all three at once.
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

// GlobalMise returns the shared mise data dir.
func GlobalMise() string { return filepath.Join(GlobalStorage(), "mise") }

// GlobalCache returns the shared cache dir.
func GlobalCache() string { return filepath.Join(GlobalStorage(), "cache") }

// ContainerDir returns the tracking-files dir.
func ContainerDir() string { return filepath.Join(GlobalStorage(), "containers") }

// AgentsDir returns the per-jail briefing staging dir.
func AgentsDir() string { return filepath.Join(GlobalStorage(), "agents") }

// ApprovalsDir returns $HOME/.local/share/yolo-jail/approvals — where the
// last-approved config snapshot for each workspace lives, one
// <container-name>.json per workspace.
//
// HOST-SIDE IS THE WHOLE POINT (docs/design/config-safety.md, OQ-D1). The
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

// FlakeBundleDir is where a from-source `just install` stages the self-contained
// flake bundle (flake.nix + flake.lock + prebuilt bin/linux-<arch>/) so an
// installed `yolo` builds the jail image with no source checkout — the
// "installs are self-contained" guarantee. reporoot.Resolve consults it.
//
// It is a DEDICATED LEAF under GlobalStorage, deliberately NOT derived from the
// binary's install dir. The first cut computed it as $(dirname $GOBIN)/share/
// yolo-jail, which for the common GOBIN=~/.local/bin collapses onto
// GlobalStorage() itself ($HOME/.local/share/yolo-jail) — and the staging script
// leads with `rm -rf $DEST`, so `just install` deleted the whole state dir. A
// fixed leaf under GlobalStorage can never equal GlobalStorage, so that class of
// collision is structurally impossible.
func FlakeBundleDir() string { return filepath.Join(GlobalStorage(), "flake-bundle") }

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
	".yolo/bin/launch", // entrypoint.Env.LaunchDir — lazy installers, PATH tail
	"go/bin",           // entrypoint.Env.GoBin (GOPATH/bin)
}

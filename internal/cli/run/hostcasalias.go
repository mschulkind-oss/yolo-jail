package run

// hostcasalias.go is the HOST half of L9 — the launch's decision about whether a
// recognised host cache is ALIASED from the host instead of pooled a second time
// inside the jail (docs/reference/disk-footprint.md "The levers" L9, ruled as
// OQ-BF10 on 2026-09-08 for pants' content-addressed store and widened to package
// caches by OQ-BF11 on 2026-10-22).
//
// The DECISION and its justification live in internal/hostcas, which `yolo
// stores` reads too. This file is the binding: it turns the launch's own seams
// into hostcas.Facts, provisions the destination's backing directory, and
// discloses what it did. It contains no gate of its own, so the mount a launch
// emits and the row `yolo stores` prints cannot disagree.
//
// THE DECISION IS NOT RECORDED ANYWHERE, on purpose. It is a pure function of
// the host's filesystem and platform, so both readers re-derive it; a stamp under
// BuildDir() would be a second source of truth for a question the disk already
// answers, and it would go stale the first time a user deleted their pants cache.
// That is also why this file names no writer: there is nothing written.

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// planHostCASAlias settles L9 for this launch: one hostcas.Disposition per
// recognised store, aliased or not, with the reason.
//
// It never refuses a launch and returns no error, because there is no failure
// here that a jail should not start through. Every negative outcome is the status
// quo — the jail keeps pooling its own copy — which is exactly the behaviour of
// every yolo that shipped before this.
// relocations are the user's `cache_relocations`, already loaded by the
// pipeline. They are passed IN rather than read here because an explicit config
// decision about where a cache lives outranks this optimisation, and the gate
// that enforces that lives in hostcas with the rest of them.
//
// wsState is the workspace's home state — the host side of the jail's ~/.local
// and ~/go binds, under which the module cache's and pnpm store's private
// copies live.
func (o *Options) planHostCASAlias(rt string, relocations []config.CacheRelocation, wsState string) []hostcas.Disposition {
	relocated := make([]string, 0, len(relocations))
	for _, r := range relocations {
		relocated = append(relocated, r.Subdir)
	}
	return hostcas.Plan(hostcas.Facts{
		Runtime: rt,
		IsMacOS: o.IsMacOS,
		// The HOST's platform, and the JAIL's, spelled as two values so the
		// comparison in hostcas is over facts rather than over an OS check.
		HostPlatform: hostcas.HostPlatform(),
		JailPlatform: hostcas.JailPlatform(),
		// OQ-BF11's ownership gate, from the launch's ONE `podman info` answer (the
		// readiness gate's). No answer is unknown, which declines every path-keyed
		// store. A nested launch's podman reports rootful (it runs on --userns host),
		// so a jail-in-a-jail declines them too; the rootless path is CI's to verify.
		Ownership: o.hostCASOwnership(rt),
		// Through o.Getenv, never os.Getenv: a frozen argv has to stay a function
		// of its inputs. goldenOptions returns "" for every variable, which is what
		// keeps every golden argv in this package free of an alias mount without
		// any fixture having to know this feature exists.
		HostCacheRoot:     hostcas.CacheRoot(o.Getenv),
		HostGetenv:        o.Getenv,
		JailCacheHost:     paths.GlobalCache(),
		JailLocalHost:     homeSurfaceHost(wsState, ".local"),
		JailGoPathHost:    homeSurfaceHost(wsState, "go"),
		JailMiseHost:      jailMiseStoreDir(o.inJail()),
		RelocatedSegments: relocated,
		Probe:             o.HostCASProbe,
		ScopeBreach:       scopeBreached,
	})
}

// planMacosUserHostCAS is planHostCASAlias for the macos-user arm (MMP-D12): the same
// hostcas.Plan, over the macOS rows, with the MODE as its input. Only the run-as-you mode
// passes hostcas' backend gate, and only it states the ownership fact — the sandbox IS the
// invoking user, so a file it writes into the host's cache is the user's by construction.
// The account mode (the zero value, every launch today) leaves both unset, and every store
// declines with CodeBackend.
//
// The destination is the sandbox's HOME on the host — the workspace's home state in the
// run-as-you mode (MMP-D5) — and the platform pair is this Mac's against the sandbox's,
// darwin on this machine's architecture. relocations are the user's `cache_relocations` the
// arm already planned (planMacosUserCacheRelocations), so an alias never puts a relocated
// ~/.cache segment back under the home. Under the seal there are none: the arm returns before
// this is called.
func (o *Options) planMacosUserHostCAS(mode macosuser.Mode, relocations []macosuser.CacheRelocation) []hostcas.Disposition {
	return hostcas.Plan(o.macosUserHostCASFacts(mode, relocations))
}

// macosUserHostCASFacts are the facts planMacosUserHostCAS plans from, which
// resolveMacosUserHostCAS judges each resolved source against again.
func (o *Options) macosUserHostCASFacts(mode macosuser.Mode, relocations []macosuser.CacheRelocation) hostcas.Facts {
	relocated := make([]string, 0, len(relocations))
	for _, r := range relocations {
		relocated = append(relocated, r.Subdir)
	}
	self := mode == macosuser.ModeSelf
	ownership := hostcas.OwnershipUnknown
	sandboxHome, machine := macosuser.SandboxHome(), ""
	if self {
		ownership = hostcas.OwnershipYours
		homes := macosuser.LaunchHomes(mode, o.homeDir(), o.macosUserHostHome())
		sandboxHome, machine = homes.Home, homes.Machine
	}
	hostPlatform := o.macosHostCASPlatform
	if hostPlatform == "" {
		hostPlatform = hostcas.HostPlatform()
	}
	return hostcas.Facts{
		Runtime:           "macos-user",
		MacosUserSelf:     self,
		IsMacOS:           o.IsMacOS,
		HostPlatform:      hostPlatform,
		JailPlatform:      hostcas.DarwinJailPlatform(),
		Ownership:         ownership,
		HostCacheRoot:     hostcas.CacheRoot(o.Getenv),
		HostGetenv:        o.Getenv,
		MacosUserHome:     sandboxHome,
		MacosUserMachine:  machine,
		RelocatedSegments: relocated,
		Probe:             o.HostCASProbe,
		ScopeBreach:       scopeBreached,
	}
}

// macosUserHostHome is the invoking user's home a run-as-you macos-user launch places its homes
// under: the one cli resolved (MacosUserHostHome), else HOME as Options reads it, else this
// process's.
func (o *Options) macosUserHostHome() string {
	if o.MacosUserHostHome != "" {
		return o.MacosUserHostHome
	}
	if o.Getenv != nil {
		if h := o.Getenv("HOME"); h != "" {
			return h
		}
	}
	return paths.Home()
}

// macosSiting is the macOS layout the macos-user context-mount and cache-relocation siting read:
// o.macosCtxSiting when a test set it, else a default macOS install's for this launch's mode
// (macosuser.DarwinContextSitingFor: the run-as-you mode reaches your home, the account mode does
// not).
func (o *Options) macosSiting() macosuser.ContextSiting {
	if o.macosCtxSiting != nil {
		return *o.macosCtxSiting
	}
	return macosuser.DarwinContextSitingFor(o.MacosUserMode, o.macosUserHostHome())
}

// resolveMacosUserHostCAS resolves every aliased source through its symlinks, as the host
// sees it, because the profile's `cache-alias-allow` names the target verbatim and Seatbelt
// judges the resolved path. A source that cannot be resolved (it went away after the probe) is
// DOWNGRADED to absent rather than handed over unresolved, the direction every failure here
// takes: the sandbox keeps its own copy.
//
// AND THE RESOLVED TARGET IS JUDGED AGAIN (hostcas.ResolvedSourceProblem against f, the facts
// the plan was made from): Plan read the path as spelled, so a cache directory replaced by a
// link to the home, ~/.config or a boundary directory would otherwise be opened read-write
// under its resolved name. Such a source is declined as unsafe.
func resolveMacosUserHostCAS(ds []hostcas.Disposition, f hostcas.Facts) []hostcas.Disposition {
	out := make([]hostcas.Disposition, 0, len(ds))
	for _, d := range ds {
		if d.Aliased {
			if resolved, err := filepath.EvalSymlinks(d.Source); err == nil {
				if why := hostcas.ResolvedSourceProblem(resolved, f); why != "" {
					d.Aliased = false
					d.Code = hostcas.CodeUnsafeSource
					d.Reason = "the host " + d.Store.Tool + " cache path " + d.Source + " resolves to " +
						resolved + ", which " + why + ", so it is not shared"
				} else {
					d.Source = resolved
				}
			} else {
				d.Aliased = false
				d.Code = hostcas.CodeAbsent
				d.Reason = "the host " + d.Store.Tool + " store at " + d.Source +
					" could not be resolved: " + err.Error()
			}
		}
		out = append(out, d)
	}
	return out
}

// macosUserCacheAliases is what the macos-user backend is handed for this launch's aliased
// stores (HostContext.CacheAliases): a link at the destination to the resolved source.
func macosUserCacheAliases(ds []hostcas.Disposition) []macosuser.CacheAlias {
	var out []macosuser.CacheAlias
	for _, d := range hostcas.Aliased(ds) {
		out = append(out, macosuser.CacheAlias{Tool: d.Store.Tool, Link: d.Dest, Target: d.Source})
	}
	return out
}

// scopeBreached is the credential-boundary rule a read-write `mounts` source runs
// (paths.WritableSourceScopeBreach), handed to hostcas for env-derived host paths.
func scopeBreached(p string) bool { return paths.WritableSourceScopeBreach(p) != nil }

// hostCASOwnership is podman's rootless answer as an ownership fact: only a
// podman launch whose readiness gate parsed an answer naming rootless can say.
func (o *Options) hostCASOwnership(rt string) hostcas.Ownership {
	if rt != "podman" || o.podmanFacts == nil || !o.podmanFacts.parsed { // parity: NotApplicable — rootless is a podman fact; macos-user states its own ownership from its mode (planMacosUserHostCAS), and hostcas declines every other backend before ownership is read
		return hostcas.OwnershipUnknown
	}
	return hostcas.OwnershipOf(o.podmanFacts.info.Host.Security.Rootless)
}

// homeSurfaceHost is the host side of a per-workspace home bind, read from the
// same list podmanBaseMounts emits (paths.HomeSurfaces), so the stranded path and
// the bind cannot disagree. "" without a wsState or for a name not on the list.
func homeSurfaceHost(wsState, homeRel string) string {
	if wsState == "" {
		return ""
	}
	for _, s := range paths.HomeSurfaces() {
		if s.HomeRel == homeRel {
			return filepath.Join(wsState, s.Subtree)
		}
	}
	return ""
}

// prepareHostCASAlias creates the DESTINATION's mountpoint for every aliased
// store, before the argv is assembled.
//
// THE MOUNTPOINT AND THE STRANDED COPY ARE THE SAME DIRECTORY, and that is worth
// reading twice: the destination /home/agent/.cache/<rel> sits inside the
// paths.GlobalCache() bind, so its host side is GlobalCache()/<rel> — which is
// exactly where the jail's own private copy already is. The alias mounts over
// the private copy IN PLACE, which is what makes the migration question
// ([Disposition.Stranded]) answerable at all: nothing is moved or deleted, the
// bytes are simply no longer the ones the jail reads.
//
// BOTH HALVES OF cache_relocations' ordering rule are MEASURED here, on podman
// 5.8.4, 2026-09-09, against a real `localhost/yolo-jail` image:
//
//   - a missing bind SOURCE kills the whole container with a bare
//     "statfs …: no such file or directory". That is why hostcas declines on
//     absence rather than creating anything.
//   - a missing DESTINATION does get auto-created by crun, two levels deep,
//     inside the read-write .cache bind — so this MkdirAll is not what makes the
//     mount work. What it changes is what the mountpoint IS: crun's is root-owned
//     mode 0755 with the STICKY BIT set (drwxr-xr-t), left behind in the user's
//     cache after the jail exits. One MkdirAll makes it an ordinary directory the
//     jail already owns, which is the same reason podmanBaseMounts pre-creates
//     its own nested mountpoints.
//
// It never touches the SOURCE. Creating a missing host store would alias an empty
// directory over whatever the jail has, which is the one outcome strictly worse
// than doing nothing — so hostcas declines on absence and this is only ever
// reached for stores that already exist.
//
// A failure is NOT fatal, unlike EnsureCacheRelocations': the alias is an
// optimisation whose failure mode is the status quo, and refusing a launch over
// it would trade a working jail for a preference about where bytes live. A
// disposition that could not be provisioned is DOWNGRADED rather than dropped, so
// no argv names a destination whose mountpoint is missing and the inventory can
// still explain what happened.
func prepareHostCASAlias(ds []hostcas.Disposition) []hostcas.Disposition {
	out := make([]hostcas.Disposition, 0, len(ds))
	for _, d := range ds {
		if !d.Aliased {
			out = append(out, d)
			continue
		}
		switch {
		case d.Stranded == "":
			d.Aliased = false
			d.Code = hostcas.CodeSameTree
			d.Reason = "the jail's own cache directory could not be resolved, so there is " +
				"no mountpoint to alias over"
		default:
			if err := os.MkdirAll(d.Stranded, 0o755); err != nil {
				d.Aliased = false
				d.Code = hostcas.CodeUnwritable
				d.Reason = "the mountpoint " + d.Stranded + " could not be created: " + err.Error()
			}
		}
		out = append(out, d)
	}
	return out
}

// noteHostCASAlias discloses this launch's L9 decision on stderr, in the same
// dim register and at the same point as notePackHostAccess — because it is the
// same class of fact. A WRITABLE BIND OF THE HOST USER'S OWN CACHE IS HOST ACCESS,
// and yolo's convention is that effective host access is visible at every launch
// rather than recorded once somewhere.
//
// Two things print, and nothing else:
//
//   - an ACTIVE alias, always. This is the disclosure, and it is not suppressible:
//     the trust step is the writable direction, so the launch says what it granted
//     and where.
//   - a decline the user could ACT ON — the host store is there but unwritable,
//     is not a directory, is empty over a warm private copy, or its mountpoint
//     could not be made. That is a happy path that degraded, and §5.4's failure
//     discipline is that a degradation is never silent.
//
// EVERY OTHER DECLINE PRINTS NOTHING, and the line between the two is "is there
// anything to fix?". A machine that does not run the tool, a backend or a host OS
// that cannot do this at all, no resolvable cache root, or a `cache_relocations`
// entry the user WROTE — none of those is a degradation, they are structural
// facts about this machine, and none has an action behind it. §5.3's own
// degenerate rule ("an empty store or a class with 0 B reclaimable is silent") is
// the precedent, and §5.1's warning about the slot printing over a running
// agent's TUI is what the noise would cost — restating the absence of a pants
// cache on every launch of every jail on every machine is not disclosure.
// `yolo stores` is where every decline is legible, on demand.
//
// The relocation case is the one worth naming explicitly, because it is the only
// silent decline the USER caused: they moved that cache somewhere on purpose, so
// honoring it is the expected outcome rather than a loss to report.
func (o *Options) noteHostCASAlias(ds []hostcas.Disposition) {
	var active, degraded []hostcas.Disposition
	for _, d := range ds {
		switch {
		case d.Aliased:
			active = append(active, d)
		case d.Code == hostcas.CodeUnwritable || d.Code == hostcas.CodeNotDir ||
			d.Code == hostcas.CodeWouldColdStart:
			degraded = append(degraded, d)
		}
	}
	if len(active) == 0 && len(degraded) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	// ONE alias reads as it always did. Several (OQ-BF11's package caches) are
	// still each named with both paths — the disclosure is per alias — but under
	// one heading, so a machine with every tool installed gets a compact block
	// rather than a dozen repeated sentences.
	if len(active) == 1 {
		d := active[0]
		out.print("[dim]Host cache aliased (writable): " + d.Source + " → " + d.Dest +
			" — this jail shares the host's " + d.Store.Tool +
			" store instead of keeping a second copy.[/dim]")
	} else if len(active) > 1 {
		out.print("[dim]Host caches aliased (writable) — this jail shares these host stores " +
			"instead of keeping second copies:[/dim]")
		for _, d := range active {
			out.print("[dim]  " + d.Store.Tool + ": " + d.Source + " → " + d.Dest + "[/dim]")
		}
	}
	for _, d := range degraded {
		out.printf("[yellow]Not aliasing the host %s store:[/yellow] %s. "+
			"[dim]This jail keeps its own copy at %s, as before.[/dim]",
			d.Store.Tool, d.Reason, d.Stranded)
	}
}

// hostCASAliasArgs is the -v pairs for this launch's aliased stores. Emitted by
// podmanBaseMounts, adjacent to the cache_relocations block it is the same shape
// as: a rw bind nested INSIDE the paths.GlobalCache() → /home/agent/.cache mount,
// so ~/.cache/<rel> in the jail is an ordinary writable directory backed by other
// storage. Table order, so the argv is deterministic.
func hostCASAliasArgs(ds []hostcas.Disposition) []string {
	var args []string
	for _, d := range hostcas.Aliased(ds) {
		args = append(args, "-v", d.Source+":"+d.Dest)
	}
	return args
}

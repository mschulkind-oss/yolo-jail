package loopholes

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// yolo-jail.jsonc loopholes: entries as Loophole records (lifecycle spawned,
// source config).
//
// A config entry's daemon is a THIRD-PARTY PROGRAM yolo did not write: it binds
// an AF_UNIX socket at the path substituted into its `command`. That used to
// force the retired unix-socket transport onto these records, because nothing
// yolo shipped let such a daemon publish a loopback-TLS endpoint file
// (internal/hostservice is internal/, unimportable from outside this module).
// The FRONT dissolved that objection (loophole-packaging.md §2.2): the record
// now says what is true of the daemon — Transport loopback-tls with
// HostDaemon.Publishes = "socket" — and the run pipeline waits for the daemon's
// socket by connect, runs the svcendpoint front over it, and publishes the
// endpoint file itself. The argv is unchanged, the daemon's behaviour is
// unchanged, and the jail gains a real endpoint (YOLO_SERVICE_<NAME>_ENDPOINT +
// the mounted endpoint file) exactly like a manifest loophole's.
//
// An entry with NO `command` runs no daemon, so it gets TransportNone — which
// is what that value means — rather than advertising a transport nothing serves.
func synthesizeConfigLoopholes(loopholesConfig *jsonx.OrderedMap) []*Loophole {
	out := []*Loophole{}
	if loopholesConfig == nil {
		return out
	}
	for _, name := range loopholesConfig.Keys() {
		specV, _ := loopholesConfig.Get(name)
		spec, ok := specV.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		description := ""
		if dv, ok := spec.Get("description"); ok && loopholedecl.Truthy(dv) {
			description = loopholedecl.Str(dv)
		}
		enabled := true
		if ev, ok := spec.Get("enabled"); ok {
			enabled = loopholedecl.Truthy(ev)
		}
		doctorCmd, doctorSet := []string(nil), false
		if dcv, ok := spec.Get("doctor_cmd"); ok {
			if list, isList := dcv.([]any); isList && loopholedecl.AllStrings(list) {
				doctorCmd = loopholedecl.StringSlice(list)
				doctorSet = true
			}
		}
		transport := TransportNone
		var hostDaemon *HostDaemon
		if cv, ok := spec.Get("command"); ok {
			if list, isList := cv.([]any); isList && len(list) > 0 && loopholedecl.AllStrings(list) {
				// The preamble defaults OFF here and ON for a manifest, and the
				// asymmetry is the point rather than an oversight. A manifest is
				// a declaration written against yolo's transport, so its author
				// can be asked to say `"preamble": false` if the daemon is a dumb
				// pipe. A config entry is not: it is an argv for a THIRD-PARTY
				// PROGRAM (see the note at the top of this file) that is already
				// running for somebody, with a protocol that has no room for a
				// frame it never asked for. Defaulting ON would prepend bytes to
				// a working setup on the strength of a key its author never saw.
				// `"preamble": true` is the opt-in.
				preamble := false
				if pv, ok := spec.Get("preamble"); ok {
					preamble = loopholedecl.Truthy(pv)
				}
				hostDaemon = &HostDaemon{
					Cmd:        loopholedecl.StringSlice(list),
					Env:        NewEnvMap(),
					Publishes:  PublishesSocket,
					RequestEnd: RequestEndFramed,
					Preamble:   preamble,
					// SPELLED OUT rather than left to the zero value, even though ""
					// and ScopeJail behave identically at every reader. A config
					// entry is an argv for a program yolo did not write, running on
					// this user's machine — the one kind of daemon that must never
					// become a host-wide singleton, because "ensure this" means yolo
					// declining to start a process the config asked it to start.
					// There is no config key for it, and writing the answer here is
					// what makes that absence a decision rather than an omission.
					Scope: ScopeJail,
				}
				transport = TransportLoopbackTLS
			}
		}
		out = append(out, &Loophole{
			Name:         name,
			Description:  description,
			Path:         "<yolo-jail.jsonc:loopholes." + name + ">",
			Enabled:      enabled,
			Transport:    transport,
			Lifecycle:    "spawned",
			Intercepts:   []Intercept{},
			BrokerIP:     DefaultBrokerIP,
			JailEnv:      NewEnvMap(),
			DoctorCmd:    doctorCmd,
			DoctorCmdSet: doctorSet,
			HostDaemon:   hostDaemon,
			Source:       SourceConfig,
		})
	}
	return out
}

// ConfigEnabledOverride reports what the merged config says about one loophole's
// `enabled` key: the value, and whether the key was written at all.
//
// THE USER'S SWITCH, and the one place its rule is stated. `loopholes.<name>.enabled`
// is a different key from the manifest's `default_enabled` — the pack author's default
// versus the user's answer (docs/design/loophole-activation.md OQ-A9) — and it outranks
// the default in BOTH directions, from either scope, because the merged block this reads
// is already user-config-plus-workspace with the workspace winning.
//
// Exported and factored out because it has TWO readers that must not drift:
// applyWorkspaceOverrides, which resolves the record a launch acts on, and `yolo check`,
// which reports what that launch will do. It used to have one, and the reporting surface
// answered off the manifest default alone — so a loophole the user had switched ON
// rendered as the greenest line in the section (`[PASS] loophole X: disabled`) and its
// host daemon was never probed. The absent second reader is what made that possible;
// a shared rule is what stops it coming back.
//
// The "set" bool is load-bearing: "the config said true" and "the config said nothing"
// are different answers, and only the second may leave the author's default standing.
//
// COERCED WITH Truthy rather than type-checked, deliberately and unlike the manifest's
// `default_enabled`. It is what the launch path has always done here, so tightening it
// would change which configs activate — and the config key HAS a type check already, one
// layer up in internal/config (`enabled: expected a boolean`), which refuses the sloppy
// spelling before this ever sees it.
func ConfigEnabledOverride(loopholesConfig *jsonx.OrderedMap, name string) (enabled bool, set bool) {
	if loopholesConfig == nil {
		return false, false
	}
	specV, ok := loopholesConfig.Get(name)
	if !ok {
		return false, false
	}
	spec, isMap := specV.(*jsonx.OrderedMap)
	if !isMap {
		return false, false
	}
	enabledV, present := spec.Get("enabled")
	if !present {
		return false, false
	}
	return loopholedecl.Truthy(enabledV), true
}

// matching entries of `existing` in place and returns the NEW inline loopholes
// (in document order) that matched nothing.
func applyWorkspaceOverrides(existing map[string]*Loophole, loopholesConfig *jsonx.OrderedMap) []*Loophole {
	newInline := []*Loophole{}
	if loopholesConfig == nil {
		return newInline
	}
	for _, name := range loopholesConfig.Keys() {
		specV, _ := loopholesConfig.Get(name)
		spec, ok := specV.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		target := existing[name]
		if target == nil {
			single := jsonx.NewOrderedMap()
			single.Set(name, spec)
			newInline = append(newInline, synthesizeConfigLoopholes(single)...)
			continue
		}
		if v, set := ConfigEnabledOverride(loopholesConfig, name); set {
			target.Enabled = v
		}
		if envV, ok := spec.Get("env"); ok && loopholedecl.Truthy(envV) {
			if envMap, isMap := envV.(*jsonx.OrderedMap); isMap && target.HostDaemon != nil {
				override := NewEnvMap()
				for _, k := range envMap.Keys() {
					v, _ := envMap.Get(k)
					override.Set(k, loopholedecl.Str(v))
				}
				target.HostDaemon.Env = target.HostDaemon.Env.MergedWith(override)
			}
		}
		if jailEnvV, ok := spec.Get("jail_env"); ok && loopholedecl.Truthy(jailEnvV) {
			if jailEnvMap, isMap := jailEnvV.(*jsonx.OrderedMap); isMap {
				override := NewEnvMap()
				for _, k := range jailEnvMap.Keys() {
					v, _ := jailEnvMap.Get(k)
					override.Set(k, loopholedecl.Str(v))
				}
				target.JailEnv = target.JailEnv.MergedWith(override)
			}
		}
	}
	return newInline
}

// loadModuleDirs loads a caller-supplied list of loophole MODULE dirs (each one holding
// a manifest.jsonc), in the given order, under the given source label.
//
// WARN AND CONTINUE, never refuse: one bad manifest must not take the others down with
// it. A manifest the loader rejects makes its loophole VANISH — no host daemon, no
// endpoint, no injected env var, no entry in `yolo loopholes list` — and every consumer
// then fails with a symptom that names something else entirely, which is why the warning
// is not optional (TestInvalidManifestDoesNotBreakOthers). Note what that means for a
// pack-contributed dir — the PACK layer refuses a `from` naming a directory the pack does
// not contain (a pack.json error, decidable by `yolo pack lint`), while THIS layer only
// warns about a manifest it cannot parse. That split is deliberate
// (docs/design/loophole-packaging.md §3.1): the pack layer refuses, the discovery layer
// warns.
//
// IT IS ALSO THE ONLY LOADER LEFT. There used to be a second, loadFromDir, which walked a
// DIRECTORY OF loopholes for the bundled channel; that channel was retired on 2026-08-19
// when its last inhabitant became a contribution of `packs/claude`
// (docs/design/broker-as-a-pack.md OQ-BP4). Every manifest yolo reads now arrives as a
// named module dir — a pack's contribution — or as a `loopholes:` entry in a config.
//
// THE SOURCE LABEL SELECTS THE LOADER, which is how §3.1's pack-shipped subset reaches the
// launch path at all. LoadPackLoophole applies the subset and had ZERO non-test callers: every
// discovery read went through the plain loadManifest, so `jail_env`, an absolute or `$VAR`
// bind host, a writable bind and a self-publishing daemon were all refused in a package
// nothing on this path called. Measured: a manifest with all four violations was discovered,
// Active, and produced `-v /:/ctx/hostroot` (readonly:false honored) plus
// `-e LD_PRELOAD=/ctx/evil.so`.
//
// Pack-shippedness is the CALLER's fact (load.go says why it cannot be a manifest field), and
// this function is the caller that knows it — its `source` parameter IS that fact. It takes
// SourcePack from every production call site today; the parameter stays because the mapping
// source → loader is the security property, and a loader hardcoded here would put it back
// where it was before loaderFor existed (a subset nothing on the launch path applied).
func loadModuleDirs(dirs []string, source string) (map[string]*Loophole, []string) {
	out := map[string]*Loophole{}
	var order []string
	load := loaderFor(source)
	for _, dir := range dirs {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			warnf("loophole module dir %s is not a directory, so that loophole is NOT active", dir)
			continue
		}
		loophole, err := load(dir)
		if err != nil {
			warnf("loophole manifest %s failed to load, so that loophole is NOT active: %v", dir, err)
			continue
		}
		// A pack's loophole carries SKEW NOTES for the same reason the other sources do, and
		// with more force: a pack crosses the version boundary by construction, so a key only
		// a newer yolo knows is the expected case rather than an anomaly. Warned, never
		// refused — a degraded loophole whose symptom names something else is the failure mode
		// this package keeps paying for (the `tier` incident).
		for _, note := range loophole.SkewNotes {
			warnf("loophole %s: %s", loophole.Name, note)
		}
		loophole.Source = source
		if _, seen := out[loophole.Name]; !seen {
			order = append(order, loophole.Name)
		}
		out[loophole.Name] = loophole
	}
	return out, order
}

// loaderFor picks the manifest loader a SOURCE gets: the pack-shipped subset for a pack, the
// full vocabulary for everything else.
//
// A function of the source rather than an `if` at each read, because the mapping is the whole
// security property and it should be stated once. Both loaders are TOLERANT — the subset is
// orthogonal to version skew, and conflating them is how a pack's loophole would vanish over a
// key only a newer build knows (see LoadPackLoophole's own doc).
func loaderFor(source string) func(string) (*Loophole, error) {
	if source == SourcePack {
		return LoadPackLoophole
	}
	return loadManifest
}

// THE RESERVED LOOPHOLE NAMESPACE IS GONE, and its emptiness is the end of a sprint
// rather than a lapse. `ReservedLoopholeNames()` composed the names a pack could not
// ship — the ones yolo answered to itself — and every one of them has become a pack's
// own name, in this order:
//
//   - `journal` and `cgroup-delegate` (2026-08-18) were `paths.BuiltinLoopholeNames`,
//     the builtin services. Their reservations were CONSTANTS, so each had to be
//     deleted by hand in the commit that shipped its pack.
//   - `host-processes` and `audio` (2026-08-18) were reserved only as bundled DIRECTORY
//     names, read off the embed the loader materialized, so `git mv` retired them free.
//   - `claude-oauth-broker` (2026-08-19) was reserved BOTH ways — from
//     `broker.BrokerLoopholeName` unconditionally AND as the last bundled directory —
//     which is why moving it into `packs/claude` had to delete this function in the same
//     commit (docs/design/broker-as-a-pack.md §6.1's warning). A reservation left
//     standing over a pack-shipped name is not a warning: the pre-flight is FATAL, so
//     every launch selecting that pack is refused.
//
// What still protects the names yolo keys on by hand — `claude-oauth-broker` above all,
// which `yolo broker status`, `yolo check` and brokerEnsure each reach by literal — is
// two things, and neither is a list here. First, run.PackLoopholeNameConflicts' OTHER
// half: loophole names are exclusive ACROSS PACKS, fatally, so `packs/claude` occupying
// the name means any other pack claiming it refuses the launch by name. Second, the
// ORIGIN GATE: a record from a pack yolo has not approved to touch the host is Active
// but not Honored, and run.brokerLoopholeActive asks for Honored. That is the same bound
// `cgroup-delegate` took when it retired its own reservation, and the same one OQ-A3
// already admits for every other pack-declared capability.
//
// Restoring a reservation is a small change (a name, an origin string, and the refusal
// branch in run.PackLoopholeNameConflicts) and should be made deliberately rather than
// by reviving this comment: a set that reserves a name a pack now ships is the one
// mistake in this area that breaks every user of that pack at once.

// DiscoverOptions carries the discovery parameters. The zero value discovers nothing,
// which is the honest answer now that every source is something a caller supplies.
//
// TWO CHANNEL SWITCHES HAVE LEFT THIS STRUCT, and both left the same way — the channel
// they gated stopped existing. `Root` named the hand-placed user loopholes dir (OQ-LP10,
// retired.go). `IncludeBundled` named `bundled_loopholes/`, emptied and retired on
// 2026-08-19 (docs/design/broker-as-a-pack.md OQ-BP4). While each lived, every caller
// that did NOT want that channel had to remember to switch it off — and, worse for
// IncludeBundled, every caller that DID want it had to remember to switch it on, with
// the zero value silently answering "no bundled loopholes on this machine". That is the
// shape of an option nobody should have had to pass, and NewHostSet exists because of it.
type DiscoverOptions struct {
	IncludeDisabled bool
	LoopholesConfig *jsonx.OrderedMap
	// PackModules are loophole MODULE directories — in practice a pack's `loophole`
	// contributions, already resolved and origin-gated by the caller.
	//
	// PATHS AND A BOOL, deliberately, not packs. internal/loopholes never learns what a
	// pack is (and cannot: loopholes → config → packload, so importing packload here is
	// a cycle). Each entry is a directory holding a manifest.jsonc — the same on-disk
	// shape a bundled or user loophole has, so one loader reads all four sources. The
	// SELECTION stays the caller's business: a pack the user did not select contributes
	// nothing because its dir is never passed in.
	//
	// Each is loaded individually rather than by scanning a parent, because a pack's
	// contributions can point anywhere inside its staged tree.
	PackModules []PackModule
	// PackSupersessions are the selected packs' `supersedes` claims — a capability
	// name, the pack that claimed it, and the mandatory reason
	// (docs/design/pack-capabilities.md). STRINGS, not packs, for the same cycle
	// reason PackModules is paths-and-a-bool.
	//
	// It is a SEPARATE list from PackModules, not a field on it, because the two sets
	// are genuinely different: the motivating pack (Bedrock auth) supersedes the
	// bundled broker while shipping no loophole module at all, so keying one off the
	// other would make the case this exists for unrepresentable.
	//
	// EMPTY means nothing is superseded, which is the safe direction: a caller that
	// never resolved packs cannot silently turn a loophole off.
	PackSupersessions []PackSupersession
}

// PackModule is one externally-contributed loophole module dir plus the ORIGIN GATE
// decision the caller made about it.
//
// The bool is in the INPUT rather than derived here because only the caller can know it:
// deciding it needs the pack's origin and the approval lockfile, both of which live in
// packages this one cannot import. Making it a required part of the input is what lets
// RunDoctorChecks refuse to execute a module whose gate nobody evaluated — the
// "until the convergence exists" guard docs/design/loophole-packaging.md §5.1 asks for,
// expressed so the unsafe call is unrepresentable rather than merely avoided.
type PackModule struct {
	// Dir is the absolute path to the module directory (holding manifest.jsonc).
	Dir string
	// HostExecApproved says the caller EVALUATED this module's origin gate and the
	// origin may run host code — an embedded or local pack (whose origin carries the
	// user's own authority), or a fetched pack whose host-access claims the user
	// approved at `yolo pack install`. FALSE is the safe default: the loophole is still
	// discovered and LISTED (so "installed but not approved" is visible rather than
	// missing), but its doctor_cmd never runs.
	HostExecApproved bool
}

// packModules is the process-wide record of the loophole modules THIS HOST's selected
// packs contribute, with each one's origin gate already evaluated.
//
// A record rather than a parameter, and that is the shape the convergence needs
// (docs/design/loophole-packaging.md §5.1). Resolving a pack needs the `packs` config,
// the pack store and the approval lockfile — internal/packload — and this package cannot
// import it (loopholes → config → packload is a cycle, measured). So the resolution
// happens in whichever command resolved packs, and lands here ONCE; every consumer then
// reads the same value through NewHostSet instead of assembling its own DiscoverOptions.
// This is the same shape jailcontent.SetPackSkillDirs already has, set by the same function
// (run.stagePacks) for the same reason.
//
// EMPTY IS FAIL-SAFE, which is what makes any partial wiring honest: a command that never
// resolved packs sees no pack loopholes at all, so it cannot run an unapproved fetched
// pack's doctor_cmd. A pack loophole missing from `yolo loopholes list` is a visible
// omission; an unaudited daemon self-check executing under `yolo check` would not be.
//
// IN-JAIL IS OUT OF SCOPE, deliberately, and here is the whole of the reason (§5.1 asks the
// question: "sites 6 and 7 also run IN-JAIL, where the staged root is /ctx/packs, so their
// wiring is not the same as the run path's"). Measured at HEAD:
//
//   - Site 7, `yolo check`'s loophole sections, SHORT-CIRCUIT in-jail already —
//     "Inside jail — loophole checks skipped (managed by host)" — and so does
//     `yolo loopholes status` (site 5). Nothing to wire: the surfaces that execute host code
//     do not run there at all, which is the correct answer for a jail and not a gap.
//   - `yolo loopholes list` (site 5) and the config validator (site 6) DO run in-jail, and
//     both degrade to the empty-record branch: a pack loophole is absent from the list, and
//     an `enabled` entry for one takes the unknown-name fallback. That is a MISSING ENTRY,
//     never a wrong permission — and the in-jail config is the generated snapshot, where
//     loophole scope violations are already warnings rather than errors for the same
//     reason (a jail must not refuse a preflight over a file the in-jail user cannot fix).
//
// Wiring it would mean teaching this package to resolve /ctx/packs, which is
// internal/entrypoint's tree and a THIRD resolution path beside the staged record and the
// store fallback — for a surface whose only symptom is a list entry. Named rather than
// solved, on purpose.
var (
	packModules    []PackModule
	packModulesSet bool

	// packModuleResolver is the LAZY FALLBACK: a host-side function that resolves and
	// gates the configured packs' loophole modules on demand, for the surfaces that reach
	// discovery WITHOUT having staged anything.
	//
	// It exists because of an ordering fact that a record alone cannot cover: on the launch
	// path config validation runs BEFORE pack staging (internal/cli/run/run.go —
	// loadAndValidateConfig, then stageRunPacks), so at the moment
	// config.LoopholeResolver.Known() is consulted the authoritative record is still empty.
	// Without a fallback, `loopholes.<pack-loophole>.enabled` would take the unknown-name
	// path and warn "no loophole named 'x' is installed on this machine" at EVERY launch —
	// the same sentence a user gets when a pack genuinely failed to stage
	// (docs/design/loophole-packaging.md §5.2's prerequisite). The same fallback is what
	// gives `yolo loopholes list`/`status` and `yolo check` — which never stage — a
	// pack-aware, GATED view instead of the fork §5.1 refuses to leave open.
	//
	// It reads the pack STORE rather than a staged tree, which is the one way it differs
	// from the record: an `only`/`exclude` filter that removed a module dir is visible to
	// stagePacks and not here. That is why the record SUPERSEDES it rather than merging —
	// the launch's own view must be the staged one.
	packModuleResolver  func() []PackModule
	packModulesCached   []PackModule
	packModulesResolved bool
)

// SetPackModules records the pack-contributed loophole modules for this process, from the
// STAGED trees. Called by the host-side command that resolved and gated the pack set.
//
// It SUPERSEDES the lazy resolver for the rest of the process: staging is the authoritative
// view (it is what the jail will actually mount), and a launch must not end up validating
// against one set and mounting another.
func SetPackModules(mods []PackModule) {
	packModules = append([]PackModule(nil), mods...)
	packModulesSet = true
}

// SetPackModuleResolver installs the lazy fallback. Registered once per process by the
// host-side package that can resolve packs; called at most once, on the first PackModules()
// that finds no staged record.
func SetPackModuleResolver(fn func() []PackModule) {
	packModuleResolver = fn
	packModulesCached, packModulesResolved = nil, false
}

// PackModules returns the pack-contributed loophole modules: the staged record when one has
// been set, else the lazy resolver's answer, else nothing.
//
// EMPTY IS FAIL-SAFE at every branch. A process with neither a record nor a resolver sees no
// pack loopholes at all, so it cannot run an unapproved fetched pack's doctor_cmd. A pack
// loophole missing from `yolo loopholes list` is a visible omission; an unaudited daemon
// self-check executing under a read-only preflight would not be.
func PackModules() []PackModule {
	if packModulesSet {
		return append([]PackModule(nil), packModules...)
	}
	if packModuleResolver != nil && !packModulesResolved {
		// Memoized: resolution reads the pack store and every pack's manifest, and the
		// surfaces that need it ask more than once per process.
		packModulesCached, packModulesResolved = packModuleResolver(), true
	}
	return append([]PackModule(nil), packModulesCached...)
}

// ResetPackModules clears BOTH the staged record and the lazy resolver's cache. For tests,
// which must not leak a recording into the next one — the record is deliberately
// process-wide (it IS the convergence point), which makes isolation mandatory.
func ResetPackModules() {
	packModules, packModulesSet = nil, false
	packModulesCached, packModulesResolved = nil, false
}

// Set is THE loophole set for one host-side operation: the bundled, pack-contributed,
// user-installed and config-declared records, resolved once.
//
// It exists because there were SEVEN independent discovery surfaces
// (docs/design/loophole-packaging.md §5.1) — the briefing, the broker-active predicate,
// the container argv, the host daemon spawn, `yolo loopholes list`/`status`,
// config.LoopholeResolver, and `yolo check`'s own walker — each assembling its own
// DiscoverOptions. Seven assemblies is seven chances to disagree about what this machine
// has, and two of them EXECUTE host code (RunDoctorChecks), so a disagreement there is
// not a cosmetic drift: it is a command users treat as read-only preflight running a
// daemon's self-check that another surface never admitted existed.
//
// It holds the INCLUDE-DISABLED superset and offers the narrower views, so one
// construction serves a consumer that wants everything (list, the config resolver) and
// one that wants only what is live (the argv, the spawn) without a second walk of the
// filesystem — and so the two can never be built from different inputs.
type Set struct {
	all []*Loophole
	// supersessions are the claims this Set was built from, kept so
	// SupersessionProblems can answer "which claim matched nothing" without a second
	// walk of the filesystem. The EFFECT of the claims is already stamped on the
	// records (Loophole.SupersededBy); this is only the reporting half.
	supersessions []PackSupersession
	// gate maps a pack-contributed module dir to whether its origin gate PASSED.
	// Absent means the record did not come from a pack module (bundled, user dir,
	// config) and needs no origin decision — those three carry the user's own
	// authority by construction. Keyed by module dir, which is Loophole.Path.
	gate map[string]bool
}

// gateOf builds the module-dir → approved map from the input modules.
func gateOf(mods []PackModule) map[string]bool {
	if len(mods) == 0 {
		return nil
	}
	out := make(map[string]bool, len(mods))
	for _, m := range mods {
		out[m.Dir] = m.HostExecApproved
	}
	return out
}

// MayRunHostCode reports whether this record's doctor_cmd (or any other host execution
// derived from its manifest) may run.
//
// TRUE for everything that did not come from a pack module: bundled loopholes ship with
// yolo, and a user directory or a user-config entry carries the user's own authority (the
// same reason a file:// pack does). For a PACK module it is the gate decision the caller
// recorded — and false for a pack module this Set does not know about, which is the
// fail-safe branch that makes a Set assembled without gate information unable to execute
// anything a pack shipped.
func (s Set) MayRunHostCode(lp *Loophole) bool {
	if lp == nil {
		return false
	}
	if lp.Source != SourcePack {
		return true
	}
	return s.gate[lp.Path]
}

// NewSet constructs the set. IncludeDisabled is forced on: the views below do the
// filtering, and a Set built without the disabled records could not answer
// `loopholes list`.
func NewSet(opts DiscoverOptions) Set {
	opts.IncludeDisabled = true
	return Set{
		all:           Discover(opts),
		supersessions: append([]PackSupersession(nil), opts.PackSupersessions...),
		gate:          gateOf(opts.PackModules),
	}
}

// SupersessionProblems reports every `supersedes` claim in this Set that matched no
// served capability — the typo case (docs/design/pack-capabilities.md §5).
//
// PURE: it recomputes from the records and the claims rather than caching what
// Discover warned about, so a caller may ask more than once without a duplicate
// line. Discover itself warns each problem to stderr as it applies the claims; this
// is the value-shaped seam for a surface that wants to render them (`yolo check`'s
// loophole section is the obvious next reader).
func (s Set) SupersessionProblems() []string {
	return unmatchedSupersessions(s.all, s.supersessions)
}

// NewHostSet is THE constructor every host-side consumer uses: the packs this process
// recorded (SetPackModules, or the lazy resolver) plus the given config block.
//
// It is the convergence point. A consumer that calls this cannot assemble a DIFFERENT
// view of what this machine has, cannot forget a source (it used to be able to forget
// IncludeBundled, whose zero value is false, so every call site had to remember to set
// it), and cannot bypass the origin gate — so the seven surfaces agree by construction
// rather than by six call sites happening to pass the same struct literal.
func NewHostSet(loopholesConfig *jsonx.OrderedMap) Set {
	return NewSet(DiscoverOptions{
		LoopholesConfig:   loopholesConfig,
		PackModules:       PackModules(),
		PackSupersessions: PackSupersessions(),
	})
}

// SetOf wraps an already-discovered slice. For tests and for a caller holding a slice
// from somewhere else; prefer NewSet.
//
// It carries NO gate information, so MayRunHostCode is false for every SourcePack record
// in it. That is the fail-safe direction: a Set assembled by hand cannot execute a pack
// loophole's doctor_cmd, and a caller who genuinely evaluated the gate says so by going
// through NewSet.
func SetOf(all []*Loophole) Set { return Set{all: all} }

// withGate copies src's origin gate AND its supersession claims onto s, for narrowing a
// Set to a subset of its own records without dropping either. Unexported: it is only ever
// safe between a Set and a view OF that same Set, which is a property the caller can see
// and a parameter cannot express.
//
// The claims ride along for the same reason the gate does — a narrowed view that answered
// SupersessionProblems from an empty claim list would report "no problems" for a set whose
// claims simply were not copied, which is a silent false negative in exactly the direction
// this report exists to avoid.
func (s Set) withGate(src Set) Set {
	s.gate = src.gate
	s.supersessions = src.supersessions
	return s
}

// All returns every record, disabled ones included, in discovery order.
func (s Set) All() []*Loophole { return s.all }

// Enabled returns the records whose `enabled` is true — exactly what
// Discover(IncludeDisabled:false) returned before this type existed, which is what makes
// the argv/spawn/briefing call sites byte-identical after the convergence.
func (s Set) Enabled() []*Loophole {
	out := []*Loophole{}
	for _, lp := range s.all {
		if lp.Enabled {
			out = append(out, lp)
		}
	}
	return out
}

// Active returns the records that are enabled AND whose requirements are met.
//
// The distinction from Enabled is not pedantic: a briefing built from Enabled advertises
// an inactive loophole to the agent as a live capability (§5.1's shipped bug), and a
// broker predicate built from Enabled would wire a terminator with nothing behind it.
func (s Set) Active() []*Loophole {
	out := []*Loophole{}
	for _, lp := range s.all {
		if lp.Active() {
			out = append(out, lp)
		}
	}
	return out
}

// Honored returns the records that are Active AND whose ORIGIN GATE admits them: what this
// jail actually gets.
//
// The distinction from Active() is the one §4.3 G3 draws, and it is a third distinction
// beside Enabled/Active rather than a synonym for either. `Enabled` is the user's switch,
// `Active` adds "the machine can run it" (platform, `requires`), and this adds "the pack it
// came from is approved to touch the host". A record can be perfectly Active and still cross
// nothing, because the pack shipping it was never approved.
//
// It exists for the surfaces that DESCRIBE what crossed rather than perform it — the
// briefing, above all, which is instructions the agent ACTS ON. Advertising an unapproved
// pack's loophole there sends the agent to debug host wiring that was deliberately withheld,
// which is the same failure mode Active() was introduced to fix one axis over (§5.1's shipped
// bug: an enabled-but-inactive loophole advertised as live).
//
// The surfaces that PERFORM a crossing do not use this — RuntimeArgsFor and
// ManifestHostDaemonSpecs enforce the gate inside themselves, because a slice carries no gate
// and a filter the caller must remember to apply is a filter the next caller omits.
func (s Set) Honored() []*Loophole {
	out := []*Loophole{}
	for _, lp := range s.all {
		if lp.Active() && s.MayRunHostCode(lp) {
			out = append(out, lp)
		}
	}
	return out
}

// Lookup returns the record with this name, disabled ones included.
func (s Set) Lookup(name string) (*Loophole, bool) {
	for _, lp := range s.all {
		if lp.Name == name {
			return lp, true
		}
	}
	return nil, false
}

// Discover resolves the loophole records this machine has: the caller's pack modules,
// then the config block's overrides and inline entries. It never errors — a per-manifest
// failure is warned and skipped (loadModuleDirs), which seven call sites rely on.
func Discover(opts DiscoverOptions) []*Loophole {
	// The retired hand-placed directory is not a source; it is only ever a thing to
	// tell its owner about (retired.go). Warned HERE, at the one function every
	// discovery surface funnels through, so the notice reaches a plain `yolo run` and
	// not only the commands somebody remembered to wire it into.
	warnRetiredUserLoopholes()

	byName := map[string]*Loophole{}
	var order []string
	appendOrdered := func(m map[string]*Loophole, keys []string) {
		for _, k := range keys {
			if _, seen := byName[k]; !seen {
				order = append(order, k)
			}
			byName[k] = m[k]
		}
	}
	// Pack-contributed module dirs come first and the config block overrides them. A
	// pack-vs-pack name collision never reaches here at all — the launch pre-flight
	// refused it (docs/design/loophole-packaging.md §5.1).
	if len(opts.PackModules) > 0 {
		dirs := make([]string, 0, len(opts.PackModules))
		for _, m := range opts.PackModules {
			dirs = append(dirs, m.Dir)
		}
		pm, pk := loadModuleDirs(dirs, SourcePack)
		appendOrdered(pm, pk)
	}

	inline := applyWorkspaceOverrides(byName, opts.LoopholesConfig)

	// Supersession is applied over the WHOLE resolved set, before the enabled filter,
	// because the claims are matched against `serves` — a declaration a disabled
	// loophole still carries, and one a caller asking for the include-disabled view
	// (`yolo loopholes list`) has to be able to see the consequence of.
	//
	// An unmatched claim is WARNED rather than refused; unmatchedSupersessions says at
	// length why "refused at load" cannot hold for the match half. Warning here rather
	// than at each consumer follows loadModuleDirs's precedent: discovery is the one place
	// that knows a declaration did nothing.
	all := make([]*Loophole, 0, len(order)+len(inline))
	for _, name := range order {
		all = append(all, byName[name])
	}
	all = append(all, inline...)
	applySupersessions(all, opts.PackSupersessions)
	for _, problem := range unmatchedSupersessions(all, opts.PackSupersessions) {
		warnf("%s", problem)
	}

	out := []*Loophole{}
	for _, m := range all {
		if !opts.IncludeDisabled && !m.Enabled {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ValidateEntry is one result of ValidateLoopholes: the module dir, the loaded
// loophole (nil on error), and the error string ("" when OK).
type ValidateEntry struct {
	Path     string
	Loophole *Loophole
	Err      string
}

// ValidateLoopholes walks every loophole MODULE this machine has, reporting parse errors
// instead of skipping them.
//
// It is `yolo check`'s independent walker — census site 7
// (docs/design/loophole-packaging.md §5.1) — and the reason it is a walker at all rather
// than a Discover call is the ERROR CHANNEL: Discover swallows a per-manifest failure by
// contract (resolver.go's invariant), and a preflight whose whole job is reporting bad
// manifests cannot use a loader that hides them.
//
// IT TOOK AN `includeBundled bool` UNTIL 2026-08-19, and both call sites passed true. The
// bundled channel is retired (docs/design/broker-as-a-pack.md OQ-BP4), so the parameter
// named a source that no longer exists — and a walker whose only remaining source is the
// recorded pack modules cannot be asked to exclude them and still be census site 7.
//
// It reads the SAME recorded pack modules Discover does (SetPackModules), so the two do
// not disagree about which sources exist on this machine. It is the one census site whose
// gate cannot be carried in the return value — a ValidateEntry is a manifest, not a set —
// so callers that go on to EXECUTE a doctor_cmd must route through
// SetOf(...).DoctorCandidates or ValidateSet below.
func ValidateLoopholes() []ValidateEntry {
	// Same reason Discover warns: this walker backs `yolo check`, which is the command
	// a user runs when a loophole stopped working — precisely the symptom the retired
	// directory now produces. `yolo check` also renders the notice through its own
	// reporter; this covers every other caller of the walker.
	warnRetiredUserLoopholes()

	out := []ValidateEntry{}
	// The pack-contributed modules, individually — a pack's `loophole` contribution
	// points at a directory anywhere inside its staged tree, so there is no parent to
	// scan. A module dir that is not a directory at all is REPORTED here rather than
	// skipped: this function's contract is that a broken source is visible, and the pack
	// layer's own refusal (a `from` naming a directory the pack does not contain) does
	// not cover a tree that vanished after staging.
	//
	// Through THE SAME LOADER discovery uses for a pack module (loaderFor(SourcePack), i.e.
	// the pack-shipped subset), because this walker's answer must not be kinder than the
	// loader's: `yolo check` reporting a manifest as fine while every launch refuses it is
	// the report/gate disagreement the whole subset was factored to avoid. A subset
	// violation therefore lands in Err, where this function already puts a broken source —
	// which is also the only surface that names it before the user launches.
	for _, mod := range PackModules() {
		if fi, err := os.Stat(mod.Dir); err != nil || !fi.IsDir() {
			out = append(out, ValidateEntry{Path: mod.Dir, Loophole: nil,
				Err: "pack-contributed loophole module dir is missing or not a directory"})
			continue
		}
		loophole, err := loaderFor(SourcePack)(mod.Dir)
		if err != nil {
			out = append(out, ValidateEntry{Path: mod.Dir, Loophole: nil, Err: err.Error()})
			continue
		}
		loophole.Source = SourcePack
		out = append(out, ValidateEntry{Path: mod.Dir, Loophole: loophole, Err: ""})
	}
	return out
}

// ValidateSet is ValidateLoopholes' entries plus the gate, so a caller that both reports
// bad manifests AND runs doctor_cmds gets both from one walk.
//
// It exists because those two needs pull opposite ways: the report needs the error
// channel Discover throws away, and the execution needs the gate a ValidateEntry cannot
// carry. Returning the pair from one function is what stops `yolo check` from having to
// re-derive either.
func ValidateSet() ([]ValidateEntry, Set) {
	entries := ValidateLoopholes()
	var loaded []*Loophole
	for _, e := range entries {
		if e.Loophole != nil && e.Err == "" {
			loaded = append(loaded, e.Loophole)
		}
	}
	// ValidateLoopholes walks the directories itself rather than going through Discover
	// (it needs the error channel Discover throws away), so the supersession pass has to
	// be applied HERE too or `yolo check` would be the one census site that reports a
	// superseded loophole as live. Same claims, same function — the convergence Set
	// exists for is only real if every construction path runs it.
	claims := PackSupersessions()
	applySupersessions(loaded, claims)
	return entries, Set{all: loaded, supersessions: claims, gate: gateOf(PackModules())}
}

package config

// inherit.go answers "what does an inner `yolo` running INSIDE a jail get to read as its
// user scope?" — OQ-LP9's three-part split (docs/reference/loophole-system.md#oq-lp9).
//
// THE PROBLEM THIS REPLACES. Until now the answer was a raw single-file `:ro` bind of the
// human's real ~/.config/yolo-jail/config.jsonc (userConfigMountArgs, internal/cli/run).
// That is the wrong method for three measured reasons:
//
//   - It carries keys whose MEANING DOES NOT SURVIVE the boundary. A `cache_relocations`
//     target on a big host disk, a `gpu` block naming host drivers, a `devices` entry
//     naming /dev/bus/usb — in-jail `yolo check` evaluates each against a world where the
//     referent is simply absent, and reports problems the user does not have. Measured
//     2026-08-14: a user config carrying `gpu.enabled` makes an in-jail `yolo check` emit
//     FOUR fails (nvidia-smi/nvidia-ctk/runc/CDI "not found") about a host GPU the human
//     has correctly configured; `mounts` warns "host path does not exist" for every host
//     path. Only `cache_relocations` and `kvm` had ever been patched, one `inJail()` guard
//     at a time, which is the pattern this file replaces with a rule.
//   - It carries GRANTS whose referent silently changes. `host_files` means "your real
//     home" read on the host; read inside jail A, "the host home" is jail A's disposable
//     home. Same words, different object.
//   - And what crossed was NEITHER the effective config NOR a designed subset: only
//     config.jsonc (and, until the transform was removed, config.lua) was mounted, so
//     `include_if_found` files stayed host-side. The raw bind was already filtering —
//     by accident.
//
// THE MODEL. "User level" is not a fixed path; it is the scope that owns the machine a
// daemon runs on (docs/reference/gate-placement-principle.md). On the human's laptop that is
// their config. Inside jail A, the machine is jail A, so jail A's own config is the user
// level and jail A's agent legitimately owns it — the blast radius is a container you throw
// away. So the inner scope is GENERATED, per consumer, from the effective config.
//
// TWO FILES, SPLIT BY CONSUMER — not by the abstract question "does this key's meaning
// survive the boundary", which is one judgement call per key with no checkable answer:
//
//	PREFLIGHT  what the in-jail READERS evaluate (`yolo check`, `yolo loopholes`,
//	           `yolo pack`, `yolo config`). Keys they can meaningfully judge in here.
//	NESTED     what an inner LAUNCHER composes a jail FROM (`packages`, `packs`,
//	           `mise_tools`, ...). Written only where nesting is possible.
//
// Each file is the effective config through a per-consumer FILTER, so neither can drift
// from the source. Every top-level key is classified exactly once below, and
// TestInheritCensusIsTotal fails the build when a new key is added to
// knownTopLevelConfigKeys without a classification — the same forcing function
// render.JailFields()/HostFields() gives contribution kinds.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// InheritScope names a generated inner-scope file. The two are separate CONSUMERS, not two
// halves of one snapshot: a key can be in both (`loopholes` is read by `yolo loopholes` AND
// composed into a nested launch), in one, or in neither.
type InheritScope int

const (
	// InheritPreflight is the file the in-jail READ-ONLY commands consult.
	InheritPreflight InheritScope = iota
	// InheritNested is the file an in-jail LAUNCHER composes a jail from.
	InheritNested
)

// String names the scope for messages and file headers.
func (s InheritScope) String() string {
	switch s {
	case InheritPreflight:
		return "preflight"
	case InheritNested:
		return "nested"
	}
	return "unknown"
}

// keyDisposition records where one top-level config key goes, and WHY. The reason is not
// decoration: it is what a future reader needs to re-decide the key when its consumers
// change, and it is what the census test asserts is non-empty. A key in NO scope is still
// listed — with the reason it is excluded — because "assigned to neither" has to be a
// decision on the record rather than an omission.
type keyDisposition struct {
	// preflight / nested report membership in each generated file.
	preflight bool
	nested    bool
	// reason explains the classification in one sentence, in terms of the CONSUMER.
	reason string
}

// inheritCensus classifies every top-level config key. THIS IS THE CENSUS. It is
// exhaustive over knownTopLevelConfigKeys by test, so a key added to the schema fails the
// build until it is classified here.
//
// The classification question is never "is this key host-shaped?" but "which in-jail
// consumer reads it, and can that consumer judge it from in here?" — which is why
// `runtime` is preflight-excluded (an in-jail check resolves its own runtime, and a host
// value like "macos-user" would be judged against the wrong machine) while `packages` is
// nested-only (nothing in-jail VALIDATES it; an inner launcher BAKES it).
var inheritCensus = map[string]keyDisposition{
	// ---- Both files -------------------------------------------------------------
	// `loopholes` is the key OQ-LP9 exists for. `yolo loopholes list/status` reads it
	// in-jail (census site 5), and an inner launcher spawns the daemons from it — so it is
	// the one key both consumers genuinely need. Its host-path-shaped INNARDS
	// (`command`, `doctor_cmd`) are not a reason to drop it: those are evaluated
	// host-side only (`sections_loopholes.go` skips exec checks in-jail, measured), and
	// dropping the key would make `yolo loopholes list` blind to the human's own
	// installs — a visible omission, which §5.1 rules worse than a stale path.
	//
	// What crosses is the user and workspace config's block, NEVER the per-workspace file's
	// switches (workspacefile.go): the launch renders these files from
	// LoadConfigWithoutWorkspaceFile, for `brokered`'s reason below — that file is keyed by a host
	// workspace path, and inherited as user scope its switch would apply to every workspace a
	// launch inside the jail opens.
	"loopholes": {preflight: true, nested: true, reason: "read by `yolo loopholes list/status` and spawned by an inner launcher"},
	// `packs` is user-scope-only BY CONSTRUCTION (config/packs.go reads the user file
	// directly), so it can only ever arrive through this file. Both consumers need it:
	// `yolo pack ls/status` reports it, and an inner launcher stages from it.
	"packs": {preflight: true, nested: true, reason: "reported by `yolo pack ls/status` and staged by an inner launcher"},
	// The declaration is user-scope-only and must cross as user scope in either consumer;
	// FilterInherit replaces any merged-map copy with the strict direct user read.
	"host_import_exclusions": {preflight: true, nested: true, reason: "filters user host config imports for this jail and a nested launcher"},
	// `agent_updates` is user-scope-only for `packs`' reason and one step sharper (a
	// workspace value would let the agent freeze its own updates), so this file is its only
	// route in. Both consumers have a real referent here: the in-jail `yolo check` dry-run
	// GENERATES the launchers and bakes the policy into each one, and an inner launcher
	// passes it to the jail it spawns. Its keys are pack names, which mean the same thing
	// on both sides of the boundary.
	"agent_updates": {preflight: true, nested: true, reason: "the check dry-run bakes it into the launchers it generates; an inner launcher passes it on"},
	// The conventional local pack lives beside this file, so `include_if_found` had to be
	// resolved BEFORE the render — see FilterInherit, which consumes it exactly as
	// LoadJSONCWithIncludes does. Listed for the census, never emitted.
	"include_if_found": {reason: "already resolved into the rendered config; emitting it would re-resolve host-relative paths against the jail"},
	// THE IN-JAIL-PROVISIONING KEYS, all four in BOTH — and the preflight half is MEASURED,
	// not judged. `yolo check`'s entrypoint dry-run (check/entrypoint.go) feeds exactly
	// these into a temp home as YOLO_BLOCK_CONFIG / YOLO_MISE_TOOLS / YOLO_LSP_SERVERS /
	// YOLO_MCP_SERVERS and runs the real generators over them, so an in-jail check
	// genuinely evaluates each; and the run pipeline feeds the same four to a
	// real container. One consumer VALIDATES, the other COMPOSES — which is why "in one
	// file" must not be allowed to mean "in both" by default: these earn both memberships.
	//
	// They are also the key class the raw bind got RIGHT, and the reason the filter is not
	// "drop anything host-shaped": every referent here is inside the jail.
	"security":    {preflight: true, nested: true, reason: "blocked_tools decides this jail's shims; the check dry-run generates them and a launcher passes them on"},
	"mise_tools":  {preflight: true, nested: true, reason: "the check dry-run runs ConfigureMisePrism over it; an inner launcher installs from it"},
	"mcp_servers": {preflight: true, nested: true, reason: "MCP processes run in the jail; the check dry-run renders their wrappers and a launcher passes them on"},
	"lsp_servers": {preflight: true, nested: true, reason: "LSP servers the jail's agents spawn; the check dry-run renders their config"},
	// `providers` is merged-scope EXCEPT for its credential-routing half: `endpoints` in any
	// form, `api_key_env_name`, and a null provider are user-scope only (OQ-LM3 for the
	// address, notch-convergence OQ-NC6 for the rest; validate.go's
	// validateProviderCredentialScope). The distinction survives the crossing for free rather
	// than by care — this file is a generated config an inner launcher reads as its own USER
	// scope, the same property `packs` five entries up depends on — so an inherited field
	// arrives at the scope it was written at, and the inner launch's own gate still refuses
	// one the inner WORKSPACE adds.
	"providers": {preflight: true, nested: true, reason: "cloud provider declarations for agent configuration and nested launches"},
	// `model_facts` is `providers`' companion: the facts a model's routes share, composed into
	// the same table (packload.WithModelFacts), so it crosses wherever that table is composed.
	"model_facts": {preflight: true, nested: true, reason: "facts a model's provider routes share, composed into the provider table here and in nested launches"},
	// `profiles` and `profile` are user-scope-only (OQ-CS5 ruled BOTH keys), exactly
	// like `packs` three entries up — so a workspace spelling can never reach the render
	// that this census classifies, because the launch refuses it first. The two keys
	// arrive here differently, which is the distinction worth recording: `profiles` is
	// user-scope-only BY CONSTRUCTION (config/profiles.go reads the user file directly),
	// while `profile` is read off the merged config and is user-scope-only BY
	// REFUSAL (config.validateProfiles errors on a workspace spelling). The host CLI
	// resolves both into the YOLO_PROFILES/YOLO_USE_PROFILES tables for THIS jail, and an
	// inner launcher composes the same tables for the jail it spawns. The key crosses
	// VERBATIM, "*" and all: an inner launcher folds it over its own pack set, as this one did.
	"profiles": {preflight: true, nested: true, reason: "user-declared profiles over provider-declared options; the launch resolves them into YOLO_PROFILES here and in nested launches"},
	"profile":  {preflight: true, nested: true, reason: "the profile each agent runs (a name for every agent, or CLI name → name with \"*\" for the rest), folded into YOLO_USE_PROFILES for this jail and nested launches"},
	// `adapters` is `profiles`' twin in scope and in crossing: user-scope-only BY
	// CONSTRUCTION (config/adapters.go reads the user file directly) AND by refusal, and
	// what it carries is an ADDRESS inside the jail — the port an adapted provider answers
	// at. A nested launcher composes its own providers table and needs the same override,
	// or the inner jail would silently use the shipped default while the outer one does
	// not; the preflight seat is the same read, one command earlier.
	"adapters": {preflight: true, nested: true, reason: "the address an adapted provider is reached at, for this jail's provider composition and a nested launcher's"},
	// `required_capabilities` earns its NESTED seat on a check an inner launch performs,
	// and its preflight seat on the in-jail readers — the distinction matters to whoever
	// re-decides it, and it stopped being "shape alone on both sides" on 2026-09-17.
	// OQ-CAP2's fatal refusal (docs/reference/mcp-configuration.md#the-capability-gate, which config_ref.txt
	// cites for exactly this key) is BUILT: a LAUNCH now refuses when nothing the config or
	// its selected packs declare satisfies a required name, from inside the config gate
	// (internal/cli/run/preflight.go refuseUnmetCapabilities), which is why the key has
	// to reach an inner launcher — it composes a jail the same gate judges. The
	// PREFLIGHT seat is not that gate: the in-jail read-only commands validate the list's
	// type, and `yolo check` PREDICTS the gate through the launch's own census
	// (config.UnmetCapabilities), refusing nothing. It travelled to the jail as
	// YOLO_REQUIRED_CAPABILITIES until the gate landed; that export is gone, because the
	// requirement is judged before a container starts and no in-jail consumer of the
	// variable ever existed.
	"required_capabilities": {preflight: true, nested: true, reason: "shape-validated as a string list by the in-jail readers, its gate predicted by the in-jail yolo check, and passed to nested launches, whose own launch gate refuses a capability nothing declares"},

	// ---- Preflight only ---------------------------------------------------------
	// `agents_md_extra` is briefing prose rendered into this jail's own AGENTS.md.
	"agents_md_extra": {preflight: true, reason: "prose rendered into this jail's briefing"},
	// `briefing_provenance` changes only the text of this jail's own composed briefing.
	"briefing_provenance": {preflight: true, reason: "shapes the text of this jail's own briefing"},
	// `writable_home_dirs` names paths under /home/agent — in-jail referents, and the
	// config-ref explicitly calls the key safe at any scope.
	"writable_home_dirs": {preflight: true, reason: "names /home/agent subpaths, which exist in here"},

	// ---- Nested only ------------------------------------------------------------
	// The image-composition keys. Nothing in-jail VALIDATES these against a referent (an
	// in-jail `yolo check --no-build` skips the image section entirely, measured); an
	// inner launcher BAKES them. So they are the nested file's core and are absent from
	// preflight, where they would only invite a judgement about a host image.
	"packages":  {nested: true, reason: "baked into the image an inner launcher builds"},
	"resources": {nested: true, reason: "memory/cpu limits and the disk I/O priority an inner launcher applies to its container"},
	// `perf_logging` is the persistent form of --timing (docs/reference/perf-logging.md).
	// NESTED, because an inner launcher IS a launcher: the preference is "time the
	// launches I make", and a nested launch is one of them — which is also how the
	// feature was verified in the first place. NOT preflight: no read-only command
	// times anything, so emitting it there would add a key `yolo check` can see and
	// cannot evaluate.
	//
	// This does NOT contradict the ruling that YOLO_TIMING is never forwarded into a
	// jail (§4, D5). That ruling is about the host↔jail ENV wire, whose two halves
	// redeploy on different cadences; this file is a generated config an inner
	// launcher reads as its own user scope, and it re-derives its own gate from it.
	"perf_logging": {nested: true, reason: "an inner launcher times the launches it makes"},
	// `macos_user_shared_tmp` is the same backend's shared-/tmp hatch (OQ-MM8), not inherited for
	// the same reason.
	"macos_user_shared_tmp": {reason: "opens the shared /tmp to a run-as-you macos-user launch on a macOS host; a nested jail is Linux and has no macos-user backend"},
	"network":               {nested: true, reason: "the network mode an inner launcher gives its container"},
	// `env_sources` is nested-only for a measured reason, not a judgement: its string
	// entries are HOST FILE PATHS, and resolving them in-jail emits "dotenv file not
	// found, skipping" on every in-jail check (measured 2026-08-14). An inner launcher
	// resolves them against ITS OWN home, which is the correct referent for a nested jail.
	"env_sources": {nested: true, reason: "dotenv paths resolve against the launching home; an in-jail resolve warns about absent host files"},
	// Scratch/venv/prune plumbing an inner launcher decides for its own container. None
	// is judgeable in-jail (checkDiskUsage skips in-jail; the venv shadows are the
	// launcher's mounts).
	"ephemeral_storage": {nested: true, reason: "the scratch backing an inner launcher gives its container"},
	"per_side_paths":    {nested: true, reason: "shadow mounts an inner launcher creates"},
	"prune":             {nested: true, reason: "a disk threshold `yolo prune` uses host-side; the in-jail check skips it"},

	// ---- Neither ----------------------------------------------------------------
	// EVERY EXCLUSION BELOW IS A FALSE-ERROR CLASS OR A MISREAD GRANT. This is the half
	// the design exists for, so each says which.
	//
	// `cache_relocations`: the named class. Targets are host paths absent from the
	// container, and it is a READ-WRITE host mount an inner launcher must not inherit —
	// a nested jail's cache is its own per-workspace dir anyway (LoadCacheRelocations
	// already returns nil in-jail, which is the guard this file makes unnecessary).
	"cache_relocations": {reason: "host paths absent in the container, and a rw host grant a nested jail must not inherit"},
	// `host_files`: the misread grant. "The host home" rebinds to the jail's own
	// disposable home, so the same words name a different object — the one exclusion here
	// that is about MEANING rather than a missing referent.
	"host_files": {reason: "\"the host home\" silently rebinds to the jail's own disposable home"},
	// `host_wrappers`: the same class as host_files, one step further out. It enables a
	// directory of launch wrappers on the HOST's PATH — a claim on a shell yolo does not
	// have in here, over programs the jail runs through its own launchers instead. Neither
	// half of the key has a referent in a container.
	"host_wrappers": {reason: "enables wrappers on the host's PATH, which a jail has neither of"},
	// `share_host_fonts`: a grant of the HOST user's font folder. Inherited, a nested launcher
	// would share the jail's own home's font folder instead, the same words naming a different
	// object (host_files' class).
	"share_host_fonts": {reason: "shares the host user's font folder, which a jail's own home is not"},
	// `local_clones`: HOST paths of the user's clones, which the host's pack check reads. In a container they
	// name nothing, and an entry that names nothing is a fatal config error, so inheriting them would refuse
	// every in-jail check (cache_relocations' false-error class).
	"local_clones": {reason: "host paths of the user's clones, absent in the container, where an entry naming nothing is an error"},
	// `host_floor`: which selected packs' programs yolo installs into the HOST's own prefix for
	// `yolo host` to run. A jail provisions its agents through its own launchers and has no
	// host prefix, so the key has no referent in a container.
	"host_floor": {reason: "chooses what yolo installs into the host's agent floor, which a jail has none of"},
	// `host_path`: folders added to the PATH `yolo host`'s checks read and its agent is handed.
	// A jail's PATH is composed by its own boot (entrypoint.BootPath), and a host folder named here
	// has no referent in a container, so the key means nothing inside one.
	"host_path": {reason: "adds folders to the PATH of a `yolo host` launch, which a jail does not have"},
	// `provisioners`: the user's order over the recipes packs ship, read at the HOST notch alone
	// (docs/design/provisioner-sets.md PS-D11). A jail's agents come from the pack's own launcher,
	// so an inherited host order would rank provisioners a container never runs.
	"provisioners": {reason: "ranks the provisioners that install a program on the host, which a jail's own launchers never consult"},
	// `brokered`: a workspace's own key, the repositories its broker may reach beyond its remotes,
	// approved at that workspace's gate (docs/design/workspace-widening.md). In an inherited user
	// scope it would apply to every workspace a launch inside the jail opens, and user scope now
	// refuses it; a nested workspace's own config carries its own.
	// `on_request` is a command the HOST's brokers run as the user; an inner launcher's
	// brokers would run it inside the jail, where the command it runs could call `yolo approve` on the inner jail's
	// requests with no human at the host seeing them.
	"on_request": {reason: "names a command the host's brokers run per filed request; inside a jail it would run with no human at the host to answer"},
	"brokered":   {reason: "one workspace's own key, approved at its gate; in an inherited user scope it would apply to every workspace an inner launch opens, and user scope refuses it"},
	// `update_check`: gates a check of the HOST's yolo install, which no in-jail yolo runs
	// (selfupdate.Enabled is false wherever YOLO_VERSION is set) and a nested launcher's
	// binary is the image's, updated by rebuilding it rather than by any channel.
	"update_check": {reason: "gates the host install's update check, which never runs in a jail"},
	// `host_apply_on_launch`: the same class again, and the sharpest of the three. It gates a
	// re-render of the invoking user's REAL home at `yolo host -- <bin>`, and in a jail
	// paths.Home() is /home/agent — so the key's referent rebinds to the container's own
	// disposable home exactly as host_files' does, while the mechanism it gates (a wrapped host
	// launch) does not exist in here at all. Inheriting it would hand a nested launcher a
	// standing licence to write a home the key was never about.
	"host_apply_on_launch": {reason: "gates a re-render of the HOST's real home at a wrapped " +
		"host launch — a jail has neither the home nor the launch"},
	// `host_management`: the same class as the three above, and it is the one whose words
	// survive the boundary most convincingly — which is exactly why it must not be inherited.
	// "Who owns the config files in the home yolo renders into" is a sentence a jail can read
	// without complaint, but paths.Home() in here is /home/agent, so the REFERENT rebinds to
	// the container's own disposable home — the misread grant host_files' entry names, one
	// declaration further out. An inner launcher inheriting `own` would hand a throwaway home
	// an ownership contract the human declared about their laptop.
	"host_management": {reason: "declares ownership of the HOST's real home; in a jail the " +
		"referent rebinds to the container's own disposable home"},
	// `promotion_target`: a host-only choice of which of the user's packs receives captured
	// settings. Promotion is refused in a jail, so an inherited value has no in-jail reader.
	"promotion_target": {reason: "selects a host-side promotion destination; promotion is refused in a jail"},
	// `shipped_skill_prefix`: read from the HOST's user scope when the host stages a jail's
	// skills and composes its briefing; nothing in a jail reads it.
	"shipped_skill_prefix": {reason: "names the skills the host stages for a jail; no in-jail reader"},
	// `programs`: NEITHER, and unusually it is neither a false-error class nor a misread
	// grant — it is a key with no in-jail READER at all. `programs.autoprune` is read on
	// the host out of the user file directly (ProgramsAutoprune; workspace scope is
	// inexpressible for a key that authorises deleting binaries) and delivered to the jail
	// as YOLO_PROGRAMS_AUTOPRUNE, so nothing in here would consult the key even if it were
	// inherited: `yolo programs` reads the variable and the disk. The NESTED half is a
	// decision rather than an omission — an inner launcher inheriting it would hand a
	// nested jail's own home a standing licence to delete, which the human who set the
	// option for their machine never granted for a container's throwaway one.
	// `programs.overrides` (BG-D15) takes the same line for a second reason: it points a
	// program at a host checkout, so inheriting it would hand an inner launch the outer
	// user's bytes. An inner launch reads its own user config, which decides its own.
	"programs": {reason: "host-read via ProgramsAutoprune and delivered as an env var; no in-jail reader, and a nested launch must not inherit a destructive option or an outer user's program overrides"},
	// `mounts`: host paths again. Measured to warn "host path does not exist and will be
	// skipped" for every host path in an in-jail check.
	"mounts": {reason: "host paths absent in the container; validation warns on every one"},
	// `gpu` and `devices`: the loudest measured class. A `gpu.enabled` host config makes
	// an in-jail check print four fails about drivers the container legitimately lacks.
	"gpu":     {reason: "host driver/CDI state absent in the container; in-jail check reports four fails about it"},
	"devices": {reason: "host /dev paths and lsusb absent in the container"},
	// `kvm`: same class, and the one that was already patched by hand — its check section
	// prints "Inside jail — kvm checks skipped". The census makes the patch redundant.
	"kvm": {reason: "a host /dev/kvm passthrough; the in-jail check already skips it"},
	// `workspace_readonly`: paths inside the WORKSPACE, which is the one scope that
	// crosses live through the /workspace bind. A user-scope entry naming another
	// project's paths is meaningless here, and the workspace's own config carries the
	// real ones.
	"workspace_readonly": {reason: "workspace-relative paths that ride the live /workspace bind from the workspace config"},
	// Retired keys. They stay in knownTopLevelConfigKeys so their targeted retirement
	// message fires instead of a generic unknown-key error; emitting either into a
	// generated file would resurrect a key yolo refuses.
	"agents":    {reason: "RETIRED — an agent arrives as a pack; emitting it would re-trigger the retirement error"},
	"repo_path": {reason: "RETIRED"},
	// `host_processes` and `journal` joined them on 2026-08-18, and their exclusion is the
	// one that MATTERS rather than merely tidying: each key is now a hard error on the
	// host, so emitting one into a generated inner scope would hand a nested launcher a
	// config that refuses itself — and the in-jail downgrade to a warning exists for
	// snapshots written by an OLDER launcher, not for ones this build writes.
	//
	// `journal` is the more interesting of the two to see here, because it was in BOTH
	// scopes right up until this commit — the one key the census classified as "a
	// reserved loophole `yolo loopholes` reports and an inner launcher starts". Both of
	// those consumers still exist and neither reads this key any more: the loophole is
	// discovered from the `journal` pack's manifest like every other, and `loopholes`
	// (already in both scopes, three entries up) carries the switch and the settings.
	"host_processes": {reason: "RETIRED — the keys moved to loopholes.host-processes.settings; emitting it would re-trigger the retirement error"},
	"journal":        {reason: "RETIRED — the bridge is the journal pack's loophole, its access the read and read_all grants; emitting it would re-trigger the retirement error"},
	// `macos_log` joined them on 2026-10-09, for the same reason: the unified log is the
	// `macos-log` loophole's now (packs/macos-log/README.md).
	"macos_log": {reason: "RETIRED — the log moved to the macos-log loophole; emitting it would re-trigger the retirement error"},
	// `mcp_presets` joined them on 2026-10-23 (docs/design/mcp-presets-removal.md §13 step 3):
	// the browser is the chrome-devtools pack, and sequential-thinking is deleted.
	"mcp_presets": {reason: "RETIRED — the browser is the chrome-devtools pack; emitting it would re-trigger the retirement error"},
	// The three keys named setups replace joined them (docs/design/named-setups.md §10): the
	// host refuses each, so none reaches a crossing it accepted.
	"runtime":         {reason: "RETIRED — where an agent runs is a setup now; emitting it would re-trigger the retirement error"},
	"confinement":     {reason: "RETIRED — where an agent runs is a setup now; emitting it would re-trigger the retirement error"},
	"macos_user_mode": {reason: "RETIRED — the account is a setup's run_as now; emitting it would re-trigger the retirement error"},
	// `agent_profiles` joined them on 2026-09-01, renamed to `pack_profiles` — the
	// keys were always CLI names and core knows packs, not agents — which was itself
	// renamed to `use_profiles` on 2026-09-02 (docs/reference/providers.md — Profiles and options:
	// `pack` named neither of the two things the key holds). `pack_profiles` is gone
	// with no census entry at all: it never shipped in a release, so the old spelling is
	// an ordinary unknown key (config.go's rule comment).
	"agent_profiles": {reason: "RETIRED — renamed to use_profiles (briefly pack_profiles, never in a release), and that to profile; emitting it would re-trigger the retirement error"},
	// `use_profiles` joined them on 2026-09-29, renamed to `profile` to mirror -p/--profile
	// (docs/design/providers-and-profiles-redesign.md PP-D10). It shipped in every release
	// from v0.9.0 through v0.11.0, so a snapshot an older launcher wrote carries it, and the
	// in-jail validator warns on it.
	"use_profiles": {reason: "RETIRED — renamed to profile, which mirrors -p/--profile; emitting it would re-trigger the retirement error"},

	// `setup` and `setups` choose and define where an agent runs on the host
	// (docs/design/named-setups.md); a nested jail is one setup's inside, and its own
	// launches take the inner OS's default.
	"setup":  {reason: "chooses where the agent runs on the host; a nested jail is one setup's inside"},
	"setups": {reason: "defines where an agent may run on the host; a nested jail is one setup's inside"},
}

// readableInheritDisposition is a readable-root census entry: the keyDisposition, plus
// whether the root is projected FIELD BY FIELD (filterReadableInheritRoot) rather than
// crossing whole.
type readableInheritDisposition struct {
	keyDisposition
	// projected roots carry their legacy keys' split one field at a time, so the flags above
	// say only which scopes receive SOME of the root; filterReadableInheritRoot is the rule.
	projected bool
}

// readableInheritCensus classifies the top-level roots that exist only in the readable
// (TOML) vocabulary, which inheritCensus cannot hold: its own drift tests pin it to
// knownTopLevelConfigKeys, the legacy key set. TestInheritCensusCoversReadableRoots walks
// ReadableSchemaKeys() and fails on a readable root classified in neither map.
//
// EACH ROOT IS CLASSIFIED AS ITS LEGACY EQUIVALENT IS, so a config.toml and the
// config.jsonc it was converted from give a jail the same user scope.
var readableInheritCensus = map[string]readableInheritDisposition{
	// install / machine / env gather legacy keys whose dispositions differ (packages is
	// nested-only, mise_tools both; gpu neither, resources nested), so they are split per
	// field to keep each field where its legacy key went.
	"install": {projected: true, keyDisposition: keyDisposition{preflight: true, nested: true,
		reason: "per field: mise as mise_tools (both files), nix as packages (nested only)"}},
	"machine": {projected: true, keyDisposition: keyDisposition{nested: true,
		reason: "per field: resources, ports and forward as resources/network (nested only); gpu as gpu (neither)"}},
	"env": {projected: true, keyDisposition: keyDisposition{nested: true,
		reason: "per field: files, set and unset go where env_sources goes (nested only)"}},
	// `active_provider` is `profile` renamed (readable-config.md RC-D1, OQ-RC14: the
	// agent-to-setup selection keeps its job under the new name). It names `providers`, which
	// cross both files, and is user scope only, which an inherited file is by construction.
	"active_provider": {keyDisposition: keyDisposition{preflight: true, nested: true,
		reason: "profile under its readable name: the provider each agent runs on, for this jail's readers and nested launches"}},
	// `pack_sources` is the source half of what legacy `packs` entries carried inline
	// (config-user.md §4): `packs = ["acme/tools"]` names a source declared here. `packs`
	// crosses both files, and a jail reading it without the sources it names could not
	// resolve the entry at all. Its local paths are host paths, exactly as a legacy path
	// entry in `packs` was; nothing in-jail stats them (packsrc.ParseSourceDefinitions).
	"pack_sources": {keyDisposition: keyDisposition{preflight: true, nested: true,
		reason: "the sources packs entries name; packs crosses both files and cannot be resolved without them"}},
	// `allow` is the user's ceiling over what a project may be granted — the readable form of
	// brokered's user-scope grants (config-user.md §3 rule 6; A.4: "`allow` is user scope
	// only"; A.2: the jail's copy omits the broker grants). brokered crosses into neither file,
	// and a ceiling inherited as a nested launch's user scope would let a jail's own projects
	// request host access with no human at the host approving it (readable-config-plan.md:
	// nested inheritance has "no service authority widening").
	"allow": {keyDisposition: keyDisposition{
		reason: "the user's ceiling on host access a project may request; like brokered's grants, never inherited by a jail"}},
	// `requests` is project scope only (config-user.md §3 rule 6: "`requests` in a project is
	// the request"), the readable form of brokered's per-workspace repos. It reaches this
	// filter only from the launching workspace's own file; inherited as user scope it would
	// apply to every workspace an inner launch opens (brokered's reason), and the user-scope
	// reader refuses it. A nested workspace's own config carries its own.
	"requests": {keyDisposition: keyDisposition{
		reason: "one project's request, approved at its own gate; as inherited user scope it would apply to every workspace an inner launch opens, and user scope refuses it"}},
}

// InheritDisposition returns the census entry for a key, and ok=false for a key the census
// does not know. Callers use it to explain a dropped key rather than dropping it silently.
func InheritDisposition(key string) (preflight, nested bool, reason string, ok bool) {
	d, found := inheritCensus[key]
	if !found {
		readable, isReadable := readableInheritCensus[key]
		if !isReadable {
			return false, false, "", false
		}
		d = readable.keyDisposition
	}
	return d.preflight, d.nested, d.reason, true
}

// InheritKeys returns the keys a scope emits, sorted. Sorted because it feeds a HEADER
// comment and a test, both of which want a stable order.
func InheritKeys(scope InheritScope) []string {
	var out []string
	add := func(k string, d keyDisposition) {
		if (scope == InheritPreflight && d.preflight) || (scope == InheritNested && d.nested) {
			out = append(out, k)
		}
	}
	for k, d := range inheritCensus {
		add(k, d)
	}
	for k, d := range readableInheritCensus {
		add(k, d.keyDisposition)
	}
	sort.Strings(out)
	return out
}

// FilterInherit projects an effective config down to one scope's keys. The one user-only
// exception, host_import_exclusions, is taken from a strict direct user-scope read rather
// than the merged input; FilterInheritErr returns any read or validation failure.
//
// It takes the ALREADY-COMPOSED effective config — the same value `yolo config dump`
// renders (config.LoadConfig → SnapshotJSON) — rather than composing its own, which is
// what keeps the two generated files renders of ONE computation. A key the census does not
// know is DROPPED and named in unknown, so a caller can report it; silently passing an
// unclassified key through would defeat the census, and silently dropping it would hide a
// schema addition.
//
// `include_if_found` is consumed here for the same reason LoadJSONCWithIncludes consumes
// it: its entries are paths relative to the file that named them, so a jail re-resolving
// them would look for host-relative siblings of a generated file. The composed config it
// receives already has the includes merged in.
func FilterInherit(effective *jsonx.OrderedMap, scope InheritScope) (out *jsonx.OrderedMap, unknown []string) {
	out, unknown, err := FilterInheritErr(effective, scope)
	if err != nil {
		return filterInheritMap(effective, scope, nil)
	}
	return out, unknown
}

// InheritHeader is the comment block that opens a generated file. It names the file's
// PURPOSE, its GENERATOR, and that it was generated at launch — the three things the
// maintainer's ruling requires, and between them the answer to "why is my key not in
// here?" for someone reading the file with no design doc at hand.
//
// launchedAt is a human-readable timestamp ("" omits the line: for a byte-stable golden, and for
// the macos-user home overlay, which an unchanged relaunch reuses only while its bytes are the same).
// The header is JSONC comments, so the file parses as the config it is.
//
// BACKEND-NEUTRAL WORDING, because the same file reaches a macos-user sandbox, as a copy the
// Seatbelt profile write-protects rather than a `:ro` bind (run.macosUserInheritedScope). Its
// FIRST LINE is also how a later launch on that backend recognises a copy it may remove, so it
// keeps its words.
func InheritHeader(scope InheritScope, launchedAt string) string {
	var b strings.Builder
	b.WriteString("// GENERATED by yolo at jail launch — do not edit; your edits are\n")
	b.WriteString("// overwritten on the next launch, and this file is read-only in the jail.\n")
	b.WriteString("//\n")
	switch scope {
	case InheritPreflight:
		b.WriteString("// PURPOSE: this is the USER-SCOPE config the read-only commands in this jail\n")
		b.WriteString("// consult — `yolo check`, `yolo loopholes list/status`, `yolo pack`,\n")
		b.WriteString("// `yolo config`. It carries ONLY the keys those readers can judge from\n")
		b.WriteString("// inside a jail.\n")
		b.WriteString("//\n")
		b.WriteString("// A key you set on the host and do not see here was filtered on purpose: its\n")
		b.WriteString("// referent does not exist in a jail (host GPU drivers, /dev paths, cache\n")
		b.WriteString("// relocation targets) or its meaning would change (host_files' \"host home\"\n")
		b.WriteString("// is this jail's disposable home in here). Evaluating those would report\n")
		b.WriteString("// problems you do not have. Your host config is unchanged.\n")
	case InheritNested:
		b.WriteString("// PURPOSE: this exists FOR JAIL-IN-JAIL. It carries the keys an inner `yolo`\n")
		b.WriteString("// composes a nested jail FROM — packages, packs, mise tools, resources — so a\n")
		b.WriteString("// jail launched from in here inherits what you configured.\n")
		b.WriteString("//\n")
		b.WriteString("// On a backend that cannot nest, this file is simply not written.\n")
	}
	b.WriteString("//\n")
	b.WriteString("// GENERATOR: internal/config/inherit.go (FilterInherit), from the effective\n")
	b.WriteString("// config of the jail that launched this one — the same computation\n")
	b.WriteString("// `yolo config dump` renders, less the launching workspace's per-workspace\n")
	b.WriteString("// file, whose switches are for that workspace alone. Recursion is by\n")
	b.WriteString("// COMPOSITION: a nested launch\n")
	b.WriteString("// filters ITS effective config again, so every level sees one inherited file\n")
	b.WriteString("// and one writable file of its own, at any depth.\n")
	b.WriteString("//\n")
	b.WriteString("// LAUNCH-FROZEN: a host-side config edit lands at the NEXT launch, not live\n")
	b.WriteString("// (`yolo config drift` reports whether it moved). That is the jail's normal\n")
	b.WriteString("// contract — env, image and relay wiring are all frozen at launch.\n")
	if launchedAt != "" {
		b.WriteString("//\n")
		b.WriteString("// GENERATED AT: " + launchedAt + "\n")
	}
	return b.String()
}

// FilterInheritErr projects an effective map while resolving host_import_exclusions from
// strict user scope. A malformed or unreadable user policy is returned as an error rather
// than copied from the merged map or silently allowed to cross the boundary.
func FilterInheritErr(effective *jsonx.OrderedMap, scope InheritScope) (*jsonx.OrderedMap, []string, error) {
	if effective == nil {
		return jsonx.NewOrderedMap(), nil, nil
	}
	policy, err := LoadHostImportExclusions()
	if err != nil {
		return nil, nil, err
	}
	out, unknown := filterInheritMap(effective, scope, policy)
	return out, unknown, nil
}

func filterInheritMap(effective *jsonx.OrderedMap, scope InheritScope, policy map[string][]string) (*jsonx.OrderedMap, []string) {
	out := jsonx.NewOrderedMap()
	if effective == nil {
		return out, nil
	}
	var unknown []string
	for _, k := range effective.Keys() {
		if k == hostImportExclusionsKey {
			continue
		}
		v, _ := effective.Get(k)
		if projected, recognized := filterReadableInheritRoot(k, v, scope); recognized {
			if projected != nil {
				out.Set(k, projected)
			}
			continue
		}
		d, ok := inheritCensus[k]
		if readable, isReadable := readableInheritCensus[k]; isReadable {
			// A projected root that filterReadableInheritRoot did not recognize has a shape
			// its per-field rule cannot split, so it is reported rather than passed whole.
			d, ok = readable.keyDisposition, !readable.projected
		}
		if !ok {
			unknown = append(unknown, k)
			continue
		}
		if (scope == InheritPreflight && !d.preflight) || (scope == InheritNested && !d.nested) {
			continue
		}
		out.Set(k, v)
	}
	if policy != nil {
		out.Set(hostImportExclusionsKey, hostImportExclusionsOrderedMap(policy))
	}
	return out, unknown
}

// filterReadableInheritRoot is the per-field projection for the readable roots
// readableInheritCensus marks projected. Every other readable root crosses whole, by its
// readableInheritCensus disposition.
func filterReadableInheritRoot(key string, value any, scope InheritScope) (any, bool) {
	m, ok := value.(*jsonx.OrderedMap)
	if !ok {
		return nil, false
	}
	out := jsonx.NewOrderedMap()
	copyKeys := func(source *jsonx.OrderedMap, target *jsonx.OrderedMap, keys ...string) {
		for _, key := range keys {
			if v, present := source.Get(key); present {
				target.Set(key, v)
			}
		}
	}
	switch key {
	case "install":
		if scope == InheritPreflight {
			copyKeys(m, out, "mise")
		} else {
			copyKeys(m, out, "nix", "mise")
		}
	case "machine":
		if scope == InheritNested {
			copyKeys(m, out, "resources", "ports", "forward")
		}
	case "env":
		if scope == InheritNested {
			copyKeys(m, out, "sources", "files", "set", "unset")
			// A null list is a legacy env_sources null that replaced the lists beneath it
			// (lowerLegacyEnvSources). The handed-in scope is the jail's bottom user layer,
			// with nothing beneath to replace, so the null means what absence does.
			for _, key := range []string{"sources", "files"} {
				if v, present := out.Get(key); present && v == nil {
					out.Delete(key)
				}
			}
		}
	default:
		return nil, false
	}
	if out.Len() == 0 {
		return nil, true
	}
	return out, true
}

// RenderFiltered renders an ALREADY-FILTERED map as a generated inner-scope file: the
// header comment followed by canonical snapshot JSON.
//
// Canonical JSON (not a pretty-printed JSONC) because the whole point is that this is a
// RENDER of the one computation `yolo config dump` already serializes: same sorted keys,
// same escaping, same stable bytes. JSONC is a superset of JSON, so the file parses
// through the ordinary config loader with no special case, and the leading `//` comments
// ride along.
func RenderFiltered(filtered *jsonx.OrderedMap, scope InheritScope, launchedAt string) (string, error) {
	return RenderFilteredWithOrigins(filtered, nil, scope, launchedAt)
}

// RenderFilteredWithOrigins is RenderFiltered with the file's launch origins (launchorigins.go)
// from src, the provenance of the effective config filtered was cut from: one comment line at the
// end of the header naming, for each value, the host file that set it, so a refusal in the jail
// names that file rather than this one. A nil src writes no such line.
//
// IT REFUSES A VALUE THE HOST READ AS TOML WHERE THE READERS JUDGE BY FORMAT. Two readers decide a
// value's meaning by the format of the file that wrote it: a pack entry (packEntryIsReadable) and
// a provider's readable-only key (tomlProviderKeys). In the jail every value of a JSONC copy reads
// as legacy, so one of those a TOML file wrote — a handle qualified by a named source, a bare git
// table, a provider's own `model` — read as broken there although the host accepted it. Such a
// value crosses only as TOML (RenderFilteredForDelivery). Every other value means the same in
// both formats and may cross either way.
func RenderFilteredWithOrigins(filtered *jsonx.OrderedMap, src *Sources, scope InheritScope, launchedAt string) (string, error) {
	if where := formatJudgedTOMLValue(filtered, src); where != "" {
		return "", fmt.Errorf("the inherited %s user config cannot cross as legacy JSONC: %s was written in TOML, "+
			"and the jail's legacy reader would misjudge it", scope, where)
	}
	body, err := SnapshotJSON(filtered)
	if err != nil {
		return "", err
	}
	header := InheritHeader(scope, launchedAt)
	if record := LaunchOriginsJSON(filtered, src); record != "" {
		header += "//\n// The line below records which host file set each key, so a refusal in this jail\n" +
			"// names that file.\n" + originsLinePrefix + record + "\n"
	}
	return header + body + "\n", nil
}

// formatJudgedTOMLValue names the first value of values a reader judges by its file's format
// that src says a TOML file wrote — "config.packs[1] (<file>:<line>:<col>)" — or "" when there is
// none or src does not know.
func formatJudgedTOMLValue(values *jsonx.OrderedMap, src *Sources) string {
	if src == nil || src.root == nil || values == nil {
		return ""
	}
	fromTOML := func(n *srcNode) bool {
		origins := n.origins()
		return len(origins) > 0 && origins[0].format() == configdoc.TOML
	}
	named := func(path string) string {
		if locations := src.Locations(path); len(locations) > 0 {
			return path + " (" + locations[0] + ")"
		}
		return path
	}
	if raw, ok := values.Get("packs"); ok {
		if list, ok := raw.([]any); ok {
			packs := src.root.keys["packs"]
			for i := range list {
				if n := packs.elem(i); n != nil && fromTOML(n) {
					return named(fmt.Sprintf("config.packs[%d]", i))
				}
			}
		}
	}
	if raw, ok := values.Get(providersKey); ok {
		if providers, ok := raw.(*jsonx.OrderedMap); ok {
			readableOnly := readableOnlyProviderKeys()
			for _, name := range providers.Keys() {
				entry, _ := providers.Get(name)
				fields, ok := entry.(*jsonx.OrderedMap)
				if !ok {
					continue
				}
				for _, key := range fields.Keys() {
					if readableOnly[key] && src.writtenInTOML(providersKey, name, key) {
						return named("config." + providersKey + "." + name + "." + key)
					}
				}
			}
		}
	}
	return ""
}

// InheritTOMLHeader is InheritHeader using TOML's comment prefix.
func InheritTOMLHeader(scope InheritScope, launchedAt string) string {
	lines := strings.Split(InheritHeader(scope, launchedAt), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "//") {
			lines[i] = "#" + strings.TrimPrefix(line, "//")
		}
	}
	return strings.Join(lines, "\n")
}

// RenderFilteredForDelivery is RenderInheritedDelivery with no lowering, for a caller that
// prints no disclosure: the rendered file, its format and the JSONC route's reason. A launch uses
// RenderInheritedDelivery, which also returns what a TOML delivery left out.
func RenderFilteredForDelivery(filtered *jsonx.OrderedMap, src *Sources, scope InheritScope, launchedAt string) (string, configdoc.Format, string, error) {
	d, err := RenderInheritedDelivery(filtered, src, scope, launchedAt, nil)
	return d.Body, d.Format, d.FallbackReason, err
}

// InheritOmission is one legacy-written value a TOML delivery of the inherited user scope leaves
// out because the jail's TOML reader has no spelling for it (LC-D4,
// docs/design/legacy-config-compat.md): the launch discloses each one once, and nothing refuses.
type InheritOmission struct {
	// Path is the value as the validators spell it: "config.loopholes.lab-tool".
	Path string
	// Where is the host file, line and column that wrote it; "" when the record does not hold it.
	Where string
	// Reason says why it has no TOML spelling, what the jail does without it, and the step where
	// a ruling names one.
	Reason string
}

// String is the omission as one disclosure line's body.
func (o InheritOmission) String() string {
	where := ""
	if o.Where != "" {
		where = " (" + o.Where + ")"
	}
	return o.Path + where + ": " + o.Reason
}

// UserScopeLowering is the lowering of a legacy user-scope read to its TOML spelling that `yolo
// config migrate` writes (configmigrate.LowerInheritedUserScope): the conversions resting on
// pack declarations and the provider composition, which this package cannot reach. tomlWritten
// reports whether a TOML file wrote the value at a validator-spelled path ("packs[0]",
// "providers.x.model"); the lowering respells only the values it does not (LC-D5) and leaves
// every TOML-written value as written. It never declines: a legacy value with no TOML spelling
// is left out and returned as an omission (LC-D4).
type UserScopeLowering func(values *jsonx.OrderedMap, tomlWritten func(path string) bool) (*jsonx.OrderedMap, []InheritOmission)

// InheritedDelivery is one rendered inherited scope file.
type InheritedDelivery struct {
	Body   string
	Format configdoc.Format
	// FallbackReason is set when a scope with no TOML-written value a reader judges by format
	// crosses as JSONC.
	FallbackReason string
	// Omissions are the legacy-written values a TOML delivery left out, each to be disclosed.
	Omissions []InheritOmission
}

// RenderInheritedDelivery renders a filtered inherited scope for the jail. It never refuses a
// scope the host accepted (LC-D4, which supersedes RC-D55's "still refused"):
//
//   - a scope the readable schema takes as it is (after the read-side lowerings and the null
//     lowering) crosses as TOML;
//   - a scope holding no value the jail's legacy reader would misjudge (formatJudgedTOMLValue)
//     crosses as JSONC with its origins line, as before, a legacy loopholes block and all;
//   - any other scope crosses as TOML, always: each TOML-written value as written, each legacy
//     value through lower (the migration's lowering applied to legacy-written values only,
//     LC-D5), and a legacy value neither spells is left out and returned as an omission.
//
// A legacy `null` the readable schema has no removal spelling for means what absence means
// (LC-D8): the scope is the jail's bottom user layer, with nothing beneath it to remove. The only
// error left is a yolo bug: a TOML-written value the jail's TOML reader refuses.
func RenderInheritedDelivery(filtered *jsonx.OrderedMap, src *Sources, scope InheritScope, launchedAt string, lower UserScopeLowering) (InheritedDelivery, error) {
	values := jsonx.DeepCopyMap(filtered)
	var node *srcNode
	if src != nil {
		node = src.node()
	}
	values, node = projectLegacyB2Config(values, node)
	tomlValues := jsonx.DeepCopyMap(values)
	// A legacy null crosses in its TOML removal spelling (an empty list, or "off"), the one
	// lowering `yolo config migrate` writes too (RC-D50); one with no removal spelling is absent.
	for _, left := range LowerLegacyNulls(tomlValues) {
		deleteDottedPath(tomlValues, strings.Split(left, "."))
	}
	body, err := encodeInheritedTOML(jsonx.DeepCopyMap(tomlValues), values, node, scope, launchedAt)
	if err == nil {
		return InheritedDelivery{Body: body, Format: configdoc.TOML}, nil
	}
	if formatJudgedTOMLValue(filtered, src) == "" {
		legacy, legacyErr := RenderFilteredWithOrigins(filtered, src, scope, launchedAt)
		if legacyErr != nil {
			return InheritedDelivery{}, legacyErr
		}
		return InheritedDelivery{Body: legacy, Format: configdoc.JSONC,
			FallbackReason: "legacy user-scope values are not representable by the current TOML reader: " + err.Error()}, nil
	}
	tomlWritten := func(path string) bool {
		n := node.follow(path)
		if n == nil {
			return false
		}
		origins := n.origins()
		return len(origins) > 0 && origins[0].format() == configdoc.TOML
	}
	lowered := tomlValues
	var omissions []InheritOmission
	if lower != nil {
		lowered, omissions = lower(jsonx.DeepCopyMap(tomlValues), tomlWritten)
	}
	for i := range omissions {
		if omissions[i].Where == "" {
			omissions[i].Where = firstLocation(src, omissions[i].Path)
		}
	}
	// The schema is the last word: a legacy value still outside it is left out and disclosed.
	for {
		v, err := encodedReadableViolation(lowered)
		if err != nil {
			return InheritedDelivery{}, fmt.Errorf("the inherited %s user config cannot cross into the jail: %v. This is a "+
				"yolo bug: report it with this message", scope, err)
		}
		if v == nil {
			break
		}
		display := v.display()
		if tomlWritten(display) {
			return InheritedDelivery{}, fmt.Errorf("the inherited %s user config cannot cross into the jail: config.%s (%s) "+
				"was written in TOML and the host accepted it, but the jail's TOML reader refuses it (%s). This is a yolo "+
				"bug: report it with this message", scope, display, firstLocation(src, "config."+display), v.message)
		}
		if !deleteAtSteps(lowered, v.locationPath) {
			return InheritedDelivery{}, fmt.Errorf("the inherited %s user config cannot cross into the jail: config.%s "+
				"(%s) has no TOML spelling and could not be left out. This is a yolo bug: report it with this message",
				scope, display, v.message)
		}
		// A table the omission emptied held only what was left out, so it goes too.
		for up := v.locationPath[:len(v.locationPath)-1]; len(up) > 0; up = up[:len(up)-1] {
			if m, ok := valueAtSteps(lowered, up); !ok || !isEmptyMap(m) || !deleteAtSteps(lowered, up) {
				break
			}
		}
		omissions = append(omissions, InheritOmission{Path: "config." + display, Where: firstLocation(src, "config."+display),
			Reason: v.message + "; the TOML user scope the jail reads has no spelling for it, so the jail reads its user " +
				"scope without it, and the host's own launch reads it as before"})
	}
	// No host origins: the jail judges a value's format by the file its origin names
	// (formatJudgedTOMLValue), a value the record says nothing about takes its parent's, and the
	// host file behind a respelled value is the legacy one, while what the jail reads is the TOML
	// yolo wrote for it, which is the right format for every value it holds.
	body, err = encodeInheritedTOML(lowered, nil, node, scope, launchedAt)
	if err != nil {
		return InheritedDelivery{}, fmt.Errorf("the inherited %s user config cannot cross into the jail: %v. This is a "+
			"yolo bug: report it with this message", scope, err)
	}
	return InheritedDelivery{Body: body, Format: configdoc.TOML, Omissions: omissions}, nil
}

// firstLocation is the first place src records for a validator-spelled path ("config.x"), or "".
func firstLocation(src *Sources, path string) string {
	if locations := src.Locations(path); len(locations) > 0 {
		return locations[0]
	}
	return ""
}

// deleteDottedPath removes the value at a dotted map path, if it is there.
func deleteDottedPath(values *jsonx.OrderedMap, path []string) {
	current := values
	for i, key := range path {
		if i == len(path)-1 {
			current.Delete(key)
			return
		}
		v, _ := current.Get(key)
		next, ok := asMap(v)
		if !ok {
			return
		}
		current = next
	}
}

// deleteAtSteps removes the value at a document location path (map keys and list indexes),
// reporting whether it was there.
func deleteAtSteps(values *jsonx.OrderedMap, steps []configdoc.Step) bool {
	if len(steps) == 0 {
		return false
	}
	parent, ok := valueAtSteps(values, steps[:len(steps)-1])
	if !ok {
		return false
	}
	last := steps[len(steps)-1]
	switch p := parent.(type) {
	case *jsonx.OrderedMap:
		if _, present := p.Get(last.Key); last.IsIndex || !present {
			return false
		}
		p.Delete(last.Key)
		return true
	case []any:
		if !last.IsIndex || last.Index < 0 || last.Index >= len(p) || len(steps) < 2 {
			return false
		}
		// A list is its parent's value, so the shortened list is written back there.
		shortened := append(append([]any(nil), p[:last.Index]...), p[last.Index+1:]...)
		grand, ok := valueAtSteps(values, steps[:len(steps)-2])
		if !ok {
			return false
		}
		at := steps[len(steps)-2]
		switch g := grand.(type) {
		case *jsonx.OrderedMap:
			g.Set(at.Key, shortened)
			return true
		case []any:
			g[at.Index] = shortened
			return true
		}
	}
	return false
}

// encodedReadableViolation is the first value the jail's TOML reader would refuse in values,
// judged as encodeInheritedTOML judges it (encoded, parsed back, numbers normalized), or nil.
// values' integers are normalized in place, as the encoding does.
func encodedReadableViolation(values *jsonx.OrderedMap) (*readableTOMLViolation, error) {
	if err := normalizeInheritedTOMLIntegers(values); err != nil {
		return nil, err
	}
	encoded, err := configdoc.Encode(values, configdoc.TOML)
	if err != nil {
		return nil, err
	}
	doc, err := configdoc.Parse(encoded, configdoc.TOML)
	if err != nil {
		return nil, err
	}
	if err := normalizeTOMLNumbers(doc.Values); err != nil {
		return nil, err
	}
	return firstReadableTOMLViolation(doc.Values, SchemaScopeUserOnly), nil
}

func isEmptyMap(v any) bool {
	m, ok := v.(*jsonx.OrderedMap)
	return ok && m.Len() == 0
}

// valueAtSteps is the value at a document location path.
func valueAtSteps(values *jsonx.OrderedMap, steps []configdoc.Step) (any, bool) {
	var current any = values
	for _, step := range steps {
		switch c := current.(type) {
		case *jsonx.OrderedMap:
			if step.IsIndex {
				return nil, false
			}
			v, ok := c.Get(step.Key)
			if !ok {
				return nil, false
			}
			current = v
		case []any:
			if !step.IsIndex || step.Index < 0 || step.Index >= len(c) {
				return nil, false
			}
			current = c[step.Index]
		default:
			return nil, false
		}
	}
	return current, true
}

// encodeInheritedTOML encodes tomlValues as the generated TOML scope file and proves the jail's
// reader takes it, or fails naming why. origins is the read the header's provenance line is cut
// from (values before any respelling, so a refusal in the jail still names the host file); nil
// writes no such line.
func encodeInheritedTOML(tomlValues, origins *jsonx.OrderedMap, node *srcNode, scope InheritScope, launchedAt string) (string, error) {
	if err := normalizeInheritedTOMLIntegers(tomlValues); err != nil {
		return "", err
	}
	encoded, err := configdoc.Encode(tomlValues, configdoc.TOML)
	if err != nil {
		return "", err
	}
	doc, err := configdoc.Parse(encoded, configdoc.TOML)
	if err != nil {
		return "", err
	}
	if err := normalizeTOMLNumbers(doc.Values); err != nil {
		return "", err
	}
	if err := validateReadableTOML(doc.Values, SchemaScopeUserOnly, "inherited user scope", doc.Index); err != nil {
		return "", err
	}
	header := InheritTOMLHeader(scope, launchedAt)
	if record := LaunchOriginsJSON(origins, sourcesOf(node)); origins != nil && record != "" {
		header += "#\n# The line below records which host file set each key, so a refusal in this jail\n" +
			"# names that file.\n" + originsTOMLLinePrefix + record + "\n"
	}
	return header + string(encoded) + "\n", nil
}

func normalizeInheritedTOMLIntegers(value any) error {
	switch current := value.(type) {
	case *jsonx.OrderedMap:
		for _, key := range current.Keys() {
			item, _ := current.Get(key)
			normalized, err := inheritedTOMLInteger(item)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			current.Set(key, normalized)
		}
	case []any:
		for i, item := range current {
			normalized, err := inheritedTOMLInteger(item)
			if err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
			current[i] = normalized
		}
	}
	return nil
}

func inheritedTOMLInteger(value any) (any, error) {
	if jsonx.IsInt(value) {
		integer, ok := jsonx.AsInt(value)
		if !ok {
			return nil, fmt.Errorf("integer is outside TOML's signed 64-bit range")
		}
		return integer, nil
	}
	switch value.(type) {
	case *jsonx.OrderedMap, []any:
		if err := normalizeInheritedTOMLIntegers(value); err != nil {
			return nil, err
		}
	}
	return value, nil
}

// RenderInherit filters and renders in one step — the whole-file form, for callers with no
// reason to inspect the filtered map.
func RenderInherit(effective *jsonx.OrderedMap, scope InheritScope, launchedAt string) (string, []string, error) {
	filtered, unknown, err := FilterInheritErr(effective, scope)
	if err != nil {
		return "", nil, err
	}
	out, err := RenderFiltered(filtered, scope, launchedAt)
	return out, unknown, err
}

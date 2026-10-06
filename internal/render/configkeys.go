package render

// configkeys.go extends the census from contribution kinds to CONFIG KEYS
// (docs/design/declaration-parity.md, OQ-DP5's second half, ruled 2026-09-13; DP-B31).
//
// THE GAP IT CLOSES. FieldSet was the machinery built to guarantee that nothing a pack declares
// is silently absent at a target, and it was keyed on packdecl.Kind alone. A top-level config
// key is not a kind, so the census could not see `packages`, `mounts`, `network` or `resources`
// at all: `yolo host apply` was taught to name `packages` and `mise_tools` by two hand-written
// calls, and every other key a host does nothing with went unsaid. A hand-written call closes
// one instance; the next key added to the schema got no better treatment.
//
// THE SHAPE IS internal/config/inherit.go's, as the ruling asked: one entry per top-level key,
// each with a disposition AND a reason, exhaustive over the schema by test
// (TestHostConfigKeyCensusIsTotal), so a key added to the config fails the build until it is
// classified here. The reason is written for the reader who re-decides the key when its
// consumers change; like the kinds' reasons (render.refusalReasons, render.hostUnimplemented),
// NO TERMINAL VIEW PRINTS IT. The report names the key and stops.
//
// THE UNIT IS THE NOTCH, NOT ONE COMMAND. A key is KeyHonored at the host when ANY host-notch
// verb does what it declares — `yolo host apply`, `yolo host -- <cmd>`, `yolo host env`, the
// host floor, or a host-side command acting on a setting of this machine's, as `yolo check`
// warns at the `prune` threshold. A command that only READS a key to account for its effect in
// a jail does not honor it: `yolo stores` and `yolo prune` read `cache_relocations` to find the
// segments it moved, and what the key declares, a jail's cache on a host path, is still a
// jail's. That differs on purpose from the kinds' honored-but-unbuilt map, which is a
// limit of the APPLY command (`env` is there because apply never starts a process, though
// `yolo host --` delivers it): a config key the report names is one no host verb honors, so
// "does not apply at the host" is true of every key it prints. `env_sources` and `adapters`,
// which only `yolo host --` reads, are therefore honored here and never named by the apply.

// KeyDisposition is what a target does with one top-level config key.
type KeyDisposition int

const (
	// KeyUnclassified is the zero value: the census states nothing for the key at this target
	// (a jail's FieldSet, or a key the schema does not have).
	KeyUnclassified KeyDisposition = iota
	// KeyHonored: some verb at this target acts on the key. The reason names the reader.
	KeyHonored
	// KeyNotApplicable: the key has no meaning at this target, and the reason is terminal —
	// OQ-DP5's shape (a), a decline yolo decides, said in one line.
	KeyNotApplicable
	// KeyUnbuilt: the key applies at this target and nothing honors it yet — OQ-DP5's shape
	// (c), held as data. Like render.hostUnimplemented, an entry leaves this disposition the
	// day its reader is built, and none left is the end state.
	KeyUnbuilt
)

// LeftUndone reports whether a declaration of the key does nothing at the target, which is
// the question the report asks: both a key with no meaning here and one not built yet.
func (d KeyDisposition) LeftUndone() bool {
	return d == KeyNotApplicable || d == KeyUnbuilt
}

// keyCensusEntry is one key's classification at one target.
type keyCensusEntry struct {
	at KeyDisposition
	// reason explains the classification in one sentence, in terms of the reader (honored) or
	// of why there is none. Required for every entry, honored ones included, for inherit.go's
	// reason: a classification with no stated reason is indistinguishable from a guess.
	reason string
}

// ConfigKey returns what this FieldSet's target does with a top-level config key, and the
// census reason. KeyUnclassified, "" when the target states no key census (JailFields: a jail
// launch is the maximal target, and its readers are the launch pipeline itself) or the key is
// not one the schema has.
//
// Target.Fields() hands the host's FieldSet to `guest`, `preview` and an unset target too, so
// those get the host's key answers, exactly as they get its kind answers (DP-B28's caveat,
// which applies here unchanged).
func (f FieldSet) ConfigKey(key string) (KeyDisposition, string) {
	e, ok := f.keys[key]
	if !ok {
		return KeyUnclassified, ""
	}
	return e.at, e.reason
}

// hostConfigKeys classifies every LIVE top-level config key at the host notch. THIS IS THE
// CENSUS for config keys; HostFields carries it. Retired spellings are not here: validation
// refuses them before any target is reached, so a disposition for one would be data nothing
// reads.
//
// Every honored entry names its reader, and each was checked against the tree when written
// (2026-10-01); re-check one before relying on it, because a reader that moves is exactly what
// turns an entry false without touching this file.
var hostConfigKeys = map[string]keyCensusEntry{
	// ---- Honored: some host-notch verb acts on the key ---------------------------------
	"packs": {KeyHonored, "the selection `yolo host apply` renders and `yolo host --` launches from"},
	"profile": {KeyHonored, "the selection the host composes for each agent, applied edge-triggered " +
		"by `yolo host apply` and resolved by `yolo host --`"},
	"profiles":  {KeyHonored, "user-declared profiles the host resolves the selection over"},
	"providers": {KeyHonored, "the provider table the host's derives and `yolo host --` compose"},
	"adapters": {KeyHonored, "the address an adapted provider is reached at, read by `yolo host --`'s " +
		"provider composition (LoadAdapterAddresses)"},
	"env_sources": {KeyHonored, "the dotenv files and values `yolo host --` and `yolo host env` " +
		"deliver through the credential gate"},
	// Honored as a KEY, and its one undone entry shape is named PER ENTRY rather than here: an
	// inline loophole (an entry with a `command` and no manifest) is a host daemon whose only
	// client is a jail, and `yolo host apply` names each enabled one on its notch line
	// (cli.inertInlineLoopholes, run.HostInlineLoopholes), as it names a source-bearing
	// host_files entry by destination.
	"loopholes": {KeyHonored, "`yolo host --` opens an enabled loophole's doorway for the agent whose " +
		"selection asks for one (PlanHostDoorways), and a loophole's settings feed the region fill; " +
		"the jail daemons themselves have no client off-container, and an inline loophole is named " +
		"by entry as not applying"},
	"mcp_servers": {KeyHonored, "composed into every agent's MCP files by `yolo host apply` " +
		"(composeHostInputs), over the selected packs' `mcp` entries, less an entry naming a path " +
		"only a jail has, which it names"},
	"lsp_servers": {KeyHonored, "composed into every agent's LSP files by `yolo host apply` " +
		"(composeHostInputs), Claude's as the yolo-lsp plugin in every skills destination " +
		"(applyHostLSPPlugin), less an entry naming a path only a jail has, which it names"},
	"host_files": {KeyHonored, "a source-less entry is written into your real home by `yolo host " +
		"apply`; one with a source mirrors a host file into a jail and is named by destination"},
	"host_management": {KeyHonored, "the ownership contract `yolo host apply` renders your real " +
		"home under (hostOwnership)"},
	"host_apply_on_launch": {KeyHonored, "gates the apply a wrapped `yolo host -- <agent>` runs " +
		"before it execs (hostapplygate.go)"},
	"host_wrappers": {KeyHonored, "the launch wrappers `yolo host apply` writes for the host's PATH"},
	"host_floor": {KeyHonored, "which selected packs' programs the host floor installs " +
		"(HostFloorWire)"},
	"host_path":     {KeyHonored, "folders `yolo host`'s tool lookup searches after its own PATH"},
	"agent_updates": {KeyHonored, "the host floor's update policy, as it is a jail launcher's"},
	"agents_md_extra": {KeyHonored, "prose `yolo host apply` appends to the briefing it writes " +
		"(applyhostbriefings.go)"},
	"briefing_provenance": {KeyHonored, "shapes the text of the briefing `yolo host apply` writes"},
	"confinement": {KeyHonored, "`yolo apply` reads it to pick its notch, so `confinement: host` is " +
		"what makes a bare `yolo apply` render at the host"},
	"runtime": {KeyHonored, "the backend the host floor's `yolo capture` boots its jail on " +
		"(captureRuntime), and what `yolo host apply` reads to say whether a darwin package " +
		"profile can be materialized"},
	"include_if_found": {KeyHonored, "resolved by the loader into the user scope every host verb reads"},
	"update_check":     {KeyHonored, "gates this machine's own update check (UpdateCheckEnabled)"},
	"promotion_target": {KeyHonored, "where `yolo config promote`, a host-side command, writes " +
		"captured settings"},
	"prune": {KeyHonored, "the free-disk threshold `yolo check`'s disk section warns at, on this " +
		"machine"},
	"perf_logging": {KeyHonored, "`yolo host --` and `yolo host apply` record their spans when it " +
		"is on, silently, into the machine-wide host-notch-perf.log (run.HostNotchTimingLog, " +
		"reading config.PerfLoggingEnabled), as a jail launch records into its workspace's file"},
	"required_capabilities": {KeyHonored, "`yolo host --` asks OQ-CAP2's gate over the user scope " +
		"it composes from, after the pack refresh and before its apply gate " +
		"(refuseHostUnmetCapabilities), refusing as a jail launch does"},
	"security": {KeyHonored, "the user scope's `blocked_tools` are rendered by `yolo host --` into " +
		"shims first on the PATH of the program it starts, beside the selected packs' " +
		"(composeHostBlockers, HE-D11); a workspace's are never read at the host"},

	// ---- Not applicable: the key means nothing off-container -------------------------
	// A GRANT, not a mechanism (docs/design/yolo-as-environment-manager.md §4: "the grants stay
	// where they are … at lower notches they are inert"). The reason used to be "a mount needs a
	// mount namespace", false of macos-user, which links a context mount into its sandbox with
	// none.
	"mounts": {KeyNotApplicable, "a mount grants a jail a host folder through the jail's wall; at " +
		"the host there is no wall, and the folder is already where you are"},
	"workspace_readonly": {KeyNotApplicable, "locks workspace paths read-only inside a jail; at the " +
		"host the workspace is your own directory"},
	"per_side_paths": {KeyNotApplicable, "shadow-mounts a jail's own copy over workspace paths, so " +
		"host and jail keep separate ones; at the host there is only the one side"},
	"writable_home_dirs": {KeyNotApplicable, "names jail-writable home subtrees; off-container the " +
		"home simply is writable"},
	"ephemeral_storage": {KeyNotApplicable, "the backing for a container's /tmp, /var/tmp and " +
		"/var/lib/containers; the host's are your own"},
	"cache_relocations": {KeyNotApplicable, "moves a jail's cache segments onto host paths; " +
		"off-container the caches are already yours (host-side, `yolo stores` and `yolo prune` " +
		"read it only to account for the relocated segments)"},
	"network": {KeyNotApplicable, "a container's network mode, published ports and forwarded host " +
		"ports; a host process is already on your network"},
	"resources": {KeyNotApplicable, "the memory, CPU and disk I/O limits a jail is run under; " +
		"yolo bounds no process it does not contain"},
	"devices": {KeyNotApplicable, "passes host devices into a container; at the host they are " +
		"already yours"},
	"gpu": {KeyNotApplicable, "passes the host GPU into a container; at the host it is already yours"},
	"kvm": {KeyNotApplicable, "passes /dev/kvm into a container; at the host it is already yours"},
	"macos_log": {KeyNotApplicable, "dials what the macos-user sandbox's yolo-log helper may read; " +
		"the host notch runs no sandbox and installs no helper"},
	"mise_tools": {KeyNotApplicable, "a jail composes mise's config from it, and nothing at the " +
		"host manages your own mise"},
	"mcp_presets": {KeyNotApplicable, "a preset's command is a wrapper only a jail's boot writes " +
		"(HC-D27), so `yolo host apply` writes none and names each one it leaves out; the " +
		"chrome-devtools pack carries that server to the host, in a jail and on macos-user"},
	// `brokered` is a workspace's own key, read only by a jail launch's config-change gate, which
	// approves its `brokered.<source>.repos` entry and hands the result to the GitHub broker's
	// scope file (run's writeScopeFiles), and by `yolo check`'s report of that gate; no host-notch
	// verb starts the broker. `yolo host apply` reads the user scope, where the key is refused
	// (docs/design/workspace-widening.md WW-D9), so only a refused value can reach this entry.
	"brokered": {KeyNotApplicable, "lists the repositories a jail's GitHub broker admits for one " +
		"workspace, from that workspace's own config, and a user-config value is refused; the " +
		"broker is not offered at the host (boundary-broker.md BB-D17), where an agent runs your " +
		"own gh"},
	"programs": {KeyNotApplicable, "`programs.autoprune` lets a jail's boot delete the orphaned " +
		"agent binaries in its home; the host floor removes a deselected program on an owned " +
		"host's `yolo host apply --assert` whatever this key says (HP-D8), and under \"none\" " +
		"`yolo check` names the removal by hand"},
	// Not unbuilt: host-tool-provisioning.md's HP-DIR3 (2026-09-29) rules that at the host yolo
	// manages the agent's environment and never the workspace's runtime, which is what
	// `packages:` declares, so provisioner-sets.md's OQ-PS1 gives darwinpkg.MaterializeAt no host
	// caller and OQ-NX8 calls the key permanently inert here.
	"packages": {KeyNotApplicable, "the workspace's runtime, which a jail gets from its image or the " +
		"boot-written store farm; at the host yolo never provisions it (HP-DIR3), and the tools " +
		"there are the ones on your own PATH"},

	// ---- Unbuilt: the key applies at the host and nothing honors it yet ---------------
	// None, since 2026-10-04: `required_capabilities`, the last, gained its host reader.
	// KeyUnbuilt stays the disposition for the next key that applies here before its reader
	// exists.
}

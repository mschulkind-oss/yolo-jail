package config

// inherit.go answers "what does an inner `yolo` running INSIDE a jail get to read as its
// user scope?" — OQ-LP9's three-part split (docs/design/loophole-packaging.md §OQ-LP9).
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
//     config.jsonc and config.lua were mounted, so `include_if_found` files stayed
//     host-side. The raw bind was already filtering — by accident.
//
// THE MODEL. "User level" is not a fixed path; it is the scope that owns the machine a
// daemon runs on (docs/design/gate-placement-principle.md). On the human's laptop that is
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
	"sort"
	"strings"

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
	"loopholes": {preflight: true, nested: true, reason: "read by `yolo loopholes list/status` and spawned by an inner launcher"},
	// `packs` is user-scope-only BY CONSTRUCTION (config/packs.go reads the user file
	// directly), so it can only ever arrive through this file. Both consumers need it:
	// `yolo pack ls/status` reports it, and an inner launcher stages from it.
	"packs": {preflight: true, nested: true, reason: "reported by `yolo pack ls/status` and staged by an inner launcher"},
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
	// THE IN-JAIL-PROVISIONING KEYS, all five in BOTH — and the preflight half is MEASURED,
	// not judged. `yolo check`'s entrypoint dry-run (check/entrypoint.go) feeds exactly
	// these into a temp home as YOLO_BLOCK_CONFIG / YOLO_MISE_TOOLS / YOLO_LSP_SERVERS /
	// YOLO_MCP_SERVERS / YOLO_MCP_PRESETS and runs the real generators over them, so an
	// in-jail check genuinely evaluates each; and the run pipeline feeds the same five to a
	// real container. One consumer VALIDATES, the other COMPOSES — which is why "in one
	// file" must not be allowed to mean "in both" by default: these earn both memberships.
	//
	// They are also the key class the raw bind got RIGHT, and the reason the filter is not
	// "drop anything host-shaped": every referent here is inside the jail.
	"security":    {preflight: true, nested: true, reason: "blocked_tools decides this jail's shims; the check dry-run generates them and a launcher passes them on"},
	"mise_tools":  {preflight: true, nested: true, reason: "the check dry-run runs ConfigureMisePrism over it; an inner launcher installs from it"},
	"mcp_servers": {preflight: true, nested: true, reason: "MCP processes run in the jail; the check dry-run renders their wrappers and a launcher passes them on"},
	"mcp_presets": {preflight: true, nested: true, reason: "MCP presets the check dry-run resolves and an inner launcher passes on"},
	"lsp_servers": {preflight: true, nested: true, reason: "LSP servers installed in the jail; the check dry-run renders their config"},
	"providers":   {preflight: true, nested: true, reason: "cloud provider declarations for agent configuration and nested launches"},
	// `profiles` and `use_profiles` are user-scope-only (OQ-CS5 ruled BOTH keys), exactly
	// like `packs` three entries up — so a workspace spelling can never reach the render
	// that this census classifies, because the launch refuses it first. The two keys
	// arrive here differently, which is the distinction worth recording: `profiles` is
	// user-scope-only BY CONSTRUCTION (config/profiles.go reads the user file directly),
	// while `use_profiles` is read off the merged config and is user-scope-only BY
	// REFUSAL (config.validateProfiles errors on a workspace spelling). The host CLI
	// resolves both into the YOLO_PROFILES/YOLO_USE_PROFILES tables for THIS jail, and an
	// inner launcher composes the same tables for the jail it spawns.
	"profiles":              {preflight: true, nested: true, reason: "user-declared profiles over provider-declared options; the launch resolves them into YOLO_PROFILES here and in nested launches"},
	"use_profiles":          {preflight: true, nested: true, reason: "active CLI-to-profile-name selections for this jail and nested launches (keys are CLI names: core knows packs, not agents)"},
	"required_capabilities": {preflight: true, nested: true, reason: "required capabilities validated at pre-flight and passed to nested launches"},

	// ---- Preflight only ---------------------------------------------------------
	// `agents_md_extra` is briefing prose rendered into this jail's own AGENTS.md.
	"agents_md_extra": {preflight: true, reason: "prose rendered into this jail's briefing"},
	// `writable_home_dirs` names paths under /home/agent — in-jail referents, and the
	// config-ref explicitly calls the key safe at any scope.
	"writable_home_dirs": {preflight: true, reason: "names /home/agent subpaths, which exist in here"},
	// `confinement` is the notch this environment IS; an in-jail reader reporting it is
	// reporting a fact about itself.
	"confinement": {preflight: true, reason: "names the notch this environment is running at"},

	// ---- Nested only ------------------------------------------------------------
	// The image-composition keys. Nothing in-jail VALIDATES these against a referent (an
	// in-jail `yolo check --no-build` skips the image section entirely, measured); an
	// inner launcher BAKES them. So they are the nested file's core and are absent from
	// preflight, where they would only invite a judgement about a host image.
	"packages":  {nested: true, reason: "baked into the image an inner launcher builds"},
	"resources": {nested: true, reason: "memory/cpu limits an inner launcher applies to its container"},
	"network":   {nested: true, reason: "the network mode an inner launcher gives its container"},
	// `env_sources` is nested-only for a measured reason, not a judgement: its string
	// entries are HOST FILE PATHS, and resolving them in-jail emits "env_sources file not
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
	// `host_apply_on_launch`: the same class again, and the sharpest of the three. It gates a
	// re-render of the invoking user's REAL home at `yolo host -- <bin>`, and in a jail
	// paths.Home() is /home/agent — so the key's referent rebinds to the container's own
	// disposable home exactly as host_files' does, while the mechanism it gates (a wrapped host
	// launch) does not exist in here at all. Inheriting it would hand a nested launcher a
	// standing licence to write a home the key was never about.
	"host_apply_on_launch": {reason: "gates a re-render of the HOST's real home at a wrapped " +
		"host launch — a jail has neither the home nor the launch"},
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
	// `runtime`: judged against the wrong machine. A host `macos-user` value read in a
	// Linux container names a runtime that cannot exist in here, and an inner launcher
	// must pick its own (YOLO_RUNTIME and auto-detect already decide it).
	"runtime": {reason: "an in-jail launcher detects its own runtime; a host value names a machine that is not this one"},
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
	"journal":        {reason: "RETIRED — the switch moved to loopholes.journal.enabled and the mode to its settings; emitting it would re-trigger the retirement error"},
	// `agent_profiles` joined them on 2026-09-01, renamed to `pack_profiles` — the
	// keys were always CLI names and core knows packs, not agents — which was itself
	// renamed to `use_profiles` on 2026-09-02 (docs/reference/providers.md — Profiles and options:
	// `pack` named neither of the two things the key holds). `pack_profiles` is gone
	// with no census entry at all: it never shipped in a release, so the old spelling is
	// an ordinary unknown key (config.go's rule comment).
	"agent_profiles": {reason: "RETIRED — renamed to use_profiles (briefly pack_profiles, never in a release); emitting it would re-trigger the retirement error"},
}

// InheritDisposition returns the census entry for a key, and ok=false for a key the census
// does not know. Callers use it to explain a dropped key rather than dropping it silently.
func InheritDisposition(key string) (preflight, nested bool, reason string, ok bool) {
	d, found := inheritCensus[key]
	if !found {
		return false, false, "", false
	}
	return d.preflight, d.nested, d.reason, true
}

// InheritKeys returns the keys a scope emits, sorted. Sorted because it feeds a HEADER
// comment and a test, both of which want a stable order.
func InheritKeys(scope InheritScope) []string {
	var out []string
	for k, d := range inheritCensus {
		if (scope == InheritPreflight && d.preflight) || (scope == InheritNested && d.nested) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// FilterInherit projects an effective config down to one scope's keys.
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
	out = jsonx.NewOrderedMap()
	if effective == nil {
		return out, nil
	}
	for _, k := range effective.Keys() {
		d, ok := inheritCensus[k]
		if !ok {
			unknown = append(unknown, k)
			continue
		}
		if (scope == InheritPreflight && !d.preflight) || (scope == InheritNested && !d.nested) {
			continue
		}
		v, _ := effective.Get(k)
		out.Set(k, v)
	}
	return out, unknown
}

// InheritHeader is the comment block that opens a generated file. It names the file's
// PURPOSE, its GENERATOR, and that it was generated at launch — the three things the
// maintainer's ruling requires, and between them the answer to "why is my key not in
// here?" for someone reading the file with no design doc at hand.
//
// launchedAt is a human-readable timestamp ("" omits the line, for a byte-stable golden).
// The header is JSONC comments, so the file parses as the config it is.
func InheritHeader(scope InheritScope, launchedAt string) string {
	var b strings.Builder
	b.WriteString("// GENERATED by yolo at jail launch — do not edit; your edits are\n")
	b.WriteString("// overwritten on the next launch, and this file is mounted read-only.\n")
	b.WriteString("//\n")
	switch scope {
	case InheritPreflight:
		b.WriteString("// PURPOSE: this is the USER-SCOPE config the read-only commands in this jail\n")
		b.WriteString("// consult — `yolo check`, `yolo loopholes list/status`, `yolo pack`,\n")
		b.WriteString("// `yolo config`. It carries ONLY the keys those readers can judge from\n")
		b.WriteString("// inside a container.\n")
		b.WriteString("//\n")
		b.WriteString("// A key you set on the host and do not see here was filtered on purpose: its\n")
		b.WriteString("// referent does not exist in a container (host GPU drivers, /dev paths, cache\n")
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
	b.WriteString("// `yolo config dump` renders. Recursion is by COMPOSITION: a nested launch\n")
	b.WriteString("// filters ITS effective config again, so every level sees one inherited file\n")
	b.WriteString("// and one writable file of its own, at any depth.\n")
	b.WriteString("//\n")
	b.WriteString("// LAUNCH-FROZEN: a host-side config edit lands at the NEXT launch, not live\n")
	b.WriteString("// (`yolo config drift` reports whether it moved). That is the jail's normal\n")
	b.WriteString("// contract — env, image and relay wiring are all frozen at container start.\n")
	if launchedAt != "" {
		b.WriteString("//\n")
		b.WriteString("// GENERATED AT: " + launchedAt + "\n")
	}
	return b.String()
}

// FilterInheritErr is FilterInherit with an error return, for callers that want the
// filtered map itself (e.g. to ask whether it is empty before rendering a file for it).
// The error is always nil today and exists so a future filter that can fail — a value-level
// rewrite rather than a key-level projection — does not change every call site.
func FilterInheritErr(effective *jsonx.OrderedMap, scope InheritScope) (*jsonx.OrderedMap, []string, error) {
	out, unknown := FilterInherit(effective, scope)
	return out, unknown, nil
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
	body, err := SnapshotJSON(filtered)
	if err != nil {
		return "", err
	}
	return InheritHeader(scope, launchedAt) + body + "\n", nil
}

// RenderInherit filters and renders in one step — the whole-file form, for callers with no
// reason to inspect the filtered map.
func RenderInherit(effective *jsonx.OrderedMap, scope InheritScope, launchedAt string) (string, []string, error) {
	filtered, unknown := FilterInherit(effective, scope)
	out, err := RenderFiltered(filtered, scope, launchedAt)
	return out, unknown, err
}

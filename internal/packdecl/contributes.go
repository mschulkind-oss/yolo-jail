package packdecl

// contributes.go is the manifest shape: one `contributes` list of typed
// contributions, each with an explicit `kind` from the closed core-owned set
// (kinds.go). See docs/design/pack-system.md §2-§3.
//
// The accessors below (Contributions and the per-kind projections) are the one
// way core reads a pack's effects; nothing outside this package looks at the
// Contribution fields directly.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Contribution is one typed effect a pack declares. Exactly one kind per entry;
// which fields are meaningful depends on the kind (see the per-kind validation in
// validateContribution). It is deliberately a flat superset struct rather than a
// per-kind union — the same shape the legacy sub-types (Install/Mount/HostFile/
// Hook) already used, so synthesis from them is a field copy, and JSON decoding
// stays a plain struct with DisallowUnknownFields.
type Contribution struct {
	// Kind selects from the closed set (KindProgram, KindSkills, …). Required.
	Kind Kind `json:"kind"`

	// --- program (install) / requires (assertion) ---
	Bin     string   `json:"bin,omitempty"`     // program/requires/launch: the binary name
	Via     string   `json:"via,omitempty"`     // program: "npm" | "installer"
	Package string   `json:"package,omitempty"` // program via npm: the npm package
	URL     string   `json:"url,omitempty"`     // program via installer: the curl-to-shell URL
	Flags   []string `json:"flags,omitempty"`   // program: extra install flags; launch: the injected flags
	// InstallHints maps a host package manager ("brew"|"apt"|"dnf"|"pacman"|"nix") to
	// the package name that provides Bin on that manager (env-manager plan Phase 6). Read
	// from a `program` AND from a `requires` contribution — a pack that only ASSERTS a
	// binary is exactly the case with the most use for a remedy, since yolo will never
	// install it. A pack author knows this better than a built-in attr→pkg table that
	// goes stale; it
	// is what `check`/`apply` below jail use to probe for the binary and, if missing,
	// emit a runnable manifest (a Brewfile and kin). Optional: absent means yolo can
	// name the binary as missing but not the remedy. Declared by the pack that
	// INTRODUCES the dep, never re-declaring one another tool owns.
	//
	// One key names an installer FLAVOR rather than a manager: "brew-cask", for a
	// Homebrew cask (`brew install --cask`, and `cask "<token>"` in a Brewfile) as
	// opposed to a formula. Use it whenever the token is a cask —
	// https://formulae.brew.sh/api/cask/<token>.json exists and .../formula/... 404s —
	// because a Brewfile `brew` line naming a cask token fails, and bare
	// `brew install <token>` silently prefers a same-named formula (brew's `copilot`
	// formula is AWS's deprecated ECS CLI). "brew-cask" wins over "brew" when a pack
	// declares both; internal/depcheck holds the lookup order.
	InstallHints map[string]string `json:"install_hints,omitempty"`

	// --- skills / briefing / files (staged trees) ---
	// From is the pack-relative source path. OPTIONAL for `skills` and `briefing`,
	// which each have a conventional source, and REQUIRED for `files`, which does not
	// (see validateContribution). Read it through SkillsSource() / BriefingCandidates()
	// rather than off the struct: an absent `from` means the convention, and a call
	// site that resolves that by hand is a call site that can quietly stop honoring
	// the declared value (which is exactly what all three skills readers did).
	From string `json:"from,omitempty"` // pack-relative source path
	Into string `json:"into,omitempty"` // home-relative jail destination
	// After, as `"host:<path>"` on a `briefing`, prepends the user's own host file to the
	// jail's composed briefing (run.briefingHostOverlay → jailcontent.PrependHostBriefing) — so a
	// personal AGENTS.md outranks anything a pack ships INSIDE A JAIL.
	//
	// JAIL-ONLY, and after §6a that is a narrower claim than it looks. At the host notch the
	// path it names is now the DESTINATION yolo generates wholesale, so there is no
	// user-maintained file left to prepend: the host render ignores After entirely, and the user's
	// own prose reaches every destination through the local pack instead. It survives because the
	// jail case is still real (a `:ro`-mounted staging copy composed from a host file yolo does
	// NOT OWN), not because it means something at both notches.
	//
	// "DOES NOT OWN" IS NOW CHECKED, not assumed. Once §6a made the host destination yolo's own
	// output, this field named that output on every machine where `yolo host apply` had run: the
	// jail prepended a file already holding every pack's prose and then composed the same packs
	// again, so each pack arrived TWICE (measured 2026-08-31). The run pipeline asks
	// entrypoint.GeneratedHostBriefings before prepending — the briefing half of S3.
	//
	// It is still a HOST-ACCESS CLAIM either way (HostAccessClaims, NeedsHostAccessContributions)
	// — declaring it means reading the host home, which is exactly what a fetched pack needs
	// approval for, whether or not this launch is in a jail.
	After string `json:"after,omitempty"`

	// Tier is a TOMBSTONE for the per-contribution tier S2 removed: it declared a GLOBAL
	// property (what a skill is called) at a PER-DESTINATION site, so it could not express a
	// consistent name — and mergedest inherited it per destination, which is how a pack that
	// declared nothing ended up namespaced in one home and flat in another. It is now
	// Manifest.SkillsTier: one positive choice, per pack, honored everywhere.
	//
	// It stays DECODABLE, and refused by name (validateContribution), rather than being
	// deleted outright. Deleting it would make an existing pack fail on the strict decoder's
	// `json: unknown field "tier"` — loud, but naming neither the replacement nor the reason,
	// which is the one thing a pack author cannot act on. Nothing reads the value.
	Tier string `json:"tier,omitempty"`

	// --- config / config-overlay ---
	Surface string `json:"surface,omitempty"` // config-overlay: the target surface "agent/name"
	// Profile, on a config-overlay contribution, gates the whole contribution on a profile
	// being ACTIVE for the target surface's OWNING agent — the name the active-profile
	// table holds at the target's agent segment, which is a CLI name, the namespace the
	// table keys on (profiles-as-pack-variants.md §7). Absent means UNCONDITIONAL, the
	// pre-field behavior every existing overlay keeps; an explicitly empty string decodes
	// to the same fact, so there is no emptiness to validate — "" and undeclared are one
	// declaration.
	//
	// The gate is a SKIP, not an error: profile values are free-form (parent OQ-3), so a
	// name nothing selected is inert exactly like an unselected owner is. REFUSED ON EVERY
	// OTHER KIND (validateContribution) — the same modifier is contemplated for `env` and
	// `launch` (parent §7.2's closing line), but config-overlay is the one consumer today,
	// and a field silently doing nothing on the kinds it does not gate is the
	// accepted-and-ignored defect this schema refuses everywhere else.
	Profile string `json:"profile,omitempty"`

	// --- state ---
	At    string `json:"at,omitempty"`    // state: the home-relative subtree
	Scope string `json:"scope,omitempty"` // state: "workspace" (default) | "machine"
	Why   string `json:"because,omitempty"`

	// --- reads-host / mount ---
	Host string `json:"host,omitempty"` // reads-host: the host-home-relative file; mount: the host-home-relative dir/file

	// --- env ---
	// Vars is a static map of environment variables the pack sets in the jail. Values
	// are literal strings only — no interpolation, no secrets, no host references — so
	// an env contribution never reads the host and is honored regardless of origin.
	Vars map[string]string `json:"vars,omitempty"`

	// --- launch alias map (kept as the legacy flagAliases shape) ---
	Aliases map[string][]string `json:"aliases,omitempty"`

	// --- hook ---
	Hook string `json:"hook,omitempty"` // hook: the named capability from KnownHooks

	// --- autonomy (§4.2 / env-manager plan Phase 9) ---
	// A pack declares BOTH postures; the confinement notch's AgentAutonomy policy
	// selects which one renders (autonomous at jail/guest, guarded at host). Each
	// posture folds config-managed keys into the pack's OWN surfaces and merges launch
	// flags — it is not a second config writer, it is a notch-gated patch of the
	// managed layer. Either posture may be empty (pi is permissive by default, so its
	// autonomous is empty and only guarded tightens it).
	Autonomous *AutonomyPosture `json:"autonomous,omitempty"`
	Guarded    *AutonomyPosture `json:"guarded,omitempty"`

	// --- provider (profiles-as-pack-variants.md §4.1 as ruled, OQ-12) ---
	// Name is REQUIRED and is the provider's whole identity: the key the entry lands
	// under in the composed `providers` table, what a profile's `requires_provider`
	// names, and what the derives emit as the provider/model id. Sole-owned across packs
	// (kinds.go), so two packs shipping one name is a collision — while one pack shipping
	// two names is two contributions, which is the ordinary multi-provider pack.
	Name string `json:"name,omitempty"`
	// Endpoints carries the SERVICE FACTS by protocol: which URL speaks which wire
	// protocol. An endpoint is {base_url, wire_api}, and the keys are protocol names —
	// "anthropic" (Claude's native wire) and "openai" (the OpenAI-client shape) are the
	// two anything consumes today.
	//
	// The KEY set is deliberately left open, unlike the kinds themselves: a protocol core
	// does not know resolves to nothing (no agent speaks it), which is inert rather than
	// dangerous, and closing it here would make a third protocol the `tier` incident a
	// fifth time — a manifest a newer host staged would refuse an older baked entrypoint's
	// boot, because DecodeTolerant validates per entry and cannot skip a value it cannot
	// see (knownVias records the measurement). The VALUES are not free: each base_url is
	// checked below.
	//
	// Top-level `base_url` is NOT part of this kind, on purpose: that spelling is the
	// single-protocol shorthand the user's `providers` entry has, and a pack that ships
	// one protocol still names which one, so the composed entry has exactly one shape for
	// "where does this protocol point".
	Endpoints map[string]ProviderEndpoint `json:"endpoints,omitempty"`
	// APIKeyEnvName is the NAME of the environment variable holding the credential —
	// never the credential. It is the one key-shaped field on the kind, which is what
	// makes a literal key unrepresentable: the user hydrates the variable through
	// env_sources or the invoking environment, and the pack ships only where to look.
	// The `_name` is the value's type read out loud (parent OQ-6), the same distinction
	// the `providers` config key draws with `api_key_env`.
	APIKeyEnvName string `json:"api_key_env_name,omitempty"`
	// Region is the region a regional provider is reached through — Bedrock's address
	// half, where "where is this service" is a region plus a well-known host rather than
	// a base URL. It is a service fact for the same reason an endpoint is: it says where
	// the provider lives, never who is calling, and the user's own `providers` entry
	// overrides it like any other field.
	Region string `json:"region,omitempty"`
	// Models maps a model ALIAS an agent asks for to the provider's model ID —
	// "default"/"fast" → "glm-4.7". Alias names are open vocabulary: which aliases a
	// provider's consumers read is the consumer's business, not core's.
	Models map[string]string `json:"models,omitempty"`
	// EnvShape is how a profile ACTIVE for an agent delivers THIS provider to it, by
	// protocol: protocol → {ENV_VAR → placeholder}. The placeholders are the closed set
	// ValidateProviderEnvShape enforces — "{endpoint}" (that protocol's base_url),
	// "{key}" (the hydrated value of APIKeyEnvName), "{region}" (Region) and
	// "{model:<alias>}" (an alias of Models); anything else is refused, because anything
	// else would be the credential or a host fact smuggled in through a template
	// (validateContribution) — refused at authoring time, and dropped one VARIABLE at a
	// time across a version boundary, where a placeholder a newer host staged is skew
	// rather than corruption (unknownEnvShapeValueSkip). Declared per protocol so one
	// provider can spell claude's
	// ANTHROPIC_* pair and leave the OpenAI-shaped agents to their config files (parent
	// OQ-14: no agent is special-cased, the provider says how it is delivered).
	EnvShape map[string]map[string]string `json:"env_shape,omitempty"`

	// --- profile (profiles-as-pack-variants.md §3.1) ---
	// The body rides fields declared above: Name is the profile's SELECTOR VALUE (the
	// name the user writes in pack_profiles or -p) and Raw is its `config` patch. What is
	// new here is the part no other kind needed:
	//
	// Launch is the same per-binary shape a posture's is, at the TOP level of a
	// contribution for the first time — `launch` the KIND carries Bin and Flags flat, and
	// this is a LIST of those, so it cannot reuse the kind's own field.
	//
	// Env is deliberately NOT Vars: map[string]string cannot tell a JSON null from an
	// empty string, and for a variant the difference is the whole meaning — null UNSETS
	// the variable (OQ-7, the merge-patch convention `providers` already uses), an empty
	// string sets it empty. EnvValue carries that bit. Its values are literal strings, the
	// same restriction Vars carries — so like `env` and `autonomy` the kind reads nothing
	// from the host and is not origin-gated.
	//
	// RequiresProvider names a provider entry this variant needs (parent §6.2): a
	// reference into the user's `providers` config, resolved at launch — not a credential
	// and not a host read, which is why the kind makes no host-access claim.
	Launch           []AutonomyLaunch    `json:"launch,omitempty"`
	Env              map[string]EnvValue `json:"env,omitempty"`
	RequiresProvider string              `json:"requires_provider,omitempty"`

	// Raw carries kind-specific structured payloads that do not fit a scalar field
	// — today only a `config` contribution's surface definition (the agentcfg
	// surface schema), decoded by internal/agentcfg/manifest, kept as RawMessage
	// so packdecl stays free of an engine dependency (same reason Manifest.Surfaces
	// is RawMessage).
	Raw json.RawMessage `json:"config,omitempty"`
}

// viaDelivery is one value of the closed `via` enum: the name a pack author writes on
// a `program` contribution, and the legacy Install.Kind the jail's installer renders
// it as.
type viaDelivery struct {
	name        string
	installKind string
}

// knownVias is THE authority for the `via` vocabulary, closed in the same sense
// kinds.go's kind set is closed — core has to know how to DELIVER a mechanism before a
// pack may name it, so a value outside this list is not a mechanism yolo can install.
//
// It exists as one list because the set had been spelled independently in three places
// — unknownViaSkip (packdecl.go), InstallContributions and validateContribution — with
// nothing coupling them. Measured: teaching BOTH switches in this file a third value
// left every test green while the tolerant decoder, which the in-jail read goes
// through, still dropped every contribution using it. The author would have seen a
// manifest that validated, and the jail would have installed nothing, with the skip
// note the only evidence and no test able to see the disagreement.
//
// Declaration order is the order the diagnostics list them in (KnownVias), so it is
// the order a pack author reads in an error message; it is not otherwise meaningful.
var knownVias = []viaDelivery{
	{name: "npm", installKind: "npm"},
	{name: "installer", installKind: "native"},
}

// KnownVia reports whether v names a delivery mechanism this build knows. An empty
// string is NOT known — a program that names no mechanism installs nothing, and it is
// a hard problem on both decode paths rather than version skew (unknownViaSkip).
func KnownVia(v string) bool {
	for _, d := range knownVias {
		if d.name == v {
			return true
		}
	}
	return false
}

// KnownVias returns the closed via set in declaration order, for diagnostics and
// tests. Callers join it into their own sentence ("npm or installer").
func KnownVias() []string {
	out := make([]string, len(knownVias))
	for i, d := range knownVias {
		out[i] = d.name
	}
	return out
}

// viaList renders the closed set the way the validator's diagnostics name it
// ("npm or installer"), so the message cannot outlive the vocabulary it quotes.
func viaList() string {
	return strings.Join(KnownVias(), " or ")
}

// installKindFor returns the legacy Install.Kind a via renders as, and "" for a via
// this build does not know (including the empty one). The empty result is what makes
// an unknown mechanism inert downstream rather than mis-installed.
func installKindFor(via string) string {
	for _, d := range knownVias {
		if d.name == via {
			return d.installKind
		}
	}
	return ""
}

// Contributions returns the pack's declared contributions. THE accessor every
// read path uses — the legacy-shaped projections below (InstallContributions,
// HostFileContributions, …) are all derived from it, so a consumer that wants
// "the installs" or "the host files" reads them without touching the manifest
// shape.
func (m *Manifest) Contributions() []Contribution {
	return m.Contributes
}

// The projections below group the flat contributions by kind into the per-effect
// shapes core consumes (Install, HostFile, Mount, ...), so a caller that wants "the
// install" or "the host files" reads them through one accessor rather than
// filtering the contribution list itself.

// InstallContributions returns EVERY program contribution as a legacy Install, in
// declaration order. Empty when the pack declares none.
//
// Plural because a pack declaring two programs means two programs. This used to
// `return` inside the loop and hand back only the FIRST, so a pack wanting
// `shellcheck` + `shfmt` (or `jq` + `yq`) silently got a launcher for one of them —
// while DepRequirements below looped to completion, so the HOST path already reported
// both. The jail was the side dropping data, with no diagnostic.
//
// `program` is CombineExclusive by BIN NAME, not per pack (kinds.go): two packs both
// installing `fzf` is still a collision, one pack installing two different bins never
// was. And since the launchers moved to ~/.yolo/bin/launch — ordered after /bin — N
// launchers carry no more shadowing risk than one.
func (m *Manifest) InstallContributions() []Install {
	var out []Install
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram {
			continue
		}
		in := Install{Bin: c.Bin, Flags: c.Flags}
		// The KIND comes from the closed via table, never from a case in this switch:
		// that is the coupling to KnownVia, and it is what stops this projection from
		// learning a mechanism the decoders would still drop (see knownVias). A via
		// this build does not know renders an empty Kind — inert, not mis-installed.
		in.Kind = installKindFor(c.Via)
		switch c.Via {
		case "npm":
			in.Package = c.Package
		case "installer":
			in.InstallerURL = c.URL
		}
		out = append(out, in)
	}
	return out
}

// DepRequirement is one binary a pack needs on the host, with the per-manager package
// names that provide it (env-manager plan Phase 6). Below the jail notch, where yolo
// bakes no image, this is what a dep check probes for and, if missing, turns into a
// runnable install line.
type DepRequirement struct {
	// Bin is the binary that must be on PATH (e.g. "psql").
	Bin string
	// Hints maps a host package manager to the package providing Bin. May be empty:
	// then yolo can report Bin missing but not a remedy.
	Hints map[string]string
	// SelfInstall is the command the PACK ITSELF declares for this binary, derived from a
	// `program` contribution's via/url/package — `npm install -g <pkg>` or
	// `curl -fsSL <url> | sh`. Empty for a `requires` contribution, which by definition
	// installs nothing, and for a program with no recognized `via`.
	//
	// It exists because routing a tool with a FIRST-PARTY installer through a distro
	// package manager is a staleness trap: measured 2026-08-02, nixpkgs was current for
	// claude-code/codex/pi-coding-agent and 16 releases behind for github-copilot-cli
	// (1.0.61 vs 1.0.77), with nothing in the packaging to say which. The pack already
	// declares the tool's own updater — the mechanism these CLIs are designed around — so
	// that is the remedy to lead with, and no new schema is needed to know it.
	//
	// It is a SUGGESTION the user runs, never something yolo runs. A curl-to-shell is a
	// different trust proposition from `brew install` (yolo already flags an installer URL
	// `⚠ review` in the footprint), and running one is env-manager Phase 4.3's
	// confirm-gated territory.
	SelfInstall string
}

// selfInstallCommand derives the pack's OWN install command for a program contribution, or
// "" when the kind/via carries none. Kept beside the via/url/package field docs because
// that is where a reader looks for what those fields mean.
func selfInstallCommand(c Contribution) string {
	if c.Kind != KindProgram {
		return "" // `requires` installs nothing; that is the whole difference
	}
	switch c.Via {
	case "npm":
		if c.Package == "" {
			return ""
		}
		return "npm install -g " + c.Package
	case "installer":
		if c.URL == "" {
			return ""
		}
		return "curl -fsSL " + c.URL + " | sh"
	}
	return ""
}

// DepRequirements returns every program AND requires contribution as the host-dep
// requirements the dep checker probes. A contribution with no hints still needs its bin,
// but yolo has no remedy to offer, so it is reported as unprobeable-remedy rather than
// omitted — the caller decides. Only contributions with a Bin are returned.
//
// `requires` is here for the reason the kind exists: below the jail notch yolo bakes no
// image, so "this binary must exist" is the same host question as "yolo would install
// this", and answering it through a second probe would let the two disagree. The kinds
// differ in what they do to a JAIL (a program gets a launcher, a requires gets an
// assertion), not in what they ask of a host.
func (m *Manifest) DepRequirements() []DepRequirement {
	var out []DepRequirement
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram && c.Kind != KindRequires {
			continue
		}
		if c.Bin == "" {
			continue
		}
		out = append(out, DepRequirement{
			Bin: c.Bin, Hints: c.InstallHints, SelfInstall: selfInstallCommand(c),
		})
	}
	return out
}

// RequiredBins returns the binaries this pack ASSERTS must exist — the `requires`
// contributions — in declaration order. Distinct from DepRequirements (which folds these
// in with `program` for the host probe) because the JAIL asserts only these: a `program`
// bin that is absent is normal, since its launcher installs it on first use.
func (m *Manifest) RequiredBins() []DepRequirement {
	var out []DepRequirement
	for _, c := range m.Contributions() {
		if c.Kind != KindRequires || c.Bin == "" {
			continue
		}
		out = append(out, DepRequirement{Bin: c.Bin, Hints: c.InstallHints})
	}
	return out
}

// AutonomyPosture is one side of an autonomy contribution (§4.2): the config-managed
// keys and launch flags that express either the autonomous (no-prompts) or guarded
// (prompts-on) posture for the pack's agent. Config patches fold into the managed layer
// of a surface the SAME pack owns (keyed by "agent/name"); launch flags merge into the
// binary's launch flags. It is not a second config writer — it is a notch-gated patch.
type AutonomyPosture struct {
	// Config patches the managed layer of the pack's own surfaces. Reuses the config
	// surface schema (agent/name identify the target surface; managed carries the keys),
	// kept as RawMessage for the same reason config's Raw is — packdecl stays free of the
	// agentcfg engine dependency; the engine decodes it.
	Config json.RawMessage `json:"config,omitempty"`
	// Launch is the flags to inject for a binary in this posture (e.g.
	// ["--dangerously-skip-permissions"] for autonomous, [] for guarded).
	Launch []AutonomyLaunch `json:"launch,omitempty"`
}

// AutonomyLaunch is a per-binary launch-flag set within a posture.
type AutonomyLaunch struct {
	Bin   string   `json:"bin"`
	Flags []string `json:"flags,omitempty"`
}

// AutonomyContribution is a pack's autonomy declaration: the two postures. Returns nil
// when the pack declares none (every pack that never touches permission posture).
type AutonomyContribution struct {
	Autonomous *AutonomyPosture
	Guarded    *AutonomyPosture
}

// AutonomyContributions returns the pack's autonomy declaration, or nil when it declares
// no autonomy contribution. At most one is meaningful (a second is a validation error).
func (m *Manifest) AutonomyContributions() *AutonomyContribution {
	for _, c := range m.Contributions() {
		if c.Kind != KindAutonomy {
			continue
		}
		return &AutonomyContribution{Autonomous: c.Autonomous, Guarded: c.Guarded}
	}
	return nil
}

// PostureFor returns the posture selected by an autonomy policy: the autonomous posture
// when autonomy is true, the guarded posture otherwise. Returns nil when the pack has no
// autonomy contribution or the selected posture is empty.
func (m *Manifest) PostureFor(autonomy bool) *AutonomyPosture {
	ac := m.AutonomyContributions()
	if ac == nil {
		return nil
	}
	if autonomy {
		return ac.Autonomous
	}
	return ac.Guarded
}

// EnvValue is one value in a profile's `env` map, which — alone in the manifest — may be
// an explicit JSON null. Null means UNSET the variable (OQ-7), and it needs a type of its
// own because map[string]string decodes a null to "" with no error, which would read back
// as a deliberate empty value: the one distinction the field exists to carry.
//
// encoding/json calls UnmarshalJSON for a literal null too, so both shapes land here and
// nothing about the null is lost on either decode path (the tolerant path decodes the same
// field with the same method, so skew cannot turn an unset into an empty value).
type EnvValue struct {
	// Set is false only when the JSON value was null. An empty STRING is a real value —
	// "set it to empty" is a different declaration from "remove it" — so Set is the
	// meaning-bearing bit and Value is not consulted when it is false.
	Set bool
	// Value is the literal string to set. No interpolation, no host references: the same
	// restriction Vars carries, so a profile is origin-gated exactly like autonomy is not.
	Value string
}

// UnmarshalJSON accepts a JSON string or a JSON null, and refuses everything else — a
// number or a bool in an env map is an author's typo, and a silent false would set the
// variable to the empty string.
func (v *EnvValue) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = EnvValue{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*v = EnvValue{Set: true, Value: s}
	return nil
}

// Unset reports whether this value REMOVES the variable rather than setting it — the
// meaning of a JSON null in the map (OQ-7).
func (v EnvValue) Unset() bool { return !v.Set }

// ProfileContribution is one named variant of a pack's own declarations: the name it
// answers to, the config patch it folds into the pack's own surfaces, the launch flags it
// merges, the env it sets (or unsets), and the provider it requires. The open-selector
// twin of AutonomyContribution — whose selector is the confinement notch, not a name.
type ProfileContribution struct {
	Name             string
	Config           json.RawMessage
	Launch           []AutonomyLaunch
	Env              map[string]EnvValue
	RequiresProvider string
}

// Profiles returns every profile the pack declares, in declaration order — which is the
// later-wins fold order, so it must not be normalized here.
//
// A SLICE, not a first-match accessor: a pack may ship as many variants as it has
// intentions (`bedrock` and `glm` on one pack is the ordinary case). The name is
// sole-owned WITHIN the pack (validateProfileNames), so the slice never carries two
// entries a ProfileFor lookup would have to choose between.
func (m *Manifest) Profiles() []ProfileContribution {
	var out []ProfileContribution
	for _, c := range m.Contributions() {
		if c.Kind != KindProfile {
			continue
		}
		out = append(out, ProfileContribution{
			Name:             c.Name,
			Config:           c.Raw,
			Launch:           c.Launch,
			Env:              c.Env,
			RequiresProvider: c.RequiresProvider,
		})
	}
	return out
}

// ProfileFor returns the pack's profile declaration named by `name`, or nil when the pack
// declares none — the shape the fold and the launch disclosure both key off ("did any
// selected pack DECLARE this variant?").
//
// The selector is the name the user chose (`pack_profiles`, `-p`), which is what makes
// this the open-selector twin of PostureFor(autonomy bool): same body, different
// authority for the choice (§3.1).
func (m *Manifest) ProfileFor(name string) *ProfileContribution {
	for _, c := range m.Contributions() {
		if c.Kind != KindProfile || c.Name != name {
			continue
		}
		return &ProfileContribution{
			Name:             c.Name,
			Config:           c.Raw,
			Launch:           c.Launch,
			Env:              c.Env,
			RequiresProvider: c.RequiresProvider,
		}
	}
	return nil
}

// ProviderEndpoint is one protocol's half of a provider: the URL that speaks it and the
// wire protocol that URL speaks. Both are SERVICE facts — the same for every user of the
// provider — which is why a pack may declare them and why neither can carry a credential
// (validateContribution refuses userinfo in a base_url for exactly that reason).
type ProviderEndpoint struct {
	// BaseURL is the endpoint's root URL. Must be http/https and carry no userinfo —
	// `https://user:tok@host/v1` is a credential in a file a pack ships to strangers.
	BaseURL string `json:"base_url,omitempty"`
	// WireAPI is the wire protocol that URL speaks, named in yolo's CANONICAL protocol
	// vocabulary — one of the closed set KnownWireAPIs returns, the same enum the
	// `providers` config key composes into: the enum that tightens one tightens both
	// (internal/config's validateWireAPI asks packdecl for the set), or the two validators
	// would disagree about what a provider is. It is a protocol name, NOT a wire value:
	// nothing consumes it verbatim. Each agent's derive translates it into that agent's
	// own spelling and emits no entry at all for a protocol that agent cannot speak
	// (provider-table-fidelity.md §3.4 / OQ-PT1).
	WireAPI string `json:"wire_api,omitempty"`
}

// ProviderContribution is one provider a pack ships: a name and the service facts that
// go with it. The credential is deliberately absent — the only key-shaped field is the
// NAME of the variable the user hydrates.
type ProviderContribution struct {
	Name          string
	Endpoints     map[string]ProviderEndpoint
	APIKeyEnvName string
	Region        string
	Models        map[string]string
	EnvShape      map[string]map[string]string
}

// Providers returns every provider the pack ships, in declaration order.
//
// A SLICE, not a first-match accessor like AutonomyContributions: the exclusivity this
// kind is sole-owned under is per NAME, so a pack declaring two DIFFERENT providers is
// the ordinary multi-provider pack and nothing here may fold them into one. The same
// name twice is the collision, and that is validateContributions' to refuse — it can see
// the siblings, which an accessor cannot.
func (m *Manifest) Providers() []ProviderContribution {
	var out []ProviderContribution
	for _, c := range m.Contributions() {
		if c.Kind != KindProvider {
			continue
		}
		out = append(out, ProviderContribution{
			Name:          c.Name,
			Endpoints:     c.Endpoints,
			APIKeyEnvName: c.APIKeyEnvName,
			Region:        c.Region,
			Models:        c.Models,
			EnvShape:      c.EnvShape,
		})
	}
	return out
}

// DefaultSkillsDir is the conventional pack-relative directory a `skills`
// contribution reads when it declares no `from` — and what a zero-ceremony pack (a
// bare skills/ dir, no manifest at all) uses. Every pack yolo ships relies on it.
const DefaultSkillsDir = "skills"

// SkillsSource is the pack-relative source directory THIS skills contribution reads:
// its declared `from`, or DefaultSkillsDir when absent.
//
// It exists because `from` used to be accepted and silently ignored on `skills` — all
// three readers (two on the jail path, one at the host) hardcoded "skills" — so a pack
// declaring `{"kind":"skills","from":"my-skills"}` got skills/ read instead, with no
// warning. One resolver, called by every reader, is what keeps the three from drifting
// again; the shape mirrors hostBriefingProse's `from`-first-then-convention precedence
// for `briefing`.
//
// Kind is NOT checked here: it is a method on the contribution the caller has already
// filtered by kind, and returning "skills" for a `files` contribution would be a worse
// answer than trusting the caller. Callers that hold a whole manifest use
// SkillsSources.
func (c Contribution) SkillsSource() string {
	if c.From != "" {
		return c.From
	}
	return DefaultSkillsDir
}

// SkillsSources returns the resolved pack-relative source dir of every `skills`
// contribution, in declaration order, deduplicated.
//
// Deduplicated because two contributions naming one source (a pack delivering the same
// skills to two agents' dirs — the ordinary multi-agent case) is ONE tree to read: the
// jail path stages the union of these into a per-pack dir, so a repeat would copy the
// same content twice for no effect.
//
// EMPTY for a pack with no `skills` contribution, and that is load-bearing rather than
// incidental: the jail's zero-ceremony merge reads DefaultSkillsDir for such a pack, so
// the caller supplies that fallback (see run.packSkillSourceDirs) instead of this
// returning a source the manifest never claimed.
func (m *Manifest) SkillsSources() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range m.Contributions() {
		if c.Kind != KindSkills {
			continue
		}
		src := c.SkillsSource()
		if seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	return out
}

// DefaultBriefingFiles are the conventional pack-relative files a `briefing` contribution
// reads when it declares no `from`, in precedence order.
//
// THERE IS ONE, and it is AGENTS.md. This returned ["AGENTS.md", "CLAUDE.md"] until
// 2026-08-17, defended on the grounds that "both names are in the wild and a pack author
// should not have to know which one yolo happens to read". That argument is about the
// world, not about this repo, and it still lost (pack-code-separation.md §3.3): AGENTS.md
// is the CROSS-TOOL convention and CLAUDE.md is one particular tool's own, so a core schema
// package reading the second for free is core knowing about that tool — the last such
// mention outside the migration debt. yolo picks one convention and it picks the shared one.
//
// The cost is bounded and was measured before the deletion, not after: no pack in the tree
// or on the maintainer's host relied on the fallback. The six shipped packs carry no
// briefing prose file at all (their `briefing` contribution exists to name a DESTINATION,
// which is unaffected — `.claude/CLAUDE.md` as an `into` is a path, not a source), and every
// local pack names AGENTS.md in an explicit `from`. So the pair never had a second
// inhabitant; what changed is only what a FUTURE pack gets without asking.
//
// A pack whose prose lives elsewhere — CLAUDE.md included — writes `from` explicitly, which
// is what `from` is for, and gets a REPORT if that file turns out to be missing
// (packload.missingBriefingFromProblem) rather than the silence a conventional name buys.
// The return stays a SLICE rather than a single string because BriefingCandidates prepends
// `from` to it, and because a second convention is a data change if one ever earns its way in.
func DefaultBriefingFiles() []string { return []string{"AGENTS.md"} }

// BriefingCandidates returns the pack-relative files THIS briefing contribution's prose
// may live in, in precedence order: the declared `from` first, then the convention.
// The caller reads the first one that exists and is non-empty.
//
// Same shape and the same reason as SkillsSource: `from` is optional on this kind because
// every reader already falls back to the convention (entrypoint's hostBriefingProse builds
// exactly this list), so requiring it in the schema only made the author write a literal
// the resolver would have supplied.
//
// It is the AUTHORITY for that precedence, and since 2026-08-04 the ONLY copy of it: both
// readers go through packload.BriefingProseFor, which calls this. Before that they each inlined
// the pair — hostBriefingProse `from`-first-then-convention, run.readPackBriefing
// convention-only, ignoring `from` — and a pack whose prose lived elsewhere briefed at the host
// notch and not in a jail (roadmap.md §6a-4). SkillsSource is the precedent this
// followed.
//
// A FALLBACK CHAIN is the contract, not a single choice, and that is the one place `briefing`
// differs from `skills`: a declared `from` that is absent resolves to the convention rather than
// refusing, because the host notch always did that and narrowing it would break packs.
// BriefingProseFor makes the fallback loud instead of silent.
//
// Kind is NOT checked, for the reason SkillsSource states: this is a method on a
// contribution the caller has already filtered by kind.
func (c Contribution) BriefingCandidates() []string {
	if c.From == "" {
		return DefaultBriefingFiles()
	}
	return append([]string{c.From}, DefaultBriefingFiles()...)
}

// LoopholeSources returns the pack-relative module directory of every `loophole`
// contribution, in declaration order, deduplicated.
//
// THE accessor for the kind, and deliberately the ONLY thing this package offers for it:
// what a loophole DOES lives in <from>/manifest.jsonc, which is outside pack.json and
// therefore outside this package's reach (it has no pack root and no internal imports —
// kinds.go). Everything else — the footprint claims, the approval strings, discovery —
// goes through packload, which has a Root and may import internal/loopholedecl.
//
// Deduplicated because two contributions naming one module are one loophole; the exclusivity
// rule is per NAME, and the same `from` twice is a repeat of one declaration rather than a
// self-collision. (Two DIFFERENT `from` paths with the same basename IS a self-collision, and
// it is not detectable here — a basename is not the loophole name until the manifest's own
// `name` has been read and agreed with it. That check belongs to the launch pre-flight.)
func (m *Manifest) LoopholeSources() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range m.Contributions() {
		if c.Kind != KindLoophole || c.From == "" {
			continue
		}
		if seen[c.From] {
			continue
		}
		seen[c.From] = true
		out = append(out, c.From)
	}
	return out
}

// HostFileContributions returns the reads-host contributions as legacy HostFiles.
func (m *Manifest) HostFileContributions() []HostFile {
	var out []HostFile
	for _, c := range m.Contributions() {
		if c.Kind == KindReadsHost {
			out = append(out, HostFile{From: c.Host, To: c.Into})
		}
	}
	return out
}

// HostMountContributions returns the mount contributions as {From (host-home
// source), To (/ctx destination)} pairs. Unlike reads-host the source may be a
// directory and the destination is an arbitrary /ctx path. Origin-gated: the
// caller honors these only for a host-permitted pack (see NeedsHostAccess).
func (m *Manifest) HostMountContributions() []HostFile {
	var out []HostFile
	for _, c := range m.Contributions() {
		if c.Kind == KindMount {
			out = append(out, HostFile{From: c.Host, To: c.Into})
		}
	}
	return out
}

// EnvContributions merges every env contribution's vars into one map, later
// contributions winning a key. Static values only — no interpolation, no host
// reads — so this is never origin-gated. Returns nil when no pack sets env.
func (m *Manifest) EnvContributions() map[string]string {
	var out map[string]string
	for _, c := range m.Contributions() {
		if c.Kind != KindEnv {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		for k, v := range c.Vars {
			out[k] = v
		}
	}
	return out
}

// MountContributions returns the skills/briefing/files contributions as legacy
// Mounts, reconstructing the from/to/hostOverlay a mount had. The magic-string
// `from` is reproduced so the existing mount consumers (skills vs briefing vs
// opaque) keep working until they too key off kind.
func (m *Manifest) MountContributions() []Mount {
	var out []Mount
	for _, c := range m.Contributions() {
		switch c.Kind {
		case KindSkills, KindFiles:
			out = append(out, Mount{From: c.From, To: c.Into})
		case KindBriefing:
			mt := Mount{From: c.From, To: c.Into}
			if strings.HasPrefix(c.After, "host:") {
				mt.HostOverlay = strings.TrimPrefix(c.After, "host:")
			}
			out = append(out, mt)
		}
	}
	return out
}

// ConfigOverlay is one config-overlay contribution: the identity of the surface it
// targets and the raw body it contributes to it.
//
// It is deliberately NOT foldable into SurfaceContributions' one concatenated array.
// A config contribution DECLARES a surface, so several of them are just a longer list;
// an overlay NAMES someone else's surface, so its target travels with its body and the
// two are only meaningful together.
type ConfigOverlay struct {
	// Surface is the target surface identity, "agent/name".
	Surface string
	// Profile is the optional gate: when non-empty, the contribution applies only while
	// that name is the active profile for the surface's owning agent (the identity's agent
	// segment). Empty is unconditional — the behavior before the field existed.
	Profile string
	// Config is the raw `config` body — the keys this pack asserts onto the target.
	// Decoded by internal/agentcfg/manifest (DecodeOverlay), kept as RawMessage for the
	// same reason Contribution.Raw is: packdecl stays free of the engine dependency.
	Config json.RawMessage
}

// ConfigOverlayContributions returns every config-overlay the pack declares, in
// declaration order — which is the FOLD order the engine applies (later wins), so the
// order must not be normalized here. Every overlay is returned regardless of its profile
// gate: the gate is a LAUNCH fact (which profile is active), not a declaration fact, and
// the readers that owe a per-launch answer — the collector that gates, the footprint that
// claims — both decide it from their own inputs rather than from a filtered projection.
func (m *Manifest) ConfigOverlayContributions() []ConfigOverlay {
	var out []ConfigOverlay
	for _, c := range m.Contributions() {
		if c.Kind == KindConfigOverlay {
			out = append(out, ConfigOverlay{Surface: c.Surface, Profile: c.Profile, Config: c.Raw})
		}
	}
	return out
}

// SurfaceContributions returns the raw JSON of every config contribution's
// surface definition, concatenated into one array — the shape Manifest.Surfaces
// held. Empty when the pack declares no config surface.
func (m *Manifest) SurfaceContributions() json.RawMessage {
	var raws []json.RawMessage
	for _, c := range m.Contributions() {
		if c.Kind == KindConfig && len(c.Raw) > 0 {
			raws = append(raws, c.Raw)
		}
	}
	return mergeSurfaceArrays(raws)
}

// WritableDirContributions / SharedDirContributions return the state contributions
// at each scope as legacy home-relative dir lists.
func (m *Manifest) WritableDirContributions() []string { return m.stateDirs("workspace") }
func (m *Manifest) SharedDirContributions() []string   { return m.stateDirs("machine") }

func (m *Manifest) stateDirs(scope string) []string {
	var out []string
	for _, c := range m.Contributions() {
		if c.Kind != KindState {
			continue
		}
		s := c.Scope
		if s == "" {
			s = "workspace"
		}
		if s == scope {
			out = append(out, c.At)
		}
	}
	return out
}

// LaunchFlagContributions / FlagAliasContributions return the launch contributions
// as the legacy per-bin maps.
func (m *Manifest) LaunchFlagContributions() map[string][]string {
	out := map[string][]string{}
	for _, c := range m.Contributions() {
		if c.Kind == KindLaunch && c.Bin != "" {
			out[c.Bin] = c.Flags
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m *Manifest) FlagAliasContributions() map[string][]string {
	for _, c := range m.Contributions() {
		if c.Kind == KindLaunch && len(c.Aliases) > 0 {
			return c.Aliases
		}
	}
	return nil
}

// HookContributions returns the hook contributions as legacy Hooks.
func (m *Manifest) HookContributions() []Hook {
	var out []Hook
	for _, c := range m.Contributions() {
		if c.Kind == KindHook {
			out = append(out, Hook{Name: c.Hook, File: c.From, SharedDir: c.At})
		}
	}
	return out
}

// NeedsHostAccessContributions is the origin-gate predicate over contributions: a
// reads-host, a program installed via a curl-to-shell URL, or a briefing that
// prepends a host file. Same set NeedsHostAccess covered, expressed over kinds.
func (m *Manifest) NeedsHostAccessContributions() []string {
	var reasons []string
	for _, c := range m.Contributions() {
		switch {
		case c.Kind == KindReadsHost:
			reasons = append(reasons, "reads-host (reads the host home)")
		case c.Kind == KindMount:
			reasons = append(reasons, "mount (reads a host-home dir/file)")
		case c.Kind == KindProgram && c.Via == "installer":
			reasons = append(reasons, "program via installer (runs a fetched script)")
		case c.Kind == KindBriefing && strings.HasPrefix(c.After, "host:"):
			reasons = append(reasons, "briefing after a host file (reads the host home)")
		}
	}
	return reasons
}

// HostAccessClaims returns the SPECIFIC, stable host-access claims a pack makes —
// one line per claim, naming the exact target — sorted for deterministic comparison.
// This is the set a user approves at `pack install`; a pin move whose claims are a
// superset of the approved set re-prompts, so the strings must be specific (which
// dir, which file) rather than the generic reasons NeedsHostAccessContributions
// gives for display. Empty when the pack reads nothing from the host.
func (m *Manifest) HostAccessClaims() []string {
	var claims []string
	for _, c := range m.Contributions() {
		switch {
		case c.Kind == KindReadsHost:
			claims = append(claims, "reads-host "+c.Host)
		case c.Kind == KindMount:
			claims = append(claims, "mount "+c.Host+" -> /ctx/"+c.Into)
		case c.Kind == KindProgram && c.Via == "installer":
			claims = append(claims, "installer "+c.URL)
		case c.Kind == KindBriefing && strings.HasPrefix(c.After, "host:"):
			claims = append(claims, "briefing "+strings.TrimPrefix(c.After, "host:"))
		}
	}
	sort.Strings(claims)
	return claims
}

// mergeSurfaceArrays concatenates several JSON arrays of surface DTOs into one
// array. Each Raw is expected to be a JSON array (the shape Manifest.Surfaces and
// a config contribution's `config` body both use); a single-object body is
// wrapped. Returns nil for no input.
func mergeSurfaceArrays(raws []json.RawMessage) json.RawMessage {
	if len(raws) == 0 {
		return nil
	}
	var all []json.RawMessage
	for _, r := range raws {
		var arr []json.RawMessage
		if err := json.Unmarshal(r, &arr); err == nil {
			all = append(all, arr...)
			continue
		}
		// Not an array — treat the whole thing as one surface object.
		all = append(all, r)
	}
	out, err := json.Marshal(all)
	if err != nil {
		return nil
	}
	return out
}

// validateContributions reports every structural problem in a `contributes` list:
// an unknown kind, or a required field missing for the kind. Kept per-kind and
// loud, matching the legacy Validate.
func (m *Manifest) validateContributions() []string {
	var problems []string
	for i, c := range m.Contributes {
		problems = append(problems, validateContributionAt(i, c)...)
	}
	problems = append(problems, m.validateProviderNames()...)
	problems = append(problems, m.validateProfileNames()...)
	return problems
}

// validateProfileNames refuses a profile NAME declared twice by ONE pack (§3.4).
//
// Within a pack the name is sole-owned: it is the selector value, and ProfileFor returns
// the FIRST match, so a second declaration with the same name would silently replace the
// first in every fold while the footprint showed two healthy variants. Cross-pack the
// same name is NOT owned — profile `bedrock` in two packs is two unrelated declarations
// that happen to share a selector value — so nothing here looks beyond one manifest, and
// packload.Collisions never sees a profile collision either (the claim target carries the
// pack name for exactly that reason).
//
// Strict path only, like validateProviderNames: DecodeTolerant validates entries one at a
// time and cannot see siblings.
func (m *Manifest) validateProfileNames() []string {
	var problems []string
	seen := map[string]int{}
	for i, c := range m.Contributes {
		if c.Kind != KindProfile || c.Name == "" {
			continue
		}
		if first, dup := seen[c.Name]; dup {
			problems = append(problems, fmt.Sprintf(
				"contributes[%d]: profile %q is declared again (first at contributes[%d]) — a "+
					"profile name is sole-owned within a pack: it is the selector value, and "+
					"the second declaration would silently replace the first", i, c.Name, first))
			continue
		}
		seen[c.Name] = i
	}
	return problems
}

// validateProviderNames refuses a provider NAME declared twice by ONE pack.
//
// Cross-pack, the name is sole-owned and packload.Collisions' exclusive loop is the
// check — the claim target is the bare name, so two packs shipping `zai` group on it.
// Within a pack that loop is silent by design (`len(packSet) < 2`), and the failure it
// would leave behind is the silent kind: the composed providers table is keyed by name,
// so the second declaration would REPLACE the first while every footprint and lint run
// showed two healthy claims. So this is the authoring-time half, strict path only — the
// same place `retiredFieldProblems` draws the authoring/jail line, and for the same
// reason: DecodeTolerant validates entries one at a time and cannot see siblings.
func (m *Manifest) validateProviderNames() []string {
	var problems []string
	seen := map[string]int{}
	for i, c := range m.Contributes {
		if c.Kind != KindProvider || c.Name == "" {
			continue
		}
		if first, dup := seen[c.Name]; dup {
			problems = append(problems, fmt.Sprintf(
				"contributes[%d]: provider %q is declared again (first at contributes[%d]) — a "+
					"provider name is sole-owned, and the composed providers table is keyed by "+
					"name, so the second declaration would silently replace the first", i, c.Name, first))
			continue
		}
		seen[c.Name] = i
	}
	return problems
}

// validateContributionAt reports the structural problems of ONE contributes entry,
// labeled with its index. Shared by the strict path (validateContributions, which
// validates every entry) and the tolerant path (DecodeTolerant, which validates only
// the entries it keeps but labels them by their ORIGINAL index — the position the
// author sees in pack.json — so a skipped unknown kind never shifts a sibling's label).
func validateContributionAt(i int, c Contribution) []string {
	label := fmt.Sprintf("contributes[%d]", i)
	if c.Kind == "" {
		return []string{label + ": missing \"kind\""}
	}
	if msg := ValidateKind(c.Kind); msg != "" {
		return []string{label + ": " + msg}
	}
	return validateContribution(label, c)
}

// ValidBinName reports whether name is a BARE PROGRAM NAME — a single PATH segment, the
// only shape a `bin` field may have.
//
// It is the traversal guard for the one manifest field that names a FILE YOLO WRITES
// rather than a path yolo reads: a host launch wrapper (hostwrap.Generate), a jail lazy
// launcher (GenerateAgentLaunchers), and a blocked-tool shim (GenerateShims) are all filed
// at filepath.Join(dir, name), so a bin carrying path structure writes an executable
// OUTSIDE the generated directory — and the canonical target of such a write is a
// bashrc. The schema refuses it here; the writers re-ask this predicate as
// defense-in-depth for callers that bypass the loaders.
//
// Colons are refused for the PATH's sake: a name containing the list separator could
// never be resolved by a PATH lookup, so a manifest declaring one has misnamed something.
// "." and ".." are refused as filenames in their own right.
func ValidBinName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/:")
}

// binProblem appends the bare-program-name refusal for a bin field. Split from
// validateContribution's kind switch because FOUR fields route through it (program,
// requires, launch, autonomy launch) and their messages must not drift.
func binProblem(problems []string, field, bin string) []string {
	if bin == "" || ValidBinName(bin) {
		return problems // empty is the required-field check's own message
	}
	return append(problems, field+": must be a bare program name — no \"/\", \"..\", "+
		"\":\" or absolute path ("+bin+")")
}

// validateContribution checks one entry's required fields for its kind, and
// runs the path guards on every path-bearing field.
func validateContribution(label string, c Contribution) []string {
	var problems []string
	req := func(field, val string) {
		if val == "" {
			problems = append(problems, fmt.Sprintf("%s: kind %q needs %q", label, c.Kind, field))
		}
	}
	// `profile` gates ONE kind, and the refusal is this schema's standing answer to a
	// field that would otherwise be accepted and ignored: the modifier is contemplated for
	// `env` and `launch` too (profiles-as-pack-variants.md §7.2's closing line), but until
	// one of those ships its consumer, a `profile` on any other kind is a declaration that
	// silently does nothing — the exact defect `requires does not take "via"` refuses
	// below, for the same reason. Ahead of the kind switch so a kind added tomorrow
	// inherits the refusal instead of learning about it the hard way.
	if c.Profile != "" && c.Kind != KindConfigOverlay {
		problems = append(problems, fmt.Sprintf(
			"%s: kind %q does not take \"profile\" — it is the config-overlay modifier, "+
				"gating a cross-pack contribution on a profile being active for the target "+
				"surface's agent", label, c.Kind))
	}
	switch c.Kind {
	case KindProgram:
		req("bin", c.Bin)
		problems = binProblem(problems, label+".bin", c.Bin)
		// `via` is a CLOSED enum, and the strict refusal below is only half the rule: the
		// tolerant path skips an unknown value instead of refusing it, so a third delivery
		// mechanism cannot brick a jail on a pre-`just load` image (packdecl.unknownViaSkip,
		// program-delivery.md §6.2 / R6). An EMPTY via stays a hard problem on both paths.
		//
		// The sibling closed enums with the SAME future-skew shape — `state`'s scope, the
		// hook names (KnownHooks), and the manifest-level `skills_tier` — are deliberately
		// NOT widened here: each is skew-sensitive in exactly this way, and whoever adds a
		// value to one extends its tolerance first, in its own change.
		//
		// MEMBERSHIP IS KnownVia's, not this switch's, so the strict refusal and the
		// tolerant skip cannot come to disagree about which values exist (knownVias).
		// What stays here is the per-value REQUIRED FIELD, which is this function's own
		// business — the switch below adds no member and can subtract none.
		switch {
		case c.Via == "":
			problems = append(problems, label+": program needs \"via\" ("+viaList()+")")
		case !KnownVia(c.Via):
			problems = append(problems, fmt.Sprintf("%s: unknown via %q (%s)", label, c.Via, viaList()))
		case c.Via == "npm":
			req("package", c.Package)
		case c.Via == "installer":
			req("url", c.URL)
		}
	case KindRequires:
		req("bin", c.Bin)
		problems = binProblem(problems, label+".bin", c.Bin)
		// `via`/`package`/`url` belong to program, and a `requires` carrying one is the
		// author confusing the two kinds — which is worth saying, because the mistake is
		// silent otherwise (the fields are simply never read, and the tool never installs).
		for _, f := range []struct{ name, val string }{
			{"via", c.Via}, {"package", c.Package}, {"url", c.URL},
		} {
			if f.val != "" {
				problems = append(problems, fmt.Sprintf(
					"%s: requires does not take %q — it ASSERTS a binary is present and "+
						"installs nothing; use kind \"program\" to have yolo install it",
					label, f.name))
			}
		}
	case KindSkills, KindBriefing, KindFiles:
		// `from` is required on `files` ONLY, and the split is the whole point rather than
		// an inconsistency. `skills` and `briefing` each have a CONVENTIONAL source that
		// every reader already falls back to — DefaultSkillsDir for skills (see
		// SkillsSource), AGENTS.md for briefing (entrypoint's
		// hostBriefingProse, run.readPackBriefing) — so demanding the field made every pack
		// author write a literal the resolver would have supplied anyway, and the validator
		// was the only half of the code that thought it mattered. `files` is
		// CombineExclusive over an ARBITRARY path with no conventional location, so there is
		// nothing to default to: the declaration is the only thing that can name the tree.
		//
		// `into` stays required on all three, and that is not symmetry-for-its-own-sake
		// either. A source has one right answer per KIND; a destination has one right answer
		// per AGENT, so inferring it means inferring the agent set — which is what the
		// `packs` list is for. The jail already infers a skills destination where the host
		// does not, and that asymmetry is a silent no-op bug, not a convention to spread.
		if c.Kind == KindFiles {
			req("from", c.From)
		}
		req("into", c.Into)
		problems = appendPathProblems(problems, label+".from", c.From)
		problems = appendPathProblems(problems, label+".into", c.Into)
		// A DESTINATION IN THE JAIL HOME MAY NOT BE ON PATH. `files` is the kind this
		// matters for — an arbitrary tree at an arbitrary path — but skills and briefing
		// share the guard rather than being exempted by omission: nothing about a skills
		// dir at ~/.local/bin is more sensible, and a guard the reader has to check three
		// case arms to find the shape of is how the fourth kind gets added without it.
		problems = appendJailPathProblems(problems, label+".into", c.Into)
	case KindConfig:
		if len(c.Raw) == 0 {
			problems = append(problems, label+": config needs a \"config\" surface definition")
		}
	case KindConfigOverlay:
		req("surface", c.Surface)
		if len(c.Raw) == 0 {
			problems = append(problems, label+": config-overlay needs a \"config\" body")
		}
	case KindState:
		req("at", c.At)
		problems = appendPathProblems(problems, label+".at", c.At)
		// State is the STRONGER case for the PATH guard, not a weaker one: `files` mounts
		// read-only, but a state subtree is WRITABLE, so a state dir on PATH lets whatever
		// runs in the jail put its own executable there later. The destination is refused
		// for the same reason either way — kind "program" is how a name reaches PATH.
		problems = appendJailPathProblems(problems, label+".at", c.At)
		if c.Scope != "" && c.Scope != "workspace" && c.Scope != "machine" {
			problems = append(problems, fmt.Sprintf("%s: unknown scope %q (workspace or machine)", label, c.Scope))
		}
		if c.Scope == "machine" && c.Why == "" {
			problems = append(problems, label+": machine-scope state needs a \"because\" (it leaks across workspaces)")
		}
	case KindReadsHost:
		req("host", c.Host)
		problems = appendPathProblems(problems, label+".host", c.Host)
	case KindMount:
		req("host", c.Host)
		req("into", c.Into)
		problems = appendPathProblems(problems, label+".host", c.Host)
		problems = appendPathProblems(problems, label+".into", c.Into)
	case KindEnv:
		if len(c.Vars) == 0 {
			problems = append(problems, label+": env needs a non-empty \"vars\" map")
		}
		for k := range c.Vars {
			if k == "" {
				problems = append(problems, label+": env has an empty variable name")
			}
		}
	case KindLaunch:
		req("bin", c.Bin)
		problems = binProblem(problems, label+".bin", c.Bin)
	case KindHook:
		if c.Hook == "" {
			problems = append(problems, label+": hook needs a \"hook\" name")
		} else if !knownHook(c.Hook) {
			problems = append(problems, fmt.Sprintf("%s: unknown hook %q", label, c.Hook))
		}
	case KindAutonomy:
		if c.Autonomous == nil && c.Guarded == nil {
			problems = append(problems, label+": autonomy needs at least one of \"autonomous\" or \"guarded\"")
		}
		problems = append(problems, validateAutonomyPosture(label+".autonomous", c.Autonomous)...)
		problems = append(problems, validateAutonomyPosture(label+".guarded", c.Guarded)...)
	case KindProvider:
		req("name", c.Name)
		problems = append(problems, validateProviderEndpoints(label, c.Endpoints)...)
		problems = append(problems, ValidateProviderEnvShape(label, c.EnvShape)...)
	case KindProfile:
		// `name` is the whole selector, so it is the one required field: a variant that
		// answers to nothing is unreachable, and nothing else about the body would tell
		// the author that. Everything else is optional — a named variant carrying only an
		// env map, or only launch flags, is a perfectly good one.
		req("name", c.Name)
		// The launch entries are the posture's shape, so they go through the same bin
		// guard the four other users of it do — the message cannot be allowed to drift
		// from theirs.
		for i, l := range c.Launch {
			if l.Bin == "" {
				problems = append(problems, fmt.Sprintf("%s.launch[%d]: needs a \"bin\"", label, i))
			}
			problems = binProblem(problems, fmt.Sprintf("%s.launch[%d].bin", label, i), l.Bin)
		}
		for _, k := range sortedKeys(c.Env) {
			if k == "" {
				problems = append(problems, label+": profile has an empty variable name")
			}
		}
	case KindLoophole:
		// `from` is REQUIRED, unlike skills/briefing and like files: a loophole module has
		// no conventional location to fall back to, and the whole contribution is the
		// pointer. It is also the only thing that can name the loophole, since the module
		// dir's basename IS the loophole's name (loadManifest enforces the agreement), so a
		// missing `from` is not a defaultable omission — it is a claim with no target.
		req("from", c.From)
		problems = appendPathProblems(problems, label+".from", c.From)
		// `into` is deliberately NOT required and NOT accepted-and-ignored: a loophole
		// module is not delivered to a home-relative destination at all. Its host half is
		// spawned by the run pipeline and its jail half is bind-mounted at a path core
		// owns (/etc/yolo-jail/loopholes/<name>), so there is no destination for a pack to
		// name. A declared one would be a field that silently does nothing, which is the
		// defect `skills`' ignored `from` already cost this repo once.
		if c.Into != "" {
			problems = append(problems, label+": loophole does not take \"into\" — a loophole "+
				"module is not staged to a home-relative path; its host half runs on the host and "+
				"its jail half is mounted at /etc/yolo-jail/loopholes/<name>, which core owns")
		}
	}
	return problems
}

// validateAutonomyPosture checks one posture's shape: each launch entry needs a bin.
// The config patch's surface schema is validated by the engine at decode time (packdecl
// stays free of the agentcfg dependency), so here we only enforce the structural
// invariants packdecl owns.
func validateAutonomyPosture(label string, p *AutonomyPosture) []string {
	if p == nil {
		return nil
	}
	var problems []string
	for i, l := range p.Launch {
		if l.Bin == "" {
			problems = append(problems, fmt.Sprintf("%s.launch[%d]: needs a \"bin\"", label, i))
		}
		problems = binProblem(problems, fmt.Sprintf("%s.launch[%d].bin", label, i), l.Bin)
	}
	return problems
}

// validateProviderEndpoints checks each endpoint's address and protocol: the base_url
// must be an http/https URL carrying NO userinfo, and the wire_api — when the endpoint
// declares one — must be in the closed set KnownWireAPIs returns.
//
// The userinfo half is the credential rule, and it is a refusal rather than a warning
// because a manifest is the most shareable artifact yolo has: `https://user:tok@host/v1`
// puts a working credential in front of everyone who installs the pack, and a URL check
// that only asked "does it parse" would wave it through. The scheme half is the same rule
// pointed the other way — a `file://` or bare-host URL is a fact about the local machine
// a stranger's manifest cannot know.
//
// The wire_api half is closed because the value is TRANSLATED, not passed through: it
// composes into the providers table, the table crosses to the jail as YOLO_PROVIDERS with
// no re-validation, and each derive maps it onto that agent's own spelling — emitting no
// entry at all for a protocol that agent cannot speak (provider-table-fidelity.md §3.4).
// A name outside the set is therefore one NO derive can translate: it would reach every
// consumer as no protocol, silently, from a jail that booted green. The enum is the same
// one internal/config enforces for user-written providers (validateWireAPI asks
// KnownWireAPIs), because the two spellings of a provider are the same entry in the same
// table.
//
// THE SET IS SKEW-SENSITIVE, and this refusal is only the AUTHORING half of the rule (the
// same split ValidateProviderEnvShape documents): a pack staged by a newer host may name a
// wire protocol this build has never heard of, and refusing it on the tolerant path would
// be the `tier` incident again. DecodeTolerant drops the unknown VALUE and reports it
// (unknownWireAPISkip); an endpoint that declares NO wire_api is simply unremarkable here,
// so the skip's output passes this check clean.
func validateProviderEndpoints(label string, endpoints map[string]ProviderEndpoint) []string {
	var problems []string
	for _, proto := range sortedKeys(endpoints) {
		ep := endpoints[proto]
		u := ep.BaseURL
		if u == "" {
			problems = append(problems, fmt.Sprintf("%s.endpoints[%q]: needs a \"base_url\"", label, proto))
			continue
		}
		parsed, err := url.Parse(u)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s.endpoints[%q].base_url: %v", label, proto, err))
			continue
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			problems = append(problems, fmt.Sprintf(
				"%s.endpoints[%q].base_url: must be an http or https URL (%s)", label, proto, u))
		}
		if parsed.User != nil {
			problems = append(problems, fmt.Sprintf(
				"%s.endpoints[%q].base_url: must not carry userinfo — %q is a credential in a "+
					"file a pack ships to strangers; name an env var in api_key_env_name and let "+
					"the user hydrate it", label, proto, u))
		}
		if w := ep.WireAPI; w != "" && !KnownWireAPI(w) {
			problems = append(problems, fmt.Sprintf(
				"%s.endpoints[%q].wire_api: unknown wire_api %q (%s) — the derives translate "+
					"this value into each agent's own spelling, and a name outside the set "+
					"translates to nothing, so no agent would get the provider at all", label, proto, w, wireAPIList()))
		}
	}
	return problems
}

// knownWireAPIs is THE canonical protocol vocabulary (provider-table-fidelity.md §3.0a,
// OQ-PT1): three PROTOCOL-shaped names, chosen to be NOBODY'S dialect, so a derive cannot
// pass one through and have it work by accident. `openai-chat` and `openai-completions`
// were one protocol under two agents' spellings and collapse into `openai-chat-completions`;
// codex's `responses` loses its spelling in favour of `openai-responses`. A canonical name
// names a protocol — it is never a value any agent reads.
//
// It is closed in the same sense kinds.go's kind set is, but what the closure guards is no
// longer a verbatim crossing: each derive translates canonical → its own agent's spelling
// and emits NOTHING for a protocol that agent cannot speak (§3.4). A name outside the set
// is therefore not "a name some agent happens to know" — it is a name no derive can
// translate, which reaches every consumer as no protocol at all, silently, from a jail
// that booted green. Refusing it here is that failure moved to authoring time, where the
// pack author is still looking.
//
// It lives HERE and not beside the config key it composes into because both layers read
// one vocabulary: a provider a pack ships and the same provider a user writes over are
// the same entry in the same table, and the field's own doc (ProviderEndpoint.WireAPI)
// says the enum that tightens one tightens both. internal/config's validateWireAPI asks
// KnownWireAPIs for exactly that reason — a second copy of the literals is a second
// vocabulary that can drift away from the one this validator enforces.
//
// Sorted, because the enum's error message lists it and the message is a frozen string.
// Three values, one per protocol a provider can speak; a fourth is one line here PLUS a
// dialect row in every derive that can speak it — a protocol no derive translates is a
// name in this list that delivers nothing.
var knownWireAPIs = []string{"anthropic", "openai-chat-completions", "openai-responses"}

// KnownWireAPI reports whether v names a wire protocol this build knows. An empty string
// is NOT known — but for this field an empty value is the ABSENT claim rather than a
// defect (the field is omitempty, so "" and undeclared decode to the same fact, and an
// endpoint may leave the protocol to the consumer's own default), so emptiness is simply
// nobody's problem rather than the hard-problem-on-both-paths rule an empty `via` or
// env_shape value follows.
func KnownWireAPI(v string) bool {
	for _, api := range knownWireAPIs {
		if v == api {
			return true
		}
	}
	return false
}

// KnownWireAPIs returns the closed wire_api set, sorted, for diagnostics and tests —
// internal/config's validateWireAPI is the non-test consumer, so the enum a user's config
// is held to and the enum a manifest is held to are one list.
func KnownWireAPIs() []string {
	out := make([]string, len(knownWireAPIs))
	copy(out, knownWireAPIs)
	return out
}

// wireAPIList renders the closed set the way the validator's diagnostics name it
// ("\"anthropic\", \"openai-chat-completions\" or \"openai-responses\""), so the
// message cannot outlive the vocabulary it quotes (viaList's rule).
func wireAPIList() string {
	parts := make([]string, len(knownWireAPIs))
	for i, v := range knownWireAPIs {
		parts[i] = strconv.Quote(v)
	}
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

// The placeholders an env_shape value may be. Declared beside the validation that
// enforces them and exported because the consumer of the shape — the launch-time env
// composition in internal/agentenv — has to spell them identically: a second copy of the
// literal in the composer is a second vocabulary that can drift away from the one the
// validator enforces.
//
// Every placeholder names a field of the COMPOSED provider entry, which is the whole
// point of the closed set: a pack ships which variable takes its value from where, and
// the value itself is the user's — their endpoints, their region, their model ids, their
// hydrated credential. There is deliberately no literal form, because a literal in a
// git-tracked manifest is exactly where a credential gets shipped to strangers (§4.2's
// escape hatch); a static value belongs in a profile's own `env`, which is what that
// field is for.
const (
	// EnvShapeEndpoint is that protocol's base_url.
	EnvShapeEndpoint = "{endpoint}"
	// EnvShapeKey is the hydrated value of the provider's api_key_env_name variable —
	// relayed by the launcher from the launch environment, never carried by a manifest.
	EnvShapeKey = "{key}"
	// EnvShapeRegion is the provider's region — the address half of a provider reached
	// by region rather than by base URL (Bedrock).
	EnvShapeRegion = "{region}"
	// EnvShapeModelPrefix opens a {model:<alias>} reference: the model id the entry's
	// `models` map names for <alias>. The alias is whoever declares the shape's choice of
	// word — core keeps no model vocabulary, so an agent pack's own alias names
	// (claude's "haiku"/"sonnet") live in the pack that needs them, not here.
	EnvShapeModelPrefix = "{model:"
)

// EnvShapeModelAlias reports whether v is a {model:<alias>} reference — the one
// placeholder that carries a parameter, so it is spelled as a prefix and parsed rather
// than compared — returning the alias it names. An empty alias is not a reference:
// "{model:}" is an author's typo, and reading it as "alias empty" would compose nothing
// with nothing to say why.
func EnvShapeModelAlias(v string) (string, bool) {
	if !strings.HasPrefix(v, EnvShapeModelPrefix) || !strings.HasSuffix(v, "}") {
		return "", false
	}
	alias := v[len(EnvShapeModelPrefix) : len(v)-1]
	if alias == "" {
		return "", false
	}
	return alias, true
}

// KnownEnvShapeValue reports whether v is a placeholder this build can compose: one of
// the three parameterless forms, or a well-formed {model:<alias>} reference. An empty
// string is NOT known — it names no fact at all, and like an empty `via` it stays a hard
// problem on BOTH decode paths rather than version skew (unknownEnvShapeValueSkip).
//
// THE membership test for the placeholder set, and deliberately not a second spelling of
// it: the strict validator (ValidateProviderEnvShape) and the tolerant decoder's skip
// both ask this rather than comparing the constants themselves, so the two cannot come to
// disagree about which placeholders exist. That disagreement is not hypothetical — it was
// MEASURED for `via` (knownVias): teaching both switches a new value left the whole suite
// green while the tolerant decoder still dropped every contribution using it.
func KnownEnvShapeValue(v string) bool {
	if v == EnvShapeEndpoint || v == EnvShapeKey || v == EnvShapeRegion {
		return true
	}
	// The alias is checked for well-formedness, not resolved: `models` may be entirely
	// the user's, and a shape naming an alias no entry carries yet composes nothing
	// rather than failing a launch (agentenv's rule).
	_, isModel := EnvShapeModelAlias(v)
	return isModel
}

// ValidateProviderEnvShape checks the env_shape template values: the ONLY placeholders
// are "{endpoint}" (that protocol's base_url), "{key}" (the user's hydrated credential),
// "{region}" (the provider's region) and "{model:<alias>}" (a named model id).
//
// A closed set, and deliberately not a template language: an env_shape value that could
// interpolate anything else would be a channel for exactly the two things this kind must
// never ship — a credential, or a fact about the authoring machine.
//
// THE SET IS SKEW-SENSITIVE, and this refusal is only the AUTHORING half of the rule (the
// same split validateSkillsTier documents for its own two-value risk): a pack staged by a
// newer host may name a placeholder this build has never heard of, and refusing it here
// on the tolerant path would be the `tier` incident again — every jail on a
// pre-`just load` image refusing to boot over a variable it was never going to compose.
// So DecodeTolerant drops the unknown VARIABLE and reports it (unknownEnvShapeValueSkip)
// while this check keeps refusing it, and membership is KnownEnvShapeValue's on both
// sides so they cannot drift apart. Whoever adds a placeholder teaches the composer
// (agentenv) and this predicate in the same change — teaching only one of them is a
// manifest that validates and composes nothing.
//
// Exported because the value is no longer pack-only: a provider entry the USER wrote
// carries the same field with the same meaning (profiles-as-pack-variants.md §14,
// OQ-15), and internal/config's ValidateConfig calls this rather than keeping a second
// copy of the set — one placeholder vocabulary, enforced once.
func ValidateProviderEnvShape(label string, shape map[string]map[string]string) []string {
	var problems []string
	for _, proto := range sortedKeys(shape) {
		vars := shape[proto]
		for _, name := range sortedKeys(vars) {
			if KnownEnvShapeValue(vars[name]) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s.env_shape[%q][%q]: must be \"{endpoint}\", \"{key}\", \"{region}\" or "+
					"\"{model:<alias>}\" (%q)", label, proto, name, vars[name]))
		}
	}
	return problems
}

// sortedKeys returns a string-keyed map's keys sorted, so a validation pass over a map
// reports problems in a deterministic order — the same reason footprint.go sorts the maps
// it renders.
func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

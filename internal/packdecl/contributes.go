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
	"sort"
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
	// not own), not because it means something at both notches.
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

	// Raw carries kind-specific structured payloads that do not fit a scalar field
	// — today only a `config` contribution's surface definition (the agentcfg
	// surface schema), decoded by internal/agentcfg/manifest, kept as RawMessage
	// so packdecl stays free of an engine dependency (same reason Manifest.Surfaces
	// is RawMessage).
	Raw json.RawMessage `json:"config,omitempty"`
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
// was. And since the launchers moved to ~/.yolo-launchers — ordered after /bin — N
// launchers carry no more shadowing risk than one.
func (m *Manifest) InstallContributions() []Install {
	var out []Install
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram {
			continue
		}
		in := Install{Bin: c.Bin, Flags: c.Flags}
		switch c.Via {
		case "npm":
			in.Kind, in.Package = "npm", c.Package
		case "installer":
			in.Kind, in.InstallerURL = "native", c.URL
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

// DefaultBriefingFiles are the conventional pack-relative files a `briefing`
// contribution reads when it declares no `from`, in precedence order. Both names are in
// the wild and a pack author should not have to know which one yolo happens to read, so
// the convention is a PAIR rather than a single name — which is also why `briefing` has a
// candidate list where `skills` has one DefaultSkillsDir.
func DefaultBriefingFiles() []string { return []string{"AGENTS.md", "CLAUDE.md"} }

// BriefingCandidates returns the pack-relative files THIS briefing contribution's prose
// may live in, in precedence order: the declared `from` first, then the conventional pair.
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
	// Config is the raw `config` body — the keys this pack asserts onto the target.
	// Decoded by internal/agentcfg/manifest (DecodeOverlay), kept as RawMessage for the
	// same reason Contribution.Raw is: packdecl stays free of the engine dependency.
	Config json.RawMessage
}

// ConfigOverlayContributions returns every config-overlay the pack declares, in
// declaration order — which is the FOLD order the engine applies (later wins), so the
// order must not be normalized here.
func (m *Manifest) ConfigOverlayContributions() []ConfigOverlay {
	var out []ConfigOverlay
	for _, c := range m.Contributions() {
		if c.Kind == KindConfigOverlay {
			out = append(out, ConfigOverlay{Surface: c.Surface, Config: c.Raw})
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

// validateContribution checks one entry's required fields for its kind, and
// runs the path guards on every path-bearing field.
func validateContribution(label string, c Contribution) []string {
	var problems []string
	req := func(field, val string) {
		if val == "" {
			problems = append(problems, fmt.Sprintf("%s: kind %q needs %q", label, c.Kind, field))
		}
	}
	switch c.Kind {
	case KindProgram:
		req("bin", c.Bin)
		switch c.Via {
		case "npm":
			req("package", c.Package)
		case "installer":
			req("url", c.URL)
		case "":
			problems = append(problems, label+": program needs \"via\" (npm or installer)")
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown via %q (npm or installer)", label, c.Via))
		}
	case KindRequires:
		req("bin", c.Bin)
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
		// SkillsSource), AGENTS.md then CLAUDE.md for briefing (entrypoint's
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
	}
	return problems
}

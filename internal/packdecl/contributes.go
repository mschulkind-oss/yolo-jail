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

	// --- program (install) ---
	Bin     string   `json:"bin,omitempty"`     // program/launch: the binary name
	Via     string   `json:"via,omitempty"`     // program: "npm" | "installer"
	Package string   `json:"package,omitempty"` // program via npm: the npm package
	URL     string   `json:"url,omitempty"`     // program via installer: the curl-to-shell URL
	Flags   []string `json:"flags,omitempty"`   // program: extra install flags; launch: the injected flags
	// InstallHints maps a host package manager ("brew"|"apt"|"dnf"|"pacman"|"nix") to
	// the package name that provides Bin on that manager (env-manager plan Phase 6). A
	// pack author knows this better than a built-in attr→pkg table that goes stale; it
	// is what `check`/`apply` below jail use to probe for the binary and, if missing,
	// emit a runnable manifest (a Brewfile and kin). Optional: absent means yolo can
	// name the binary as missing but not the remedy. Declared by the pack that
	// INTRODUCES the dep, never re-declaring one another tool owns.
	InstallHints map[string]string `json:"install_hints,omitempty"`

	// --- skills / briefing / files (staged trees) ---
	From  string `json:"from,omitempty"`  // pack-relative source path
	Into  string `json:"into,omitempty"`  // home-relative jail destination
	After string `json:"after,omitempty"` // briefing: "host:<path>" to prepend the user's own file

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

	// Raw carries kind-specific structured payloads that do not fit a scalar field
	// — today only a `config` contribution's surface definition (the agentcfg
	// surface schema), decoded by internal/agentcfg/manifest, kept as RawMessage
	// so packdecl stays free of an engine dependency (same reason Manifest.Surfaces
	// is RawMessage).
	Raw json.RawMessage `json:"config,omitempty"`
}

// Contributions returns the pack's declared contributions. THE accessor every
// read path uses — the legacy-shaped projections below (InstallContribution,
// HostFileContributions, …) are all derived from it, so a consumer that wants
// "the install" or "the host files" reads them without touching the manifest
// shape.
func (m *Manifest) Contributions() []Contribution {
	return m.Contributes
}

// The projections below group the flat contributions by kind into the per-effect
// shapes core consumes (Install, HostFile, Mount, ...), so a caller that wants "the
// install" or "the host files" reads them through one accessor rather than
// filtering the contribution list itself.

// InstallContribution returns the pack's program contribution as a legacy *Install,
// or nil when it declares none.
func (m *Manifest) InstallContribution() *Install {
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram {
			continue
		}
		in := &Install{Bin: c.Bin, Flags: c.Flags}
		switch c.Via {
		case "npm":
			in.Kind, in.Package = "npm", c.Package
		case "installer":
			in.Kind, in.InstallerURL = "native", c.URL
		}
		return in
	}
	return nil
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
}

// DepRequirements returns every program contribution that carries install_hints, as
// the host-dep requirements the dep checker probes. A program with no hints still needs
// its bin, but yolo has no remedy to offer, so it is reported as unprobeable-remedy
// rather than omitted — the caller decides. Only program contributions with a Bin are
// returned.
func (m *Manifest) DepRequirements() []DepRequirement {
	var out []DepRequirement
	for _, c := range m.Contributions() {
		if c.Kind != KindProgram || c.Bin == "" {
			continue
		}
		out = append(out, DepRequirement{Bin: c.Bin, Hints: c.InstallHints})
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
		label := fmt.Sprintf("contributes[%d]", i)
		if c.Kind == "" {
			problems = append(problems, label+": missing \"kind\"")
			continue
		}
		if msg := ValidateKind(c.Kind); msg != "" {
			problems = append(problems, label+": "+msg)
			continue
		}
		problems = append(problems, validateContribution(label, c)...)
	}
	return problems
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
	case KindSkills, KindBriefing, KindFiles:
		req("from", c.From)
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
	}
	return problems
}

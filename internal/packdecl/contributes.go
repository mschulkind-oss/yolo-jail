package packdecl

// contributes.go is the Phase-4 manifest shape: one `contributes` list of typed
// contributions, each with an explicit `kind` from the closed set (kinds.go),
// replacing the nine effect fields (install/mounts/writableDirs/sharedDirs/
// hostFiles/surfaces/launchFlags/flagAliases/hooks/retireMiseTools) and the
// filename-based magic-string dispatch (docs/design/pack-declaration-reform.md
// §3.1).
//
// COMPATIBILITY WINDOW (Phase 4 step 1): both shapes parse. A manifest with a
// non-empty `contributes` uses it and ignores the legacy fields; a manifest
// without one has its contributions SYNTHESIZED from the legacy fields (the exact
// inverse of the Phase-1 packload footprint shim). This lets the read paths move
// to Contributions() one at a time, each nested-jail-verified while the shipped
// pack.json still work, before the legacy fields are deleted in the final step.
// Nothing in core reads Contribution yet — this is the parse + normalize layer.

import (
	"encoding/json"
	"fmt"
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

	// --- reads-host ---
	Host string `json:"host,omitempty"` // reads-host: the host-home-relative file

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

// Contributions returns the pack's effective contributions: the declared
// `contributes` list when non-empty, else the synthesis from the legacy fields.
// This is THE accessor the read paths migrate to; while both shapes are
// supported it hides which one a given pack used.
func (m *Manifest) Contributions() []Contribution {
	if len(m.Contributes) > 0 {
		return m.Contributes
	}
	return m.synthesizeContributions()
}

// synthesizeContributions builds contributions from the legacy effect fields —
// the inverse of the Phase-1 footprint shim, so a pack.json that has not yet been
// rewritten behaves identically. Deleted with the legacy fields in the final step.
func (m *Manifest) synthesizeContributions() []Contribution {
	var out []Contribution
	if m.Install != nil && m.Install.Bin != "" {
		c := Contribution{Kind: KindProgram, Bin: m.Install.Bin, Flags: m.Install.Flags}
		switch m.Install.Kind {
		case "npm":
			c.Via, c.Package = "npm", m.Install.Package
		case "native":
			c.Via, c.URL = "installer", m.Install.InstallerURL
		}
		out = append(out, c)
	}
	for _, mt := range m.Mounts {
		switch {
		case mt.From == "skills":
			out = append(out, Contribution{Kind: KindSkills, From: mt.From, Into: mt.To})
		case mt.From == "AGENTS.md" || mt.From == "CLAUDE.md":
			c := Contribution{Kind: KindBriefing, From: mt.From, Into: mt.To}
			if mt.HostOverlay != "" {
				c.After = "host:" + mt.HostOverlay
			}
			out = append(out, c)
		default:
			out = append(out, Contribution{Kind: KindFiles, From: mt.From, Into: mt.To})
		}
	}
	if len(m.Surfaces) > 0 {
		out = append(out, Contribution{Kind: KindConfig, Raw: m.Surfaces})
	}
	for _, d := range m.WritableDirs {
		out = append(out, Contribution{Kind: KindState, At: d, Scope: "workspace"})
	}
	for _, d := range m.SharedDirs {
		out = append(out, Contribution{Kind: KindState, At: d, Scope: "machine"})
	}
	for _, hf := range m.HostFiles {
		out = append(out, Contribution{Kind: KindReadsHost, Host: hf.From, Into: hf.To})
	}
	for bin, flags := range m.LaunchFlags {
		out = append(out, Contribution{Kind: KindLaunch, Bin: bin, Flags: flags, Aliases: m.FlagAliases})
	}
	for _, h := range m.Hooks {
		out = append(out, Contribution{Kind: KindHook, Hook: h.Name, From: h.File, At: h.SharedDir})
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
// re-runs the path guards the legacy fields had.
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

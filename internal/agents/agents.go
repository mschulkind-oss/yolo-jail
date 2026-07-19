// Package agents is a hand-ported, drift-suite-pinned copy of
// entrypoint/agent_registry.py — the single source of truth for the coding
// agents yolo-jail can install into a jail. Per the port plan (§3, foundation
// packages) this is pure data hand-ported to Go with NO Python-side refactor;
// the drift suite byte-diffs it against the live Python registry on every
// commit, so any Python change without a matching Go change is a red build.
package agents

// InstallSpec: how an agent CLI is installed and updated inside the jail.
// Kind is "npm" or "native".
type InstallSpec struct {
	Kind         string   // "npm" | "native"
	Bin          string   // binary name on PATH (also the launcher/shim filename)
	Package      string   // npm package name (kind == "npm"); "" == Python None
	InstallFlags []string // extra npm flags
	InstallerURL string   // curl installer (kind == "native"); "" == Python None
}

// BriefingSpec: where an agent's AGENTS.md/CLAUDE.md briefing is staged/mounted.
type BriefingSpec struct {
	Staging    string
	Mount      string
	HostSource string
}

// AgentSpec is the full per-agent record. Optional string fields use "" for
// Python None; optional slices use nil for the Python default_factory=list.
type AgentSpec struct {
	Name         string
	Install      InstallSpec
	ConfigWriter string // function name in entrypoint.agent_configs
	Briefing     BriefingSpec
	OverlayDirs  []string
	Skills       string // "" == Python None
	YoloFlags    []string
	Alias        string // "" == Python None
	MiseRetire   []string
}

// SkillsStaging returns the staging dir name for this agent's skills, or ""
// when the agent has no skills dir. Mirrors AgentSpec.skills_staging.
func (a AgentSpec) SkillsStaging() string {
	if a.Skills == "" {
		return ""
	}
	return "skills-" + a.Name
}

// YoloFlagAliases: --yolo and -y are the same switch (gemini); the injector
// must not add --yolo when the user already passed -y.
var YoloFlagAliases = map[string][]string{"--yolo": {"-y"}}

// specs preserves the exact declaration order of Python's _SPECS list. Order
// is load-bearing (Order/ALL_MISE_RETIRE follow it).
var specs = []AgentSpec{
	{
		Name: "claude",
		Install: InstallSpec{
			Kind:         "native",
			Bin:          "claude",
			InstallerURL: "https://claude.ai/install.sh",
		},
		ConfigWriter: "configure_claude",
		Briefing: BriefingSpec{
			Staging:    "CLAUDE.md",
			Mount:      ".claude/CLAUDE.md",
			HostSource: ".claude/CLAUDE.md",
		},
		OverlayDirs: []string{".claude"},
		Skills:      ".claude/skills",
		YoloFlags:   []string{"--dangerously-skip-permissions"},
		Alias:       "",
		MiseRetire:  []string{`"npm:@anthropic-ai/claude-code"`},
	},
	{
		Name:         "copilot",
		Install:      InstallSpec{Kind: "npm", Bin: "copilot", Package: "@github/copilot"},
		ConfigWriter: "configure_copilot",
		Briefing: BriefingSpec{
			Staging:    "AGENTS-copilot.md",
			Mount:      ".copilot/AGENTS.md",
			HostSource: ".copilot/AGENTS.md",
		},
		OverlayDirs: []string{".copilot"},
		Skills:      ".copilot/skills",
		YoloFlags:   []string{"--yolo", "--no-auto-update"},
		Alias:       "copilot --yolo --no-auto-update",
		MiseRetire:  []string{`"npm:@github/copilot"`},
	},
	{
		Name:         "gemini",
		Install:      InstallSpec{Kind: "npm", Bin: "gemini", Package: "@google/gemini-cli"},
		ConfigWriter: "configure_gemini",
		Briefing: BriefingSpec{
			Staging:    "AGENTS-gemini.md",
			Mount:      ".gemini/AGENTS.md",
			HostSource: ".gemini/AGENTS.md",
		},
		OverlayDirs: []string{".gemini"},
		Skills:      ".gemini/skills",
		YoloFlags:   []string{"--yolo"},
		Alias:       "gemini --yolo",
		MiseRetire:  []string{"gemini"},
	},
	{
		Name:         "opencode",
		Install:      InstallSpec{Kind: "npm", Bin: "opencode", Package: "opencode-ai"},
		ConfigWriter: "configure_opencode",
		Briefing: BriefingSpec{
			Staging:    "AGENTS-opencode.md",
			Mount:      ".config/opencode/AGENTS.md",
			HostSource: ".config/opencode/AGENTS.md",
		},
		OverlayDirs: nil,
		Skills:      "",
		YoloFlags:   nil,
		Alias:       "",
		MiseRetire:  nil,
	},
	{
		Name: "pi",
		Install: InstallSpec{
			Kind:         "npm",
			Bin:          "pi",
			Package:      "@earendil-works/pi-coding-agent",
			InstallFlags: []string{"--ignore-scripts"},
		},
		ConfigWriter: "configure_pi",
		Briefing: BriefingSpec{
			Staging:    "AGENTS-pi.md",
			Mount:      ".pi/agent/AGENTS.md",
			HostSource: ".pi/agent/AGENTS.md",
		},
		OverlayDirs: []string{".pi"},
		Skills:      "",
		YoloFlags:   nil,
		Alias:       "",
		MiseRetire:  nil,
	},
	{
		Name:         "codex",
		Install:      InstallSpec{Kind: "npm", Bin: "codex", Package: "@openai/codex"},
		ConfigWriter: "configure_codex",
		Briefing: BriefingSpec{
			Staging:    "AGENTS-codex.md",
			Mount:      ".codex/AGENTS.md",
			HostSource: ".codex/AGENTS.md",
		},
		OverlayDirs: []string{".codex"},
		Skills:      "",
		YoloFlags:   []string{"--dangerously-bypass-approvals-and-sandbox"},
		Alias:       "",
		MiseRetire:  nil,
	},
}

// Order is the agent names in declaration order (Python: list(AGENTS.keys())).
var Order = func() []string {
	o := make([]string, len(specs))
	for i, s := range specs {
		o[i] = s.Name
	}
	return o
}()

// agentsByName mirrors the AGENTS dict.
var agentsByName = func() map[string]AgentSpec {
	m := make(map[string]AgentSpec, len(specs))
	for _, s := range specs {
		m[s.Name] = s
	}
	return m
}()

// Get returns the spec for name and whether it exists.
func Get(name string) (AgentSpec, bool) {
	s, ok := agentsByName[name]
	return s, ok
}

// DefaultAgents is the agent set when a config omits `agents` — claude only.
var DefaultAgents = []string{"claude"}

// ValidAgents is the sorted set of known agent names (Python: sorted(VALID_AGENTS)).
var ValidAgents = func() []string {
	v := make([]string, len(Order))
	copy(v, Order)
	sortStrings(v)
	return v
}()

// AllMiseRetire is every agent's mise-retire tokens, unioned in declaration
// order (Python: [token for spec in _SPECS for token in spec.mise_retire]).
var AllMiseRetire = func() []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.MiseRetire...)
	}
	return out
}()

// AllOverlayDirs is the sorted unique union of every agent's overlay dirs
// (Python: sorted({d for spec in _SPECS for d in spec.overlay_dirs})).
var AllOverlayDirs = func() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range specs {
		for _, d := range s.OverlayDirs {
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				out = append(out, d)
			}
		}
	}
	sortStrings(out)
	return out
}()

// ResolveAgents returns the AgentSpec list for names (unknown names skipped),
// preserving the order of names. names==nil falls back to DefaultAgents.
// Mirrors resolve_agents.
func ResolveAgents(names []string) []AgentSpec {
	if names == nil {
		names = DefaultAgents
	}
	var out []AgentSpec
	for _, n := range names {
		if s, ok := agentsByName[n]; ok {
			out = append(out, s)
		}
	}
	return out
}

// InjectYoloFlags returns fullCommand with the leading agent's YOLO flags
// injected right after the binary, mirroring _inject_agent_yolo_flags. The
// agent is matched by fullCommand[0] == its Install.Bin; a non-agent head is
// returned unchanged. Flags are inserted so their relative order is preserved
// (Python inserts each at index 1 in reverse); a flag already present — or a
// known alias (e.g. -y for --yolo) — is skipped. The input slice is not mutated;
// a new slice is returned (Go idiom over Python's in-place mutation).
func InjectYoloFlags(fullCommand []string) []string {
	if len(fullCommand) == 0 {
		return fullCommand
	}
	head := fullCommand[0]
	var spec *AgentSpec
	for i := range specs {
		if specs[i].Install.Bin == head {
			spec = &specs[i]
			break
		}
	}
	if spec == nil {
		return fullCommand
	}
	out := append([]string{}, fullCommand...)
	// Insert in reverse so relative order is preserved (each at index 1).
	for i := len(spec.YoloFlags) - 1; i >= 0; i-- {
		flag := spec.YoloFlags[i]
		if containsStr(out, flag) {
			continue
		}
		skip := false
		for _, a := range YoloFlagAliases[flag] {
			if containsStr(out, a) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Insert flag at index 1 without aliasing the backing array.
		inserted := make([]string, 0, len(out)+1)
		inserted = append(inserted, out[0], flag)
		inserted = append(inserted, out[1:]...)
		out = inserted
	}
	return out
}

func containsStr(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// sortStrings is a tiny insertion sort to avoid importing "sort" for these
// short, fixed slices (keeps the package dependency-free like the Python one).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

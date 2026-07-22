// Package agents is the single source of truth for the coding agents yolo-jail
// can install into a jail: pure registry data plus the helpers that derive the
// ordered/sorted views over it. Golden tests pin the exact bytes, so any drift
// in this table is a red build.
package agents

// InstallSpec: how an agent CLI is installed and updated inside the jail.
// Kind is "npm" or "native".
type InstallSpec struct {
	Kind         string   // "npm" | "native"
	Bin          string   // binary name on PATH (also the launcher/shim filename)
	Package      string   // npm package name (kind == "npm"); "" == unset
	InstallFlags []string // extra npm flags
	InstallerURL string   // curl installer (kind == "native"); "" == unset
}

// BriefingSpec: where an agent's AGENTS.md/CLAUDE.md briefing is staged/mounted.
type BriefingSpec struct {
	Staging    string
	Mount      string
	HostSource string
}

// AgentSpec is the full per-agent record. Optional string fields use "" when
// unset; optional slices use nil for an empty default.
type AgentSpec struct {
	Name         string
	Install      InstallSpec
	ConfigWriter string // config-writer function name
	Briefing     BriefingSpec
	OverlayDirs  []string
	Skills       string // "" == no skills dir
	YoloFlags    []string
	Alias        string // "" == no alias
	MiseRetire   []string
}

// SkillsStaging returns the staging dir name for this agent's skills, or ""
// when the agent has no skills dir.
func (a AgentSpec) SkillsStaging() string {
	if a.Skills == "" {
		return ""
	}
	return "skills-" + a.Name
}

// YoloFlagAliases: --yolo and -y are the same switch (gemini); the injector
// must not add --yolo when the user already passed -y.
var YoloFlagAliases = map[string][]string{"--yolo": {"-y"}}

// specs is the agent registry. Declaration order is load-bearing (Order and
// AllMiseRetire follow it).
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
	{
		// agy is Google's Antigravity CLI — a native Go binary installed to
		// ~/.local/bin/agy via a curl|bash installer. It shares the ~/.gemini
		// tree with gemini but lives in its own antigravity-cli/ subdir, so its
		// overlay is scoped to .gemini/antigravity-cli (zero collision with
		// gemini's .gemini overlay — both are seeded/persisted independently).
		// Born directly on the prism: settings.json is the agySettings surface
		// (internal/agentcfg/builtin.go), configured by ConfigureAgyPrism with no
		// bespoke fallback (docs/plans/antigravity-agy-support.md).
		//
		// NOTE: InstallerURL is the plan's PLACEHOLDER pending confirmation of
		// the real Antigravity installer endpoint — the config plumbing is
		// URL-agnostic; only first-use `agy` install fetches it.
		Name: "agy",
		Install: InstallSpec{
			Kind:         "native",
			Bin:          "agy",
			InstallerURL: "https://antigravity.google.com/install.sh",
		},
		ConfigWriter: "configure_agy",
		Briefing: BriefingSpec{
			Staging:    "AGENTS-agy.md",
			Mount:      ".gemini/antigravity-cli/AGENTS.md",
			HostSource: ".gemini/antigravity-cli/AGENTS.md",
		},
		OverlayDirs: []string{".gemini/antigravity-cli"},
		Skills:      ".gemini/antigravity-cli/skills",
		YoloFlags:   []string{"--dangerously-skip-permissions"},
		Alias:       "",
		MiseRetire:  nil,
	},
}

// Order is the agent names in declaration order.
var Order = func() []string {
	o := make([]string, len(specs))
	for i, s := range specs {
		o[i] = s.Name
	}
	return o
}()

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

// ValidAgents is the sorted set of known agent names.
var ValidAgents = func() []string {
	v := make([]string, len(Order))
	copy(v, Order)
	sortStrings(v)
	return v
}()

// AllMiseRetire is every agent's mise-retire tokens, unioned in declaration
// order.
var AllMiseRetire = func() []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.MiseRetire...)
	}
	return out
}()

// AllOverlayDirs is the sorted unique union of every agent's overlay dirs.
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
// injected right after the binary. The agent is matched by fullCommand[0] ==
// its Install.Bin; a non-agent head is returned unchanged. Flags are inserted
// in reverse (each at index 1) so their relative order is preserved; a flag
// already present — or a known alias (e.g. -y for --yolo) — is skipped. The
// input slice is not mutated; a new slice is returned.
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
// short, fixed slices.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

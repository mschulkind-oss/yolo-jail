// Package pluginpack recognizes an EXISTING agent plugin — a directory carrying a
// `.claude-plugin/plugin.json` manifest — sitting inside a yolo pack, so a user can pull
// one in as a pack instead of hand-translating it.
//
// The design constraint that shapes this whole package: yolo READS the fields it needs and
// passes the tree through intact. It deliberately does NOT re-implement the plugin schema.
// A plugin manifest declares more than yolo models — hooks, MCP servers, LSP servers,
// sub-agents, output styles, commands, themes, workflows — and lowering those into yolo's own
// contribution kinds would silently drop every one yolo has no kind for, then need a new
// lowering rule each time the plugin schema grows. So the decode below is LENIENT (no
// DisallowUnknownFields, unlike packdecl's own manifest): an unknown field is somebody
// else's feature travelling through, not an authoring mistake yolo should report.
//
// What yolo does need from the manifest is exactly two things:
//
//   - the NAME, because it is the destination directory AND the namespace the tools
//     qualify the plugin's skills with (`<name>:<skill>`);
//   - WHICH COMPONENTS it carries, because some of them are CODE THAT RUNS. A plugin is
//     someone else's repo, and hooks, MCP and LSP servers, monitors, `bin/` executables, a
//     subagent status line, workflow scripts and syntax-highlighting grammars mean processes
//     started, or JavaScript run, on the user's behalf. Those are what the footprint's
//     ⚠ RUNS CODE line reports (packload.FootprintOf), and a component yolo failed to notice
//     would be hooks nobody was told about. They were an APPROVAL question until OQ-TP9 deleted
//     the prompt (docs/design/trust-paths.md, 2026-09-04); they are a DISCLOSURE question now.
//
// "Carries" is every manifest AND the filesystem. Claude Code gives every component a default
// location it loads when the manifest is silent (hooks/hooks.json, .mcp.json, .lsp.json,
// monitors/monitors.json, bin/, settings.json, …: the "Standard layout" table of
// https://code.claude.com/docs/en/plugins-reference, and workflows/ and themes/ beside them in
// Claude Code's own loader), so a manifest-only reading misses code that runs; and the tools
// disagree about which manifest is the plugin's (see manifestDirs).
// That reading is the one thing here that is not a pass-through: the defaults are listed in
// the components table below, beside the fields they stand in for.
//
// It is dependency-free on the rest of the repo, for the same reason packdecl is: both the
// host CLI (footprint, `pack init`) and the host renderer read it.
package pluginpack

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// manifestDirs is where a plugin manifest may live inside a plugin directory, in the order
// the tools search. Verified against the shipped Copilot bundle (its own search list) and
// Claude's documented `.claude-plugin/` convention; "." means the manifest sits directly in
// the plugin dir.
//
// All four are recognized even though Claude reads only `.claude-plugin/`, because
// recognition drives the TRUST report: a manifest yolo did not notice is a manifest whose
// hooks were never surfaced. And every one PRESENT is read, not only the first, because the
// tools disagree about which is the plugin's: Copilot takes the first in this order that
// parses, while Claude Code reads `.claude-plugin/plugin.json` whatever else sits beside it
// (measured on 2.1.288), so a component-free `plugin.json` at the root used to hide the
// manifest Claude Code runs. Which one is primary is recorded (Plugin.ManifestPath) so
// delivery writes its ownership marker back into the same file.
var manifestDirs = []string{".plugin", ".", ".github/plugin", ".claude-plugin"}

// manifestName is the manifest file inside one of manifestDirs.
const manifestName = "plugin.json"

// PreferredManifestDir is where yolo puts a manifest it writes itself. It is the only one
// of manifestDirs that BOTH known tier-A tools read, so a tree yolo authors uses it.
const PreferredManifestDir = ".claude-plugin"

// SkillsSubdir is the pack-relative directory whose immediate children Discover scans for
// wrapped plugins. Exported because `pack init --from-plugin` scaffolds into it.
const SkillsSubdir = "skills"

// Manifest is the subset of a plugin manifest yolo reads. Every component field is kept as
// RawMessage rather than a typed shape: yolo only needs to know whether it is DECLARED (and
// for the path-bearing ones, which paths), and decoding someone else's evolving schema into
// Go structs would turn their next release into yolo's parse error.
type Manifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`

	// Path-bearing components: a string, an array of strings, or
	// {paths: [...], exclusive: bool} (see pathSpec).
	Skills   json.RawMessage `json:"skills"`
	Commands json.RawMessage `json:"commands"`
	Agents   json.RawMessage `json:"agents"`

	// Code-bearing components. Each one means a process runs on the user's behalf, which
	// is the trust question this package exists to surface.
	Hooks      json.RawMessage `json:"hooks"`
	MCPServers json.RawMessage `json:"mcpServers"`
	LSPServers json.RawMessage `json:"lspServers"`

	OutputStyles json.RawMessage `json:"outputStyles"`
	// Themes is the top-level spelling of a plugin's color themes; Claude Code reads
	// `experimental.themes` first (see themes).
	Themes json.RawMessage `json:"themes"`
	// Workflows names workflow scripts: JavaScript files, or directories of them, that Claude
	// Code runs when a workflow is invoked. Only the top-level spelling loads (2.1.289).
	Workflows json.RawMessage `json:"workflows"`

	// Monitors is the top-level spelling of a plugin's background monitors, which Claude Code
	// still loads with a `claude plugin validate` warning; Experimental carries the current
	// one, `experimental.monitors`. Experimental stays raw for the reason every field here
	// does: typing it would make a manifest with any other shape there unreadable, and an
	// unreadable manifest is not a plugin at all, so its hooks would be disclosed nowhere.
	Monitors     json.RawMessage `json:"monitors"`
	Experimental json.RawMessage `json:"experimental"`

	// Settings is the inline form of the plugin's root settings.json. Of the two keys Claude
	// Code honors there, `subagentStatusLine` is a shell command it runs to draw each
	// subagent's row in the agent panel; `agent` names one of the plugin's own agents, which
	// the main session then runs as.
	Settings json.RawMessage `json:"settings"`
}

// setting is the manifest's `settings.<key>`.
func (m Manifest) setting(key string) json.RawMessage {
	var s map[string]json.RawMessage
	if declared(m.Settings) && json.Unmarshal(m.Settings, &s) == nil {
		return s[key]
	}
	return nil
}

// experimental is the manifest's `experimental.<key>`.
func (m Manifest) experimental(key string) json.RawMessage {
	var exp map[string]json.RawMessage
	if declared(m.Experimental) && json.Unmarshal(m.Experimental, &exp) == nil {
		return exp[key]
	}
	return nil
}

// monitors is the declared monitors value: `experimental.monitors` when present, else the
// top-level `monitors`.
func (m Manifest) monitors() json.RawMessage {
	if exp := m.experimental("monitors"); declared(exp) {
		return exp
	}
	return m.Monitors
}

// themes is the declared themes value: `experimental.themes` when present, else the top-level
// `themes`, the order Claude Code reads them in.
func (m Manifest) themes() json.RawMessage {
	if exp := m.experimental("themes"); declared(exp) {
		return exp
	}
	return m.Themes
}

// Plugin is one recognized plugin tree.
type Plugin struct {
	// Dir is the absolute plugin root — the directory that gets copied.
	Dir string
	// ManifestPath is the absolute manifest file found (one of manifestDirs).
	ManifestPath string
	// Manifest is what it declared.
	Manifest Manifest

	// others is every OTHER manifest the tree carries that parses, in manifestDirs order.
	// Components and ComponentPaths read them beside Manifest (see manifestDirs).
	others []foundManifest
}

// foundManifest is one parsed manifest and its plugin-relative, slash-separated path.
type foundManifest struct {
	rel string
	m   Manifest
}

// manifests is the primary manifest, then the others.
func (p *Plugin) manifests() []foundManifest {
	return append([]foundManifest{{rel: p.ManifestRel(), m: p.Manifest}}, p.others...)
}

// ManifestRel is the manifest's path relative to the plugin dir, slash-separated. Delivery
// needs it to write yolo's ownership marker back into the SAME file the source used, rather
// than giving a plugin whose manifest lives at `.plugin/plugin.json` a second one under
// `.claude-plugin/`.
func (p *Plugin) ManifestRel() string {
	rel, err := filepath.Rel(p.Dir, p.ManifestPath)
	if err != nil {
		return PreferredManifestDir + "/" + manifestName
	}
	return filepath.ToSlash(rel)
}

// Name is the plugin's identity: its declared name, falling back to its directory name.
//
// The declared name wins because it is what the TOOLS namespace by — deliver a plugin into
// a directory whose name disagrees with its manifest and the skills invoke under a prefix
// the user cannot predict from the filesystem.
func (p *Plugin) Name() string {
	if n := strings.TrimSpace(p.Manifest.Name); n != "" {
		return n
	}
	return filepath.Base(p.Dir)
}

// Component is one thing a plugin carries, as yolo reports it.
type Component struct {
	// Name is the manifest field ("hooks", "mcpServers", "syntaxHighlighting" under
	// `experimental`, …), the key of a setting ("subagentStatusLine", "agent"), or "bin" for the
	// executables directory, which has no field.
	Name string
	// Detail is a one-line human note for the footprint and refusal lines.
	Detail string
	// RunsCode marks a component that starts a process or executes a script on the
	// user's behalf. These are the ones the launch footprint names outright
	// (internal/packload/footprint.go). It gated an install approval until that
	// approval was deleted; disclosure is what it feeds now.
	RunsCode bool
	// Sources is where the plugin carries it, plugin-relative and slash-separated: the
	// manifest file when a manifest field declares it, then the default location when Claude
	// Code loads that too. At least one entry. A component named both ways is ONE component
	// with two sources, so a count of components is a count of kinds of code, never of files.
	Sources []string
}

// defaultShape is what makes a default location present.
type defaultShape int

const (
	// defaultFile is present when a file (not a directory) sits there.
	defaultFile defaultShape = iota
	// defaultDir is present when a directory with at least one entry sits there. An empty
	// directory loads nothing.
	defaultDir
	// defaultExecDir is present when a directory sits there holding at least one entry that
	// is not a directory: `bin/` goes on a shell's PATH, and a PATH lookup never descends.
	defaultExecDir
	// defaultKeyInFile is present when a JSON object sits there declaring the component's own
	// name as a key: a root settings.json counts for subagentStatusLine only when it sets it.
	defaultKeyInFile
)

// components is the closed description of what yolo reports per component, with the
// code-running verdict attached. `skills` is absent on purpose: it is the one component
// yolo models itself, so it is delivered rather than reported as a pass-through.
//
// It is EVERY component Claude Code loads from a plugin, not only the code: a flat delivery
// refuses by name exactly what this table reports (hostskills' deliverPluginFlat), so a row
// missing here is a component that vanishes from a flat skills dir without a word.
//
// The order is the report's: code first, then prose. Two launches of one pack print the same
// line only because this order is fixed.
//
// def and merges are Claude Code's, verified against its plugin reference
// (https://code.claude.com/docs/en/plugins-reference, "Standard layout" and "How each key
// combines with its default location", read 2026-10-03), its changelog, and the plugin loader
// shipped in Claude Code 2.1.289 (MEASURED 2026-10-03, strings of its binary):
//
//   - hooks/hooks.json, .mcp.json and .lsp.json MERGE with the manifest field: the default
//     file loads first, then what the manifest declares. hooks/hooks.json also carries the
//     `modules` key of a JavaScript or TypeScript hooks module (Claude Code 2.1.287's mods),
//     which runs inside the agent itself.
//   - monitors/monitors.json, commands/, agents/, output-styles/, themes/ and workflows/ are
//     REPLACED by their field: a manifest that sets one loads its paths and not the default.
//     The loader reads `experimental.monitors ?? monitors` and `experimental.themes ?? themes`,
//     but only the top-level `workflows` and `outputStyles`.
//   - bin/ has no field: "Files in bin/ at the plugin root are on the PATH of the Bash tool's
//     shell while the plugin is enabled" (changelog 2.1.91: "Plugins can now ship executables
//     under bin/ and invoke them as bare commands from the Bash tool").
//   - subagentStatusLine and agent are settings, in a root settings.json or the manifest's
//     `settings`, and 2.1.289 keeps exactly those two keys from either. It reads ONE of the two
//     sources, whole: a settings.json that sets either key, with values its settings schema
//     accepts, is used, and the manifest's `settings` is then never read (the reference:
//     settings.json "takes precedence over this key"). yolo still reports each key from every
//     source that sets it, so a key only a shadowed manifest sets is reported though it never
//     applies. That over-report is deliberate: Claude Code falls back to the manifest when
//     settings.json's values fail its settings schema, so deciding the shadow means copying that
//     schema, and a copy that drifts hides a status-line command that runs. Both rows are marked as
//     merging so that the manifest's key is reported beside settings.json's.
//   - a workflow is a JavaScript file Claude Code runs, in a `node:vm` context, when the
//     workflow is invoked; a theme is a JSON file its theme picker offers.
//   - `experimental.syntaxHighlighting.hljsLanguages` names highlight.js grammars, each a
//     JavaScript function Claude Code's highlighter calls in its own process. It has no default
//     location.
//
// Not rows, because each loads nothing of its own: `channels` binds one of the plugin's MCP
// servers (reported as mcpServers); `userConfig` and `dependencies` are prompts and other
// plugins; `binaries` is fetched into bin/ when Claude Code installs the plugin, which yolo's
// copy is not;
// `types` and `experimental.evals` are read by a plugin's dependents' type check and an
// evaluation harness, not by a session.
var components = []struct {
	name     string
	detail   string
	runsCode bool
	// pick is the manifest field's value; nil when the component has no field.
	pick func(Manifest) json.RawMessage
	// paths says pick's value names plugin paths (a string, a list of them, or {paths}), which
	// a flat delivery keeps out of a root skill's copy (ComponentPaths). A setting's value and an
	// inline object name none: `settings.agent` is an agent's name.
	paths bool
	// def is the plugin-relative default location, slash-separated; "" for none.
	def   string
	shape defaultShape
	// merges says the default loads beside a declaration rather than being replaced by it.
	merges bool
}{
	{name: "hooks", detail: "runs code at agent lifecycle events", runsCode: true,
		pick: func(m Manifest) json.RawMessage { return m.Hooks }, paths: true,
		def: "hooks/hooks.json", shape: defaultFile, merges: true},
	{name: "mcpServers", detail: "starts MCP server processes", runsCode: true,
		pick: func(m Manifest) json.RawMessage { return m.MCPServers }, paths: true,
		def: ".mcp.json", shape: defaultFile, merges: true},
	{name: "lspServers", detail: "starts language server processes", runsCode: true,
		pick: func(m Manifest) json.RawMessage { return m.LSPServers }, paths: true,
		def: ".lsp.json", shape: defaultFile, merges: true},
	{name: "monitors", detail: "runs background shell commands for the whole session", runsCode: true,
		pick: Manifest.monitors, paths: true, def: "monitors/monitors.json", shape: defaultFile},
	{name: "bin", detail: "puts executables on the agent's shell PATH", runsCode: true,
		def: "bin", shape: defaultExecDir},
	{name: "subagentStatusLine", detail: "runs a shell command to draw each subagent's status row",
		runsCode: true, pick: settingKey("subagentStatusLine"),
		def: "settings.json", shape: defaultKeyInFile, merges: true},
	{name: "workflows", runsCode: true,
		detail: "adds workflow scripts, JavaScript Claude Code runs when one is invoked",
		pick:   func(m Manifest) json.RawMessage { return m.Workflows },
		paths:  true, def: "workflows", shape: defaultDir},
	{name: "syntaxHighlighting", runsCode: true,
		detail: "adds syntax-highlighting grammars, JavaScript Claude Code runs in its own process",
		pick:   experimentalKey("syntaxHighlighting")},
	{name: "commands", detail: "adds slash commands (prompt text)",
		pick: func(m Manifest) json.RawMessage { return m.Commands }, paths: true,
		def: "commands", shape: defaultDir},
	{name: "agents", detail: "adds sub-agent definitions",
		pick: func(m Manifest) json.RawMessage { return m.Agents }, paths: true,
		def: "agents", shape: defaultDir},
	{name: "outputStyles", detail: "adds output styles",
		pick: func(m Manifest) json.RawMessage { return m.OutputStyles }, paths: true,
		def: "output-styles", shape: defaultDir},
	{name: "themes", detail: "adds color themes",
		pick: Manifest.themes, paths: true, def: "themes", shape: defaultDir},
	{name: "agent", detail: "runs the main session as one of its agents",
		pick: settingKey("agent"), def: "settings.json", shape: defaultKeyInFile, merges: true},
}

// settingKey picks the manifest's `settings.<key>`.
func settingKey(key string) func(Manifest) json.RawMessage {
	return func(m Manifest) json.RawMessage { return m.setting(key) }
}

// experimentalKey picks the manifest's `experimental.<key>`.
func experimentalKey(key string) func(Manifest) json.RawMessage {
	return func(m Manifest) json.RawMessage { return m.experimental(key) }
}

// Components returns the non-skill components this plugin carries, in a stable order: each
// one any of its manifests declares (see manifestDirs), and each one sitting at the default
// location Claude Code loads it from (see components). Inferring from the filesystem is not
// crying wolf: a hooks/hooks.json with no manifest entry runs, which `claude plugin validate`
// shows by listing its hooks. What is reported is only what would load — a hooks/ directory without
// hooks.json, an empty bin/, a default a replacing field overrides — so a prose plugin stays
// unflagged.
func (p *Plugin) Components() []Component {
	var out []Component
	manifests := p.manifests()
	for _, c := range components {
		var sources []string
		if c.pick != nil {
			for _, fm := range manifests {
				if declared(c.pick(fm.m)) {
					sources = append(sources, fm.rel)
				}
			}
		}
		// A replacing key overrides its default only for a tool whose manifest sets it, so the
		// default still loads unless EVERY manifest a tool might read sets it.
		replaced := !c.merges && len(sources) == len(manifests)
		if c.def != "" && !replaced && defaultPresent(p.Dir, c.def, c.shape, c.name) {
			sources = append(sources, c.def)
		}
		if len(sources) == 0 {
			continue
		}
		out = append(out, Component{Name: c.name, Detail: c.detail, RunsCode: c.runsCode,
			Sources: sources})
	}
	return out
}

// defaultPresent reports whether a component's default location holds something Claude Code
// would load. Stat, not Lstat: the tools follow a symlinked file or directory, so a disclosure
// that did not would miss what they load.
func defaultPresent(dir, rel string, shape defaultShape, key string) bool {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if shape == defaultFile {
		return !fi.IsDir()
	}
	if shape == defaultKeyInFile {
		data, err := os.ReadFile(path)
		var obj map[string]json.RawMessage
		return err == nil && json.Unmarshal(trimBOM(data), &obj) == nil && declared(obj[key])
	}
	if !fi.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if shape == defaultDir {
			return true
		}
		if sub, err := os.Stat(filepath.Join(path, e.Name())); err == nil && !sub.IsDir() {
			return true
		}
	}
	return false
}

// RunsCode reports whether any component it carries starts a process or runs a script.
func (p *Plugin) RunsCode() bool {
	for _, c := range p.Components() {
		if c.RunsCode {
			return true
		}
	}
	return false
}

// SkillRoots resolves the manifest's `skills` paths to absolute directories, plus a problem
// per path that escapes the plugin dir.
//
// The resolution mirrors what the tools do, because a divergence here means yolo delivers a
// different skill set than the tool would load from the same tree:
//
//	absent            → <dir>/skills
//	"x" or ["x","y"]  → ONLY those paths
//	{paths:[…]}       → <dir>/skills PLUS those paths
//	{paths:[…],exclusive:true} → only those paths
//
// With ONE evidence-driven exception: a list naming the plugin root itself (`skills: ["./"]`,
// which is what the real scaffolder emits) also gets the default `skills/` dir. Verified
// against a scaffolded plugin, where that single manifest yields BOTH the root's own skill
// (`/<plugin>`) and a nested one (`/<plugin>:<skill>`) — so reading the list as strictly
// exclusive there would drop every nested skill of the most common layout there is.
//
// An escaping path is refused rather than clamped: `skills: ["../../.ssh"]` in someone
// else's repo must not become a directory yolo copies into the user's home.
func (p *Plugin) SkillRoots() (roots []string, problems []string) {
	def := filepath.Join(p.Dir, SkillsSubdir)
	paths, exclusive, form := pathSpec(p.Manifest.Skills)
	switch form {
	case specAbsent:
		return []string{def}, nil
	case specObject:
		if !exclusive {
			roots = append(roots, def)
		}
	}
	selfReferential := false
	for _, rel := range paths {
		abs := filepath.Join(p.Dir, strings.TrimPrefix(rel, "./"))
		if !inside(p.Dir, abs) {
			problems = append(problems, "skills path "+rel+" escapes the plugin directory")
			continue
		}
		if filepath.Clean(abs) == filepath.Clean(p.Dir) {
			selfReferential = true
		}
		roots = append(roots, abs)
	}
	if selfReferential {
		roots = append(roots, def)
	}
	return dedupe(roots), problems
}

// SkillDirs resolves the plugin's skill roots down to individual skills: {invocation name
// -> source dir}. This is what a TIER-B delivery writes, since a flat skills dir has no way
// to carry the plugin itself.
//
// The two shapes come straight from how the tools discover: a root that IS a skill (it has
// a SKILL.md) counts as one, otherwise its immediate subdirectories carrying a SKILL.md do.
// Requiring SKILL.md is what keeps `.claude-plugin/`, `hooks/` and `agents/` out of a flat
// delivery — they are not skills, and the caller refuses them by name instead.
func (p *Plugin) SkillDirs() map[string]string {
	out := map[string]string{}
	roots, _ := p.SkillRoots()
	for _, root := range roots {
		if hasSkillManifest(root) {
			// A root that is itself a skill invokes under the PLUGIN's name when the root
			// is the plugin dir (the `skills: ["./"]` shape), which is the identity the
			// tools would have used for it.
			name := filepath.Base(root)
			if filepath.Clean(root) == filepath.Clean(p.Dir) {
				name = p.Name()
			}
			out[name] = root
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // a declared-but-absent skills dir is normal
		}
		for _, e := range entries {
			sub := filepath.Join(root, e.Name())
			// Stat, not the DirEntry: a symlinked skill dir is legitimate (the tools
			// follow them) and an Lstat-based IsDir would drop it.
			if fi, err := os.Stat(sub); err != nil || !fi.IsDir() {
				continue
			}
			if hasSkillManifest(sub) {
				out[e.Name()] = sub
			}
		}
	}
	return out
}

// ComponentPaths returns every absolute path inside the plugin that belongs to the PLUGIN
// MACHINERY rather than to a plain skill: its manifest dirs and every component path it
// declares (plus each component's default location, since a tool falls back to those).
//
// It exists for one narrow but sharp case. When a plugin's root is itself a skill — the
// `skills: ["./"]` layout the real scaffolder emits — a flat destination has to deliver that
// root as an ordinary skill directory, and a naive recursive copy of it drags the ENTIRE
// plugin along: manifest, hooks, agents, everything the flat path just refused by name. That
// turns the refusal into a cosmetic message while the components land anyway, which is worse
// than either delivering or refusing honestly.
//
// Driven by the manifest, because the point is to exclude what THIS plugin says its components
// are, and by the components table's default locations, the same table Components reads, so
// what a flat delivery refuses by name and what it keeps out of the copy cannot disagree.
func (p *Plugin) ComponentPaths() []string {
	var out []string
	for _, sub := range manifestDirs {
		if sub == "." {
			out = append(out, filepath.Join(p.Dir, manifestName))
			continue
		}
		out = append(out, filepath.Join(p.Dir, filepath.FromSlash(sub)))
	}
	// Every component's declared paths plus its conventional default DIRECTORY NAMES. Both
	// spellings, because a manifest field is camelCase (`outputStyles`) while the directory
	// beside it is conventionally kebab-case (`output-styles/`) — guessing only the field name
	// missed the real directory, which a running test caught. A component declared as an inline
	// object (hooks as a map, say) has no path to exclude at all: its content lives in the
	// manifest, already excluded above.
	exclude := func(field string, raw json.RawMessage) {
		out = append(out, filepath.Join(p.Dir, field), filepath.Join(p.Dir, kebab(field)))
		paths, _, _ := pathSpec(raw)
		for _, rel := range paths {
			abs := filepath.Join(p.Dir, strings.TrimPrefix(rel, "./"))
			// A path resolving to the plugin root is the self-reference, not a component dir;
			// excluding it would exclude everything.
			if filepath.Clean(abs) == filepath.Clean(p.Dir) || !inside(p.Dir, abs) {
				continue
			}
			out = append(out, abs)
		}
	}
	manifests := p.manifests()
	for _, fm := range manifests {
		exclude("skills", fm.m.Skills)
	}
	for _, c := range components {
		// Each component's DEFAULT location too, by its top-level entry (`hooks/` for
		// hooks/hooks.json, `.mcp.json`, `bin/`): it is plugin machinery whether or not the
		// manifest names it, and Components reports it as refused on a flat destination.
		if c.def != "" {
			top, _, _ := strings.Cut(c.def, "/")
			out = append(out, filepath.Join(p.Dir, top))
		}
		// Only a field whose value is paths: a setting's value is a name, and read as a path it
		// would drop the root skill's own folder of that name from the copy.
		if c.pick != nil && c.paths {
			for _, fm := range manifests {
				exclude(c.name, c.pick(fm.m))
			}
		}
	}
	return dedupe(out)
}

// Load reads the plugin manifests in dir, returning ok=false when dir is not plugin-shaped.
// The first manifest in manifestDirs order that parses is the primary one, the one Copilot
// reads; every other one that parses is kept, because Claude Code may read it instead.
//
// A tree whose every manifest is malformed reads as NOT a plugin rather than as an error, and
// that direction is deliberate: the caller's alternative is to fail an entire pack load over a
// file it only consults to be generous. The tree still stages as ordinary content, and the
// tools themselves log the parse failure — where the plugin's author can act on it. One
// malformed manifest beside a sound one does not unmake the plugin: Copilot skips it and reads
// the next, and Claude Code never reads a root one, so treating the tree as ordinary content
// would copy a loadable plugin whole into a flat skills dir with nothing refused.
func Load(dir string) (*Plugin, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	var found []foundManifest
	primary := ""
	for _, sub := range manifestDirs {
		path := filepath.Join(dir, sub, manifestName)
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(trimBOM(data), &m); err != nil {
			continue
		}
		if primary == "" {
			primary = path
		}
		found = append(found, foundManifest{rel: filepath.ToSlash(filepath.Join(sub, manifestName)), m: m})
	}
	if len(found) == 0 {
		return nil, false
	}
	absManifest, err := filepath.Abs(primary)
	if err != nil {
		absManifest = primary
	}
	return &Plugin{Dir: abs, ManifestPath: absManifest, Manifest: found[0].m, others: found[1:]}, true
}

// trimBOM drops a leading UTF-8 byte order mark, which Claude Code strips before it parses a
// manifest and encoding/json refuses.
func trimBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
}

// ManifestPath returns the plugin manifest inside dir and whether one exists, searching
// manifestDirs in order.
func ManifestPath(dir string) (string, bool) {
	for _, sub := range manifestDirs {
		p := filepath.Join(dir, sub, manifestName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Discover returns the plugin trees a pack carries, scanning the CONVENTIONAL skills dir —
// the right entry for a caller that holds only a path. A caller that also holds the pack's
// manifest should use DiscoverIn, so a `skills` contribution's `from` is honored here too.
func Discover(packRoot string) []*Plugin { return DiscoverIn(packRoot, nil) }

// DiscoverIn returns the plugin trees a pack carries, in a deterministic order: the pack ROOT
// if it is itself plugin-shaped, then each immediate child of each skills dir that is.
//
// skillsDirs are absolute directories to scan (what a `skills` contribution's `from`
// resolves to); nil means the conventional <packRoot>/skills. It is a LIST because a pack may
// declare several skills contributions, and honoring `from` on only the first would be the
// same silent-ignore bug one contribution over.
//
// Those two layouts, and not an arbitrary-depth walk, because depth would find a plugin
// vendored inside a skill's test fixtures and deliver it — a surprise, and one that arrives
// with hooks. Both supported layouts are legible from `ls`:
//
//	<pack>/<skills dir>/<plugin>/.claude-plugin/plugin.json   portable: the plugin rides the
//	                                                   skills source it sits in
//	<pack>/.claude-plugin/plugin.json                  wrap-in-place: the pack root IS the
//	                                                   plugin, carried by every skills
//	                                                   source of the pack
//
// Both notches deliver both through one writer, hostskills' (docs/plans/notch-convergence.md
// #OQ-NC11): a jail used to copy a pack's skills subtree flat, so a wrap-in-place plugin never
// reached it and a portable one arrived as a bare directory whatever the pack's tier.
func DiscoverIn(packRoot string, skillsDirs []string) []*Plugin {
	var out []*Plugin
	if p, ok := Load(packRoot); ok {
		out = append(out, p)
	}
	if len(skillsDirs) == 0 {
		skillsDirs = []string{filepath.Join(packRoot, SkillsSubdir)}
	}
	seen := map[string]bool{}
	for _, skillsDir := range skillsDirs {
		entries, err := os.ReadDir(skillsDir)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			sub := filepath.Join(skillsDir, name)
			if fi, err := os.Stat(sub); err != nil || !fi.IsDir() {
				continue
			}
			// Deduped by resolved path: two contributions may name one source dir (the
			// same skills delivered to two agents), and a plugin found twice would be
			// delivered twice and collide with itself in pluginNameCollisions.
			if c := filepath.Clean(sub); seen[c] {
				continue
			} else {
				seen[c] = true
			}
			if p, ok := Load(sub); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// Contains reports whether path is dir or lives under it. Used by delivery to keep a
// plugin's own subtree out of the ordinary skill set, so a wrapped plugin is not delivered
// twice — once as a plugin and once as a pile of loose skills.
func Contains(dir, path string) bool {
	return inside(dir, path) || filepath.Clean(dir) == filepath.Clean(path)
}

// specForm distinguishes the three shapes a path-bearing manifest field takes, because they
// resolve differently (see SkillRoots).
type specForm int

const (
	specAbsent specForm = iota
	specList            // a bare string or array: those paths and nothing else
	specObject          // {paths, exclusive}: the default dir too, unless exclusive
)

// pathSpec normalizes a path-bearing manifest field. An unparseable value reads as absent,
// which resolves to the default directory — the same thing the tools do with it.
func pathSpec(raw json.RawMessage) (paths []string, exclusive bool, form specForm) {
	if !declared(raw) {
		return nil, false, specAbsent
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, false, specList
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, false, specList
	}
	var obj struct {
		Paths     []string `json:"paths"`
		Exclusive bool     `json:"exclusive"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Paths) > 0 {
		return obj.Paths, obj.Exclusive, specObject
	}
	return nil, false, specAbsent
}

// declared reports whether a manifest field carries a real value (present and not null).
func declared(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

// hasSkillManifest reports whether dir is a skill (the shape every one of these tools
// reads: a directory containing SKILL.md).
func hasSkillManifest(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !fi.IsDir()
}

// inside reports whether path is strictly under root.
func inside(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// kebab lowers a camelCase manifest field to the kebab-case directory name the same component
// conventionally uses on disk ("outputStyles" -> "output-styles").
func kebab(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		c := filepath.Clean(s)
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

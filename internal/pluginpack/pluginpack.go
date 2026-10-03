// Package pluginpack recognizes an EXISTING agent plugin — a directory carrying a
// `.claude-plugin/plugin.json` manifest — sitting inside a yolo pack, so a user can pull
// one in as a pack instead of hand-translating it.
//
// The design constraint that shapes this whole package: yolo READS the fields it needs and
// passes the tree through intact. It deliberately does NOT re-implement the plugin schema.
// A plugin manifest declares more than yolo models — hooks, MCP servers, LSP servers,
// sub-agents, output styles, commands — and lowering those into yolo's own contribution
// kinds would silently drop every one yolo has no kind for, then need a new lowering rule
// each time the plugin schema grows. So the decode below is LENIENT (no
// DisallowUnknownFields, unlike packdecl's own manifest): an unknown field is somebody
// else's feature travelling through, not an authoring mistake yolo should report.
//
// What yolo does need from the manifest is exactly two things:
//
//   - the NAME, because it is the destination directory AND the namespace the tools
//     qualify the plugin's skills with (`<name>:<skill>`);
//   - WHICH COMPONENTS it carries, because some of them are CODE THAT RUNS. A plugin is
//     someone else's repo, and hooks, MCP and LSP servers, monitors and `bin/` executables
//     mean processes started on the user's behalf. Those are what the footprint's ⚠ RUNS
//     CODE line reports (packload.FootprintOf), and a component yolo failed to notice would
//     be hooks nobody was told about. They were an APPROVAL question until OQ-TP9 deleted the
//     prompt (docs/design/trust-paths.md, 2026-09-04); they are a DISCLOSURE question now.
//
// "Carries" is the manifest AND the filesystem. Claude Code gives every component a default
// location it loads when the manifest is silent (hooks/hooks.json, .mcp.json, .lsp.json,
// monitors/monitors.json, bin/, …: the "Standard layout" table of
// https://code.claude.com/docs/en/plugins-reference), so a manifest-only reading misses code
// that runs. That reading is the one thing here that is not a pass-through: the defaults are
// listed in the components table below, beside the fields they stand in for.
//
// It is dependency-free on the rest of the repo, for the same reason packdecl is: both the
// host CLI (footprint, `pack init`) and the host renderer read it.
package pluginpack

import (
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
// hooks were never surfaced for approval. Which one was found is recorded (Plugin.Manifest
// Path) so delivery writes its ownership marker back into the same file.
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

	// Monitors is the top-level spelling of a plugin's background monitors, which Claude Code
	// still loads with a `claude plugin validate` warning; Experimental carries the current
	// one, `experimental.monitors`. Experimental stays raw for the reason every field here
	// does: typing it would make a manifest with any other shape there unreadable, and an
	// unreadable manifest is not a plugin at all, so its hooks would be disclosed nowhere.
	Monitors     json.RawMessage `json:"monitors"`
	Experimental json.RawMessage `json:"experimental"`
}

// monitors is the declared monitors value: `experimental.monitors` when present, else the
// top-level `monitors`.
func (m Manifest) monitors() json.RawMessage {
	var exp struct {
		Monitors json.RawMessage `json:"monitors"`
	}
	if declared(m.Experimental) && json.Unmarshal(m.Experimental, &exp) == nil &&
		declared(exp.Monitors) {
		return exp.Monitors
	}
	return m.Monitors
}

// Plugin is one recognized plugin tree.
type Plugin struct {
	// Dir is the absolute plugin root — the directory that gets copied.
	Dir string
	// ManifestPath is the absolute manifest file found (one of manifestDirs).
	ManifestPath string
	// Manifest is what it declared.
	Manifest Manifest
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
	// Name is the manifest field ("hooks", "mcpServers", …), or "bin" for the executables
	// directory, which has no field.
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
)

// components is the closed description of what yolo reports per component, with the
// code-running verdict attached. `skills` is absent on purpose: it is the one component
// yolo models itself, so it is delivered rather than reported as a pass-through.
//
// The order is the report's: code first, then prose. Two launches of one pack print the same
// line only because this order is fixed.
//
// def and merges are Claude Code's, verified against its plugin reference
// (https://code.claude.com/docs/en/plugins-reference, "Standard layout" and "How each key
// combines with its default location", read 2026-10-03) and its changelog:
//
//   - hooks/hooks.json, .mcp.json and .lsp.json MERGE with the manifest field: the default
//     file loads first, then what the manifest declares. hooks/hooks.json also carries the
//     `modules` key of a JavaScript or TypeScript hooks module (Claude Code 2.1.287's mods),
//     which runs inside the agent itself.
//   - monitors/monitors.json, commands/, agents/ and output-styles/ are REPLACED by their field:
//     a manifest that sets one loads its paths and not the default.
//   - bin/ has no field: "Files in bin/ at the plugin root are on the PATH of the Bash tool's
//     shell while the plugin is enabled" (changelog 2.1.91: "Plugins can now ship executables
//     under bin/ and invoke them as bare commands from the Bash tool").
var components = []struct {
	name     string
	detail   string
	runsCode bool
	// pick is the manifest field's value; nil when the component has no field.
	pick func(Manifest) json.RawMessage
	// def is the plugin-relative default location, slash-separated; "" for none.
	def   string
	shape defaultShape
	// merges says the default loads beside a declaration rather than being replaced by it.
	merges bool
}{
	{"hooks", "runs code at agent lifecycle events", true,
		func(m Manifest) json.RawMessage { return m.Hooks }, "hooks/hooks.json", defaultFile, true},
	{"mcpServers", "starts MCP server processes", true,
		func(m Manifest) json.RawMessage { return m.MCPServers }, ".mcp.json", defaultFile, true},
	{"lspServers", "starts language server processes", true,
		func(m Manifest) json.RawMessage { return m.LSPServers }, ".lsp.json", defaultFile, true},
	{"monitors", "runs background shell commands for the whole session", true,
		Manifest.monitors, "monitors/monitors.json", defaultFile, false},
	{"bin", "puts executables on the agent's shell PATH", true,
		nil, "bin", defaultExecDir, false},
	{"commands", "adds slash commands (prompt text)", false,
		func(m Manifest) json.RawMessage { return m.Commands }, "commands", defaultDir, false},
	{"agents", "adds sub-agent definitions", false,
		func(m Manifest) json.RawMessage { return m.Agents }, "agents", defaultDir, false},
	{"outputStyles", "adds output styles", false,
		func(m Manifest) json.RawMessage { return m.OutputStyles }, "output-styles", defaultDir, false},
}

// Components returns the non-skill components this plugin carries, in a stable order: each
// one its manifest declares, and each one sitting at the default location Claude Code loads
// it from (see components). Inferring from the filesystem is not crying wolf: a
// hooks/hooks.json with no manifest entry runs, which `claude plugin validate` shows by
// listing its hooks. What is reported is only what would load — a hooks/ directory without
// hooks.json, an empty bin/, a default a replacing field overrides — so a prose plugin stays
// unflagged.
func (p *Plugin) Components() []Component {
	var out []Component
	for _, c := range components {
		var sources []string
		isDeclared := c.pick != nil && declared(c.pick(p.Manifest))
		if isDeclared {
			sources = append(sources, p.ManifestRel())
		}
		if c.def != "" && (c.merges || !isDeclared) && defaultPresent(p.Dir, c.def, c.shape) {
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
func defaultPresent(dir, rel string, shape defaultShape) bool {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if shape == defaultFile {
		return !fi.IsDir()
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
	exclude("skills", p.Manifest.Skills)
	for _, c := range components {
		// Each component's DEFAULT location too, by its top-level entry (`hooks/` for
		// hooks/hooks.json, `.mcp.json`, `bin/`): it is plugin machinery whether or not the
		// manifest names it, and Components reports it as refused on a flat destination.
		if c.def != "" {
			top, _, _ := strings.Cut(c.def, "/")
			out = append(out, filepath.Join(p.Dir, top))
		}
		if c.pick != nil {
			exclude(c.name, c.pick(p.Manifest))
		}
	}
	return dedupe(out)
}

// Load reads the plugin manifest in dir, returning ok=false when dir is not plugin-shaped.
//
// A malformed manifest reads as NOT a plugin rather than as an error, and that direction is
// deliberate: the caller's alternative is to fail an entire pack load over a file it only
// consults to be generous. The tree still stages as ordinary content, and the tools
// themselves log the parse failure — where the plugin's author can act on it.
func Load(dir string) (*Plugin, bool) {
	path, ok := ManifestPath(dir)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	absManifest, err := filepath.Abs(path)
	if err != nil {
		absManifest = path
	}
	return &Plugin{Dir: abs, ManifestPath: absManifest, Manifest: m}, true
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

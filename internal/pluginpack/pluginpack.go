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
//   - WHICH COMPONENTS are declared, because some of them are CODE THAT RUNS. A plugin is
//     someone else's repo, and `hooks`/`mcpServers`/`lspServers` mean processes started on
//     the user's behalf. Those are what the footprint's ⚠ RUNS CODE line reports
//     (packload.FootprintOf), and a manifest yolo failed to notice would be hooks nobody
//     was told about. They were an APPROVAL question until OQ-TP9 deleted the prompt
//     (docs/design/trust-paths.md, 2026-09-04); they are a DISCLOSURE question now.
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

// Component is one thing a plugin manifest declares, as yolo reports it.
type Component struct {
	// Name is the manifest field ("hooks", "mcpServers", …).
	Name string
	// Detail is a one-line human note for the footprint and refusal lines.
	Detail string
	// RunsCode marks a component that starts a process or executes a script on the
	// user's behalf. These are the ones the install approval gates.
	RunsCode bool
}

// components is the closed description of what yolo reports per manifest field, with the
// code-running verdict attached. `skills` is absent on purpose: it is the one component
// yolo models itself, so it is delivered rather than reported as a pass-through.
var components = []struct {
	name     string
	detail   string
	runsCode bool
	pick     func(Manifest) json.RawMessage
}{
	{"hooks", "runs code at agent lifecycle events", true,
		func(m Manifest) json.RawMessage { return m.Hooks }},
	{"mcpServers", "starts MCP server processes", true,
		func(m Manifest) json.RawMessage { return m.MCPServers }},
	{"lspServers", "starts language server processes", true,
		func(m Manifest) json.RawMessage { return m.LSPServers }},
	{"commands", "adds slash commands (prompt text)", false,
		func(m Manifest) json.RawMessage { return m.Commands }},
	{"agents", "adds sub-agent definitions", false,
		func(m Manifest) json.RawMessage { return m.Agents }},
	{"outputStyles", "adds output styles", false,
		func(m Manifest) json.RawMessage { return m.OutputStyles }},
}

// Components returns the non-skill components this plugin declares, in a stable order.
// Nothing is inferred from the filesystem: a `hooks/` directory with no manifest entry is
// inert to the tools, so reporting it would cry wolf.
func (p *Plugin) Components() []Component {
	var out []Component
	for _, c := range components {
		if !declared(c.pick(p.Manifest)) {
			continue
		}
		out = append(out, Component{Name: c.name, Detail: c.detail, RunsCode: c.runsCode})
	}
	return out
}

// RunsCode reports whether any declared component starts a process or runs a script.
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
// Driven by the manifest rather than by a hardcoded dir list, because the point is to exclude
// what THIS plugin says its components are, not what a plugin usually calls them.
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
	for field, raw := range map[string]json.RawMessage{
		"skills": p.Manifest.Skills, "commands": p.Manifest.Commands,
		"agents": p.Manifest.Agents, "hooks": p.Manifest.Hooks,
		"mcpServers": p.Manifest.MCPServers, "lspServers": p.Manifest.LSPServers,
		"outputStyles": p.Manifest.OutputStyles,
	} {
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
	// `.mcp.json` is the conventional sibling file an mcpServers entry points at, and it is
	// plugin machinery wherever it is named from.
	out = append(out, filepath.Join(p.Dir, ".mcp.json"))
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
//	<pack>/<skills dir>/<plugin>/.claude-plugin/plugin.json   portable: the jail's flat skills
//	                                                   merge lands it at the same path the
//	                                                   host render writes, so one layout
//	                                                   works at both notches
//	<pack>/.claude-plugin/plugin.json                  wrap-in-place: the pack root IS the
//	                                                   plugin. Host-only — a jail delivers
//	                                                   the pack's skills subtree, never its
//	                                                   root, so the manifest never arrives.
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

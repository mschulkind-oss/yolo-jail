package jailcontent

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
	"github.com/mschulkind-oss/yolo-jail/internal/treesync"
)

// PrepareSkills stages per-agent skills dirs on the host for :ro bind mounting.
// Each pack-declared destination's staging dir gets the built-in skill suite
// (builtinskills.FS) plus every selected pack's skills. Returns the staging
// directory (AGENTS_DIR/<cname>).
// CRITICAL: each skills_dir is SYNCED to its new content (prepareSkillTarget) — the dir itself
// is NEVER rmtree+mkdir'd, because a running jail's bind mount captured its inode and a fresh
// inode would silently detach attach-time refreshes, and an unchanged skill is never touched.
// packSkillDirs are the per-pack `skills/` sources to layer in, in config
// order (C3). Threaded as a package-level var rather than a parameter so the
// existing four-arg signature and its callers stay untouched; the CLI sets it once
// per run, before PrepareSkills.
//
// Precedence within one agent's staging dir: the WORKSPACE < built-ins < packs, with the
// CONVENTIONAL LOCAL PACK last among the packs (config.LoadPacks appends it there).
// So a pack may override a built-in — a legitimate reason to ship one — and the
// user's own copy is composed last. The workspace layer (workspaceskills.go,
// PrepareSkillsWith) only fills names nothing above took. There is no layer reading a
// destination back in: that one was S3's defect, and the workspace layer reads sources.
//
// TWO PACKS MAY NOT SHIP ONE NAME (docs/plans/notch-convergence.md#OQ-NC11, ruled 2026-09-28 by
// parity): the pack layers are the HOST's layer plan, composed by hostskills.ComposeInto, so two
// packs claiming one unnamespaced name at one destination refuse the launch — the local pack
// included, as at the host — instead of the later one winning silently. "The local pack last" is
// therefore an order among names that do not collide, and the override it used to buy is spelled
// with a rename or `skills_tier: "namespaced"`, the host's two remedies.
//
// IT CARRIES AN AUDIENCE PER SOURCE since the audience selector (docs/reference/agent-briefings.md#audiences-what-varies-per-destination), where it was a flat
// []string. A flat list was the `skills` half of the same defect the briefing half had: the
// list is GLOBAL — every selected pack's skills reach every destination — so a source that
// arrived as a bare path had no way to say who it was for, and a claude-specific skill was
// copied into ~/.pi/agent/skills with nothing able to stop it.
var packSkillDirs []PackSkillSource

// PackSkillSource is one skills SOURCE to layer in, the audience it names, and the pack-level
// facts the host's layer writer needs to deliver it as the host would.
//
// A re-declaration of packload.SkillsSource rather than a use of it, and that is not
// duplication for its own sake: this record is filled once per launch by the CLI, which holds the
// packs, and read by staging passes that do not (an attach adopts the running jail's packs into
// it). Dir and Agents are deliberately packload's own so the conversion is a copy with nothing to
// get wrong; the rest is hostskills.PackLayer's, the one constructor the host composition reads a
// pack through too (run.jailSkillSources fills it).
type PackSkillSource struct {
	// Dir is the absolute source directory to copy skill subdirs from. EMPTY MEANS NO SOURCE: the
	// record carries the pack's root plugins alone (Plugins), which sit inside none of its sources
	// and reach a jail whether or not the pack has any (run.jailSkillSources).
	Dir string
	// Agents is the audience this source names. EMPTY MEANS BROADCAST (P2) — every pack that
	// ships today, and the only thing a pack with no pack.json can ask for.
	Agents []string
	// Pack is the name of the pack this source belongs to. At a namespaced tier it is the
	// subtree's name and the invocation namespace; it is the unit a collision names; and a
	// workspace skill this source shadows is disclosed as shadowed BY this pack. "" reads as "a
	// pack" and makes every such source a layer of its own.
	Pack string
	// Tier is the PACK's skills_tier (hostskills.PackTier) — the same for every source of one
	// pack, because a tier decides what a skill is CALLED (S2). The zero value is flat.
	Tier hostskills.Tier
	// Description goes into a namespaced subtree's plugin manifest.
	Description string
	// Plugins are the wrapped plugin trees this pack carries INSIDE Dir, or at the pack root when
	// Dir is empty, delivered as the host delivers them: verbatim at a namespaced tier, their
	// skills alone (and every other component named as refused) at a flat one.
	Plugins []*pluginpack.Plugin
	// SourceOf maps a path under Dir to the file the user edits, for a collision message: the
	// launch reads a STAGED copy of every pack. Nil is identity.
	SourceOf func(string) string
}

// SetPackSkillDirs sets the pack skills sources consulted by the next PrepareSkills.
// Passing nil clears them.
func SetPackSkillDirs(sources []PackSkillSource) { packSkillDirs = sources }

// PackSkillDirs returns what SetPackSkillDirs last set — the SOURCES the next
// PrepareSkills will copy from. Exported so the CLI's own tests can assert which dir a
// pack's `skills` contribution resolved to, which is otherwise observable only by
// inspecting the staged output after a full PrepareSkills run.
func PackSkillDirs() []PackSkillSource { return packSkillDirs }

// SkillTarget is one pack-declared skills destination: which staging dir to build, and
// the jail path it will be mounted at.
type SkillTarget struct {
	// Staging is the staging subdir name (per PACK, so two packs cannot collide).
	Staging string
	// Dest is the home-relative jail path the staged dir is mounted at.
	Dest string
	// Agent is the IDENTITY the declaring pack gave this destination — the launcher command
	// whose agent reads it — or "" for a destination that declared none.
	//
	// "" is not an error: it means no `agents` selector can name this destination, which is
	// the state every pack.json was in before the field existed (R4). Such a destination
	// still receives every broadcast source; it just cannot be addressed.
	Agent string
	// ProjectDirs is where this destination's agent reads skills at PROJECT scope — the
	// destination contribution's `project_dirs`, workspace-relative. It is the SKIP RULE of the
	// workspace layer (docs/reference/agent-briefings.md): a workspace source directory this
	// list resolves to is one the agent already reads natively, so this destination gets no
	// copy of it. Empty receives every workspace source — the broadcast reading, as for Agent.
	ProjectDirs []string
	// Reserved names children of this destination no layer may compose — the destination
	// contribution's `reserved` (docs/reference/pack-system.md, OQ-ST2). packs/claude reserves
	// `synced`, Claude Code's sync root. ReservedNotes says what each one is, in the owning pack's
	// words, for the line that says it was withheld.
	Reserved      []string
	ReservedNotes map[string]string

	// A HostSource FIELD USED TO LIVE HERE, naming "the user's OWN skills tree to layer in
	// last" — and it was set to the DESTINATION (run.packSkillTargets), i.e. the host's own
	// copy of the very path this staging dir gets mounted over. That was right while the
	// destination held loose user files and became
	// circular once `yolo host apply` COMPOSED it: a jail read yolo's own generated output back
	// in as "the user's tree", and since the local pack is an ordinary pack entry its content
	// arrived twice by two paths (roadmap.md S3).
	//
	// There is nothing to replace it with, because the slot it described already has a home:
	// the CONVENTIONAL LOCAL PACK. config.LoadPacks appends it LAST, so its skills are an
	// ordinary layer of the plan below, reached by the same route every other pack's content
	// takes.
}

// packSkillTargets are the destinations PrepareSkills builds. Set per run by the CLI
// from pack declarations; nil means none, which is why a jail with no packs stages
// nothing rather than inventing a destination.
var packSkillTargets []SkillTarget

// SetPackSkillTargets sets the pack-declared skills destinations for the next
// PrepareSkills call.
func SetPackSkillTargets(targets []SkillTarget) { packSkillTargets = targets }

// PackSkillTargets returns what SetPackSkillTargets last set.
//
// Exported for the reason PackSkillDirs is, plus one the run pipeline discovered the hard
// way: one process can run more than one launch (auto-capture runs the ordinary pipeline
// for its throwaway capture jail), so a launch has to put back whatever its caller had —
// and it cannot put back a record it has no way to read.
func PackSkillTargets() []SkillTarget { return packSkillTargets }

// SkillStagingName is the staging subdir for one pack's skills.
func SkillStagingName(pack string) string { return "skills-" + pack }

// SkillPlan is the jail's LAYER PLAN: one hostskills.Destination per target, in target order,
// whose layers are every source addressed to that target's agent, grouped per pack in source
// order. It is the host's plan type, so the host's Collisions reads it and the host's writer
// (hostskills.ComposeInto) composes it.
//
// THE FAN-OUT IS THE JAIL'S, deliberately (OQ-NC11): every selected pack's skills reach every
// destination their audience admits, where the host sends a pack's skills only to the
// destinations it names (packload.ResolveDestinations). Which of the two is right is OQ-S4,
// open, and one plan type does not decide it.
//
// A destination is named "~/<dest>" — the path a message shows the user, which is also the path
// the agent in the jail reads. The staging writes into its own scratch dir, never into Dir.
func SkillPlan(sources []PackSkillSource, targets []SkillTarget) []hostskills.Destination {
	out := make([]hostskills.Destination, 0, len(targets))
	for _, t := range targets {
		d := hostskills.Destination{Dir: "~/" + filepath.ToSlash(t.Dest),
			Reserved: append([]string(nil), t.Reserved...), ReservedNotes: t.ReservedNotes}
		index := map[string]int{}
		for _, src := range sources {
			// THE AUDIENCE FILTER, and it is the whole `skills` half of
			// docs/reference/agent-briefings.md#audiences-what-varies-per-destination. The list is
			// global, so this is the only point at which "who is this content for?" can be asked —
			// and it is asked against the string the DESTINATION declared about itself, never
			// anything derived (OQ-BA2).
			if !sourceAddressesAgent(src.Agents, t.Agent) {
				continue
			}
			i, seen := index[src.Pack]
			if !seen || src.Pack == "" {
				i = len(d.Layers)
				index[src.Pack] = i
				d.Layers = append(d.Layers, hostskills.Layer{Pack: src.Pack, Description: src.Description,
					Tier: src.Tier, SourceOf: src.SourceOf})
			}
			// A record with no Dir carries plugins and nothing to copy: the host's writer delivers
			// a layer's plugins whether or not it has a source (hostskills.writeLayer).
			if src.Dir != "" {
				d.Layers[i].Sources = append(d.Layers[i].Sources, src.Dir)
			}
			// Once per layer, should two records of one pack name one plugin.
			for _, pl := range src.Plugins {
				dup := false
				for _, have := range d.Layers[i].Plugins {
					dup = dup || have.Dir == pl.Dir
				}
				if !dup {
					d.Layers[i].Plugins = append(d.Layers[i].Plugins, pl)
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// SkillCollisionError is the S1 refusal over the jail's whole plan, or nil: hostskills'
// CollisionError, every collision at every destination in one message, so one launch names every
// rename to make. The launch runs it as a pre-flight before any container exists (run.stagePacks),
// and PrepareSkillsWith runs it again before it writes anything.
func SkillCollisionError(sources []PackSkillSource, targets []SkillTarget) error {
	if cols := hostskills.Collisions(SkillPlan(sources, targets)); len(cols) > 0 {
		return hostskills.CollisionError(cols)
	}
	return nil
}

// PACK-DECLARED skills destinations replace the agent list: SetPackSkillTargets is
// what tells this which staging dirs to build, so a pack gets its skills whether or not
// anything calls it an agent.
//
// homeDir and agentNames are both vestigial now and kept for their callers' sake: they existed
// to locate the host's own ~/.<agent>/skills trees, which S3 removed as a layer (see
// SkillTarget). The signature is left alone deliberately — five call sites pass them, and
// churning those would be a bigger diff than the fix, with no behavior in it.
func PrepareSkills(cname, homeDir string, agentNames []string) (string, error) {
	staging, _, err := PrepareSkillsWith(cname, nil)
	return staging, err
}

// PrepareSkillsWith is PrepareSkills with the WORKSPACE as one more source: the lowest layer of
// every destination, never shadowing anything above it (docs/reference/agent-briefings.md). ws nil
// or with no Dirs stages exactly what PrepareSkills always did.
//
// The report is everything the launch must SAY about the composition — what the workspace layer
// delivered, what a higher layer shadowed, which same-named skills two source dirs both carried,
// every entry refused because reading it would have left the workspace, and what the pack layers
// could not deliver or withheld (PackNotices). It is never nil. The caller prints it; this package
// does not own a stream.
//
// A COLLISION IS FATAL BEFORE ANY DESTINATION IS TOUCHED, over the whole plan, for
// hostskills.RenderHostSkills' reason: a per-destination check would compose one staging dir and
// refuse the next.
//
// A parameter rather than one more package-level setter, unlike the pack records above: the
// workspace is per LAUNCH, and a process runs more than one launch (auto-capture, packrecords.go),
// so a record that one launch set and the next forgot to reset would stage the first launch's
// workspace into the second's jail. An argument cannot outlive its call.
func PrepareSkillsWith(cname string, ws *WorkspaceSkills) (string, *WorkspaceSkillsReport, error) {
	if err := SkillCollisionError(packSkillDirs, packSkillTargets); err != nil {
		return "", nil, err
	}
	plan := SkillPlan(packSkillDirs, packSkillTargets)
	staging := filepath.Join(paths.AgentsDir(), cname)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", nil, err
	}
	layer, err := stageWorkspaceLayer(ws)
	if err != nil {
		return "", nil, err
	}
	defer layer.close()

	var notices []PackSkillNotice
	for i, target := range packSkillTargets {
		said, err := prepareSkillTarget(plan[i], target, filepath.Join(staging, target.Staging), layer)
		if err != nil {
			return "", nil, err
		}
		notices = append(notices, said...)
	}
	rep := layer.report()
	rep.PackNotices = groupPackNotices(notices)
	return staging, rep, nil
}

// PackSkillNotice is one line the pack layers need said: an entry the host's writer refused (a
// namespaced delivery downgraded, a wrapped plugin's component a flat destination cannot carry)
// or a reserved child it withheld — once per entry and reason, however many destinations.
type PackSkillNotice struct {
	Name   string   // the entry
	Detail string   // the writer's own reason
	In     []string // the destinations, "~/<dest>", sorted
}

// groupPackNotices folds per-destination notices into one per entry and reason, in first-seen
// order, so a child every destination withholds prints once.
func groupPackNotices(notices []PackSkillNotice) []PackSkillNotice {
	var out []PackSkillNotice
	at := map[[2]string]int{}
	for _, n := range notices {
		k := [2]string{n.Name, n.Detail}
		i, seen := at[k]
		if !seen {
			i = len(out)
			at[k] = i
			out = append(out, PackSkillNotice{Name: n.Name, Detail: n.Detail})
		}
		for _, d := range n.In {
			out[i].In = appendUnique(out[i].In, d)
		}
	}
	for i := range out {
		sort.Strings(out[i].In)
	}
	return out
}

// prepareSkillTarget composes one destination's skills tree and SYNCS it into its staging
// dir, which a podman jail binds at the destination.
//
// COMPOSED ASIDE, THEN SYNCED, and never cleared-and-refilled in place, which is what this did
// until the concurrent-launch fix (docs/reference/pack-system.md#concurrent-launches-of-one-workspace).
// Every invocation stages skills, an attach to a running jail included, and clearing the bound
// dir emptied the live agent's skills until the copy caught up — every skill, on every attach,
// whether or not anything had changed. The layers are built in a private scratch tree in the
// same order as before, so precedence is untouched, and treesync then changes only what
// differs. skillsDir itself is never removed (the bind captured it).
func prepareSkillTarget(dest hostskills.Destination, target SkillTarget, skillsDir string,
	layer *workspaceLayer) ([]PackSkillNotice, error) {
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return nil, err
	}
	scratchRoot, err := os.MkdirTemp("", "yolo-skills-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratchRoot)
	composed := filepath.Join(scratchRoot, "skills")
	if err := os.Mkdir(composed, 0o755); err != nil {
		return nil, err
	}
	notices, err := composeSkillLayers(dest, target, composed, layer)
	if err != nil {
		return nil, err
	}
	_, err = treesync.Sync(composed, skillsDir)
	return notices, err
}

// composeSkillLayers writes one destination's skills layers into skillsDir, in precedence
// order from the top: yolo's own LSP plugin, every pack's skills addressed to this destination,
// yolo's built-in suite, then the WORKSPACE.
//
// THE PACKS ARE WRITTEN FIRST, INTO AN EMPTY DIR, BY THE HOST'S WRITER (hostskills.ComposeInto),
// and every lower layer fills only the names they left free — which is the same precedence the
// old order (built-ins, then packs over them) produced, reached without handing the host's
// writer a directory it did not compose. Built-ins written first would read to that writer as
// entries it has no claim on.
//
// THE WORKSPACE IS WRITTEN LAST AND RANKS FIRST-FROM-THE-BOTTOM, which are the same thing said
// two ways: it fills only the names every layer above it left free, so it can add a skill and
// never replace one (OQ-WS2). Written first and overwritten, it would give the same tree — but
// the report could not say which names were lost or to whom, and a shadow is a disclosure.
func composeSkillLayers(dest hostskills.Destination, target SkillTarget, skillsDir string,
	layer *workspaceLayer) ([]PackSkillNotice, error) {
	// 1. PACK skills, in config order, each at its own tier, wrapped plugins included.
	res, err := hostskills.ComposeInto(dest, skillsDir)
	if err != nil {
		return nil, err
	}
	var notices []PackSkillNotice
	for _, r := range res.Results {
		if r.Action == hostskills.ActionRefused || r.Action == hostskills.ActionReserved {
			notices = append(notices, PackSkillNotice{Name: r.Name, Detail: r.Detail, In: []string{dest.Dir}})
		}
	}
	// taken is every name a layer above the workspace wrote, and who wrote it: the input the
	// workspace layer's shadow disclosure names.
	taken := map[string]string{}
	for name, pack := range res.Taken {
		taken[name] = "a pack's skill"
		if pack != "" {
			taken[name] = "pack " + pack + "'s skill"
		}
	}
	// A RESERVED child is no layer's, including the two below: it is another tool's tree, and a
	// jail composing any copy of it is the adopted-tree leak (docs/reference/pack-system.md, ST-R).
	for _, r := range target.Reserved {
		if _, ok := taken[r]; !ok {
			taken[r] = "a name reserved for another tool's tree"
		}
	}
	// 2. The built-in skill suite, into every name the packs left (every skills-bearing agent
	//    gets it). A pack may override a built-in, a legitimate reason to ship one.
	if err := writeBuiltinSkills(skillsDir, taken); err != nil {
		return nil, err
	}
	for _, name := range builtinSkillNames() {
		if _, ok := taken[name]; !ok {
			taken[name] = "yolo's built-in skill"
		}
	}
	// 3. yolo's OWN LSP plugin, over everything — see writeLSPPlugin for why this is not one of
	//    the layers above and why it must win. Written into every skills destination: Claude is
	//    the agent that reads a plugin, and a destination that is not Claude's simply has a
	//    directory no tool there looks for, which is cheaper than teaching this loop which agent
	//    is which (core knows no agents).
	if err := writeLSPPlugin(skillsDir); err != nil {
		return nil, err
	}
	// The plugin's name is taken whether or not a plugin was written: writeLSPPlugin REMOVES a
	// same-named directory when nothing is configured, so a lowest layer carrying one would have
	// lost it either way — and a workspace must not be able to plant something Claude loads as
	// yolo's LSP declaration.
	taken[LSPPluginDir] = "yolo's LSP plugin"
	// 4. THE WORKSPACE, into whatever names are left.
	return notices, layer.deliver(target, skillsDir, taken)
}

// builtinSkillNames lists the top-level skill directories of the built-in suite.
func builtinSkillNames() []string {
	entries, err := fs.ReadDir(builtinskills.FS, ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// sourceAddressesAgent reports whether a source naming `agents` belongs in the staging dir of
// a destination whose declared identity is `agent`.
//
// The `skills` twin of briefing.go's addressesAgent, and identical by intent: EMPTY IS
// BROADCAST, so a jail of packs that name nobody stages exactly what it did before, and an
// empty `agent` receives every broadcast and no addressed source. It is a second small
// predicate rather than a shared one because the two live in the same package and neither
// imports the other's type — folding them would mean a shared helper over two field lists,
// which is more coupling than the four lines are worth.
func sourceAddressesAgent(agents []string, agent string) bool {
	if len(agents) == 0 {
		return true
	}
	for _, a := range agents {
		if a == agent {
			return true
		}
	}
	return false
}

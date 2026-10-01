package jailcontent

// workspaceskills.go is the WORKSPACE layer of the skills composition — the staged mirror of
// docs/reference/agent-briefings.md, the workspace layer, as ruled on 2026-09-27 (OQ-WS1–WS4).
//
// A repo that commits `.claude/skills/review/` has chosen its readers' agent for them: `claude`,
// `copilot` and `opencode` read that path and `codex`, `pi` and `agy` do not. This layer closes
// that gap without writing a byte into the repo: every project-scope skills directory any agent
// pack declares (the SOURCE SET, OQ-WS3) is copied host-side into every skills destination's
// staging dir, as the LOWEST layer (OQ-WS2), except into a destination whose own agent already
// reads that directory natively (the SKIP RULE, §5).
//
// # P5: nothing outside the workspace is ever read, and that is structural here
//
// The copier the pack layers go through (hostskills' copyTree, the host's own writer since
// OQ-NC11) follows symlinks, correctly: its sources are pack trees packstage already refused
// escaping links in, and yolo's built-ins are embedded. The workspace
// is neither. It is populated by a `git clone` and editable by the agent inside the jail, and this
// code runs ON THE HOST — so `.agents/skills/x/SKILL.md → ~/.ssh/id_ed25519`, committed or planted
// between two attaches, would have been read by the host user and bound into the jail as a skill.
//
// So the workspace has its own reader, confinedTree, and three properties make the refusal hold
// rather than merely be checked:
//
//  1. LINKS ARE RESOLVED LEXICALLY, INSIDE THE ROOT, before anything is opened. resolve reads each
//     link's target through an os.Root opened on the workspace, with the root's own Readlink,
//     and walks it; a target that climbs above the root or is an absolute path outside every
//     spelling of it ends the walk BEFORE anything there is touched — not even an lstat — so an
//     escaping entry is NAMED without the host having read, or even checked the existence of,
//     what it pointed at. What resolve returns is the REAL path the entry stands for, a path with
//     no link in it, and every refusal below is a decision about that path.
//  2. EVERY READ FOLLOWS NO LINK AT ALL. The bytes are read by a walk from the workspace root's
//     own descriptor, one component at a time, every open O_NOFOLLOW (and O_DIRECTORY for a
//     directory): so what is read is exactly the real path that was classified. A component
//     swapped for a link after it was classified fails the walk instead of being followed. An
//     os.Root cannot give this — it follows a link that stays inside it — and that is how a race
//     used to read the host's per-side bytes (a file flipped to a link into node_modules between
//     its classification and its open). A race can make a copy fail; it cannot make one read
//     anything but the path that was checked.
//  3. NOTHING IS OPENED THAT COULD BLOCK. Every open carries O_NONBLOCK (a FIFO planted where a
//     SKILL.md was would otherwise hang the launcher on the next attach, measured) and a directory
//     open carries O_DIRECTORY; a special file is refused by its fstat, after the open.
//
// A refused entry is SKIPPED AND NAMED, never fatal: a clone must not be able to refuse a jail.
// That covers the scratch copy too. An entry whose path inside its skill is longer than
// maxSkillPath, which not every destination could hold, and an entry whose copy could not be
// written, are refused like one that could not be read. The only error this layer returns is a
// failure to create its scratch root, and nothing it made outlives the call that made it.
//
// # The cap: what one launch copies is bounded, whatever the links multiply
//
// Links cost nothing to commit, and a directory is copied once per skill that links it, so the
// scratch copy has a per-launch cap in bytes and in entries (maxWorkspaceSkillBytes and
// maxWorkspaceSkillEntries, OQ-WS7). Every write is charged before it is made. A skill whose
// copy would pass either is refused whole and named; the skills copied before it stay. Every
// destination copies from scratch, so the cap bounds what each of them receives too.
//
// # What else is never read
//
// A path that resolves under `.git` (never content) or `.yolo` (yolo's own state, and reading it
// would make this a layer that reads generated output — the deleted HostSource's defect), and a
// PER-SIDE path: the jail sees its own copy of `.venv`, `node_modules` and every `per_side_paths`
// entry, so the host's bytes there are exactly what the jail cannot read, and the mirror's whole
// justification is that it grants no authority the repo lacks (OQ-WS1's answer).
//
// # What a destination is never sent
//
// A copy of a skill its own agent already reads natively, at either grain (the skip rule), and a
// copy of any skill whose NAME a directory it reads natively carries: a second skill of one name,
// with different content, is the double delivery the skip rule exists to prevent, whichever copy
// won the name among the sources.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// WorkspaceSkills is the workspace's half of one launch's skills composition.
type WorkspaceSkills struct {
	// Root is the workspace root on the host, as the launcher resolved it. Trusted: it is the
	// directory the user ran yolo in, and its own spelling may pass through symlinks.
	Root string
	// Aliases are other absolute spellings of Root that an absolute link INSIDE it may use —
	// the path a container jail mounts the workspace at. A link to `/workspace/.agents/x`
	// written by the agent in the jail names the same directory the agent reads it as, so it is
	// followed as a link into the root. Nothing at an alias on the host is ever read.
	Aliases []string
	// Dirs is the SOURCE SET: workspace-relative skills directories, in precedence order. When
	// two of them carry the same skill name the FIRST wins, everywhere.
	Dirs []string
	// PerSide are workspace-relative paths the jail sees its own copy of. Never read.
	PerSide []string
}

// WorkspaceSkillsReport is everything a launch must say about the workspace layer.
type WorkspaceSkillsReport struct {
	// Mirrored is one entry per source dir that delivered anything, in source order.
	Mirrored []WorkspaceSkillDelivery
	// Shadowed is one entry per workspace skill a higher layer took the name of, in some
	// destination. One line per NAME, however many destinations it was shadowed in (OQ-WS2).
	Shadowed []WorkspaceSkillShadow
	// Collisions is one entry per skill name two source dirs both carry, with different
	// content.
	Collisions []WorkspaceSkillCollision
	// HeldBack is one entry per skill a destination was not sent because a directory its agent
	// reads natively carries a skill of the same name — when no collision line already says so.
	HeldBack []WorkspaceSkillHeldBack
	// Competing is one entry per workspace skill an agent reads NATIVELY under a name a higher
	// layer also delivers to it. The mirror can hold its own copy back; it cannot stop an agent
	// reading its project directory, so this is said rather than prevented.
	Competing []WorkspaceSkillCompeting
	// Refused is every entry skipped because reading it would have left the workspace, or
	// could not be read or staged safely — named by its workspace-relative path.
	Refused []WorkspaceSkillRefusal
	// PackNotices are the PACK layers' lines, beside the workspace's because one composition
	// produces both: what the host's writer refused to deliver, and every reserved child it
	// withheld (skills.go), one per entry and reason.
	PackNotices []PackSkillNotice
}

// Empty reports whether the report has nothing to say — the common case, which is silent.
func (r *WorkspaceSkillsReport) Empty() bool {
	return r == nil || len(r.Mirrored)+len(r.Shadowed)+len(r.Collisions)+len(r.HeldBack)+
		len(r.Competing)+len(r.Refused)+len(r.PackNotices) == 0
}

// WorkspaceSkillDelivery is what one source dir delivered, and to whom.
type WorkspaceSkillDelivery struct {
	Source string   // the workspace-relative source dir, as declared
	Skills []string // the skill names delivered from it, sorted
	To     []string // the destinations that received at least one of them
}

// WorkspaceSkillShadow is one workspace skill a higher layer took the name of.
type WorkspaceSkillShadow struct {
	Name   string
	Source string   // the workspace-relative source dir it came from
	By     []string // who took the name ("yolo's built-in skill", "pack house's skill")
	In     []string // the destinations it was shadowed in
}

// WorkspaceSkillCollision is one skill name two or more source dirs carry.
type WorkspaceSkillCollision struct {
	Name   string
	Winner string   // the source dir whose copy is delivered
	Losers []string // the source dirs whose copies are not
	// ReadNatively is, per losing dir, the destinations whose agent reads THAT copy natively.
	// They are sent no other copy, so for them the winner is not the copy in use.
	ReadNatively []WorkspaceSkillNativeCopy
}

// WorkspaceSkillNativeCopy is one source dir's copy of a skill, and the destinations whose
// agents read it natively.
type WorkspaceSkillNativeCopy struct {
	Source string
	By     []string
}

// WorkspaceSkillHeldBack is one skill some destinations were not sent because a directory their
// agent reads natively carries a skill of that name.
type WorkspaceSkillHeldBack struct {
	Name   string
	From   string   // the source dir whose copy was not sent
	Native string   // the natively-read source dir that carries the name
	In     []string // the destinations
}

// WorkspaceSkillCompeting is one workspace skill some agents read natively under a name a higher
// layer also delivers to them.
type WorkspaceSkillCompeting struct {
	Name    string
	Source  string   // the source dir they read it from natively
	With    []string // who delivers the same name ("yolo's built-in skill")
	Readers []string // the destinations whose agents read it natively
}

// WorkspaceSkillRefusal is one entry the reader would not read, or could not stage.
type WorkspaceSkillRefusal struct {
	Path string // workspace-relative
	// Reason is yolo's own fixed text — never an error's message, which can carry a path the
	// workspace spelled (a link's target), and so a line the workspace wrote.
	Reason string
}

// maxSkillPath bounds an entry's path INSIDE its skill (the skill's name, then the rest), in
// bytes. The copy lands under several prefixes — the scratch tree, each destination's compose
// dir, its staging dir, and on macos-user the sandbox home — and macOS's PATH_MAX is 1024, so a
// path the workspace can hold (the reader walks one component at a time and has no limit of its
// own) could otherwise fail to be written under the longest of them and fail the launch. 512
// leaves every such prefix room on both systems; a skill deeper than that is not a skill.
const maxSkillPath = 512

// The per-launch cap on the workspace layer's scratch copy (OQ-WS7, ruled 2026-09-28, WS-D18):
// what the layer writes into scratch, kept or discarded, across every skill of one launch.
// Symlinks cost nothing to commit, so without it a clone whose skills each link one directory
// makes the host copy that directory once per skill, and again into every destination (R10).
//
// Set where no real skill set meets it. Measured 2026-09-28, regular-file bytes with links
// followed and entries counted as files plus directories: yolo's built-in skills 21,770 bytes
// in 5 entries; two personal skill packs 217,524/29 and 199,410/22; one user's composed
// ~/.claude/skills 437,859/55; the largest one plugin's skills dir in Claude's official
// marketplace (plugin-dev) 488,003/76; and all eighteen of that marketplace's plugin skills
// dirs taken as ONE set, which no repo carries, 1,389,298 bytes in 210 entries. The caps sit
// about 24× and 19× above that last figure, leaving room for a vendored tool of a few megabytes.
const (
	maxWorkspaceSkillBytes   = 32 << 20 // 32 MiB
	maxWorkspaceSkillEntries = 4096     // files and directories
)

// workspaceSkillCaps is one launch's cap, in bytes and entries.
type workspaceSkillCaps struct{ bytes, entries int64 }

// workspaceCaps is the cap stageWorkspaceLayer enforces: the constants above. A variable only
// so a test can cross it without writing tens of megabytes.
var workspaceCaps = workspaceSkillCaps{bytes: maxWorkspaceSkillBytes, entries: maxWorkspaceSkillEntries}

// workspaceLayer is the workspace's skills, copied ONCE per launch out of the confined tree into
// a private scratch tree, and then handed to each destination from there. Once, so a refusal is
// reported once and every destination receives the same bytes even if the workspace changes
// mid-launch; from scratch, because scratch is yolo's own tree and holds no links at all.
type workspaceLayer struct {
	tree   *confinedTree
	dir    string // the scratch tree; "" when nothing was staged
	staged int    // skills copied into it so far, each into a directory named by its number
	// caps is this launch's cap, and spent what the layer has written into scratch against it:
	// every file's bytes and every file and directory made, kept or discarded. It only grows:
	// a skill refused for crossing the cap keeps what it had copied charged, so a clone of many
	// skills each just past the cap cannot make the host write the cap once per skill.
	caps    workspaceSkillCaps
	spent   workspaceSkillCaps
	sources []*wsSource
	skills  []*wsSkill // the winners, in source order then name order
	// resolved caches resolveQuiet by workspace-relative path.
	resolved map[string]string

	refused    []WorkspaceSkillRefusal
	refusedAt  map[string]bool
	collisions map[string]*wsCollision
	collOrder  []string
	shadows    map[string]*WorkspaceSkillShadow
	shadowOrd  []string
	heldBack   map[string]*WorkspaceSkillHeldBack
	heldOrd    []string
	competing  map[string]*WorkspaceSkillCompeting
	compOrd    []string
	delivered  map[*wsSource]*deliveryAcc
}

type wsSource struct {
	rel  string // as declared
	real string // root-relative real path
	// entryReals is the real path of every skill directory in this source, winners and losers
	// alike: the skill-level half of the skip rule.
	entryReals map[string]bool
	// names is every top-level entry an agent reading this directory natively may take for a
	// skill: every directory, and every link, refused ones included — the agent resolves a link
	// in the jail, where a target this reader refused (a per-side path, say) may well be a
	// directory. A destination reading this source natively is sent no copy of any of these
	// names from another source.
	names map[string]bool
}

type wsSkill struct {
	name   string
	source *wsSource
	real   string // root-relative real path of the skill directory
	stored string // its copy in the scratch tree
}

type wsCollision struct {
	name   string
	winner *wsSource
	losers []*wsSource
	native map[*wsSource][]string // a losing source → the destinations reading it natively
}

type deliveryAcc struct {
	skills map[string]bool
	to     []string
	toSeen map[string]bool
}

// scratchBase is where the scratch tree is made: "" is os.TempDir. A variable so a test can put
// the scratch under a prefix long enough that a write into it fails.
var scratchBase = ""

// testHookBeforeRead, when set, runs after an entry has been classified and before its bytes are
// opened: the window a swap would race. Tests only.
var testHookBeforeRead func(real string)

// stageWorkspaceLayer resolves the source set and copies every winning skill into scratch.
// A nil or empty ws yields an empty layer that delivers nothing.
//
// The one error it returns is a failure to create the scratch root. Everything else a workspace
// can cause — an entry that cannot be read, or cannot be written into scratch — is a refusal.
func stageWorkspaceLayer(ws *WorkspaceSkills) (l *workspaceLayer, err error) {
	l = &workspaceLayer{
		resolved:   map[string]string{},
		refusedAt:  map[string]bool{},
		collisions: map[string]*wsCollision{},
		shadows:    map[string]*WorkspaceSkillShadow{},
		heldBack:   map[string]*WorkspaceSkillHeldBack{},
		competing:  map[string]*WorkspaceSkillCompeting{},
		delivered:  map[*wsSource]*deliveryAcc{},
		caps:       workspaceCaps,
	}
	if ws == nil || ws.Root == "" || len(ws.Dirs) == 0 {
		return l, nil
	}
	tree, err := openConfinedTree(ws)
	if err != nil {
		// Not the clone's doing and not fatal: the layer is simply absent, and SAID to be.
		l.refuse(".", "the workspace could not be opened for reading skills ("+errnoText(err)+")")
		return l, nil
	}
	l.tree = tree
	// Whatever this returns an error from, it leaves nothing behind: the tree closed, the
	// scratch removed.
	defer func() {
		if err != nil {
			l.close()
			l = nil
		}
	}()
	dir, err := os.MkdirTemp(scratchBase, "yolo-workspace-skills-")
	if err != nil {
		return nil, err
	}
	l.dir = dir

	bySource := map[string]*wsSource{}
	winners := map[string]*wsSkill{}
	for _, rel := range ws.Dirs {
		real, reason := tree.resolveSource(rel)
		if reason != "" {
			l.refuse(rel, reason)
			continue
		}
		if real == "" {
			continue // absent: the common case, and silent
		}
		l.resolved[rel] = real
		if _, dup := bySource[real]; dup {
			continue // a second spelling of a source already in the set (a committed link)
		}
		src := &wsSource{rel: rel, real: real, entryReals: map[string]bool{}, names: map[string]bool{}}
		bySource[real] = src
		l.sources = append(l.sources, src)
		names, err := tree.listDir(real)
		if err != nil {
			l.refuse(rel, readFailure(err))
			continue
		}
		for _, name := range names {
			if name == ".git" {
				continue
			}
			display := rel + "/" + name
			entry := joinRel(real, name)
			childReal, reason := tree.resolveEntry(entry)
			if reason != "" {
				src.names[name] = true
				l.refuse(display, reason)
				continue
			}
			if childReal == "" {
				continue // vanished between the listing and the look
			}
			if containsPath(childReal, real) {
				// `x → .` or `x → ../..`: a "skill" that is its own source dir, or the whole
				// workspace, is a cycle before a single file is copied.
				src.names[name] = true
				l.refuse(display, "a symlink to a directory that contains it (a cycle)")
				continue
			}
			fi, err := tree.root.Lstat(childReal)
			if err != nil || !fi.IsDir() {
				// A top-level FILE is not a skill directory, for this layer exactly as for every
				// other one (the pack layers' reader, hostskills' collectSkills, skips it too).
				continue
			}
			src.names[name] = true
			src.entryReals[childReal] = true
			if w, ok := winners[name]; ok {
				if w.real != childReal {
					l.collide(name, w.source, src)
				}
				continue
			}
			if sk := l.stageSkill(src, name, childReal, entry, display); sk != nil {
				winners[name] = sk
				l.skills = append(l.skills, sk)
			}
		}
	}
	return l, nil
}

// stageSkill copies one skill directory into scratch and returns it, or nil when not one file
// of it could be staged — every entry refused, or none there — so there is no skill to deliver
// and the mirror line must not claim one. The refusals were already named; a later source's
// skill of this name may still win.
//
// STAGED, THEN COMMITTED, against the layer's cap (OQ-WS7): the skill is copied into a directory
// of its own and becomes part of the layer only once the whole of it is there. When the next
// write would pass the cap, the walk stops there, the partial copy is deleted and the skill is
// refused by name, so a skill is delivered whole or not at all and the skills before it stay.
func (l *workspaceLayer) stageSkill(src *wsSource, name, real, entry, display string) *wsSkill {
	start := l.spent
	walk := &skillWalk{tree: l.tree, layer: l, linked: map[string]bool{},
		skillPrefix: len(src.rel) + 1}
	if !walk.charge(1, 0) {
		l.refuse(display, l.capReason(walk.crossed, start))
		return nil
	}
	l.staged++
	stored := filepath.Join(l.dir, fmt.Sprint(l.staged))
	if err := os.Mkdir(stored, 0o755); err != nil {
		l.refuse(display, stageFailure(err))
		return nil
	}
	if testHookBeforeRead != nil {
		testHookBeforeRead(real)
	}
	fd, err := l.tree.openDir(real)
	if err != nil {
		l.refuse(display, readFailure(err))
		_ = os.RemoveAll(stored)
		return nil
	}
	if real != entry {
		walk.linked[real] = true
	}
	walk.copyDir(fd, real, display, stored)
	_ = unix.Close(fd)
	if walk.crossed != nil {
		_ = os.RemoveAll(stored)
		l.refuse(display, l.capReason(walk.crossed, start))
		return nil
	}
	if !holdsAFile(stored) {
		_ = os.RemoveAll(stored)
		return nil
	}
	return &wsSkill{name: name, source: src, real: real, stored: stored}
}

// close releases the confined tree and deletes the scratch copy.
func (l *workspaceLayer) close() {
	if l == nil {
		return
	}
	if l.tree != nil {
		l.tree.close()
		l.tree = nil
	}
	if l.dir != "" {
		_ = os.RemoveAll(l.dir)
		l.dir = ""
	}
}

// deliver copies this layer's skills into one destination's composed tree, into whatever names
// the layers above left free, honoring the skip rule; a skill whose name is taken is recorded as
// shadowed rather than delivered.
func (l *workspaceLayer) deliver(target SkillTarget, skillsDir string, taken map[string]string) error {
	if l == nil || l.tree == nil {
		return nil
	}
	dest := target.Agent
	if dest == "" {
		dest = "~/" + target.Dest
	}
	native := l.nativeFor(target)

	// What this agent reads natively under a name a higher layer ALSO delivers to it: two skills
	// of one name the mirror had no hand in and cannot prevent, only say.
	for _, src := range native.sources {
		for _, name := range sortedSet(src.names) {
			if by, ok := taken[name]; ok {
				l.compete(name, src, by, dest)
			}
		}
	}
	// The losing copies of a collision this agent reads natively: for it, the winner is not the
	// copy in use, and the collision line says so.
	for _, name := range l.collOrder {
		c := l.collisions[name]
		for _, lo := range c.losers {
			if native.dirs[lo.real] {
				c.native[lo] = appendUnique(c.native[lo], dest)
			}
		}
	}

	for _, sk := range l.skills {
		// THE SKIP RULE, at both grains: the agent reads the source dir natively, or reads this
		// very skill directory natively through another source it reads (a committed
		// `.claude/skills/x → ../../.agents/skills/x`). Either way a copy would be the agent's
		// second sight of one skill, which pi loads as two.
		if native.dirs[sk.source.real] || native.skills[sk.real] {
			continue
		}
		// AND AT THE GRAIN OF A NAME: a directory the agent reads natively carries a skill of this
		// name — a collision's losing copy, or an entry this reader refused — so a copy of the
		// winner would be a second, different skill of one name in one agent.
		if others := native.names[sk.name]; len(others) > 0 {
			for _, o := range others {
				if !l.collisions[sk.name].hasLoser(o) {
					l.holdBack(sk, o, dest)
				}
			}
			continue
		}
		if by, ok := taken[sk.name]; ok {
			l.shadow(sk, by, dest)
			continue
		}
		if err := copyPlainTree(sk.stored, filepath.Join(skillsDir, sk.name)); err != nil {
			return err
		}
		l.deliveredTo(sk, dest)
	}
	return nil
}

// nativeReads is what one destination's agent reads at project scope, resolved against the
// source set.
type nativeReads struct {
	dirs    map[string]bool        // real source dirs
	skills  map[string]bool        // real skill directories inside them
	names   map[string][]*wsSource // a name → the natively-read sources carrying it
	sources []*wsSource            // the natively-read sources, in source order
}

// nativeFor is the source dirs, the skill directories inside them and the names they carry,
// that target's own agent reads at project scope — its ProjectDirs, resolved the same confined
// way.
func (l *workspaceLayer) nativeFor(target SkillTarget) nativeReads {
	n := nativeReads{dirs: map[string]bool{}, skills: map[string]bool{}, names: map[string][]*wsSource{}}
	for _, rel := range target.ProjectDirs {
		if real := l.resolveQuiet(rel); real != "" {
			n.dirs[real] = true
		}
	}
	for _, src := range l.sources {
		if !n.dirs[src.real] {
			continue
		}
		n.sources = append(n.sources, src)
		for r := range src.entryReals {
			n.skills[r] = true
		}
		for name := range src.names {
			n.names[name] = append(n.names[name], src)
		}
	}
	return n
}

// resolveQuiet resolves a workspace-relative dir for the skip rule, reporting nothing: a
// destination's own declared dir that cannot be resolved simply matches no source.
func (l *workspaceLayer) resolveQuiet(rel string) string {
	if real, ok := l.resolved[rel]; ok {
		return real
	}
	real := ""
	if l.tree != nil {
		if r, reason := l.tree.resolveSource(rel); reason == "" {
			real = r
		}
	}
	l.resolved[rel] = real
	return real
}

func (l *workspaceLayer) refuse(path, reason string) {
	if l.refusedAt[path] {
		return
	}
	l.refusedAt[path] = true
	l.refused = append(l.refused, WorkspaceSkillRefusal{Path: path, Reason: reason})
}

func (l *workspaceLayer) collide(name string, winner, loser *wsSource) {
	c, ok := l.collisions[name]
	if !ok {
		c = &wsCollision{name: name, winner: winner, native: map[*wsSource][]string{}}
		l.collisions[name] = c
		l.collOrder = append(l.collOrder, name)
	}
	if !c.hasLoser(loser) {
		c.losers = append(c.losers, loser)
	}
}

// hasLoser reports whether src is one of this collision's losing dirs; false on a nil collision.
func (c *wsCollision) hasLoser(src *wsSource) bool {
	if c == nil {
		return false
	}
	for _, have := range c.losers {
		if have == src {
			return true
		}
	}
	return false
}

func (l *workspaceLayer) shadow(sk *wsSkill, by, dest string) {
	s, ok := l.shadows[sk.name]
	if !ok {
		s = &WorkspaceSkillShadow{Name: sk.name, Source: sk.source.rel}
		l.shadows[sk.name] = s
		l.shadowOrd = append(l.shadowOrd, sk.name)
	}
	s.By = appendUnique(s.By, by)
	s.In = appendUnique(s.In, dest)
}

func (l *workspaceLayer) holdBack(sk *wsSkill, native *wsSource, dest string) {
	key := sk.name + "\x00" + sk.source.rel + "\x00" + native.rel
	h, ok := l.heldBack[key]
	if !ok {
		h = &WorkspaceSkillHeldBack{Name: sk.name, From: sk.source.rel, Native: native.rel}
		l.heldBack[key] = h
		l.heldOrd = append(l.heldOrd, key)
	}
	h.In = appendUnique(h.In, dest)
}

func (l *workspaceLayer) compete(name string, src *wsSource, by, dest string) {
	key := name + "\x00" + src.rel
	c, ok := l.competing[key]
	if !ok {
		c = &WorkspaceSkillCompeting{Name: name, Source: src.rel}
		l.competing[key] = c
		l.compOrd = append(l.compOrd, key)
	}
	c.With = appendUnique(c.With, by)
	c.Readers = appendUnique(c.Readers, dest)
}

func (l *workspaceLayer) deliveredTo(sk *wsSkill, dest string) {
	acc, ok := l.delivered[sk.source]
	if !ok {
		acc = &deliveryAcc{skills: map[string]bool{}, toSeen: map[string]bool{}}
		l.delivered[sk.source] = acc
	}
	acc.skills[sk.name] = true
	if !acc.toSeen[dest] {
		acc.toSeen[dest] = true
		acc.to = append(acc.to, dest)
	}
}

// report assembles what the launch prints, in a deterministic order.
func (l *workspaceLayer) report() *WorkspaceSkillsReport {
	r := &WorkspaceSkillsReport{}
	if l == nil {
		return r
	}
	for _, src := range l.sources {
		acc, ok := l.delivered[src]
		if !ok {
			continue
		}
		d := WorkspaceSkillDelivery{Source: src.rel, To: append([]string(nil), acc.to...)}
		for name := range acc.skills {
			d.Skills = append(d.Skills, name)
		}
		sort.Strings(d.Skills)
		r.Mirrored = append(r.Mirrored, d)
	}
	for _, name := range l.shadowOrd {
		r.Shadowed = append(r.Shadowed, *l.shadows[name])
	}
	for _, name := range l.collOrder {
		c := l.collisions[name]
		out := WorkspaceSkillCollision{Name: name, Winner: c.winner.rel}
		for _, lo := range c.losers {
			out.Losers = append(out.Losers, lo.rel)
			if by := c.native[lo]; len(by) > 0 {
				out.ReadNatively = append(out.ReadNatively,
					WorkspaceSkillNativeCopy{Source: lo.rel, By: append([]string(nil), by...)})
			}
		}
		r.Collisions = append(r.Collisions, out)
	}
	for _, key := range l.heldOrd {
		r.HeldBack = append(r.HeldBack, *l.heldBack[key])
	}
	for _, key := range l.compOrd {
		r.Competing = append(r.Competing, *l.competing[key])
	}
	r.Refused = append(r.Refused, l.refused...)
	return r
}

func appendUnique(xs []string, x string) []string {
	for _, have := range xs {
		if have == x {
			return xs
		}
	}
	return append(xs, x)
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// skillWalk copies one skill directory out of the confined tree.
//
// A link to a directory that CONTAINS the one being copied is a cycle, and refused.
// linked is every directory this skill has already entered THROUGH A LINK: a second link to one
// is refused, which is what keeps a tree of links fanning into one directory from copying it
// exponentially many times. That bounds a skill's copy by (its links) × (the largest tree one of
// them reaches), not by the workspace's size: N skills each linking one directory copy it N
// times, and every destination that receives them gets that again. What bounds THAT is the
// layer's per-launch cap (OQ-WS7): every write is charged first, and the walk stops at the one
// that would pass it.
type skillWalk struct {
	tree   *confinedTree
	layer  *workspaceLayer
	linked map[string]bool
	// crossed is the write that would have passed the layer's cap; once set, nothing more of
	// this skill is read or written, and stageSkill refuses the skill whole.
	crossed *capCrossing
	// stack is the real directories on the current path, however each was reached: a link to
	// one of them, or to anything containing one, is a cycle.
	stack []string
	// skillPrefix is the length of "<source dir>/" in a display path, so what follows it is the
	// entry's path inside its skill — the part every destination has to hold.
	skillPrefix int
}

// capCrossing is the write that would have passed the cap: which of the two, and what that
// write alone would have added to it.
type capCrossing struct {
	entries bool  // the entry cap; otherwise the byte cap
	pending int64 // the entries or bytes of the write that would have crossed
}

// charge spends entries and bytes of the layer's cap on one write this walk is about to make,
// and reports whether it may. When it may not, nothing is spent, the walk records the crossing
// and every later charge of the walk fails too.
func (w *skillWalk) charge(entries, bytes int64) bool {
	if w.crossed != nil {
		return false
	}
	l := w.layer
	switch {
	case l.spent.entries+entries > l.caps.entries:
		w.crossed = &capCrossing{entries: true, pending: entries}
		return false
	case l.spent.bytes+bytes > l.caps.bytes:
		w.crossed = &capCrossing{pending: bytes}
		return false
	}
	l.spent.entries += entries
	l.spent.bytes += bytes
	return true
}

// capReason is the refusal of a skill whose copy crossed the cap, start being what the layer had
// spent before the skill began. What the skill "adds" is what it had copied when the walk
// stopped plus the write that would have crossed, a lower bound: the walk stops there on purpose,
// so the refusal never costs what the cap exists to prevent.
func (l *workspaceLayer) capReason(c *capCrossing, start workspaceSkillCaps) string {
	if c.entries {
		added := l.spent.entries - start.entries + c.pending
		return fmt.Sprintf("copying it would pass this launch's cap of %d files and directories "+
			"on workspace skills: it adds at least %d to the %d already copied, so none of it is "+
			"delivered", l.caps.entries, added, start.entries)
	}
	added := l.spent.bytes - start.bytes + c.pending
	return fmt.Sprintf("copying it would pass this launch's cap of %s on workspace skills: it "+
		"adds at least %s to the %s already copied, so none of it is delivered",
		byteSize(l.caps.bytes), byteSize(added), byteSize(start.bytes))
}

// byteSize is n in the largest binary unit it fills, "32 MiB" or "1.5 KiB" — exact for a whole
// number of units, so a cap reads as the constant that set it.
func byteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}[exp]
	if n%div == 0 {
		return fmt.Sprintf("%d %s", n/div, suffix)
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), suffix)
}

// closesACycle reports whether a link to dir would re-enter the current path.
func (w *skillWalk) closesACycle(dir string) bool {
	for _, s := range w.stack {
		if containsPath(dir, s) {
			return true
		}
	}
	return false
}

// holdsAFile reports whether a scratch tree yolo wrote contains at least one regular file.
func holdsAFile(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// containsPath reports whether root-relative real path dir is p or an ancestor of it.
func containsPath(dir, p string) bool {
	return dir == "." || dir == p || strings.HasPrefix(p, dir+"/")
}

// copyDir copies the directory open at fd — the real path real, reached by a walk that followed
// no link — into dst. Every failure is a refusal of the entry it happened to.
func (w *skillWalk) copyDir(fd int, real, display, dst string) {
	w.stack = append(w.stack, real)
	defer func() { w.stack = w.stack[:len(w.stack)-1] }()
	names, err := readNames(fd)
	if err != nil {
		w.layer.refuse(display, readFailure(err))
		return
	}
	for _, name := range names {
		if w.crossed != nil {
			return // the cap was reached: the skill is refused whole, so reading on is waste
		}
		if name == ".git" {
			continue // a vendored checkout's metadata, never content
		}
		entry := joinRel(real, name)
		childDisplay := display + "/" + name
		if len(childDisplay)-w.skillPrefix > maxSkillPath {
			w.layer.refuse(childDisplay, fmt.Sprintf("its path inside the skill is longer than %d "+
				"bytes, more than every agent's skills directory can be counted on to hold", maxSkillPath))
			continue
		}
		childReal, reason := w.tree.resolveEntry(entry)
		if reason != "" {
			w.layer.refuse(childDisplay, reason)
			continue
		}
		if childReal == "" {
			continue
		}
		viaLink := childReal != entry
		// Where the entry's bytes are read from: under this directory's own descriptor when it is
		// not a link, and by a fresh walk from the root to its target when it is. Either way,
		// every component on the way was opened without following anything.
		parent, base, own := fd, name, false
		if viaLink {
			p, err := w.tree.openDir(path.Dir(childReal))
			if err != nil {
				w.layer.refuse(childDisplay, readFailure(err))
				continue
			}
			parent, base, own = p, path.Base(childReal), true
		}
		w.copyEntry(parent, base, childReal, childDisplay, filepath.Join(dst, name), viaLink)
		if own {
			_ = unix.Close(parent)
		}
	}
}

// copyEntry copies one entry, named base under the directory open at parent, whose real path
// is real.
func (w *skillWalk) copyEntry(parent int, base, real, display, target string, viaLink bool) {
	var st unix.Stat_t
	if err := fstatat(parent, base, &st); err != nil {
		w.layer.refuse(display, readFailure(err))
		return
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		// A CYCLE is a link to any directory that contains one on the current path — not only
		// one this walk entered: `x/loop → ..` names the source dir above the skill, and
		// `x/a → ../../shared` with `shared/back → ../.agents/skills/x` returns to the skill
		// through a directory reached by a link. Either would copy the skill into itself.
		if viaLink && w.closesACycle(real) {
			w.layer.refuse(display, "a symlink back to a directory that contains it (a cycle)")
			return
		}
		if viaLink {
			if w.linked[real] {
				w.layer.refuse(display, "a second symlink to a directory this skill "+
					"already copies through a symlink")
				return
			}
			w.linked[real] = true
		}
		if testHookBeforeRead != nil {
			testHookBeforeRead(real)
		}
		sub, err := openat(parent, base, dirOpenFlags)
		if err != nil {
			w.layer.refuse(display, readFailure(err))
			return
		}
		defer unix.Close(sub)
		if !w.charge(1, 0) {
			return
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			w.layer.refuse(display, stageFailure(err))
			return
		}
		w.copyDir(sub, real, display, target)
	case unix.S_IFREG:
		if testHookBeforeRead != nil {
			testHookBeforeRead(real)
		}
		if reason := copyFileAt(parent, base, target, w.charge); reason != "" {
			w.layer.refuse(display, reason)
		}
	case unix.S_IFLNK:
		// resolve found no link here, so one was swapped in since: not followed, and said.
		w.layer.refuse(display, errChanged.Error())
	default:
		w.layer.refuse(display, "not a regular file or directory")
	}
}

// confinedTree reads a workspace without ever reading outside it. See the file comment.
type confinedTree struct {
	// root CLASSIFIES: resolve reads links and lstats through it, confined to the workspace.
	root *os.Root
	// rootFD READS: every open is a walk from here that follows no link (openDir).
	rootFD int
	// spellings are the absolute, cleaned spellings of the root an absolute link may name:
	// the root as given, its real path, and the aliases.
	spellings []string
	perSide   []string // cleaned, root-relative
}

func openConfinedTree(ws *WorkspaceSkills) (*confinedTree, error) {
	abs, err := filepath.Abs(ws.Root)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	// The root's own spelling is trusted and may pass through links; nothing below it is.
	fd, err := openat(unix.AT_FDCWD, abs, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK|unix.O_CLOEXEC)
	if err != nil {
		root.Close()
		return nil, err
	}
	t := &confinedTree{root: root, rootFD: fd}
	add := func(s string) {
		if s == "" || !filepath.IsAbs(s) {
			return
		}
		s = filepath.Clean(s)
		for _, have := range t.spellings {
			if have == s {
				return
			}
		}
		t.spellings = append(t.spellings, s)
	}
	add(abs)
	// The root's own spelling may pass through a link (macOS's /var → /private/var is the
	// common one), and an absolute link written on the host names the real path.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		add(real)
	}
	for _, a := range ws.Aliases {
		add(a)
	}
	for _, p := range ws.PerSide {
		if c := filepath.ToSlash(filepath.Clean(p)); c != "." && !strings.HasPrefix(c, "../") && c != ".." {
			t.perSide = append(t.perSide, c)
		}
	}
	return t, nil
}

func (t *confinedTree) close() {
	_ = t.root.Close()
	_ = unix.Close(t.rootFD)
}

type resolution int

const (
	resolvedOK resolution = iota
	resolvedAbsent
	resolvedDangling
	resolvedEscapes
	resolvedLoop
	resolvedUnreadable
)

// maxLinkHops bounds one resolution, as the kernel's own ELOOP limit does.
const maxLinkHops = 40

// resolve walks rel, component by component, INSIDE the root, and returns the real root-relative
// path it names ("." is the root itself). No component is examined except through the root, and a
// link's target is followed lexically, so a walk that would leave the root stops before anything
// outside it is touched.
func (t *confinedTree) resolve(rel string) (string, resolution, error) {
	pending := splitRel(rel)
	var cur []string
	hops := 0
	for len(pending) > 0 {
		p := pending[0]
		pending = pending[1:]
		switch p {
		case "", ".":
			continue
		case "..":
			if len(cur) == 0 {
				return "", resolvedEscapes, nil
			}
			cur = cur[:len(cur)-1]
			continue
		}
		candidate := joinRel(joinParts(cur), p)
		fi, err := t.root.Lstat(candidate)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				if hops > 0 {
					return "", resolvedDangling, nil
				}
				return "", resolvedAbsent, nil
			}
			return "", resolvedUnreadable, err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = append(cur, p)
			continue
		}
		hops++
		if hops > maxLinkHops {
			return "", resolvedLoop, nil
		}
		target, err := t.root.Readlink(candidate)
		if err != nil {
			return "", resolvedUnreadable, err
		}
		if strings.HasPrefix(target, "/") {
			inside, ok := t.underSpelling(target)
			if !ok {
				return "", resolvedEscapes, nil
			}
			cur = nil
			pending = append(splitRel(inside), pending...)
			continue
		}
		pending = append(splitRel(target), pending...)
	}
	return joinParts(cur), resolvedOK, nil
}

// underSpelling maps an absolute link target onto the root, when it names a path under one of
// the root's spellings. The remainder is NOT cleaned: its `..` components are walked by resolve,
// after the components before them are known to be real directories.
func (t *confinedTree) underSpelling(abs string) (string, bool) {
	for _, s := range t.spellings {
		if abs == s {
			return ".", true
		}
		prefix := s
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if strings.HasPrefix(abs, prefix) {
			return abs[len(prefix):], true
		}
	}
	return "", false
}

// excluded names why a resolved real path is never read, or "".
func (t *confinedTree) excluded(real string) string {
	if real == "." {
		return ""
	}
	switch first := strings.SplitN(real, "/", 2)[0]; first {
	case ".git":
		return "resolves into .git, which is never skill content"
	case ".yolo":
		return "resolves into .yolo, yolo's own state directory, which is never skill content"
	}
	for _, p := range t.perSide {
		if real == p || strings.HasPrefix(real, p+"/") {
			return "resolves into " + p + ", a per-side path: the jail sees its own copy there, " +
				"so the host's is not delivered"
		}
	}
	return ""
}

// resolveEntry resolves one entry and applies every refusal; "" real with "" reason is an entry
// that does not exist.
func (t *confinedTree) resolveEntry(rel string) (string, string) {
	real, res, err := t.resolve(rel)
	switch res {
	case resolvedAbsent:
		return "", ""
	case resolvedDangling:
		return "", "a dangling symlink"
	case resolvedEscapes:
		return "", "a symlink that resolves outside the workspace; nothing it points at was read"
	case resolvedLoop:
		return "", "a symlink chain too long to follow (a loop?)"
	case resolvedUnreadable:
		return "", "unreadable (" + errnoText(err) + ")"
	}
	if why := t.excluded(real); why != "" {
		return "", why
	}
	return real, ""
}

// resolveSource resolves one declared source dir: "" real and "" reason when it is absent.
func (t *confinedTree) resolveSource(rel string) (string, string) {
	real, reason := t.resolveEntry(rel)
	if reason != "" || real == "" {
		return real, reason
	}
	if real == "." {
		return "", "resolves to the workspace root itself, which is not a skills directory"
	}
	fi, err := t.root.Lstat(real)
	if err != nil {
		return "", "unreadable (" + errnoText(err) + ")"
	}
	if !fi.IsDir() {
		return "", "not a directory"
	}
	return real, ""
}

// The flags of every read-side open. O_NOFOLLOW is the point: resolve has already said what each
// path IS, so a link met while reading is one swapped in since, and is not followed. O_NONBLOCK
// keeps a FIFO swapped in from hanging the launcher; O_DIRECTORY makes a directory open fail on
// anything else.
const (
	dirOpenFlags  = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	fileOpenFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
)

func openat(dirfd int, name string, flags int) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, flags, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func fstatat(dirfd int, name string, st *unix.Stat_t) error {
	for {
		err := unix.Fstatat(dirfd, name, st, unix.AT_SYMLINK_NOFOLLOW)
		if err != unix.EINTR {
			return err
		}
	}
}

// openDir opens the directory at root-relative real path dir by a walk from the root's own
// descriptor, one component at a time, following no link. The caller closes what it returns.
func (t *confinedTree) openDir(dir string) (int, error) {
	fd, err := openat(t.rootFD, ".", dirOpenFlags)
	if err != nil {
		return -1, err
	}
	for _, c := range splitRel(dir) {
		next, err := openat(fd, c, dirOpenFlags)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

// listDir lists the directory at real path dir, sorted, through openDir.
func (t *confinedTree) listDir(dir string) ([]string, error) {
	fd, err := t.openDir(dir)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	return readNames(fd)
}

// readNames lists the directory open at fd, sorted. It reads a fresh open of "." under fd, so
// fd's own offset is untouched and fd stays usable for the openat calls that follow.
func readNames(fd int) ([]string, error) {
	own, err := openat(fd, ".", dirOpenFlags)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(own), ".")
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// copyReal copies the regular file at real path real into dst, opening it the way copyDir does.
// A non-empty reason is a refusal of it.
func (t *confinedTree) copyReal(real, dst string) string {
	parent, err := t.openDir(path.Dir(real))
	if err != nil {
		return readFailure(err)
	}
	defer unix.Close(parent)
	return copyFileAt(parent, path.Base(real), dst, func(int64, int64) bool { return true })
}

// copyFileAt copies the regular file base, under the directory open at parent, into dst,
// carrying its execute bit. A non-empty reason is a refusal of this file: it could not be read,
// is not a regular file, or its copy could not be written — and then no partial copy remains.
//
// charge is asked for one entry and the file's size once the file is known to be regular and
// before a byte of it is read; when it declines, nothing is written and the reason is "", the
// caller having recorded why. A file that grows past the size it was charged for is refused as
// changed while it was being read, so no copy is ever larger than its charge.
func copyFileAt(parent int, base, dst string, charge func(entries, bytes int64) bool) string {
	fd, err := openat(parent, base, fileOpenFlags)
	if err != nil {
		return readFailure(err)
	}
	in := os.NewFile(uintptr(fd), base)
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return readFailure(err)
	}
	if !fi.Mode().IsRegular() {
		return "not a regular file or directory"
	}
	if !charge(1, fi.Size()) {
		return ""
	}
	readErr, writeErr := writeCopy(&boundedReader{r: in, left: fi.Size()}, dst, fi.Mode())
	switch {
	case readErr != nil:
		return readFailure(readErr)
	case writeErr != nil:
		return stageFailure(writeErr)
	}
	return ""
}

// errChanged is a path that is no longer what resolve classified: a component swapped for a
// link, a file or nothing between the look and the read.
var errChanged = errors.New("changed while it was being read")

// readFailure is the refusal reason for a read that failed. Fixed text and an errno's own
// description, NEVER err.Error(): a path error carries the path it failed on, and a path a link
// in the workspace spelled is text the workspace wrote — a newline and a forged disclosure line
// included.
func readFailure(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ELOOP, syscall.EMLINK, syscall.ENOTDIR, syscall.ENOENT:
			// O_NOFOLLOW on a link is ELOOP (EMLINK on some BSDs); O_DIRECTORY on a file is
			// ENOTDIR; gone is ENOENT. Each is a path that changed after it was classified.
			return errChanged.Error()
		}
		return "unreadable (" + errno.Error() + ")"
	}
	if errors.Is(err, errChanged) {
		return errChanged.Error()
	}
	return "unreadable"
}

// stageFailure is the refusal reason for a scratch write that failed, with readFailure's rule.
func stageFailure(err error) string {
	return "could not be staged (" + errnoText(err) + ")"
}

// errnoText is an error's errno description, or a fixed word when it carries none.
func errnoText(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	return "error"
}

// copyPlainTree copies a tree yolo itself wrote (the scratch layer: directories and regular
// files, no links) into dst, carrying execute bits.
func copyPlainTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		switch {
		case e.IsDir():
			if err := copyPlainTree(s, d); err != nil {
				return err
			}
		case e.Type().IsRegular():
			fi, err := e.Info()
			if err != nil {
				return err
			}
			in, err := os.Open(s)
			if err != nil {
				return err
			}
			readErr, writeErr := writeCopy(in, d, fi.Mode())
			in.Close()
			if err := errors.Join(readErr, writeErr); err != nil {
				return err
			}
		}
	}
	return nil
}

// boundedReader reads at most left bytes of r and fails with errChanged if r holds more: a file
// that grew after it was charged for its size.
type boundedReader struct {
	r    io.Reader
	left int64
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, errChanged
	}
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1] // one byte past the bound, to see whether there is one
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n, errChanged
	}
	return n, err
}

// trackedReader remembers the reader's own failure, so a copy can tell a failed read from a
// failed write.
type trackedReader struct {
	r   io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

// writeCopy writes r to a NEW file at dst, 0o644 or — when src carried any execute bit — 0o755.
// The read/write bits never come from the source, so a group-writable file in someone's repo
// does not widen the staged copy (packstage.copyFile's rule). On any failure dst is removed.
func writeCopy(r io.Reader, dst string, srcMode fs.FileMode) (readErr, writeErr error) {
	mode := fs.FileMode(0o644)
	if srcMode.Perm()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return nil, err
	}
	tr := &trackedReader{r: r}
	_, err = io.Copy(out, tr)
	cerr := out.Close()
	switch {
	case tr.err != nil:
		readErr = tr.err
	case err != nil:
		writeErr = err
	case cerr != nil:
		writeErr = cerr
	default:
		writeErr = os.Chmod(dst, mode)
	}
	if readErr != nil || writeErr != nil {
		_ = os.Remove(dst)
	}
	return readErr, writeErr
}

// splitRel splits a slash-separated path into components, dropping empties.
func splitRel(p string) []string {
	var out []string
	for _, c := range strings.Split(filepath.ToSlash(p), "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

func joinRel(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

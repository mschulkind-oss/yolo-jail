package jailcontent

// workspaceskills.go is the WORKSPACE layer of the skills composition — the staged mirror of
// docs/design/workspace-skills.md §4.1, as ruled on 2026-09-27 (OQ-WS1–WS4).
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
// The copier every other layer uses (copySkillSubdirs) follows symlinks, correctly: its sources
// are yolo's own trees and pack trees packstage already refused escaping links in. The workspace
// is neither. It is populated by a `git clone` and editable by the agent inside the jail, and this
// code runs ON THE HOST — so `.agents/skills/x/SKILL.md → ~/.ssh/id_ed25519`, committed or planted
// between two attaches, would have been read by the host user and bound into the jail as a skill.
//
// So the workspace has its own reader, confinedTree, and three properties make the refusal hold
// rather than merely be checked:
//
//  1. EVERY READ GOES THROUGH AN os.Root opened on the workspace, which the kernel-facing
//     implementation confines (openat, component by component): a path that would leave the root
//     — by `..`, by an absolute link, by a link swapped in after it was classified — fails to open.
//     A race between classifying an entry and copying it can make a copy fail; it cannot make
//     one read outside.
//  2. LINKS ARE RESOLVED LEXICALLY, INSIDE THE ROOT, before anything is opened. resolve reads each
//     link's target with the root's own Readlink and walks it; a target that climbs above the root
//     or is an absolute path outside every spelling of it ends the walk BEFORE anything there is
//     touched — not even an lstat — so an escaping entry is NAMED without the host having read,
//     or even checked the existence of, what it pointed at.
//  3. NOTHING IS OPENED THAT COULD BLOCK. Every open carries O_NONBLOCK (a FIFO planted where a
//     SKILL.md was would otherwise hang the launcher on the next attach, measured) and a directory
//     open carries O_DIRECTORY; a special file is refused by its fstat, after the open.
//
// A refused entry is SKIPPED AND NAMED, never fatal: a clone must not be able to refuse a jail.
//
// # What else is never read
//
// A path that resolves under `.git` (never content) or `.yolo` (yolo's own state, and reading it
// would make this a layer that reads generated output — the deleted HostSource's defect), and a
// PER-SIDE path: the jail sees its own copy of `.venv`, `node_modules` and every `per_side_paths`
// entry, so the host's bytes there are exactly what the jail cannot read, and the mirror's whole
// justification is that it grants no authority the repo lacks (OQ-WS1's answer).

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
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
	// Refused is every entry skipped because reading it would have left the workspace, or
	// could not be read safely — named by its workspace-relative path.
	Refused []WorkspaceSkillRefusal
}

// Empty reports whether the report has nothing to say — the common case, which is silent.
func (r *WorkspaceSkillsReport) Empty() bool {
	return r == nil || len(r.Mirrored)+len(r.Shadowed)+len(r.Collisions)+len(r.Refused) == 0
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
}

// WorkspaceSkillRefusal is one entry the reader would not read.
type WorkspaceSkillRefusal struct {
	Path   string // workspace-relative
	Reason string
}

// workspaceLayer is the workspace's skills, copied ONCE per launch out of the confined tree into
// a private scratch tree, and then handed to each destination from there. Once, so a refusal is
// reported once and every destination receives the same bytes even if the workspace changes
// mid-launch; from scratch, because scratch is yolo's own tree and holds no links at all.
type workspaceLayer struct {
	tree    *confinedTree
	dir     string // the scratch tree; "" when nothing was staged
	sources []*wsSource
	skills  []*wsSkill // the winners, in source order then name order
	// resolved caches resolveQuiet by workspace-relative path.
	resolved map[string]string

	refused    []WorkspaceSkillRefusal
	refusedAt  map[string]bool
	collisions map[string]*WorkspaceSkillCollision
	collOrder  []string
	shadows    map[string]*WorkspaceSkillShadow
	shadowOrd  []string
	delivered  map[*wsSource]*deliveryAcc
}

type wsSource struct {
	rel  string // as declared
	real string // root-relative real path
	// entryReals is the real path of every skill directory in this source, winners and losers
	// alike: the skill-level half of the skip rule.
	entryReals map[string]bool
}

type wsSkill struct {
	name   string
	source *wsSource
	real   string // root-relative real path of the skill directory
	stored string // its copy in the scratch tree
}

type deliveryAcc struct {
	skills map[string]bool
	to     []string
	toSeen map[string]bool
}

// stageWorkspaceLayer resolves the source set and copies every winning skill into scratch.
// A nil or empty ws yields an empty layer that delivers nothing.
func stageWorkspaceLayer(ws *WorkspaceSkills) (*workspaceLayer, error) {
	l := &workspaceLayer{
		resolved:   map[string]string{},
		refusedAt:  map[string]bool{},
		collisions: map[string]*WorkspaceSkillCollision{},
		shadows:    map[string]*WorkspaceSkillShadow{},
		delivered:  map[*wsSource]*deliveryAcc{},
	}
	if ws == nil || ws.Root == "" || len(ws.Dirs) == 0 {
		return l, nil
	}
	tree, err := openConfinedTree(ws)
	if err != nil {
		// Not the clone's doing and not fatal: the layer is simply absent, and SAID to be.
		l.refuse(".", "the workspace could not be opened for reading skills: "+err.Error())
		return l, nil
	}
	l.tree = tree
	dir, err := os.MkdirTemp("", "yolo-workspace-skills-")
	if err != nil {
		tree.close()
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
		src := &wsSource{rel: rel, real: real, entryReals: map[string]bool{}}
		bySource[real] = src
		l.sources = append(l.sources, src)
		names, err := tree.readDirNames(real)
		if err != nil {
			l.refuse(rel, "unreadable: "+err.Error())
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
				l.refuse(display, reason)
				continue
			}
			if childReal == "" {
				continue // vanished between the listing and the look
			}
			if containsPath(childReal, real) {
				// `x → .` or `x → ../..`: a "skill" that is its own source dir, or the whole
				// workspace, is a cycle before a single file is copied.
				l.refuse(display, "a symlink to a directory that contains it (a cycle)")
				continue
			}
			fi, err := tree.root.Lstat(childReal)
			if err != nil || !fi.IsDir() {
				// A top-level FILE is not a skill directory, for this layer exactly as for every
				// other one (copySkillSubdirs skips it too).
				continue
			}
			src.entryReals[childReal] = true
			if w, ok := winners[name]; ok {
				if w.real != childReal {
					l.collide(name, w.source.rel, rel)
				}
				continue
			}
			stored := filepath.Join(dir, strconv.Itoa(len(l.skills)))
			if err := os.Mkdir(stored, 0o755); err != nil {
				return nil, err
			}
			walk := &skillWalk{tree: tree, layer: l, linked: map[string]bool{}}
			if childReal != entry {
				walk.linked[childReal] = true
			}
			if err := walk.copyDir(childReal, display, stored); err != nil {
				return nil, err
			}
			sk := &wsSkill{name: name, source: src, real: childReal, stored: stored}
			winners[name] = sk
			l.skills = append(l.skills, sk)
		}
	}
	return l, nil
}

// close releases the confined tree and deletes the scratch copy.
func (l *workspaceLayer) close() {
	if l == nil {
		return
	}
	if l.tree != nil {
		l.tree.close()
	}
	if l.dir != "" {
		_ = os.RemoveAll(l.dir)
	}
}

// deliver copies this layer's skills into one destination's composed tree, into whatever names
// the layers above left free, honoring the skip rule; a skill whose name is taken is recorded as
// shadowed rather than delivered.
func (l *workspaceLayer) deliver(target SkillTarget, skillsDir string, taken map[string]string) error {
	if l == nil || len(l.skills) == 0 {
		return nil
	}
	dest := target.Agent
	if dest == "" {
		dest = "~/" + target.Dest
	}
	nativeDirs, nativeSkills := l.nativeFor(target)
	for _, sk := range l.skills {
		// THE SKIP RULE, at both grains: the agent reads the source dir natively, or reads this
		// very skill directory natively through another source it reads (a committed
		// `.claude/skills/x → ../../.agents/skills/x`). Either way a copy would be the agent's
		// second sight of one skill, which pi loads as two.
		if nativeDirs[sk.source.real] || nativeSkills[sk.real] {
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

// nativeFor is the source dirs, and the skill directories inside them, that target's own agent
// reads at project scope — its ProjectDirs, resolved the same confined way.
func (l *workspaceLayer) nativeFor(target SkillTarget) (dirs, skills map[string]bool) {
	dirs, skills = map[string]bool{}, map[string]bool{}
	for _, rel := range target.ProjectDirs {
		real := l.resolveQuiet(rel)
		if real == "" {
			continue
		}
		dirs[real] = true
		for _, src := range l.sources {
			if src.real == real {
				for r := range src.entryReals {
					skills[r] = true
				}
			}
		}
	}
	return dirs, skills
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

func (l *workspaceLayer) collide(name, winner, loser string) {
	c, ok := l.collisions[name]
	if !ok {
		c = &WorkspaceSkillCollision{Name: name, Winner: winner}
		l.collisions[name] = c
		l.collOrder = append(l.collOrder, name)
	}
	for _, have := range c.Losers {
		if have == loser {
			return
		}
	}
	c.Losers = append(c.Losers, loser)
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
		r.Collisions = append(r.Collisions, *l.collisions[name])
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

// skillWalk copies one skill directory out of the confined tree.
//
// A link to a directory that CONTAINS the one being copied is a cycle, and refused.
// linked is every directory this skill has already entered THROUGH A LINK: a second link to one
// is refused, which is what keeps a tree of links fanning into one directory from copying it
// exponentially many times — each link target is copied once per skill, so the copy is at most
// linear in the number of links.
type skillWalk struct {
	tree   *confinedTree
	layer  *workspaceLayer
	linked map[string]bool
	// stack is the real directories on the current path, however each was reached: a link to
	// one of them, or to anything containing one, is a cycle.
	stack []string
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

// containsPath reports whether root-relative real path dir is p or an ancestor of it.
func containsPath(dir, p string) bool {
	return dir == "." || dir == p || strings.HasPrefix(p, dir+"/")
}

func (w *skillWalk) copyDir(real, display, dst string) error {
	w.stack = append(w.stack, real)
	defer func() { w.stack = w.stack[:len(w.stack)-1] }()
	names, err := w.tree.readDirNames(real)
	if err != nil {
		w.layer.refuse(display, "unreadable: "+err.Error())
		return nil
	}
	for _, name := range names {
		if name == ".git" {
			continue // a vendored checkout's metadata, never content
		}
		entry := joinRel(real, name)
		childDisplay := display + "/" + name
		childReal, reason := w.tree.resolveEntry(entry)
		if reason != "" {
			w.layer.refuse(childDisplay, reason)
			continue
		}
		if childReal == "" {
			continue
		}
		viaLink := childReal != entry
		fi, err := w.tree.root.Lstat(childReal)
		if err != nil {
			w.layer.refuse(childDisplay, "unreadable: "+err.Error())
			continue
		}
		target := filepath.Join(dst, name)
		switch {
		case fi.IsDir():
			// A CYCLE is a link to any directory that contains one on the current path — not only
			// one this walk entered: `x/loop → ..` names the source dir above the skill, and
			// `x/a → ../../shared` with `shared/back → ../.agents/skills/x` returns to the skill
			// through a directory reached by a link. Either would copy the skill into itself.
			if viaLink && w.closesACycle(childReal) {
				w.layer.refuse(childDisplay, "a symlink back to a directory that contains it (a cycle)")
				continue
			}
			if viaLink {
				if w.linked[childReal] {
					w.layer.refuse(childDisplay, "a second symlink to a directory this skill "+
						"already copies through a symlink")
					continue
				}
				w.linked[childReal] = true
			}
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			if err := w.copyDir(childReal, childDisplay, target); err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if reason, err := w.tree.copyFile(childReal, target); err != nil {
				return err
			} else if reason != "" {
				w.layer.refuse(childDisplay, reason)
			}
		default:
			w.layer.refuse(childDisplay, "not a regular file or directory")
		}
	}
	return nil
}

// confinedTree reads a workspace without ever reading outside it. See the file comment.
type confinedTree struct {
	root *os.Root
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
	t := &confinedTree{root: root}
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

func (t *confinedTree) close() { _ = t.root.Close() }

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
		return "", "unreadable: " + err.Error()
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
		return "", "unreadable: " + err.Error()
	}
	if !fi.IsDir() {
		return "", "not a directory"
	}
	return real, ""
}

// readDirNames lists a real directory through the root, sorted. O_DIRECTORY|O_NONBLOCK: a FIFO
// swapped in for the directory fails instead of blocking the launcher.
func (t *confinedTree) readDirNames(real string) ([]string, error) {
	f, err := t.root.OpenFile(real, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// copyFile copies one regular file out of the root into dst, carrying its execute bit. A
// non-empty reason is a refusal of this file; an error is a failure to write the scratch tree.
func (t *confinedTree) copyFile(real, dst string) (string, error) {
	in, err := t.root.OpenFile(real, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "unreadable: " + err.Error(), nil
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return "unreadable: " + err.Error(), nil
	}
	if !fi.Mode().IsRegular() {
		return "not a regular file or directory", nil
	}
	return "", writeCopy(in, dst, fi.Mode())
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
			err = writeCopy(in, d, fi.Mode())
			in.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// writeCopy writes r to a NEW file at dst, 0o644 or — when src carried any execute bit — 0o755.
// The read/write bits never come from the source, so a group-writable file in someone's repo
// does not widen the staged copy (packstage.copyFile's rule).
func writeCopy(r io.Reader, dst string, srcMode fs.FileMode) error {
	mode := fs.FileMode(0o644)
	if srcMode.Perm()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return fmt.Errorf("copying %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
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

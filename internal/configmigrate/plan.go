package configmigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Link is a symlink a migration moves so a name the user linked keeps reaching its file: the old
// link is replaced by its twin, which points at the new file (readable-config-plan.md §6
// "Symlink writes"; RC-D41). Left in place it would name the original's backup.
type Link struct {
	// Path is the new link, the old link's twin; Target is its text, the old link's text respelled.
	Path, Target string
	// Old is the user's link, removed once its twin is made; Text is its text, for the undo.
	Old, Text string
}

// Target is one file a plan writes.
type Target struct {
	File *File
	// Old is the physical file read; New is the physical file written (Old's .toml twin for a
	// conversion, Old itself for a respelled TOML file).
	Old, New string
	// Backup is where the original is kept: Old + ".pre-migrate".
	Backup string
	// Link is the symlink made beside a linked old name, nil when none is needed.
	Link  *Link
	Bytes []byte
	// Comments is how many comments of the old file the new one carries.
	Comments int
	// Respelled are the include entries rewritten to their twins.
	Respelled []string
	// Dropped are the written values left out because they are the default.
	Dropped []DroppedDefault
	// Folded are the facts every model row of a provider repeated, written once in its
	// `every_model` (models.go).
	Folded []FoldedFact
	// Twins are the legacy `wire_id` rows written as `twin_of` their sibling (models.go, MF-D3).
	Twins []Twin
}

// DerivedVendor is one model the launch after migration carries a `vendor` for that the old
// config left unsaid: its id names the maker, which a readable row infers and a legacy row did
// not (model-facts-migration.md MF-D5). The proof compares it as derived; the transcript names it.
type DerivedVendor struct {
	Provider, Model, Vendor string
}

// Difference is one key on which the old and the converted config disagree.
type Difference struct {
	// Layer is "user config", "project config" or "user and project config merged".
	Layer string
	Path  string
	// Before and After are compact JSON, "(absent)" when the key is not there.
	Before, After string
}

// RecordState is what a migration does to one record only yolo reads.
type RecordState uint8

const (
	// RecordAbsent: there is no record, so nothing to respell.
	RecordAbsent RecordState = iota + 1
	// RecordRespell: the record holds exactly what the old files say; Apply rewrites it to what
	// the new files say, so the next launch sees no change.
	RecordRespell
	// RecordLeft: the record is left as it is, and that workspace prompts once.
	RecordLeft
)

// Record is one record only yolo reads (config-migration.md §4 item 5).
type Record struct {
	// Kind is "approval record" or "boot baseline".
	Kind  string
	Path  string
	State RecordState
	// Why says why a record is left.
	Why string

	before string
}

// MigrationPlan is everything one migration would do, and its proof.
type MigrationPlan struct {
	Graph       *Graph
	Targets     []Target
	Unreachable []Missing
	// Equal is the equality verdict: the effective config before and after, as canonical JSON.
	Equal       bool
	Differences []Difference
	// NotConverted are the old keys with no lossless TOML spelling here.
	NotConverted []NotConverted
	// CanonicalBytes is the size of the canonical JSON the verdict compared.
	CanonicalBytes int
	Records        []Record
	// Ignores keep each converted file out of commits as git kept its old name out.
	Ignores []Ignore
	// DerivedVendors are the models whose `vendor` the proof compared as derived (MF-D5).
	DerivedVendors []DerivedVendor

	canonical canonicalSet
}

// InequalityError is Plan's refusal: the converted files would not say what the old ones say.
type InequalityError struct{ Plan *MigrationPlan }

func (e *InequalityError) Error() string {
	var b strings.Builder
	b.WriteString("the converted config would not say what the old one says, so nothing was written:")
	for _, d := range e.Plan.Differences {
		fmt.Fprintf(&b, "\n  %s: %s\n    - %s\n    + %s", d.Layer, d.Path, d.Before, d.After)
	}
	for _, nc := range e.Plan.NotConverted {
		where := nc.Path
		if nc.File != "" {
			where = nc.File + ": " + nc.Path
		}
		fmt.Fprintf(&b, "\n  not converted: %s — %s", where, nc.Reason)
	}
	b.WriteString("\nChange the old files as each line says (or leave them; this yolo keeps reading them), then run `yolo config migrate` again.")
	return b.String()
}

// canonicalSet is one side of the equality check.
type canonicalSet struct {
	user, project, composed string
	// env is the ordered entry list the launch's env resolver walks over the merged read
	// (config.EnvEntries), whichever spelling carries it. The per-scope comparison above checks
	// the lowered keys; this checks what the launch resolves from them, without reading a file.
	env string
	// providers is the provider table a launch resolves from the merged read (providerTable):
	// the config layers compare the model rows with every fold undone, and this is what proves a
	// converted row or a fold means what the old rows meant.
	providers string
	// grants is what the user config grants each broker (brokerGrants): the user layer compares
	// `brokered` lowered to `allow`, and this is what proves the brokers read the same grants.
	grants string
}

// Plan converts every old file of g and proves the result says what the old files say. A plan
// that does not prove equal is returned with an *InequalityError, and Apply refuses it.
//
// A fold into `every_model` is made only while the proof holds (config-migration.md §4 item 4):
// a plan with folds whose provider table differs is planned again without them, and that plan's
// verdict is the answer.
//
// Inline env_sources variables converted to env.set are kept the same way: env.set applies after
// every file of every scope, so a later scope's files can make it mean something else, and a plan
// that converted them and does not prove equal in the environment is planned again with them
// left unconverted. A refusal elsewhere keeps the conversion, so it names only what it is about.
//
// A legacy `wire_id` twin is written as `twin_of` its sibling the same way (model-facts-migration.md
// MF-D3): a plan with twins whose provider table still differs once the folds are dropped is
// planned again with each twin keeping `wire_id`, first with the folds and then without.
func Plan(g *Graph) (*MigrationPlan, error) {
	opts := planOptions{fold: true, inlineEnv: true, twinOf: true}
	p, err := planWith(g, opts)
	var ineq *InequalityError
	if errors.As(err, &ineq) && p.differsIn(providerTableLayer) {
		twinned := p.twinned()
		if p.folded() {
			opts.fold = false
			p, err = planWith(g, opts)
		}
		if errors.As(err, &ineq) && twinned && p.differsIn(providerTableLayer) {
			opts.twinOf, opts.fold = false, true
			p, err = planWith(g, opts)
			if errors.As(err, &ineq) && p.folded() && p.differsIn(providerTableLayer) {
				opts.fold = false
				p, err = planWith(g, opts)
			}
		}
	}
	if errors.As(err, &ineq) && hasInlineEnv(g) && p.differsInEnv() {
		opts.inlineEnv = false
		p, err = planWith(g, opts)
	}
	return p, err
}

// planOptions are the conversions a plan makes only while the proof holds.
type planOptions struct {
	fold, inlineEnv, twinOf bool
}

// hasInlineEnv is whether a file being converted writes an inline variable entry in env_sources.
func hasInlineEnv(g *Graph) bool {
	for _, f := range g.Files {
		if f.Role != RoleConvert {
			continue
		}
		list, _ := f.doc.Values.Get("env_sources")
		entries, _ := list.([]any)
		for _, e := range entries {
			if _, isPath := e.(string); !isPath {
				return true
			}
		}
	}
	return false
}

// providerTableLayer is the equality check's layer for the provider table a launch resolves.
const providerTableLayer = "the provider table the launch resolves"

// envLayer is the equality check's layer for the environment a launch resolves.
const envLayer = "the environment the launch resolves"

func (p *MigrationPlan) twinned() bool {
	for _, t := range p.Targets {
		if len(t.Twins) > 0 {
			return true
		}
	}
	return false
}

func (p *MigrationPlan) folded() bool {
	for _, t := range p.Targets {
		if len(t.Folded) > 0 {
			return true
		}
	}
	return false
}

func (p *MigrationPlan) differsIn(layer string) bool {
	for _, d := range p.Differences {
		if d.Layer == layer {
			return true
		}
	}
	return false
}

// differsInEnv is whether a difference is in the environment a launch resolves, or in a key that
// spells part of it.
func (p *MigrationPlan) differsInEnv() bool {
	for _, d := range p.Differences {
		if d.Layer == envLayer || d.Path == "env_sources" || d.Path == "env" || strings.HasPrefix(d.Path, "env.") {
			return true
		}
	}
	return false
}

func planWith(g *Graph, opts planOptions) (*MigrationPlan, error) {
	p := &MigrationPlan{Graph: g, Unreachable: append([]Missing(nil), g.Unreachable...)}
	// Every shipped pack, for the provider table both sides are proved against: a pack's own
	// model aliases are what a fold into `every_model` could reach (models.go).
	packs := packload.Embedded()
	remotes := workspaceRemotes(g.Request.Workspace)
	view := userViewOf(g)
	newBytes := map[*File][]byte{}
	writers := writerCounts{}
	for _, f := range g.Files {
		switch f.Role {
		case RoleConvert:
			writers.add(lower(f.doc.Values, loweringContext{scope: f.Scope, remotes: remotes, user: view, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf}, f.Logical).values)
		default:
			writers.add(f.doc.Values)
		}
	}
	for _, f := range g.Files {
		switch f.Role {
		case RoleConvert:
			t, skipped, err := convertFile(f, remotes, view, writers, opts)
			if err != nil {
				return nil, err
			}
			p.NotConverted = append(p.NotConverted, skipped...)
			p.Targets = append(p.Targets, t)
			newBytes[f] = t.Bytes
		case RoleRespell:
			t, err := respellFile(f)
			if err != nil {
				return nil, err
			}
			p.Targets = append(p.Targets, t)
			newBytes[f] = t.Bytes
		default:
			newBytes[f] = f.Bytes
		}
	}
	if len(g.Old()) == 0 {
		p.Equal = true
		return p, nil
	}
	if err := p.preflight(); err != nil {
		return nil, err
	}
	before, err := readCanonical(g, oldTop(g), g.Request.Workspace, remotes, view, "", true, opts, packs)
	if err != nil {
		return nil, fmt.Errorf("read the old config: %w", err)
	}
	mirror, err := os.MkdirTemp("", "yolo-config-migrate-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(mirror)
	if resolved, err := filepath.EvalSymlinks(mirror); err == nil {
		mirror = resolved
	}
	userTop, workspace, err := writeMirror(mirror, g, newBytes)
	if err != nil {
		return nil, fmt.Errorf("stage the converted files: %w", err)
	}
	after, err := readCanonical(g, userTop, workspace, nil, nil, mirror, false, planOptions{}, packs)
	if err != nil {
		return nil, fmt.Errorf("the converted files would not read: %w; nothing was written", err)
	}
	p.canonical = after.set
	p.CanonicalBytes = len(after.set.composed)
	p.Differences = append(p.Differences, diffValues("user config", before.user, after.user)...)
	p.Differences = append(p.Differences, diffValues("project config", before.project, after.project)...)
	p.Differences = append(p.Differences, diffValues("user and project config merged", before.composed, after.composed)...)
	p.Differences = append(p.Differences, diffValues(envLayer, before.env, after.env)...)
	p.Differences = append(p.Differences, diffValues(providerTableLayer, before.providers, after.providers)...)
	p.Differences = append(p.Differences, diffValues(brokerGrantsLayer, before.grants, after.grants)...)
	p.DerivedVendors = derivedOnlyBefore(before.derived, after.derived)
	p.Equal = before.set == after.set
	if g.Request.Workspace != "" && len(g.project) > 0 {
		p.Records = planRecords(g.Request.Workspace)
	}
	if !p.Equal {
		return p, &InequalityError{Plan: p}
	}
	p.Ignores = planIgnores(g, p.Targets)
	return p, nil
}

func oldTop(g *Graph) string {
	if g.userTop == nil {
		return ""
	}
	return g.userTop.Logical
}

// convertFile makes one old file's TOML.
func convertFile(f *File, remotes []string, view *userView, writers writerCounts, opts planOptions) (Target, []NotConverted, error) {
	l := lower(f.doc.Values, loweringContext{scope: f.Scope, remotes: remotes, user: view, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf}, f.Logical)
	dropped := dropDefaults(l.values, writers)
	var folded []FoldedFact
	if opts.fold {
		folded = foldEveryModel(l.values)
	}
	var respelled []string
	if value, ok := l.values.Get("include_if_found"); ok {
		list, _ := value.([]any)
		out := make([]any, len(list))
		copy(out, list)
		for _, edge := range f.includes {
			if edge.child != nil && edge.child.Role == RoleConvert && edge.index < len(out) {
				out[edge.index] = twinName(edge.entry)
				respelled = append(respelled, edge.entry)
			}
		}
		l.values.Set("include_if_found", out)
	}
	comments := oldComments(f.doc)
	anchor := func(newPath []string) int {
		for n := len(newPath); n > 0; n-- {
			origin, ok := l.origins[strings.Join(newPath[:n], "\x00")]
			if !ok {
				continue
			}
			steps := make([]configdoc.Step, 0, len(origin)+len(newPath)-n)
			for _, k := range origin {
				steps = append(steps, configdoc.Step{Key: k})
			}
			for _, k := range newPath[n:] {
				steps = append(steps, configdoc.Step{Key: k})
			}
			if span, found, err := f.doc.Index.Member(steps...); err == nil && found {
				return span.Start
			}
			if span, found, err := f.doc.Index.Member(steps[:len(origin)]...); err == nil && found {
				return span.Start
			}
			return -1
		}
		return -1
	}
	data, err := encodeTOML(l.values, comments, anchor)
	if err != nil {
		return Target{}, nil, fmt.Errorf("%s: %w", f.Logical, err)
	}
	t := Target{File: f, Old: f.Physical, New: twinPath(f.Physical), Backup: f.Physical + ".pre-migrate",
		Bytes: data, Comments: len(comments), Respelled: respelled, Dropped: dropped, Folded: folded, Twins: l.twins}
	link, err := linkFor(f)
	if err != nil {
		return Target{}, nil, err
	}
	t.Link = link
	return t, l.skipped, nil
}

// linkFor plans the twin of a symlinked old name. A link to a link is refused: which of the two
// a twin should copy is not something a migration can know.
func linkFor(f *File) (*Link, error) {
	info, err := os.Lstat(f.Logical)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if filepath.Dir(f.Physical) != resolvedDir(f.Logical) {
			return nil, fmt.Errorf("%s does not resolve beside its own name; point its link at the file itself, then run `yolo config migrate` again", f.Logical)
		}
		return nil, nil
	}
	text, err := os.Readlink(f.Logical)
	if err != nil {
		return nil, err
	}
	hop := text
	if !filepath.IsAbs(hop) {
		hop = filepath.Join(filepath.Dir(f.Logical), hop)
	}
	if hopInfo, err := os.Lstat(hop); err != nil || hopInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a link to another link; point it at %s itself, then run `yolo config migrate` again", f.Logical, f.Physical)
	}
	newTarget := filepath.Join(filepath.Dir(text), twinName(filepath.Base(text)))
	if !filepath.IsAbs(text) && !strings.HasPrefix(text, "."+string(filepath.Separator)) && filepath.Dir(text) == "." {
		newTarget = twinName(text)
	}
	return &Link{Path: twinPath(f.Logical), Target: newTarget, Old: f.Logical, Text: text}, nil
}

func resolvedDir(path string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return filepath.Dir(path)
	}
	abs, _ := filepath.Abs(dir)
	return abs
}

// respellFile rewrites a TOML file's include entries that name a converted file, byte for byte
// everywhere else.
func respellFile(f *File) (Target, error) {
	data := append([]byte(nil), f.Bytes...)
	type splice struct {
		span configdoc.Span
		text []byte
	}
	var splices []splice
	var respelled []string
	for _, edge := range f.includes {
		if edge.child == nil || edge.child.Role != RoleConvert {
			continue
		}
		span, ok, err := f.doc.Index.Value(configdoc.Step{Key: "include_if_found"}, configdoc.Step{Index: edge.index, IsIndex: true})
		if err != nil || !ok {
			return Target{}, fmt.Errorf("%s: cannot locate include_if_found[%d]", f.Logical, edge.index)
		}
		text, err := configdoc.EncodeValue(twinName(edge.entry), configdoc.TOML)
		if err != nil {
			return Target{}, err
		}
		splices = append(splices, splice{span: span, text: text})
		respelled = append(respelled, edge.entry)
	}
	sort.Slice(splices, func(i, j int) bool { return splices[i].span.Start > splices[j].span.Start })
	for _, s := range splices {
		data = append(data[:s.span.Start:s.span.Start], append(append([]byte(nil), s.text...), data[s.span.End:]...)...)
	}
	return Target{File: f, Old: f.Physical, New: f.Physical, Backup: f.Physical + ".pre-migrate", Bytes: data,
		Comments: len(f.doc.Index.Comments()), Respelled: respelled}, nil
}

// preflight refuses, before anything is written, every collision a write would meet.
func (p *MigrationPlan) preflight() error {
	claimed := map[string]string{}
	claim := func(path, by string) error {
		if prior, ok := claimed[path]; ok {
			return fmt.Errorf("%s and %s would both write %s; rename one of them, then run `yolo config migrate` again", prior, by, path)
		}
		claimed[path] = by
		return nil
	}
	var problems []string
	exists := func(path string) bool { _, err := os.Lstat(path); return err == nil }
	for _, t := range p.Targets {
		if t.New != t.Old {
			if err := claim(t.New, t.File.Logical); err != nil {
				return err
			}
			if exists(t.New) {
				problems = append(problems, t.New+" already exists")
			}
		}
		if err := claim(t.Backup, t.File.Logical); err != nil {
			return err
		}
		if exists(t.Backup) {
			problems = append(problems, t.Backup+" already exists")
		}
		if t.Link != nil {
			if err := claim(t.Link.Path, t.File.Logical); err != nil {
				return err
			}
			if exists(t.Link.Path) {
				problems = append(problems, t.Link.Path+" already exists")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("nothing was written, because %s; move each aside (for example to <name>.backup), then run `yolo config migrate` again", strings.Join(problems, ", and "))
	}
	return nil
}

// writeMirror stages the converted graph under root at each file's own absolute path, with links
// wherever the reader's join of an entry differs from the file, and returns the staged user
// top-level file and workspace.
func writeMirror(root string, g *Graph, content map[*File][]byte) (userTop, workspace string, err error) {
	at := func(path string) string { return filepath.Join(root, path) }
	newPhysical := func(f *File) string {
		if f.Role == RoleConvert {
			return twinPath(f.Physical)
		}
		return f.Physical
	}
	newLogical := func(f *File) string {
		if f.Role == RoleConvert {
			return twinPath(f.Logical)
		}
		return f.Logical
	}
	link := func(name string, f *File) error {
		if at(name) == at(newPhysical(f)) {
			return nil
		}
		if _, err := os.Lstat(at(name)); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(at(name)), 0o755); err != nil {
			return err
		}
		return os.Symlink(at(newPhysical(f)), at(name))
	}
	for _, f := range g.Files {
		if err := os.MkdirAll(filepath.Dir(at(newPhysical(f))), 0o755); err != nil {
			return "", "", err
		}
		if err := os.WriteFile(at(newPhysical(f)), content[f], 0o644); err != nil {
			return "", "", err
		}
	}
	for _, f := range g.Files {
		base := filepath.Dir(newPhysical(f))
		if f.Via == "" {
			base = filepath.Dir(newLogical(f))
		}
		for _, edge := range f.includes {
			if edge.child == nil {
				continue
			}
			entry := edge.entry
			if edge.child.Role == RoleConvert {
				entry = twinName(entry)
			}
			if err := link(filepath.Join(base, entry), edge.child); err != nil {
				return "", "", err
			}
		}
		if f.Via == "" {
			if err := link(newLogical(f), f); err != nil {
				return "", "", err
			}
		}
	}
	if g.userTop != nil {
		userTop = at(newLogical(g.userTop))
	}
	if g.Request.Workspace != "" {
		workspace = at(g.Request.Workspace)
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			return "", "", err
		}
	}
	return userTop, workspace, nil
}

// sideRead is one side of the equality check, lowered and canonical.
type sideRead struct {
	user, project, composed *jsonx.OrderedMap
	// env is {"entries": config.EnvEntries(merged read)}: the launch's env, in the one form
	// both spellings resolve through.
	env *jsonx.OrderedMap
	// providers is the provider table a launch resolves from the merged read (providerTable).
	providers *jsonx.OrderedMap
	// derived are the vendors providerTable filled in from an id that names its maker (MF-D5).
	derived []DerivedVendor
	// grants is what the user config grants each broker (brokerGrants).
	grants *jsonx.OrderedMap
	set    canonicalSet
}

// launchEnv is what the launch's env resolver walks over a merged read, as a map diffValues can
// name a difference in.
func launchEnv(composed *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	out.Set("entries", mergeAdjacentTables(config.EnvEntries(composed)))
	return out
}

// mergeAdjacentTables merges each run of adjacent inline tables into one, a later name winning:
// two tables in a row resolve as one, which is how env.set followed by env.unset says what one
// inline entry said.
func mergeAdjacentTables(entries []any) []any {
	var out []any
	for _, e := range entries {
		table, isTable := e.(*jsonx.OrderedMap)
		if !isTable {
			out = append(out, e)
			continue
		}
		if n := len(out); n > 0 {
			if prev, ok := out[n-1].(*jsonx.OrderedMap); ok {
				for _, k := range table.Keys() {
					v, _ := table.Get(k)
					prev.Set(k, v)
				}
				continue
			}
		}
		out = append(out, jsonx.DeepCopyMap(table))
	}
	return out
}

// loweredRoots are the roots the comparison checks per scope only: their lowering depends on the
// scope that wrote them, which the merged read no longer knows.
var loweredRoots = []string{"env_sources", "env", "brokered", "requests", "allow", "loopholes", "profile", "profiles", "active_provider"}

// readCanonical reads one side through the launch's readers (config.ReadMigrationGraph). The old
// side (remotes non-nil or mirror empty) is lowered; the new side is the readers' output with
// the staging root taken back out of every anchored path.
func readCanonical(g *Graph, userTop, workspace string, remotes []string, view *userView, mirror string, lowerOld bool, opts planOptions, packs []*packload.Pack) (*sideRead, error) {
	read, err := config.ReadMigrationGraph(userTop, workspace)
	if err != nil {
		return nil, err
	}
	brokers, err := shippedBrokers()
	if err != nil {
		return nil, fmt.Errorf("read the shipped broker declarations: %w", err)
	}
	s := &sideRead{}
	if lowerOld {
		s.user, _ = canonicalOld(read.User, loweringContext{scope: ScopeUser, remotes: remotes, user: view, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf})
		s.project, _ = canonicalOld(read.Workspace, loweringContext{scope: ScopeProject, remotes: remotes, user: view, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf})
		twins := fileTwins(map[string]bool{}, read.User, loweringContext{scope: ScopeUser, remotes: remotes, user: view, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf})
		twins = fileTwins(twins, read.Workspace, loweringContext{scope: ScopeProject, remotes: remotes, user: view, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf})
		s.composed, _ = canonicalOld(read.Composed, loweringContext{scope: ScopeProject, remotes: remotes, user: view, merged: true, inlineEnv: opts.inlineEnv, twinOf: opts.twinOf, twinsOnly: twins})
		// The OLD spelling as the launch reads it, not its lowering: the proof is that the
		// converted files resolve to the env the old ones resolve to.
		s.env = launchEnv(read.Composed)
		s.providers, s.derived = providerTable(withMovedProviderKeys(read.Composed, s.composed), packs)
		// The OLD user config as the brokers read it, not its lowering.
		s.grants = brokerGrants(read.User, brokers)
	} else {
		s.user = unmirror(read.User, mirror).(*jsonx.OrderedMap)
		s.project = unmirror(read.Workspace, mirror).(*jsonx.OrderedMap)
		s.composed = unmirror(read.Composed, mirror).(*jsonx.OrderedMap)
		s.env = launchEnv(s.composed)
		s.providers, s.derived = providerTable(s.composed, packs)
		s.grants = brokerGrants(s.user, brokers)
	}
	for _, m := range []*jsonx.OrderedMap{s.user, s.project, s.composed} {
		m.Delete(convertLaterKey)
	}
	for _, root := range loweredRoots {
		s.composed.Delete(root)
	}
	// A default written or left out is one fact, so the proof compares both sides with every
	// default filled, by the table the readers answer from.
	for _, m := range []*jsonx.OrderedMap{s.user, s.project, s.composed} {
		config.ApplyWrittenDefaults(m)
		unfoldEveryModel(m)
	}
	for _, pair := range []struct {
		m   *jsonx.OrderedMap
		out *string
	}{{s.user, &s.set.user}, {s.project, &s.set.project}, {s.composed, &s.set.composed}, {s.env, &s.set.env}, {s.providers, &s.set.providers}, {s.grants, &s.set.grants}} {
		text, err := config.SnapshotJSON(pair.m)
		if err != nil {
			return nil, err
		}
		*pair.out = text
	}
	return s, nil
}

// withMovedProviderKeys is the old side's merged read as the provider table is proved from: its
// own provider rows, plus each provider-level key the lowering moved there from elsewhere (a
// profile's `model` and `variants`, RC-D32) that the old provider does not write. Those moves are
// proved by the composed layer; this layer proves what the models say, so it must not count them
// twice as a difference.
func withMovedProviderKeys(read, lowered *jsonx.OrderedMap) *jsonx.OrderedMap {
	moved, ok := mapAt(lowered, "providers")
	if !ok {
		return read
	}
	out := jsonx.DeepCopyMap(read)
	providers, ok := mapAt(out, "providers")
	if !ok {
		providers = jsonx.NewOrderedMap()
		out.Set("providers", providers)
	}
	for _, name := range moved.Keys() {
		from, ok := mapAt(moved, name)
		if !ok {
			continue
		}
		to, ok := mapAt(providers, name)
		if !ok {
			to = jsonx.NewOrderedMap()
			providers.Set(name, to)
		}
		for _, key := range from.Keys() {
			if key == "models" || key == everyModelKey {
				continue
			}
			if _, written := to.Get(key); written {
				continue
			}
			v, _ := from.Get(key)
			to.Set(key, v)
		}
	}
	return out
}

func unmirror(v any, root string) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		out := jsonx.NewOrderedMap()
		if t == nil {
			return out
		}
		for _, k := range t.Keys() {
			item, _ := t.Get(k)
			out.Set(k, unmirror(item, root))
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = unmirror(item, root)
		}
		return out
	case string:
		if strings.HasPrefix(t, root+string(filepath.Separator)) {
			return strings.TrimPrefix(t, root)
		}
		return t
	default:
		return v
	}
}

// diffValues lists, key by key, where before and after disagree.
func diffValues(layer string, before, after *jsonx.OrderedMap) []Difference {
	var out []Difference
	var walk func(path []string, b, a any, hasB, hasA bool)
	walk = func(path []string, b, a any, hasB, hasA bool) {
		bm, bIsMap := b.(*jsonx.OrderedMap)
		am, aIsMap := a.(*jsonx.OrderedMap)
		if hasB && hasA && bIsMap && aIsMap {
			keys := map[string]bool{}
			for _, k := range bm.Keys() {
				keys[k] = true
			}
			for _, k := range am.Keys() {
				keys[k] = true
			}
			sorted := make([]string, 0, len(keys))
			for k := range keys {
				sorted = append(sorted, k)
			}
			sort.Strings(sorted)
			for _, k := range sorted {
				bv, bh := bm.Get(k)
				av, ah := am.Get(k)
				walk(append(append([]string(nil), path...), k), bv, av, bh, ah)
			}
			return
		}
		bs, as := "(absent)", "(absent)"
		if hasB {
			bs, _ = jsonx.DumpsSnapshot(b)
		}
		if hasA {
			as, _ = jsonx.DumpsSnapshot(a)
		}
		if bs != as {
			out = append(out, Difference{Layer: layer, Path: dotted(path), Before: describeOr(b, hasB), After: describeOr(a, hasA)})
		}
	}
	walk(nil, before, after, true, true)
	return out
}

func describeOr(v any, has bool) string {
	if !has {
		return "(absent)"
	}
	return describe(v)
}

// planRecords decides, per record, whether Apply respells it.
func planRecords(workspace string) []Record {
	var out []Record
	approval := Record{Kind: "approval record", Path: config.ApprovalSnapshotPath(workspace)}
	switch {
	case config.InJail():
		approval.State, approval.Why = RecordLeft, "it is the host's, outside this jail; the next launch asks once"
	default:
		approval = decideRecord(approval, config.ReadApprovalRecord, config.ApprovalRecordPart, workspace)
	}
	out = append(out, approval)
	boot := decideRecord(Record{Kind: "boot baseline", Path: config.WorkspaceConfigBootPath(workspace)},
		config.ReadBootBaseline, config.BootBaselinePart, workspace)
	out = append(out, boot)
	return out
}

func decideRecord(r Record, read func(string) (string, bool, error), current func(string) (string, error), workspace string) Record {
	text, exists, err := read(workspace)
	switch {
	case err != nil:
		r.State, r.Why = RecordLeft, "it could not be read ("+err.Error()+"); the next launch asks once"
		return r
	case !exists:
		r.State = RecordAbsent
		return r
	}
	now, err := current(workspace)
	switch {
	case err != nil:
		r.State, r.Why = RecordLeft, "the old config could not be read the way a launch reads it ("+err.Error()+"); the next launch asks once"
	case now != text:
		r.State, r.Why = RecordLeft, "it already differs from the config on disk, so the next launch asks about that change, with the new spelling in the same prompt"
	default:
		r.State, r.before = RecordRespell, text
	}
	return r
}

var errNotEqual = errors.New("this plan did not prove the converted config equal to the old one, so it writes nothing; run `yolo config migrate` again to see why")

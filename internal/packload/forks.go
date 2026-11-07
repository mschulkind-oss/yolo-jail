package packload

// forks.go is the pack-set half of the fork route (docs/design/forked-programs-as-packs.md): which
// forks a selection carries, the REWRITE that gives each fork's base its delivery, and how a fork
// appears in a footprint.
//
// # Why a rewrite, and why at the selection
//
// A fork claims no name (OQ-FP5): the base pack keeps its bin, and the fork supplies the bytes.
// So every reader that asks "how is `pi` delivered?" must hear the fork's answer, and there are
// many of them, each reading one pack's programs at a time — the launcher generator, the catalog,
// the host floor, `yolo capture`, the dep probe (FP-D5). Teaching each one a fork arm would leave
// the next reader added without one, delivering the base's UPSTREAM program under the fork's name.
// Rewriting a COPY of the base's program where the selected set is final means none of them has to
// know a fork exists: they read the base's program, and it carries the fork's delivery.
//
// Where the set is final is two places, and both call ApplyForks: the one selection function
// (config.SelectPacks), which every host verb and the launch read, and the in-jail loader
// (entrypoint's loadPackRoot), which reads the staged tree the launch delivered. The staged tree
// holds the base's pack.json unchanged, so the jail repeats the rewrite rather than trusting a
// rewritten file.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// forkClaimDetailPrefix opens a fork claim's Detail; Claim.DisclosureSentence keys its sentence
// on it, as it keys an installer's on "installer: ".
const forkClaimDetailPrefix = "fork build: "

// ForkClaimTarget is a fork's footprint target: its bin, qualified by the base pack it forks.
//
// Qualified so the `program` kind's exclusive loop keys it apart from the base's own claim on the
// bin — a fork claims no name (OQ-FP5), so the two must never read as two owners of one — while
// two forks of one base still share a target, and so collide there as they collide at the launch
// (ApplyForks refuses a second fork of one program).
func ForkClaimTarget(bin, base string) string {
	return bin + " (fork of " + base + ")"
}

// patchedForkClaimDetailPrefix opens a PATCHED fork claim's Detail, which
// Claim.DisclosureSentence keys its own sentence on: a patched fork follows its upstream, where a
// plain fork's build stays at the commit its pin names.
const patchedForkClaimDetailPrefix = "patched fork build: "

// forkClaimDetail is a fork claim's Detail: the source address and the build command, verbatim,
// so two forks that differ in either render as two lines. A PATCHED fork's names its series too
// (docs/reference/patched-forks.md §7) — the directory and the follow rule, and the patch count and
// series digest when root's series reads — since two series of one upstream would otherwise
// render identically.
func forkClaimDetail(root string, c packdecl.Contribution) string {
	if !c.IsPatchedFork() {
		return forkClaimDetailPrefix + c.Source + ", " + BuildLineQuote(c.Build)
	}
	follow, err := packsrc.ParseFollow(c.Follow)
	rule := c.Follow
	if err == nil {
		rule = follow.String()
	}
	series := "the series in " + c.Patches
	if c.PatchBranch != "" {
		series = "the patch branch " + c.PatchBranch
	} else if s, err := packsrc.ReadSeries(root, c.Patches); err == nil {
		series = fmt.Sprintf("%d %s in %s (series %s)", s.Len(), plural(s.Len(), "patch", "patches"),
			c.Patches, s.ShortDigest())
	}
	return patchedForkClaimDetailPrefix + c.Source + " + " + series + ", following " + rule +
		", " + BuildLineQuote(c.Build)
}

// Fork is one fork a pack set carries: the fork pack's own `via: "source"` contribution, read off
// its manifest (not off the rewritten base, which is where a reader of the PROGRAM looks).
type Fork struct {
	// Pack is the fork pack's name.
	Pack string
	// Base is the pack whose program it forks (`fork_of`).
	Base string
	// Bin is the program's bin, which is the base's.
	Bin string
	// Source, Build and Produces are the fork's delivery, verbatim from its manifest.
	Source, Build string
	Produces      []string
	// Platforms is where the fork builds (`platforms`), nil for everywhere.
	Platforms []string
	// Root is the fork pack's root directory, which a patched fork's series is read from.
	Root string
	// Patches and Follow are a PATCHED fork's (docs/reference/patched-forks.md): the series directory,
	// relative to Root, and the follow rule as written. Both "" for a plain fork.
	Patches, Follow string
	// PatchBranch is a PATCH BRANCH's address (docs/reference/patched-forks.md §3.5), in Patches' place: the fork is
	// patched, its series the patch branch's commit as the last check resolved it. "" otherwise.
	PatchBranch string
	// PatchBranchDir is the directory a RELATIVE patch branch (packsrc.ParseFrom) is resolved against: the
	// declaring directory, or its host-to-jail workspace mapping.
	PatchBranchDir string
	// PatchBranchRepoIdentity is the host-established identity of this relative branch's repository,
	// carried from the selected pack-tree record into the jail. Empty on the host, where it is derived.
	PatchBranchRepoIdentity string
	// PackBases is the base of every fork the fork pack declares, this one's included, and in turn
	// every base those bases fork (forkBaseClosure): the packs a build sealed to that pack must
	// carry, since a selection holding a fork whose base it lacks is refused (ApplyForks) on the
	// host and again by the jail's own loader, and a base that forks another pack's program is such
	// a fork. A base yolo ships joins by itself; a configured one is in the selection only when the
	// seal names it (docs/reference/pi-extensions.md PPX-D39).
	PackBases []string
	// NodeFloor is the base program's declared Node floor. A cached source build's env-node entrypoint
	// remains compatible only when the selected base keeps its runtime floor.
	NodeFloor string
	// Into is a PATCHED EXTENSION's home-relative landing (docs/reference/pi-extensions.md;
	// patchedtrees.go), "" for every fork of a program. With it set the value is an extension:
	// Bin is its name, the last segment of Into; Base is ""; and Produces are tree-relative.
	Into string
	// Owner is a patched extension's OWNING AGENT PACK (PPX-D4), "" when no list entry names its
	// tree; OwnerForks are the selection's fork packs of the owner's programs, whose
	// `agent_updates` holds it too (PPX-D9).
	Owner      string
	OwnerForks []string
	// ListedInJail and ListedAtHost say where the owner's list entry naming the tree reaches: a
	// `config-list` reaches both, an autonomous posture list every jail, a guarded one the host
	// alone (PPX-D12: the launchers stop only at a notch the entry reaches).
	ListedInJail, ListedAtHost bool
	// Fallback is an UNMODIFIED EXTENSION's raw list entry (docs/design/pi-extension-store-builds.md
	// XB-D7), "" when it declares none and always for a patched one: what takes the tree's list entry's
	// place in the contributing pack's lists wherever a launch hands no tree (ApplyTreeFallbacks).
	Fallback string
	// From and Run are a PACK-TREE PROGRAM's (packtree.go, `via: "pack"`): its source directory,
	// relative to Root, and its program file, relative to From. Both "" for every fork. With Run set
	// the value is a pack-tree program: Base is "" (it forks nothing), Source is "", Produces are
	// packdecl.PackTreeProduces, and its build's identity is BuildSource, its tree digest and
	// PackTreeRecipe (packtree.go).
	From, Run string
	// Prebuilts is a pack-tree program's declared `prebuilt` map, by "<goos>/<goarch>", nil when it
	// declares none (packdecl.PrebuiltBuild).
	Prebuilts map[string]packdecl.PrebuiltBuild
	// Prebuilt is the artifact THIS delivery takes (packtree.go, PackTreeResolve): set when the
	// resolver chose the prebuilt rung for a platform, so the build act fetches and verifies it in
	// place of building From. nil for a build, and for every fork.
	Prebuilt *packdecl.PrebuiltBuild
	// PrebuiltWhy is why the prebuilt rung was chosen, for the disclosure line: its pack ships no
	// From, or the user opted into it (BG-D18). "" unless Prebuilt is set.
	PrebuiltWhy string
	// Checkout is the user's own CHECKOUT this delivery builds in place of From
	// (`programs.overrides.<bin>.path`, docs/design/pack-buildable-programs.md §5, BG-D19): the
	// path with its symlinks resolved, digested and copied exactly as From would be, under the
	// pack's own build and run. "" for a build of the pack's tree, and for every fork.
	Checkout string
	// Upstream marks the UPSTREAM BUILD of a patched fork or patched extension
	// (docs/reference/patched-forks.md §8.5, "Skip the patches", YOLO_SKIP_PATCHES): the upstream's newest list entry
	// built under the fork's own build recipe with no series applied. Its Patches and PatchBranch are
	// empty, its series is the empty one, and its Key carries UpstreamKeySuffix, so its check record,
	// build lock, receipts and reaps are its own and never the series'. Made by UpstreamBuild only.
	Upstream bool
}

// UpstreamKeySuffix ends an upstream build's Key: "<pack>/<bin>#upstream". No pack or bin name holds
// a '#', so it never names a fork of its own.
const UpstreamKeySuffix = "#upstream"

// UpstreamBuild is f's UPSTREAM BUILD (Fork.Upstream): f with no series, under an identity of its own.
func (f Fork) UpstreamBuild() Fork {
	f.Upstream = true
	f.Patches, f.PatchBranch = "", ""
	return f
}

// SeriesKey is the key of the fork as its manifest declares it, "<pack>/<bin>", which an upstream
// build's lines and hatches name: its Key without UpstreamKeySuffix.
func (f Fork) SeriesKey() string { return f.Pack + "/" + f.Bin }

// Unmodified reports whether f is an UNMODIFIED EXTENSION: a built tree with no series, the upstream
// at one commit or version (XB-D1). Its series is the empty one (ReadSeries). An upstream build is not
// one: its manifest declares a series, which this build leaves out.
func (f Fork) Unmodified() bool {
	return f.IsTree() && f.Patches == "" && f.PatchBranch == "" && !f.Upstream
}

// Npm reports whether f follows an npm package rather than a git repository (XB-D5).
func (f Fork) Npm() bool { return packsrc.IsNpmSource(f.Source) }

// FollowsUpstream reports whether f is checked and advanced against its upstream by the ratchet
// (patched-forks.md PF-D8, pi-extensions.md PPX-D1): a patched fork, and every built tree,
// patched or not. A plain fork's pin moves only by `yolo pack update` (FP-D18).
func (f Fork) FollowsUpstream() bool { return f.Patched() || f.IsTree() }

// Key is the fork's identity in the fork lock and everywhere a fork is named by one string:
// "<fork pack>/<bin>" (FP-D7). A pack may fork several programs, each its own key. For a patched
// fork it is the OWNER KEY too (PF-D22): its check record, its lock and its explicit acts use it.
func (f Fork) Key() string {
	if f.Upstream {
		return f.SeriesKey() + UpstreamKeySuffix
	}
	return f.SeriesKey()
}

// Patched reports whether the fork is a PATCHED fork: one that declares `patches`, or a `patch_branch`
// — or the upstream build of one, which goes through the same advance with the empty series.
func (f Fork) Patched() bool { return f.Patches != "" || f.PatchBranch != "" || f.Upstream }

// RebaseRecipe is what the printed git recipe that rebases f's series needs (packsrc.RebaseRecipe;
// patched-forks.md §8.4, PF-D85): its upstream, the series' base, and the series directory when it
// is the user's own — not a copy in yolo's state directory (a fetched or shipped pack, a
// process's pack tree) or a tree a launch staged into a jail, which no act writes.
func (f Fork) RebaseRecipe() packsrc.RebaseRecipe {
	r := packsrc.RebaseRecipe{Repo: packsrc.CheckRepo(f.Source), Pack: f.Pack, Rel: f.Patches,
		Dir: f.Pack + "-" + f.Bin + "-rebase"}
	if f.PatchBranch != "" {
		// A PATCH BRANCH is repaired in its own repository, by a merge (packsrc.RebaseRecipe.Lines).
		a, err := f.PatchBranchAddr()
		if err != nil || a.Repo == "" {
			// A RELATIVE PATCH BRANCH THIS PROCESS CANNOT RESOLVE (a pack read through a copy) is still
			// repaired by a merge: the recipe names the repository as the manifest writes it.
			a, _ = packsrc.ParseFrom(f.PatchBranch, "")
			a.Repo = strings.SplitN(f.PatchBranch, "?", 2)[0]
		}
		r.PatchBranchRepo, r.PatchBranchRef, r.Dir = a.Repo, a.Ref, f.Pack+"-"+f.Bin+"-patch-branch"
		return r
	}
	if s, err := f.ReadSeries(); err == nil {
		r.Base = s.Base
	}
	if f.Root != "" && !notUsersPackRoot(f.Root) {
		r.Patches = filepath.Join(f.Root, filepath.FromSlash(f.Patches))
	}
	return r
}

// PatchBranchAddr is the patch branch's address, a relative locator resolved against PatchBranchDir
// (packsrc.ParseFrom): the repository it names, or why it names none.
func (f Fork) PatchBranchAddr() (packsrc.Addr, error) {
	return packsrc.ParseFrom(f.PatchBranch, f.PatchBranchDir)
}

// RefNotResolvedLine is what a reader that resolved none of f's git refs says of it, dir being the pack's
// directory to lint: the default (offline) `yolo pack lint`, and `yolo check` when no check record holds
// them. `--online` resolves them, in a scratch mirror.
func (f Fork) RefNotResolvedLine(dir string) string {
	return f.Label() + ": ref not resolved (offline) — `yolo pack lint --online " + shquote.QuoteDisplay(dir) + "` checks it"
}

// patchBranchDirOf is the directory a pack's relative patch branch is resolved against: its mapped
// host source directory in a jail, otherwise the directory it was read from.
func patchBranchDirOf(p *Pack, contribution string) string {
	if p != nil {
		if mapping, ok := p.PatchBranchMappings[p.StagedSlug()+"/"+contribution]; ok && mapping.Mapped {
			return mapping.Directory
		}
		if p.SourceRoot != "" {
			return p.SourceRoot
		}
		return p.Root
	}
	return ""
}

func patchBranchRepositoryOf(p *Pack, contribution string) string {
	if p == nil {
		return ""
	}
	return p.PatchBranchMappings[p.StagedSlug()+"/"+contribution].Repository
}

// notUsersPackRoot reports whether a pack root is a copy yolo made rather than the user's own
// directory: under yolo's state directory (the pack store, the embedded packs), a process's pack
// tree, or the tree a launch staged for a jail (YOLO_PACK_ROOT).
func notUsersPackRoot(root string) bool {
	under := func(dir string) bool {
		rel, err := filepath.Rel(dir, root)
		return dir != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return under(paths.GlobalStorage()) || under(os.Getenv("YOLO_PACK_ROOT")) ||
		strings.Contains(root, string(filepath.Separator)+EmbeddedFallbackPrefix)
}

// ReadSeries reads a patched fork's series from its pack, once (packsrc.ReadSeries): every reader
// of the series' bytes reads them through here and digests what it read. An UNMODIFIED EXTENSION's
// is the empty series (packsrc.EmptySeries), which every entry of its upstream fits.
//
// A PATCH BRANCH's series is its commit as the last check resolved it, read from the check record in the
// machine's pack store with no git (packsrc.PatchBranchSeries); before any check resolved it, the
// unresolved patch branch (packsrc.UnresolvedPatchBranchSeries, Series.Unresolved), which no walk runs on and
// whose recipe no build carries. ReadSeriesFrom reads another store's record.
func (f Fork) ReadSeries() (*packsrc.Series, error) { return f.ReadSeriesFrom(nil) }

// DeclaredSeries is the series as the manifest declares it, for an identity that must not move under
// a check: ReadSeries for a patch series, whose bytes are the pack's; for a PATCH BRANCH, the branch's
// address alone (packsrc.UnresolvedPatchBranchSeries), since its commit is what the check itself
// resolves and records, so reading it would change the identity in the middle of the run that checks.
func (f Fork) DeclaredSeries() (*packsrc.Series, error) {
	if f.PatchBranch != "" {
		return packsrc.UnresolvedPatchBranchSeries(f.PatchBranch, f.PatchBranchDir), nil
	}
	return f.ReadSeries()
}

// ReadSeriesFrom is ReadSeries reading a patch branch's record from store, the machine's pack store when nil.
func (f Fork) ReadSeriesFrom(store *packsrc.Store) (*packsrc.Series, error) {
	if f.Upstream {
		return packsrc.EmptySeries(), nil // the upstream build applies nothing
	}
	if f.PatchBranch != "" {
		if store == nil {
			store = &packsrc.Store{Dir: paths.PacksDir()}
		}
		s, err := store.ReadPatchBranchWithIdentity(f.Key(), f.PatchBranch, f.PatchBranchDir,
			f.PatchBranchRepoIdentity, f.Source)
		switch {
		case packsrc.IsPatchBranchRecordError(err):
			// A RECORD THAT CANNOT BE READ is said as that, never as "not fetched yet": an act that
			// checks starts it over and reads the branch as unresolved (UnresolvedPatchBranchSeries).
			return nil, err
		case err != nil:
			// NOT RESOLVED HERE YET: the unresolved patch branch, whose recipe no build carries; the
			// next check resolves it.
			return packsrc.UnresolvedPatchBranchSeries(f.PatchBranch, f.PatchBranchDir), nil
		}
		return s, nil
	}
	if f.Unmodified() {
		return packsrc.EmptySeries(), nil
	}
	if !f.Patched() {
		return nil, fmt.Errorf("fork %s declares no patch series", f.Key())
	}
	if f.IsTree() {
		return packsrc.ReadTreeSeries(f.Root, f.Patches) // an extension's remedies (PPX-D37)
	}
	return packsrc.ReadSeries(f.Root, f.Patches)
}

// CheckWant is the check request for a patched fork whose series s was read: its owner key, its
// upstream and follow rule, and the series' base.
func (f Fork) CheckWant(s *packsrc.Series) packsrc.PatchedWant {
	w := packsrc.PatchedWant{Owner: f.Key(), Source: f.Source, Follow: f.Follow, PatchBranch: f.PatchBranch,
		PatchBranchDir: f.PatchBranchDir, PatchBranchRepoIdentity: f.PatchBranchRepoIdentity}
	if s != nil && f.PatchBranch == "" {
		w.Base = s.Base // a patch branch's base is the check's to resolve
	}
	return w
}

// Forks lists every fork the packs carry, in pack order and then declaration order.
func Forks(packs []*Pack) []Fork {
	var out []Fork
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		bases := forkBaseClosure(packs, p)
		for _, c := range p.Decl.Contributions() {
			if !c.IsFork() {
				continue
			}
			base := resolveForkBase(packs, c.ForkOf)
			out = append(out, Fork{
				Pack: p.Name, Base: base, Bin: c.Bin, Source: c.Source, Build: c.Build,
				Produces:  append([]string(nil), c.Produces...),
				Platforms: append([]string(nil), c.Platforms...),
				Root:      p.Root, Patches: c.Patches, Follow: c.Follow, PatchBranch: c.PatchBranch,
				PatchBranchDir:          patchBranchDirOf(p, c.ExtensionName()),
				PatchBranchRepoIdentity: patchBranchRepositoryOf(p, c.ExtensionName()),
				PackBases:               slices.Clone(bases),
				NodeFloor:               baseProgramNodeFloor(packs, base, c.Bin),
			})
		}
	}
	return out
}

func baseProgramNodeFloor(packs []*Pack, base, bin string) string {
	for _, p := range packs {
		if p == nil || p.Name != base || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Provides() && c.Bin == bin {
				return c.NodeFloor
			}
		}
	}
	return ""
}

// resolveForkBase is the selected pack a fork's `fork_of` names. `fork_of` is a bare pack name
// (packdecl refuses a "/"), while a configured pack's Name is its handle, `<source>/<pack>` —
// so a base is matched first by its whole Name (a shipped pack's, which is bare), then by its own
// word, the handle's last segment, which is the author's name for the pack (packsrc's handleScope)
// and the only spelling a fork author can write, since the source is the user's choice. A word two
// selected packs share resolves to neither, and is returned as written; ApplyForks names the
// packs that share it.
func resolveForkBase(packs []*Pack, base string) string {
	matches := forkBaseCandidates(packs, base)
	if len(matches) == 1 {
		return matches[0]
	}
	return base
}

// forkBaseCandidates is the distinct Names of the selected packs base can name: the one whose Name
// is base, else every pack whose own word is base.
func forkBaseCandidates(packs []*Pack, base string) []string {
	var own []string
	seen := map[string]bool{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		if p.Name == base {
			return []string{base}
		}
		if packOwnWord(p.Name) == base && !seen[p.Name] {
			seen[p.Name] = true
			own = append(own, p.Name)
		}
	}
	return own
}

// packOwnWord is a pack Name's last "/" segment: a handle's own word, a bare name itself.
func packOwnWord(name string) string {
	return name[strings.LastIndex(name, "/")+1:]
}

// forkBases is the distinct base pack names p's forks name, in declaration order: the packs a
// fork brings into the launch the way an unconditional `needs` entry does (ResolveNeeds).
func forkBases(p *Pack) []string {
	if p == nil || p.Decl == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range p.Decl.Contributions() {
		if c.IsFork() && !seen[c.ForkOf] {
			seen[c.ForkOf] = true
			out = append(out, c.ForkOf)
		}
	}
	return out
}

// forkBaseClosure is forkBases(p), then every base's own forkBases among packs, and so on, in the
// order a breadth-first walk meets them, with p itself left out: the packs a selection narrowed to p
// must hold for ApplyForks to accept it, since each base that forks a configured pack's program
// needs that pack in turn (Fork.PackBases). A base not among packs ends its branch: a pack yolo
// ships joins a selection by itself, with what it needs (ResolveNeeds). A cycle ends at the first
// pack met twice.
func forkBaseClosure(packs []*Pack, p *Pack) []string {
	byName := map[string]*Pack{}
	for _, q := range packs {
		if q != nil {
			if _, seen := byName[q.Name]; !seen {
				byName[q.Name] = q
			}
		}
	}
	var out []string
	seen := map[string]bool{p.Name: true}
	for queue := []*Pack{p}; len(queue) > 0; queue = queue[1:] {
		for _, declared := range forkBases(queue[0]) {
			base := resolveForkBase(packs, declared)
			if seen[base] {
				continue
			}
			seen[base] = true
			out = append(out, base)
			if q := byName[base]; q != nil {
				queue = append(queue, q)
			}
		}
	}
	return out
}

// ApplyForks returns packs with every fork applied: each base pack a fork names is replaced, at
// its own position, by a COPY whose program of the fork's bin carries the fork's delivery
// (forkedProgram). Every other pack, the fork packs included, is returned as it came. The input is
// never modified — an embedded pack is one shared value per process (packload.Embedded), so an
// in-place rewrite would leak the fork into every later selection in the same process.
//
// It refuses, naming each, the shapes FP-D5 lists: a fork whose base is not in the set, a base
// that declares no program by the fork's bin, a pack forking itself, and two forks of one program
// — the last as the launch refuses two owners of one agent name. A refusal returns no packs,
// because every caller treats it as "this selection cannot run", as a refused closure.
func ApplyForks(packs []*Pack) ([]*Pack, error) {
	forks := Forks(packs)
	if len(forks) == 0 {
		return packs, nil
	}
	index := map[string]int{}
	for i, p := range packs {
		if p != nil {
			if _, seen := index[p.Name]; !seen {
				index[p.Name] = i
			}
		}
	}
	out := append([]*Pack(nil), packs...)
	copied := map[int]bool{}
	claimed := map[string]string{}
	var problems []string
	for _, f := range forks {
		if f.Base == f.Pack {
			problems = append(problems, fmt.Sprintf("pack %s forks itself — `fork_of` names the pack "+
				"whose program it builds, which is another pack", f.Pack))
			continue
		}
		i, ok := index[f.Base]
		if !ok {
			if shared := forkBaseCandidates(packs, f.Base); len(shared) > 1 {
				problems = append(problems, fmt.Sprintf("pack %s forks pack %s's %q, and %d selected packs "+
					"are named %s: %s — keep one of them in packs", f.Pack, f.Base, f.Bin, len(shared), f.Base,
					strings.Join(shared, ", ")))
				continue
			}
			problems = append(problems, fmt.Sprintf("pack %s forks pack %s's %q, and %s is not in "+
				"this selection — a pack yolo ships joins by itself, and any other base must be in "+
				"your config's packs list as a pack named %s (such as local/%s)", f.Pack, f.Base, f.Bin, f.Base,
				f.Base, f.Base))
			continue
		}
		key := f.Base + "/" + f.Bin
		if prev, dup := claimed[key]; dup {
			problems = append(problems, fmt.Sprintf("packs %s and %s both fork pack %s's %q — one "+
				"program can run one fork's build; drop one of them from packs", prev, f.Pack, f.Base, f.Bin))
			continue
		}
		base := out[i]
		at := baseProgram(base, f.Bin)
		if at < 0 {
			problems = append(problems, fmt.Sprintf("pack %s forks pack %s's %q, and %s declares no "+
				"program %q — a fork supplies the bytes of a program its base installs",
				f.Pack, f.Base, f.Bin, f.Base, f.Bin))
			continue
		}
		claimed[key] = f.Pack
		if !copied[i] {
			base = copyPackDecl(base)
			out[i] = base
			copied[i] = true
		}
		fork := forkContributionOf(packs, f)
		base.Decl.Contributes[at] = forkedProgram(base.Decl.Contributes[at], fork, f.Pack)
		// The fork pack's root, which a patched fork's series directory is relative to: a reader of
		// the base's program (the host floor) reads the series through it.
		base.Decl.Contributes[at].ForkRoot = f.Root
		base.Decl.Contributes[at].ForkPatchBranchDir = f.PatchBranchDir
		base.Decl.Contributes[at].ForkPatchBranchRepoIdentity = f.PatchBranchRepoIdentity
		// The fork's `launch_computed` for its bin, merged into the base's (packdecl.MergeForkComputed;
		// docs/reference/pack-system.md#LC-D1), checked against the base's launch selection, which
		// the fork pack's own validation could not see.
		if own := forkLaunchComputed(packs, f); len(own) > 0 {
			if probs := packdecl.ForkLaunchComputedProblems(f.Pack, f.Bin, base.Decl.Contributes[at].LaunchSelection,
				own); len(probs) > 0 {
				problems = append(problems, probs...)
				continue
			}
			merged := make(map[string][]packdecl.LaunchComputedFlag, len(base.Decl.LaunchComputedByBin)+1)
			for bin, flags := range base.Decl.LaunchComputedByBin {
				merged[bin] = flags
			}
			merged[f.Bin] = packdecl.MergeForkComputed(base.Decl.LaunchComputedByBin[f.Bin], own, f.Pack)
			base.Decl.LaunchComputedByBin = merged
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	for i, wasCopied := range copied {
		if wasCopied {
			refreshEmbeddedMetadataBinding(out[i])
		}
	}
	return out, nil
}

// baseProgram is the index of p's own program contribution for bin (never a fork's), or -1.
func baseProgram(p *Pack, bin string) int {
	if p == nil || p.Decl == nil {
		return -1
	}
	for i, c := range p.Decl.Contributes {
		if c.Provides() && c.Bin == bin && !c.IsFork() {
			return i
		}
	}
	return -1
}

// forkContributionOf finds f's own contribution again, for the fields Fork does not carry.
func forkContributionOf(packs []*Pack, f Fork) packdecl.Contribution {
	for _, p := range packs {
		if p == nil || p.Name != f.Pack || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.IsFork() && c.Bin == f.Bin && resolveForkBase(packs, c.ForkOf) == f.Base {
				return c
			}
		}
	}
	return packdecl.Contribution{}
}

// forkLaunchComputed is the `launch_computed` entries f's own pack declares for f's bin, nil for none.
func forkLaunchComputed(packs []*Pack, f Fork) []packdecl.LaunchComputedFlag {
	for _, p := range packs {
		if p != nil && p.Name == f.Pack && p.Decl != nil {
			return p.Decl.LaunchComputedByBin[f.Bin]
		}
	}
	return nil
}

// copyPackDecl copies a pack deep enough to rewrite its contribution list: the Pack, its manifest,
// and the list itself. The contributions' own slices and maps are shared, and nothing here writes
// into them — forkedProgram assigns fields of a copied value.
func copyPackDecl(p *Pack) *Pack {
	cp := *p
	decl := *p.Decl
	decl.Contributes = append([]packdecl.Contribution(nil), p.Decl.Contributes...)
	cp.Decl = &decl
	preserveEmbeddedMetadataBinding(p, &cp)
	return &cp
}

// forkedProgram is FP-D6, one field at a time: the base's program with the fork's delivery.
//
// THE FORK'S (each absent unless the fork sets it, and packdecl refuses all but two on a fork):
// `via`, `package`, `url`, `flags`, `update`, `versions_dir`, `installer_env`, `install_hints`,
// `platforms` and `model_catalog`, plus the fork's own `source`, `build` and `produces`. Those
// say how the bytes ARRIVE. `reads_shared_models` is the fork's too, replacing the base's: it
// says what the bytes READ. An inherited `update` would let the launcher's hourly self-update
// replace the pinned build with the vendor's release; an inherited install hint would tell
// `yolo check-deps` to install the upstream program; an inherited `model_catalog` names files
// inside an npm package a fork does not install.
//
// THE BASE'S: `refresh`, `probe_args`, `temp_caches`, `protocols`, `provider_sets`,
// `platform_switches`, `capabilities`, `platform_regions`, `unlisted_background_models`,
// `exact_menu_refuses`, `agent_files`, `needs_model_list` and `built_in_providers`. Those say what
// the program DOES once it is there, which a fork of it still does. `node_floor` is the base's
// unless the fork declares its own: the floor a fork's entrypoint needs is the fork's to raise.
//
// `fork_of` is left off the copy, so the rewritten program installs (InstallContributions) and
// claims its name (the base's own claim), and ForkedBy names the fork pack for provenance.
func forkedProgram(base, fork packdecl.Contribution, forkPack string) packdecl.Contribution {
	out := base
	out.Via = packdecl.ViaSource
	out.Package, out.URL, out.VersionsDir = "", "", ""
	out.Flags, out.Update, out.InstallHints, out.ModelCatalog = nil, nil, nil, nil
	out.InstallerEnv = nil
	out.Platforms = append([]string(nil), fork.Platforms...)
	if len(out.Platforms) == 0 {
		out.Platforms = nil
	}
	if fork.NodeFloor != "" {
		out.NodeFloor = fork.NodeFloor
	}
	out.Source, out.Build = fork.Source, fork.Build
	// What model file the program READS is a fact about its bytes, and the fork supplies them, so
	// the fork's `reads_shared_models` replaces the base's (docs/design/model-lists-and-pickers.md
	// ML-D14): a patched pi reading the shared form says so on its own fork, and the upstream pi
	// it replaces did not.
	out.ReadsSharedModels = fork.ReadsSharedModels
	out.Produces = append([]string(nil), fork.Produces...)
	// A patched fork's series directory and follow rule, so the rewritten program says it is one
	// (Install.IsPatchedFork). The directory stays relative to the FORK pack's root, which
	// ForkedBy names.
	out.Patches, out.Follow, out.PatchBranch = fork.Patches, fork.Follow, fork.PatchBranch
	out.ForkOf, out.ForkedBy = "", forkPack
	return out
}

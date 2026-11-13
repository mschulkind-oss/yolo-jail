package check

// packs.go is D1: HOST-SIDE VALIDATION of pack contributions.
//
// Composition stays in the container (ruling 3), and A12 makes a generator failure
// fatal there — so a broken pack HALTS a jail rather than warning into a running one.
// That is correct but late: the user finds out when their jail refuses to start.
//
// This section catches the same problems on the host, at `yolo check`, where erroring
// is normal and the message can be actionable. It is defense in depth, not the only
// line of defense: everything here is re-checked in the jail.
//
// It deliberately does NOT fetch. `yolo check` is read-only preflight: it must work
// offline and must not make a surprise network call, so a git pack the pack store does not
// hold yet is reported as a [SKIP] saying the next host launch fetches it (a launch runs the
// pack refresh step before it resolves anything; docs/reference/pack-system.md, "Fetch,
// refresh, lock") rather than fetched on the spot. It is a SKIP and not a FAIL because
// nothing is wrong: the launch will supply the pack. What check cannot do is look inside
// it, and a skip is the level that says "not examined" without counting it as a pass.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type selectedPackTreeOwner struct {
	mu          sync.Mutex
	path        string
	closing     bool
	active      int
	drained     chan struct{}
	cleanupDone chan struct{}
	removeAll   func(string) error
}

var errSelectedPackTreeClosed = errors.New("selected pack tree owner is closed")

// create makes and registers the selected-pack staging tree while cleanup is excluded. The
// returned release keeps cleanup from removing the root while sectionPacks is still staging into it.
func (o *selectedPackTreeOwner) create(factory func() (string, error)) (string, func(), error) {
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		return "", func() {}, errSelectedPackTreeClosed
	}
	path, err := factory()
	if err != nil {
		o.mu.Unlock()
		return "", func() {}, err
	}
	o.path = path
	o.active++
	if o.active == 1 {
		o.drained = make(chan struct{})
	}
	o.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			o.mu.Lock()
			defer o.mu.Unlock()
			o.active--
			if o.active == 0 {
				close(o.drained)
				o.drained = nil
			}
		})
	}
	return path, release, nil
}

// cleanup closes ownership, waits for in-flight staging to stop writing, then removes the root.
// Concurrent callers wait for that removal to finish before returning.
func (o *selectedPackTreeOwner) cleanup() {
	o.mu.Lock()
	if done := o.cleanupDone; done != nil {
		o.mu.Unlock()
		<-done
		return
	}
	done := make(chan struct{})
	o.cleanupDone = done
	o.closing = true
	for o.active != 0 {
		drained := o.drained
		o.mu.Unlock()
		<-drained
		o.mu.Lock()
	}
	path := o.path
	o.path = ""
	o.mu.Unlock()
	if path != "" {
		removeAll := o.removeAll
		if removeAll == nil {
			removeAll = os.RemoveAll
		}
		_ = removeAll(path)
	}
	close(done)
}

// sectionPacks validates the configured packs.
//
// Zero packs is the NOTABLE state, not the boring one: packs are how content —
// an agent included — reaches a jail, so an empty list means a jail with nothing in
// it. This section used to return silently there, on the reasoning that a non-pack
// user should not see output for a feature they do not use; that reasoning died with
// the `agents` key, because there is no longer a non-pack way to get an agent.
//
// The notice text is config.NoPacksMessage/NoPacksGuidance, shared with the launch-time
// warning in internal/cli/run so `yolo check` and a launch cannot tell the user
// different things.
func (o *Options) sectionPacks(r *reporter, merged *jsonx.OrderedMap) {
	// The header and the trailing blank are now UNCONDITIONAL — every branch below
	// prints something, and the separator used to be missing because the section only
	// existed for pack users (it ran straight into "Entrypoint Dry-Run").
	r.sectionHeader("Packs")
	defer r.blank()

	// Per-entry problems land on r.configWarn as GRADED [WARN] rows the summary counts.
	// They were ungraded "Warning:" lines until step 1 of
	// docs/design/reference-mismatch-diagnostics.md, which meant a skipped pack entry —
	// a pack the user asked for and did not get — would not have been counted.
	//
	// SAY "WOULD NOT HAVE BEEN" RATHER THAN "WAS NOT", because this sink is UNREACHABLE
	// from Check() today, the same way LoadCacheRelocations' is (see check.go's note).
	// Measured 2026-09-02 across five reportable shapes: every problem checkPacks can
	// report here is also added as a hard error by config.validatePacks, which runs the
	// SAME checkPacks (config/packs.go:477) — so sectionMergedConfig emits [FAIL] and the
	// accumulated-fail gate returns before sectionPacks runs. Wired anyway, and graded,
	// because the alternative is a discarding sink that goes wrong silently the day a
	// warn-only shape appears. Unlike the launch-side notice, check does NOT suppress the
	// empty-packs warning when they occur: the problems are printed right above it here,
	// so the two together read as "these entries were skipped, and what is left is
	// nothing" rather than as a misdiagnosis.
	entries, err := config.LoadPacks(r.configWarn)
	if err != nil {
		r.fail("Loading packs: "+err.Error(),
			"Fix the `packs` list in "+paths.UserConfigDisplayPath()+" (`yolo pack --help` shows an entry's forms), "+recheck)
		return
	}
	// HasConfiguredPack, not len(entries): the conventional local pack arrives with no config
	// line and is content, not an agent, so a home that has only that one still warrants the
	// no-agent notice. The local pack itself is still reported per-entry by the loop below.
	if !config.HasConfiguredPack(entries) {
		r.warn(config.NoPacksMessage, config.NoPacksGuidance)
		if len(entries) == 0 {
			o.selectedPacks, o.selectedPacksKnown = nil, true
			return
		}
	}

	lockPath := packsrc.LockPath(paths.UserConfigPath())
	lock, lockErr := packsrc.LoadLock(lockPath)
	if lockErr != nil {
		r.fail("Lockfile: "+lockErr.Error(), lockfileNote(lockPath, lockErr))
		lock = &packsrc.Lock{Packs: map[string]packsrc.LockEntry{}}
	}
	configured := map[string]string{}
	for _, e := range entries {
		configured[e.Name] = e.Source
	}

	// The loaded SELECTED set, resolved entry by entry below. It feeds the config-surface
	// exclusivity check after the selection, which is the one footprint rule the
	// Embedded()-only check at the bottom of this function cannot answer: a user's own pack
	// declaring a surface a shipped pack owns is invisible to a check that only ever looks at
	// what yolo ships, and that is the single most likely instance of the clash
	// (docs/reference/pack-system.md R1).
	//
	// One throwaway tree for the section, removed when it returns: every entry is staged into
	// <tree>/<slug>, the shape the launcher stages (stagePacks writes <staging root>/<Slug>), so
	// a loaded pack's Root basename is the staged slug here too. That basename is
	// packload.Pack.StagedSlug — the key the two halves mount a reads-host grant under.
	sectionTreeOwner := o.packTreeOwner
	if sectionTreeOwner == nil {
		sectionTreeOwner = &selectedPackTreeOwner{}
		defer sectionTreeOwner.cleanup()
	}
	factory := o.packTreeFactory
	if factory == nil {
		factory = func() (string, error) { return os.MkdirTemp("", "yolo-check-pack-") }
	}
	tree, releaseTreeUse, treeErr := sectionTreeOwner.create(factory)
	defer releaseTreeUse()
	// The SELECTION CLOSURE (docs/reference/wire-bridge.md §3.1, WB-D10; the `via` half,
	// docs/design/wire-bridge-gateway.md WG-I11), beside the pack list and before the
	// exclusivity checks below, for the reason those checks give: the launch runs them
	// over the COMPLETE set — the closure runs inside staging, before every pre-flight —
	// so check running them over anything narrower could pass a config the launch
	// refuses. It is the launch's own selection function (config.SelectPacks); what differs is
	// how an entry resolves (staged here, each entry reported as it resolves) and the
	// selection table, the merged config's `profile` (the table the protocol-pairing
	// prediction reads, and it cannot see `-p` either). A closure refusal
	// (a need or a via naming a pack outside the embedded official set, a via naming a
	// pack that serves no via route, a needs cycle) is a FAIL here, not a warning, for the
	// same reason: `yolo check` passing on a config that cannot start a jail is the one
	// outcome this section exists to prevent.
	//
	// The additions print (WB-D12 — the same cause strings the launch banner
	// carries, and never silently), and each added pack joins `loaded` so it is
	// footprint-accounted exactly as the launch will account it.
	//
	// The user's profile declarations are read ONCE for the section and shared with the
	// protocol-pairing prediction below: both need them, and each read hands its findings to
	// r.configWarn, so a second read would grade and count every malformed entry twice.
	//
	// Each reader resolves against the launch's ONE SELECTION NAMESPACE
	// (config.SelectableProfiles, docs/reference/config/user-config.md#which-provider-each-agent-runs-on): the declarations plus an implied profile per
	// provider name, alias and `<provider>/<variant>` the pack set declares, so a provider
	// selected by its own name is predicted as the launch resolves it rather than as undeclared.
	// The closure reads it over the set it is handed; the readers below, over `loaded`.
	declaredProfiles := sync.OnceValues(func() (map[string]packload.UserProfile, error) {
		return config.LoadProfiles(r.configWarn)
	})
	selectableOver := func(set []*packload.Pack) (map[string]packload.UserProfile, error) {
		declared, err := declaredProfiles()
		if err != nil {
			return nil, err
		}
		return config.SelectableProfiles(declared, merged, set)
	}
	var closureSet []*packload.Pack
	sel, _ := config.SelectPacks(entries, config.PackSelectSpec{
		Resolve: func(e config.PackEntry) (*packload.Pack, error) {
			if treeErr != nil {
				r.fail(e.Name+": "+treeErr.Error(),
					"yolo check stages each pack under "+os.TempDir()+". Make it writable, or point TMPDIR "+
						"at a directory that is, "+recheck)
				return nil, nil
			}
			// THE ONE RESOLVER, the launch's (config.ResolvePack): staged with the REAL executor, so
			// the escaping-symlink refusal surfaces here instead of at boot, and loaded from the copy,
			// so the declarations checked are the ones a jail would render — an embedded entry's
			// `only`/`exclude` included. ReadOnlyStore: check writes nothing and reaches no network,
			// and Resolve CHECKS OUT a commit whose tree is missing — which, in the partial
			// (--filter=blob:none) mirror, fetches that commit's blobs from the remote. Getenv is
			// threaded because resolution falls back to the DELIVERED tree under YOLO_PACK_ROOT when
			// the address is not visible from here — a jail's inherited config names host paths, so
			// that is every local pack, every time — and check's tests inject that environment.
			res, err := config.ResolvePack(e, config.ResolvePackSpec{
				Dest: filepath.Join(tree, e.Slug()), ReadOnlyStore: true, Getenv: o.getenv,
			})
			if err != nil {
				o.reportUnresolvedPack(r, e, err)
				return nil, nil
			}
			switch {
			case e.Embedded():
				// Reported PASSING rather than skipped silently: a user who wrote
				// `packs: ["claude"]` should see it acknowledged here, not wonder whether the key
				// took effect.
				r.ok(e.Name + ": ships with yolo")
			case res.StagedFrom != "":
				// A SOURCE THAT IS NOT VISIBLE FROM HERE IS NOT A BROKEN PACK — Resolve already
				// found the staged copy, and StagedFrom is how it says so. The ruling and the
				// reason the predicate is filesystem-keyed rather than "am I in a jail" live with
				// the fallback, in packsrc.Store.Resolve; this branch is only the REPORTING half,
				// which is check's alone (the launcher stages the same tree silently).
				r.ok(e.Name + ": staged at " + res.StagedFrom)
				if addr, perr := packsrc.Parse(e.Source); perr == nil {
					r.note("  source " + res0Path(addr, e.Source) + " is host-side and not visible from in here")
				}
			case len(res.Staged.Staged) == 0:
				// Not a hard failure — a pack may legitimately be empty mid-authoring —
				// but never silent, because it is nearly always a filter typo.
				r.warn(e.Name+": stages 0 files",
					"Its `only`/`exclude` filters match none of its files. Fix them in its `packs` entry in "+
						paths.UserConfigDisplayPath()+" (`yolo pack --help` shows the filters), "+recheck)
				return nil, nil
			default:
				r.ok(fmt.Sprintf("%s: %d file(s) stage", e.Name, len(res.Staged.Staged)))
			}
			if advice := packMigrateAdvice(e); advice != "" {
				r.note(advice)
			}
			// There is nothing origin-dependent left to match: OQ-TP9 deleted the host-access gate,
			// so `check` and the launch load a pack the same way.
			for _, prob := range res.Problems {
				r.fail(prob, "the launch refuses this pack until it is fixed.\n"+packFixNote(e))
			}
			// A contribution this yolo cannot read is a WARNING, not a failure: the launch runs the
			// pack without it and says so (docs/reference/patched-forks.md PF-D68), so check passing
			// is the launch's own answer. Graded, so the summary counts what a launch will skip.
			if res.Pack != nil {
				for _, note := range res.Pack.SkewNotes {
					r.warn(note, skewFixNote(e))
				}
			}
			if res.Pack != nil && len(res.Problems) == 0 {
				return res.Pack, nil
			}
			return nil, nil
		},
		Selection: packload.Selection{
			UseProfiles: func(set []*packload.Pack) map[string]string {
				closureSet = set
				return config.ConfigProfileTable(merged, set)
			},
			UserProfiles: func() (map[string]packload.UserProfile, error) {
				return selectableOver(closureSet)
			},
		},
	})

	loaded := sel.Configured
	if sel.ClosureErr != nil {
		r.fail("Pack selection: "+sel.ClosureErr.Error(),
			"Fix the `packs` entry, or the `needs` of a pack you wrote, that this names "+
				"(`yolo pack --help`), "+recheck+"\n"+
				yoloBugNote("The same problem in a pack that ships with yolo"))
	} else {
		loaded = sel.Packs()
		for _, cause := range sel.Causes {
			r.dim(cause)
		}
	}
	// The readers below resolve over the selection the closure settled on.
	userProfiles := sync.OnceValues(func() (map[string]packload.UserProfile, error) {
		return selectableOver(loaded)
	})
	// Handed forward to the host-wrappers section, which predicts what `yolo host apply
	// --assert` would generate: the host apply wraps the programs of the packs the same selection
	// function hands it, the closure's additions included (notch-convergence item 6).
	o.selectedPacks = append([]*packload.Pack(nil), loaded...)
	o.selectedPacksKnown = true
	o.reportSelectedCallBrokerPrerequisites(r, loaded)

	// A REF NO CHECK HAS RESOLVED ON THIS MACHINE: check reaches no network, so a patch branch or an
	// upstream ?ref= naming nothing passes here and is refused at the launch that fetches it. Said, with
	// the step that resolves it now.
	for _, line := range unresolvedRefLines(loaded) {
		r.dim(line)
	}
	// A DEV OVERRIDE naming no selected program does nothing here, and is information, not a
	// failure (docs/design/pack-buildable-programs.md §5).
	for _, line := range run.UndeclaredProgramOverrideLines(loaded) {
		r.dim(line)
	}
	// A DEV OVERRIDE of a program no pack builds from its own tree refuses the launch (BG-D19), so it
	// fails here too, with the same next step: a fork pack.
	for _, line := range run.NonPackTreeOverrideLines(loaded, config.ProgramOverrides(nil)) {
		r.fail("a dev override names a program no pack builds from its tree", line)
	}

	for _, finding := range selectedPackCollisionFindings(loaded) {
		r.fail(finding.message, finding.note)
	}
	// And each need another selected pack satisfies, in the launch's own words (OQ-PS5).
	for _, n := range packload.ResolveProgramNeeds(loaded) {
		if line := n.ProvidedLine(); line != "" {
			r.ok(line)
		}
	}
	for _, w := range packload.DefaultNamespaceRenames(loaded,
		packload.ConvertedConfigIn(filepath.Dir(paths.UserConfigPath()))) {
		r.warn("pack skills renamed by a default namespace", w)
	}
	// Over the packs `yolo host apply` composes: each destination resolved, so a pack that names
	// its audience rather than a directory (`agents`, or nothing) is counted where it lands.
	resolved, _ := packload.ResolveDestinations(loaded)
	for _, c := range hostskills.Collisions(hostskills.ComposeHostSkills(resolved, paths.Home())) {
		r.fail("pack skill name collision", c.Message())
	}

	// Pack destination exclusivity (KindFiles) — exact duplicate destinations and nested claims.
	// FATAL here for the same reason the launch refuses it: reporting it as a warning would mean
	// `yolo check` passing on a config that cannot start a jail.
	for _, c := range run.PackDestConflicts(loaded, packdecl.KindFiles) {
		r.fail("pack files collision", c+"\n"+keepOneNote())
	}
	for _, s := range run.PackFilesShadowedSurfaces(loaded) {
		r.fail("pack files shadows a config surface", s)
	}

	// A `files` tree landing in a slot whose `expects` it misses, over the same selected set. A
	// WARNING: the launch still delivers and registers the tree, and whether it loads is the
	// agent's to say (docs/reference/pack-system.md PR-D4).
	for _, n := range packload.ExpectsNotes(loaded, loaded) {
		r.warn(n.Msg, n.Fix)
	}

	// The launch's PROTOCOL-PAIRING gate, predicted over the SELECTED set — here rather
	// than in Merged Configuration because the gate compares a config selection against
	// PACK DECLARATIONS, and `loaded` is the only place both are in hand. After
	// ResolveNeeds, so a pack pulled in by `needs` can supply the adapter that resolves a
	// pairing, exactly as it does at launch. protocols.go states why this calls the
	// launch's own gate instead of restating it.
	served := o.predictedServed(merged, loaded)
	var prediction protocolPrediction
	pairErrs, pairWarns := protocolPairingGap(loaded, merged, served, r.configWarn, userProfiles, &prediction)
	for _, e := range pairErrs {
		r.fail(e, launchPredictionNote)
	}
	for _, w := range pairWarns {
		r.warn(w, launchPredictionNote)
	}
	// What the `models` contributions of the selected packs could not do as written — a
	// duplicate, an `only` id nothing added, a provider nothing declares — over the same
	// selected set, since a pack pulled in by `needs` shapes a list at launch too.
	for _, w := range modelListNotes(loaded, merged) {
		r.warn(w, modelListFixNote)
	}
	emptyModelListReport(r, prediction)
	// Which of those lists a profile's `enforce_models` off leaves out of an agent's menu
	// (docs/design/model-lists-and-pickers.md MM-D5, MM-D29): over the same selected set and the
	// same composition, with the profiles resolved as the launch resolves them.
	unnarrowedMenuReport(r, loaded, merged, userProfiles)
	// Which per-model OpenRouter pins no request to their provider can carry, over the same
	// composition, so a pin that does nothing is named rather than silently accepted.
	inertRoutingReport(r, loaded, merged)
	// Whether the ids those lists name are ones an installed agent's own catalog knows
	// (docs/design/model-lists-and-pickers.md MM-D16): over the same selected set and the same
	// composition, read from the files each agent's pack declares, so a warning here names an id
	// the launch would really hand an agent.
	o.modelCatalogReport(r, loaded, merged)
	// Whether each model a profile's `subagents` or a pack names in an agent's config resolves in
	// that agent's registry (docs/design/model-lists-and-pickers.md MM-D45), over the same
	// prediction, so a value that matches nothing is named where it was declared.
	o.modelReferenceReport(r, prediction)

	// The launch's ENV-OVERRIDE refusal, predicted over the same selected set and for the
	// same reason it sits in this section rather than in Merged Configuration: both the
	// overridden contribution and its `overridden_by` declaration are a selected pack's, and
	// `loaded` is the only place they are in hand. After ResolveNeeds, so a pack pulled in by
	// `needs` delivers here exactly as it does at launch. envoverrides.go states why this
	// calls the launch's own rule instead of restating it, which two delivery channels it
	// cannot see, and why a directory grant counts only off macOS (!o.IsMacOS below).
	overrideErrs, overrideWarns, scope := envOverrideGap(loaded, merged, served, o.Workspace, !o.IsMacOS, r.configWarn, userProfiles)
	for _, e := range overrideErrs {
		r.fail(e.msg, e.note)
	}
	for _, w := range overrideWarns {
		r.warn(w.msg, w.note)
	}
	// The env-set refusals and lines the launch prints, over the same gate (secret-env-scoping.md
	// §3.5-§3.7). The hydration is read again on r.configWarn, which prints a message once, so a
	// file overrideScope already reported unreadable is not reported twice.
	secretFails, secretWarns := secretEnvGap(scope, config.ResolveEnv(o.Workspace, merged, r.configWarn), o.Workspace)
	for _, f := range secretFails {
		r.fail(f.msg, f.note)
	}
	for _, w := range secretWarns {
		r.warn(w.msg, w.note)
	}

	// Drift last, so it reads as a summary rather than interleaving with per-pack
	// results. It is a WARNING, not a failure: the jail will still start, using the
	// config address, and for a git pack that launch's refresh step fetches it and
	// rewrites the lock entry (a local pack's entry only install rewrites). What the
	// warning reports is that the lock does not yet say what was asked for.
	for _, d := range lock.DriftFrom(configured) {
		remedy := "the next host launch fetches the config address and rewrites the lock entry " +
			"(or run `yolo pack install` to do it now)"
		if a, err := packsrc.Parse(d.WantedSource); err != nil || a.IsLocal() {
			// A launch records only the git packs its refresh fetched, never a local one.
			remedy = "a local pack: run `yolo pack install` to record the config address " +
				"(no launch rewrites a local pack's entry)"
		}
		r.warn(d.Name+": config address changed since the lock was written",
			"locked "+d.LockedSource+", config "+d.WantedSource+" — "+remedy)
	}

	// Footprint collision check (the one-writer rule, §3.6): compute the union of
	// what packs claim and refuse a collision on a sole-owned target before boot.
	// Runs over the embedded packs — the ones with real declarations that every
	// launch includes; a configured local pack's declarations join once the
	// footprint reads a staged tree (a later phase). A collision is FATAL here
	// because it is a pack-authoring bug that would otherwise surface as a mount
	// conflict at boot with no obvious cause (§1.4).
	if cols := packload.Collisions(packload.Embedded()); len(cols) > 0 {
		for _, c := range cols {
			r.fail(fmt.Sprintf("pack footprint collision: %s %s", c.Kind, c.Target),
				"packs "+strings.Join(c.Packs, ", ")+" — "+c.Reason+"\n"+
					yoloBugNote("A collision between the packs yolo ships"))
		}
	}
}

// reportUnresolvedPack reports an entry the resolver could not resolve. A store miss the next
// HOST launch repairs is a SKIP (launchWouldRepair says which); everything else — a malformed
// address, a subpath absent at the commit, an escaping symlink, a broken embedded
// materialization — is a FAIL, with the resolver's reason and without its "packs: <name>: "
// prefix, since the line already starts with the name.
func (o *Options) reportUnresolvedPack(r *reporter, e config.PackEntry, err error) {
	why := err
	var rerr *config.PackResolveError
	if errors.As(err, &rerr) {
		why = rerr.Err
	}
	if !e.Embedded() {
		if addr, perr := packsrc.Parse(e.Source); perr == nil {
			switch o.launchWouldRepair(addr, why) {
			case packsrc.ErrNotFetched:
				r.skip(e.Name+": not in the pack store yet — the next launch fetches it, "+
					"so its contents are not checked here",
					"pack store: "+why.Error()+"\n"+
						"run `yolo pack install` to fetch it now, then `yolo check` again")
				return
			case packsrc.ErrNotCheckedOut:
				r.skip(e.Name+": not checked out yet — the next launch checks it out, "+
					"so its contents are not checked here",
					"pack store: "+why.Error()+"\n"+
						"run `yolo pack install` to check it out now, then `yolo check` again")
				return
			}
		}
	}
	r.fail(e.Name+": "+why.Error(), packFixNote(e))
}

// unresolvedRefLines is a line for each patched fork or extension of the selected packs whose git refs no
// check on this machine has resolved (packload.Fork.RefNotResolvedLine): a patch branch whose commit no
// check record holds, or an upstream with no check record at all. A plain fork's pin and an npm upstream
// have no ref to resolve. Read from the machine's pack store, which check never writes.
func unresolvedRefLines(packs []*packload.Pack) []string {
	store := &packsrc.Store{Dir: paths.PacksDir()}
	var lines []string
	for _, f := range append(packload.Forks(packs), packload.PatchedTrees(packs)...) {
		if !f.FollowsUpstream() {
			continue
		}
		if f.PatchBranch != "" {
			if s, err := f.ReadSeriesFrom(store); err == nil && s.Unresolved() {
				lines = append(lines, unresolvedRefLine(f))
			}
			continue
		}
		if f.Npm() {
			continue
		}
		if _, err := store.LoadCheckRecord(f.Key()); errors.Is(err, packsrc.ErrNoCheckRecord) {
			lines = append(lines, unresolvedRefLine(f))
		}
	}
	return lines
}

// unresolvedRefLine is unresolvedRefLines' line for f. A RELATIVE patch branch that does not resolve from
// the directory this process read the pack from is a pack read through a copy — in a jail, the tree
// the launch delivered, which is in no repository — so the lint to run is of the pack's own directory.
func unresolvedRefLine(f packload.Fork) string {
	if packsrc.IsRelativeLocator(f.PatchBranch) {
		if _, err := f.PatchBranchAddr(); err != nil {
			return f.Label() + ": ref not resolved (offline) — run `yolo pack lint --online <dir>` on the pack's own " +
				"directory, inside the repository its patch branch " + f.PatchBranch + " names, to check it"
		}
	}
	return f.RefNotResolvedLine(f.PatchBranchDir)
}

// packFixNote is the next step for a pack whose declaration or address the launch refuses: the
// user's own pack is theirs to fix, and a pack that ships with yolo is the maintainers'.
func packFixNote(e config.PackEntry) string {
	if e.Embedded() {
		return ShippedPackFix(e.Name) + ", " + recheck
	}
	return UserPackFix(e.Source) + ", " + recheck
}

// skewFixNote is the next step for a contribution of e's pack this yolo cannot read: a newer yolo
// may read it, or the field is a typo `yolo pack lint` names. A pack yolo ships is never written
// for a newer yolo than itself, so there it is the maintainers' bug.
func skewFixNote(e config.PackEntry) string {
	if e.Embedded() {
		return "each launch skips it and says so.\n" + packFixNote(e)
	}
	return "each launch runs this pack without it and says so, and `yolo host apply --assert` " +
		"renders nothing while it is skipped. Update yolo (`yolo update`) if the pack was written " +
		"for a newer one; if a field is misspelled, `yolo pack lint` on the pack at " + e.Source +
		" names it, " + recheck
}

// UserPackFix is packFixNote for the user's own pack, at source, without the re-check, so another
// verb reporting the same pack (`yolo check-deps`) gives the same fix and ends on its own re-check.
func UserPackFix(source string) string {
	return "Fix the pack at " + source + " (`yolo pack --help` documents every field), or its `packs` " +
		"entry in " + paths.UserConfigDisplayPath()
}

// ShippedPackFix is packFixNote for a pack that ships with yolo, named name, without the re-check:
// it is the maintainers' to fix (rung 4), so the step is the issue tracker and, until then,
// dropping the pack.
func ShippedPackFix(name string) string {
	return yoloBugNote("A problem in a pack that ships with yolo") + "\nUntil it is fixed, drop " +
		name + " from `packs` in " + paths.UserConfigDisplayPath()
}

// keepOneNote is the next step for two selected packs claiming one name: the launch refuses the
// pair, so the selection keeps one of them.
func keepOneNote() string {
	return "Keep one of them in `packs` in " + paths.UserConfigDisplayPath() + ", " + recheck
}

// launchPredictionNote follows a launch refusal check predicts over the selected packs: the
// refusal's own words name what to change, in the config or a pack's declarations.
const launchPredictionNote = "Change what it names, in your config (`yolo config-ref`) or a pack you wrote " +
	"(`yolo pack --help`), " + recheck

// modelListFixNote follows a `models` contribution that could not do what it says. The pack's
// author can fix it; for a pack that ships with yolo that is the maintainers.
var modelListFixNote = "Fix the `models` contribution of the pack it names, if the pack is yours " +
	"(`yolo pack --help`), " + recheck + "\n" + yoloBugNote("The same problem in a pack that ships with yolo")

// launchWouldRepair reports which store miss a resolution failure is when the next HOST
// launch repairs it: packsrc.ErrNotFetched for a git pack whose mirror the store does not
// have, or whose ref the mirror does not hold and no fetch has come back without; and
// packsrc.ErrNotCheckedOut for a commit the store holds but has not checked out. The launch's
// refresh does exactly those before it resolves anything (docs/reference/pack-system.md,
// "Fetch, refresh, lock").
//
// NIL, so the failure stays a FAIL, for everything else:
//   - a local (file://) pack, which nothing fetches;
//   - in a jail, where the refresh step does nothing (the jail has no pack store and no git
//     credentials), so a nested launch refuses a pack the outer launch did not stage;
//   - a ref a SUCCESSFUL fetch did not find (a typo in ?ref=, a deleted branch), which the
//     store reports without ErrNotFetched: every launch would fail on it;
//   - any other store error — a subpath absent at the resolved commit, a failed checkout —
//     which a fetch does not repair and the launch refuses by name.
//
// It classifies by the store's TYPED errors (errors.Is), not its wording.
// TestSectionPacksSkipsAPackTheLaunchWouldFetch drives the REAL store into each case.
func (o *Options) launchWouldRepair(addr packsrc.Addr, err error) error {
	if addr.IsLocal() || o.getenv("YOLO_VERSION") != "" {
		return nil
	}
	for _, kind := range []error{packsrc.ErrNotFetched, packsrc.ErrNotCheckedOut} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return nil
}

// getenv is nil-safe: several tests drive a zero Options directly rather than
// through fillDefaults, and a nil func there would panic rather than fail.
func (o *Options) getenv(key string) string {
	if o.Getenv != nil {
		return o.Getenv(key)
	}
	return os.Getenv(key)
}

// res0Path renders the address for the note above. The parsed form is the honest
// one when it has a path; the raw source string is the fallback.
func res0Path(addr packsrc.Addr, raw string) string {
	if addr.Path != "" {
		return addr.Path
	}
	return raw
}

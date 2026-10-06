package stores

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Sizing says HOW a size figure was obtained. It is printed beside every number
// this command emits, which is the discipline the whole design is built on:
// "No figure is ever printed without saying how it was obtained."
type Sizing string

const (
	// SizingMeasured — walked or queried in this run, complete.
	SizingMeasured Sizing = "measured"
	// SizingPartial — the walk hit its budget or skipped an unreadable subtree.
	// Bytes is a LOWER BOUND and is rendered as one.
	SizingPartial Sizing = "partial"
	// SizingCached — carried over from a recorded sample rather than walked now.
	// Reserved for the cheap-probe path the housekeeping slot will feed; nothing
	// emits it yet, and the renderer labels it with the sample's date when
	// something does.
	SizingCached Sizing = "cached"
	// SizingAbsent — the store does not exist here. Size 0, and NOT an error: a
	// fresh machine has almost none of these.
	SizingAbsent Sizing = "absent"
	// SizingUnknown — it exists and could not be read. Never 0: reporting zero
	// for a store you could not read is the defect this value exists to prevent.
	SizingUnknown Sizing = "unknown"
)

// Verdict is the plain answer to "can yolo reclaim this at all?" — the column
// that makes a store nothing owns as legible as one that is swept.
type Verdict string

const (
	// VerdictYolo — yolo has a reclaimer for it and may run it.
	VerdictYolo Verdict = "yolo"
	// VerdictHuman — the bytes are reclaimable, but only by the user: yolo has
	// no evidence it owns them, or no reclaimer covers them.
	VerdictHuman Verdict = "human"
	// VerdictNotOurs — not yolo's bytes at all.
	VerdictNotOurs Verdict = "not yolo's"
)

// Reclaimer names what would reclaim a store and what runs it. Func is an
// EXPORTED SYMBOL of internal/prune or internal/capture — a name a reader can
// grep — and TestEveryNamedReclaimerExists parses those packages to prove each
// one still does, so this table cannot quietly outlive the function it names.
// An empty Func means the honest "none".
type Reclaimer struct {
	Func    string `json:"func,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Trigger string `json:"trigger,omitempty"`
}

// none is the reclaimer of every store nothing reclaims — the class §5.5 exists
// to surface.
func none() Reclaimer { return Reclaimer{} }

// Dead is the --age accounting for one store: how much of it is older than the
// cutoff. Coverage is a separate question (the Reclaimer column) — these bytes
// are old, not necessarily reclaimable.
type Dead struct {
	Bytes         int64   `json:"bytes"`
	Files         int     `json:"files"`
	OlderThanDays float64 `json:"older_than_days"`
}

// Growth is a rate derived from the sample ledger, with the window it was
// measured over. The window is part of the figure, never a footnote.
type Growth struct {
	BytesPerDay int64     `json:"bytes_per_day"`
	Days        float64   `json:"days"`
	Since       time.Time `json:"since"`
	Samples     int       `json:"samples"`
}

// Store is one row of the inventory.
type Store struct {
	// Key is the stable ledger key. It names the sample file, so renaming one
	// silently starts a store's history over — which is why keys are literals
	// here rather than derived from a display name.
	Key     string `json:"key"`
	Section string `json:"section"`
	Name    string `json:"name"`
	Path    string `json:"path"`

	Sizing Sizing `json:"sizing"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files,omitempty"`
	// Count/CountLabel carry a count-shaped store's own unit (images, paths,
	// tars) beside its bytes, because "4 rows" is the actionable half of the
	// untagged-image row and a byte total alone would hide it.
	Count      int    `json:"count,omitempty"`
	CountLabel string `json:"count_label,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Unreadable int    `json:"unreadable_subtrees,omitempty"`
	Elapsed    string `json:"elapsed,omitempty"`

	Dead      *Dead     `json:"dead,omitempty"`
	Growth    *Growth   `json:"growth,omitempty"`
	Reclaimer Reclaimer `json:"reclaimer"`
	Verdict   Verdict   `json:"verdict"`
	// Note is the row's own sentence — why yolo declines, what the number counts,
	// what a user may run instead. Printed under the table, never truncated.
	Note string `json:"note,omitempty"`
}

// Report is one run's whole inventory.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Frame is "host" or "in-jail". THE HEADER STATES WHICH MACHINE'S VIEW IT
	// MEASURED, and this is not cosmetic: paths.GlobalCache() resolves to the
	// host's tree for a host yolo and to a jail's own for an in-jail one, and
	// reading that expression in the wrong frame is what produced a wrong
	// retraction in the design corpus (§5.5).
	Frame     string   `json:"frame"`
	FrameNote string   `json:"frame_note"`
	Runtime   string   `json:"runtime,omitempty"`
	Budget    string   `json:"walk_budget"`
	Stores    []Store  `json:"stores"`
	Notes     []string `json:"notes,omitempty"`

	// Recorded is how many ledger lines this run wrote (0 with --no-record).
	Recorded   int      `json:"recorded"`
	SamplesDir string   `json:"samples_dir,omitempty"`
	RecordErrs []string `json:"record_errors,omitempty"`
}

// Section names, in print order.
const (
	SectionState  = "yolo's state dir"
	SectionCache  = "shared cache"
	SectionAlias  = "host caches this jail aliases"
	SectionImages = "container image store"
	// SectionVolumes is the podman scratch volumes (internal/prune/scratchvolumes.go):
	// every jail's /tmp, /var/tmp and nested container store, which are disk-backed and
	// outlive the jail by as long as their deletion takes.
	SectionVolumes = "container scratch volumes"
	SectionNix     = "yolo's own /nix/store outputs"
)

// Inventory measures every store and returns the report. It never mutates
// anything: the ledger write is Run's, gated on --no-record, and deliberately
// not reachable from here.
func Inventory(o Options) Report {
	fillDefaults(&o)
	rt := o.DetectRuntime()
	rep := Report{
		GeneratedAt: o.Now().UTC(),
		Frame:       "host",
		FrameNote:   "these are this HOST's stores",
		Runtime:     rt,
		Budget:      o.Budget.String(),
	}
	if o.InJail() {
		rep.Frame = "in-jail"
		rep.FrameNote = "these are THIS JAIL's own stores — the host's are a different, usually much larger tree"
	}

	cacheRows := cacheStores(o)
	rep.Stores = append(rep.Stores, stateStores(o, cacheRows)...)
	rep.Stores = append(rep.Stores, cacheRows...)
	rep.Stores = append(rep.Stores, aliasStores(o)...)
	rep.Stores = append(rep.Stores, imageStores(o, rt)...)
	rep.Stores = append(rep.Stores, scratchStores(o, rt)...)
	rep.Stores = append(rep.Stores, durableStores(o, rt)...)
	rep.Stores = append(rep.Stores, nixStores(o)...)
	return rep
}

// aliasStores inventories L9's host-CAS aliases — one row per recognised
// content-addressed host cache, aliased or not
// (docs/design/disk-levers-and-backfill.md OQ-BF10).
//
// IT IS ITS OWN SECTION, AND THAT IS THE ANTI-DOUBLE-COUNT. These bytes belong
// to the HOST USER, not to yolo, so they must never join the shared-cache
// section: stateStores sums that section into the state dir's `cache/` row, and
// a 27 G host store folded in there would inflate yolo's own footprint by a tree
// yolo does not own and cannot reclaim. A separate section is summed by nothing.
//
// The verdict is NOT YOLO'S for the same reason, which also keeps the row out of
// the "what nothing reclaims" list (renderText filters on VerdictHuman): the
// whole point of the list is bytes the USER could decide about on yolo's behalf,
// and offering to reclaim another owner's build cache is exactly what §5.5's
// forbidden list rules out.
//
// The STRANDED private copy is not a row of its own. It is inside the
// `cache/<tool>` row of the section above — the alias mounts over it in place —
// so a second row would report the same bytes twice within one report. Each row's
// note names the path and what reclaims it instead.
func aliasStores(o Options) []Store {
	var out []Store
	for _, d := range o.HostCAS() {
		s := Store{
			Key:        "alias." + d.Store.Name,
			Section:    SectionAlias,
			Name:       d.Store.CacheRel,
			Path:       d.Source,
			Reclaimer:  Reclaimer{Detail: "the host user's own store; yolo never reclaims it"},
			Verdict:    VerdictNotOurs,
			CountLabel: "",
		}
		if d.Source == "" {
			// No host cache root resolved, so there is no path this row could be
			// about. Naming the store and the reason is still the useful answer.
			s.Path = "(no host cache directory resolved)"
			s.Sizing = SizingAbsent
			s.Note = "not aliased: " + d.Reason
			out = append(out, s)
			continue
		}
		sizeStore(&s, d.Source, o)
		if d.Aliased {
			s.Note = "ALIASED (writable) at " + d.Dest + " — this jail shares these host bytes " +
				"instead of pooling a second copy. Its former private copy is at " + d.Stranded +
				", now stranded: nothing in the jail reads it, and PurgeCacheByAge reclaims it " +
				"once its files age past the cache rule. THIS row's bytes are the host's and are " +
				"counted only here; the stranded copy's are in the cache/" +
				firstSegment(d.Store.CacheRel) + " row above, when this frame has one. " +
				d.Store.Evidence
		} else {
			s.Note = "not aliased: " + d.Reason + ". This jail keeps its own copy at " +
				d.Stranded + " (counted in the shared-cache section above)"
		}
		out = append(out, s)
	}
	return out
}

// firstSegment is a store's top-level cache subdir — the row in the shared-cache
// section its stranded bytes are counted in.
func firstSegment(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// stateReclaimers maps a direct child of the state dir to what reclaims it.
//
// It is a TABLE OF SYMBOL NAMES, not of prose claims, for a reason: every entry
// is checkable (TestEveryNamedReclaimerExists), and a child absent from the
// table gets the honest default — reclaimer none — rather than a guess. That
// default is the safe direction: a store that grew a reclaimer yesterday reads
// as unreclaimed until someone adds it here, where a store whose reclaimer was
// deleted would otherwise keep claiming one forever.
var stateReclaimers = map[string]Reclaimer{
	"captures": {Func: "capture.PruneSupersededCaptures", Detail: "newest per program", Trigger: "yolo prune --apply"},
	"agents":   {Func: "PruneOrphanAgentStaging", Detail: "liveness-gated", Trigger: "yolo prune --apply"},
	"build":    {Func: "PruneOrphanImageRoots", Detail: "+ legacy build roots, dangling out-links", Trigger: "yolo prune --apply"},
	"home":     {Func: "PruneShadowedHome", Detail: "overlay-masked seeds", Trigger: "yolo prune --apply"},
	// ⚠ "keep 3 generations" is true of the STAMPED buckets — the skills/files/briefing/
	// retired copies a render replaced, which it can regenerate. The `config` bucket inside
	// this tree is keyed by SURFACE and holds the one copy of a user's pre-yolo file
	// (OQ-CO7, render.Target.ArchivePath), so the sweep below cannot parse it as a
	// generation and leaves it — which is the point, not an oversight. The reclaimer named
	// here is still the right one; what it reclaims is a subset.
	"archive":    {Func: "PruneHostArchiveBuckets", Detail: "keep 3 generations (adoption archive exempt)", Trigger: "yolo prune --apply"},
	"state":      {Func: "PruneRetiredLoopholeState", Detail: "keep 3 generations", Trigger: "yolo prune --apply"},
	"containers": {Func: "PruneStoppedContainers", Detail: "tracking files", Trigger: "yolo prune --apply"},
	// One immutable copy of the built-in packs per build (paths.EmbeddedPacksDir). Every
	// process reading a tree holds its lease, so the sweep reaps only trees nothing holds —
	// and never this build's own, which the next command would only write again.
	// An archive delivery's per-attempt directories (paths.ImageDeliveryDir). The
	// Apple Container delivery records beside them are reaped with their image when
	// next read, not by this sweep.
	"image-delivery": {Func: "PruneImageDelivery", Detail: "interrupted deliveries past a 1h floor", Trigger: "yolo prune --apply"},
	"embedded-packs": {Func: "PruneEmbeddedPackTrees", Detail: "other builds' trees, lease-gated; current build kept", Trigger: "yolo prune --apply"},
	// The host agent floor (paths.HostFloorDir): yolo's own copies of the selected packs' agents,
	// which `yolo host` runs. Self-bounded in the two ways that matter — an install keeps its
	// program's current and previous version, and `yolo host apply --assert` removes an entry no
	// selected pack delivers (hostfloor.Floor.Reconcile) — so prune's reach is only what a killed
	// install left, lock-gated.
	"host-floor": {Func: "PruneHostFloor", Detail: "interrupted installs, lock-gated; each program keeps current + previous, deselected ones go at `yolo host apply --assert`", Trigger: "yolo prune --apply"},
	// The model menus `yolo host --` hands the programs it runs (paths.HostModelMenusDir;
	// docs/design/model-lists-and-pickers.md MM-D27). Self-bounded, collected by liveness: a launch
	// that writes a new menu removes a program's others once no program holding one runs
	// (modelmenu.Request.WriteIn), so no sweep of prune's has anything to add.
	"model-menus": {Detail: "self-bounded: a `yolo host --` that writes a new menu removes the program's others once no program holding one runs", Trigger: "the next `yolo host --` that writes a menu"},
	// The host's copies of each patched extension's good build (paths.HostTreesDir;
	// docs/design/patched-extensions.md §8.3), which the links `yolo host apply` owns at `~/<into>`
	// name and the host agent loads. Self-bounded: a render keeps the build its link names and the
	// one before (pruneHostTreeVersions), `yolo host apply --assert` removes a dropped extension's
	// copies once no recorded link names them (sweepDroppedHostTrees), and `--revert` removes them
	// all — so no sweep of prune's has anything to add, and a user deleting them leaves dangling links.
	"host-trees": {Detail: "self-bounded: each patched extension keeps the build its link names and the one before; a dropped extension's copies go at `yolo host apply --assert`, all of them at `yolo host apply --revert`", Trigger: "yolo host apply --assert"},
}

// stateStores inventories the direct children of the state dir, one row each.
//
// The cache row is folded in from the already-measured cache section rather than
// walked a second time — the cache is the largest tree on the machine and
// walking it twice would double this command's cost for a number it already has.
func stateStores(o Options, cacheRows []Store) []Store {
	root := o.GlobalStorage()
	cacheLeaf := filepath.Base(o.GlobalCache())

	entries, err := os.ReadDir(root)
	if err != nil {
		s := Store{
			Key: "state", Section: SectionState, Name: "(whole state dir)", Path: root,
			Reclaimer: none(), Verdict: VerdictYolo,
		}
		if os.IsNotExist(err) {
			s.Sizing = SizingAbsent
			s.Note = "no state dir on this machine yet — nothing has launched here"
		} else {
			s.Sizing = SizingUnknown
			s.Reason = err.Error()
		}
		return []Store{s}
	}

	var out []Store
	var stray int64
	var strayFiles int
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.IsDir() {
			stray += info.Size()
			strayFiles++
			continue
		}
		name := e.Name()
		s := Store{
			Key: "state." + name, Section: SectionState, Name: name + "/",
			Path: filepath.Join(root, name), Reclaimer: none(), Verdict: VerdictHuman,
		}
		if r, ok := stateReclaimers[name]; ok {
			s.Reclaimer, s.Verdict = r, VerdictYolo
		}
		switch {
		case name == cacheLeaf:
			// Summed from the cache section's own rows, so the two totals cannot
			// disagree and the tree is walked once.
			s.Sizing, s.Bytes, s.Files = SizingMeasured, 0, 0
			var dead Dead
			sized, unreadable := 0, 0
			for _, c := range cacheRows {
				s.Bytes += c.Bytes
				s.Files += c.Files
				if c.Dead != nil {
					dead.Bytes += c.Dead.Bytes
					dead.Files += c.Dead.Files
					dead.OlderThanDays = c.Dead.OlderThanDays
				}
				switch c.Sizing {
				case SizingMeasured:
					sized++
				case SizingPartial:
					sized++
					s.Sizing = SizingPartial
					s.Reason = "at least one cache subdir is a lower bound"
				case SizingUnknown:
					unreadable++
					s.Sizing = SizingPartial
					s.Reason = "at least one cache subdir could not be read"
				}
			}
			if sized == 0 && unreadable > 0 {
				// Not "≥ 0 B": nothing was read at all, so there is no lower bound to
				// state. A total summed from nothing but failures is unknown.
				s.Sizing = SizingUnknown
				s.Reason = "no cache subdir could be read"
			}
			if o.Age {
				s.Dead = &dead
			}
			s.Reclaimer = Reclaimer{Func: "PurgeCacheByAge", Detail: "per subdir — see the cache section", Trigger: "yolo prune --apply"}
			s.Verdict = VerdictYolo
			s.Note = "broken out per subdir below; the subdir rows are where coverage is decided"
		case name == filepath.Base(paths.BrokerDir()):
			// The brokers' directory (docs/design/boundary-broker.md §7, §8): the audit log
			// is BOUNDED, NOT PRUNED — its writer rotates it — and a launch's scope file and a
			// broker's run dir go with their owner, or, when a kill skipped that, with the next
			// start's liveness sweep; so no reclaimer runs here, by design.
			sizeStore(&s, s.Path, o)
			s.Reclaimer = Reclaimer{Detail: fmt.Sprintf("self-bounded: the audit log rotates at %d MiB "+
				"and keeps %d archives; a launch's scope file and a broker's run dir go with their owner, "+
				"or at the next start once that owner is gone",
				brokeraudit.RotateBytes>>20, brokeraudit.Archives)}
			s.Verdict = VerdictYolo
			s.Note = "the brokers' audit log and each launch's repository scope; `yolo prune` never touches the audit log"
		case name == filepath.Base(paths.GlobalMise()):
			sizeStore(&s, s.Path, o)
			miseRow(&s, o)
		case filepath.Clean(s.Path) == filepath.Clean(o.SamplesDir()):
			sizeStore(&s, s.Path, o)
			s.Reclaimer = Reclaimer{Detail: fmt.Sprintf("self-bounded: %d samples per store", MaxSamples)}
			s.Verdict = VerdictYolo
			s.Note = "this command's own sample ledger — bounded by the writer, so nothing has to reclaim it"
		default:
			sizeStore(&s, s.Path, o)
		}
		out = append(out, s)
	}
	if strayFiles > 0 {
		out = append(out, Store{
			Key: "state._files", Section: SectionState, Name: "(stray files)", Path: root,
			Sizing: SizingMeasured, Bytes: stray, Files: strayFiles,
			Reclaimer: none(), Verdict: VerdictHuman,
			Note: "loose files at the top of the state dir (layout markers, manifests); no sweep covers them",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

// miseRow fills the shared mise tool store's row: its reclaimer, and what that reclaimer would
// do now (docs/design/minimal-disk-footprint.md OQ-DF4, ruled 2026-10-05). It asks the same
// judgement `yolo prune` acts on — every jail's use record against the running jails — and
// deletes nothing.
//
// IN A JAIL THERE IS NOTHING TO CLAIM. A jail's tools, and those of every jail it launches, come
// from /mise, the host's store (jailMiseStoreDir binds /mise for an in-jail launch), not from the
// state dir's own mise/ in here, and the reclaimer is host-only besides, so promising it in-jail
// would promise a sweep that never comes.
func miseRow(s *Store, o Options) {
	if o.InJail() {
		s.Reclaimer, s.Verdict = none(), VerdictHuman
		s.Note = "this jail's tools, and those of every jail it launches, come from /mise, the " +
			"host's store, which `yolo stores` on the host reports, not from this copy"
		return
	}
	if o.IsMacOS() {
		s.Reclaimer, s.Verdict = none(), VerdictHuman
		s.Note = "a Mac's jails keep their tools in a volume inside the container VM (or in the " +
			"sandbox account, on macos-user), not here, and yolo does not reclaim that store yet"
		return
	}
	s.Reclaimer = Reclaimer{
		Func:    "PruneUnusedMiseVersions",
		Detail:  fmt.Sprintf("versions no jail used for %d d", int(prune.MiseVersionsWindow.Hours()/24)),
		Trigger: "yolo prune --apply; post-launch slot once consented",
	}
	s.Verdict = VerdictYolo
	var live runtime.LiveSet
	if rt := o.DetectRuntime(); rt != "" {
		live = prune.LiveYoloContainers(rt, o.Exec)
	}
	sw := prune.FindUnusedMiseVersions(s.Path, live, o.Now(), o.Budget)
	switch {
	case sw.Declined != "":
		s.Note = "yolo cannot tell which versions are unused right now: " + sw.Declined
		if sw.Remedy != "" {
			s.Note += "; to fix: " + sw.Remedy
		}
	case sw.Waiting != "":
		s.Note = "nothing judged yet: " + sw.Waiting
	case len(sw.Candidates) == 0:
		s.Note = fmt.Sprintf("every one of its %d installed tool version(s) was used by a jail within "+
			"%d days, or installed since", sw.Installed, int(prune.MiseVersionsWindow.Hours()/24))
	default:
		lower := ""
		if sw.Partial {
			lower = "at least "
		}
		s.Note = fmt.Sprintf("%s%s is in %d tool version(s) no jail has used for %d days: a launch "+
			"offers to remove them once they reach 1 GiB, and `yolo prune --apply` removes them now",
			lower, prune.FmtBytes(sw.Bytes), len(sw.Candidates), int(prune.MiseVersionsWindow.Hours()/24))
	}
}

// cacheStores inventories the shared cache, ONE ROW PER SUBDIR, because that is
// where the interesting distinction lives: PurgeCacheByAge covers a fixed list
// of subdirs and nothing else, so a subdir's coverage — and therefore whether
// anything at all reclaims those gigabytes — is a per-subdir fact. Coverage is
// read LIVE off prune's own exported lists, never re-typed here.
func cacheStores(o Options) []Store {
	root := o.GlobalCache()
	covered := map[string]Reclaimer{}
	// The post-launch slot purges this class too, but only on a HOST launch and
	// only once the offered tier has consent (OQ-BF1/BF2) — so the trigger a jail
	// sees and the trigger a host sees are different sentences, and printing the
	// host's inside a jail would promise a sweep that never comes.
	slot := "; post-launch slot once consented"
	if o.InJail() {
		slot = ""
	}
	for _, sub := range prune.CachePurgeDefaultSubdirs {
		covered[sub] = Reclaimer{
			Func:    "PurgeCacheByAge",
			Detail:  fmt.Sprintf("older than %dd", prune.NewDefaultOptions().CacheAge),
			Trigger: "yolo prune --apply" + slot,
		}
	}
	for _, sub := range prune.CachePurgeHeavySubdirs {
		covered[sub] = Reclaimer{
			Func:    "PurgeCacheByAge",
			Detail:  fmt.Sprintf("older than %dd, OPT-IN", prune.NewDefaultOptions().CacheAge),
			Trigger: "yolo prune --apply --purge-heavy-caches",
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		s := Store{
			Key: "cache", Section: SectionCache, Name: "(whole cache)", Path: root,
			Reclaimer: none(), Verdict: VerdictYolo,
		}
		if os.IsNotExist(err) {
			s.Sizing = SizingAbsent
		} else {
			s.Sizing = SizingUnknown
			s.Reason = err.Error()
		}
		return []Store{s}
	}

	var out []Store
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		name := e.Name()
		s := Store{
			Key: "cache." + name, Section: SectionCache, Name: "cache/" + name,
			Path: filepath.Join(root, name), Reclaimer: none(), Verdict: VerdictHuman,
		}
		if r, ok := covered[name]; ok {
			s.Reclaimer, s.Verdict = r, VerdictYolo
		}
		if name == "images" {
			// The tar cache is its own reclaimer, and its keep is RUNTIME-RESOLVED
			// since OQ-BF6 — asked here rather than hardcoded, so this row cannot
			// disagree with the sweep that acts on it.
			keep := prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, o.DetectRuntime())
			s.Reclaimer = Reclaimer{
				Func:    "PruneImageCache",
				Detail:  fmt.Sprintf("keep %d", keep),
				Trigger: "yolo prune --apply",
			}
			s.Verdict = VerdictYolo
			s.Note = "one-shot image load artifacts; a running jail depends on the store closure, never on these"
		}
		sizeStore(&s, s.Path, o)
		// NO PER-ROW NOTE for an uncovered subdir: the reclaimer column already
		// says "none" and the "what nothing reclaims" summary names it with its
		// size. A repeated sentence under every uncovered row buried the two rows
		// that have something specific to say.
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

// podmanImage is the subset of `podman images --format json` this command reads.
// The listing is READ-ONLY and starts no container, which §5.5 forbids.
type podmanImage struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Size   int64    `json:"Size"`
	Labels map[string]string
}

// imageStores inventories the container runtime's image store in three rows:
// yolo's own tagged images, the untagged rows nothing reclaims, and everything
// else, which is not yolo's at all.
//
// THE UNTAGGED ROW IS THE POINT (minimal-disk-footprint.md OQ-DF3 REACH, ruled
// 2026-09-08): rows that predate the provenance label are left alone
// PERMANENTLY, and surfaced HERE as a class nothing reclaims — the count, the
// bytes, why yolo declines, and that `podman image prune` is the user's to run.
// Explicitly NOT on the launch path: an unactionable line in front of every jail
// start is what OQ-BF1 ruled against.
func imageStores(o Options, rt string) []Store {
	mk := func(key, name string) Store {
		return Store{Key: key, Section: SectionImages, Name: name, Path: rt + " image store",
			Reclaimer: none(), Verdict: VerdictHuman}
	}
	ours := mk("images.yolo", "yolo-jail images")
	ours.Reclaimer = Reclaimer{
		Func: "PruneOldImages",
		// NOT A COUNT SINCE OQ-LS3: retention is one CURRENT-IMAGE POINTER per
		// workspace (prune.CurrentImageTags), union'd with the `podman ps` veto.
		// The pointer count is machine state rather than a constant, so this row
		// names the RULE — `yolo prune` prints the number it found.
		Detail: "each workspace's current image, liveness-vetoed",
		// OQ-BF5 moved this out of the pre-attach launch path and into the
		// post-launch housekeeping slot; the debounce is unchanged.
		Trigger: "post-launch slot (24h) + yolo prune --apply",
	}
	ours.Verdict = VerdictYolo
	untagged := mk("images.untagged", "untagged rows")
	untagged.Note = "yolo declines these permanently: an untagged row has lost the repository name that was the " +
		"only evidence it was yolo's, and it predates the provenance label. `podman image prune` is yours to run"
	other := mk("images.other", "other images")
	other.Verdict = VerdictNotOurs
	other.Note = "images this runtime holds that are not yolo-jail's — yolo never counts them as reclaimable"

	rows := []Store{ours, untagged, other}
	setAll := func(sizing Sizing, reason string) []Store {
		for i := range rows {
			rows[i].Sizing = sizing
			rows[i].Reason = reason
		}
		return rows
	}

	if !slices.Contains(paths.SupportedRuntimes, rt) {
		return setAll(SizingAbsent, "no container runtime on this notch")
	}
	if rt != "podman" {
		// Apple Container's listing is not podman's, and DF3's label probe was
		// explicitly NOT MEASURED there. Reporting unknown is the honest answer;
		// inventing a parse would be a figure with no provenance.
		return setAll(SizingUnknown, "Apple Container's image listing is not read by this command")
	}
	res := o.Exec([]string{rt, "images", "-a", "--format", "json"}, probeTimeout)
	if !res.Ran || res.RC != 0 {
		return setAll(SizingUnknown, "could not list images ("+rt+" unreachable or refused)")
	}
	var imgs []podmanImage
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &imgs); err != nil {
		return setAll(SizingUnknown, "could not parse the image listing: "+err.Error())
	}

	seen := map[string]bool{}
	for _, im := range imgs {
		if im.ID == "" || seen[im.ID] {
			continue // one row per tag in podman's output; the image is the store entry
		}
		seen[im.ID] = true
		switch {
		case len(im.Names) == 0:
			// WHEN THE PROVENANCE LABEL OF OQ-DF3 SHIPS, a labelled untagged row
			// becomes attributable and belongs in `ours`, not here. The filter
			// belongs at this line; it is not written yet because nothing sets the
			// label, and a filter keyed on a spelling no image carries would be a
			// claim this command cannot check.
			rows[1].Count++
			rows[1].Bytes += im.Size
		case slices.ContainsFunc(im.Names, func(n string) bool {
			return strings.Contains(n, paths.JailImageRepoShort)
		}):
			rows[0].Count++
			rows[0].Bytes += im.Size
		default:
			rows[2].Count++
			rows[2].Bytes += im.Size
		}
	}
	for i := range rows {
		rows[i].Sizing = SizingMeasured
		rows[i].CountLabel = "images"
	}
	// One sentence for all three, because it qualifies all three: podman's per-image
	// Size counts every layer the image holds, and a layer shared with another image
	// is counted in both. Summing them therefore OVERSTATES the store, and the
	// overstatement is largest exactly where these rows matter — a re-stream twin
	// measures 3.5 GB here and 91 kB of unique bytes on disk.
	rows[0].Reason = "sum of per-image sizes; layers shared between images are counted in each"
	rows[1].Reason = rows[0].Reason
	rows[2].Reason = rows[0].Reason
	return rows
}

// scratchStores inventories the podman scratch volumes in two rows: those a jail still
// holds, and those of jails that are gone — the leftovers a remover never reached.
//
// Sized by walking each volume's mountpoint under ONE budget per row, so a row of four
// nested container stores costs what one store's walk may. A rootless store's volumes
// hold files owned by subordinate ids the host user cannot read, which the walk counts
// as unreadable subtrees and reports as a lower bound; a mountpoint that is not on this
// machine at all (podman's VM, on macOS) makes the row unknown rather than zero.
func scratchStores(o Options, rt string) []Store {
	mk := func(key, name string) Store {
		return Store{Key: key, Section: SectionVolumes, Name: name, Path: rt + " volumes",
			Verdict: VerdictYolo, CountLabel: "volumes"}
	}
	live := mk("volumes.scratch.live", "live jails' scratch")
	live.Reclaimer = Reclaimer{Func: "RemoveScratchVolume", Detail: "once the jail exits",
		Trigger: "the launcher's detached remover"}
	live.Note = "each running jail's /tmp, /var/tmp and nested container store; deleted in the background once its jail exits"
	gone := mk("volumes.scratch.gone", "gone jails' scratch")
	gone.Reclaimer = Reclaimer{Func: "PruneScratchVolumes", Detail: "dangling, past a 1m floor",
		Trigger: "every launch's housekeeping slot + yolo prune --apply"}
	gone.Note = "left by a launcher that died before its remover started, or by a removal cut short"
	rows := []Store{live, gone}
	setAll := func(sizing Sizing, reason string) []Store {
		for i := range rows {
			rows[i].Sizing, rows[i].Reason, rows[i].CountLabel = sizing, reason, ""
		}
		return rows
	}
	if !slices.Contains(paths.SupportedRuntimes, rt) {
		return setAll(SizingAbsent, "no container runtime on this notch")
	}
	if rt != "podman" {
		return setAll(SizingAbsent, "this runtime's scratch dirs are tmpfs")
	}
	vols, known := prune.ListScratchVolumes(rt, o.Exec)
	if !known {
		return setAll(SizingUnknown, "could not list volumes ("+rt+" unreachable or refused)")
	}
	var cutoff time.Time
	if o.Age {
		cutoff = o.Now().Add(-time.Duration(o.AgeDays * 24 * float64(time.Hour)))
	}
	for i := range rows {
		rows[i].Sizing = SizingMeasured
		if o.Age {
			rows[i].Dead = &Dead{OlderThanDays: o.AgeDays}
		}
	}
	deadline := [2]time.Time{o.Now().Add(o.Budget), o.Now().Add(o.Budget)}
	for _, v := range vols {
		i := 0
		if v.Dangling {
			i = 1
		}
		r := &rows[i]
		r.Count++
		if r.Sizing == SizingUnknown {
			continue
		}
		left := deadline[i].Sub(o.Now())
		if left <= 0 {
			r.Sizing, r.Reason = SizingPartial, fmt.Sprintf("walk budget %s exhausted", o.Budget)
			continue
		}
		res, err := o.Walk(v.Mountpoint, cutoff, left, o.Now)
		if err != nil {
			r.Sizing, r.Reason = SizingUnknown, "a volume's mountpoint is not readable here ("+err.Error()+")"
			continue
		}
		r.Bytes += res.Bytes
		r.Files += res.Files
		r.Unreadable += res.Unreadable
		if r.Dead != nil {
			r.Dead.Bytes += res.DeadBytes
			r.Dead.Files += res.DeadFiles
		}
		if res.Partial {
			r.Sizing, r.Reason = SizingPartial, fmt.Sprintf("walk budget %s exhausted", o.Budget)
		} else if res.Unreadable > 0 && r.Sizing == SizingMeasured {
			r.Sizing, r.Reason = SizingPartial, fmt.Sprintf("%d unreadable subtree(s)", r.Unreadable)
		}
	}
	return rows
}

// nixOutputClass is one family of yolo's own /nix/store outputs.
type nixOutputClass struct {
	key    string
	suffix string
	name   string
	note   string
	// reaped says whether OQ-BF3's named store delete covers this suffix. It is
	// a claim about internal/prune, so TestNixClassCoverageMatchesTheReclaimer
	// runs that reclaimer against a temp store and compares — the day a suffix
	// joins or leaves prune's own list, this table fails instead of lying.
	reaped bool
}

// nixOutputClasses are the three name families the design measured (§2.3). They
// are matched by SUFFIX, which is how a nix output path is named: <hash>-<name>.
var nixOutputClasses = []nixOutputClass{
	{"nix.install-prefix", "-yolo-jail-install-prefix", "install prefixes",
		"the binaries and flake bundle every launch mounts at /opt/yolo-jail; a superseded one is garbage the moment its checkout's out-link moves", true},
	{"nix.go-build", "-yolo-jail-go-0-dev", "Go builds",
		"build-time only — the prefix copies the binaries out, so a Go build's output is garbage the moment the prefix exists", true},
	{"nix.stream", "-stream-yolo-jail", "stream scripts",
		"the scripts that stream an image into the runtime; the closures they hold alive are NOT counted here", false},
}

// storeOutputReclaimer is what OQ-BF3 shipped, and it is FRAME-DEPENDENT: the
// pass refuses in-jail, because there /nix/store is a read-only bind of the
// host's with the gcroots dir unmounted, so a jail cannot tell rooted from
// unrooted. A row that claimed a reclaimer an in-jail yolo will never run would
// be exactly the kind of unchecked claim this command exists to replace.
func storeOutputReclaimer(inJail bool) (Reclaimer, Verdict, string) {
	if inJail {
		return Reclaimer{Detail: "host-only — an in-jail yolo cannot tell rooted from unrooted"},
			VerdictHuman,
			"a HOST yolo reclaims these; from in here nothing does"
	}
	return Reclaimer{
		Func:    "SupersededStoreOutputs",
		Detail:  "unrooted only, by name",
		Trigger: "post-launch slot (24h) + yolo prune --apply",
	}, VerdictYolo, ""
}

// nixStores inventories yolo's own outputs in the nix store — the class with no
// reclaimer and no trigger, and the largest such class the design measured.
//
// IT NEVER ASKS THE STORE DATABASE ANYTHING. No `nix-store --query --roots`, no
// `nix store gc --dry-run`, no GC lock: a read that stalls every concurrent
// build on the machine is exactly what §5.5 forbids, and it is why the rooted /
// unrooted split the design reports is absent here and said to be absent rather
// than guessed.
func nixStores(o Options) []Store {
	entries, err := os.ReadDir(o.NixStore)
	if err != nil {
		var out []Store
		for _, c := range nixOutputClasses {
			s := Store{Key: c.key, Section: SectionNix, Name: c.name, Path: o.NixStore,
				Reclaimer: none(), Verdict: VerdictHuman, Note: c.note}
			if os.IsNotExist(err) {
				s.Sizing = SizingAbsent
			} else {
				s.Sizing = SizingUnknown
				s.Reason = err.Error()
			}
			out = append(out, s)
		}
		return out
	}

	byClass := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		for _, c := range nixOutputClasses {
			if strings.HasSuffix(name, c.suffix) {
				byClass[c.key] = append(byClass[c.key], filepath.Join(o.NixStore, name))
			}
		}
	}

	var out []Store
	for _, c := range nixOutputClasses {
		// A class NOT in OQ-BF3's list still has the §2.3 finding hanging over it:
		// nothing on a normal machine ever runs `nix store gc` — not the daemon
		// (min-free = 0), and yolo's own is opt-in, host-only and human-typed.
		reclaimer := Reclaimer{Detail: "nix store gc only, which nothing runs on its own"}
		verdict, extra := VerdictHuman, ""
		if c.reaped {
			reclaimer, verdict, extra = storeOutputReclaimer(o.InJail())
		}
		note := c.note
		if extra != "" {
			note += " — " + extra
		}
		s := Store{
			Key: c.key, Section: SectionNix, Name: c.name,
			Path:       filepath.Join(o.NixStore, "*"+c.suffix),
			Reclaimer:  reclaimer,
			Verdict:    verdict,
			Note:       note,
			Count:      len(byClass[c.key]),
			CountLabel: "paths",
		}
		if s.Count == 0 {
			s.Sizing = SizingAbsent
			out = append(out, s)
			continue
		}
		sizeClass(&s, byClass[c.key], o)
		out = append(out, s)
	}
	return out
}

// sizeClass sums a set of store paths under ONE shared budget — the class is the
// unit a budget is spent on, so a class of 250 paths cannot cost 250 budgets.
func sizeClass(s *Store, roots []string, o Options) {
	start := o.Now()
	deadline := start.Add(o.Budget)
	s.Sizing = SizingMeasured
	done := 0
	for _, root := range roots {
		remaining := deadline.Sub(o.Now())
		if o.Budget > 0 && remaining <= 0 {
			s.Sizing = SizingPartial
			s.Reason = fmt.Sprintf("walk budget %s exhausted after %d of %d paths", o.Budget, done, len(roots))
			break
		}
		done++
		res, err := o.Walk(root, time.Time{}, remaining, o.Now)
		if err != nil {
			s.Unreadable++
			continue
		}
		s.Bytes += res.Bytes
		s.Files += res.Files
		if res.Partial {
			s.Sizing = SizingPartial
			s.Reason = fmt.Sprintf("walk budget %s exhausted", o.Budget)
			break
		}
	}
	if s.Unreadable > 0 && s.Sizing == SizingMeasured {
		s.Sizing = SizingPartial
		s.Reason = fmt.Sprintf("%d path(s) unreadable", s.Unreadable)
	}
	s.Elapsed = o.Now().Sub(start).Round(time.Millisecond).String()
	if s.Sizing == SizingMeasured {
		s.Reason = "apparent size; nix hardlinks are counted in every path that holds them"
	}
}

// SectionDurable is every known workspace's durable dir (docs/design/durable-scratch-space.md
// §5.4): the agents' scratch worktrees and clones under `<workspace>/.yolo/durable`.
const SectionDurable = "workspace durable dirs"

// durableStores is one row per workspace this command already knows — the workspaces of the
// runtime's yolo containers (prune.FindYoloWorkspaces) — whose durable dir exists. A
// workspace with none gets no row: there are no bytes to account for, and "absent" rows for
// every jail launched before the durable dir existed would only be noise.
//
// ⚠ THAT IS ONLY THE WORKSPACES WITH A CONTAINER THE RUNTIME STILL LISTS. Jails run with
// `--rm`, so a workspace whose jail has exited has no row, and those are exactly the durable
// dirs most likely to be forgotten. yolo keeps no durable list of workspaces to read instead
// (DS-D22 records the limit); the launch line and `yolo check` in the workspace are the views
// that never miss one, and each row's note says so.
//
// THE VERDICT IS HUMAN AND THE RECLAIMER NONE, by OQ-DS2's ruling: only the agent that made a
// worktree knows whether it holds unlanded work, so yolo keeps the growth visible and deletes
// nothing. The figures follow DS-D3 in every frame: an lstat walk beneath an os.Root and git's
// own admin files, never git itself, never a file beneath the durable dir.
func durableStores(o Options, rt string) []Store {
	if !slices.Contains(paths.SupportedRuntimes, rt) {
		return nil
	}
	var out []Store
	for _, ws := range prune.FindYoloWorkspaces(rt, o.Exec) {
		dir := durable.HostPath(ws)
		// Every read goes through durable.Open, which refuses a link at `.yolo` as well as
		// at `durable`: the jail can point `.yolo` at any host directory, whose names and
		// sizes a by-path read would print here.
		root, err := durable.Open(ws)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			out = append(out, Store{
				Key: "durable." + shortHash(ws), Section: SectionDurable,
				Name: filepath.Base(ws) + "/.yolo/durable", Path: dir,
				Verdict: VerdictHuman, Sizing: SizingUnknown,
				Reason:    durable.Printable(durable.Reason(err, ws)),
				Reclaimer: Reclaimer{Detail: "the user's work; yolo never reclaims it — `git worktree remove`"},
			})
			continue
		}
		root.Close()
		sc := durable.ScanDir(durable.ScanOptions{Workspace: ws,
			Aliases: map[string]string{"/workspace": ws}})
		s := Store{
			Key:        "durable." + shortHash(ws),
			Section:    SectionDurable,
			Name:       filepath.Base(ws) + "/.yolo/durable",
			Path:       dir,
			Count:      len(sc.Worktrees),
			CountLabel: "worktrees",
			Verdict:    VerdictHuman,
			Reclaimer:  Reclaimer{Detail: "the user's work; yolo never reclaims it — `git worktree remove`"},
		}
		sz, err := durable.Measure(ws, o.Budget, o.Now)
		switch {
		case err != nil:
			s.Sizing, s.Reason = SizingUnknown, err.Error()
		case sz.Partial:
			s.Sizing, s.Bytes, s.Reason = SizingPartial, sz.Bytes, fmt.Sprintf("walk budget %s exhausted", o.Budget)
		default:
			s.Sizing, s.Bytes = SizingMeasured, sz.Bytes
		}
		note := "workspace " + ws
		if len(sc.Worktrees) > 0 && !sc.Worktrees[0].LastActive.IsZero() {
			// The name is the jail's choice, and the note is rendered as rich markup, so it is
			// escaped as well as Printable: a directory named `[/dim][bold red]` stays text.
			note += fmt.Sprintf("; oldest idle %s (%s)",
				durable.HumanIdle(o.Now().Sub(sc.Worktrees[0].LastActive)), richtext.Escape(sc.Worktrees[0].Name()))
		}
		if len(sc.Others) > 0 {
			note += fmt.Sprintf("; %d other entries", len(sc.Others))
		}
		s.Note = note + ". `yolo check` in that workspace lists each worktree. Only workspaces with a " +
			"jail the runtime still lists get a row here."
		out = append(out, s)
	}
	return out
}

// shortHash names a per-workspace row's ledger key stably.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

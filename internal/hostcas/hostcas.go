// Package hostcas decides whether a launch ALIASES a host content-addressed
// cache into the jail, instead of letting the jail pool a second copy of the
// same bytes — lever L9 of docs/design/disk-levers-and-backfill.md, ruled there
// as OQ-BF10 on 2026-09-08.
//
// It is a THIRD DISPOSITION, and that is why it lives in its own package rather
// than in internal/prune. Backfill deletes what accumulated; retention bounds
// what accumulates; aliasing makes the store NOT EXIST TWICE. Nothing here
// deletes anything, and nothing here is a reaper.
//
// # CONTENT-ADDRESSED ONLY, AND THE REASON IS INJECTION, NOT SIZE
//
// This is the whole of the ruling and it is not a size heuristic that could be
// relaxed for a big enough cache. A **path-keyed** store lets a jail write
// content that the host tool later reads BECAUSE OF WHERE IT SITS: the jail
// chooses both the key and the bytes, so a jail write is an instruction to the
// host's own build. A **content-addressed** store's keys are digests of their
// own contents, so the worst a hostile jail can do is add a blob under a name
// that does not match its digest — which the reading tool rejects — or fill the
// disk. That asymmetry is the entire justification for widening the trust step
// below, and it is why a path-keyed store is not "lower value" here but
// CATEGORICALLY EXCLUDED.
//
// Consequence, stated so nobody re-litigates it: this may not be generalised to
// "share the host's caches". Every candidate is judged one store at a time
// against the CAS test, with its evidence recorded beside it in [Stores].
//
// # THE TRUST STEP, AND WHY THE CAS ANSWERS IT
//
// The precedent is the `:ro` nix-store bind (`hostNixStore`,
// internal/cli/run/hostprobes.go): yolo already shares the host's store rather
// than duplicating it. THIS ONE IS WRITABLE, and that widening is the whole risk
// surface — jail code can write into the host user's own cache. A cache is
// useless read-only, so there is no narrower version of this feature; the CAS
// property is what makes the writable form acceptable, and it is the only thing
// that does.
//
// # THE GATES
//
// Aliased if and only if: the store is content-addressed; the runtime is podman;
// the host is not macOS (where the jail is Linux in a VM and nothing is
// compatible anyway); the host and the jail build for the same platform; the host
// store exists, is a directory and is writable; and aliasing would not replace a
// warm private copy with an empty one. Every failure DEGRADES TO THE STATUS QUO —
// the jail keeps its own copy — and NEVER fails a launch. A jail start must never
// depend on a host store being present.
//
// # ONE PREDICATE, TWO READERS, NO RECORD
//
// The launch emits the mount; `yolo stores` explains the decision and points at
// the stranded private copy. Both call [Plan] over injected facts, so there is
// nothing for yolo to record about the aliasing and therefore no writer to name:
// the decision is a pure function of the host's own filesystem and platform, and
// re-deriving it is cheaper and cannot go stale. A recorded answer here would be
// a second source of truth for a question the disk already answers.
package hostcas

import (
	"path/filepath"
	goruntime "runtime"
	"strings"
)

// HostPlatform is "<goos>/<goarch>" of THIS process — what the host's own copy
// of a tool builds for.
func HostPlatform() string { return goruntime.GOOS + "/" + goruntime.GOARCH }

// JailPlatform is what the JAIL's copy of a tool builds for.
//
// Derived, not probed, on the precedent internal/cli/run's containerJailPlatform
// states for the capture manifest: "the jail is a Linux container on THIS
// machine, so its architecture is a fact about the local one, known without
// asking anything." Those are two spellings of one value with different
// consumers, and TestJailPlatformMatchesTheCaptureManifestsPlatform pins them
// together so neither can drift alone.
func JailPlatform() string { return "linux/" + goruntime.GOARCH }

// jailCacheDir is the container-side XDG cache root — the destination
// `podmanBaseMounts` binds paths.GlobalCache() at, and therefore the prefix
// every alias destination nests inside.
//
// Spelled here because the DESTINATION and the STRANDED path are two halves of
// one fact: the bytes the alias hides are the ones behind this mount. run's
// emitter reads Disposition.Dest rather than rebuilding it, and
// TestAliasDestinationNestsInsideTheCacheMount compares this against the argv
// the assembler actually emits, so the two cannot drift.
const jailCacheDir = "/home/agent/.cache"

// CacheRoot resolves a user's XDG cache root — XDG_CACHE_HOME when it is
// absolute, else $HOME/.cache — through a getenv seam.
//
// THE SEAM IS THE WHOLE REASON THIS IS HERE rather than at each call site. The
// launcher must read the environment through Options.Getenv or a frozen argv
// stops being a function of its inputs (goldenOptions returns "" for everything,
// which is what keeps every golden argv in internal/cli/run free of an alias
// mount); `yolo stores` has no such seam and passes os.Getenv. One
// implementation, two seams, so "the launcher and the inventory look in the same
// place" is true by construction instead of by a test comparing two copies.
//
// XDG_CACHE_HOME then $HOME/.cache is the convention the tools in [Stores]
// follow — pants' documented default store is ~/.cache/pants/lmdb_store. A user
// who has moved their cache somewhere this does not name simply has no store
// found, and the launch degrades to the status quo. That is the right direction
// for a guess: yolo never invents a path and never creates the source.
func CacheRoot(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	if xdg := getenv("XDG_CACHE_HOME"); filepath.IsAbs(xdg) {
		return filepath.Clean(xdg)
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".cache")
	}
	return ""
}

// Store is one recognised content-addressed cache yolo may alias.
//
// THE SET IS CODE, NOT CONFIG, and that is a ruling rather than an omission.
// OQ-BF10 fixes the membership test — "aliased if and only if content-addressed
// … limited to tools that document a shared per-user store" — and then closes
// the question: *"let's keep it this way and we can analyze storage after all
// this is implemented to see if we should go further."* A config key naming a
// store would hand that decision to a user who cannot check the CAS property,
// and there is nothing else for a key to carry: yolo picks both sides of the
// mount from this table, so no path is user-supplied.
type Store struct {
	// Name is the stable report key.
	Name string
	// Tool is the program whose cache this is, for a human-readable notice.
	Tool string
	// CacheRel is the store's path relative to the XDG cache root, and it is the
	// SAME on both sides: the host source is <host cache root>/<CacheRel> and the
	// jail destination is /home/agent/.cache/<CacheRel>. That the two agree is
	// what makes the alias an alias rather than a relocation.
	CacheRel string
	// Evidence is why this store passes the CAS test, MEASURED rather than
	// assumed. It is printed by `yolo stores`, so a reader can check the claim
	// against their own disk instead of trusting this table.
	Evidence string
}

// Source is the host-side path of this store under a given XDG cache root.
func (s Store) Source(hostCacheRoot string) string {
	return filepath.Join(hostCacheRoot, s.CacheRel)
}

// Dest is the container-side path the alias mounts at.
func (s Store) Dest() string { return filepath.Join(jailCacheDir, s.CacheRel) }

// Stranded is the HOST side of the jail's own private copy — the bytes the alias
// hides. jailCacheHost is paths.GlobalCache(), the directory bound at
// [jailCacheDir].
func (s Store) Stranded(jailCacheHost string) string {
	return filepath.Join(jailCacheHost, s.CacheRel)
}

// Stores is the recognised set. ONE ENTRY, deliberately.
//
// Four sibling caches were evaluated against the CAS test and EXCLUDED, and the
// reasons are recorded here rather than in a report nobody will find, because
// "why isn't npm in this list?" is the first question this table earns:
//
//   - pants' `named_caches` (13 G here) — not content-addressed, and
//     path-poisoned besides: a sampled `pex_root/…/INTERP-INFO` records
//     interpreters under the JAIL's home. Excluded by the ruling BY NAME.
//   - npm `_cacache` — `content-v2` is a CAS, but `index-v5` maps a REQUEST URL
//     to an integrity digest (MEASURED: an index entry keyed
//     `make-fetch-happen:request-cache:https://registry.npmjs.org/…`). A jail
//     that writes both halves has the host's npm serve the jail's bytes for that
//     URL, and the integrity check passes because the jail chose it. The store as
//     a unit is path-keyed; half a directory whose halves must stay consistent is
//     not an aliasable unit.
//   - Go's build cache — `<actionID>-a` maps an action ID CHOSEN BY THE WRITER to
//     an output ID (MEASURED: `v1 <actionID> <outputID> …`). Same shape as npm's
//     index, same exclusion. It also self-trims, so it is the candidate with the
//     least to gain.
//   - uv and pip — `simple-v2*` and `http-v2` are index/URL-keyed; `uv`'s
//     `environments-v2` holds absolute-path venvs on top of that.
//   - `nce` — content-addressed by name, but it is the download store
//     `named_caches`' INTERP-INFO records point INTO, so the design rules it
//     "coupled to `named_caches` and cannot be reasoned about separately from
//     it".
//
// Widening this list is NOT a follow-up item. It is a question to re-ask against
// a post-implementation storage measurement, which OQ-BF9's sample ledger
// (`yolo stores`) is what makes possible.
var Stores = []Store{{
	Name:     "pants-lmdb-store",
	Tool:     "pants",
	CacheRel: "pants/lmdb_store",
	Evidence: "digest-named blobs, verified: immutable/files/<2-hex>/<64-hex> and the 64-hex name " +
		"IS the sha256 of the file's contents (MEASURED 2026-09-09 on a 232 MB blob); the blobs are " +
		"mode r-xr-xr-x. pants' own default is ONE per-user store shared by every repo on the machine, " +
		"so multi-process sharing is the designed norm rather than a workaround",
}}

// Code is the machine-readable outcome of one store's decision. It exists so the
// launch notice, the inventory row and the tests all name the same outcome — a
// test that asserted only the prose would pass on a message that had stopped
// describing the gate it came from.
type Code string

const (
	// CodeAliased — the mount is emitted.
	CodeAliased Code = "aliased"
	// CodeBackend — not podman. Apple Container and macos-user both land here.
	CodeBackend Code = "backend"
	// CodeMacOS — a macOS host, refused as its own rule rather than as a side
	// effect of the platform comparison below.
	CodeMacOS Code = "macos"
	// CodePlatform — the host and the jail build for different platforms.
	CodePlatform Code = "platform-mismatch"
	// CodeNoCacheRoot — the launching user's XDG cache root could not be
	// resolved, so there is no path to look under.
	CodeNoCacheRoot Code = "no-host-cache-root"
	// CodeSameTree — the host store IS (or sits inside) the jail's own cache, so
	// there is no second copy to alias away.
	CodeSameTree Code = "same-tree"
	// CodeAbsent — no host store. The common case on a machine that does not run
	// the tool, and not an error.
	CodeAbsent Code = "absent"
	// CodeNotDir — the host path exists and is not a directory.
	CodeNotDir Code = "not-a-directory"
	// CodeUnwritable — the host store exists but the launching user cannot write
	// it, and a cache the jail can only read is a cache the jail cannot use.
	CodeUnwritable Code = "unwritable"
	// CodeWouldColdStart — the host store is EMPTY while the jail's private copy
	// is not, so aliasing would hide a warm cache behind an empty one.
	CodeWouldColdStart Code = "would-cold-start"
	// CodeRelocated — the user moved this cache subdir somewhere else with
	// `cache_relocations`, which is an EXPLICIT decision about where these bytes
	// live. An automatic optimisation does not overrule one.
	CodeRelocated Code = "relocated-by-config"
)

// Disposition is what one launch — or one inventory run — decided about one
// store. Source/Dest/Stranded are filled whatever the outcome, because the
// inventory has to be able to say WHICH paths the decision was about even when
// the answer was no.
type Disposition struct {
	Store    Store
	Aliased  bool
	Source   string
	Dest     string
	Stranded string
	Code     Code
	// Reason is the sentence a human reads. Empty when Aliased.
	Reason string
}

// Presence is what the planner needs to know about one directory. It is gathered
// through a seam ([Facts.Probe]) rather than with os.Stat here, and the reason is
// testability of the degenerate cases specifically: this project's own suite runs
// as root, where access(2) always reports write permission, so an UNWRITABLE
// directory is not constructible in a test. A gate whose failure branch no test
// can reach is a gate that works until the day it matters.
type Presence struct {
	Exists   bool
	IsDir    bool
	Writable bool
	// Empty is true for a directory with no entries. It is only consulted for the
	// cold-start gate, so a probe that cannot cheaply answer it may report false.
	Empty bool
}

// Facts are the launch-independent inputs to a decision. Everything is passed in
// rather than read here, so the launcher and `yolo stores` can be shown to make
// the same decision from the same facts.
type Facts struct {
	// Runtime is the effective container runtime ("podman", "container",
	// "macos-user").
	Runtime string
	// IsMacOS is the host OS check, injected on the run pipeline's own convention
	// (never paths.IsMacOS at the decision site, so a golden argv is the same on
	// every host).
	IsMacOS bool
	// HostPlatform is "<goos>/<goarch>" of the launching process — what the HOST's
	// copy of the tool builds for.
	HostPlatform string
	// JailPlatform is what the JAIL's copy builds for: "linux/<goarch>", on
	// containerJailPlatform's precedent ("the jail is a Linux container on THIS
	// machine, so its architecture is a fact about the local one").
	//
	// On every launch yolo can make today the ARCH halves are equal by
	// construction and the OS half is the live discriminator — a darwin host is
	// the case that fails. It is still spelled as a comparison rather than as an
	// OS check, because the reason the gate exists is that a CAS blob is an
	// arch-specific build artifact: the day a cross-arch launch is representable,
	// this refuses instead of poisoning 27 G of someone's build cache.
	JailPlatform string
	// HostCacheRoot is the launching user's XDG cache root.
	HostCacheRoot string
	// JailCacheHost is paths.GlobalCache() — the HOST side of the directory bound
	// at the jail's ~/.cache, and therefore where the stranded private copy lives.
	JailCacheHost string
	// RelocatedSegments are the top-level cache subdirs the user moved elsewhere
	// with `cache_relocations`. A store under one of them is NOT aliased — see
	// CodeRelocated.
	RelocatedSegments []string
	// Probe gathers a Presence. nil => DefaultProbe.
	Probe func(string) Presence
}

// Plan decides every recognised store, in table order. It returns one
// Disposition per entry in [Stores] — never a filtered list — because the
// inventory's job is to explain the stores yolo did NOT alias just as much as the
// ones it did.
//
// It touches the filesystem only through Facts.Probe, and it never creates,
// moves or deletes anything. The destination's backing directory is provisioned
// by the caller (run.prepareHostCASAlias), which is the same split
// cache_relocations keeps: the decision is pure, the provisioning is the
// pipeline's.
func Plan(f Facts) []Disposition {
	probe := f.Probe
	if probe == nil {
		probe = DefaultProbe
	}
	out := make([]Disposition, 0, len(Stores))
	for _, s := range Stores {
		out = append(out, decide(s, f, probe))
	}
	return out
}

// Aliased returns just the dispositions that produced a mount, which is what the
// argv emitter wants. Sorted by table order, so the argv is deterministic.
func Aliased(ds []Disposition) []Disposition {
	var out []Disposition
	for _, d := range ds {
		if d.Aliased {
			out = append(out, d)
		}
	}
	return out
}

func decide(s Store, f Facts, probe func(string) Presence) Disposition {
	d := Disposition{
		Store:    s,
		Source:   s.Source(f.HostCacheRoot),
		Dest:     s.Dest(),
		Stranded: s.Stranded(f.JailCacheHost),
	}
	if f.HostCacheRoot == "" {
		d.Source = ""
	}
	if f.JailCacheHost == "" {
		d.Stranded = ""
	}

	// The backend gate is first, and it is storePackagesEligible's first clause
	// for the same reason: Apple Container cannot be relied on to honor a nested
	// bind's options, and macos-user has no container to mount into at all.
	if f.Runtime != "podman" {
		d.Code, d.Reason = CodeBackend, "it needs podman, and this launch uses the "+
			f.Runtime+" runtime"
		return d
	}
	// NEVER ON macOS. Stated as its own refusal rather than left to fall out of
	// the platform comparison below: a rule that is only ever enforced as a side
	// effect of a different comparison is one that silently stops being true.
	if f.IsMacOS {
		d.Code, d.Reason = CodeMacOS, "it is never done on macOS: the jail is Linux in a VM, "+
			"so the host's build artifacts and the jail's are not interchangeable"
		return d
	}
	if f.HostPlatform == "" || f.JailPlatform == "" || f.HostPlatform != f.JailPlatform {
		d.Code, d.Reason = CodePlatform, "the host builds for "+orUnknown(f.HostPlatform)+
			" and the jail for "+orUnknown(f.JailPlatform)+
			", and a content-addressed blob is an architecture-specific build artifact"
		return d
	}
	if f.HostCacheRoot == "" || !filepath.IsAbs(f.HostCacheRoot) {
		d.Code, d.Reason = CodeNoCacheRoot, "the launching user's cache directory could not be "+
			"resolved, so there is nowhere to look for a host store"
		return d
	}
	// SAME TREE means there is no second copy to alias away — the source and the
	// destination's own backing are one directory. Reachable when XDG_CACHE_HOME
	// points at yolo's state dir, and the mount it would otherwise emit is a
	// directory bound over itself.
	if f.JailCacheHost != "" && (under(d.Source, f.JailCacheHost) || under(f.JailCacheHost, d.Source)) {
		d.Code, d.Reason = CodeSameTree, "the host store is inside the jail's own cache ("+
			f.JailCacheHost+"), so there is no second copy of it to alias away"
		return d
	}

	// AN EXPLICIT CONFIG DECISION OUTRANKS AN AUTOMATIC OPTIMISATION, and this one
	// would otherwise be defeated silently in the direction its own feature exists
	// to prevent: a user relocates `pants` to get 40 G off the disk $HOME is on,
	// and the alias would put `pants/lmdb_store` — 27 G of it — straight back onto
	// that disk, since the host store IS under the user's home cache. Both mounts
	// would apply (podman orders by destination depth, so the deeper one wins for
	// its own subtree), which is exactly what makes the loss silent.
	for _, seg := range f.RelocatedSegments {
		if seg == "" || seg != firstSegment(s.CacheRel) {
			continue
		}
		d.Code, d.Reason = CodeRelocated, "cache_relocations moves "+seg+
			" to storage you chose, and aliasing the host's copy of "+s.CacheRel+
			" would put those bytes back under your home cache"
		return d
	}

	src := probe(d.Source)
	switch {
	case !src.Exists:
		// NOT AN ERROR, and NOT CREATED. Creating it would alias an empty
		// directory over whatever the jail has, which is the one outcome that is
		// strictly worse than doing nothing.
		d.Code, d.Reason = CodeAbsent, "this host has no "+s.Tool+" store at "+d.Source
		return d
	case !src.IsDir:
		d.Code, d.Reason = CodeNotDir, d.Source+" exists but is not a directory"
		return d
	case !src.Writable:
		d.Code, d.Reason = CodeUnwritable, d.Source+" is not writable by the launching user, "+
			"and a cache the jail can only read is a cache the jail cannot use"
		return d
	}

	// THE COLD-START GATE, which the ruling does not name and P4 requires. An
	// empty host store aliased over a warm private copy hides a cache and buys
	// nothing: the jail then re-fetches everything it already had, and §5.2's
	// whole argument for the OFFERED tier is that an unbounded re-fetch is never
	// imposed without asking. Emptiness is one ReadDir, so this costs nothing;
	// the partial case (a small host store over a large private one) is a
	// one-time partial re-fetch into a store BOTH sides then reuse, which is the
	// merge working rather than a loss.
	if src.Empty && d.Stranded != "" {
		if priv := probe(d.Stranded); priv.Exists && priv.IsDir && !priv.Empty {
			d.Code, d.Reason = CodeWouldColdStart, "the host store at "+d.Source+
				" is empty while this jail's own copy is not, so aliasing it would hide a "+
				"warm cache behind an empty one and force a re-fetch"
			return d
		}
	}

	d.Aliased, d.Code = true, CodeAliased
	return d
}

// firstSegment is a store's top-level cache subdir — the granularity
// `cache_relocations` works at, which is why the relocation gate compares at
// this level rather than on the whole relative path.
func firstSegment(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// under reports whether child is base or a descendant of base.
func under(child, base string) bool {
	if child == "" || base == "" {
		return false
	}
	child, base = filepath.Clean(child), filepath.Clean(base)
	if child == base {
		return true
	}
	rel, err := filepath.Rel(base, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown platform"
	}
	return s
}

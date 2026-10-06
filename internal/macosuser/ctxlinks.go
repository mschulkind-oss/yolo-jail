package macosuser

import (
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ctxlinks.go is the macos-user DELIVERY of context mounts (docs/design/context-mounts.md §3,
// §4 steps 4 and 5). A config `mounts` element or a pack `mount` grant becomes a ROOT-OWNED
// SYMBOLIC LINK in the context dir (StagedCtxRoot, which $YOLO_CONTEXT_DIR names), at the path
// the container backends' /ctx would use, whose target is the resolved host source — and the
// Seatbelt profile decides every access to that target.
//
// ONE EXCEPTION, A COPY: a pack `mount` whose source is a single FILE is copied into the composed
// tree at the same path instead (SiteContextCopies; context-mounts.md's CX-D23), because a pack
// grant names a path in the user's home, which a link cannot serve on this backend, and one
// file is cheap to copy. A directory grant keeps the link and its refusal.
//
// THE LINK IS ONLY A NAME (§3.3). Seatbelt judges the TARGET of every access, for an absolute
// link and a relative one alike (measured on hardware 2026-09-13, declaration-parity.md §6.1
// probe 1), so an agent that plants its own link anywhere it may write gains nothing, and no
// rule here relies on the link for confinement: the link being root-owned keeps the NAME
// honest, and the profile's rules on the resolved source are the whole access decision.
//
// THREE GATES, each answering a different question, in the order a launch asks them:
//
//   - SITING (SiteContextLinks): may this source be delivered on this backend at all? Pure and
//     lexical over resolved paths. Every macOS fact it reads is a field of ContextSiting, so a
//     Linux test can state each one on its own.
//   - THE DAC PREFLIGHT (ContextPreflight): can the sandbox UID reach the source at the POSIX
//     layer? Asked of the kernel, as that uid, before the sandbox starts — never computed from
//     mode bits, because ACLs decide too (§3.5).
//   - THE PROFILE (the context blocks SeatbeltProfileWithContext renders): what the sandbox
//     may do with the source once it is delivered (§3.4).
//
// ⚠ NOTHING HERE HAS RUN ON A MAC. The rules, the argvs and the refusals are unit-tested on
// Linux; whether the kernel enforces them is what integration/macosuserseatbelt_test.go's
// context cases and integration/macosusercontextmounts_test.go ask on the scheduled
// macos-user.yml job.

// lnBin and testBin are the two system tools this delivery adds to the run path, pinned for
// the reason the others in macosuser.go are: the argv must not depend on the caller's PATH.
const (
	lnBin   = "/bin/ln"
	testBin = "/bin/test"
)

// ContextLink is one context mount this backend delivers by link.
type ContextLink struct {
	// Dest is the path the container backends bind this mount at: /ctx/<rel>. The link sits at
	// the same <rel> under the context dir, so a pack's text naming $YOLO_CONTEXT_DIR/<rel>
	// means one thing on every backend.
	Dest string
	// Source is the host path, ~-expanded and symlink-resolved ON THE HOST by the caller. The
	// profile names it as given, and a rule on an unresolved path matches nothing
	// (declaration-parity.md §6.1 probe 2: `/tmp` vs `/private/tmp`).
	Source string
	// Named is the source as the declaration spells it ("~/datasets/acme" for a pack grant),
	// for messages only. "" means Source.
	Named string
	// RW is true for a read-write config element. A pack grant is always read-only (CX-D3).
	RW bool
	// Dir is true for a directory source, false for a single file (a pack `mount` may name one).
	Dir bool
	// Pack names the pack whose `mount` grant this is; "" for a config `mounts` element.
	Pack string
}

// Rel is the link's path under the context dir, or "" when Dest is not under /ctx — which
// SiteContextLinks refuses, because a link can only be a name inside the context dir (§3.2).
func (l ContextLink) Rel() string {
	d := path.Clean(l.Dest)
	rel := strings.TrimPrefix(d, paths.ContainerContextDir+"/")
	if rel == d || rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

// Origin names the declaration a link came from, for messages.
func (l ContextLink) Origin() string {
	if l.Pack != "" {
		return "pack " + l.Pack + "'s `mount`"
	}
	return "`mounts`"
}

// Mode is "read-write" or "read-only".
func (l ContextLink) Mode() string {
	if l.RW {
		return "read-write"
	}
	return "read-only"
}

// NamedSource is the source as the declaration spells it.
func (l ContextLink) NamedSource() string {
	if l.Named != "" {
		return l.Named
	}
	return l.Source
}

// ContextRefusal is a context mount this backend does not deliver, and why. A launch with any
// refuses whole (DP-D15: a fatal error rather than a mount that is "surprisingly not there").
type ContextRefusal struct {
	Link   ContextLink
	Reason string
}

// ContextSiting is every macOS fact the siting rules read. A struct of facts, not syscalls,
// for homeLayout's reason: the check stays lexical (a dry run on Linux prints the macOS
// plan), and a Linux test can move any one of them. DarwinContextSiting is the layout a
// default macOS install has; contextsiting_darwin_test.go checks it against a running Mac.
type ContextSiting struct {
	// UsersRoot is the directory whose children are homes; UsersRootAliases are other paths
	// that ARE it (a firmlink, which symlink resolution does not see); FoldCase says the
	// volume compares names case-insensitively. HomeContaining's facts, the same ones.
	//
	// FoldCase also governs every overlap rule below: on a volume that folds case, two
	// spellings differing only in case are ONE directory, so a source spelled `/PRIVATE/TMP/x`
	// is inside the writable set whatever the profile names. Folding there can only refuse
	// more, never admit what a byte comparison refused, so the two ADMISSIONS (the shared root
	// for a read-write source, the boot volume under VolumesRoot) stay byte for byte.
	UsersRoot        string
	UsersRootAliases []string
	FoldCase         bool
	// DataVolume is the Data volume's own mount point, through which every firmlinked
	// directory (/private, /opt, /usr/local, /Users, …) has a second spelling that symlink
	// resolution keeps. A source spelled through it would be judged against none of the
	// spellings the rules and the profile name, so it is refused with the spelling to use.
	DataVolume string
	// SharedRoot is where a read-write source must sit: the one place a file the sandbox
	// creates inherits the group access that lets the host user change it afterwards
	// (SharedRootProvisionCommands).
	SharedRoot string
	// SandboxHome is the sandbox account's own home.
	SandboxHome string
	// StateDirs are every spelling of yolo's root-owned state dir, which holds the context dir.
	StateDirs []string
	// WritableRoots are the places the profile lets the sandbox write besides the workspace
	// and its own home — the profile's own list (profileWritableRoots), not a copy of it.
	WritableRoots []string
	// VolumesRoot is where other volumes mount; BootVolume is the one entry under it the
	// profile re-allows.
	VolumesRoot string
	BootVolume  string
	// PrivacyDirs are the home-relative directories macOS's privacy controls (TCC) guard, named
	// in a refusal so a reader knows a second wall stands behind the first.
	PrivacyDirs []string
}

// DarwinContextSiting is the layout of a default macOS install.
func DarwinContextSiting() ContextSiting {
	return ContextSiting{
		UsersRoot:        macOSHomes.usersRoot,
		UsersRootAliases: append([]string(nil), macOSHomes.aliases...),
		FoldCase:         macOSHomes.foldCase,
		DataVolume:       "/System/Volumes/Data",
		SharedRoot:       SharedRootDefault(),
		SandboxHome:      SandboxHome(),
		StateDirs:        []string{stateDir, "/private" + stateDir},
		WritableRoots:    append([]string(nil), profileWritableRoots...),
		VolumesRoot:      "/Volumes",
		BootVolume:       bootVolume,
		PrivacyDirs:      []string{"Desktop", "Documents", "Downloads", "Library/Mobile Documents"},
	}
}

// homes is the siting's home layout, the type HomeContaining asks.
func (s ContextSiting) homes() homeLayout {
	return homeLayout{usersRoot: s.UsersRoot, aliases: s.UsersRootAliases, foldCase: s.FoldCase}
}

// usersRoots is every spelling of the users root.
func (s ContextSiting) usersRoots() []string {
	return append([]string{s.UsersRoot}, s.UsersRootAliases...)
}

// SiteContextLinks returns every link in `links` this backend cannot deliver, with its reason;
// none means all of them can be. `workspace` is resolved; `occupied` are the context-dir paths
// (/ctx/...) yolo's own staging already uses, which no link may land at, inside or around.
//
// The rules, most fundamental first, so the reason a reader gets is the one to act on:
//
//  1. the jail path is under /ctx (§3.2: a link is only a name in the context dir);
//  2. the source is a resolved absolute path (the caller's contract, checked because the
//     profile names it verbatim);
//  3. it does not contain the users root (the read allow would re-open every home);
//  4. it is spelled under the users root as the profile spells it (a firmlink or case variant
//     is a spelling nobody has measured Seatbelt against), and not through the Data volume's
//     mount point at all (the same bytes under a second name the rules below never see);
//  5. it is not in the sandbox home, nor in any real home (OQ-CX7: v1 refuses home sources,
//     because the sandbox uid reaches nothing inside one and ancestorLiterals grants no
//     traversal there);
//  6. it does not overlap yolo's state dir, where the context dir itself lives (§2.3 clause 3);
//  7. it is not on another volume (CX-D5: unmeasured, so refused until measured);
//  8. a read-write source sits under the shared root (§3.5);
//  9. it does not overlap the workspace (§2.3 clause 2; §3.4 for a read-only one);
//  10. a read-only source does not overlap the rest of the writable set, nor a read-write
//     source (§3.4: with no mount namespace one path carries one mode);
//  11. no two links land at, inside or around one another, nor at one of `occupied` (a link
//     cannot hold another link, and staging one inside another would write into the source).
//
// Every overlap in rules 6 and 9-11 compares as the volume does (ContextSiting.FoldCase).
func SiteContextLinks(s ContextSiting, workspace string, links []ContextLink, occupied []string) []ContextRefusal {
	var out []ContextRefusal
	for i, l := range links {
		why := s.siteOne(workspace, l)
		if why == "" {
			why = siteAgainst(links, i, occupied, s.FoldCase)
		}
		if why != "" {
			out = append(out, ContextRefusal{Link: l, Reason: why})
		}
	}
	return out
}

// siteOne is rules 1-10 for one link, against the facts alone.
func (s ContextSiting) siteOne(workspace string, l ContextLink) string {
	src := l.Source
	if l.Rel() == "" {
		return "its jail path " + l.Dest + " is not under " + paths.ContainerContextDir +
			": with no mount namespace a context mount here is only a name inside $" +
			paths.ContextDirEnv + ", and yolo will not move it to a path you did not choose"
	}
	if !path.IsAbs(src) || path.Clean(src) != src {
		return "its source " + src + " is not a resolved absolute path"
	}
	for _, root := range s.usersRoots() {
		if pathWithin(root, src, s.FoldCase) {
			return "its source " + src + " contains " + root + ", and with it every home on this Mac"
		}
	}
	if !pathWithin(src, s.UsersRoot, false) {
		for _, root := range s.usersRoots() {
			if pathWithin(src, root, s.FoldCase) {
				return "its source " + src + " is spelled through another name for " + s.UsersRoot +
					"; spell it under " + s.UsersRoot + ", the spelling the sandbox profile names"
			}
		}
	}
	if s.DataVolume != "" && pathWithin(src, s.DataVolume, s.FoldCase) {
		plain := src
		if n := len(s.DataVolume); len(src) >= n && strings.EqualFold(src[:n], s.DataVolume) {
			plain = "/" + strings.TrimLeft(src[n:], "/")
		}
		return "its source " + src + " is spelled through " + s.DataVolume + ", the Data volume's " +
			"own mount point, so the rules that keep one path to one mode would not recognise it; " +
			"spell it as " + plain + ", the spelling the sandbox profile names"
	}
	if s.SandboxHome != "" && pathWithin(src, s.SandboxHome, s.FoldCase) {
		return "its source " + src + " is inside the sandbox account's own home " + s.SandboxHome
	}
	if home, in := s.homes().containing(src); in {
		msg := "its source " + src + " is inside the home folder " + home
		if dir := s.privacyDir(src, home); dir != "" {
			msg += ", in " + dir + ", which macOS's privacy controls guard as well"
		}
		return msg + ": the sandbox account cannot reach into a real home, so this backend " +
			"delivers only sources outside every home"
	}
	for _, sd := range s.StateDirs {
		if rel := overlap(src, sd, s.FoldCase); rel != "" {
			return "its source " + src + " " + rel + " yolo's state directory " + sd +
				", where the context dir itself lives"
		}
	}
	if s.VolumesRoot != "" && pathWithin(src, s.VolumesRoot, s.FoldCase) &&
		(s.BootVolume == "" || !pathWithin(src, s.BootVolume, false)) {
		return "its source " + src + " is on another volume (under " + s.VolumesRoot + "): " +
			"whether the sandbox account gets through a removable or network volume's ownership " +
			"and privacy protections has not been measured, so this backend refuses one until it is"
	}
	if l.RW && (s.SharedRoot == "" || src == s.SharedRoot || !pathWithin(src, s.SharedRoot, false)) {
		return "a read-write source must sit under " + s.SharedRoot + ", and " + src +
			" does not: only there does a file the sandbox creates inherit the access that lets " +
			"you change it afterwards"
	}
	if rel := overlap(src, workspace, s.FoldCase); rel != "" {
		return "its source " + src + " " + rel + " the workspace " + workspace +
			oneModeClause
	}
	if !l.RW {
		for _, w := range s.WritableRoots {
			if rel := overlap(src, w, s.FoldCase); rel != "" {
				return "its source " + src + " " + rel + " " + w + ", which the sandbox may write" +
					oneModeClause
			}
		}
	}
	return ""
}

// oneModeClause is §3.4's reason, shared by every overlap refusal.
const oneModeClause = ": with no mount namespace one path carries one mode here, so the mount " +
	"would either be writable or make part of what the sandbox writes read-only"

// siteAgainst is rule 10's cross-link half and rule 11, for links[i] against the others,
// comparing as the volume does (fold): the context dir is on the same volume as the sources.
//
// A pair is reported ONCE, on its later member, so two colliding entries read as one refusal
// naming both rather than two that each name the other.
func siteAgainst(links []ContextLink, i int, occupied []string, fold bool) string {
	l := links[i]
	dest := path.Clean(l.Dest)
	for j, o := range links {
		if j == i || o.Rel() == "" {
			continue
		}
		if !l.RW && o.RW {
			if rel := overlap(l.Source, o.Source, fold); rel != "" {
				return "its read-only source " + l.Source + " " + rel + " the read-write source " +
					o.Source + " (" + o.Origin() + ")" + oneModeClause
			}
		}
		if j < i {
			if rel := overlap(dest, path.Clean(o.Dest), fold); rel != "" {
				return "its jail path " + dest + " " + rel + " " + path.Clean(o.Dest) + " (" +
					o.Origin() + " of " + o.NamedSource() + "): a link cannot hold another link, " +
					"so two context mounts here cannot share or nest a path"
			}
		}
	}
	for _, p := range occupied {
		if rel := overlap(dest, path.Clean(p), fold); rel != "" {
			return "its jail path " + dest + " " + rel + " " + path.Clean(p) +
				", which yolo's own staging uses"
		}
	}
	return ""
}

// SiteContextCopies returns every COPY in `copies` this backend cannot land in the context dir,
// with its reason; none means all of them can land.
//
// A copy is a selected pack's single-FILE `mount`, which this backend delivers by copying the
// file into the composed tree at the grant's /ctx path instead of linking it: a pack grant names
// a path in the user's home, which the sandbox account cannot reach through a link (OQ-CX7), and
// the host CLI, running as the user, can read it. Each is described by the ContextLink fields a
// copy has (Dest, Source as the host resolved it, Named, Pack; never RW, never Dir).
//
// DESTINATION RULES ONLY, because the source is never opened by the sandbox: the bytes land in
// the root-owned tree, so where the source sits, which rules 2-10 of SiteContextLinks judge, is
// not this backend's question for a copy. What is judged is where the copy lands:
//
//  1. under /ctx, as a link must be;
//  2. not at, inside or around one of `occupied` (yolo's reserved children of the context dir
//     and the selected packs' declared `reads-host` destinations — ContextOccupied's list);
//  3. not at, inside or around another copy (reported once, on the later member);
//  4. not at, inside or around a link in `links`, which would be staged into the copy's
//     directory or replace it.
//
// The caller adds each copy's Dest to the `occupied` it hands SiteContextLinks too, so a link
// is refused around a copy from its own side as well, and PlanInvariants' re-siting (which
// reads HostContext.Copied through ContextOccupied) sees the copies without this function.
// Every overlap compares as the volume does (ContextSiting.FoldCase).
func SiteContextCopies(s ContextSiting, copies, links []ContextLink, occupied []string) []ContextRefusal {
	var out []ContextRefusal
	for i, c := range copies {
		why := ""
		dest := path.Clean(c.Dest)
		if c.Rel() == "" {
			why = "its jail path " + c.Dest + " is not under " + paths.ContainerContextDir +
				": with no mount namespace a copied file here can only land inside $" +
				paths.ContextDirEnv
		}
		for _, p := range occupied {
			if why != "" {
				break
			}
			if rel := overlap(dest, path.Clean(p), s.FoldCase); rel != "" {
				why = "its jail path " + dest + " " + rel + " " + path.Clean(p) +
					", which yolo's own staging uses"
			}
		}
		for _, o := range copies[:i] {
			if why != "" {
				break
			}
			if o.Rel() == "" {
				continue
			}
			if rel := overlap(dest, path.Clean(o.Dest), s.FoldCase); rel != "" {
				why = "its jail path " + dest + " " + rel + " " + path.Clean(o.Dest) + " (" +
					o.Origin() + " of " + o.NamedSource() + "): two copied files cannot share or " +
					"nest a path"
			}
		}
		for _, l := range links {
			if why != "" {
				break
			}
			if l.Rel() == "" {
				continue
			}
			if rel := overlap(dest, path.Clean(l.Dest), s.FoldCase); rel != "" {
				why = "its jail path " + dest + " " + rel + " " + path.Clean(l.Dest) + " (" +
					l.Origin() + " of " + l.NamedSource() + "), where a link is staged: a copied " +
					"file and a link cannot share or nest a path"
			}
		}
		if why != "" {
			out = append(out, ContextRefusal{Link: c, Reason: why})
		}
	}
	return out
}

// --- cache_relocations (docs/plans/cache-relocation.md) ------------------------------------
//
// A USER-SCOPE `cache_relocations` entry is delivered here as the context mounts are, by the
// same three gates and for the same reasons: there is no bind to make, so the sandbox account
// home's ~/.cache/<subdir> becomes a LINK the bootstrap lays to the resolved target
// (entrypoint's DarwinCacheRelocationsEnv), and the Seatbelt profile opens the target, read and
// write, because Seatbelt judges a link's target (declaration-parity.md §6.1 probe 1).
//
// ⚠ ~/.cache ONLY. A darwin tool that keeps its cache under ~/Library/Caches — Go's
// os.UserCacheDir, and so the Go build cache; pip and playwright very likely — is not moved by
// a relocation on this backend, whatever subdir name the entry uses. huggingface_hub honors
// ~/.cache/huggingface on macOS too, so the motivating case is covered.
//
// THE DIFFERENCES FROM A CONTEXT MOUNT, each a decision (cache-relocation.md's ledger):
//
//   - The link is in the SANDBOX HOME, at a path the key itself fixes (~/.cache/<subdir>), not
//     a name in the root-owned context dir. The link is still only a name: an agent can replace
//     it with one of its own, which opens nothing, because the profile names the target.
//   - Every relocation is READ-WRITE, and the target need NOT sit under the shared root. A
//     cache is the sandbox's own bytes, so the shared root's rule for a context source — that a
//     file the sandbox makes there stays changeable by you — is met another way: the host CLI
//     grants the target the shared root's inheriting entries when it creates it, and the DAC
//     preflight and the write probe refuse a target the sandbox cannot use.
//   - A target under /Volumes is ADMITTED, narrowing CX-D5 for this key alone: a relocation
//     exists to put a cache on another disk, and DP-D15's "a fatal error rather than surprisingly
//     not there" is met by the launch-time probe, which refuses a volume the sandbox cannot write
//     before the agent starts, instead of by refusing every volume unmeasured.

// CacheRelocation is one user-scope `cache_relocations` entry this backend delivers.
type CacheRelocation struct {
	// Subdir is the entry's key: one path segment under ~/.cache.
	Subdir string
	// Target is the host directory, ~-expanded and symlink-resolved ON THE HOST by the caller.
	// The profile names it as given, and a rule on an unresolved path matches nothing.
	Target string
	// Named is the target as the user config spells it, for messages only. "" means Target.
	Named string
	// Created is true when the host CLI made the target on this launch, and so granted it the
	// sandbox's inheriting access entries (CacheRelocationACECommands). A target that was
	// already there keeps whatever access it had, which the preflight then asks about.
	Created bool
}

// NamedTarget is the target as the user config spells it.
func (r CacheRelocation) NamedTarget() string {
	if r.Named != "" {
		return r.Named
	}
	return r.Target
}

// LinkPath is where the bootstrap lays this relocation's link in the account home.
func (r CacheRelocation) LinkPath(home string) string {
	if home == "" {
		home = SandboxHome()
	}
	return home + "/.cache/" + r.Subdir
}

// CacheRelocationRefusal is a relocation this backend does not deliver, and why. A launch with
// any refuses whole: the cache would otherwise sit back on the disk the user moved it off.
type CacheRelocationRefusal struct {
	Relocation CacheRelocation
	Reason     string
}

// SiteCacheRelocations returns every relocation in `relocs` this backend cannot deliver, with
// its reason; none means all can be. `workspace` is resolved, and `links` are the launch's
// delivered context mounts, whose sources no target may overlap.
//
// The rules, most fundamental first:
//
//  1. the key is one path segment (the validator's rule, re-checked because it names a link);
//  2. the target is a resolved absolute path;
//  3. it does not contain the users root (the read allow would re-open every home);
//  4. it is spelled under the users root as the profile spells it, and not through the Data
//     volume's mount point;
//  5. it is not in the sandbox home (that is where the link itself lives), nor in any real
//     home (the sandbox account reaches nothing inside one, OQ-CX7's v1 rule);
//  6. it does not overlap yolo's state dir;
//  7. it is not /Volumes itself, nor anything containing it (the read allow would re-open every
//     other volume). One volume, or a folder on one, is admitted (CX-D5 narrowed for this key);
//  8. it does not overlap the workspace, nor any context mount's source: with no mount
//     namespace one path carries one mode, and a read-only source's deny would win over the
//     relocation's write allow, so part of the cache would be silently read-only.
//
// What it does NOT ask is where on neutral ground the target sits: unlike a read-write context
// source it need not be under the shared root (the type's header says why), and a target in the
// sandbox's writable places (/tmp, /var/folders) is simply already writable.
//
// Every overlap compares as the volume does (ContextSiting.FoldCase).
func SiteCacheRelocations(s ContextSiting, workspace string, relocs []CacheRelocation, links []ContextLink) []CacheRelocationRefusal {
	var out []CacheRelocationRefusal
	for _, r := range relocs {
		if why := s.siteRelocation(workspace, r, links); why != "" {
			out = append(out, CacheRelocationRefusal{Relocation: r, Reason: why})
		}
	}
	return out
}

// siteRelocation is rules 1-8 for one relocation.
func (s ContextSiting) siteRelocation(workspace string, r CacheRelocation, links []ContextLink) string {
	if r.Subdir == "" || r.Subdir == "." || r.Subdir == ".." || strings.Contains(r.Subdir, "/") {
		return "its key " + r.Subdir + " is not one path segment under ~/.cache"
	}
	t := r.Target
	if !path.IsAbs(t) || path.Clean(t) != t {
		return "its target " + t + " is not a resolved absolute path"
	}
	for _, root := range s.usersRoots() {
		if pathWithin(root, t, s.FoldCase) {
			return "its target " + t + " contains " + root + ", and with it every home on this Mac"
		}
	}
	if !pathWithin(t, s.UsersRoot, false) {
		for _, root := range s.usersRoots() {
			if pathWithin(t, root, s.FoldCase) {
				return "its target " + t + " is spelled through another name for " + s.UsersRoot +
					"; spell it under " + s.UsersRoot + ", the spelling the sandbox profile names"
			}
		}
	}
	if s.DataVolume != "" && pathWithin(t, s.DataVolume, s.FoldCase) {
		plain := t
		if n := len(s.DataVolume); len(t) >= n && strings.EqualFold(t[:n], s.DataVolume) {
			plain = "/" + strings.TrimLeft(t[n:], "/")
		}
		return "its target " + t + " is spelled through " + s.DataVolume + ", the Data volume's " +
			"own mount point; spell it as " + plain + ", the spelling the sandbox profile names"
	}
	if s.SandboxHome != "" && pathWithin(t, s.SandboxHome, s.FoldCase) {
		return "its target " + t + " is inside the sandbox account's own home " + s.SandboxHome +
			", where the relocation's link itself lives"
	}
	if home, in := s.homes().containing(t); in {
		msg := "its target " + t + " is inside the home folder " + home
		if dir := s.privacyDir(t, home); dir != "" {
			msg += ", in " + dir + ", which macOS's privacy controls guard as well"
		}
		return msg + ": the sandbox account cannot reach into a real home"
	}
	for _, sd := range s.StateDirs {
		if rel := overlap(t, sd, s.FoldCase); rel != "" {
			return "its target " + t + " " + rel + " yolo's state directory " + sd
		}
	}
	if s.VolumesRoot != "" && pathWithin(s.VolumesRoot, t, s.FoldCase) {
		return "its target " + t + " is or contains " + s.VolumesRoot + ", and with it every " +
			"other volume on this Mac; name one volume, or a folder on one"
	}
	if rel := overlap(t, workspace, s.FoldCase); rel != "" {
		return "its target " + t + " " + rel + " the workspace " + workspace + oneModeClause
	}
	for _, l := range links {
		if rel := overlap(t, l.Source, s.FoldCase); rel != "" {
			return "its target " + t + " " + rel + " the " + l.Mode() + " source " + l.Source +
				" (" + l.Origin() + ")" + oneModeClause
		}
	}
	return ""
}

// CacheRelocationACECommands are the two inheriting access entries the host CLI grants a target
// it created on this launch, run AS THE INVOKING USER (the owner needs no sudo to change its own
// directory's ACL): the shared root's own pair (SharedRootProvisionCommands), so what the sandbox
// creates there inherits the access that lets you read and delete it afterwards, as under the
// shared root. A target that was already there is not touched; the preflight asks about it.
func CacheRelocationACECommands(target string) [][]string {
	aces := WorkspaceACLAces(SandboxGroup)
	return [][]string{
		{chmodBin, "+a", aces["dir"], target},
		{chmodBin, "+a", aces["file_inherit"], target},
	}
}

// CacheRelocationProbe is one question the launch asks about a relocation's target before the
// agent starts: the DAC preflight's read, search and write, each as the sandbox account
// (CacheRelocationPreflight), and the write-and-remove under the session's own Seatbelt profile
// (CacheRelocationWriteProbe).
type CacheRelocationProbe struct {
	Relocation CacheRelocation
	// Access is "read", "search", "write", or "write under the sandbox profile".
	Access string
	// Argv is the whole command, `sudo` included; it exits 0 when the access is granted.
	Argv []string
}

// cacheRelocationProfileAccess is the write probe's Access.
const cacheRelocationProfileAccess = "write under the sandbox profile"

// CacheRelocationPreflight is the DAC preflight for every relocation: read, search and write on
// the target, each asked of access(2) as `user` (SandboxUser when ""), which weighs the ACLs as
// well as the mode bits. ContextPreflight's shape for a read-write directory source.
func CacheRelocationPreflight(relocs []CacheRelocation, user string) []CacheRelocationProbe {
	if user == "" {
		user = SandboxUser
	}
	var out []CacheRelocationProbe
	for _, r := range relocs {
		for _, a := range [][2]string{{"read", "-r"}, {"search", "-x"}, {"write", "-w"}} {
			out = append(out, CacheRelocationProbe{Relocation: r, Access: a[0],
				Argv: []string{"sudo", "--user=" + user, testBin, a[1], r.Target}})
		}
	}
	return out
}

// CacheRelocationProbeFile is the file the write probe creates and removes in a target, named by
// the session so two sessions never race on one.
func CacheRelocationProbeFile(target, sessionID string) string {
	if sessionID == "" {
		sessionID = SessionPlaceholder
	}
	return target + "/.yolo-relocation-probe-" + sessionID
}

// CacheRelocationWriteProbe is one REAL write: as the sandbox account, under the session's
// Seatbelt profile, create the probe file in the target and remove it. It is the question the
// DAC preflight cannot ask — whether the profile's allow for the target, and the volume under it
// (ownership, a removable or network volume's own rules), let the write through — asked before
// the agent rather than by the agent's first cache write. The file is handed to the shell as
// "$1", so no path is ever spliced into the script.
func CacheRelocationWriteProbe(r CacheRelocation, profilePath, sessionID, user string) CacheRelocationProbe {
	if user == "" {
		user = SandboxUser
	}
	home := SandboxHome()
	return CacheRelocationProbe{Relocation: r, Access: cacheRelocationProfileAccess, Argv: []string{
		"sudo", "--user=" + user, "/usr/bin/env", "-i", "HOME=" + home, "USER=" + user,
		"PATH=/usr/bin:/bin", "/usr/bin/sandbox-exec", "-f", profilePath, "--",
		"/bin/sh", "-c", `: > "$1" && exec ` + rmBin + ` -f "$1"`, "sh",
		CacheRelocationProbeFile(r.Target, sessionID),
	}}
}

// CacheRelocationRefusalMessage is the launch's message for the relocation probes that failed,
// one line each, with the next steps: grant the sandbox account access to a target that was
// already there, move the target somewhere it can be used, or a container runtime. step is
// containerStep's clause, as ContextPreflightRefusal takes it.
func CacheRelocationRefusalMessage(failed []CacheRelocationProbe, step string) string {
	msg := "[bold red]Refusing the macos-user launch: the sandbox account cannot use every " +
		"cache_relocations target.[/bold red]\n"
	seen := map[string]bool{}
	var targets []string
	for _, p := range failed {
		what := p.Access + " it"
		if p.Access == cacheRelocationProfileAccess {
			what = "create a file in it under the session's sandbox profile"
		}
		msg += "  • ~/.cache/" + p.Relocation.Subdir + " → " + p.Relocation.Target + ": " +
			SandboxUser + " cannot " + what + "\n"
		if !seen[p.Relocation.Target] {
			seen[p.Relocation.Target] = true
			targets = append(targets, p.Relocation.Target)
		}
	}
	msg += "The sandbox runs as " + SandboxUser + ", a separate account, and a relocated cache " +
		"is its own bytes in your folder. A folder yolo creates for a relocation is granted the " +
		"sandbox's access as it is created; one that was already there, and holds files, needs " +
		"the same entries added:\n"
	for _, t := range targets {
		msg += "  yolo macos-fix-permissions " + shquote.Quote(t) + "\n"
	}
	return msg + "Every folder above a target must also let the sandbox account pass (`ls -lde` " +
		"each one; mode 755 is enough), so a target inside a folder only you may open, such as " +
		"your own temporary directory, needs another place. " +
		"If a target is on a disk the sandbox account cannot write at all, point the entry at a " +
		"folder on another disk. " +
		"Or remove the entry from ~/.config/yolo-jail/config.jsonc, or use `runtime: \"podman\"`" +
		step + ", which mounts it instead."
}

// cacheRelocationProblems is PlanInvariants' half for the relocations a plan delivers. Each must
// be deliverable by the siting rules (the run pipeline sites them first; this is what fails if a
// caller hands the plan builder one it did not), OPENED by the profile — its read allow, after
// the /Volumes and /Users read denies it must re-open, and its write allow, each read out of the
// profile text — ASKED of the kernel by the DAC preflight and by a write under the profile, and
// LINKED by the bootstrap, which is told the target under the key.
func cacheRelocationProblems(plan RunPlan) []string {
	if len(plan.CacheRelocations) == 0 {
		return nil
	}
	var problems []string
	for _, r := range SiteCacheRelocations(DarwinContextSiting(), plan.Workspace, plan.CacheRelocations, plan.ContextLinks) {
		problems = append(problems, "the cache relocation ~/.cache/"+r.Relocation.Subdir+
			" cannot be delivered on this backend: "+r.Reason)
	}
	read := profileBlock(plan.Seatbelt, "cache-relocation-read-allow")
	write := profileBlock(plan.Seatbelt, "cache-relocation-write-allow")
	readAt := strings.Index(plan.Seatbelt, ";; #seatbelt-test-id:cache-relocation-read-allow#")
	for _, id := range []string{"volumes-read-deny", "users-read-deny"} {
		if at := strings.Index(plan.Seatbelt, ";; #seatbelt-test-id:"+id+"#"); readAt >= 0 && at > readAt {
			problems = append(problems, "the cache relocations' read allow comes before the "+id+
				" rule it must re-open; last match wins, so a target there would stay unreadable")
		}
	}
	links := map[string]string{}
	if wire, ok := argvEnvValue(plan.BootstrapArgv, entrypoint.DarwinCacheRelocationsEnv); ok {
		links, _ = entrypoint.ParseDarwinCacheRelocations(wire)
	}
	asked := map[string]bool{}
	for _, p := range append(append([]CacheRelocationProbe(nil), plan.CacheRelocationPreflight...),
		plan.CacheRelocationProbes...) {
		asked[strings.Join(p.Argv, "\x00")] = true
	}
	for _, r := range plan.CacheRelocations {
		name := "the cache relocation ~/.cache/" + r.Subdir + " (" + r.Target + ")"
		clause := "(subpath " + sbplStr(r.Target) + ")"
		if !strings.Contains(read, clause) {
			problems = append(problems, name+" has no read allow in the Seatbelt profile; the "+
				"agent would see the link and be refused everything behind it")
		}
		if !strings.Contains(write, clause) {
			problems = append(problems, name+" has no write allow in the Seatbelt profile; "+
				"every cache write through the link would be refused")
		}
		if links[r.Subdir] != r.Target {
			problems = append(problems, name+" is not named to the bootstrap ("+
				entrypoint.DarwinCacheRelocationsEnv+"); no link would be laid, and the cache "+
				"would stay in the sandbox home")
		}
		for _, p := range CacheRelocationPreflight([]CacheRelocation{r}, "") {
			if !asked[strings.Join(p.Argv, "\x00")] {
				problems = append(problems, name+" is not checked by the DAC preflight ("+
					strings.Join(p.Argv, " ")+"); a target "+SandboxUser+" cannot "+p.Access+
					" would fail only at the agent's first cache write")
			}
		}
		if p := CacheRelocationWriteProbe(r, plan.ProfilePath, plan.SessionID, ""); !asked[strings.Join(p.Argv, "\x00")] {
			problems = append(problems, name+" is not probed by a write under the session's "+
				"Seatbelt profile; a volume the sandbox cannot write would fail only at the "+
				"agent's first cache write")
		}
	}
	return problems
}

// privacyDir names the privacy-guarded directory src sits in, inside home, or "".
func (s ContextSiting) privacyDir(src, home string) string {
	for _, d := range s.PrivacyDirs {
		if p := home + "/" + d; pathWithin(src, p, s.FoldCase) {
			return p
		}
	}
	return ""
}

// copiedDests is each copy's /ctx destination, the paths a copy occupies in the context dir.
func copiedDests(copies []ContextLink) []string {
	out := make([]string, 0, len(copies))
	for _, c := range copies {
		out = append(out, c.Dest)
	}
	return out
}

// ContextOccupied is every context-dir path yolo's own staging uses on this launch: the
// reserved children of the context dir (paths.ReservedContextPaths, the names `yolo check`
// refuses a `mounts` element at) and every destination the composed tree delivers a host
// file to (HostContext.Delivered, and the copied pack `mount` files, HostContext.Copied). A
// link at, inside or around one would collide with a copied file, or be staged INTO a
// directory of the composed tree.
func ContextOccupied(delivered []string) []string {
	var out []string
	for _, r := range paths.ReservedContextPaths() {
		out = append(out, r.Path)
	}
	out = append(out, delivered...)
	return out
}

// ContextProbe is one question the DAC preflight asks the kernel, as the sandbox account,
// before the sandbox starts.
type ContextProbe struct {
	Link ContextLink
	// Access is "read", "search" or "write".
	Access string
	// Argv is the whole command, `sudo` included: `/bin/test` exits 0 when the access is
	// granted, and it asks access(2), which weighs the ACLs as well as the mode bits.
	Argv []string
}

// ContextPreflight is the DAC preflight (§3.5): read for every source, search for a
// directory, and write for a read-write one, each asked as `user` (SandboxUser when "").
//
// THE ROOT OF THE SOURCE ONLY, not the subtree: a 0700 subdirectory fails when the agent opens
// it, which the briefing states (§3.5's stated delta). Asking access(2) of the source also asks
// it of every ancestor, since the lookup needs search on each, so a home that is not
// traversable fails here too.
func ContextPreflight(links []ContextLink, user string) []ContextProbe {
	if user == "" {
		user = SandboxUser
	}
	var out []ContextProbe
	add := func(l ContextLink, access, flag string) {
		out = append(out, ContextProbe{Link: l, Access: access,
			Argv: []string{"sudo", "--user=" + user, testBin, flag, l.Source}})
	}
	for _, l := range links {
		add(l, "read", "-r")
		if l.Dir {
			add(l, "search", "-x")
		}
		if l.RW {
			add(l, "write", "-w")
		}
	}
	return out
}

// ContextPreflightRefusal is the launch's message for the probes that failed, one line each.
// step follows its container-runtime step: containerStep's clause, which names the jail notch
// beside a guest (env-manager plan EMP-D5) and is "" at the jail notch.
func ContextPreflightRefusal(failed []ContextProbe, step string) string {
	msg := "[bold red]Refusing the macos-user launch: the sandbox account cannot reach " +
		"every context mount.[/bold red]\n"
	for _, p := range failed {
		msg += "  • " + p.Link.Origin() + ": " + SandboxUser + " cannot " + p.Access + " " +
			p.Link.Source + "\n"
	}
	return msg + "The sandbox runs as " + SandboxUser + ", a separate account, and the file " +
		"permissions or an ACL refuse it before the\nSeatbelt profile is consulted. Copy the " +
		"folder under " + SharedRootDefault() + " (cp -R, or a fresh clone there): what is\n" +
		"created there is shared with the sandbox account as it is created, and a folder moved " +
		"there is not.\nOr use a container runtime (`runtime: \"podman\"` or `\"container\"`)" + step + "."
}

// StageContextDirCommands stages the context dir for this launch: the composed tree when the
// host CLI composed one (StageCtxCommands), an empty directory otherwise
// (StageEmptyCtxCommands), and then, inside the same `.new` directory and before the swap, one
// root-owned link per delivered context mount.
//
// THE LINKS COME AFTER THE RECURSIVE chmod, and that order is the point: a `chmod -R` over a
// tree that already held a link to a host source could follow it into the user's own files
// (BSD chmod's default is not to, and nothing here should depend on that). Each parent
// directory a link needs is made and opened to the sandbox explicitly, because root's umask
// under sudo is the invoking user's, which need not be 022.
//
// ⚠ `ln -s` INTO AN EXISTING DIRECTORY CREATES THE LINK INSIDE IT (BSD ln has no -T), which is
// one of the reasons SiteContextLinks refuses a link at, inside or around another link or a path
// the composed tree uses: none of these commands ever meets a directory at a link's own path.
func StageContextDirCommands(hostCtxTree string, links []ContextLink, cname, sd string) [][]string {
	var base [][]string
	if hostCtxTree != "" {
		base = StageCtxCommands(hostCtxTree, cname, sd)
	} else {
		base = StageEmptyCtxCommands(cname, sd)
	}
	if len(links) == 0 {
		return base
	}
	tmp := StagedCtxRoot(cname, sd) + ".new"
	// Both stagers end with `rm -rf <dst>` then `mv -f <tmp> <dst>`; the links go in front.
	head, swap := base[:len(base)-2], base[len(base)-2:]
	out := append(append([][]string{}, head...), contextLinkCommands(tmp, links)...)
	return append(out, swap...)
}

// contextLinkCommands makes each link's parents (top-down, each opened a+rx) and then the links.
func contextLinkCommands(tmp string, links []ContextLink) [][]string {
	parents := map[string]bool{}
	for _, l := range links {
		rel := l.Rel()
		if rel == "" {
			continue
		}
		for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
			parents[dir] = true
		}
	}
	dirs := make([]string, 0, len(parents))
	for d := range parents {
		dirs = append(dirs, d)
	}
	// Top-down: a parent sorts before its children because it is a prefix of them.
	sort.Strings(dirs)
	var out [][]string
	for _, d := range dirs {
		out = append(out, []string{mkdirBin, "-p", tmp + "/" + d}, []string{chmodBin, "755", tmp + "/" + d})
	}
	for _, l := range links {
		if rel := l.Rel(); rel != "" {
			out = append(out, []string{lnBin, "-s", l.Source, tmp + "/" + rel})
		}
	}
	return out
}

// stagesContextLink reports whether the stage commands make l's link at its path under the
// context dir — inside the `.new` directory the final `mv` swaps in, which is where
// StageContextDirCommands writes it.
func stagesContextLink(cmds [][]string, contextDir string, l ContextLink) bool {
	want := contextDir + ".new/" + l.Rel()
	for _, c := range cmds {
		if len(c) == 4 && c[0] == lnBin && c[1] == "-s" && c[2] == l.Source && c[3] == want {
			return true
		}
	}
	return false
}

// contextLinkProblems is PlanInvariants' half for the context mounts a plan delivers. Every
// link must be deliverable by the siting rules (the run pipeline sites them first; this is what
// fails if a caller hands the plan builder one it did not), STAGED at its path in the context
// dir, OPENED by the profile — the read allow, and the write allow or the write deny its mode
// needs, each read out of the profile text rather than re-rendered — and ASKED of the kernel by
// the DAC preflight before the sandbox starts.
//
// THE PROFILE HALF IS THE ONE THAT KEEPS THE LINK A NAME (§3.3): a link with no rule naming its
// source is a link the agent can see and the sandbox cannot open, and the only way such a plan
// could "work" is a rule broad enough to open the source by accident.
func contextLinkProblems(plan RunPlan) []string {
	if len(plan.ContextLinks) == 0 {
		return nil
	}
	var problems []string
	for _, r := range SiteContextLinks(DarwinContextSiting(), plan.Workspace, plan.ContextLinks, plan.ContextOccupied) {
		problems = append(problems, "the context mount at "+r.Link.Dest+" ("+r.Link.Origin()+
			") cannot be delivered on this backend: "+r.Reason)
	}
	read := profileBlock(plan.Seatbelt, "context-read-allow")
	write := profileBlock(plan.Seatbelt, "context-write-allow")
	deny := profileBlock(plan.Seatbelt, "context-readonly-deny")
	asked := map[string]bool{}
	for _, p := range plan.ContextPreflight {
		asked[strings.Join(p.Argv, "\x00")] = true
	}
	for _, l := range plan.ContextLinks {
		name := "the context mount at " + l.Dest + " (" + l.Source + ")"
		clause := "(subpath " + sbplStr(l.Source) + ")"
		if !strings.Contains(read, clause) {
			problems = append(problems, name+" has no read allow in the Seatbelt profile; the "+
				"agent would see the link and be refused everything behind it")
		}
		if l.RW && !strings.Contains(write, clause) {
			problems = append(problems, name+" is read-write and the Seatbelt profile allows "+
				"no write to its source")
		}
		if !l.RW && !strings.Contains(deny, clause) {
			problems = append(problems, name+" is read-only and the Seatbelt profile does not "+
				"deny writes to its source")
		}
		if plan.ContextDir == "" || !stagesContextLink(plan.StageCommands, plan.ContextDir, l) {
			problems = append(problems, name+" is not staged as a link in the context dir; "+
				"$"+paths.ContextDirEnv+"/"+l.Rel()+" would not exist")
		}
		need := []string{"-r"}
		if l.Dir {
			need = append(need, "-x")
		}
		if l.RW {
			need = append(need, "-w")
		}
		for _, flag := range need {
			argv := []string{"sudo", "--user=" + SandboxUser, testBin, flag, l.Source}
			if !asked[strings.Join(argv, "\x00")] {
				problems = append(problems, name+" is not checked by the DAC preflight ("+
					strings.Join(argv, " ")+"); a source "+SandboxUser+" cannot reach would "+
					"fail only when the agent opens it")
			}
		}
	}
	return problems
}

// --- the profile's context blocks (rendered by SeatbeltProfileWithContext) -----------------

// contextSources is the de-duplicated sources of the links that pass keep, in order.
func contextSources(links []ContextLink, keep func(ContextLink) bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range links {
		if !keep(l) || seen[l.Source] || !strings.HasPrefix(l.Source, "/") {
			continue
		}
		seen[l.Source] = true
		out = append(out, l.Source)
	}
	return out
}

// subpathClauses renders one `(subpath "…")` line per source, without the trailing newline.
func subpathClauses(sources []string) string {
	var b strings.Builder
	for _, s := range sources {
		b.WriteString("    (subpath " + sbplStr(s) + ")\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// contextWriteAllow re-allows writes under every read-write source. It sits right after the
// writable-set allow and BEFORE every write deny that must win (workspace_readonly, the staged
// home content, the read-only sources), §3.4's step 3.
func contextWriteAllow(links []ContextLink) string {
	src := contextSources(links, func(l ContextLink) bool { return l.RW })
	if len(src) == 0 {
		return ""
	}
	return "\n" +
		";; --- Context mounts, read-write (docs/design/context-mounts.md §3.4): the resolved\n" +
		";;     source, under the shared root.  Before every write deny that must win. ---\n" +
		";; #seatbelt-test-id:context-write-allow#\n" +
		"(allow file-write*\n" + subpathClauses(src) + ")\n"
}

// contextReadonlyDeny re-denies writes under every read-only source, after every write allow.
// Belt and braces for any source the siting admits — none of them is in the writable set, so
// the root write deny already refuses the write — and the rule that keeps "read-only" true if
// a later edit ever re-allows writes there.
func contextReadonlyDeny(links []ContextLink) string {
	src := contextSources(links, func(l ContextLink) bool { return !l.RW })
	if len(src) == 0 {
		return ""
	}
	return "\n" +
		";; --- Context mounts, read-only: never writable, whatever is allowed above.\n" +
		";;     After every write allow — last match wins. ---\n" +
		";; #seatbelt-test-id:context-readonly-deny#\n" +
		"(deny file-write*\n" + subpathClauses(src) + ")\n"
}

// contextReadAllow re-allows reads under every delivered source. After the /Users read deny it
// re-opens, and BEFORE the keychain denies, so a source that holds a keychain (a mount of
// /Library) still cannot read it: §3.4's step 2, ahead of step 4.
func contextReadAllow(links []ContextLink) string {
	src := contextSources(links, func(ContextLink) bool { return true })
	if len(src) == 0 {
		return ""
	}
	return "\n" +
		";; --- Context mounts: every delivered source, by its RESOLVED path.  The link in\n" +
		";;     the context dir is only a name: Seatbelt judges the target of every access,\n" +
		";;     so this allow is what opens it.  Before every read deny that must win. ---\n" +
		";; #seatbelt-test-id:context-read-allow#\n" +
		"(allow file-read*\n" + subpathClauses(src) + ")\n"
}

// profileBlock returns the SBPL form that follows `;; #seatbelt-test-id:<id>#` in a profile, up
// to the next blank or comment line, or "" when the id is absent. PlanInvariants reads a
// profile through it, never through the renderer that wrote it.
func profileBlock(profile, id string) string {
	marker := ";; #seatbelt-test-id:" + id + "#\n"
	i := strings.Index(profile, marker)
	if i < 0 {
		return ""
	}
	rest := profile[i+len(marker):]
	var b strings.Builder
	for _, line := range strings.SplitAfter(rest, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, ";;") {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// --- lexical path relations, over resolved slash paths --------------------------------------

// pathWithin reports whether p is base or below it, comparing case-insensitively when fold.
func pathWithin(p, base string, fold bool) bool {
	if base == "" || p == "" {
		return false
	}
	if fold {
		p, base = strings.ToLower(p), strings.ToLower(base)
	}
	if base == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == base || strings.HasPrefix(p, base+"/")
}

// overlap says how a relates to b — "is", "is inside" or "contains" — or "" when neither is
// within the other, comparing case-insensitively when fold.
func overlap(a, b string, fold bool) string {
	switch {
	case a == "" || b == "":
		return ""
	case a == b || (fold && strings.EqualFold(a, b)):
		return "is"
	case pathWithin(a, b, fold):
		return "is inside"
	case pathWithin(b, a, fold):
		return "contains"
	}
	return ""
}

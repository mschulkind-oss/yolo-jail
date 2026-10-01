package macosuser

import (
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ctxlinks.go is the macos-user DELIVERY of context mounts (docs/design/context-mounts.md §3,
// §4 steps 4 and 5). A config `mounts` element or a pack `mount` grant becomes a ROOT-OWNED
// SYMBOLIC LINK in the context dir (StagedCtxRoot, which $YOLO_CONTEXT_DIR names), at the path
// the container backends' /ctx would use, whose target is the resolved host source — and the
// Seatbelt profile decides every access to that target.
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

// privacyDir names the privacy-guarded directory src sits in, inside home, or "".
func (s ContextSiting) privacyDir(src, home string) string {
	for _, d := range s.PrivacyDirs {
		if p := home + "/" + d; pathWithin(src, p, s.FoldCase) {
			return p
		}
	}
	return ""
}

// ContextOccupied is every context-dir path yolo's own staging uses on this launch: the
// reserved children of the context dir (paths.ReservedContextPaths, the names `yolo check`
// refuses a `mounts` element at) and every destination the composed tree delivers a host
// file to (HostContext.Delivered). A link at, inside or around one would collide with a
// copied file, or be staged INTO a directory of the composed tree.
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
func ContextPreflightRefusal(failed []ContextProbe) string {
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
		"there is not.\nOr use a container runtime (`runtime: \"podman\"` or `\"container\"`)."
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

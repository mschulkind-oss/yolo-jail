package run

// ctxmounts.go is how a launch reads the config `mounts` key and a pack's `mount` grants:
// which ones this launch delivers, at which jail path, in which mode, and what it says
// about each (docs/design/context-mounts.md §2, §3.8).
//
// ONE READING, THREE CONSUMERS: the container argv (assembleRunCmd), the agent's briefing
// (prepare) and the read-write disclosure. The briefing listing a mount the argv skipped is
// backend-parity.md §6's defect — a jail told about a /ctx path that does not exist — so
// both call the same function rather than agreeing by construction.
//
// ⚠ READ-WRITE ELEMENTS COME FROM THE USER SCOPE ALONE (config.LoadRWMounts), never from
// the merged config this function is handed: the merge records no provenance, and the
// trust predicate of §2.2 is a statement about where an element was written. Read-only
// elements keep coming from the merged view, which is where they always came from.

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// configCtxMount is one config `mounts` element this launch delivers.
type configCtxMount struct {
	source string // resolved host path
	dest   string // jail path
	rw     bool
}

// configCtxMounts returns the config `mounts` elements a container launch delivers, in
// order — read-only elements first, as the merged config lists them, then the user scope's
// read-write ones — and hands `note` one line per element it skips (nil drops them: the
// briefing's caller, whose launch prints the same lines from the assembler).
//
// Two skips, both the rule the string form always had and neither a downgrade:
//
//   - a source that does not exist. yolo never creates a context source, and a missing
//     bind source would abort the container start;
//   - a READ-ONLY element on an Apple Container that would ignore `:ro`
//     (roBindsUnsupported). A read-write element is NOT gated by that floor: the floor
//     exists for `:ro`, and refusing a mount for a property it does not need is the wrong
//     error (§2.9). A read-only one is never handed out writable in its place.
func (o *Options) configCtxMounts(rt string, cfg *jsonx.OrderedMap, note func(string)) []configCtxMount {
	// A caller that drops the lines asks for no location either, which costs a second read of
	// the config files (missingMountSourceWarning).
	quiet := note == nil
	if quiet {
		note = func(string) {}
	}
	elements := make([]config.ContextMount, 0)
	for _, m := range config.ParseMounts(cfg) {
		if !m.RW {
			elements = append(elements, m)
		}
	}
	rw, err := config.LoadRWMounts(nil)
	if err != nil {
		// Unreachable on a launch, which loaded the same file strict before getting here;
		// said rather than swallowed, because a read-write mount the user wrote that silently
		// does not appear is the failure this whole feature refuses.
		note("[yellow]Warning: read-write mounts: " + err.Error() + " — none mounted[/yellow]")
	}
	elements = append(elements, rw...)

	roUnsafe := o.roBindsUnsupported(rt)
	var out []configCtxMount
	for _, m := range elements {
		source := resolveExpand(m.Host)
		dest := m.DestFor(source)
		if !fileExists(source) {
			if !quiet {
				note("[yellow]Warning: " + o.missingMountSourceWarning(m, source) + "[/yellow]")
			}
			continue
		}
		if !m.RW && roUnsafe != "" {
			note("[yellow]Skipping mount " + source + " → " + dest + ": " + roUnsafe + "[/yellow]")
			continue
		}
		out = append(out, configCtxMount{source: source, dest: dest, rw: m.RW})
	}
	return out
}

// missingMountSourceWarning is the launch's line for a `mounts` element whose source does not
// exist, and it names the next step (docs/reference/happy-path-principle.md): the element as
// written and the file, line and column that wrote it (config.MountElementWhere, read only on
// this path), then both fixes and the check that confirms either (CX-D24 in
// docs/design/context-mounts.md). It used to name the resolved path alone, on every launch.
func (o *Options) missingMountSourceWarning(m config.ContextMount, source string) string {
	where := "the `mounts` element `" + m.Spec + "`"
	if _, locs := config.MountElementWhere(o.Workspace, m); len(locs) > 0 {
		where += " at " + locs[0]
	}
	return "mount path does not exist, skipping: " + source + " (" + where + "). Create " + source +
		" or remove that element, then run `yolo check`."
}

// bindArg is the `-v` value for one delivered mount: `:ro` for every read-only one, no
// mode at all for a read-write one. Never `:z`/`:Z` — the run path uses label=disable, and
// a relabel would rewrite SELinux labels on the host tree (§2.9).
func (m configCtxMount) bindArg() string {
	if m.rw {
		return m.source + ":" + m.dest
	}
	return m.source + ":" + m.dest + ":ro"
}

// rwMountDisclosure is the launch line every read-write mount gets, EVERY launch
// (docs/design/context-mounts.md §2.4). It is a disclosure in OQ-RO3's sense: nothing
// suppresses it — not YOLO_NO_BANNER, which is the version line and nothing else — and it is
// teed to launch.log with everything else the launch prints.
//
// THE INJECTION SENTENCE is the point of it. A path-keyed writable directory lets the jail
// choose both the key and the bytes, which is exactly why hostcas admits only
// content-addressed stores; a user-declared read-write mount is path-keyed by definition, so
// it is admissible only because a trusted source named it — and the user is told, every
// launch, what that opens.
//
// rootful adds §2.5's ownership consequence (CX-D6: disclosed, not refused — the workspace
// bind already lands in-jail root's writes as host root on that host, and nothing refuses it).
func rwMountDisclosure(m configCtxMount, rootful bool) string {
	line := "[bold yellow]Read-write mount:[/bold yellow] " + m.source + " → " + m.dest +
		". The jail can WRITE there, and anything on this machine that later reads " +
		m.source + " reads what the jail wrote, including a symlink the jail planted."
	if rootful {
		line += " This podman is ROOTFUL, so a file the jail's root creates there is owned " +
			"by root on the host."
	}
	return line
}

// rootfulPodmanHost reports whether this launch's podman ANSWERED that it is rootful, on a
// real host. Positive facts only, hostLoopbackFacts.rootfulPodman's rule: a podman that never
// answered says nothing about ownership. A nested launch is excluded because it is forced
// onto `--userns host` and reports rootful for that reason alone; its writes land under the
// OUTER jail's mapping, not as host root.
func (o *Options) rootfulPodmanHost(rt string) bool {
	if rt != "podman" || o.IsMacOS || o.inContainer() || o.podmanFacts == nil || !o.podmanFacts.parsed { // parity: NotApplicable — a rootful podman's ownership sentence; no other backend has a host root to own the writes (CX-D6)
		return false
	}
	r := o.podmanFacts.info.Host.Security.Rootless
	return r != nil && !*r
}

// packCtxMount is one pack `mount` grant this launch delivers.
type packCtxMount struct {
	pack   string
	from   string // home-relative, as declared
	source string
	dest   string
	file   bool
}

// packCtxMounts returns the pack `mount` grants a container launch delivers, handing
// `note` one line per grant it skips: an absent source (the pack's content simply is not
// there, and a missing bind source would abort the container start) or an Apple Container
// that would ignore `:ro`. The sole decider for both hostMountArgs and the briefing.
func (o *Options) packCtxMounts(rt string, packs []*packload.Pack, note func(string)) []packCtxMount {
	if note == nil {
		note = func(string) {}
	}
	roUnsafe := o.roBindsUnsupported(rt)
	var out []packCtxMount
	for _, p := range packs {
		if p == nil {
			continue
		}
		granted, _ := p.HonoredMounts()
		for _, mt := range granted {
			src := filepath.Join(homeDir(), filepath.FromSlash(mt.From))
			dest := packload.MountCtxPath(mt)
			dir, file := isDir(src), isFile(src)
			switch {
			case !dir && !file:
				note("[yellow]Warning: pack " + p.Name + " mount source " +
					"does not exist, skipping: ~/" + mt.From + "[/yellow]")
				continue
			case roUnsafe != "":
				note("[yellow]Skipping pack " + p.Name + " mount ~/" +
					mt.From + " → " + dest + ": " + roUnsafe + "[/yellow]")
				continue
			}
			out = append(out, packCtxMount{pack: p.Name, from: mt.From, source: src, dest: dest, file: file})
		}
	}
	return out
}

// briefedCtxMounts is what the agent's briefing lists under "Additional Context Mounts":
// every config element and pack grant this launch binds, each with its mode and, for a
// grant, its pack (§3.8 — before this, an agent learned a pack mount's path only from the
// pack's own prose), at the path the agent opens under ctxDir, the context dir.
//
// macos-user lists what ITS decider delivers (macosCtxLinks: a link in the context dir per
// mount), and nothing at all when that decider refuses one, since such a launch refuses before
// any agent reads a briefing. One decider per backend for the briefing and the delivery, so the
// agent is never told about a path that is not there.
func (o *Options) briefedCtxMounts(rt, ctxDir string, cfg *jsonx.OrderedMap, packs []*packload.Pack) []jailcontent.ContextMount {
	if rt == "macos-user" { // parity: HonoredBy — macos-user delivers a context mount as a root-owned link plus Seatbelt rules (macosCtxLinks), and the briefing lists exactly what that decider delivers
		links, refused := o.macosCtxLinks(cfg, packs, nil)
		if len(refused) > 0 {
			return nil
		}
		var out []jailcontent.ContextMount
		for _, l := range links {
			out = append(out, jailcontent.ContextMount{Path: ctxDir + "/" + l.Rel(),
				Host: l.Source, ReadWrite: l.RW, Pack: l.Pack})
		}
		return out
	}
	var out []jailcontent.ContextMount
	for _, m := range o.configCtxMounts(rt, cfg, nil) {
		out = append(out, jailcontent.ContextMount{Path: m.dest, Host: m.source, ReadWrite: m.rw})
	}
	for _, m := range o.packCtxMounts(rt, packs, nil) {
		out = append(out, jailcontent.ContextMount{Path: m.dest, Host: m.source, Pack: m.pack})
	}
	return out
}
